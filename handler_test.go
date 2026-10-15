package cms

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
)

// Handler must route AdminPath to the admin handler with the prefix
// stripped, canonicalize the bare admin path, and send everything else —
// including paths that merely start with the same characters — to Pages.
func TestHandlerRouting(t *testing.T) {
	adminSaw := ""
	c := &CMS{
		cfg:      Config{AdminPath: "/admin"},
		sessions: scs.New(),
		admin: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			adminSaw = r.URL.Path
			w.WriteHeader(http.StatusTeapot) // distinguishable from Pages
		}),
	}
	h := c.Handler()

	for reqPath, want := range map[string]string{
		"/admin/":      "/",
		"/admin/users": "/users",
	} {
		adminSaw = ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))
		if rec.Code != http.StatusTeapot {
			t.Errorf("request %s: status %d, want admin handler", reqPath, rec.Code)
		}
		if adminSaw != want {
			t.Errorf("request %s: admin handler saw %q, want %q", reqPath, adminSaw, want)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin?tab=2", nil))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("bare admin path: status %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/?tab=2" {
		t.Errorf("bare admin path redirected to %q, want /admin/?tab=2", loc)
	}

	// Not the admin prefix: served by Pages (placeholder, no renderer).
	for _, reqPath := range []string{"/", "/about", "/administrator"} {
		adminSaw = ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))
		if adminSaw != "" {
			t.Errorf("request %s: reached the admin handler (saw %q)", reqPath, adminSaw)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("request %s: status %d, want 200 from Pages", reqPath, rec.Code)
		}
	}
}

// A moved admin is routed exactly like the default one, and the old
// /admin prefix becomes an ordinary public address — the host may have
// its own thing there, which is why it moved.
func TestHandlerRoutingCustomAdminPath(t *testing.T) {
	for _, adminPath := range []string{"/cms-admin", "/site/manage"} {
		adminSaw := ""
		c := &CMS{
			cfg:      Config{AdminPath: adminPath},
			sessions: scs.New(),
			admin: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				adminSaw = r.URL.Path
				w.WriteHeader(http.StatusTeapot)
			}),
		}
		h := c.Handler()

		for reqPath, want := range map[string]string{
			adminPath + "/":               "/",
			adminPath + "/users":          "/users",
			adminPath + "/x/reports/ping": "/x/reports/ping",
		} {
			adminSaw = ""
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))
			if rec.Code != http.StatusTeapot || adminSaw != want {
				t.Errorf("%s: request %s: status %d, admin saw %q; want admin handler seeing %q",
					adminPath, reqPath, rec.Code, adminSaw, want)
			}
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, adminPath, nil))
		if loc := rec.Header().Get("Location"); rec.Code != http.StatusMovedPermanently || loc != adminPath+"/" {
			t.Errorf("%s: bare admin path: status %d to %q, want 301 to %s/", adminPath, rec.Code, loc, adminPath)
		}

		for _, reqPath := range []string{"/admin", "/admin/", "/admin/users", adminPath + "-old"} {
			adminSaw = ""
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, reqPath, nil))
			if adminSaw != "" || rec.Code == http.StatusMovedPermanently {
				t.Errorf("%s: request %s reached the admin (status %d, saw %q)", adminPath, reqPath, rec.Code, adminSaw)
			}
		}
	}
}
