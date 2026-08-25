package admin

// The admin's half of the site lock: a closed site admits superadmins and
// nobody else. Without this the lock would hide the site from the public
// and leave the panel open to every editor — which is not what "the site
// is closed" says, and not what the switch is for.

import (
	"context"
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

// The two things a closed site says on the login page, and they are
// deliberately not one thing: the notice states what the site is, above
// the form and before anyone types; the error states what happened to an
// attempt that was made anyway.
const (
	lockedNotice  = "Only superadministrators can sign in"
	lockedRefusal = "This account cannot be used while the site is closed."
)

// lockTestServer is settingsTestServer with the lock switch in the
// caller's hand, so one server can be closed and opened mid-test.
func lockTestServer(t *testing.T, db *sqldb.DB, locked *bool) (*httptest.Server, *auth.Store) {
	t.Helper()
	users := auth.NewStore(db)
	h := New(Deps{
		Sessions:   scs.New(),
		Users:      users,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath:  "/admin",
		SiteLocked: func(context.Context) bool { return *locked },
	})
	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, users
}

func TestLockedSiteAdmitsOnlySuperadmins(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		locked := true
		srv, users := lockTestServer(t, db, &locked)
		seedPermUser(t, users, "editor@example.com", auth.RoleEditor)
		seedPermUser(t, users, "admin@example.com", auth.RoleAdmin)
		seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)

		// Before anyone types: the form itself says the site is closed
		// and who may still sign in, so an editor whose account works
		// perfectly well is not left reading a refusal as a fault.
		if _, page := getPage(t, srv, newClient(t), "/admin/login"); !strings.Contains(page, lockedNotice) {
			t.Errorf("the login form carries no closed-site notice:\n%s", page)
		}

		// The right password is still the wrong answer: an editor and an
		// admin alike are turned away — told what happened to the
		// attempt, on a page that still says why — and no session is
		// granted, so the dashboard they try next sends them back to the
		// login form.
		for _, email := range []string{"editor@example.com", "admin@example.com"} {
			client := newClient(t)
			page := logIn(t, srv, client, email, "password123")
			if !strings.Contains(page, lockedRefusal) {
				t.Errorf("%s: login page missing the refusal:\n%s", email, page)
			}
			if !strings.Contains(page, lockedNotice) {
				t.Errorf("%s: refused login page missing the closed-site notice:\n%s", email, page)
			}
			resp, _ := getPage(t, srv, client, "/admin/")
			if resp.Request.URL.Path != "/admin/login" {
				t.Errorf("%s: reached %s after a refused login, want the login page",
					email, resp.Request.URL.Path)
			}
		}

		// The superadmin signs in as always, and every page they load
		// says the site is closed so the state cannot be forgotten.
		client := newClient(t)
		page := logIn(t, srv, client, "super@example.com", "password123")
		if strings.Contains(page, lockedRefusal) {
			t.Fatalf("superadmin was refused a closed site:\n%s", page)
		}
		if !strings.Contains(page, "Site closed") {
			t.Errorf("superadmin's dashboard carries no closed-site badge:\n%s", page)
		}

		// Opened again, everyone is back.
		locked = false
		editor := newClient(t)
		if page := logIn(t, srv, editor, "editor@example.com", "password123"); strings.Contains(page, lockedRefusal) {
			t.Errorf("reopened: the editor was still refused:\n%s", page)
		}
		if resp, page := getPage(t, srv, editor, "/admin/"); resp.StatusCode != http.StatusOK {
			t.Errorf("reopened: dashboard status = %d, want 200:\n%s", resp.StatusCode, page)
		}
		if _, page := getPage(t, srv, newClient(t), "/admin/login"); strings.Contains(page, lockedNotice) {
			t.Errorf("reopened: the login form still carries the closed-site notice:\n%s", page)
		}
	})
}

func TestLockStopsASessionAlreadySignedIn(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		locked := false
		srv, users := lockTestServer(t, db, &locked)
		seedPermUser(t, users, "editor@example.com", auth.RoleEditor)

		client := newClient(t)
		logIn(t, srv, client, "editor@example.com", "password123")
		if resp, _ := getPage(t, srv, client, "/admin/"); resp.StatusCode != http.StatusOK {
			t.Fatalf("signed in on an open site: dashboard status = %d, want 200", resp.StatusCode)
		}

		// The editor was already working when the site closed. The next
		// request is where they stop — 503 rather than 403, because
		// nothing is wrong with their account.
		locked = true
		resp, page := getPage(t, srv, client, "/admin/")
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("locked mid-session: status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
		}
		if !strings.Contains(page, lockedRefusal) {
			t.Errorf("locked mid-session: page missing the refusal:\n%s", page)
		}

		// And a POST is refused with it — the door is the session check,
		// not the page render.
		resp, _ = postForm(t, srv, client, "/admin/settings/profile", url.Values{"name": {"Renamed"}})
		if resp.StatusCode == http.StatusOK {
			t.Error("locked mid-session: a form post went through")
		}
	})
}
