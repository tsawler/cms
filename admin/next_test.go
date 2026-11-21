package admin

import "testing"

// safeNext is the only thing between the login page and an open
// redirect, so every way a browser can be talked into leaving the site
// from a Location header gets a row here.
func TestSafeNext(t *testing.T) {
	s := &server{deps: Deps{AdminPath: "/admin"}}
	for _, tc := range []struct {
		in, want string
	}{
		// Paths on this site come back as they are.
		{"/", "/"},
		{"/health-and-safety", "/health-and-safety"},
		{"/fr/a-propos?tab=2#forms", "/fr/a-propos?tab=2#forms"},
		{"/admin/users", "/admin/users"},
		{"/storage/files/SOP%27s.pdf", "/storage/files/SOP%27s.pdf"},

		// Another host, however it is spelled.
		{"https://evil.example/", ""},
		{"http://evil.example", ""},
		{"//evil.example", ""},
		{"///evil.example", ""},
		{"/\\evil.example", ""},
		{"\\\\evil.example", ""},
		{"/x\\..\\..\\evil", ""},
		{"javascript:alert(1)", ""},
		{"data:text/html,hi", ""},
		{"evil.example", ""},
		{"https:/evil.example", ""},

		// Characters browsers strip or that end a header.
		{"/\t/evil.example", ""},
		{"/\n/evil.example", ""},
		{"/\r\nSet-Cookie: x=1", ""},
		{"/ /evil.example", ""},
		{" /about", ""},
		{"/about\x00", ""},
		{"/about\x7f", ""},

		// The login flow itself: harmless, but a loop.
		{"/admin/login", ""},
		{"/admin/login/2fa", ""},
		{"/admin/logout", ""},
		{"/admin/reset-password?token=x", ""},
		{"/admin/loginx", "/admin/loginx"},

		{"", ""},
		{"/" + string(make([]byte, maxNextLen)), ""},
	} {
		if got := s.safeNext(tc.in); got != tc.want {
			t.Errorf("safeNext(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
