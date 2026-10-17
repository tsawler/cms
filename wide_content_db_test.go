package cms

import (
	"context"
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/snippets"
)

// Content columns must take more than MySQL's TEXT limit of 64 KiB on
// every engine: the admin accepts region bodies of megabytes, and a long
// sections region, a large site stylesheet or a compiled Tailwind build
// passes 64 KiB without trying. Each widened column gets a value well past
// it, through the store that writes it, and must read back whole.
func TestContentColumnsHoldMoreThan64KiB(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newTestCMS(t, db)
		defer c.Close()
		big := func(tag string) string { return tag + strings.Repeat("x", 200<<10) + tag }
		check := func(what, got, want string) {
			t.Helper()
			if got != want {
				t.Errorf("%s: read back %d bytes, want %d", what, len(got), len(want))
			}
		}

		// cms_blocks.content, cms_page_drafts and then cms_pages head_css/body_js.
		p := &content.Page{Slug: "long", Title: "Long", TemplateName: "page.gohtml"}
		if _, err := c.content.Insert(ctx, p, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		body := big("<p>")
		if err := c.content.UpsertDraftBlock(ctx, p.ID, "main", "en", content.KindHTML, body); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		p.HeadCSS, p.BodyJS = big("/*css*/"), big("//js")
		if err := c.content.Update(ctx, p, "en"); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if err := c.content.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		blocks, err := c.content.BlocksFor(ctx, p.ID, "en", content.StatusPublished)
		if err != nil || len(blocks) != 1 {
			t.Fatalf("BlocksFor: %v, %d blocks", err, len(blocks))
		}
		check("cms_blocks.content", blocks[0].Content, body)
		live, err := c.content.GetBySlug(ctx, "long", "en", true)
		if err != nil {
			t.Fatalf("GetBySlug: %v", err)
		}
		check("cms_pages.head_css", live.HeadCSS, p.HeadCSS)
		check("cms_pages.body_js", live.BodyJS, p.BodyJS)

		// cms_settings.value.
		site := content.SiteSettings{SiteCSS: big("/*site*/")}
		if err := c.content.SaveSiteSettings(ctx, site); err != nil {
			t.Fatalf("SaveSiteSettings: %v", err)
		}
		gotSite, err := c.content.SiteSettings(ctx)
		if err != nil {
			t.Fatalf("SiteSettings: %v", err)
		}
		check("cms_settings.value", gotSite.SiteCSS, site.SiteCSS)

		// cms_snippets.html and cms_code_snippets.html.
		sn := &snippets.Snippet{Name: "Long", HTML: big("<div>")}
		id, err := snippets.NewStore(db).Insert(ctx, sn)
		if err != nil {
			t.Fatalf("snippets Insert: %v", err)
		}
		gotSn, err := snippets.NewStore(db).GetByID(ctx, id)
		if err != nil {
			t.Fatalf("snippets GetByID: %v", err)
		}
		check("cms_snippets.html", gotSn.HTML, sn.HTML)
		code := snippets.NewCodeStore(db)
		cs := &snippets.CodeSnippet{Key: "long", Name: "Long", HTML: big("<script>")}
		if _, err := code.Insert(ctx, cs); err != nil {
			t.Fatalf("code Insert: %v", err)
		}
		gotCode, err := code.ByKey(ctx, "long")
		if err != nil {
			t.Fatalf("code ByKey: %v", err)
		}
		check("cms_code_snippets.html", gotCode.HTML, cs.HTML)

		// cms_content_css.css.
		css := big("/*tw*/")
		if err := c.storeContentCSS(ctx, "0123456789abcdef", css); err != nil {
			t.Fatalf("storeContentCSS: %v", err)
		}
		_, gotCSS, err := c.loadContentCSS(ctx)
		if err != nil {
			t.Fatalf("loadContentCSS: %v", err)
		}
		check("cms_content_css.css", gotCSS, css)
	})
}
