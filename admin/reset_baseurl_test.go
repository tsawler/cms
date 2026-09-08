package admin

// emailBaseURL decides where a password-reset link points, and it is the
// one place in the admin that must not believe the request. These tests
// are the table of what it will and will not build a link from.

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmailBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		siteURL  string
		host     string
		tls      bool
		wantBase string
		wantOK   bool
	}{
		// Configured: the answer, whatever the request claims.
		{"configured wins over a spoofed host", "https://real.example", "attacker.example", false, "https://real.example", true},
		{"configured wins over loopback", "https://real.example", "127.0.0.1:4000", false, "https://real.example", true},

		// Unconfigured: loopback is a developer on their own machine.
		{"localhost", "", "localhost:4000", false, "http://localhost:4000", true},
		{"127.0.0.1", "", "127.0.0.1:4000", false, "http://127.0.0.1:4000", true},
		{"127.0.0.1 no port", "", "127.0.0.1", false, "http://127.0.0.1", true},
		{"other 127/8 address", "", "127.9.9.9:80", false, "http://127.9.9.9:80", true},
		{"ipv6 loopback", "", "[::1]:4000", false, "http://[::1]:4000", true},
		{"loopback over tls", "", "localhost:4443", true, "https://localhost:4443", true},

		// Unconfigured: everything else is somebody else's name.
		{"public host", "", "example.com", false, "", false},
		{"spoofed host", "", "attacker.example", false, "", false},
		{"public host over tls", "", "example.com", true, "", false},
		{"empty host", "", "", false, "", false},
		// Near-misses that must not read as loopback.
		{"localhost as a subdomain", "", "localhost.attacker.example", false, "", false},
		{"host merely containing localhost", "", "notlocalhost", false, "", false},
		{"public ip", "", "203.0.113.5:80", false, "", false},
		// 127.0.0.1 in the userinfo/prefix position of a hostname.
		{"127.0.0.1 as a label", "", "127.0.0.1.attacker.example", false, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &server{deps: Deps{SiteURL: tc.siteURL, AdminPath: "/admin"}}
			r := httptest.NewRequest("POST", "/admin/forgot-password", nil)
			r.Host = tc.host
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			base, ok := s.emailBaseURL(r)
			if ok != tc.wantOK || base != tc.wantBase {
				t.Errorf("emailBaseURL() = (%q, %v), want (%q, %v)", base, ok, tc.wantBase, tc.wantOK)
			}
		})
	}
}

// X-Forwarded-Proto is set by whoever sends the request unless a proxy
// overwrites it, and the only branch that reads the request at all is the
// one for localhost, which has no proxy in front of it. So it is ignored.
func TestEmailBaseURLIgnoresForwardedProto(t *testing.T) {
	s := &server{deps: Deps{AdminPath: "/admin"}}
	r := httptest.NewRequest("POST", "/admin/forgot-password", nil)
	r.Host = "localhost:4000"
	r.Header.Set("X-Forwarded-Proto", "https")

	base, ok := s.emailBaseURL(r)
	if !ok || base != "http://localhost:4000" {
		t.Errorf("emailBaseURL() = (%q, %v), want (\"http://localhost:4000\", true)", base, ok)
	}
}

// Deps.SiteBaseURL falls back to the request's own Host, which is fine
// for a link rendered back to the person who sent the request and wrong
// for one that goes in an email. emailBaseURL must not reach for it.
func TestEmailBaseURLDoesNotUseSiteBaseURL(t *testing.T) {
	s := &server{deps: Deps{
		AdminPath:   "/admin",
		SiteBaseURL: func(r *http.Request) string { return "https://" + r.Host },
	}}
	r := httptest.NewRequest("POST", "/admin/forgot-password", nil)
	r.Host = "attacker.example"

	if base, ok := s.emailBaseURL(r); ok {
		t.Errorf("emailBaseURL() = (%q, true) via SiteBaseURL; want no base", base)
	}
}
