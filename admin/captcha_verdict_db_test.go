package admin

// Failing open when the CAPTCHA returns no verdict is a decision, not an
// accident, so it is pinned here: a CAPTCHA server that cannot answer must
// not hold an admin out of their own site. The login throttle — with its
// per-account counters — is what still stands in the way of guessing.
//
// It matters most for the answers that are not outages. A wrong site key,
// or a URL pointing at some other service, produces a well-formed reply
// with no verdict in it. Decoded into a plain bool that used to read as
// Success=false — a silent rejection — so the same misconfiguration
// locked people out or waved them through depending on whether the wrong
// thing on the other end happened to speak JSON.
//
// Against a real database, because the point is that the request gets all
// the way to the credential check. The honeypot would short-circuit
// earlier than the CAPTCHA gate and prove nothing.

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
	"github.com/tsawler/cms/captcha"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// captchaTestServer is a full admin server whose CAPTCHA is the given
// handler, so a test can decide what siteverify answers.
func captchaTestServer(t *testing.T, db *sqldb.DB, verify http.HandlerFunc) (*httptest.Server, *auth.Store) {
	t.Helper()
	capSrv := httptest.NewServer(verify)
	t.Cleanup(capSrv.Close)

	cap, err := captcha.New(captcha.Config{URL: capSrv.URL, SiteKey: "site1", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	users := auth.NewStore(db)
	h := New(Deps{
		Sessions:  scs.New(),
		Users:     users,
		Captcha:   cap,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath: "/admin",
	})
	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, users
}

func TestLoginWhenTheCaptchaGivesNoVerdict(t *testing.T) {
	answers := []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, "boom"},
		{"html error page", http.StatusOK, "<html>not cap</html>"},
		{"wrong site key", http.StatusOK, `{"error":"unknown site key"}`},
		{"empty body", http.StatusOK, ""},
	}
	for _, tc := range answers {
		t.Run(tc.name, func(t *testing.T) {
			dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
				srv, users := captchaTestServer(t, db, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.body))
				})
				seedActiveUser(t, users, "pat@example.com", "password-123")

				client := newClient(t)
				csrf := csrfFrom(t, srv, client, "/admin/login")
				resp, err := client.PostForm(srv.URL+"/admin/login", url.Values{
					"csrf_token": {csrf},
					"email":      {"pat@example.com"},
					"password":   {"password-123"},
					"cap-token":  {"solved"},
				})
				if err != nil {
					t.Fatal(err)
				}
				page, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				if strings.Contains(string(page), "Verification failed") {
					t.Fatalf("no verdict was treated as a rejection, holding a valid user out:\n%s", page)
				}
				if !strings.Contains(string(page), "Dashboard") {
					t.Fatalf("a correct password did not log in:\n%s", page)
				}
			})
		})
	}
}

// And a real rejection is still a rejection — the fail-open path must not
// have swallowed the check itself.
func TestLoginStillRefusesARejectedCaptcha(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := captchaTestServer(t, db, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"success":false,"error":"Token not found"}`))
		})
		seedActiveUser(t, users, "pat@example.com", "password-123")

		client := newClient(t)
		csrf := csrfFrom(t, srv, client, "/admin/login")
		resp, err := client.PostForm(srv.URL+"/admin/login", url.Values{
			"csrf_token": {csrf},
			"email":      {"pat@example.com"},
			"password":   {"password-123"},
			"cap-token":  {"forged"},
		})
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if !strings.Contains(string(page), "Verification failed") {
			t.Errorf("a rejected token logged in anyway:\n%s", page)
		}
	})
}
