package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tsawler/cms/media"
)

// Dragging an entry onto a folder files it there, and dragging it onto the
// root crumb sends it back out. The drop targets are view-dependent —
// folder entries only exist at the root, the crumb only inside a folder —
// so each view is checked for the one it should offer and the one it
// shouldn't.
func TestDragToMoveAffordances(t *testing.T) {
	folder := media.Folder{ID: 7, Name: "Field notes", Kind: media.KindImage, Count: 2}

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
			Folders:       []media.Folder{folder},
			CurrentFolder: current,
			Entries: []media.View{{
				Media:    media.Media{ID: 1, Kind: media.KindImage, Filename: "hero.jpg", Ext: ".jpg"},
				ThumbURL: "/cms/media/abc/thumb.webp",
			}},
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("rendering media: %v", err)
		}
		return buf.String()
	}

	t.Run("files are draggable, folders and thumbnails are not", func(t *testing.T) {
		out := render(t, nil)

		// One file entry, one folder entry: only the file may be dragged.
		if n := strings.Count(out, `draggable="true"`); n != 1 {
			t.Errorf(`draggable="true" appears %d times, want 1 (the file entry only)`, n)
		}
		// An <img> is draggable by default and would otherwise win over the
		// entry around it, dragging the picture instead of the file.
		if !strings.Contains(out, `loading="lazy" draggable="false"`) {
			t.Error("thumbnail is still natively draggable and will hijack the entry's drag")
		}
	})

	t.Run("the move form is wired to the shared bulk handler", func(t *testing.T) {
		for _, current := range []*media.Folder{nil, &folder} {
			out := render(t, current)
			if !strings.Contains(out, `action="/admin/media/bulk/move" data-sel-form data-drag-move hidden`) {
				t.Errorf("inFolder=%v: drag-move form is missing, or no longer carries data-sel-form (which is what fills in the ids)", current != nil)
			}
		}
	})

	t.Run("at the root", func(t *testing.T) {
		out := render(t, nil)

		if !strings.Contains(out, `data-folder-id="7"`) {
			t.Error("folder entry carries no id, so nothing can be dropped onto it")
		}
		if strings.Contains(out, "data-crumb-root") {
			t.Error("root crumb is a drop target at the root, which has nothing above it")
		}
	})

	t.Run("inside a folder", func(t *testing.T) {
		out := render(t, &folder)

		if !strings.Contains(out, "data-crumb-root") {
			t.Error("root crumb is not a drop target, so files can't be dragged out of the folder")
		}
		if strings.Contains(out, "data-folder-id") {
			t.Error("folder entries listed inside a folder; folders are flat and cannot nest")
		}
	})
}
