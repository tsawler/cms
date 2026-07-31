package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tsawler/cms/media"
)

// Only an empty folder can be deleted. The button stays on screen either
// way — disabled, with the reason on it, rather than hidden — so the
// action doesn't appear to come and go depending on what's inside.
func TestDeleteFolderButtonNeedsAnEmptyFolder(t *testing.T) {
	// inFolder picks the view: the crumb button only exists inside a
	// folder, and folder entries — which is where the selection bar reads
	// a folder's count from — only exist at the root.
	renderView := func(t *testing.T, count int, inFolder bool) string {
		t.Helper()
		tmpl, ok := parseTemplates()["media"]
		if !ok {
			t.Fatal("media template not found")
		}
		folder := media.Folder{ID: 7, Name: "Field notes", Kind: media.KindImage, Count: count}
		data := templateData{
			AdminPath:  "/admin",
			AdminLang:  "en",
			Locales:    []string{"en"},
			EditLocale: "en",
			MediaTab:   "images",
			Folders:    []media.Folder{folder},
		}
		if inFolder {
			data.CurrentFolder = &folder
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("rendering media: %v", err)
		}
		return buf.String()
	}
	render := func(t *testing.T, count int) string {
		t.Helper()
		return renderView(t, count, true)
	}

	// The delete button is the only disabled control the crumb bar renders,
	// so its state is read from the markup around the label.
	deleteBtn := func(t *testing.T, out string) string {
		t.Helper()
		i := strings.Index(out, `action="/admin/media/folders/7/delete"`)
		if i < 0 {
			t.Fatal("folder delete form not found")
		}
		end := strings.Index(out[i:], "</form>")
		if end < 0 {
			t.Fatal("folder delete form is unterminated")
		}
		return out[i : i+end]
	}

	t.Run("folder holding files", func(t *testing.T) {
		form := deleteBtn(t, render(t, 2))

		if !strings.Contains(form, "disabled") {
			t.Error("a folder with files in it still offers an enabled Delete folder button")
		}
		// Matched up to the apostrophe, which the template escapes.
		if !strings.Contains(form, `title="Move or delete its files first`) {
			t.Error("the disabled button gives no reason")
		}
	})

	t.Run("empty folder", func(t *testing.T) {
		form := deleteBtn(t, render(t, 0))

		if strings.Contains(form, "disabled") {
			t.Error("an empty folder can't be deleted; the button is disabled")
		}
	})

	// Selecting a folder in the listing is the other way to reach the same
	// action, so the bar carries a folder delete of its own. Its enabled
	// state is decided in the browser from the entry's count; what the
	// template owes it is the count to read and an action to fill in.
	t.Run("the selection bar can delete a folder too", func(t *testing.T) {
		out := renderView(t, 2, false)

		if !strings.Contains(out, `data-count="2"`) {
			t.Error("folder entries carry no count, so the bar can't tell an empty folder from a full one")
		}
		if !strings.Contains(out, `data-action-template="/admin/media/folders/{id}/delete"`) {
			t.Error("the selection bar's folder delete has no action to fill in")
		}
		// Both delete forms confirm, so each needs its own marker — finding
		// one by "the form with data-confirm" would now pick either.
		for _, marker := range []string{"data-sel-delete", "data-sel-folder-delete"} {
			if !strings.Contains(out, marker) {
				t.Errorf("selection bar is missing %s", marker)
			}
		}
	})

	t.Run("the confirmation no longer promises to keep the files", func(t *testing.T) {
		form := deleteBtn(t, render(t, 0))

		// Deleting used to unfile the contents, and the prompt said so.
		// Now the folder has to be empty first, so there is nothing to say.
		if strings.Contains(form, "unfiled") {
			t.Errorf("confirmation still describes unfiling the folder's contents: %s", form)
		}
		if !strings.Contains(form, `data-confirm="Delete this folder?"`) {
			t.Error("confirmation prompt is missing")
		}
	})
}
