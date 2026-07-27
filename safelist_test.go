package cms

import (
	"os"
	"regexp"
	"testing"

	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

var classAttrRe = regexp.MustCompile(`class="([^"]*)"`)
var classSplitRe = regexp.MustCompile(`\s+`)

// TestReadmeListsDefaultClasses guards the README's Tailwind safelist
// blocks against drift: every class the default editor styles, snippets,
// section presets, and section styles can put into the database must
// appear in README.md, or a production build that follows the docs will
// silently drop styling the first time that content renders. When this
// fails, add the missing class to the matching safelist block in the
// README (marker classes like cms-btn are exempt — they aren't Tailwind
// utilities).
func TestReadmeListsDefaultClasses(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}

	seen := map[string]bool{}
	var classes []string
	add := func(list string) {
		for _, c := range classSplitRe.Split(list, -1) {
			if c == "" || seen[c] {
				continue
			}
			seen[c] = true
			classes = append(classes, c)
		}
	}

	for _, sn := range append(snippets.DefaultSnippets(), snippets.DefaultSectionPresets()...) {
		for _, m := range classAttrRe.FindAllStringSubmatch(sn.HTML, -1) {
			add(m[1])
		}
	}
	for _, st := range render.DefaultEditorStyles() {
		add(st.Class)
	}
	styles := render.DefaultSectionStyles()
	for _, o := range append(append(styles.Backgrounds, styles.Widths...), styles.Corners...) {
		add(o.Class)
		add(o.ContentClass)
	}

	for _, c := range classes {
		if regexp.MustCompile(`^cms-`).MatchString(c) {
			continue // CMS marker classes; styled by {{cmsHead}} or the editor, not Tailwind
		}
		// The class must appear as its own token (quoted in a JS safelist
		// or space-separated in a v4 @source inline string) — a substring
		// hit inside a longer class name doesn't count.
		re := regexp.MustCompile(`[\s"']` + regexp.QuoteMeta(c) + `[\s"',]`)
		if !re.Match(readme) {
			t.Errorf("class %q (used by a default style, snippet, or section preset) is missing from the README safelists", c)
		}
	}
}
