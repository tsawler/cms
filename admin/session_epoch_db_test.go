package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// signedIn reports whether the client's session still opens the admin,
// rather than bouncing to the login page.
func signedIn(t *testing.T, srv *httptest.Server, client *http.Client) bool {
	t.Helper()
	resp, _ := getPage(t, srv, client, "/admin/settings")
	return resp.StatusCode == http.StatusOK && resp.Request.URL.Path == "/admin/settings"
}

// Changing your password ends every other session the account has — the
// stolen one is usually why you changed it — and keeps the one you
// changed it from.
func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		seedActiveUser(t, users, "pat@example.com", "password-123")

		mine, thief := newClient(t), newClient(t)
		logIn(t, srv, mine, "pat@example.com", "password-123")
		logIn(t, srv, thief, "pat@example.com", "password-123")
		if !signedIn(t, srv, mine) || !signedIn(t, srv, thief) {
			t.Fatal("both sessions should start signed in")
		}

		csrf := csrfFrom(t, srv, mine, "/admin/settings")
		_, page := postForm(t, srv, mine, "/admin/settings/password", url.Values{
			"csrf_token": {csrf}, "current_password": {"password-123"},
			"password": {"brand-new-pass"}, "confirm": {"brand-new-pass"},
		})
		if !strings.Contains(page, "Your password has been changed.") {
			t.Fatalf("password change did not succeed:\n%s", page)
		}
		if !signedIn(t, srv, mine) {
			t.Error("the session that changed the password was signed out")
		}
		if signedIn(t, srv, thief) {
			t.Error("another session survived the password change")
		}
	})
}

// Every change to how an account signs in ends its sessions, whoever
// makes it: a reset or an admin setting the password, two-factor switched
// on, or switched off by an admin rescuing a lost phone.
func TestCredentialChangesEndSessions(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		ctx := context.Background()
		const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

		changes := map[string]func(u *auth.User) error{
			"password set": func(u *auth.User) error {
				hash, err := auth.HashPassword("someone-else-chose")
				if err != nil {
					return err
				}
				return users.UpdatePassword(ctx, u.ID, hash)
			},
			"two-factor on":  func(u *auth.User) error { return users.EnableTOTP(ctx, u.ID, secret, 0) },
			"two-factor off": func(u *auth.User) error { return users.DisableTOTP(ctx, u.ID) },
		}
		for name, change := range changes {
			u := seedActiveUser(t, users, name+"@example.com", "password-123")
			client := newClient(t)
			logIn(t, srv, client, u.Email, "password-123")
			if !signedIn(t, srv, client) {
				t.Fatalf("%s: not signed in to begin with", name)
			}
			if err := change(u); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if signedIn(t, srv, client) {
				t.Errorf("%s: the session survived", name)
			}
		}
	})
}

// A two-factor login is two requests apart. A password changed between
// them — the owner noticing a stolen password — strands the half-login
// made with the old one, even with a valid code.
func TestPasswordChangeStrandsAPendingTwoFactorLogin(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		ctx := context.Background()
		u := seedActiveUser(t, users, "pat@example.com", "password-123")
		const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
		if err := users.EnableTOTP(ctx, u.ID, secret, 0); err != nil {
			t.Fatal(err)
		}

		client := newClient(t)
		if page := logIn(t, srv, client, "pat@example.com", "password-123"); !strings.Contains(page, "Enter your code") {
			t.Fatalf("login did not reach the code challenge:\n%s", page)
		}
		csrf := csrfFrom(t, srv, client, "/admin/login/2fa")

		hash, err := auth.HashPassword("the-owners-new-one")
		if err != nil {
			t.Fatal(err)
		}
		if err := users.UpdatePassword(ctx, u.ID, hash); err != nil {
			t.Fatal(err)
		}

		code, err := auth.TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		postForm(t, srv, client, "/admin/login/2fa", url.Values{"csrf_token": {csrf}, "code": {code}})
		if signedIn(t, srv, client) {
			t.Error("a half-login made with the old password completed after the change")
		}
	})
}

// A masquerade hands the owner's account back on exit — unless the
// owner's credentials changed meanwhile, which ended their sessions,
// this one included.
func TestMasqueradeExitAfterOwnerCredentialChange(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		super := seedPermUser(t, users, "super@example.com", auth.RoleSuperadmin)
		editor := seedPermUser(t, users, "ed@example.com", auth.RoleEditor)

		client := newClient(t)
		logIn(t, srv, client, "super@example.com", "password123")
		csrf := csrfFrom(t, srv, client, "/admin/users")
		if resp, _ := postForm(t, srv, client, masqueradePath(editor.ID), url.Values{"csrf_token": {csrf}}); resp.Request.URL.Path != "/admin/" {
			t.Fatalf("masquerade did not start: landed on %s", resp.Request.URL.Path)
		}

		hash, err := auth.HashPassword("changed-elsewhere")
		if err != nil {
			t.Fatal(err)
		}
		if err := users.UpdatePassword(context.Background(), super.ID, hash); err != nil {
			t.Fatal(err)
		}

		postForm(t, srv, client, "/admin/masquerade/exit", url.Values{"csrf_token": {csrf}})
		if signedIn(t, srv, client) {
			t.Error("exiting the masquerade restored an owner whose password had changed")
		}
	})
}
