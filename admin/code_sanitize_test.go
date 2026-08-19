package admin

import (
	"strings"
	"testing"
)

// TestCodePlaceholderSurvivesSanitizer is the load-bearing property of
// the whole custom-code design: an editor without the admin role saves
// through editorHTMLPolicy, and the reference to a code block has to
// come back intact. If it were stripped, an editor fixing a typo
// elsewhere in the section would silently delete an admin's widget.
func TestCodePlaceholderSurvivesSanitizer(t *testing.T) {
	in := `<p>before</p><div class="cms-snippet cms-code" data-cms-code="booking-widget"></div><p>after</p>`
	got := editorHTMLPolicy.Sanitize(in)
	if !strings.Contains(got, `data-cms-code="booking-widget"`) {
		t.Errorf("the code placeholder was stripped from a non-admin save:\n%s", got)
	}
	if !strings.Contains(got, "cms-code") {
		t.Errorf("the placeholder lost its classes:\n%s", got)
	}
}

// TestCodeKeyBounds keeps the allowance to the key vocabulary. The value
// is written back into an attribute and matched by a regex on the render
// side, so anything needing escaping — or long enough to be a payload
// rather than a name — must not get through.
func TestCodeKeyBounds(t *testing.T) {
	keep := []string{"a", "widget", "booking-widget-2", strings.Repeat("x", 64)}
	drop := []string{
		"",
		"-leading-hyphen",
		"Widget",
		"has space",
		"under_score",
		strings.Repeat("x", 65),
	}
	for _, key := range keep {
		in := `<div data-cms-code="` + key + `"></div>`
		if got := editorHTMLPolicy.Sanitize(in); !strings.Contains(got, "data-cms-code") {
			t.Errorf("key %q was stripped: %s", key, got)
		}
	}
	for _, key := range drop {
		in := `<div data-cms-code="` + key + `"></div>`
		if got := editorHTMLPolicy.Sanitize(in); strings.Contains(got, "data-cms-code") {
			t.Errorf("key %q should have been stripped: %s", key, got)
		}
	}
}

// TestCodeKeyCannotSmuggleAttributes covers the quote-breaking attempt:
// the parser reads it as a valid key plus a second attribute, and the
// second attribute is not one the policy allows.
func TestCodeKeyCannotSmuggleAttributes(t *testing.T) {
	in := `<div data-cms-code="x" onload="alert(1)"></div>`
	got := editorHTMLPolicy.Sanitize(in)
	if strings.Contains(got, "onload") || strings.Contains(got, "alert(1)") {
		t.Errorf("an event handler rode in beside the key: %s", got)
	}
}

// TestCodeAttributeIsDivOnly pins the attribute to the element the
// renderer looks for it on, so nothing else in a page can pretend to be
// a code block.
func TestCodeAttributeIsDivOnly(t *testing.T) {
	for _, el := range []string{"p", "span", "section", "a"} {
		in := "<" + el + ` data-cms-code="widget"></` + el + ">"
		if got := editorHTMLPolicy.Sanitize(in); strings.Contains(got, "data-cms-code") {
			t.Errorf("<%s> kept the code attribute: %s", el, got)
		}
	}
}

// TestScriptStillStrippedForEditors is the other half of the bargain:
// the placeholder is allowed through precisely because it carries
// nothing executable, and inline script must still not survive a
// non-admin save.
func TestScriptStillStrippedForEditors(t *testing.T) {
	in := `<div class="cms-code" data-cms-code="widget"><script>alert(1)</script></div>`
	got := editorHTMLPolicy.Sanitize(in)
	if strings.Contains(got, "alert(1)") || strings.Contains(got, "<script") {
		t.Errorf("script survived a non-admin save:\n%s", got)
	}
}
