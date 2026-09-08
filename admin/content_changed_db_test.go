package admin

// Deps.ContentChanged is how the CMS learns that stored content moved, so
// the generated Tailwind stylesheet can be rebuilt. Forgetting it in a
// handler breaks nothing visible: the site keeps working, and classes
// typed into content quietly stop being compiled, which surfaces much
// later as a style that "just doesn't apply".
//
// That is why the notification lives in finishContentAction rather than
// in each handler. This is the test that keeps it there.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

func TestContentActionsNotifyContentChanged(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		var notified atomic.Int64

		users := auth.NewStore(db)
		store := content.NewStore(db, "en")
		h := New(Deps{
			Sessions:       scs.New(),
			Users:          users,
			Content:        store,
			Renderer:       formTemplates(t),
			Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
			AdminPath:      "/admin",
			ContentChanged: func() { notified.Add(1) },
		})
		mux := http.NewServeMux()
		mux.Handle("/admin/", http.StripPrefix("/admin", h))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		seedPermUser(t, users, "boss@example.com", auth.RoleSuperadmin)
		client := newClient(t)
		logIn(t, srv, client, "boss@example.com", "password123")

		newPage := func(slug string, publish bool) int64 {
			t.Helper()
			p := &content.Page{Slug: slug, Title: slug, TemplateName: "page.gohtml"}
			id, err := store.Insert(ctx, p, "en")
			if err != nil {
				t.Fatal(err)
			}
			if publish {
				if err := store.Publish(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			return id
		}
		post := func(path string) {
			t.Helper()
			csrf := csrfFrom(t, srv, client, "/admin/pages")
			resp, err := client.PostForm(srv.URL+path, url.Values{"csrf_token": {csrf}})
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}

		for _, tc := range []struct {
			name    string
			action  string
			publish bool
		}{
			{"unpublish", "/unpublish", true},
			{"discard", "/discard", true},
			{"delete", "/delete", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				id := newPage("page-"+tc.name, tc.publish)
				before := notified.Load()
				post("/admin/pages/" + strconv.FormatInt(id, 10) + tc.action)
				if notified.Load() == before {
					t.Errorf("%s did not notify ContentChanged — the generated "+
						"stylesheet will not be rebuilt for it", tc.name)
				}
			})
		}
	})
}
