package admin

// clientIP decides which bucket a failed login lands in, so getting it
// wrong breaks the throttle in one of two ways: everyone shares a bucket
// (no proxy header configured behind a proxy), or everyone gets their own
// on demand (a header trusted that the client can set).

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		header     string // Deps.ClientIPHeader
		remoteAddr string
		set        map[string]string
		want       string
	}{
		// Unconfigured: the connection, and only the connection. A header
		// nobody asked to trust must not move the answer.
		{"connection address", "", "203.0.113.7:5555", nil, "203.0.113.7"},
		{"header ignored when unconfigured", "", "203.0.113.7:5555",
			map[string]string{"X-Forwarded-For": "198.51.100.1"}, "203.0.113.7"},
		{"ipv6 connection", "", "[2001:db8::1]:443", nil, "2001:db8::1"},
		{"remote addr without a port", "", "203.0.113.7", nil, "203.0.113.7"},

		// Configured: the named header wins.
		{"single value header", "CF-Connecting-IP", "10.0.0.1:5555",
			map[string]string{"CF-Connecting-IP": "198.51.100.1"}, "198.51.100.1"},
		{"xff single hop", "X-Forwarded-For", "10.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "198.51.100.1"}, "198.51.100.1"},

		// The rightmost entry is the one the nearest proxy appended.
		// Everything left of it is what the client claimed, so a client
		// prepending addresses must not be able to move the answer —
		// otherwise the per-source counter is one they can reset at will.
		{"xff client prepends", "X-Forwarded-For", "10.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "1.2.3.4, 5.6.7.8, 198.51.100.1"}, "198.51.100.1"},
		{"xff spacing", "X-Forwarded-For", "10.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "1.2.3.4,198.51.100.1"}, "198.51.100.1"},
		{"xff padded", "X-Forwarded-For", "10.0.0.1:5555",
			map[string]string{"X-Forwarded-For": "  198.51.100.1  "}, "198.51.100.1"},

		// A proxy that did not set it, a health check that bypassed the
		// proxy: fall back to the connection rather than to a blank key,
		// which would put every such request in one bucket.
		{"header absent", "X-Forwarded-For", "203.0.113.7:5555", nil, "203.0.113.7"},
		{"header empty", "X-Forwarded-For", "203.0.113.7:5555",
			map[string]string{"X-Forwarded-For": ""}, "203.0.113.7"},
		{"header all spaces", "X-Forwarded-For", "203.0.113.7:5555",
			map[string]string{"X-Forwarded-For": "   "}, "203.0.113.7"},
		{"xff trailing comma", "X-Forwarded-For", "203.0.113.7:5555",
			map[string]string{"X-Forwarded-For": "1.2.3.4,"}, "203.0.113.7"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &server{deps: Deps{ClientIPHeader: tc.header}}
			r := httptest.NewRequest("POST", "/admin/login", nil)
			r.RemoteAddr = tc.remoteAddr
			for k, v := range tc.set {
				r.Header.Set(k, v)
			}
			if got := s.clientIP(r); got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}
