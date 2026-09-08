package cms

// What New starts in the background, Close stops. Nothing needed it while
// a process built one CMS and then exited; a process that builds several
// — tests, and hosts that rebuild on reload — used to leave a goroutine
// per CMS sweeping a database it no longer served.

import (
	"context"
	"testing"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

func TestCloseStopsTheSessionSweep(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newSeedTestCMS(t, db)

		done := make(chan error, 1)
		go func() { done <- c.Close() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Close: %v", err)
			}
		case <-t.Context().Done():
			t.Fatal("Close did not return")
		}

		// Twice is not a panic. A shutdown path reachable from a signal
		// handler and a defer is reachable twice.
		if err := c.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
	})
}

// Config.DB belongs to the host: it opened the pool, may be using it for
// its own tables, and closing somebody else's pool is not Close's
// business.
func TestCloseLeavesTheHostsDatabaseOpen(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		var n int
		if err := db.QueryRow(ctx, "SELECT count(*) FROM cms_users").Scan(&n); err != nil {
			t.Fatalf("the host's database was closed along with the CMS: %v", err)
		}
	})
}
