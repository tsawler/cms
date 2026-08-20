package render

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/content"
)

// noticeFS is a minimal site: one layout with an ordinary <body>, and a
// second that places {{cmsNotice}} itself.
var noticeFS = fstest.MapFS{
	"base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><head>{{cmsHead}}</head><body class="x">` +
			`<header>nav</header><main>{{block "content" .}}{{end}}</main>` +
			`{{cmsScripts}}</body></html>{{end}}`)},
	"pages/home.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<p>body</p>{{end}}`)},
	"pages/placed.gohtml": &fstest.MapFile{Data: []byte(
		`<html><head>{{cmsHead}}</head><body><header>nav{{cmsNotice}}</header>` +
			`<main><p>body</p></main>{{cmsScripts}}</body></html>`)},
}

func newNoticeRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := New(noticeFS, []string{"base.gohtml"}, []PageTemplate{
		{File: "pages/home.gohtml", Label: "Home"},
		{File: "pages/placed.gohtml", Label: "Placed"},
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// renderNotice renders the home page with the given settings and notice
// content, and returns the page.
func renderNotice(t *testing.T, r *Renderer, site content.SiteSettings, text string, edit bool) string {
	t.Helper()
	in := Input{
		Page:   &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"},
		Locale: "en",
		Site:   site,
	}
	if text != "" {
		in.Shared = []content.Block{{Region: NoticeRegion, Kind: content.KindHTML, Content: text, Locale: "en"}}
	}
	if edit {
		in.Edit = &EditInfo{PageID: 1, AdminPath: "/admin", Locale: "en"}
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, in); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func TestNoticeBarOffRendersNothing(t *testing.T) {
	r := newNoticeRenderer(t)
	// Words written while the bar was on must not reach the page once it
	// is switched off — the switch is the whole control, and an editor
	// who turns it off has not agreed to keep announcing anything.
	out := renderNotice(t, r, content.SiteSettings{}, "<p>Closed Monday</p>", false)
	if strings.Contains(out, "data-cms-notice") || strings.Contains(out, "Closed Monday") {
		t.Errorf("the bar rendered with the setting off:\n%s", out)
	}
}

func TestNoticeBarRendersAboveTheTemplate(t *testing.T) {
	r := newNoticeRenderer(t)
	site := content.SiteSettings{NoticeBar: true, NoticeStyle: "warning"}
	out := renderNotice(t, r, site, "<p>Closed Monday</p>", false)

	if !strings.Contains(out, "cms-notice-warning") {
		t.Errorf("the chosen style is missing:\n%s", out)
	}
	if !strings.Contains(out, "Closed Monday") {
		t.Errorf("the notice's words are missing:\n%s", out)
	}
	// A layout that never places the bar gets it injected right after
	// its <body> tag — before the header the template drew, which is the
	// point of the whole feature.
	bodyAt := strings.Index(out, `<body class="x">`)
	barAt := strings.Index(out, "data-cms-notice")
	headerAt := strings.Index(out, "<header>")
	if bodyAt < 0 || barAt < bodyAt || barAt > headerAt {
		t.Errorf("the bar is not between <body> and the header (body %d, bar %d, header %d):\n%s",
			bodyAt, barAt, headerAt, out)
	}
	// The bar's own CSS ships with the page, so it looks right on a site
	// whose stylesheet has never heard of it.
	if !strings.Contains(out, ".cms-notice-inner{") {
		t.Error("the bar's CSS was not emitted by cmsHead")
	}
	// Nothing is dismissible here, so no dismissal machinery.
	if strings.Contains(out, `class="cms-notice-close"`) || strings.Contains(out, "localStorage") {
		t.Errorf("a bar with no close button emitted dismissal code:\n%s", out)
	}
}

func TestNoticeBarPlacedByTemplateIsNotAlsoInjected(t *testing.T) {
	r := newNoticeRenderer(t)
	var buf bytes.Buffer
	err := r.Render(&buf, Input{
		Page:   &content.Page{ID: 1, TemplateName: "pages/placed.gohtml", Title: "Placed"},
		Locale: "en",
		Site:   content.SiteSettings{NoticeBar: true},
		Shared: []content.Block{{Region: NoticeRegion, Kind: content.KindHTML,
			Content: "<p>Closed Monday</p>", Locale: "en"}},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if n := strings.Count(out, "data-cms-notice"); n != 1 {
		t.Errorf("got %d bars, want the one the template placed:\n%s", n, out)
	}
	// Placed inside the header, where the template asked for it.
	if strings.Index(out, "data-cms-notice") < strings.Index(out, "<header>") {
		t.Errorf("the bar was injected rather than left where the template put it:\n%s", out)
	}
}

func TestNoticeBarWithNothingWrittenInIt(t *testing.T) {
	r := newNoticeRenderer(t)
	site := content.SiteSettings{NoticeBar: true}

	// A visitor sees no bar until there are words for it to carry — an
	// empty coloured strip across every page reads as a broken site.
	for _, text := range []string{"", "<p><br></p>", "<p>&nbsp;</p>", "   "} {
		out := renderNotice(t, r, site, text, false)
		if strings.Contains(out, "data-cms-notice") {
			t.Errorf("an empty notice (%q) rendered a bar:\n%s", text, out)
		}
	}

	// An editor gets the bar with a placeholder in it, or there would be
	// nothing on the page to click into and write the notice.
	out := renderNotice(t, r, site, "", true)
	if !strings.Contains(out, `data-cms-region="site:notice"`) {
		t.Errorf("the edit render has no editable notice region:\n%s", out)
	}
	if !strings.Contains(out, "Write the notice here") {
		t.Errorf("the edit render has no placeholder to type over:\n%s", out)
	}
	if !strings.Contains(out, `data-cms-kind="html"`) {
		t.Error("the notice region is not marked as a rich region")
	}
}

func TestNoticeBarDismissal(t *testing.T) {
	r := newNoticeRenderer(t)
	site := content.SiteSettings{NoticeBar: true, NoticeDismissible: true}
	out := renderNotice(t, r, site, "<p>Closed Monday</p>", false)

	if !strings.Contains(out, `class="cms-notice-close"`) || !strings.Contains(out, "cms-notice-closable") {
		t.Errorf("a dismissible bar has no close button:\n%s", out)
	}
	// The hide script belongs in <head>: replaying the dismissal from
	// the end of the body would show the bar and then yank it away.
	headEnd := strings.Index(out, "</head>")
	hideAt := strings.Index(out, "display:none")
	if hideAt < 0 || hideAt > headEnd {
		t.Errorf("the hide script is not in <head> (at %d, head ends %d):\n%s", hideAt, headEnd, out)
	}
	if !strings.Contains(out, "localStorage.setItem('cms-notice'") {
		t.Errorf("nothing remembers the dismissal:\n%s", out)
	}

	// The key names the notice's words, so rewriting the notice shows it
	// again to everyone who closed the previous one.
	other := renderNotice(t, r, site, "<p>Open late Thursday</p>", false)
	if noticeKeyIn(t, out) == noticeKeyIn(t, other) {
		t.Error("two different notices share a dismissal key; the second would stay hidden")
	}

	// An edit render's button works too — a logged-in editor reading
	// their own site is a visitor, and a dead button reads as a broken
	// site. What it must not do is remember the click: an editor who
	// dismissed the bar for good could not get back to the words they
	// were writing.
	edit := renderNotice(t, r, site, "<p>Closed Monday</p>", true)
	if !strings.Contains(edit, `class="cms-notice-close"`) {
		t.Error("an edit render should still show the close button an editor's visitors see")
	}
	if !strings.Contains(edit, "addEventListener('click'") {
		t.Errorf("an edit render left the close button unwired:\n%s", edit)
	}
	if strings.Contains(edit, "localStorage") {
		t.Errorf("an edit render remembered the dismissal:\n%s", edit)
	}
	// And it refuses while the page is actually being edited, where
	// removing the bar would strand the region being typed in.
	if !strings.Contains(edit, "cms-editing") {
		t.Errorf("the close button is not guarded against edit mode:\n%s", edit)
	}
	// The public script carries the same guard, harmlessly — it is one
	// script, built once.
	if !strings.Contains(out, "cms-editing") {
		t.Errorf("the public close script lost the edit-mode guard:\n%s", out)
	}
}

// noticeKeyIn pulls the stored dismissal key back out of a rendered page.
func noticeKeyIn(t *testing.T, page string) string {
	t.Helper()
	const marker = "localStorage.setItem('cms-notice',"
	at := strings.Index(page, marker)
	if at < 0 {
		t.Fatalf("no dismissal key in the page:\n%s", page)
	}
	rest := page[at+len(marker):]
	return rest[:strings.Index(rest, ")")]
}

func TestNoticeBarTranslationBadge(t *testing.T) {
	r := newNoticeRenderer(t)
	in := Input{
		Page:   &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"},
		Locale: "fr",
		Site:   content.SiteSettings{NoticeBar: true},
		Edit:   &EditInfo{PageID: 1, AdminPath: "/admin", Locale: "fr"},
		// The stored notice is the English one: this page is showing a
		// region it has no French copy of.
		Shared: []content.Block{{Region: NoticeRegion, Kind: content.KindHTML,
			Content: "<p>Closed Monday</p>", Locale: "en"}},
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, in); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "data-cms-fallback") {
		t.Errorf("an untranslated notice is not badged:\n%s", buf.String())
	}
}

func TestValidNoticeStyle(t *testing.T) {
	if got := ValidNoticeStyle("alert"); got != "alert" {
		t.Errorf("ValidNoticeStyle(alert) = %q", got)
	}
	// Unknown and unset both resolve to a real scheme rather than
	// rendering a bar with no colours at all.
	for _, in := range []string{"", "chartreuse", `"><script>`} {
		if got := ValidNoticeStyle(in); got != NoticeStyles[0].Key {
			t.Errorf("ValidNoticeStyle(%q) = %q, want %q", in, got, NoticeStyles[0].Key)
		}
	}
}

func TestSharedRegionsIncludesTheNotice(t *testing.T) {
	// Nothing in these templates declares {{cmsShared "notice"}}, and a
	// save still has to be able to name it.
	r := newNoticeRenderer(t)
	var found bool
	for _, reg := range r.SharedRegions() {
		if reg.Name == NoticeRegion {
			if reg.Kind != KindShared {
				t.Errorf("the notice region has kind %q, want %q", reg.Kind, KindShared)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("SharedRegions() = %v, want it to include %q", r.SharedRegions(), NoticeRegion)
	}
	// And it must not turn up in a page's own region list, where it
	// would become a field on the admin's page form.
	for _, reg := range r.Regions("pages/home.gohtml") {
		if reg.Name == NoticeRegion {
			t.Error("the notice region leaked into a page's regions")
		}
	}
}

func TestInsertAfterBodyTag(t *testing.T) {
	cases := []struct {
		name, page, want string
	}{
		{"plain", "<html><body><p>x</p></body></html>", "<html><body>BAR<p>x</p></body></html>"},
		{"attributes", `<html><body class="a b" id="c"><p>x</p></body>`,
			`<html><body class="a b" id="c">BAR<p>x</p></body>`},
		// A ">" inside an attribute value is legal and must not be
		// mistaken for the end of the tag.
		{"gt in attribute", `<body data-x="a>b"><p>y</p>`, `<body data-x="a>b">BAR<p>y</p>`},
		{"uppercase", "<BODY><p>x</p>", "<BODY>BAR<p>x</p>"},
		{"newline in tag", "<body\n  class=\"x\">\n<p>y</p>", "<body\n  class=\"x\">BAR\n<p>y</p>"},
		// Not the body tag.
		{"lookalike first", "<bodyguard></bodyguard><body><p>x</p>",
			"<bodyguard></bodyguard><body>BAR<p>x</p>"},
		// No body at all (a fragment template): the bar still goes first.
		{"no body", "<p>x</p>", "BAR<p>x</p>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(insertAfterBodyTag([]byte(c.page), "BAR"))
			if got != c.want {
				t.Errorf("insertAfterBodyTag(%q) =\n%q\nwant\n%q", c.page, got, c.want)
			}
		})
	}
}

func TestNoticeBlank(t *testing.T) {
	blank := []string{"", "   ", "<p></p>", "<p><br></p>", "<p>&nbsp;</p>", "<p>\n  <br>\n</p>"}
	for _, s := range blank {
		if !noticeBlank(s) {
			t.Errorf("noticeBlank(%q) = false, want true", s)
		}
	}
	filled := []string{"<p>x</p>", "words", `<p><img src="/a.png"></p>`, "<p><strong>Closed</strong></p>"}
	for _, s := range filled {
		if noticeBlank(s) {
			t.Errorf("noticeBlank(%q) = true, want false", s)
		}
	}
}
