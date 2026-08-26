package snippets

import (
	"strings"
	"testing"
)

// find returns the named snippet from a library, or fails.
func find(t *testing.T, lib []Snippet, name string) Snippet {
	t.Helper()
	for _, s := range lib {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no snippet named %q", name)
	return Snippet{}
}

func TestFAQPresetIsCollapsible(t *testing.T) {
	// The preset used to be a run of <h3>/<p>, which reads as a wall of
	// text once a site has twenty questions. It is a disclosure list now.
	faq := find(t, DefaultSectionPresets(), "FAQ")
	if n := strings.Count(faq.HTML, "<details"); n != 3 {
		t.Errorf("FAQ preset has %d questions, want 3", n)
	}
	if !strings.Contains(faq.HTML, "<summary>") {
		t.Error("a <details> with no <summary> has nothing to click")
	}
	if strings.Contains(faq.HTML, "<h3") {
		t.Error("the old heading-and-paragraph shape is still in the preset")
	}
}

func TestQuestionSnippetMatchesThePreset(t *testing.T) {
	// The whole point of the inline snippet is that an editor can grow an
	// accordion: put the caret after the last answer, insert one more.
	// If its markup drifted from the preset's, a hand-added question
	// would look different from the three that came with the section.
	item := find(t, DefaultSnippets(), "Question & answer")
	faq := find(t, DefaultSectionPresets(), "FAQ")
	if !strings.Contains(item.HTML, faqItemHTML) {
		t.Error("the inline snippet is not built from faqItemHTML")
	}
	if !strings.Contains(faq.HTML, faqItemHTML) {
		t.Error("the section preset is not built from faqItemHTML")
	}
}

func TestFAQItemsAreClosedByDefault(t *testing.T) {
	// A page of questions that all start expanded is a page of answers,
	// which is what the accordion was there to avoid.
	if strings.Contains(faqItemHTML, "open") {
		t.Error("faqItemHTML ships expanded")
	}
}

func TestFAQMarkupCarriesNoAppearance(t *testing.T) {
	// Like the nav, the module emits structure and the host styles it.
	// A colour or a size here would be one the host has to override.
	for _, bad := range []string{"style=", "text-", "bg-", "border-", "font-"} {
		if strings.Contains(faqItemHTML, bad) {
			t.Errorf("faqItemHTML carries %q — appearance belongs to the host", bad)
		}
	}
}

func TestFAQBodyIsAddressable(t *testing.T) {
	// The host needs a hook for the answer specifically — Typography puts
	// top margin on the first child of anything, which inside a
	// disclosure strands the answer below its question.
	if !strings.Contains(faqItemHTML, `class="cms-faq-body"`) {
		t.Error("the answer has no class for a host to address")
	}
}
