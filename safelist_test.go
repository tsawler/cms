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

// TestDocsListDefaultClasses guards the Tailwind safelist blocks in both
// guides against drift: every class the default editor styles, snippets,
// section presets, and section styles can put into the database must appear
// in each, or a production build that follows the docs will silently drop
// styling the first time that content renders.
//
// Both files carry the list rather than one delegating to the other — a
// reader following the quickstart should not have to cross-reference the
// README to get a correct build — which is exactly why this has to be
// checked mechanically. When it fails, add the missing class to the
// safelist blocks in every file listed here (marker classes like cms-btn
// are exempt — they aren't Tailwind utilities).
func TestDocsListDefaultClasses(t *testing.T) {
	docs := map[string][]byte{}
	for _, name := range []string{"README.md", "QUICKSTART.md"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		docs[name] = b
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

	defaults := append(snippets.DefaultSnippets(), snippets.LibrarySnippets()...)
	defaults = append(defaults, snippets.DefaultSectionPresets()...)
	defaults = append(defaults, snippets.LibrarySectionPresets()...)
	for _, sn := range defaults {
		for _, m := range classAttrRe.FindAllStringSubmatch(sn.HTML, -1) {
			add(m[1])
		}
	}
	for _, st := range render.DefaultEditorStyles() {
		add(st.Class)
	}
	// Classes the editor writes on its own — no snippet carries them, so
	// nothing above would have collected them.
	for _, cls := range render.EditorAppliedClasses() {
		add(cls)
	}
	styles := render.DefaultSectionStyles()
	// Every curated axis, Paddings and Sizes included — the spacing and
	// text-size classes are as invisible to a content scan as the widths
	// are.
	sectionOptions := append(append(styles.Backgrounds, styles.Widths...), styles.Corners...)
	sectionOptions = append(sectionOptions, styles.Paddings...)
	sectionOptions = append(sectionOptions, styles.Sizes...)
	for _, o := range sectionOptions {
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
		for name, doc := range docs {
			if !re.Match(doc) {
				t.Errorf("class %q (used by a default style, snippet, or section preset) is missing from the safelists in %s", c, name)
			}
		}
	}
}
