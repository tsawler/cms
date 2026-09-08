package sqldb

import "testing"

// EscapeLike is what keeps a search box from being a way to match
// everything. It had grown three copies — in auth, content and media —
// which is how a rule like this quietly stops being one rule.
func TestEscapeLike(t *testing.T) {
	tests := map[string]string{
		"":           "",
		"plain":      "plain",
		"100%":       `100\%`,
		"a_b":        `a\_b`,
		`back\slash`: `back\\slash`,
		"%_%":        `\%\_\%`,
		// The backslash is escaped first, so an escape the caller typed
		// does not turn into an escape of ours.
		`\%`: `\\\%`,
		// Everything else is left alone: this is a LIKE escaper, not a
		// sanitiser, and the value still goes to the driver as a bound
		// parameter.
		"O'Brien <tag>": "O'Brien <tag>",
	}
	for in, want := range tests {
		if got := EscapeLike(in); got != want {
			t.Errorf("EscapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}
