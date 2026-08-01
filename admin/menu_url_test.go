package admin

import "testing"

// validMenuURL is mirrored by a regex in editor/src/menu.js, which rejects
// a bad address before the request is made. The two must agree: a value
// only the client accepts reaches the server and fails there instead, and
// a value only the server accepts cannot be entered at all.
func TestValidMenuURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		// Site-relative paths.
		{"/", true},
		{"/contact", true},
		{"/blog/launch-day", true},
		{"/#pricing", true}, // path plus anchor: works from any page

		// Absolute, in the schemes a menu legitimately links to.
		{"https://example.com", true},
		{"http://example.com", true},
		{"mailto:sales@example.com", true},
		{"tel:5064596832", true},

		// Same-page anchors: a one-page site's nav is made of these.
		{"#pricing", true},
		{"#inventory", true},
		{"#a", true},

		// A bare "#" links nowhere — the half-filled field, not an anchor.
		{"#", false},

		// Neither a path nor a scheme we accept.
		{"", false},
		{"contact", false},
		{"example.com", false},
		{"javascript:alert(1)", false},
		{"data:text/html,<script>alert(1)</script>", false},
		{"ftp://example.com", false},
		{" /leading-space", false},
	}
	for _, tc := range cases {
		if got := validMenuURL(tc.url); got != tc.want {
			t.Errorf("validMenuURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// A menu item pointing at a page carries no URL, and one pointing at a
// URL carries no page; both shapes have to survive menuItemInput.
func TestMenuItemInputAcceptsAnchor(t *testing.T) {
	s := &server{deps: Deps{Locales: []string{"en"}, DefaultLocale: "en"}}

	in, msg := s.menuItemInput(menuItemJSON{Label: "Pricing", URL: "#pricing"})
	if msg != "" {
		t.Fatalf("anchor URL rejected: %s", msg)
	}
	if in.URL != "#pricing" {
		t.Errorf("URL = %q, want #pricing", in.URL)
	}
	if in.PageID != nil {
		t.Errorf("PageID = %v, want nil for a custom link", in.PageID)
	}

	if _, msg := s.menuItemInput(menuItemJSON{Label: "Nowhere", URL: "#"}); msg == "" {
		t.Error("a bare # was accepted; it links nowhere")
	}
}
