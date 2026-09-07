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
		"About Us":      "about-us",
		"Café & Bar!":   "cafe-bar",
		"  Über uns  ":  "uber-uns",
		"FAQ":           "faq",
		"Prix / Tarifs": "prix-tarifs",
		"!!!":           "",
		"Services—2026": "services-2026",
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

// Visibility is access control, not workflow: a private page publishes
// like any other and is then served only to people who are logged in. The
// admin form and the metadata API both gate on this before storing what a
// request sent, so anything it lets through is a value the renderer has to
// understand.
func TestValidVisibility(t *testing.T) {
	for _, s := range []string{"public", "private"} {
		if !ValidVisibility(s) {
			t.Errorf("ValidVisibility(%q) = false, want true", s)
		}
	}
	// "" is not valid on the way in — orPublic fills it in for callers
	// that never set the field, which is a different thing from accepting
	// an empty value off a form.
	for _, s := range []string{"", "Public", "PRIVATE", "hidden", "public ", "draft"} {
		if ValidVisibility(s) {
			t.Errorf("ValidVisibility(%q) = true, want false", s)
		}
	}
	// The constants are what the rest of the package compares against, so
	// they must be the strings the check admits.
	if !ValidVisibility(string(VisibilityPublic)) || !ValidVisibility(string(VisibilityPrivate)) {
		t.Error("a Visibility constant is not accepted by ValidVisibility")
	}
}

// A page that never set the field is public, so an old row and a new one
// are served the same way.
func TestVisibilityOrPublic(t *testing.T) {
	if got := Visibility("").orPublic(); got != VisibilityPublic {
		t.Errorf("empty visibility = %q, want %q", got, VisibilityPublic)
	}
	if got := VisibilityPrivate.orPublic(); got != VisibilityPrivate {
		t.Errorf("private visibility = %q, want %q", got, VisibilityPrivate)
	}
}
