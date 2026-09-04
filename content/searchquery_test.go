package content_test

import (
	"testing"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dialect"
)

func terms(ts ...content.SearchTerm) []content.SearchTerm { return ts }

func word(s string) content.SearchTerm   { return content.SearchTerm{Text: s} }
func phrase(s string) content.SearchTerm { return content.SearchTerm{Text: s, Phrase: true} }
func not(t content.SearchTerm) content.SearchTerm {
	t.Exclude = true
	return t
}

func TestParseSearchQuery(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []content.SearchTerm
	}{
		{"words are terms", "opening hours", terms(word("opening"), word("hours"))},
		{"a quoted phrase is one term", `"opening hours"`, terms(phrase("opening hours"))},
		{"a leading minus excludes", "hours -weekend",
			terms(word("hours"), not(word("weekend")))},
		{"a minus excludes a phrase too", `hours -"bank holiday"`,
			terms(word("hours"), not(phrase("bank holiday")))},
		{"a minus inside a word is part of it", "e-mail", terms(word("e-mail"))},
		{"apostrophes survive", "l'entreprise", terms(word("l'entreprise"))},
		{"non-ASCII letters survive", "ελληνικά 日本語",
			terms(word("ελληνικά"), word("日本語"))},
		{"an unclosed quote runs to the end", `"opening hours`,
			terms(phrase("opening hours"))},
		{"punctuation is dropped, not passed on", "hours!!! (weekend) *",
			terms(word("hours"), word("weekend"))},
		{"a trailing hyphen is not kept", "hours- -", terms(word("hours"))},
		{"nothing to search for", "!!! ???", nil},
		{"only exclusions is not a search", "-cat -dog", nil},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := content.ParseSearchQuery(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseSearchQuery(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("term %d of %q = %+v, want %+v", i, tt.in, got[i], tt.want[i])
				}
			}
		})
	}
}

// A search box takes whatever is pasted into it. Nothing that comes out of
// the parser may carry a character either engine's query language reads as
// an operator, because the engines are handed these strings verbatim.
func TestParseSearchQueryStripsQueryLanguageOperators(t *testing.T) {
	// Postgres's websearch syntax, MySQL's boolean mode syntax, and the
	// characters that make either one a syntax error.
	nasty := `+cat -dog (a OR b) ~c "d* @2 <e >f 100% x_y \z ' " ; -- /*`
	for _, term := range content.ParseSearchQuery(nasty) {
		for _, bad := range []rune{'+', '(', ')', '~', '*', '@', '<', '>', '%', '_', '\\', '"', ';', ':', '&', '|', '!'} {
			for _, r := range term.Text {
				if r == bad {
					t.Errorf("term %q kept the operator character %q", term.Text, bad)
				}
			}
		}
	}
}

func TestParseSearchQueryBoundsWhatItAccepts(t *testing.T) {
	var long string
	for range 200 {
		long += "word "
	}
	if n := len(content.ParseSearchQuery(long)); n > 12 {
		t.Errorf("ParseSearchQuery returned %d terms, want at most 12", n)
	}
}

// The two engines are asked the same question in their own words. The
// shapes differ — boolean mode needs an explicit "+" where websearch
// defaults to AND — but the meaning must not.
func TestDialectsRenderTheSameQuery(t *testing.T) {
	parsed := content.ParseSearchQuery(`opening "bank holiday" -weekend`)
	if got, want := (dialect.Postgres{}).SearchQuery(parsed),
		`opening "bank holiday" -weekend`; got != want {
		t.Errorf("Postgres SearchQuery = %q, want %q", got, want)
	}
	if got, want := (dialect.MySQL{}).SearchQuery(parsed),
		`+opening +"bank holiday" -weekend`; got != want {
		t.Errorf("MySQL SearchQuery = %q, want %q", got, want)
	}
}
