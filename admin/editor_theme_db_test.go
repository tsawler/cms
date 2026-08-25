package admin

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// The editor's colour scheme is nobody's privilege — it changes nothing a
// visitor sees, and the person it inconveniences is whoever is editing —
// so any editor may set it. Like the notice bar, what needs guarding is
// what an unrelated save does to it, and that only the two known names
// can ever be stored.

func storedEditorTheme(t *testing.T, s *server) string {
	t.Helper()
	site, err := s.deps.Content.SiteSettings(context.Background())
	if err != nil {
		t.Fatalf("SiteSettings: %v", err)
	}
	return site.EditorTheme
}

func TestAPISettingsEditorTheme(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := settingsServer(db)

		rec := httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleEditor,
			`{"siteName":"Acme","editorTheme":"light"}`))
		if rec.Code != 200 {
			t.Fatalf("editor switching the theme: got %d, want 200 (%s)", rec.Code, rec.Body)
		}
		if got := storedEditorTheme(t, s); got != "light" {
			t.Fatalf("stored theme = %q, want light", got)
		}

		// The Site code panel PUTs the settings it knows about and
		// nothing else. A body that never mentions the theme must leave
		// it alone rather than dropping the site back to dark chrome as
		// a side effect of saving a stylesheet.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleAdmin,
			`{"siteName":"Acme","siteCss":"body{margin:0}"}`))
		if rec.Code != 200 {
			t.Fatalf("saving unrelated settings: got %d, want 200 (%s)", rec.Code, rec.Body)
		}
		if got := storedEditorTheme(t, s); got != "light" {
			t.Errorf("an unrelated save changed the theme to %q", got)
		}

		// A scheme nobody ships is refused rather than quietly stored:
		// the client falls back to dark on anything it does not know, so
		// a silently accepted value would look like the save had simply
		// been ignored.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleEditor,
			`{"siteName":"Acme","editorTheme":"solarized"}`))
		if rec.Code != 422 {
			t.Errorf("unknown editor theme: got %d, want 422", rec.Code)
		}
		if got := storedEditorTheme(t, s); got != "light" {
			t.Errorf("a refused save changed the stored theme to %q", got)
		}

		// Back to dark, explicitly.
		rec = httptest.NewRecorder()
		s.apiSaveSettings(rec, settingsRequest(t, s, auth.RoleEditor,
			`{"siteName":"Acme","editorTheme":"dark"}`))
		if rec.Code != 200 {
			t.Fatalf("switching back to dark: got %d, want 200 (%s)", rec.Code, rec.Body)
		}
		if got := storedEditorTheme(t, s); got != "dark" {
			t.Errorf("stored theme = %q, want dark", got)
		}
	})
}
