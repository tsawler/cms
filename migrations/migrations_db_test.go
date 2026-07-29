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
// dropPostMediaColumns removes the two columns 0023 adds, so the migration
// can be applied again for real. MySQL and MariaDB refuse to drop a column
// a foreign key still uses, and their constraint names are generated, so
// the names are read back from information_schema rather than assumed;
// Postgres drops the constraint with the column and needs none of this.
func dropPostMediaColumns(t *testing.T, db *sqldb.DB) {
	t.Helper()
	ctx := context.Background()

	if db.Dialect().Name() != "postgres" {
		rows, err := db.Query(ctx, `
			SELECT DISTINCT constraint_name
			FROM information_schema.key_column_usage
			WHERE table_schema = DATABASE() AND table_name = 'cms_posts'
			  AND column_name IN ('thumbnail_media_id', 'header_media_id')
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

	for _, col := range []string{"thumbnail_media_id", "header_media_id"} {
		if _, err := db.Exec(ctx, "ALTER TABLE cms_posts DROP COLUMN "+col); err != nil {
			t.Fatalf("dropping %s: %v", col, err)
		}
	}
}

func TestPostMediaBackfill(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

		dropPostMediaColumns(t, db)
		if _, err := db.Exec(ctx, "DELETE FROM cms_schema_migrations WHERE version = 23"); err != nil {
			t.Fatalf("forgetting migration 23: %v", err)
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
			t.Fatalf("re-applying 0023: %v", err)
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
		var headerID sql.NullInt64
		if err := db.QueryRow(ctx,
			"SELECT header_media_id FROM cms_posts WHERE id = $1", library).Scan(&headerID); err != nil {
			t.Fatalf("reading header id: %v", err)
		}
		if !headerID.Valid || headerID.Int64 != mediaID {
			t.Errorf("header_media_id = %v, want %d", headerID, mediaID)
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
