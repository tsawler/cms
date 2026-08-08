package admin

// The account settings page and the two-factor login, end to end against
// a real database: change a password behind the current one, enroll an
// authenticator app from the QR/manual key the page shows, log in through
// the code challenge, and the properties that make it worth having — a
// password alone does not finish a two-factor login, and no code is
// accepted twice.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// settingsTestServer is a full admin server over a real user store,
// mounted the way a host mounts it so redirects resolve.
func settingsTestServer(t *testing.T, db *sqldb.DB) (*httptest.Server, *auth.Store) {
	t.Helper()
	users := auth.NewStore(db)
	h := New(Deps{
		Sessions:  scs.New(),
		Users:     users,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath: "/admin",
	})
	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, users
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

// logIn drives the login form and returns the final page, following
// redirects — the dashboard for a plain account, the code challenge for a
// two-factor one.
func logIn(t *testing.T, srv *httptest.Server, client *http.Client, email, password string) string {
	t.Helper()
	csrf := csrfFrom(t, srv, client, "/admin/login")
	resp, err := client.PostForm(srv.URL+"/admin/login", url.Values{
		"csrf_token": {csrf}, "email": {email}, "password": {password},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return string(body)
}

func postForm(t *testing.T, srv *httptest.Server, client *http.Client, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := client.PostForm(srv.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestSettingsChangePassword(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		client := newClient(t)
		seedActiveUser(t, users, "pat@example.com", "old-password-1")
		logIn(t, srv, client, "pat@example.com", "old-password-1")

		csrf := csrfFrom(t, srv, client, "/admin/settings")

		// The wrong current password changes nothing.
		resp, page := postForm(t, srv, client, "/admin/settings/password", url.Values{
			"csrf_token":       {csrf},
			"current_password": {"not-my-password"},
			"password":         {"brand-new-password"},
			"confirm":          {"brand-new-password"},
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("wrong current password: status = %d, want 422", resp.StatusCode)
		}
		if !strings.Contains(page, "current password") {
			t.Fatalf("no current-password message:\n%s", page)
		}
		if _, err := users.Authenticate(context.Background(), "pat@example.com", "old-password-1"); err != nil {
			t.Fatal("password changed despite wrong current password")
		}

		// Mismatched confirmation changes nothing either.
		resp, page = postForm(t, srv, client, "/admin/settings/password", url.Values{
			"csrf_token":       {csrf},
			"current_password": {"old-password-1"},
			"password":         {"brand-new-password"},
			"confirm":          {"different-password"},
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("mismatched confirm: status = %d, want 422", resp.StatusCode)
		}
		if !strings.Contains(page, "match") {
			t.Fatalf("no mismatch message:\n%s", page)
		}

		// The real thing.
		_, page = postForm(t, srv, client, "/admin/settings/password", url.Values{
			"csrf_token":       {csrf},
			"current_password": {"old-password-1"},
			"password":         {"brand-new-password"},
			"confirm":          {"brand-new-password"},
		})
		if !strings.Contains(page, "Your password has been changed.") {
			t.Fatalf("no success flash:\n%s", page)
		}
		if _, err := users.Authenticate(context.Background(), "pat@example.com", "old-password-1"); err == nil {
			t.Error("old password still authenticates")
		}
		if _, err := users.Authenticate(context.Background(), "pat@example.com", "brand-new-password"); err != nil {
			t.Errorf("new password rejected: %v", err)
		}
	})
}

var totpSecretRe = regexp.MustCompile(`class="cms-totp-secret">([A-Z2-7 ]+)<`)

// TestTwoFactorEnrollment: the settings page's enroll flow — generate,
// show, and only save once a live code proves the app has the secret.
func TestTwoFactorEnrollment(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		client := newClient(t)
		u := seedActiveUser(t, users, "pat@example.com", "password-123")
		logIn(t, srv, client, "pat@example.com", "password-123")

		csrf := csrfFrom(t, srv, client, "/admin/settings")
		_, page := postForm(t, srv, client, "/admin/settings/2fa/setup", url.Values{
			"csrf_token": {csrf},
		})

		// The page shows the pending secret (QR plus manual key)...
		m := totpSecretRe.FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("no manual-entry secret on the settings page:\n%s", page)
		}
		secret := strings.ReplaceAll(m[1], " ", "")
		if !strings.Contains(page, "data:image/png;base64,") {
			t.Fatalf("no QR code on the settings page:\n%s", page)
		}

		// ...but nothing is saved yet, and a wrong code saves nothing.
		if got, _ := users.GetByID(context.Background(), u.ID); got.TwoFactorEnabled() {
			t.Fatal("two-factor enabled before any code confirmed it")
		}
		resp, page := postForm(t, srv, client, "/admin/settings/2fa/confirm", url.Values{
			"csrf_token": {csrf}, "code": {"000000"},
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("wrong confirm code: status = %d, want 422", resp.StatusCode)
		}
		if got, _ := users.GetByID(context.Background(), u.ID); got.TwoFactorEnabled() {
			t.Fatal("two-factor enabled by a wrong code")
		}

		// A live code turns it on, with the shown secret.
		code, err := auth.TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		_, page = postForm(t, srv, client, "/admin/settings/2fa/confirm", url.Values{
			"csrf_token": {csrf}, "code": {code},
		})
		if !strings.Contains(page, "Two-factor authentication is on.") {
			t.Fatalf("no success flash:\n%s", page)
		}
		got, err := users.GetByID(context.Background(), u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.TwoFactorEnabled() || got.TOTPSecret != secret {
			t.Fatalf("stored secret = %q, want the one the page showed", got.TOTPSecret)
		}
	})
}

// TestTwoFactorLogin: with two-factor on, a password parks the visitor at
// the code challenge — not in — and codes are single-use.
func TestTwoFactorLogin(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		u := seedActiveUser(t, users, "pat@example.com", "password-123")
		const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
		// Enrolled directly (last step 0) so the codes this test computes
		// are all fresh, independent of when enrollment would have run.
		if err := users.EnableTOTP(context.Background(), u.ID, secret, 0); err != nil {
			t.Fatal(err)
		}

		client := newClient(t)
		page := logIn(t, srv, client, "pat@example.com", "password-123")
		if !strings.Contains(page, "Enter your code") {
			t.Fatalf("login did not land on the code challenge:\n%s", page)
		}

		// The password alone is not a session: the admin still bounces to
		// the login page.
		resp, err := client.Get(srv.URL + "/admin/")
		if err != nil {
			t.Fatal(err)
		}
		home, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(home), "Log in") {
			t.Fatalf("admin reachable after only the password step:\n%s", home)
		}

		csrf := csrfFrom(t, srv, client, "/admin/login/2fa")

		// A wrong code is refused.
		resp, page = postForm(t, srv, client, "/admin/login/2fa", url.Values{
			"csrf_token": {csrf}, "code": {"000000"},
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("wrong code: status = %d, want 422", resp.StatusCode)
		}

		// The right code logs in.
		code, err := auth.TOTPCode(secret, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		_, page = postForm(t, srv, client, "/admin/login/2fa", url.Values{
			"csrf_token": {csrf}, "code": {code},
		})
		if !strings.Contains(page, "Dashboard") {
			t.Fatalf("correct code did not reach the dashboard:\n%s", page)
		}

		// Log out and try to replay the same code: its step is spent, so
		// it is refused however fresh the clock still thinks it is.
		csrf = csrfFrom(t, srv, client, "/admin/settings")
		postForm(t, srv, client, "/admin/logout", url.Values{"csrf_token": {csrf}})

		logIn(t, srv, client, "pat@example.com", "password-123")
		csrf = csrfFrom(t, srv, client, "/admin/login/2fa")
		resp, _ = postForm(t, srv, client, "/admin/login/2fa", url.Values{
			"csrf_token": {csrf}, "code": {code},
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("replayed code: status = %d, want 422", resp.StatusCode)
		}

		// A later step's code still works.
		next, err := auth.TOTPCode(secret, time.Now().Add(30*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		_, page = postForm(t, srv, client, "/admin/login/2fa", url.Values{
			"csrf_token": {csrf}, "code": {next},
		})
		if !strings.Contains(page, "Dashboard") {
			t.Fatalf("next step's code did not reach the dashboard:\n%s", page)
		}
	})
}

// TestTwoFactorDisable: turning it off needs the password, and an admin
// can reset a locked-out user from the user form.
func TestTwoFactorDisable(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		srv, users := settingsTestServer(t, db)
		const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

		// Self-service, behind the password.
		u := seedActiveUser(t, users, "pat@example.com", "password-123")
		if err := users.EnableTOTP(context.Background(), u.ID, secret, 0); err != nil {
			t.Fatal(err)
		}
		client := newClient(t)
		logIn(t, srv, client, "pat@example.com", "password-123")
		code, _ := auth.TOTPCode(secret, time.Now())
		csrf := csrfFrom(t, srv, client, "/admin/login/2fa")
		postForm(t, srv, client, "/admin/login/2fa", url.Values{"csrf_token": {csrf}, "code": {code}})

		csrf = csrfFrom(t, srv, client, "/admin/settings")
		resp, _ := postForm(t, srv, client, "/admin/settings/2fa/disable", url.Values{
			"csrf_token": {csrf}, "current_password": {"wrong"},
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("wrong password on disable: status = %d, want 422", resp.StatusCode)
		}
		if got, _ := users.GetByID(context.Background(), u.ID); !got.TwoFactorEnabled() {
			t.Fatal("two-factor disabled despite wrong password")
		}
		_, page := postForm(t, srv, client, "/admin/settings/2fa/disable", url.Values{
			"csrf_token": {csrf}, "current_password": {"password-123"},
		})
		if !strings.Contains(page, "Two-factor authentication is off.") {
			t.Fatalf("no disabled flash:\n%s", page)
		}
		if got, _ := users.GetByID(context.Background(), u.ID); got.TwoFactorEnabled() {
			t.Fatal("two-factor still enabled after disable")
		}

		// The admin rescue: reset another user's two-factor from the form.
		hash, _ := auth.HashPassword("admin-password-1")
		adminUser := &auth.User{Email: "boss@example.com", Name: "Boss", PasswordHash: hash, Role: auth.RoleAdmin, Active: true}
		if _, err := users.Insert(context.Background(), adminUser); err != nil {
			t.Fatal(err)
		}
		if err := users.EnableTOTP(context.Background(), u.ID, secret, 0); err != nil {
			t.Fatal(err)
		}

		adminClient := newClient(t)
		logIn(t, srv, adminClient, "boss@example.com", "admin-password-1")
		csrf = csrfFrom(t, srv, adminClient, "/admin/users")
		resp, page = postForm(t, srv, adminClient, "/admin/users/"+strconv.FormatInt(u.ID, 10), url.Values{
			"csrf_token": {csrf},
			"name":       {"Pat"},
			"email":      {"pat@example.com"},
			"role":       {"editor"},
			"active":     {"on"},
			"reset_totp": {"on"},
		})
		if !strings.Contains(page, "User updated.") {
			t.Fatalf("admin update failed (status %d):\n%s", resp.StatusCode, page)
		}
		if got, _ := users.GetByID(context.Background(), u.ID); got.TwoFactorEnabled() {
			t.Fatal("admin reset did not clear two-factor")
		}
	})
}

// TestSettingsAndChallengeRequireTheirStates: unauthenticated visitors
// can reach neither the settings page nor a challenge they never earned.
func TestSettingsAndChallengeRequireTheirStates(t *testing.T) {
	srv, client := newLoginTestServer(t, nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := client.Get(srv.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), "/login") {
		t.Errorf("GET /settings unauthenticated = %d → %q, want redirect to login", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, err = client.Get(srv.URL + "/login/2fa")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), "/login") {
		t.Errorf("GET /login/2fa with no pending login = %d → %q, want redirect to login", resp.StatusCode, resp.Header.Get("Location"))
	}
}
