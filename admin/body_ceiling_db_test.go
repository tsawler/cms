package admin

// The request-body ceiling has to hold whichever way the CSRF token
// arrives.
//
// Handlers used to set their own, which worked for a request carrying the
// token in a header — the body was still untouched at that point — and was
// inert for a form post, where the middleware had already read the body
// to find the token in it. Same limit written down in the handler,
// enforced in one case out of two, and no way to tell from reading it.
//
// The ceiling now goes on in the middleware, before either path, so this
// checks both: a form post is refused outright, and a header post reaches
// the handler with a body it cannot read past the limit.

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

func TestBodyCeilingAppliesToBothCSRFPaths(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		const ceiling = 4 << 10

		// The probe reads its own body, so the test can see how far it
		// got rather than inferring it from a status code.
		var readErr error
		var readN int
		users := auth.NewStore(db)
		h := New(Deps{
			Sessions:        scs.New(),
			Users:           users,
			Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
			AdminPath:       "/admin",
			MaxRequestBytes: ceiling,
			Sections: []Section{{
				Path: "probe",
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var b []byte
					b, readErr = io.ReadAll(r.Body)
					readN = len(b)
				}),
			}},
		})
		mux := http.NewServeMux()
		mux.Handle("/admin/", http.StripPrefix("/admin", h))
		srv := httptest.NewServer(mux)
		defer srv.Close()

		seedPermUser(t, users, "pat@example.com", auth.RoleAdmin)
		client := newClient(t)
		logIn(t, srv, client, "pat@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/")

		oversized := strings.Repeat("x", ceiling*4)

		t.Run("token in the form body", func(t *testing.T) {
			readErr, readN = nil, 0
			resp, err := client.PostForm(srv.URL+"/admin/x/probe/", url.Values{
				"csrf_token": {csrf}, "payload": {oversized},
			})
			if err != nil {
				return // cut off mid-send is the ceiling working
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				t.Errorf("status = %d, want 413", resp.StatusCode)
			}
		})

		t.Run("token in the header", func(t *testing.T) {
			readErr, readN = nil, 0
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/x/probe/",
				strings.NewReader(oversized))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("X-CSRF-Token", csrf)
			resp, err := client.Do(req)
			if err != nil {
				return // as above
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if readErr == nil {
				t.Errorf("the handler read %d bytes of a %d-byte body with no error — "+
					"the ceiling does not apply when the token comes in a header",
					readN, len(oversized))
			}
			if readN > ceiling {
				t.Errorf("the handler read %d bytes, past the %d-byte ceiling", readN, ceiling)
			}
		})
	})
}
