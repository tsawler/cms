package render

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSliderChromeIsStrippedByTheEditor is the one coupling in this
// feature that fails silently, and it fails in the worst direction:
// sliderJS writes chrome and bookkeeping into the DOM at runtime, and
// editor/src/slider.js is what takes it back out before a save. Anything
// the script leaves behind is saved into the page as content — where it
// is sanitized down to something half-real and then disagrees with the
// chrome the script rebuilds on the next load.
//
// Two halves, because the script writes two kinds of thing.
func TestSliderChromeIsStrippedByTheEditor(t *testing.T) {
	stripper, err := os.ReadFile("../editor/src/slider.js")
	if err != nil {
		t.Fatalf("reading the editor's slider module: %v", err)
	}
	strip := string(stripper)

	// Elements. Every one the script appends to the slider must be
	// marked data-cms-ui, which is the single thing the stripper looks
	// for — so the count of top-level appends and the count of markings
	// have to agree. An element built and appended without the mark is
	// one the stripper cannot see.
	appends := strings.Count(sliderJS, "el.appendChild(")
	marks := strings.Count(sliderJS, `setAttribute('data-cms-ui'`)
	if appends == 0 {
		t.Fatal("sliderJS appends nothing to the slider — the check is looking at nothing")
	}
	if marks != appends {
		t.Errorf("sliderJS appends %d elements to the slider but marks %d with data-cms-ui; "+
			"an unmarked one is saved into the page as content", appends, marks)
	}
	if !strings.Contains(strip, "data-cms-ui") {
		t.Error("slider.js does not strip [data-cms-ui]")
	}

	// Attributes. These sit on the slider itself, so there is no
	// ancestor to take them away with — the stripper has to name each
	// one. The two the gear writes are content and must NOT be stripped.
	content := map[string]bool{"data-cms-slider": true, "data-cms-slider-auto": true}
	runtime := map[string]bool{}
	for _, m := range regexp.MustCompile(`el\.setAttribute\('(data-cms-slider[a-z-]*)'`).
		FindAllStringSubmatch(sliderJS, -1) {
		if !content[m[1]] {
			runtime[m[1]] = true
		}
	}
	if len(runtime) == 0 {
		t.Fatal("found no runtime attributes on the slider element — the check is looking at nothing")
	}
	for _, a := range sorted(runtime) {
		if !strings.Contains(strip, `"`+a+`"`) {
			t.Errorf("sliderJS writes %s on the slider, which slider.js does not strip", a)
		}
	}
	for a := range content {
		if strings.Contains(strip, `RUNTIME_ATTRS`) &&
			strings.Contains(runtimeAttrsOf(t, strip), `"`+a+`"`) {
			t.Errorf("slider.js strips %s, which is the gear's own setting and belongs in the database", a)
		}
	}

	// Per-slide state classes, same reasoning: they live on elements
	// that survive, so each has to be named.
	for _, c := range []string{"cms-slide-on", "cms-slide-prev"} {
		if !strings.Contains(sliderJS, c) {
			continue
		}
		if !strings.Contains(strip, `"`+c+`"`) {
			t.Errorf("sliderJS writes .%s on slides, which slider.js does not strip", c)
		}
	}
}

// sorted returns a map's keys in a stable order, so a failure names the
// same attribute every run.
func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// runtimeAttrsOf returns the body of slider.js's RUNTIME_ATTRS list.
func runtimeAttrsOf(t *testing.T, strip string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)RUNTIME_ATTRS = \[(.*?)\]`).FindStringSubmatch(strip)
	if m == nil {
		t.Fatal("no RUNTIME_ATTRS list in slider.js")
	}
	return m[1]
}

// TestSliderShipsToVisitors guards the two wiring points. The CSS and the
// script are useless separately — layout with no behaviour is a stack of
// slides on top of each other, behaviour with no layout is nothing at all
// — so both have to be emitted, and by the right one of the two funcs.
func TestSliderShipsToVisitors(t *testing.T) {
	src, err := os.ReadFile("render.go")
	if err != nil {
		t.Fatalf("reading render.go: %v", err)
	}
	s := string(src)
	head := between(t, s, "func headHTML(", "\n}")
	scripts := between(t, s, "func scriptsHTML(", "\n}")
	if !strings.Contains(head, "sliderCSS") {
		t.Error("{{cmsHead}} does not emit sliderCSS — every slide would draw on top of the others")
	}
	if !strings.Contains(scripts, "sliderJS") {
		t.Error("{{cmsScripts}} does not emit sliderJS — the slider would never move")
	}
}

func between(t *testing.T, s, from, to string) string {
	t.Helper()
	i := strings.Index(s, from)
	if i < 0 {
		t.Fatalf("no %q in render.go", from)
	}
	rest := s[i:]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("no end of %q in render.go", from)
	}
	return rest[:j]
}

// TestSliderStandsDownWhileEditing is a behaviour worth pinning because
// getting it wrong is invisible in a browser until somebody tries to
// work: an autoplay timer that keeps running moves the words out from
// under an editor mid-sentence, and the arrows sit exactly where the
// content is.
func TestSliderStandsDownWhileEditing(t *testing.T) {
	if !strings.Contains(sliderJS, "cms-editing") {
		t.Fatal("sliderJS never checks for edit mode")
	}
	// Checked per event and per tick rather than once at startup: edit
	// mode is entered long after the script runs.
	if n := strings.Count(sliderJS, "editing()"); n < 4 {
		t.Errorf("sliderJS consults edit mode %d times; it has to on every "+
			"click, key, tick and build, not once at startup", n)
	}
}
