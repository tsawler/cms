package cms

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// PrivateMenuItemsForSignedIn: a private page's menu item shows to every
// signed-in user, readers included, when the site asks for it — and never
// to an anonymous visitor either way.
func TestPrivateMenuItemsForSignedIn(t *testing.T) {
	menuFS := fstest.MapFS{
		"templates/base.gohtml": &fstest.MapFile{Data: []byte(
			`{{define "base"}}<html><body><nav>{{range cmsMenu "main"}}[{{.Label}}]{{end}}</nav>{{block "content" .}}{{end}}</body></html>{{end}}`)},
		"templates/pages/standard.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}<main>{{cmsRegion "main"}}</main>{{end}}`)},
	}
	for _, on := range []bool{false, true} {
		name := "off"
		if on {
			name = "on"
		}
		t.Run(name, func(t *testing.T) {
			dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
				ctx := context.Background()
				c, err := New(Config{
					DB: db.SQL(), Dialect: db.Dialect().Name(),
					Logger:                      slog.New(slog.NewTextHandler(io.Discard, nil)),
					TemplateFS:                  menuFS,
					SharedTemplates:             []string{"templates/base.gohtml"},
					PageTemplates:               []PageTemplate{{File: "templates/pages/standard.gohtml", Label: "Standard"}},
					PrivateMenuItemsForSignedIn: on,
				})
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if err := c.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
				ids := map[string]int64{}
				for _, p := range []struct {
					slug string
					vis  content.Visibility
				}{{"open", content.VisibilityPublic}, {"staff", content.VisibilityPrivate}} {
					pg := &content.Page{Slug: p.slug, Title: p.slug, TemplateName: "templates/pages/standard.gohtml"}
					if _, err := c.content.Insert(ctx, pg, "en"); err != nil {
						t.Fatal(err)
					}
					if err := c.content.Publish(ctx, pg.ID); err != nil {
						t.Fatal(err)
					}
					if err := c.content.SetVisibility(ctx, pg.ID, p.vis); err != nil {
						t.Fatal(err)
					}
					ids[p.slug] = pg.ID
				}
				open, staff := ids["open"], ids["staff"]
				if err := c.content.ReplaceMenu(ctx, "main", []content.MenuItemInput{
					{Label: "Open", PageID: &open},
					{Label: "Staff", PageID: &staff},
				}); err != nil {
					t.Fatal(err)
				}
				reader := sessionFor(t, c, insertUser(t, c, "reader@example.com", auth.RoleReader))

				h := c.Handler()
				menu := func(path string, cookie *http.Cookie) string {
					t.Helper()
					r := httptest.NewRequest(http.MethodGet, path, nil)
					if cookie != nil {
						r.AddCookie(cookie)
					}
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, r)
					if rec.Code != http.StatusOK {
						t.Fatalf("GET %s: %d", path, rec.Code)
					}
					body := rec.Body.String()
					return body[strings.Index(body, "<nav>"):strings.Index(body, "</nav>")]
				}

				if got := menu("/open", nil); strings.Contains(got, "[Staff]") {
					t.Errorf("anonymous visitor sees the private item: %s", got)
				}
				got := menu("/open", reader)
				if on != strings.Contains(got, "[Staff]") {
					t.Errorf("option %v: reader's menu is %s", on, got)
				}
				// The private page itself is a reader's to read either way,
				// without an editor.
				r := httptest.NewRequest(http.MethodGet, "/staff", nil)
				r.AddCookie(reader)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, r)
				if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "csrf") {
					t.Errorf("reader on the private page: %d, editor present: %v", rec.Code, strings.Contains(rec.Body.String(), "csrf"))
				}
			})
		})
	}
}
