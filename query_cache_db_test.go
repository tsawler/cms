package cms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sessiondata"
	"github.com/tsawler/cms/internal/sqldb"
)

// Menus come from a short-lived copy rather than a query per render, and
// clearCaches — what an admin write triggers — makes the next render read
// them afresh.
func TestMenusAreCachedUntilCleared(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		defer c.Close()
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		labels := func() []string {
			t.Helper()
			items, err := c.menuItems(ctx)
			if err != nil {
				t.Fatalf("menuItems: %v", err)
			}
			var out []string
			for _, it := range items {
				out = append(out, it.Label)
			}
			return out
		}
		set := func(label string) {
			t.Helper()
			if err := c.content.ReplaceMenu(ctx, "main", []content.MenuItemInput{{Label: label, URL: "/x"}}); err != nil {
				t.Fatalf("ReplaceMenu: %v", err)
			}
		}

		set("Before")
		if got := labels(); len(got) != 1 || got[0] != "Before" {
			t.Fatalf("menu = %v, want [Before]", got)
		}
		set("After") // written behind the cache's back, as another instance would
		if got := labels(); len(got) != 1 || got[0] != "Before" {
			t.Errorf("menu = %v inside the TTL, want the cached [Before]", got)
		}
		c.clearCaches()
		if got := labels(); len(got) != 1 || got[0] != "After" {
			t.Errorf("menu = %v after clearCaches, want [After]", got)
		}
	})
}

// An admin write clears the cached menus on its way out, so the editor
// who saved a menu sees it on the next render rather than when the cache
// expires.
func TestAMenuSavedThroughTheAdminShowsAtOnce(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		defer c.Close()
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if err := c.content.ReplaceMenu(ctx, "main", []content.MenuItemInput{{Label: "Old", URL: "/x"}}); err != nil {
			t.Fatal(err)
		}
		if items, _ := c.menuItems(ctx); len(items) != 1 || items[0].Label != "Old" {
			t.Fatalf("menu = %v, want [Old]", items)
		}

		// A signed-in admin's session, with the CSRF token the API wants.
		sctx, err := c.sessions.Load(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		c.sessions.Put(sctx, sessiondata.KeyUserID, insertUser(t, c, "admin@example.com", auth.RoleAdmin))
		c.sessions.Put(sctx, sessiondata.KeyCSRF, "tok")
		token, _, err := c.sessions.Commit(sctx)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPut, "/admin/api/menu",
			strings.NewReader(`{"menu":"main","items":[{"label":"New","url":"/y"}]}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", "tok")
		r.AddCookie(&http.Cookie{Name: c.sessions.Cookie.Name, Value: token})
		rec := httptest.NewRecorder()
		c.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT /admin/api/menu = %d: %s", rec.Code, rec.Body.String())
		}

		if items, _ := c.menuItems(ctx); len(items) != 1 || items[0].Label != "New" {
			t.Errorf("menu after the admin save = %v, want [New] at once", items)
		}
	})
}
