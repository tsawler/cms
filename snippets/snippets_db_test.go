package snippets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/snippets"
)

func TestSnippetInsertAndGet(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewStore(db)

		sn := &snippets.Snippet{
			Name:     "Hero",
			HTML:     "<div>hero</div>",
			Settings: map[string]string{"bg": "paper", "width": "wide"},
		}
		id, err := s.Insert(ctx, sn)
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if id == 0 || sn.ID == 0 {
			t.Fatal("Insert did not set the snippet id")
		}

		got, err := s.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Name != "Hero" {
			t.Errorf("name = %q, want %q", got.Name, "Hero")
		}
		if got.HTML != "<div>hero</div>" {
			t.Errorf("html = %q, want %q", got.HTML, "<div>hero</div>")
		}
		// The JSON settings column has to survive the round trip.
		if got.Settings["bg"] != "paper" || got.Settings["width"] != "wide" {
			t.Errorf("settings = %v, want bg=paper width=wide", got.Settings)
		}
		if got.CreatedAt.IsZero() {
			t.Error("created_at was not defaulted by the database")
		}
	})
}

func TestSnippetNilSettingsStoresNull(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewStore(db)

		// Insert documents that a nil Settings map means a plain block
		// rather than a section preset, and is stored as NULL.
		id, err := s.Insert(ctx, &snippets.Snippet{Name: "Plain", HTML: "<p>plain</p>"})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		got, err := s.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Settings != nil {
			t.Errorf("settings = %v, want nil for a plain block", got.Settings)
		}
	})
}

func TestSnippetNotFound(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewStore(db)

		if _, err := s.GetByID(ctx, 4242); !errors.Is(err, snippets.ErrNotFound) {
			t.Errorf("GetByID(missing) = %v, want ErrNotFound", err)
		}
		if err := s.Update(ctx, &snippets.Snippet{ID: 4242, Name: "x"}); !errors.Is(err, snippets.ErrNotFound) {
			t.Errorf("Update(missing) = %v, want ErrNotFound", err)
		}
		if err := s.Delete(ctx, 4242); !errors.Is(err, snippets.ErrNotFound) {
			t.Errorf("Delete(missing) = %v, want ErrNotFound", err)
		}
	})
}

func TestSnippetUpdate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewStore(db)
		sn := &snippets.Snippet{Name: "Before", HTML: "<p>before</p>",
			Settings: map[string]string{"bg": "paper"}}
		if _, err := s.Insert(ctx, sn); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		sn.Name = "After"
		sn.HTML = "<p>after</p>"
		sn.Settings = map[string]string{"bg": "spruce", "corners": "round"}
		if err := s.Update(ctx, sn); err != nil {
			t.Fatalf("Update: %v", err)
		}

		got, err := s.GetByID(ctx, sn.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Name != "After" || got.HTML != "<p>after</p>" {
			t.Errorf("snippet = %+v, want the updated name and html", got)
		}
		if got.Settings["bg"] != "spruce" || got.Settings["corners"] != "round" {
			t.Errorf("settings = %v, want the replaced map", got.Settings)
		}
	})
}

func TestSnippetAllAndCount(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewStore(db)

		if n, err := s.Count(ctx); err != nil || n != 0 {
			t.Fatalf("Count(empty) = %d, %v, want 0, nil", n, err)
		}

		for _, name := range []string{"Gamma", "Alpha", "Beta"} {
			if _, err := s.Insert(ctx, &snippets.Snippet{Name: name, HTML: "<p>" + name + "</p>"}); err != nil {
				t.Fatalf("Insert(%s): %v", name, err)
			}
		}

		n, err := s.Count(ctx)
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n != 3 {
			t.Errorf("Count = %d, want 3", n)
		}

		all, err := s.All(ctx)
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		want := []string{"Alpha", "Beta", "Gamma"}
		if len(all) != len(want) {
			t.Fatalf("All returned %d snippets, want %d", len(all), len(want))
		}
		for i := range want {
			if all[i].Name != want[i] {
				t.Errorf("All order = %v at %d, want %v", all[i].Name, i, want)
			}
		}
	})
}

func TestSnippetDelete(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := snippets.NewStore(db)
		sn := &snippets.Snippet{Name: "Doomed", HTML: "<p>bye</p>"}
		if _, err := s.Insert(ctx, sn); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		if err := s.Delete(ctx, sn.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := s.GetByID(ctx, sn.ID); !errors.Is(err, snippets.ErrNotFound) {
			t.Errorf("GetByID after Delete = %v, want ErrNotFound", err)
		}
	})
}
