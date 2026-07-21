package admin

import (
	"html"
	"strings"
	"testing"

	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

// Every default snippet and section preset must pass the editor
// sanitization policy unchanged — editor-role saves run through it, so a
// stripped attribute would mean the block silently degrades the first
// time a non-admin touches the page. Two policy artifacts are cosmetic
// and accepted: typographic entities decode (&ldquo; → “), and links gain
// rel="nofollow" (see TestEditorHTMLPolicy). Normalize both so only real
// stripping fails.
func TestDefaultSnippetsSurvivePolicy(t *testing.T) {
	all := append(snippets.DefaultSnippets(), snippets.DefaultSectionPresets()...)
	for _, sn := range all {
		want := html.UnescapeString(sn.HTML)
		got := strings.ReplaceAll(editorHTMLPolicy.Sanitize(sn.HTML), ` rel="nofollow"`, "")
		if got != want {
			t.Errorf("policy alters %q:\nin:  %s\nout: %s", sn.Name, want, got)
		}
	}
}

// Preset settings must stay within the section-settings vocabulary and
// resolve against the default section styles — an unknown key here would
// silently fall back to the default option in the editor.
func TestDefaultSectionPresetSettings(t *testing.T) {
	styles := render.DefaultSectionStyles()
	for _, sn := range snippets.DefaultSectionPresets() {
		if sn.Settings == nil {
			t.Errorf("preset %q has nil Settings — it would be a plain snippet", sn.Name)
			continue
		}
		for key, val := range sn.Settings {
			switch key {
			case "bg":
				if styles.Background(val).Key != val {
					t.Errorf("preset %q: unknown background key %q", sn.Name, val)
				}
			case "width":
				if styles.Width(val).Key != val {
					t.Errorf("preset %q: unknown width key %q", sn.Name, val)
				}
			case "height":
				if render.ValidSectionHeight(val) != val {
					t.Errorf("preset %q: invalid height %q", sn.Name, val)
				}
			case "valign":
				if render.ValidSectionVAlign(val) != val {
					t.Errorf("preset %q: invalid valign %q", sn.Name, val)
				}
			case "bgcolor":
				if render.ValidBackgroundColor(val) != val {
					t.Errorf("preset %q: invalid bgcolor %q", sn.Name, val)
				}
			case "bgimage":
				if render.ValidBackgroundURL(val) != val {
					t.Errorf("preset %q: invalid bgimage %q", sn.Name, val)
				}
			default:
				t.Errorf("preset %q: unknown setting %q", sn.Name, key)
			}
		}
	}
}
