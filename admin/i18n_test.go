package admin

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

// TestTemplatesRenderInFrench executes every admin template with
// AdminLang=fr and representative data, so a bad {{.T ...}} call or a
// missing field fails the test instead of a live request.
func TestTemplatesRenderInFrench(t *testing.T) {
	data := templateData{
		AdminPath:     "/admin",
		AdminLang:     "fr",
		LangToggle:    "en",
		User:          &auth.User{Name: "Test", Role: auth.RoleAdmin},
		Users:         []auth.User{{Name: "A", Email: "a@example.com", Role: auth.RoleEditor, Active: true}},
		FormUser:      &auth.User{Role: auth.RoleEditor},
		PagesEnabled:  true,
		PostsEnabled:  true,
		MediaEnabled:  true,
		Pages:         []content.Page{{Title: "Home", Status: content.StatusPublished}},
		FormPage:      &content.Page{ID: 1, Slug: "home", Status: content.StatusPublished},
		Posts:         []content.Post{{}},
		FormPost:      &content.Post{},
		Snippets:      []snippets.Snippet{{Name: "S"}},
		FormSnippet:   &snippets.Snippet{},
		SectionStyles: render.DefaultSectionStyles(),
		Locales:       []string{"en", "fr"},
		EditLocale:    "fr",
		HasDraftEdits: true,
		Regions:       []render.Region{{Name: "body", Kind: "sections"}, {Name: "lede", Kind: "text"}, {Name: "hero", Kind: "image"}},
		RememberHours: 24,
	}
	// EditLocale drives IsDefaultLocale, and the default-locale tab is
	// where the standalone discard/unpublish/delete forms live — so render
	// both tabs or that markup is never executed.
	for _, editLocale := range []string{"fr", "en"} {
		data.EditLocale = editLocale
		for name, tmpl := range parseTemplates() {
			if err := tmpl.ExecuteTemplate(io.Discard, "layout", data); err != nil {
				t.Errorf("rendering %s in French (%s tab): %v", name, editLocale, err)
			}
		}
	}

	// The media page renders one kind per tab, and its root and in-folder
	// views differ — so walk every combination or most of the template is
	// never executed.
	folder := media.Folder{ID: 7, Name: "Field notes", Kind: media.KindImage, Count: 2}
	data.Folders = []media.Folder{folder}
	data.Media = []media.View{{Media: media.Media{ID: 1, Kind: media.KindImage, Filename: "hero.jpg", Ext: ".jpg", Width: 800, Height: 600}}}
	data.Videos = []media.View{{Media: media.Media{ID: 2, Kind: media.KindVideo, Filename: "clip.mp4", Ext: ".mp4"}}}
	data.Documents = []media.View{{Media: media.Media{ID: 3, Kind: media.KindFile, Filename: "terms.pdf", Ext: ".pdf"}}}
	mediaTmpl := parseTemplates()["media"]
	for _, tab := range []string{"images", "documents", "videos"} {
		for _, inFolder := range []bool{false, true} {
			data.MediaTab = tab
			data.CurrentFolder = nil
			if inFolder {
				data.CurrentFolder = &folder
			}
			switch tab {
			case "documents":
				data.Entries = data.Documents
			case "videos":
				data.Entries = data.Videos
			default:
				data.Entries = data.Media
			}
			if err := mediaTmpl.ExecuteTemplate(io.Discard, "layout", data); err != nil {
				t.Errorf("rendering media in French (%s tab, in folder=%v): %v", tab, inFolder, err)
			}
		}
	}
}

func TestTReturnsKeyForEnglishAndUnknown(t *testing.T) {
	en := templateData{AdminLang: "en"}
	if got := en.T("Save draft"); got != "Save draft" {
		t.Errorf(`en T("Save draft") = %q, want the key back`, got)
	}
	fr := templateData{AdminLang: "fr"}
	if got := fr.T("no such key, ever"); got != "no such key, ever" {
		t.Errorf(`fr T(unknown) = %q, want the key back`, got)
	}
}

func TestTReturnsFrench(t *testing.T) {
	fr := templateData{AdminLang: "fr"}
	for key, want := range map[string]string{
		"Save draft":      "Enregistrer le brouillon",
		"Page published.": "Page publiée.",
		"Users":           "Utilisateurs",
	} {
		if got := fr.T(key); got != want {
			t.Errorf("fr T(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestFrStringsWellFormed(t *testing.T) {
	// Terms French keeps as-is (or that are identical by design).
	identical := map[string]bool{
		"Pages": true, "Description": true, "Date": true, "Type": true,
		"Documents": true, "Images": true, "admin": true, "superadmin": true,
		"Destination": true, "Dimensions": true, "Permissions": true,
	}
	for k, v := range frStrings {
		if strings.TrimSpace(k) == "" {
			t.Error("frStrings has an empty key")
		}
		if strings.TrimSpace(v) == "" {
			t.Errorf("frStrings[%q] has an empty value", k)
		}
		if k == v && !identical[k] {
			t.Errorf("frStrings[%q] is identical to its key; add it to the identical list if intended", k)
		}
	}
}

func TestAdminLangResolution(t *testing.T) {
	frSite := &server{deps: Deps{Locales: []string{"en", "fr"}}}
	enSite := &server{deps: Deps{Locales: []string{"en"}}}

	r := httptest.NewRequest("GET", "/", nil)
	if got := frSite.adminLang(r); got != "en" {
		t.Errorf("no cookie, no header: lang = %q, want en", got)
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "fr-CA,fr;q=0.9,en;q=0.5")
	if got := frSite.adminLang(r); got != "fr" {
		t.Errorf("french Accept-Language: lang = %q, want fr", got)
	}
	if got := enSite.adminLang(r); got != "en" {
		t.Errorf("french Accept-Language without fr locale: lang = %q, want en", got)
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "en-US,en;q=0.9,fr;q=0.5")
	if got := frSite.adminLang(r); got != "en" {
		t.Errorf("english-preferring Accept-Language: lang = %q, want en", got)
	}
}

func TestAdminLangCookieWins(t *testing.T) {
	frSite := &server{deps: Deps{Locales: []string{"en", "fr"}}}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "en-US,en;q=0.9")
	r.Header.Set("Cookie", langCookieName+"=fr")
	if got := frSite.adminLang(r); got != "fr" {
		t.Errorf("cookie=fr should beat an English Accept-Language, got %q", got)
	}
	r.Header.Set("Cookie", langCookieName+"=de")
	r.Header.Set("Accept-Language", "fr")
	if got := frSite.adminLang(r); got != "fr" {
		t.Errorf("invalid cookie should fall back to Accept-Language, got %q", got)
	}
}
