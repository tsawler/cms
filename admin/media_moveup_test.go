package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tsawler/cms/media"
)

// Folders are flat, so "up" only ever means the root. Inside a folder that
// move gets a button of its own, and the folder being browsed drops out of
// the destination list — it is not somewhere to go. At the root neither
// applies: there is nothing above it, and every folder is a destination.
func TestMoveUpAppearsOnlyInsideAFolder(t *testing.T) {
	folder := media.Folder{ID: 7, Name: "Field notes", Kind: media.KindImage, Count: 2}
	other := media.Folder{ID: 8, Name: "Archive", Kind: media.KindImage}

	// The upload dialog has a destination select of its own, where "No
	// folder" and a preselected current folder are both correct. Only the
	// selection bar is under test, so cut it out rather than matching
	// option markup across the whole page.
	selbar := func(t *testing.T, out string) string {
		t.Helper()
		start := strings.Index(out, `<div class="cms-selbar"`)
		end := strings.Index(out, `<div class="cms-browser-body"`)
		if start < 0 || end < start {
			t.Fatal("selection bar not found in rendered media page")
		}
		return out[start:end]
	}

	render := func(t *testing.T, current *media.Folder) string {
		t.Helper()
		tmpl, ok := parseTemplates()["media"]
		if !ok {
			t.Fatal("media template not found")
		}
		data := templateData{
			AdminPath:     "/admin",
			AdminLang:     "en",
			Locales:       []string{"en"},
			EditLocale:    "en",
			MediaTab:      "images",
			Folders:       []media.Folder{folder, other},
			CurrentFolder: current,
			Entries: []media.View{{Media: media.Media{
				ID: 1, Kind: media.KindImage, Filename: "hero.jpg", Ext: ".jpg",
			}}},
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("rendering media: %v", err)
		}
		return buf.String()
	}

	t.Run("inside a folder", func(t *testing.T) {
		out := selbar(t, render(t, &folder))

		if !strings.Contains(out, "data-sel-up") {
			t.Error("no move-up button: a file in a folder can't be sent back to the root in one click")
		}
		if strings.Contains(out, ">No folder<") {
			t.Error(`root destination still reads "No folder" from inside a folder`)
		}
		if !strings.Contains(out, "↑ All media") {
			t.Error("root destination is not labelled as a move up")
		}
		if strings.Contains(out, `<option value="7">`) {
			t.Error("the folder being browsed is offered as a destination")
		}
		if !strings.Contains(out, `<option value="8">`) {
			t.Error("the other folder is missing from the destination list")
		}
	})

	t.Run("at the root", func(t *testing.T) {
		out := selbar(t, render(t, nil))

		if strings.Contains(out, "data-sel-up") {
			t.Error("move-up button offered at the root, which has nothing above it")
		}
		if !strings.Contains(out, ">No folder<") {
			t.Error(`root destination should read "No folder" when not inside one`)
		}
		for _, id := range []string{`<option value="7">`, `<option value="8">`} {
			if !strings.Contains(out, id) {
				t.Errorf("destination %s missing at the root", id)
			}
		}
	})
}
