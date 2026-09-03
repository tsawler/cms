package snippets

import (
	"strings"
	"testing"
)

func TestSliderPresetIsBuiltFromTheSlide(t *testing.T) {
	s := find(t, DefaultSectionPresets(), "Slider")
	if n := strings.Count(s.HTML, slideHTML); n != 3 {
		t.Errorf("Slider preset is built from %d copies of slideHTML, want 3", n)
	}
	// The gear adds slides by copying one that is already there, so the
	// three it ships with are the template for every one added later.
	if !strings.Contains(s.HTML, `data-cms-slider="fade"`) {
		t.Error("the slider ships with no transition set")
	}
	if !strings.Contains(s.HTML, "cms-slider-bleed") {
		t.Error("the section preset's slider does not bleed — a hero with gutters")
	}
}

// TestSliderMarkupHoldsNoChrome is the storage half of the contract
// render/slider_test.go checks from the runtime side: arrows and dots are
// built by sliderJS from the slide count, so any in here would be a
// second, stale copy that an added slide could not renumber.
func TestSliderMarkupHoldsNoChrome(t *testing.T) {
	s := find(t, DefaultSectionPresets(), "Slider")
	for _, bad := range []string{
		"<button", "cms-slider-nav", "cms-slider-dot", "cms-slide-on",
		"data-cms-slider-at", "data-cms-slider-built",
	} {
		if strings.Contains(s.HTML, bad) {
			t.Errorf("the stored slider markup carries %q — that is sliderJS's to build", bad)
		}
	}
}

// TestEverySlideCanHoldAnything is the requirement that a slide is not a
// headline field and a subtitle field. Its words live in an ordinary box
// that an editor can put a snippet into, so the box has to be real markup
// with no editor-only marker making it special.
func TestEverySlideCanHoldAnything(t *testing.T) {
	if !strings.Contains(slideHTML, `class="cms-slide-body`) {
		t.Fatal("a slide has no content box")
	}
	if !strings.Contains(slideHTML, "<a href=") || !strings.Contains(slideHTML, "cms-btn") {
		t.Error("the starting slide has no button — the one thing every hero slide wants")
	}
	// A photo slot rather than an <img> with a src: the picture is
	// chosen from the media library, and naming one here would name a
	// file that does not exist on a fresh install.
	if !strings.Contains(slideHTML, "data-cms-photo-slot") {
		t.Error("a slide's picture is not a media-library slot")
	}
	if strings.Contains(slideHTML, "<img") {
		t.Error("the starting slide hard-codes an image")
	}
}
