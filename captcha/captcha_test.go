package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewValidates(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"complete", Config{URL: "http://cap:3000", SiteKey: "k", Secret: "s"}, true},
		{"trailing slash", Config{URL: "http://cap:3000/", SiteKey: "k", Secret: "s"}, true},
		{"missing url", Config{SiteKey: "k", Secret: "s"}, false},
		{"relative url", Config{URL: "cap:3000", SiteKey: "k", Secret: "s"}, false},
		{"missing site key", Config{URL: "http://cap:3000", Secret: "s"}, false},
		{"missing secret", Config{URL: "http://cap:3000", SiteKey: "k"}, false},
	}
	for _, tc := range cases {
		_, err := New(tc.cfg)
		if (err == nil) != tc.ok {
			t.Errorf("%s: New() err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestURLs(t *testing.T) {
	c, err := New(Config{URL: "https://cap.example.com/", SiteKey: "abc123", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.ScriptURL(), "https://cap.example.com/assets/widget.js"; got != want {
		t.Errorf("ScriptURL() = %q, want %q", got, want)
	}
	if got, want := c.WidgetEndpoint(), "https://cap.example.com/abc123/"; got != want {
		t.Errorf("WidgetEndpoint() = %q, want %q", got, want)
	}
	if got, want := c.Origin(), "https://cap.example.com"; got != want {
		t.Errorf("Origin() = %q, want %q", got, want)
	}
}

// fakeCap answers siteverify like a Cap server: success iff the secret and
// token match what it expects.
func fakeCap(t *testing.T, secret, goodToken string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/site1/siteverify" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var in struct{ Secret, Response string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("bad body: %v", err)
		}
		json.NewEncoder(w).Encode(map[string]bool{
			"success": in.Secret == secret && in.Response == goodToken,
		})
	}))
}

func TestVerify(t *testing.T) {
	srv := fakeCap(t, "sekrit", "good-token")
	defer srv.Close()

	c, err := New(Config{URL: srv.URL, SiteKey: "site1", Secret: "sekrit"})
	if err != nil {
		t.Fatal(err)
	}

	ok, err := c.Verify(context.Background(), "good-token")
	if err != nil || !ok {
		t.Errorf("Verify(good) = %v, %v; want true, nil", ok, err)
	}
	ok, err = c.Verify(context.Background(), "bad-token")
	if err != nil || ok {
		t.Errorf("Verify(bad) = %v, %v; want false, nil", ok, err)
	}
}

// A separate internal URL must be used for verification while the widget
// URLs keep pointing at the public address.
func TestVerifyUsesInternalURL(t *testing.T) {
	srv := fakeCap(t, "s", "tok")
	defer srv.Close()

	c, err := New(Config{URL: "https://cap.example.com", InternalURL: srv.URL, SiteKey: "site1", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := c.Verify(context.Background(), "tok")
	if err != nil || !ok {
		t.Errorf("Verify() = %v, %v; want true, nil", ok, err)
	}
	if got, want := c.WidgetEndpoint(), "https://cap.example.com/site1/"; got != want {
		t.Errorf("WidgetEndpoint() = %q, want %q", got, want)
	}
}

// Cap rejects bad tokens with 4xx statuses; those are verdicts and must
// come back as (false, nil), never as a fail-open error.
func TestVerifyRejectionStatuses(t *testing.T) {
	for _, status := range []int{400, 403, 404} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(`{"success":false,"error":"Token not found"}`))
		}))
		c, err := New(Config{URL: srv.URL, SiteKey: "site1", Secret: "s"})
		if err != nil {
			t.Fatal(err)
		}
		ok, err := c.Verify(context.Background(), "garbage")
		if err != nil || ok {
			t.Errorf("Verify() with %d rejection = %v, %v; want false, nil", status, ok, err)
		}
		srv.Close()
	}
}

// Transport failures and 5xx statuses must surface as errors, not as a
// rejected token, so the caller can choose to fail open.
func TestVerifyErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := New(Config{URL: srv.URL, SiteKey: "site1", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Verify(context.Background(), "tok"); err == nil {
		t.Error("Verify() with 500 response: want error, got nil")
	}

	srv.Close() // now unreachable
	if _, err := c.Verify(context.Background(), "tok"); err == nil {
		t.Error("Verify() with dead server: want error, got nil")
	}
}

// Every error out of Verify means the same thing — no verdict — but the
// two kinds want different things from whoever reads the log, so they
// have to be told apart. And the ways a misconfigured deployment can
// produce a non-verdict must not resolve into a quiet "no": a stray JSON
// document from whatever else is on that port would then read as a
// rejection and lock people out, where an HTML error page would let them
// through. Same misconfiguration, opposite outcomes, is the bug.
func TestVerifyNoVerdictKinds(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"server error", 500, `{"success":true}`, ErrUnavailable},
		{"gateway error", 502, "upstream gone", ErrUnavailable},

		// Something answered, but not with a verdict.
		{"html error page", 200, "<html><body>404 not found</body></html>", ErrBadResponse},
		{"proxy error page with a 4xx", 404, "<html>no such route</html>", ErrBadResponse},
		{"empty body", 200, "", ErrBadResponse},
		{"json without a verdict", 200, `{"error":"unknown site key"}`, ErrBadResponse},
		{"json array", 200, `[1,2,3]`, ErrBadResponse},
		{"json null", 200, `null`, ErrBadResponse},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, err := New(Config{URL: srv.URL, SiteKey: "site1", Secret: "s"})
			if err != nil {
				t.Fatal(err)
			}
			ok, err := c.Verify(context.Background(), "tok")
			if ok {
				t.Error("a non-verdict was read as a passing token")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("Verify() error = %v, want one matching %v", err, tc.want)
			}
			// The two kinds must stay distinguishable, or the caller
			// cannot say anything useful about either.
			other := ErrBadResponse
			if tc.want == ErrBadResponse {
				other = ErrUnavailable
			}
			if errors.Is(err, other) {
				t.Errorf("Verify() error matches both kinds: %v", err)
			}
		})
	}
}

// A real verdict is still a verdict, whichever way it goes, and neither
// is an error.
func TestVerifyVerdictsAreNotErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"accepted", 200, `{"success":true}`, true},
		{"rejected", 200, `{"success":false}`, false},
		{"rejected with a 4xx", 400, `{"success":false,"error":"Token not found"}`, false},
		// Extra fields are Cap's business, not ours.
		{"accepted with extras", 200, `{"success":true,"challenge":{"c":1}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, err := New(Config{URL: srv.URL, SiteKey: "site1", Secret: "s"})
			if err != nil {
				t.Fatal(err)
			}
			ok, err := c.Verify(context.Background(), "tok")
			if err != nil {
				t.Fatalf("Verify() = %v, want a verdict", err)
			}
			if ok != tc.want {
				t.Errorf("Verify() = %v, want %v", ok, tc.want)
			}
		})
	}
}

// A server that keeps writing must not keep the goroutine with it.
func TestVerifyBoundsTheResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"junk":"`))
		chunk := strings.Repeat("x", 4096)
		for range 1000 { // ~4MB, far past the cap
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
		w.Write([]byte(`"}`))
	}))
	defer srv.Close()

	c, err := New(Config{URL: srv.URL, SiteKey: "site1", Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	// Truncated mid-value, so it decodes to nothing usable — the point is
	// that it returns at all rather than reading everything offered.
	ok, err := c.Verify(context.Background(), "tok")
	if ok {
		t.Error("a truncated body was read as a passing token")
	}
	if !errors.Is(err, ErrBadResponse) {
		t.Errorf("Verify() error = %v, want ErrBadResponse", err)
	}
}
