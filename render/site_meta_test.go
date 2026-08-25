package render

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/content"
)

// The site's head markup is the one field whose whole value is "these
// exact tags, in the <head>, where a crawler will find them" — so what
// it needs proving is placement and verbatimness, not escaping.

var siteMetaFS = fstest.MapFS{
	"base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><head>{{cmsHead}}</head><body>` +
			`{{block "content" .}}{{end}}{{cmsScripts}}</body></html>{{end}}`)},
	"pages/home.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<p>body</p>{{end}}`)},
}

func renderSiteMeta(t *testing.T, site content.SiteSettings) string {
	t.Helper()
	r, err := New(siteMetaFS, []string{"base.gohtml"},
		[]PageTemplate{{File: "pages/home.gohtml", Label: "Home"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, Input{
		Page:   &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"},
		Locale: "en",
		Site:   site,
	}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func TestSiteMetaGoesIntoTheHeadVerbatim(t *testing.T) {
	tag := `<meta name="google-site-verification" content="abc&123">`
	got := renderSiteMeta(t, content.SiteSettings{SiteMeta: tag})
	if !strings.Contains(got, tag) {
		t.Fatalf("the stored tag is not in the page as written:\n%s", got)
	}
	head := got[:strings.Index(got, "</head>")]
	if !strings.Contains(head, tag) {
		t.Errorf("the tag landed outside <head>:\n%s", got)
	}
	// Ahead of everything else the CMS emits: a verification tag is worth
	// nothing if a crawler gives up before reaching it.
	if strings.Index(head, tag) > strings.Index(head, "<style>") {
		t.Errorf("the tag came after the CMS stylesheet:\n%s", head)
	}
}

func TestSiteMetaEmptyAddsNothing(t *testing.T) {
	got := renderSiteMeta(t, content.SiteSettings{SiteMeta: "  \n  "})
	blank := renderSiteMeta(t, content.SiteSettings{})
	if got != blank {
		t.Errorf("whitespace-only head markup changed the page:\n%s", got)
	}
}
