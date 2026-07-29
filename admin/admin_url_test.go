package admin

import "testing"

// TestAbs covers the value behind "Copy link": it must be pastable
// somewhere else, without mangling media that already lives on its own
// domain.
func TestAbs(t *testing.T) {
	for _, c := range []struct{ name, base, in, want string }{
		{"site-relative gets the base", "https://example.com",
			"/cms/media/abc/web.webp", "https://example.com/cms/media/abc/web.webp"},
		{"relative without a slash", "https://example.com",
			"cms/media/abc/web.webp", "https://example.com/cms/media/abc/web.webp"},
		// An S3 PublicBaseURL or a public bucket already serves absolute
		// URLs; prefixing those would break them.
		{"already absolute is untouched", "https://example.com",
			"https://cdn.example.net/media/abc.webp", "https://cdn.example.net/media/abc.webp"},
		{"protocol-relative is untouched", "https://example.com",
			"//cdn.example.net/media/abc.webp", "//cdn.example.net/media/abc.webp"},
		// No base configured and no request origin: leave links alone
		// rather than inventing a host.
		{"no base leaves it relative", "", "/cms/media/abc/web.webp", "/cms/media/abc/web.webp"},
		{"empty url stays empty", "https://example.com", "", ""},
	} {
		td := templateData{SiteBase: c.base}
		if got := td.Abs(c.in); got != c.want {
			t.Errorf("%s: Abs(%q) with base %q = %q, want %q", c.name, c.in, c.base, got, c.want)
		}
	}
}
