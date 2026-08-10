package auth_test

// Permission grants against a real database: ReplacePermissions
// round-trips, the user lookups carry grants back, and login still works.

import (
	"context"
	"slices"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

func TestReplacePermissions(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		store := auth.NewStore(db)
		u := seedUser(t, store, auth.User{Email: "editor@example.com", Active: true})

		// A fresh user has no grants.
		got, err := store.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Permissions) != 0 {
			t.Fatalf("new user grants = %v, want none", got.Permissions)
		}

		// Duplicates in the input collapse to one row.
		perms := []auth.Permission{auth.PermBlogs, auth.PermPages, "vehicles", auth.PermBlogs}
		if err := store.ReplacePermissions(ctx, u.ID, perms); err != nil {
			t.Fatal(err)
		}
		got, err = store.GetByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := []auth.Permission{auth.PermBlogs, auth.PermPages, "vehicles"}
		for _, p := range want {
			if !slices.Contains(got.Permissions, p) {
				t.Errorf("grants %v missing %q", got.Permissions, p)
			}
		}
		if len(got.Permissions) != len(want) {
			t.Errorf("grants = %v, want exactly %v", got.Permissions, want)
		}

		// GetByEmail loads the same grants; Authenticate still works and
		// hands back a user whose Can answers match.
		byEmail, err := store.GetByEmail(ctx, "editor@example.com")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(byEmail.Permissions, got.Permissions) {
			t.Errorf("GetByEmail grants = %v, GetByID grants = %v", byEmail.Permissions, got.Permissions)
		}
		authed, err := store.Authenticate(ctx, "editor@example.com", "password123")
		if err != nil {
			t.Fatal(err)
		}
		if !authed.Can(auth.PermBlogs) || authed.Can(auth.PermNews) {
			t.Errorf("authenticated grants = %v, want blogs but not news", authed.Permissions)
		}

		// Replace narrows to the new set; replace-with-nothing clears.
		if err := store.ReplacePermissions(ctx, u.ID, []auth.Permission{auth.PermNews}); err != nil {
			t.Fatal(err)
		}
		got, _ = store.GetByID(ctx, u.ID)
		if !slices.Equal(got.Permissions, []auth.Permission{auth.PermNews}) {
			t.Errorf("after replace, grants = %v, want [news]", got.Permissions)
		}
		if err := store.ReplacePermissions(ctx, u.ID, nil); err != nil {
			t.Fatal(err)
		}
		got, _ = store.GetByID(ctx, u.ID)
		if len(got.Permissions) != 0 {
			t.Errorf("after clearing, grants = %v, want none", got.Permissions)
		}
	})
}
