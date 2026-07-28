package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/render"
)

// Unpublishing posts to its own route rather than riding the main editor
// form, so that taking content off the site can't carry unsaved form
// fields into the draft with it. These tests pin that down: the standalone
// form must render for published content, the main form must not carry an
// unpublish action, and neither may appear for content that isn't live.
func TestUnpublishIsAStandaloneForm(t *testing.T) {
	exec := func(t *testing.T, tmplName string, data templateData) string {
		t.Helper()
		tmpl, ok := parseTemplates()[tmplName]
		if !ok {
			t.Fatalf("template %s not found", tmplName)
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("rendering %s: %v", tmplName, err)
		}
		return buf.String()
	}

	base := func() templateData {
		return templateData{
			AdminPath:    "/admin",
			AdminLang:    "en",
			PagesEnabled: true,
			PostsEnabled: true,
			Locales:      []string{"en"},
			EditLocale:   "en",
			Regions:      []render.Region{{Name: "body", Kind: "sections"}},
		}
	}

	t.Run("published page", func(t *testing.T) {
		d := base()
		d.FormPage = &content.Page{ID: 7, Slug: "about", Status: content.StatusPublished}
		out := exec(t, "page_form", d)

		if !strings.Contains(out, `action="/admin/pages/7/unpublish"`) {
			t.Error("published page: standalone unpublish form is missing")
		}
		if strings.Contains(out, `value="unpublish"`) {
			t.Error("published page: unpublish is still a submit action on the main form")
		}
	})

	t.Run("draft page", func(t *testing.T) {
		d := base()
		d.FormPage = &content.Page{ID: 7, Slug: "about", Status: content.StatusDraft}
		out := exec(t, "page_form", d)

		if strings.Contains(out, "/unpublish") {
			t.Error("draft page: offers to unpublish content that isn't live")
		}
	})

	t.Run("published post", func(t *testing.T) {
		d := base()
		d.FormPost = &content.Post{PostID: 3, Page: content.Page{Status: content.StatusPublished}}
		out := exec(t, "post_form", d)

		if !strings.Contains(out, `action="/admin/posts/3/unpublish"`) {
			t.Error("published post: standalone unpublish form is missing")
		}
		if strings.Contains(out, `value="unpublish"`) {
			t.Error("published post: unpublish is still a submit action on the main form")
		}
	})

	t.Run("draft post", func(t *testing.T) {
		d := base()
		d.FormPost = &content.Post{PostID: 3, Page: content.Page{Status: content.StatusDraft}}
		out := exec(t, "post_form", d)

		if strings.Contains(out, "/unpublish") {
			t.Error("draft post: offers to unpublish content that isn't live")
		}
	})
}
