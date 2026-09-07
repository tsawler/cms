package admin

// The package's public surface for host-registered admin sections:
// UserFrom, CSRFToken, SetFlash and RenderPage. A host mounts its own
// http.Handler into the admin and reaches back through these to find out
// who is signed in, to post a form back safely, to leave a confirmation,
// and to draw a page inside the admin's chrome.
//
// All four work by finding the admin server in the request context, which
// only sectionHandler puts there — so each is driven through a real
// mounted section rather than called on a bare request, and each is also
// checked outside one, where the contract is a documented fallback rather
// than a panic.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// mountSection wires h into the admin the way a host's section is wired,
// and returns a function that drives one request through it as the given
// user. The request that comes back is the one the handler saw, so a test
// can read the session it left behind.
func mountSection(t *testing.T, s *server, u *auth.User, sec Section) func(method, path string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	mounted := s.sectionHandler(sec)

	return func(method, path string) (*httptest.ResponseRecorder, *http.Request) {
		t.Helper()
		r := withRouteAndSession(t, s, u, httptest.NewRequest(method, path, nil), nil)
		rec := httptest.NewRecorder()
		mounted.ServeHTTP(rec, r)
		return rec, r
	}
}

// A section handler is behind the admin's login, so the user is there to
// be found — that is what lets a host scope its own screens to whoever is
// looking at them.
func TestUserFromInsideASection(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formUser(t, s, "section-user@example.com", auth.RoleEditor, auth.PermPages)

		var seen *auth.User
		drive := mountSection(t, s, u, Section{
			Path: "reports",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = UserFrom(r)
			}),
		})
		drive(http.MethodGet, "/x/reports/")

		if seen == nil {
			t.Fatal("UserFrom returned nil inside a section handler")
		}
		if seen.Email != "section-user@example.com" {
			t.Errorf("user = %q, want the signed-in one", seen.Email)
		}
		// The permissions come with it, so a host can ask what this
		// person is allowed to do rather than only who they are.
		if !seen.Can(auth.PermPages) {
			t.Error("the user arrived without their grants")
		}
	})
}

// The token a section's forms have to send back: the admin middleware
// rejects an unsafe request without it, so a section that could not read
// it could not have a form at all.
func TestCSRFTokenInsideASection(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		var seen string
		sec := Section{
			Path: "reports",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = CSRFToken(r)
			}),
		}
		mounted := s.sectionHandler(sec)

		r := withRouteAndSession(t, s, u, httptest.NewRequest(http.MethodGet, "/x/reports/", nil), nil)
		// The middleware mints the token into the session; the helper's
		// job is handing back whatever is there.
		s.deps.Sessions.Put(r.Context(), sessionKeyCSRF, "token-from-the-session")
		mounted.ServeHTTP(httptest.NewRecorder(), r)

		if seen != "token-from-the-session" {
			t.Errorf("CSRFToken = %q, want the session's token", seen)
		}
	})
}

// SetFlash is the post/redirect/get confirmation: a section leaves the
// message and it shows on the next admin page the user loads, wherever
// that is.
func TestSetFlashInsideASection(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		drive := mountSection(t, s, u, Section{
			Path: "reports",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				SetFlash(r, "Report generated.")
				http.Redirect(w, r, SectionPath(r), http.StatusSeeOther)
			}),
		})
		rec, r := drive(http.MethodPost, "/x/reports/generate")

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
		// SectionPath is the absolute browser-facing URL, which is what a
		// redirect out of a mount-stripped handler needs.
		if loc := rec.Header().Get("Location"); loc != "/admin/x/reports/" {
			t.Errorf("Location = %q, want the section's own base URL", loc)
		}
		if f := flashOf(s, r); f != "Report generated." {
			t.Errorf("flash = %q, want the message the section set", f)
		}
	})
}

