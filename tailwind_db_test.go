package cms

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/internal/dbtest"
)

// newTestCMS returns a CMS wired to db with everything else defaulted — the
// minimum New accepts.
func newTestCMS(t *testing.T, db *sqldb.DB) *CMS {
	t.Helper()
	// The dialect has to come across with the pool: Config.Dialect defaults
	// to postgres, and a MySQL pool driven with Postgres SQL fails loudly.
	c, err := New(Config{
		DB:      db.SQL(),
		Dialect: db.Dialect().Name(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("cms.New: %v", err)
	}
	return c
}

func TestContentCSSSingleton(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newTestCMS(t, db)

		// A fresh install has no stored stylesheet, and that is not an error.
		hash, css, err := c.loadContentCSS(ctx)
		if err != nil {
			t.Fatalf("loadContentCSS(empty): %v", err)
		}
		if hash != "" || css != "" {
			t.Errorf("loadContentCSS(empty) = %q, %q, want empty", hash, css)
		}

		if err := c.storeContentCSS(ctx, "abc123", ".cms-x{color:red}"); err != nil {
			t.Fatalf("storeContentCSS: %v", err)
		}
		hash, css, err = c.loadContentCSS(ctx)
		if err != nil {
			t.Fatalf("loadContentCSS: %v", err)
		}
		if hash != "abc123" {
			t.Errorf("hash = %q, want %q", hash, "abc123")
		}
		if css != ".cms-x{color:red}" {
			t.Errorf("css = %q, want the stored stylesheet", css)
		}

		// Storing again replaces the single row rather than adding one — the
		// table is a singleton enforced by a one-value primary key, and this
		// is the upsert that keeps it that way.
		if err := c.storeContentCSS(ctx, "def456", ".cms-y{color:blue}"); err != nil {
			t.Fatalf("storeContentCSS(second): %v", err)
		}
		hash, css, err = c.loadContentCSS(ctx)
		if err != nil {
			t.Fatalf("loadContentCSS: %v", err)
		}
		if hash != "def456" || css != ".cms-y{color:blue}" {
			t.Errorf("after re-store = %q, %q, want the replaced values", hash, css)
		}
	})
}
