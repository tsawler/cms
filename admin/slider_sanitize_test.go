package admin

import (
	"strings"
	"testing"
)

// TestSliderSettingsSurviveTheSanitizer covers the two attributes the
// gear writes. They are content — they came from an editor's choice —
// and a save that dropped them would silently reset every slider on the
// site to a non-autoplaying fade.
func TestSliderSettingsSurviveTheSanitizer(t *testing.T) {
	in := `<div class="cms-snippet cms-slider" data-cms-slider="slide" data-cms-slider-auto="6000"></div>`
	got := editorHTMLPolicy.Sanitize(in)
	for _, want := range []string{`data-cms-slider="slide"`, `data-cms-slider-auto="6000"`} {
		if !strings.Contains(got, want) {
			t.Errorf("sanitizer dropped %s\n got: %s", want, got)
		}
	}
}

// TestSliderSettingsAreBounded is the other half: these are the only two
// values a slider carries, so they are the only two an attacker gets to
// choose, and both are matched against a pattern rather than passed
// through.
func TestSliderSettingsAreBounded(t *testing.T) {
	bad := []string{
		`<div data-cms-slider="javascript:alert(1)"></div>`,
		`<div data-cms-slider="FADE"></div>`,
		`<div data-cms-slider=""></div>`,
		// Under a second is a strobe, not a slider; sliderJS ignores it
		// and the sanitizer refuses to store it.
		`<div data-cms-slider-auto="100"></div>`,
		`<div data-cms-slider-auto="0"></div>`,
		`<div data-cms-slider-auto="99999999"></div>`,
	}
	for _, in := range bad {
		if got := editorHTMLPolicy.Sanitize(in); strings.Contains(got, "data-cms-slider") {
			t.Errorf("sanitizer kept a bad slider value: %s -> %s", in, got)
		}
	}
}

// TestSliderChromeCannotBeStored is what makes "the chrome is generated"
// true rather than merely intended: content that hand-wrote its own
// arrows would leave a stale, unlabelled pair of buttons on the page that
// nothing renumbers and nothing wires up.
func TestSliderChromeCannotBeStored(t *testing.T) {
	in := `<div class="cms-slider"><button class="cms-slider-nav">x</button></div>`
	if got := editorHTMLPolicy.Sanitize(in); strings.Contains(got, "<button") {
		t.Errorf("a <button> survived into stored content: %s", got)
	}
}
