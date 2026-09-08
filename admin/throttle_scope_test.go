package admin

// What the login counters are actually a limit on.
//
// The per-source counter keys on an account *and* where the attempt came
// from, so it caps a source, never an account: an attacker with addresses
// to spend draws the whole allowance again from each one, and a /64 of
// IPv6 is free. These tests drive that attack — a different source per
// request — and pin that the per-account counters stop it anyway.
//
// The attack is spelled with Deps.ClientIPHeader rather than by opening
// sockets from different addresses, which a test cannot do. It is the
// same thing as far as the throttle is concerned: clientIP is the one
// place a source is decided, and varying its input is varying the source.

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
)

// newRotatingSourceServer serves the admin handler reading the client
// address out of a header, so a test can be a different source per
// request.
func newRotatingSourceServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	h := New(Deps{
		Sessions:       scs.New(),
		Users:          auth.NewStore(nil),
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminPath:      "/admin",
		ClientIPHeader: "X-Forwarded-For",
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, &http.Client{Jar: jar}
}

// postLoginFrom submits the login form claiming to come from source.
func postLoginFrom(t *testing.T, srv *httptest.Server, client *http.Client, source string, form url.Values) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", source)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// honeypotForm fails the way a wrong password does, without needing a
// database behind it: the honeypot is checked before the user lookup and
// counts against the same counters.
func honeypotForm(csrf, email string) url.Values {
	return url.Values{
		"csrf_token": {csrf},
		"email":      {email},
		"password":   {"whatever"},
		"website":    {"https://spam.example.com"},
	}
}

func TestLoginAccountLimitSurvivesRotatingSources(t *testing.T) {
	srv, client := newRotatingSourceServer(t)
	csrf, _ := getLogin(t, srv, client)
	form := honeypotForm(csrf, "target@example.com")

	// Every request from an address never seen before, so the per-source
	// counter (5) is never reached by any of them.
	for i := range perAccountPasswordLimit {
		source := "198.51.100." + strconv.Itoa(i%250+1)
		if code := postLoginFrom(t, srv, client, source, form); code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d from %s: status = %d, want 422", i+1, source, code)
		}
	}

	// One more, from an address with a clean per-source record. Before
	// the account counter existed this was unlimited.
	if code := postLoginFrom(t, srv, client, "203.0.113.99", form); code != http.StatusTooManyRequests {
		t.Errorf("attempt %d from a fresh source: status = %d, want 429 — "+
			"rotating the source is walking around the limit", perAccountPasswordLimit+1, code)
	}
}

// The account counter must be exactly that: locking one account out must
// not touch anybody else's, from any source.
func TestLoginAccountLimitIsPerAccount(t *testing.T) {
	srv, client := newRotatingSourceServer(t)
	csrf, _ := getLogin(t, srv, client)

	for i := range perAccountPasswordLimit + 1 {
		postLoginFrom(t, srv, client, "198.51.100."+strconv.Itoa(i%250+1), honeypotForm(csrf, "target@example.com"))
	}
	if code := postLoginFrom(t, srv, client, "203.0.113.99", honeypotForm(csrf, "target@example.com")); code != http.StatusTooManyRequests {
		t.Fatalf("the targeted account should be blocked: status = %d, want 429", code)
	}

	// A different account, and even the same source that just tripped the
	// block, is unaffected.
	if code := postLoginFrom(t, srv, client, "203.0.113.99", honeypotForm(csrf, "bystander@example.com")); code != http.StatusUnprocessableEntity {
		t.Errorf("an untargeted account: status = %d, want 422 — one account's "+
			"lockout must not spread", code)
	}
}

// The per-source counter still has to work: it is the tight one, and the
// account counters are deliberately loose enough that a single source
// hits five long before it reaches twenty-five.
func TestLoginPerSourceLimitStillApplies(t *testing.T) {
	srv, client := newRotatingSourceServer(t)
	csrf, _ := getLogin(t, srv, client)
	form := honeypotForm(csrf, "target@example.com")

	for i := range perSourceLimit {
		if code := postLoginFrom(t, srv, client, "198.51.100.1", form); code != http.StatusUnprocessableEntity {
			t.Fatalf("attempt %d: status = %d, want 422", i+1, code)
		}
	}
	if code := postLoginFrom(t, srv, client, "198.51.100.1", form); code != http.StatusTooManyRequests {
		t.Errorf("attempt %d from one source: status = %d, want 429", perSourceLimit+1, code)
	}
}