// RenderPage is how a section draws a page that looks like the rest of the
// admin: the host's markup goes in unescaped, wrapped in the chrome the
// built-in screens wear.
func TestRenderPageInsideASection(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		// Registered as well as mounted: the chrome draws its nav from
		// the sections the host declared.
		s.deps.Sections = []Section{{Path: "reports", NavLabel: "Reports"}}

		drive := mountSection(t, s, u, Section{
			Path:     "reports",
			NavLabel: "Reports",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				RenderPage(w, r, "Quarterly reports", `<table id="host-markup"><tr><td>42</td></tr></table>`)
			}),
		})
		rec, _ := drive(http.MethodGet, "/x/reports/")

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("Content-Type = %q, want HTML", ct)
		}

		body := rec.Body.String()
		// Host HTML is trusted code, so it goes in as markup rather than
		// as escaped text.
		if !strings.Contains(body, `<table id="host-markup">`) {
			t.Errorf("the host's markup was escaped or dropped: %q", body)
		}
		if strings.Contains(body, "&lt;table") {
			t.Errorf("the host's markup was escaped: %q", body)
		}
		if !strings.Contains(body, "Quarterly reports") {
			t.Errorf("the title is missing: %q", body)
		}
		// And it is inside the admin, not a bare page: the section's own
		// nav link is one thing only the chrome draws.
		if !strings.Contains(body, `href="/admin/x/reports/"`) {
			t.Error("the page is not wrapped in the admin's navigation")
		}
		if !strings.Contains(body, "Log out") {
			t.Error("the page is missing the admin's chrome")
		}
	})
}

// A flash left by a previous request is shown by the next page that
// renders, which for a section is RenderPage.
func TestRenderPageShowsAFlash(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		mounted := s.sectionHandler(Section{
			Path: "reports",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				SetFlash(r, "Saved from the section.")
				RenderPage(w, r, "Reports", "<p>done</p>")
			}),
		})
		r := withRouteAndSession(t, s, u, httptest.NewRequest(http.MethodGet, "/x/reports/", nil), nil)
		rec := httptest.NewRecorder()
		mounted.ServeHTTP(rec, r)

		if !strings.Contains(rec.Body.String(), "Saved from the section.") {
			t.Errorf("the flash was not shown: %q", rec.Body.String())
		}
	})
}

// Every one of these is documented to work outside a section handler, and
// what it does there is part of the contract: a host that calls one from
// the wrong place gets a defined answer rather than a panic taking the
// process down.
func TestSectionHelpersOutsideASection(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/somewhere-else", nil)

	if serverFrom(r) != nil {
		t.Error("serverFrom found a server outside a section handler")
	}
	if u := UserFrom(r); u != nil {
		t.Errorf("UserFrom = %v, want nil", u)
	}
	if tok := CSRFToken(r); tok != "" {
		t.Errorf("CSRFToken = %q, want empty", tok)
	}
	// A no-op rather than a panic: there is no session to put it in.
	SetFlash(r, "nobody will see this")

	// RenderPage cannot do anything useful without the chrome, so it says
	// so in a 500 rather than writing a half-page.
	rec := httptest.NewRecorder()
	RenderPage(rec, r, "Title", "<p>body</p>")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not inside an admin section handler") {
		t.Errorf("body = %q, want it to say why", rec.Body.String())
	}
}

// A section handler sees mount-stripped paths, so its own base URL is
// only reachable through SectionPath — and the navigation has to mark the
// section being viewed off that base rather than off a path that no
// longer names it.
func TestSectionPathDrivesTheNavHighlight(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		s.deps.Sections = []Section{
			{Path: "reports", NavLabel: "Reports"},
			{Path: "imports", NavLabel: "Imports"},
		}

		var data templateData
		drive := mountSection(t, s, u, Section{
			Path:     "reports",
			NavLabel: "Reports",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data = serverFrom(r).newTemplateData(r)
			}),
		})
		drive(http.MethodGet, "/x/reports/deep/sub/page")

		var active []string
		for _, link := range data.NavSections {
			if link.Active {
				active = append(active, link.Label)
			}
		}
		if len(active) != 1 || active[0] != "Reports" {
			t.Errorf("active nav links = %v, want just Reports", active)
		}
		// Inside a section the built-in path matching is looking at
		// section-relative paths, where "/" would otherwise read as the
		// dashboard.
		if data.NavCurrent != "" {
			t.Errorf("NavCurrent = %q, want no built-in entry marked", data.NavCurrent)
		}
	})
}
