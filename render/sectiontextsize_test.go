package render

import (
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
)

// sizeStyles mirrors the default arrangement: widths that put prose on
// the content container, and size options that modify it.
func sizeStyles() *SectionStyles {
	return &SectionStyles{
		Backgrounds: []SectionOption{{Key: "default", Label: "Default", Class: ""}},
		Widths:      []SectionOption{{Key: "wide", Label: "Wide", Class: "prose mx-auto max-w-5xl"}},
		Sizes: []SectionOption{
			{Key: "normal", Label: "Normal", Class: ""},
			{Key: "large", Label: "Large", Class: "prose-lg"},
		},
	}
}

func TestSectionTextSize(t *testing.T) {
	r := &Renderer{sections: sizeStyles()}

	// A block saved before the axis existed carries no size key. Because
	// the first option contributes no class, it must render exactly as it
	// always did — the property that makes turning the axis on safe for
	// content already in the database.
	legacy := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"bg": "default", "width": "wide"},
	}, false)
	const want = `<section><div class="prose mx-auto max-w-5xl"><p>x</p></div></section>`
	if legacy != want {
		t.Errorf("a block with no size key changed:\n got %s\nwant %s", legacy, want)
	}

	// The modifier lands on the same element as prose, which is the only
	// place Typography reads it.
	large := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"bg": "default", "width": "wide", "size": "large"},
	}, false)
	if !strings.Contains(large, `class="prose mx-auto max-w-5xl prose-lg"`) {
		t.Errorf("size modifier is not on the prose element:\n%s", large)
	}

	// An unknown key falls back rather than emitting junk into class="".
	junk := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"size": "'; drop table--"},
	}, false)
	if strings.Contains(junk, "drop table") || strings.Contains(junk, "prose-lg") {
		t.Errorf("unknown size key was not resolved to the default:\n%s", junk)
	}

	// Edit renders carry the key so the gear can show the current value.
	edit := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"size": "large"},
	}, true)
	if !strings.Contains(edit, `data-cms-size="large"`) {
		t.Errorf("edit render is missing data-cms-size:\n%s", edit)
	}
	// A section at the default carries no stored size setting, but the
	// edit render still names the resolved key, so the gear opens on
	// "Normal" rather than on whatever the first option happens to be.
	editDefault := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{},
	}, true)
	if !strings.Contains(editDefault, `data-cms-size="normal"`) {
		t.Errorf("edit render does not name the default size:\n%s", editDefault)
	}
	if strings.Contains(editDefault, "prose-lg") {
		t.Errorf("the default size applied a class:\n%s", editDefault)
	}
}

// TestSectionTextSizeUnconfigured is the compatibility case: a host that
// never sets Sizes must render byte-identically to before the axis
// existed.
func TestSectionTextSizeUnconfigured(t *testing.T) {
	r := &Renderer{sections: &SectionStyles{
		Backgrounds: []SectionOption{{Key: "default", Class: ""}},
		Widths:      []SectionOption{{Key: "wide", Class: "prose mx-auto max-w-5xl"}},
	}}
	got := r.sectionHTML(content.Block{
		Kind: content.KindHTML, Content: "<p>x</p>",
		Settings: map[string]string{"bg": "default", "width": "wide", "size": "large"},
	}, false)
	want := `<section><div class="prose mx-auto max-w-5xl"><p>x</p></div></section>`
	if got != want {
		t.Errorf("unconfigured host changed:\n got %s\nwant %s", got, want)
	}
}

// TestDefaultSizesRideOnProse is the constraint the default list depends
// on: every default width puts prose on the content container, because a
// prose-* modifier with no prose beside it styles nothing — the dropdown
// would look applied and no text would move.
func TestDefaultSizesRideOnProse(t *testing.T) {
	styles := DefaultSectionStyles()
	for _, w := range styles.Widths {
		if !strings.Contains(" "+w.Class+" ", " prose ") {
			t.Errorf("default width %q has no prose class (%q), so the size axis cannot reach it",
				w.Key, w.Class)
		}
	}
	if styles.Sizes[0].Class != "" {
		t.Errorf("the first size option must contribute no class, got %q", styles.Sizes[0].Class)
	}
	if got := styles.Size("large").Class; got != "prose-lg" {
		t.Errorf(`Size("large") = %q, want "prose-lg"`, got)
	}
	if n := len(styles.Sizes); n != 4 {
		t.Errorf("default Sizes has %d options, want 4", n)
	}
}
