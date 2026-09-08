package admin

// The forgot-password flow, end to end against a real database: ask for a
// link, receive the email, follow it, set a new password, log in with it.
// Also the properties the flow exists to hold — one answer for every
// address, single-use tokens, and no routes at all without a Mailer.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// recordingMailer captures sends so a test can read the reset link the
// way a person would read their inbox.
type recordingMailer struct {
	mu    sync.Mutex
	sends []recordedMail
	done  chan struct{} // closed signal per send, for awaiting the goroutine
}

type recordedMail struct {
	To, Subject, Text, HTML string
}

func newRecordingMailer() *recordingMailer {
	return &recordingMailer{done: make(chan struct{}, 8)}
}

func (m *recordingMailer) Send(_ context.Context, to, subject, text, html string) error {
	m.mu.Lock()
	m.sends = append(m.sends, recordedMail{to, subject, text, html})
	m.mu.Unlock()
	m.done <- struct{}{}
	return nil
}

// wait blocks until one send lands or the deadline passes, returning the
// mail. The handler delivers in a goroutine, so tests must wait, not peek.
func (m *recordingMailer) wait(t *testing.T) recordedMail {
	t.Helper()
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
		t.Fatal("no reset email was sent")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sends[len(m.sends)-1]
}

func (m *recordingMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sends)
}

// resetTestServer is a full admin server over a real user store, with a
// recording mailer standing in for delivery. tweak, when non-nil, adjusts
// the Deps before the handler is built — the reset tests use it to set
// Deps.SiteURL, which decides where an emailed link points.
func resetTestServer(t *testing.T, db *sqldb.DB, mailer Mailer, tweak ...func(*Deps)) (*httptest.Server, *http.Client, *auth.Store) {
	t.Helper()
	users := auth.NewStore(db)
	deps := Deps{
		Sessions:  scs.New(),
		Users:     users,
		Mailer:    mailer,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath: "/admin",
	}
	for _, fn := range tweak {
		fn(&deps)
	}
	h := New(deps)
	// Mounted the way a host mounts it — under AdminPath with the prefix
	// stripped — because the flow's redirects land on absolute admin
	// paths and must resolve.
	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", h))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, &http.Client{Jar: jar}, users
}

// csrfFrom fetches a page and pulls the CSRF token from its form.
func csrfFrom(t *testing.T, srv *httptest.Server, client *http.Client, path string) string {
	t.Helper()
	resp, err := client.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := csrfRe.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf token on %s:\n%s", path, body)
	}
	return string(m[1])
}

func seedActiveUser(t *testing.T, users *auth.Store, email, password string) *auth.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	u := &auth.User{Email: email, Name: "Pat", PasswordHash: hash, Role: auth.RoleEditor, Active: true}
	if _, err := users.Insert(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

var resetLinkRe = regexp.MustCompile(`https?://[^\s]+/reset-password\?token=[A-Za-z0-9_-]+`)

func TestResetFlowEndToEnd(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		mailer := newRecordingMailer()
		srv, client, users := resetTestServer(t, db, mailer)
		seedActiveUser(t, users, "pat@example.com", "old-password-1")

		// Ask for the link.
		token := csrfFrom(t, srv, client, "/admin/forgot-password")
		resp, err := client.PostForm(srv.URL+"/admin/forgot-password", url.Values{
			"csrf_token": {token}, "email": {"pat@example.com"},
		})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "Check your email") {
			t.Fatalf("no confirmation page:\n%s", body)
		}

		// Read the inbox.
		mail := mailer.wait(t)
		if mail.To != "pat@example.com" {
			t.Fatalf("email went to %q", mail.To)
		}
		link := resetLinkRe.FindString(mail.Text)
		if link == "" {
			t.Fatalf("no reset link in the email body:\n%s", mail.Text)
		}
		// Followed as sent. Rewriting the host here would have made this
		// test pass no matter where the link pointed, which is exactly
		// the property the flow has to get right — see
		// TestResetLinkIgnoresHostHeader.
		if !strings.HasPrefix(link, srv.URL+"/admin/reset-password?token=") {
			t.Fatalf("reset link does not address this server:\ngot  %s\nwant %s/admin/reset-password?token=...", link, srv.URL)
		}

		// The form behind the link, twice: looking is not spending.
		for i := 0; i < 2; i++ {
			resp, err := client.Get(link)
			if err != nil {
				t.Fatal(err)
			}
			page, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !strings.Contains(string(page), "Set a new password") {
				t.Fatalf("visit %d did not show the reset form:\n%s", i, page)
			}
		}

		// Set the new password.
		u, _ := url.Parse(link)
		resetToken := u.Query().Get("token")
		csrf := csrfFrom(t, srv, client, "/admin/forgot-password") // any form serves the session token
		resp, err = client.PostForm(srv.URL+"/admin/reset-password", url.Values{
			"csrf_token": {csrf},
			"token":      {resetToken},
			"password":   {"brand-new-password"},
			"confirm":    {"brand-new-password"},
		})
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(page), "Your password has been changed") {
			t.Fatalf("no success flash after reset:\n%s", page)
		}

		// The old password is dead, the new one works.
		if _, err := users.Authenticate(context.Background(), "pat@example.com", "old-password-1"); err == nil {
			t.Fatal("old password still authenticates")
		}
		if _, err := users.Authenticate(context.Background(), "pat@example.com", "brand-new-password"); err != nil {
			t.Fatalf("new password rejected: %v", err)
		}

		// The link is spent: following it again gets the dead-link page.
		resp, err = client.Get(link)
		if err != nil {
			t.Fatal(err)
		}
		page, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		// Apostrophe-free substring: html/template writes "doesn't" as
		// "doesn&#39;t".
		if !strings.Contains(string(page), "work any more") {
			t.Fatalf("spent link did not show the invalid page:\n%s", page)
		}
	})
}

