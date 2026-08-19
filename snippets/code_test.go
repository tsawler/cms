package snippets_test

import (
	"strings"
	"testing"

	"github.com/tsawler/cms/snippets"
)

// TestValidCodeKey pins the vocabulary the render-side pattern and the
// sanitizer's allowance both repeat: lowercase letters, digits, and
// hyphens, starting with a letter or digit, 64 characters at most.
func TestValidCodeKey(t *testing.T) {
	ok := []string{"a", "9", "widget", "booking-widget-2", strings.Repeat("x", 64)}
	bad := []string{"", "-lead", "Widget", "has space", "under_score", `quote"`,
		strings.Repeat("x", 65)}
	for _, key := range ok {
		if !snippets.ValidCodeKey(key) {
			t.Errorf("ValidCodeKey(%q) = false, want true", key)
		}
	}
	for _, key := range bad {
		if snippets.ValidCodeKey(key) {
			t.Errorf("ValidCodeKey(%q) = true, want false", key)
		}
	}
}

// TestCodeKeyFor covers the name-to-key guess the create endpoint falls
// back on, including the names it cannot make anything of.
func TestCodeKeyFor(t *testing.T) {
	cases := map[string]string{
		"Booking widget":         "booking-widget",
		"  Spaced  out  ":        "spaced-out",
		"Pricing (2026!)":        "pricing-2026",
		"Ça va":                  "a-va",
		"":                       "",
		"!!!":                    "",
		strings.Repeat("ab", 40): strings.Repeat("ab", 32),
	}
	for name, want := range cases {
		if got := snippets.CodeKeyFor(name); got != want {
			t.Errorf("CodeKeyFor(%q) = %q, want %q", name, got, want)
		}
	}
	// Whatever it produces must be usable as a key.
	for _, name := range []string{"Booking widget", "Pricing (2026!)", strings.Repeat("ab", 40)} {
		if key := snippets.CodeKeyFor(name); !snippets.ValidCodeKey(key) {
			t.Errorf("CodeKeyFor(%q) produced the unusable key %q", name, key)
		}
	}
}
