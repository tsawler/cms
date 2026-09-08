package cms

// The session cookie's Secure flag, which is the one production setting
// whose absence is silent: nothing looks broken, the site works, and the
// admin session cookie is one plaintext request away from being readable.
// So it is derived from what the host has already said rather than left
// to be remembered separately.

import (
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestSecureCookiesDerivation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		want    bool
		comment string
	}{
		{"nothing said", Config{}, false,
			"a bare install is a developer on http://localhost"},
		{"http site url", Config{SiteURL: "http://localhost:4000"}, false,
			"an http:// address means what it says"},

		// The derivation, and the reason this issue existed.
		{"https site url", Config{SiteURL: "https://example.com"}, true,
			"a site served over HTTPS should not have to say so twice"},
		{"bare host site url", Config{SiteURL: "example.com"}, true,
			"normalizeSiteURL reads a bare host as https, so this counts too"},

		// Explicitly asked for: the case the derivation cannot see, an
		// HTTPS deployment that leaves SiteURL empty.
		{"forced on", Config{SecureCookies: true}, true, ""},
		{"forced on with http url", Config{SecureCookies: true, SiteURL: "http://localhost:4000"}, true,
			"an explicit request is still a request"},

		// There is no way to turn it off for an https:// site, and
		// nothing legitimate wants one: Secure describes the browser's
		// connection to the edge, not the edge's to this process, so
		// terminating TLS at a proxy is not a reason to drop it.
		{"cannot be unset for an https site", Config{SecureCookies: false, SiteURL: "https://example.com"}, true, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.SiteURL = normalizeSiteURL(cfg.SiteURL) // New does this first
			if got := secureCookies(cfg); got != tc.want {
				t.Errorf("secureCookies() = %v, want %v — %s", got, tc.want, tc.comment)
			}
		})
	}
}

// And the decision has to actually reach the cookie. A rule nothing
// applies is not a rule.
func TestNewMarksTheSessionCookieSecure(t *testing.T) {
	// sql.Open does not connect, and New never queries, so this needs no
	// database behind it.
	open := func(t *testing.T) *sql.DB {
		t.Helper()
		db, err := sql.Open("pgx", "postgres://nobody@127.0.0.1:1/none")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}

	for _, tc := range []struct {
		name string
		cfg  Config
		want bool
	}{
		{"https site url", Config{SiteURL: "https://example.com"}, true},
		{"explicit", Config{SecureCookies: true}, true},
		{"local development", Config{SiteURL: "http://localhost:4000"}, false},
		{"nothing said", Config{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.DB = open(t)
			c, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := c.sessions.Cookie.Secure; got != tc.want {
				t.Errorf("session cookie Secure = %v, want %v", got, tc.want)
			}
			// The flags that were already right stay right.
			if !c.sessions.Cookie.HttpOnly {
				t.Error("session cookie lost HttpOnly")
			}
			if c.sessions.Cookie.Persist {
				t.Error("session cookie became persistent by default")
			}
		})
	}
}
