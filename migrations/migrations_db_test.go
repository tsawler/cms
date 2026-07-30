package migrations_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/migrations"
)

// TestMigrationsAreIdempotent re-runs the migrations against a database that
// dbtest has already migrated. Run is documented as safe to call on every
// startup, so the second pass must apply nothing and report no error.
func TestMigrationsAreIdempotent(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

		for i := range 2 {
			if err := migrations.Run(ctx, db, quiet); err != nil {
				t.Fatalf("re-run %d: %v", i+1, err)
			}
		}

		// Every embedded migration should be recorded exactly once.
		var versions, distinct int
		if err := db.QueryRow(ctx, `
			SELECT count(*), count(DISTINCT version) FROM cms_schema_migrations`).
			Scan(&versions, &distinct); err != nil {
			t.Fatalf("counting applied migrations: %v", err)
		}
		if versions != distinct {
			t.Errorf("cms_schema_migrations holds %d rows across %d versions, want one row each",
				versions, distinct)
		}
		if versions == 0 {
			t.Error("no migrations were recorded")
		}
	})
}

// TestPostMediaBackfill covers the data migration in 0023, which the
// ordinary suite cannot: dbtest hands out a database that is already at
// head, so the backfill runs over no rows and proves nothing.
//
// Instead this rewinds 0023 — drop the columns, forget the ledger row —
// seeds the schema that preceded it, and lets Run apply the real embedded
// file. Posts pointing at a library image must come out holding its id;
// posts pointing anywhere else must keep the address they had.
// rewindPostMedia puts cms_posts back the shape 0023 found it in — the two
// URL columns, no media ids — so the migration can be applied again for
// real. header_url has to be put back as well as taken away: 0024 dropped
// the header pair for good, and 0023's backfill reads that column, so
// replaying it needs the column there. Replaying 0024 on top takes it away
// again, which is where the test finishes.
//
// MySQL and MariaDB refuse to drop a column a foreign key still uses, and
// their constraint names are generated, so the names are read back from
// information_schema rather than assumed; Postgres drops the constraint
// with the column and needs none of this.
func rewindPostMedia(t *testing.T, db *sqldb.DB) {
	t.Helper()
	ctx := context.Background()

	// TEXT columns take a DEFAULT only in parentheses on MySQL/MariaDB.
	headerURL := "ALTER TABLE cms_posts ADD COLUMN header_url TEXT NOT NULL DEFAULT ''"
	if db.Dialect().Name() != "postgres" {
		headerURL = "ALTER TABLE cms_posts ADD COLUMN header_url TEXT NOT NULL DEFAULT ('')"

		rows, err := db.Query(ctx, `
			SELECT DISTINCT constraint_name
			FROM information_schema.key_column_usage
			WHERE table_schema = DATABASE() AND table_name = 'cms_posts'
			  AND column_name = 'thumbnail_media_id'
			  AND referenced_table_name = 'cms_media'`)
		if err != nil {
			t.Fatalf("reading foreign keys: %v", err)
		}
		names, err := sqldb.CollectRows(rows, func(row sqldb.Scanner) (string, error) {
			var name string
			err := row.Scan(&name)
			return name, err
		})
		if err != nil {
			t.Fatalf("reading foreign keys: %v", err)
		}
		for _, name := range names {
			if _, err := db.Exec(ctx, "ALTER TABLE cms_posts DROP FOREIGN KEY "+name); err != nil {
				t.Fatalf("dropping foreign key %s: %v", name, err)
			}
		}
	}

	if _, err := db.Exec(ctx, "ALTER TABLE cms_posts DROP COLUMN thumbnail_media_id"); err != nil {
		t.Fatalf("dropping thumbnail_media_id: %v", err)
	}
	if _, err := db.Exec(ctx, headerURL); err != nil {
		t.Fatalf("restoring header_url: %v", err)
	}
}

// hasColumn reports whether cms_posts still has the named column.
func hasColumn(t *testing.T, db *sqldb.DB, column string) bool {
	t.Helper()
	scope := "table_schema = DATABASE()"
	if db.Dialect().Name() == "postgres" {
		scope = "table_schema = current_schema()"
	}
	var n int
	if err := db.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM information_schema.columns
		WHERE `+scope+` AND table_name = 'cms_posts' AND column_name = $1`, column).Scan(&n); err != nil {
		t.Fatalf("looking up column %s: %v", column, err)
	}
	return n > 0
}

func TestPostMediaBackfill(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

		rewindPostMedia(t, db)
		if _, err := db.Exec(ctx, "DELETE FROM cms_schema_migrations WHERE version IN (23, 24)"); err != nil {
			t.Fatalf("forgetting migrations 23 and 24: %v", err)
		}

		// One library image, and the URLs a pre-0023 picker would have
		// stored for it.
		const item = "0123456789abcdef01234567"
		mediaID, err := db.InsertID(ctx, `
			INSERT INTO cms_media (kind, store_key, filename, mime, ext, variant_ext, width, height, size, created_at)
			VALUES ('image', $1, 'photo.jpg', 'image/jpeg', '.jpg', '.webp', 2000, 1000, 4096, now())`, item)
		if err != nil {
			t.Fatalf("seeding media: %v", err)
		}

		seed := func(slug, thumb, header string) int64 {
			t.Helper()
			pageID, err := db.InsertID(ctx, `
				INSERT INTO cms_pages (slug, template_name, status) VALUES ($1, 'post.gohtml', 'draft')`, slug)
			if err != nil {
				t.Fatalf("seeding page %q: %v", slug, err)
			}
			postID, err := db.InsertID(ctx, `
				INSERT INTO cms_posts (page_id, feed, thumbnail_url, header_url)
				VALUES ($1, 'blog', $2, $3)`, pageID, thumb, header)
			if err != nil {
				t.Fatalf("seeding post %q: %v", slug, err)
			}
			return postID
		}

		library := seed("blog/from-library",
			"/cms/media/"+item+"/web.webp", "/cms/media/"+item+"/web.webp")
		external := seed("blog/from-elsewhere",
			"https://cdn.example.com/pic.png", "/static/banner.png")
		bare := seed("blog/no-images", "", "")

		if err := migrations.Run(ctx, db, quiet); err != nil {
			t.Fatalf("re-applying 0023 and 0024: %v", err)
		}

		read := func(id int64) (mediaID sql.NullInt64, url string) {
			t.Helper()
			if err := db.QueryRow(ctx,
				"SELECT thumbnail_media_id, thumbnail_url FROM cms_posts WHERE id = $1", id).
				Scan(&mediaID, &url); err != nil {
				t.Fatalf("reading post %d: %v", id, err)
			}
			return
		}

		// The URL named the item, so the post now references the row and
		// the address is cleared: one source of truth per image.
		if got, url := read(library); !got.Valid || got.Int64 != mediaID || url != "" {
			t.Errorf("library-backed post = id %v / url %q, want id %d / empty",
				got, url, mediaID)
		}
		// 0024 ran on top and took the header pair away again — the banner
		// at the top of a post is a section now, not a column.
		for _, col := range []string{"header_media_id", "header_url"} {
			if hasColumn(t, db, col) {
				t.Errorf("cms_posts still has %s after 0024", col)
			}
		}

		// An address the library does not hold is exactly what the URL
		// column now means, so it stays put.
		if got, url := read(external); got.Valid || url != "https://cdn.example.com/pic.png" {
			t.Errorf("external post = id %v / url %q, want no id and the original address", got, url)
		}
		if got, url := read(bare); got.Valid || url != "" {
			t.Errorf("imageless post = id %v / url %q, want both empty", got, url)
		}
	})
}
