package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
)

// The editor's colour scheme reaches the browser as one attribute on the
// injected script tag, and the script trusts that attribute completely —
// so what matters here is that the tag never carries anything but the
// two names the client knows, whatever is in the settings row.
func TestRenderEditorThemeAttribute(t *testing.T) {
	render := func(t *testing.T, stored string) string {
		t.Helper()
		r := newTestRenderer(t)
		page := &content.Page{ID: 3, TemplateName: "pages/home.gohtml", Title: "Home"}
		var buf bytes.Buffer
		err := r.Render(&buf, Input{
			Page:   page,
			Locale: "en",
			Site:   content.SiteSettings{EditorTheme: stored},
			Edit:   &EditInfo{PageID: 3, AdminPath: "/admin", Locale: "en"},
		})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		return buf.String()
	}

	// A site that has never opened the Editor tab — and one saved dark —
	// both get the chrome the editor has always had.
	for _, stored := range []string{"", "dark"} {
		if out := render(t, stored); !strings.Contains(out, `data-editor-theme="dark"`) {
			t.Errorf("stored theme %q: want data-editor-theme=\"dark\" in:\n%s", stored, out)
		}
	}

	if out := render(t, "light"); !strings.Contains(out, `data-editor-theme="light"`) {
		t.Errorf("stored theme \"light\" did not reach the script tag:\n%s", out)
	}

	// Anything else is dark: a value from a newer build than this script,
	// or a hand-edited settings row, must not leave the editor with no
	// theme at all.
	if out := render(t, "chartreuse"); !strings.Contains(out, `data-editor-theme="dark"`) {
		t.Errorf("an unknown theme should fall back to dark:\n%s", out)
	}
}
