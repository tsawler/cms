package content

import "testing"

func TestNormalizeSlug(t *testing.T) {
	cases := map[string]string{
		"/About-Us/":   "about-us",
		"  news/2026 ": "news/2026",
		"/":            "",
		"":             "",
	}
	for in, want := range cases {
		if got := NormalizeSlug(in); got != want {
			t.Errorf("NormalizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"About Us":        "about-us",
		"Café & Bar!":     "cafe-bar",
		"  Über uns  ":    "uber-uns",
		"FAQ":             "faq",
		"Prix / Tarifs":   "prix-tarifs",
		"!!!":             "",
		"Services—2026":   "services-2026",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	for _, s := range []string{Slugify("About Us"), Slugify("Café & Bar!")} {
		if s != "" && !ValidSlug(s) {
			t.Errorf("Slugify output %q is not a valid slug", s)
		}
	}
}

func TestValidSlug(t *testing.T) {
	valid := []string{"", "about", "about-us", "news/2026/launch", "a1-b2"}
	invalid := []string{"About", "a b", "a_b", "a//b", "/about", "about/", "café"}
	for _, s := range valid {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true, want false", s)
		}
	}
}
