package cms

import "testing"

func TestValidateLocales(t *testing.T) {
	for _, ok := range [][]string{{"en"}, {"en", "fr"}, {"en", "fr", "pt-br"}} {
		if err := validateLocales(ok); err != nil {
			t.Errorf("validateLocales(%v): unexpected error %v", ok, err)
		}
	}
	for _, bad := range [][]string{{"EN"}, {"english"}, {"en", "en"}, {"f"}, {"fr_CA"}} {
		if err := validateLocales(bad); err == nil {
			t.Errorf("validateLocales(%v): expected error", bad)
		}
	}
}

func TestSplitLocalePath(t *testing.T) {
	c := &CMS{cfg: Config{Locales: []string{"en", "fr"}}}
	cases := []struct {
		in, locale, slug string
	}{
		{"", "en", ""},
		{"about", "en", "about"},
		{"fr", "fr", ""},
		{"fr/about", "fr", "about"},
		{"fr/blog/hello", "fr", "blog/hello"},
		{"fresh-start", "en", "fresh-start"}, // prefix must be a whole segment
		{"en/about", "en", "en/about"},       // default locale is never a prefix
		{"de/about", "en", "de/about"},       // unconfigured locale is a plain slug
	}
	for _, tc := range cases {
		locale, slug := c.splitLocalePath(tc.in)
		if locale != tc.locale || slug != tc.slug {
			t.Errorf("splitLocalePath(%q) = (%q, %q), want (%q, %q)",
				tc.in, locale, slug, tc.locale, tc.slug)
		}
	}

	// Single-locale sites never treat a segment as a locale prefix.
	single := &CMS{cfg: Config{Locales: []string{"en"}}}
	if locale, slug := single.splitLocalePath("fr/about"); locale != "en" || slug != "fr/about" {
		t.Errorf("single-locale splitLocalePath: got (%q, %q)", locale, slug)
	}
}
