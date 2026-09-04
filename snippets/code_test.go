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

func TestCodeKeysIn(t *testing.T) {
	tests := []struct {
		name string
		html string
		want []string
	}{
		{"none", `<p>Just words</p>`, nil},
		{"one placeholder", `<div class="cms-snippet cms-code" data-cms-code="signup"></div>`,
			[]string{"signup"}},
		{"two, in order", `<div data-cms-code="map"></div><p>x</p><div data-cms-code="chart"></div>`,
			[]string{"map", "chart"}},
		// The same block used twice on a page is one dependency.
		{"deduplicated", `<div data-cms-code="map"></div><div data-cms-code="map"></div>`,
			[]string{"map"}},
		// A reference with something in it is still a reference: the
		// renderer only expands empty placeholders, but a page that has
		// been filled in and re-saved still depends on the block.
		{"non-empty body", `<div data-cms-code="signup"><button>Go</button></div>`,
			[]string{"signup"}},
		// Anything outside the key vocabulary is not a key.
		{"invalid key", `<div data-cms-code="Not A Key"></div>`, nil},
		{"empty key", `<div data-cms-code=""></div>`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := snippets.CodeKeysIn(tc.html)
			if len(got) != len(tc.want) {
				t.Fatalf("CodeKeysIn = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("CodeKeysIn = %v, want %v", got, tc.want)
					break
				}
			}
		})
	}
}
