package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
)

// A site in development says so in every page's <head>, and a site in
// production says nothing at all — an unconditional robots tag would
// override whatever a host template states for itself.
func TestRenderRobotsTagFollowsSiteMode(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}

	for _, tc := range []struct {
		mode string
		want bool
	}{
		{content.ModeDevelopment, true},
		{content.ModeProduction, false},
		{"", false}, // never saved: production, so upgrades change nothing
	} {
		var buf bytes.Buffer
		in := Input{Page: page, Locale: "en", Site: content.SiteSettings{Mode: tc.mode}}
		if err := r.Render(&buf, in); err != nil {
			t.Fatalf("Render in mode %q: %v", tc.mode, err)
		}
		got := strings.Contains(buf.String(), `<meta name="robots" content="noindex, nofollow"`)
		if got != tc.want {
			t.Errorf("mode %q: robots meta present = %v, want %v\n%s", tc.mode, got, tc.want, &buf)
		}
	}
}
