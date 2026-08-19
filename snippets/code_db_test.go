package snippets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/snippets"
)

func TestCodeSnippetRoundTrip(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewCodeStore(db)

		c := &snippets.CodeSnippet{
			Key:  "booking-widget",
			Name: "Booking widget",
			HTML: `<div id="book"></div><script>book()</script>`,
		}
		if _, err := s.Insert(ctx, c); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if c.ID == 0 {
			t.Fatal("Insert did not set the id")
		}

		got, err := s.ByKey(ctx, "booking-widget")
		if err != nil {
			t.Fatalf("ByKey: %v", err)
		}
		if got.Name != c.Name || got.HTML != c.HTML {
			t.Errorf("read back %+v, want name %q and the stored markup", got, c.Name)
		}

		// The key is the identity pages point at, so an update changes
		// everything else and finds the row by it.
		c.Name = "Bookings"
		c.HTML = "<p>new</p>"
		if err := s.Update(ctx, c); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, err = s.ByKey(ctx, "booking-widget")
		if err != nil {
			t.Fatalf("ByKey after update: %v", err)
		}
		if got.Name != "Bookings" || got.HTML != "<p>new</p>" {
			t.Errorf("update did not take: %+v", got)
		}

		if err := s.Delete(ctx, "booking-widget"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := s.ByKey(ctx, "booking-widget"); !errors.Is(err, snippets.ErrNotFound) {
			t.Errorf("ByKey after delete: %v, want ErrNotFound", err)
		}
	})
}

// TestCodeSnippetKeyIsUnique covers the collision an admin can walk into
// by naming two blocks the same thing: the second insert is refused with
// a distinguishable error, not a duplicate row.
func TestCodeSnippetKeyIsUnique(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewCodeStore(db)

		if _, err := s.Insert(ctx, &snippets.CodeSnippet{Key: "dup", Name: "One"}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		_, err := s.Insert(ctx, &snippets.CodeSnippet{Key: "dup", Name: "Two"})
		if !errors.Is(err, snippets.ErrDuplicateCodeKey) {
			t.Errorf("second Insert: %v, want ErrDuplicateCodeKey", err)
		}
	})
}

// TestCodeSnippetMissingRows covers the two operations a page can invoke
// against a key that is no longer there.
func TestCodeSnippetMissingRows(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewCodeStore(db)

		if err := s.Update(ctx, &snippets.CodeSnippet{Key: "nope", Name: "x"}); !errors.Is(err, snippets.ErrNotFound) {
			t.Errorf("Update of a missing key: %v, want ErrNotFound", err)
		}
		if err := s.Delete(ctx, "nope"); !errors.Is(err, snippets.ErrNotFound) {
			t.Errorf("Delete of a missing key: %v, want ErrNotFound", err)
		}
	})
}

// TestCodeSnippetLookup covers what the renderer actually calls: one
// query per distinct key, misses cached as misses, and no error ever
// reaching the page.
func TestCodeSnippetLookup(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewCodeStore(db)
		if _, err := s.Insert(ctx, &snippets.CodeSnippet{Key: "a", Name: "A", HTML: "<i>A</i>"}); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		lookup := s.Lookup(ctx, nil)
		for range 2 {
			if html, ok := lookup("a"); !ok || html != "<i>A</i>" {
				t.Errorf(`lookup("a") = %q, %v; want "<i>A</i>", true`, html, ok)
			}
			if html, ok := lookup("missing"); ok || html != "" {
				t.Errorf(`lookup("missing") = %q, %v; want "", false`, html, ok)
			}
		}

		// All() is the editor's chooser list.
		all, err := s.All(ctx)
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		if len(all) != 1 || all[0].Key != "a" {
			t.Errorf("All() = %+v, want the one entry", all)
		}
	})
}
