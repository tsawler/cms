package migrations_test

import (
	"context"
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
