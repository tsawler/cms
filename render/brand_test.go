package render

import (
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
)

// {{cmsBrand}} is the one piece of the host's header the CMS owns, and the
// editor's site-settings dialog swaps its innards in place after a save —
// so the wrapper, its classes, and the stashed fallback are a contract,
// not just markup. The fallback rides in data-cms-default precisely so the
// editor can put it back when a name and logo are both cleared.
func TestBrandHTML(t *testing.T) {
	cases := []struct {
		name     string
		site     content.SiteSettings
		fallback []string
		want     []string
		absent   []string
	}{
		{
			name: "name only",
			site: content.SiteSettings{SiteName: "Acme Ltd"},
			want: []string{`<span class="cms-brand">`, `<span class="cms-brand-text">Acme Ltd</span>`},
			// Nothing was saved for a logo, so no <img> is emitted at all
			// rather than one with an empty src.
			absent: []string{"cms-brand-logo", "data-cms-default"},
		},
		{
			name:   "logo only",
			site:   content.SiteSettings{LogoURL: "/media/logo.png"},
			want:   []string{`<img class="cms-brand-logo" src="/media/logo.png" alt="">`},
			absent: []string{"cms-brand-text"},
		},
		{
			name: "logo and name",
			site: content.SiteSettings{SiteName: "Acme Ltd", LogoURL: "/media/logo.png"},
			// The name doubles as the logo's alt text: one saved value,
			// both jobs.
			want: []string{`alt="Acme Ltd"`, `<span class="cms-brand-text">Acme Ltd</span>`},
		},
		{
			name:     "fallback until something is saved",
			site:     content.SiteSettings{},
			fallback: []string{"My Site"},
			want: []string{`data-cms-default="My Site"`,
				`<span class="cms-brand-text">My Site</span>`},
		},
		{
			name:     "saved name wins over the fallback",
			site:     content.SiteSettings{SiteName: "Acme Ltd"},
			fallback: []string{"My Site"},
			// The fallback stays in the data attribute even when it is not
			// showing — that is what the editor restores from.
			want:   []string{`data-cms-default="My Site"`, `>Acme Ltd</span>`},
			absent: []string{">My Site</span>"},
		},
		{
			name:     "a logo alone still labels itself from the fallback",
			site:     content.SiteSettings{LogoURL: "/media/logo.png"},
			fallback: []string{"My Site"},
			// A logo with no saved name would otherwise be an unlabelled
			// image in the header.
			want:   []string{`alt="My Site"`},
			absent: []string{"cms-brand-text"},
		},
		{
			name:   "nothing saved and no fallback",
			site:   content.SiteSettings{},
			want:   []string{`<span class="cms-brand"></span>`},
			absent: []string{"cms-brand-text", "cms-brand-logo"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(brandHTML(c.site, c.fallback))
			for _, want := range c.want {
				if !strings.Contains(got, want) {
					t.Errorf("output %q is missing %q", got, want)
				}
			}
			for _, absent := range c.absent {
				if strings.Contains(got, absent) {
					t.Errorf("output %q contains %q", got, absent)
				}
			}
			if !strings.HasSuffix(got, "</span>") {
				t.Errorf("output %q is not closed", got)
			}
		})
	}
}

// Both values are site settings an admin types, and both land in markup
// the renderer marks safe — so the escaping here is the only thing between
// a site name and script in every page's header.
func TestBrandHTMLEscapes(t *testing.T) {
	site := content.SiteSettings{
		SiteName: `Acme" <script>alert(1)</script>`,
		LogoURL:  "/media/logo.png",
	}
	got := string(brandHTML(site, []string{`fall"back<`}))

	for _, raw := range []string{"<script>", `Acme" `, `fall"back<`} {
		if strings.Contains(got, raw) {
			t.Errorf("output %q carries %q unescaped", got, raw)
		}
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("output %q does not escape the name", got)
	}
	if !strings.Contains(got, "&#34;") {
		t.Errorf("output %q does not escape the quote that would break out of alt=", got)
	}
}

// A logo URL is a stored string that becomes a src, so it goes through
// ValidBackgroundURL first. A rejected one leaves the brand with whatever
// else it has rather than emitting a javascript: image source.
func TestBrandHTMLRejectsAnUnsafeLogoURL(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,<script>", "logo.png"} {
		site := content.SiteSettings{SiteName: "Acme Ltd", LogoURL: bad}
		got := string(brandHTML(site, nil))
		if strings.Contains(got, "cms-brand-logo") {
			t.Errorf("logo %q was rendered: %q", bad, got)
		}
		if !strings.Contains(got, ">Acme Ltd</span>") {
			t.Errorf("logo %q took the name down with it: %q", bad, got)
		}
	}
}
