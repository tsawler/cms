package dialect_test

import (
	"testing"

	"github.com/tsawler/cms/internal/dialect"
)

// Boolean mode reads a hyphen inside a word as an operator: +well-known is
// +well -known, which excludes every page saying "known". Such terms, and
// ones with an apostrophe, go to the engine as phrases.
func TestMySQLSearchQueryQuotesJoinedWords(t *testing.T) {
	got := dialect.MySQL{}.SearchQuery([]dialect.SearchTerm{
		{Text: "well-known"},
		{Text: "don't"},
		{Text: "plain"},
		{Text: "e-mail", Exclude: true},
		{Text: "two words", Phrase: true},
	})
	want := `+"well-known" +"don't" +plain -"e-mail" +"two words"`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