// TestResetUnknownAddressSaysNothing pins the no-oracle property: an
// address with no account gets the same page as one with an account, and
// no email moves.
func TestResetUnknownAddressSaysNothing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		mailer := newRecordingMailer()
		srv, client, users := resetTestServer(t, db, mailer)
		seedActiveUser(t, users, "real@example.com", "password-123")

		post := func(email string) string {
			csrf := csrfFrom(t, srv, client, "/admin/forgot-password")
			resp, err := client.PostForm(srv.URL+"/admin/forgot-password", url.Values{
				"csrf_token": {csrf}, "email": {email},
			})
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return string(body)
		}

		unknown := post("nobody@example.com")
		if !strings.Contains(unknown, "Check your email") {
			t.Fatalf("unknown address did not get the confirmation page:\n%s", unknown)
		}
		if n := mailer.count(); n != 0 {
			t.Fatalf("unknown address caused %d send(s)", n)
		}

		known := post("real@example.com")
		mailer.wait(t)
		// Same page, byte for byte, once the CSRF token (per-render) is
		// normalized out — the strongest form of "says nothing".
		strip := func(s string) string { return csrfRe.ReplaceAllString(s, "") }
		if strip(unknown) != strip(known) {
			t.Error("known and unknown addresses render different confirmation pages")
		}
	})
}

// TestResetInactiveUserGetsNoEmail: deactivated accounts cannot reset
// their way back in.
func TestResetInactiveUserGetsNoEmail(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		mailer := newRecordingMailer()
		srv, client, users := resetTestServer(t, db, mailer)
		hash, _ := auth.HashPassword("password-123")
		u := &auth.User{Email: "gone@example.com", PasswordHash: hash, Role: auth.RoleEditor, Active: false}
		if _, err := users.Insert(context.Background(), u); err != nil {
			t.Fatal(err)
		}

		csrf := csrfFrom(t, srv, client, "/admin/forgot-password")
		resp, err := client.PostForm(srv.URL+"/admin/forgot-password", url.Values{
			"csrf_token": {csrf}, "email": {"gone@example.com"},
		})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "Check your email") {
			t.Fatalf("inactive address did not get the confirmation page:\n%s", body)
		}
		if n := mailer.count(); n != 0 {
			t.Fatalf("inactive account caused %d send(s)", n)
		}
	})
}

// TestResetShortPasswordKeepsTokenAlive: a validation failure must not
// burn the link.
func TestResetShortPasswordKeepsTokenAlive(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		mailer := newRecordingMailer()
		srv, client, users := resetTestServer(t, db, mailer)
		u := seedActiveUser(t, users, "typo@example.com", "password-123")

		token, err := users.MintReset(context.Background(), u.ID)
		if err != nil {
			t.Fatal(err)
		}

		csrf := csrfFrom(t, srv, client, "/admin/forgot-password")
		resp, err := client.PostForm(srv.URL+"/admin/reset-password", url.Values{
			"csrf_token": {csrf}, "token": {token},
			"password": {"short"}, "confirm": {"short"},
		})
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("short password got status %d", resp.StatusCode)
		}
		if !strings.Contains(string(page), "at least 8 characters") {
			t.Fatalf("no length message:\n%s", page)
		}

		// The token survived the typo.
		if _, err := users.ResetUser(context.Background(), token); err != nil {
			t.Fatalf("token was burned by a validation failure: %v", err)
		}
	})
}

