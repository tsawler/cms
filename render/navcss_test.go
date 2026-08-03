package render

import (
	"regexp"
	"strings"
	"testing"
)

// TestNavCSSResetsDoNotOutrankHostHooks guards a bug that is easy to
// reintroduce and hard to see.
//
// The CMS emits class hooks — .cms-nav-link and friends — for the host
// site to style. Any rule the CMS ships for those elements is a reset,
// and a reset that is *more specific* than the hook silently wins: a
// dropdown's <button> came out in the page's body font while its
// plain-link siblings used the site's display font, because
// `button.cms-nav-toggle` (0,1,1) beat the host's `.cms-nav-link` (0,1,0)
// no matter what order the stylesheets loaded in.
//
// The rule is therefore: nothing in navCSS may qualify a cms-* class with
// an element name unless it is wrapped in :where(), which zeroes the
// specificity while still outranking the user agent.
func TestNavCSSResetsDoNotOutrankHostHooks(t *testing.T) {
	// Selectors like `button.cms-nav-toggle`, but not `.cms-nav-toggle`
	// and not one already inside :where(...).
	qualified := regexp.MustCompile(`(^|[,}])\s*([a-z]+)(\.cms-[a-z-]+)`)

	for _, m := range qualified.FindAllStringSubmatch(navCSS, -1) {
		offender := m[2] + m[3]
		// Find where it sits and check it is not inside a :where().
		idx := strings.Index(navCSS, offender)
		prefix := navCSS[max(0, idx-8):idx]
		if strings.HasSuffix(prefix, ":where(") {
			continue
		}
		t.Errorf("navCSS has %q, which out-specifies the host's %q hook; "+
			"wrap it in :where() so a site's own rule wins", offender, m[3])
	}
}

// TestNavCSSToggleResetIsZeroSpecificity is the specific case above,
// asserted directly so the intent survives even if the scan is relaxed.
func TestNavCSSToggleResetIsZeroSpecificity(t *testing.T) {
	if !strings.Contains(navCSS, ":where(button.cms-nav-toggle)") {
		t.Error("the dropdown button reset must be wrapped in :where(), " +
			"or it beats the host's .cms-nav-link styling")
	}
	if strings.Contains(navCSS, "}button.cms-nav-toggle") ||
		strings.HasPrefix(navCSS, "button.cms-nav-toggle") {
		t.Error("navCSS still carries an unwrapped button.cms-nav-toggle rule")
	}
}

// TestNavMarkupGivesDropdownsTheSameHook is the other half: the reset
// only matters because the button carries the same class as a plain
// link. If that ever stops being true, hosts have nothing to style.
func TestNavMarkupGivesDropdownsTheSameHook(t *testing.T) {
	entries := []MenuEntry{
		{Label: "Vehicles", Children: []MenuEntry{
			{Label: "Cars", URL: "/inventory"},
		}},
		{Label: "Financing", URL: "/financing"},
	}
	got := string(navHTML("main", entries, "", false, ""))

	if !strings.Contains(got, `class="cms-nav-link cms-nav-toggle"`) {
		t.Errorf("a dropdown's button must carry cms-nav-link too:\n%s", got)
	}
	if !strings.Contains(got, `class="cms-nav-caret"`) {
		t.Errorf("a dropdown must keep its caret:\n%s", got)
	}
}
