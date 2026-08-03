package render

import (
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
)

// styles mirrors a host that has moved its vertical padding out of the
// width presets and into the spacing axis.
func paddingStyles() *SectionStyles {
	return &SectionStyles{
		Backgrounds: []SectionOption{{Key: "default", Label: "Default", Class: ""}},
		Widths:      []SectionOption{{Key: "wide", Label: "Wide", Class: "mx-auto max-w-5xl px-6"}},
		Paddings: []SectionOption{
			{Key: "normal", Label: "Normal", Class: "py-20"},
			{Key: "tight", Label: "Tight", Class: "py-3"},
		},
	}
}

func TestSectionPadding(t *testing.T) {
	r := &Renderer{sections: paddingStyles()}

	// A block saved before the axis existed carries no padding key, and
	// must render exactly as it always did — which is what makes adding
	// the axis safe for content already in the database.
	legacy := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"bg": "default", "width": "wide"},
	}, false)
	if !strings.Contains(legacy, "py-20") {
		t.Errorf("a block with no padding key lost the default spacing:\n%s", legacy)
	}

	tight := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"bg": "default", "width": "wide", "padding": "tight"},
	}, false)
	if !strings.Contains(tight, "py-3") || strings.Contains(tight, "py-20") {
		t.Errorf("tight spacing did not replace the default:\n%s", tight)
	}

	// An unknown key falls back rather than emitting junk into class="".
	junk := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"padding": "'; drop table--"},
	}, false)
	if !strings.Contains(junk, "py-20") || strings.Contains(junk, "drop table") {
		t.Errorf("unknown padding key was not resolved to the default:\n%s", junk)
	}

	// Edit renders carry the key so the gear can show the current value.
	edit := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"padding": "tight"},
	}, true)
	if !strings.Contains(edit, `data-cms-padding="tight"`) {
		t.Errorf("edit render is missing data-cms-padding:\n%s", edit)
	}
}

// TestSectionPaddingUnconfigured is the compatibility case: a host that
// never sets Paddings must render byte-identically to before the axis
// existed.
func TestSectionPaddingUnconfigured(t *testing.T) {
	r := &Renderer{sections: &SectionStyles{
		Backgrounds: []SectionOption{{Key: "default", Class: ""}},
		Widths:      []SectionOption{{Key: "wide", Class: "mx-auto max-w-5xl px-6 py-20"}},
	}}
	got := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"bg": "default", "width": "wide"},
	}, false)
	want := `<section><div class="mx-auto max-w-5xl px-6 py-20"><p>x</p></div></section>`
	if got != want {
		t.Errorf("unconfigured host changed:\n got %s\nwant %s", got, want)
	}
}

func TestJoinClasses(t *testing.T) {
	if got := joinClasses("a", "", "b"); got != "a b" {
		t.Errorf("joinClasses = %q, want %q", got, "a b")
	}
	if got := joinClasses("", "", ""); got != "" {
		t.Errorf("joinClasses of empties = %q, want empty", got)
	}
}
