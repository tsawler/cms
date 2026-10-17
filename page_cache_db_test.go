package cms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// A page served to a signed-in user — a private page, an edit render —
// must never reach a shared cache, or a proxy keyed on the URL hands it
// to the next anonymous visitor. What every visitor sees alike stays
// cacheable.
func TestSignedInPagesAreNotCacheable(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		defer c.Close()
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		publish := func(slug string, vis content.Visibility) {
			p := &content.Page{Slug: slug, Title: slug, TemplateName: "templates/pages/standard.gohtml"}
			if _, err := c.content.Insert(ctx, p, "en"); err != nil {
				t.Fatalf("Insert(%s): %v", slug, err)
			}
			if err := c.content.Publish(ctx, p.ID); err != nil {
				t.Fatalf("Publish(%s): %v", slug, err)
			}
			if err := c.content.SetVisibility(ctx, p.ID, vis); err != nil {
				t.Fatalf("SetVisibility(%s): %v", slug, err)
			}
		}
		publish("open", content.VisibilityPublic)
		publish("staff-only", content.VisibilityPrivate)

		// One user who edits pages, one signed in with no permissions at
		// all — who still sees private pages, without an edit render.
		editor := sessionFor(t, c, insertUser(t, c, "editor@example.com", auth.RoleAdmin))
		viewer := sessionFor(t, c, insertUser(t, c, "viewer@example.com", auth.RoleEditor))

		h := c.Handler()
		get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			if cookie != nil {
				r.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			return rec
		}
		uncacheable := func(name string, rec *httptest.ResponseRecorder) {
			t.Helper()
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status = %d, want 200", name, rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Errorf("%s: Cache-Control = %q, want private, no-store", name, got)
			}
			// The session middleware's, which a CDN may ignore — hence
			// the Cache-Control — but which must still be there.
			if !slices.Contains(rec.Header().Values("Vary"), "Cookie") {
				t.Errorf("%s: Vary = %q, want it to include Cookie", name, rec.Header().Values("Vary"))
			}
		}

		uncacheable("private page, viewer", get("/staff-only", viewer))
		uncacheable("private page, editor", get("/staff-only", editor))
		uncacheable("edit render of a public page", get("/open", editor))
		uncacheable("public page, signed-in viewer", get("/open", viewer))

		if rec := get("/staff-only", nil); rec.Code != http.StatusNotFound {
			t.Errorf("private page, anonymous: status = %d, want 404", rec.Code)
		}
		anon := get("/open", nil)
		if anon.Code != http.StatusOK {
			t.Fatalf("public page, anonymous: status = %d, want 200", anon.Code)
		}
		if got := anon.Header().Get("Cache-Control"); got != "" {
			t.Errorf("public page, anonymous: Cache-Control = %q, want none set", got)
		}
	})
}
