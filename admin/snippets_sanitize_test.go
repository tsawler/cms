package admin

import (
	"html"
	"strings"
	"testing"

	"github.com/tsawler/cms/snippets"
)

// TestMapEmbedSrcs pins the sanitizer's Google Maps iframe allowance to
// the two URL shapes the map slot emits (editor/src/maps.js), and only
// those — a foreign host or a query without output=embed is stripped.
func TestMapEmbedSrcs(t *testing.T) {
	keep := []string{
		"https://www.google.com/maps/embed?pb=!1m18!2m3!4f13.1",
		"https://www.google.com/maps?q=123%20Main%20Street%2C%20Halifax&output=embed",
		"https://maps.google.com/maps?q=44.65%2C-63.57&output=embed",
	}
	drop := []string{
		"https://evil.example.com/maps?q=x&output=embed",
		"https://www.google.com/maps?q=missing-the-embed-flag",
		"https://www.google.com/mapsx?q=x&output=embed",
	}
	for _, src := range keep {
		in := `<iframe class="w-full aspect-video rounded-lg" src="` + src + `" title="Map" loading="lazy"></iframe>`
		if got := editorHTMLPolicy.Sanitize(in); !strings.Contains(got, "src=") {
			t.Errorf("map src %q was stripped: %s", src, got)
		}
	}
	for _, src := range drop {
		in := `<iframe src="` + src + `" title="Map"></iframe>`
		if got := editorHTMLPolicy.Sanitize(in); strings.Contains(got, "src=") {
			t.Errorf("src %q should have been stripped: %s", src, got)
		}
	}
}

// TestDefaultSnippetsSurviveSanitizer guards the default and imported
// snippet libraries against the editor sanitizer: every block must come
// back from editorHTMLPolicy unchanged, or an editor-role save would
// silently strip parts of freshly inserted content. Two normalizations
// before comparing: entities are decoded on both sides (the sanitizer
// re-serializes &ldquo; and emoji references without changing what they
// mean), and the rel="nofollow" the UGC policy stamps on every link is
// dropped from the sanitized side.
func TestDefaultSnippetsSurviveSanitizer(t *testing.T) {
	all := append(snippets.DefaultSnippets(), snippets.LibrarySnippets()...)
	all = append(all, snippets.DefaultSectionPresets()...)
	all = append(all, snippets.LibrarySectionPresets()...)
	for _, sn := range all {
		got := strings.ReplaceAll(editorHTMLPolicy.Sanitize(sn.HTML), ` rel="nofollow"`, "")
		if html.UnescapeString(got) != html.UnescapeString(sn.HTML) {
			t.Errorf("snippet %q is changed by the editor sanitizer:\n got: %s\nwant: %s", sn.Name, got, sn.HTML)
		}
	}
}
