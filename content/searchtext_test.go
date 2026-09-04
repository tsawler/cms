package content_test

import (
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
)

func TestSearchTextStripsMarkupWithoutGluingWordsTogether(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"adjacent blocks stay separate words",
			"<p>one</p><p>two</p>", "one two"},
		{"attributes are not words",
			`<div class="hero" data-x="banner"><p>Welcome</p></div>`, "Welcome"},
		{"entities are decoded",
			"<p>Ship it back &amp; we refund. Caf&eacute;</p>", "Ship it back & we refund. Café"},
		{"whitespace collapses",
			"<p>a\n\n\tb   c</p>", "a b c"},
		{"inline tags do not split a word",
			"<p>hy<em>phen</em>ated</p>", "hy phen ated"},
		{"empty input",
			"", ""},
		{"a custom-code placeholder holds no text",
			`<div data-cms-code="pricing"></div>`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := content.SearchText(tt.in); got != tt.want {
				t.Errorf("SearchText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The point of using bluemonday rather than a tag-stripping regexp: the
// contents of these elements are never read by a visitor, so indexing them
// would put a page's own JavaScript into the site's search results.
func TestSearchTextDropsScriptAndStyleContents(t *testing.T) {
	in := `<p>before</p><script>var token = "secretpassword";</script>` +
		`<style>.hero{color:magenta}</style><p>after</p>`
	got := content.SearchText(in)
	if got != "before after" {
		t.Errorf("SearchText = %q, want %q", got, "before after")
	}
	for _, leaked := range []string{"secretpassword", "magenta", "var", "color"} {
		if strings.Contains(got, leaked) {
			t.Errorf("SearchText leaked %q from an invisible element: %q", leaked, got)
		}
	}
}

func TestSnippetCentersOnTheMatch(t *testing.T) {
	body := strings.Join([]string{
		"one two three four five six seven eight nine ten",
		"eleven twelve thirteen fourteen fifteen sixteen",
		"needle seventeen eighteen nineteen twenty",
		"twentyone twentytwo twentythree twentyfour twentyfive",
	}, " ")
	got := content.Snippet(body, content.ParseSearchQuery("needle"), 6)
	if !strings.Contains(got, "needle") {
		t.Fatalf("snippet does not hold the match: %q", got)
	}
	if !strings.HasPrefix(got, "… ") || !strings.HasSuffix(got, " …") {
		t.Errorf("a snippet cut at both ends should say so: %q", got)
	}
	if n := len(strings.Fields(strings.Trim(got, "… "))); n != 6 {
		t.Errorf("snippet holds %d words, want 6: %q", n, got)
	}
}

func TestSnippetOpensTheTextWhenNothingMatches(t *testing.T) {
	body := "alpha beta gamma delta epsilon zeta eta theta"
	// A title-only match: the word is nowhere in the body, so the opening
	// words stand in rather than nothing at all.
	got := content.Snippet(body, content.ParseSearchQuery("missing"), 3)
	if got != "alpha beta gamma …" {
		t.Errorf("Snippet = %q, want the opening words", got)
	}
}

func TestSnippetDoesNotRunPastEitherEnd(t *testing.T) {
	// A match in the first words must still show a full window, taken
	// forwards rather than off the front of the text.
	got := content.Snippet("needle two three four five six", content.ParseSearchQuery("needle"), 4)
	if strings.HasPrefix(got, "…") {
		t.Errorf("a snippet starting at the beginning should not claim to be cut: %q", got)
	}
	if n := len(strings.Fields(strings.Trim(got, "… "))); n != 4 {
		t.Errorf("snippet holds %d words, want 4: %q", n, got)
	}
	// And a short body is returned whole, with no ellipsis at either end.
	if got := content.Snippet("just a few words", content.ParseSearchQuery("few"), 30); got != "just a few words" {
		t.Errorf("Snippet = %q, want the whole body", got)
	}
}