// TestNoMailerNoRoutes: without a Mailer the whole flow is absent — no
// link on the login page, and the routes answer 404.
func TestNoMailerNoRoutes(t *testing.T) {
	srv, client := newLoginTestServer(t, nil)

	_, page := getLogin(t, srv, client)
	if strings.Contains(page, "forgot-password") {
		t.Error("login page links to forgot-password with no Mailer configured")
	}

	for _, path := range []string{"/forgot-password", "/reset-password"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d with no Mailer, want 404", path, resp.StatusCode)
		}
	}
}

// postForgotAs submits the forgot-password form with a chosen Host
// header, the way an attacker would: they load the real form to get a
// session and a CSRF token that goes with it — both are theirs for the
// asking, the page is public — and then send the form back claiming to be
// a host of their choosing. The connection still goes to the test server;
// only the name the request claims is different.
//
// The session cookie is attached by hand rather than left to the client's
// jar. Go's Client looks jar cookies up under req.Host when it is set
// (net/http/client.go, Client.send), so a spoofed Host means the jar
// finds nothing, the request arrives with no session, and the CSRF check
// rejects it before it can reach the code under test. That is browser
// behaviour, not server behaviour, and an attacker writing raw requests
// is under no such restriction.
func postForgotAs(t *testing.T, srv *httptest.Server, client *http.Client, host, email string) string {
	t.Helper()
	csrf := csrfFrom(t, srv, client, "/admin/forgot-password")

	body := strings.NewReader(url.Values{"csrf_token": {csrf}, "email": {email}}.Encode())
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/forgot-password", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range client.Jar.Cookies(srvURL) {
		req.AddCookie(c)
	}
	req.Host = host // what the server sees in r.Host

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// A CSRF rejection here would mean the request never reached the
	// handler, and every assertion downstream would be vacuous.
	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("the spoofed request was rejected before reaching the handler: %s", page)
	}
	return string(page)
}

// TestResetLinkIgnoresHostHeader is the reason emailBaseURL exists. A
// reset link is read by whoever owns the mailbox, not by whoever asked
// for it, so a base taken from the request's Host would let anyone mail a
// victim a genuine, working reset link pointing at a server of their
// choosing. With no Config.SiteURL to fall back on, the CMS must send
// nothing at all rather than send that.
//
// The confirmation page still has to look identical — a site that
// answered differently when misconfigured would be a new oracle for which
// addresses have accounts.
func TestResetLinkIgnoresHostHeader(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		mailer := newRecordingMailer()
		srv, client, users := resetTestServer(t, db, mailer) // no SiteURL configured
		u := seedActiveUser(t, users, "pat@example.com", "password-123")

		// A link Pat legitimately holds already, so the declined request
		// below can be checked for not having revoked it.
		live, err := users.MintReset(context.Background(), u.ID)
		if err != nil {
			t.Fatal(err)
		}

		page := postForgotAs(t, srv, client, "attacker.example", "pat@example.com")
		if !strings.Contains(page, "Check your email") {
			t.Fatalf("a spoofed Host changed the answer the visitor gets:\n%s", page)
		}

		// The send is a goroutine, so absence needs a moment to be real.
		time.Sleep(250 * time.Millisecond)
		if n := mailer.count(); n != 0 {
			t.Fatalf("sent %d reset email(s) built from an attacker-chosen Host: %+v", n, mailer.sends)
		}

		// Nothing was minted either. Minting revokes the previous token,
		// so a request the CMS declines to act on must not be a way to
		// keep destroying somebody's live reset link.
		if _, err := users.ResetUser(context.Background(), live); err != nil {
			t.Fatalf("the declined request revoked a live reset token: %v", err)
		}
	})
}

// TestResetLinkUsesConfiguredSiteURL: once the host says what the site is
// called, that is what the email says, whatever the request claims.
func TestResetLinkUsesConfiguredSiteURL(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		mailer := newRecordingMailer()
		srv, client, users := resetTestServer(t, db, mailer, func(d *Deps) {
			d.SiteURL = "https://real.example"
		})
		seedActiveUser(t, users, "pat@example.com", "password-123")

		postForgotAs(t, srv, client, "attacker.example", "pat@example.com")
		mail := mailer.wait(t)

		link := resetLinkRe.FindString(mail.Text)
		if !strings.HasPrefix(link, "https://real.example/admin/reset-password?token=") {
			t.Fatalf("reset link did not use the configured SiteURL:\n%s", link)
		}
		if strings.Contains(mail.Text, "attacker.example") || strings.Contains(mail.HTML, "attacker.example") {
			t.Fatalf("the spoofed host reached the email body:\n%s", mail.Text)
		}
	})
}
