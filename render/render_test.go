package render

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tsawler/cms/content"
)

var testFS = fstest.MapFS{
	"base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><head>{{cmsHead}}</head><body>` +
			`<nav>{{range cmsMenu "main"}}<a href="{{.URL}}"{{if .Active}} class="act"{{end}}>{{.Label}}</a>{{end}}</nav>` +
			`{{cmsNav "main"}}` +
			`<h1>{{cmsText "site-name"}}</h1>{{block "content" .}}{{end}}{{cmsScripts}}</body></html>{{end}}`)},
	"pages/home.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}{{if .Title}}<p>{{cmsText "tagline"}}</p>{{end}}` +
			`<div>{{cmsRegion "main"}}</div>{{cmsSections "extra"}}{{end}}`)},
}

func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := New(testFS, []string{"base.gohtml"}, []PageTemplate{{File: "pages/home.gohtml", Label: "Home"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestRegionsWalksIncludedTemplatesAndBranches(t *testing.T) {
	r := newTestRenderer(t)
	regions := r.Regions("pages/home.gohtml")

	want := map[string]string{"site-name": "text", "tagline": "text", "main": "html", "extra": "sections"}
	if len(regions) != len(want) {
		t.Fatalf("got %d regions %v, want %d", len(regions), regions, len(want))
	}
	for _, reg := range regions {
		if want[reg.Name] != reg.Kind {
			t.Errorf("region %q: got kind %q, want %q", reg.Name, reg.Kind, want[reg.Name])
		}
	}
}

func TestRenderFillsRegionsAndEscapesText(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{
		ID: 1, TemplateName: "pages/home.gohtml", Title: "Home",
		Description: `He said "hi" & left`, HeadCSS: "body{color:red}", BodyJS: "console.log(1)",
	}
	blocks := []content.Block{
		{Region: "site-name", Kind: content.KindText, Content: "Acme <script>alert(1)</script>"},
		{Region: "tagline", Kind: content.KindText, Content: "We build things"},
		{Region: "main", Kind: content.KindHTML, Content: "<p class=\"lead\">Hello</p>"},
	}

	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Blocks: blocks, Locale: "en"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	// cmsText content must be escaped by the template engine.
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("cmsText content was not escaped")
	}
	if !strings.Contains(out, "Acme &lt;script&gt;") {
		t.Errorf("escaped site name missing from output:\n%s", out)
	}
	// cmsRegion content is trusted HTML and must pass through raw.
	if !strings.Contains(out, `<p class="lead">Hello</p>`) {
		t.Error("cmsRegion HTML did not pass through")
	}
	// cmsHead: escaped description + raw CSS. cmsScripts: raw JS.
	if !strings.Contains(out, `content="He said &#34;hi&#34; &amp; left"`) {
		t.Errorf("meta description missing or unescaped:\n%s", out)
	}
	if !strings.Contains(out, "body{color:red}") || !strings.Contains(out, "console.log(1)") {
		t.Error("per-page CSS/JS missing from output")
	}
}

func TestRenderMissingContentIsEmptyNotError(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}
	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Locale: "en"}); err != nil {
		t.Fatalf("Render with no blocks: %v", err)
	}
	if !strings.Contains(buf.String(), "<div></div>") {
		t.Errorf("empty region should render empty, got:\n%s", buf.String())
	}
}

func TestRenderEditModeMarksRegionsAndInjectsScript(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 7, TemplateName: "pages/home.gohtml", Title: "Home"}
	blocks := []content.Block{
		{Region: "site-name", Kind: content.KindText, Content: "Acme <sneaky>"},
		{Region: "main", Kind: content.KindHTML, Content: "<p>Hello</p>"},
	}

	var buf bytes.Buffer
	err := r.Render(&buf, Input{Page: page, Blocks: blocks, Locale: "en", Edit: &EditInfo{
		PageID: 7, AdminPath: "/admin", CSRFToken: "tok123", Locale: "en",
		Status: "draft", MediaEnabled: true,
		Styles: []EditorStyle{{Label: "Red", Class: "text-red-600"}},
	}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `data-styles="[{&#34;label&#34;:&#34;Red&#34;,&#34;class&#34;:&#34;text-red-600&#34;}]"`) {
		t.Errorf("styles JSON missing or unescaped in script tag:\n%s", out)
	}

	if !strings.Contains(out, `<span data-cms-region="site-name" data-cms-kind="text">Acme &lt;sneaky&gt;</span>`) {
		t.Errorf("text region marker missing or unescaped:\n%s", out)
	}
	if !strings.Contains(out, `<div data-cms-region="main" data-cms-kind="html"><p>Hello</p></div>`) {
		t.Errorf("html region marker missing:\n%s", out)
	}
	if !strings.Contains(out, `src="`+EditorScriptPath+`"`) || !strings.Contains(out, `data-csrf="tok123"`) {
		t.Errorf("editor script tag missing:\n%s", out)
	}
	// The script must land before </body>, inside the document.
	if strings.Index(out, EditorScriptPath) > strings.LastIndex(strings.ToLower(out), "</body>") {
		t.Error("editor script injected after </body>")
	}

	// A plain render of the same page must carry no editor artifacts.
	buf.Reset()
	if err := r.Render(&buf, Input{Page: page, Blocks: blocks, Locale: "en"}); err != nil {
		t.Fatalf("plain Render: %v", err)
	}
	if strings.Contains(buf.String(), "data-cms-region") || strings.Contains(buf.String(), EditorScriptPath) {
		t.Error("plain render leaked editor markers")
	}
}

func TestNewRejectsUnknownPageTemplate(t *testing.T) {
	_, err := New(testFS, nil, []PageTemplate{{File: "pages/missing.gohtml", Label: "X"}}, nil)
	if err == nil {
		t.Fatal("expected error for missing page template file")
	}
}

func TestBuildMenus(t *testing.T) {
	pid := func(id int64) *int64 { return &id }
	slug := func(s string) *string { return &s }
	status := func(s content.Status) *content.Status { return &s }

	items := []content.MenuItem{
		{ID: 1, Menu: "main", Label: "Home", PageID: pid(1), PageSlug: slug(""), PageStatus: status(content.StatusPublished)},
		{ID: 2, Menu: "main", Label: "About", PageID: pid(2), PageSlug: slug("about"), PageStatus: status(content.StatusPublished)},
		{ID: 3, Menu: "main", Label: "Secret", PageID: pid(3), PageSlug: slug("secret"), PageStatus: status(content.StatusDraft)},
		{ID: 4, Menu: "main", Label: "Docs", URL: "https://example.com/docs", NewTab: true},
		// A label-only item is a dropdown parent; children hang off ParentID.
		{ID: 5, Menu: "main", Label: "More"},
		{ID: 6, Menu: "main", ParentID: pid(5), Label: "Contact", URL: "/contact"},
		{ID: 7, Menu: "main", ParentID: pid(5), Label: "Hidden", PageID: pid(3), PageSlug: slug("secret"), PageStatus: status(content.StatusDraft)},
		{ID: 8, Menu: "main", Label: "Empty"},
		{ID: 9, Menu: "footer", Label: "Privacy", PageID: pid(4), PageSlug: slug("privacy"), PageStatus: status(content.StatusPublished)},
	}

	// Public render of /about: draft items dropped (top-level and inside
	// the dropdown), the empty dropdown dropped, Active on About.
	menus := BuildMenus(items, "about", "en", "en", false)
	main := menus["main"]
	if len(main) != 4 {
		t.Fatalf("public main menu = %d items %v, want 4", len(main), main)
	}
	if main[0].URL != "/" || main[0].Active {
		t.Errorf("home entry wrong: %+v", main[0])
	}
	if main[1].URL != "/about" || !main[1].Active {
		t.Errorf("about entry should be active: %+v", main[1])
	}
	if !main[2].External || !main[2].NewTab || main[2].Active {
		t.Errorf("external entry wrong: %+v", main[2])
	}
	if main[3].Label != "More" || main[3].URL != "" || len(main[3].Children) != 1 ||
		main[3].Children[0].URL != "/contact" {
		t.Errorf("dropdown entry wrong: %+v", main[3])
	}
	if len(menus["footer"]) != 1 {
		t.Errorf("footer menu missing: %v", menus["footer"])
	}

	// Editors see draft-page items and empty dropdowns (to fill them).
	editorMain := BuildMenus(items, "", "en", "en", true)["main"]
	if len(editorMain) != 6 {
		t.Fatalf("editor main menu = %d items %v, want 6", len(editorMain), editorMain)
	}
	if !editorMain[0].Active {
		t.Error("home entry should be active when rendering the home page")
	}
	if len(editorMain[4].Children) != 2 {
		t.Errorf("editor dropdown should keep its draft child: %+v", editorMain[4])
	}
	if editorMain[5].Label != "Empty" || len(editorMain[5].Children) != 0 {
		t.Errorf("editor should keep the empty dropdown: %+v", editorMain[5])
	}
}

func TestRenderNav(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}
	menus := map[string][]MenuEntry{
		"main": {
			{ID: 1, Label: "About <x>", URL: "/about", Active: true},
			{ID: 2, Label: "More", Children: []MenuEntry{
				{ID: 3, Label: "Docs", URL: "https://example.com", NewTab: true, External: true},
			}},
		},
	}

	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Locale: "en", Menus: menus}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<nav class="cms-nav" data-cms-menu="main">` +
			`<button type="button" class="cms-nav-burger" aria-expanded="false" aria-label="Menu">` +
			`<span class="cms-nav-burger-bar" aria-hidden="true"></span></button>` +
			`<ul class="cms-nav-list">`,
		`<a class="cms-nav-link cms-active" href="/about" aria-current="page">About &lt;x&gt;</a>`,
		`<button type="button" class="cms-nav-link cms-nav-toggle" aria-expanded="false" aria-haspopup="true">` +
			`More<span class="cms-nav-caret" aria-hidden="true"></span></button>`,
		`<ul class="cms-nav-sub"><li class="cms-nav-item">` +
			`<a class="cms-nav-link" href="https://example.com" target="_blank" rel="noopener">Docs</a></li></ul>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("cmsNav output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "data-cms-menu-item") {
		t.Error("public render leaked menu edit markers")
	}
	// The functional nav CSS and toggle script ride along with
	// cmsHead/cmsScripts.
	if !strings.Contains(out, ".cms-nav-sub{display:none") || !strings.Contains(out, ".cms-nav-toggle") {
		t.Error("nav CSS or toggle script missing from output")
	}
	// The mobile collapse: burger shown and list hidden under the
	// breakpoint, with the script's .cms-nav-open class revealing it.
	if !strings.Contains(out, "@media (max-width:768px)") ||
		!strings.Contains(out, ".cms-nav.cms-nav-open>.cms-nav-list{display:flex}") ||
		!strings.Contains(out, ".cms-nav-burger") {
		t.Error("mobile nav CSS missing from output")
	}

	// Edit renders mark each item with its id for the in-place editor.
	buf.Reset()
	if err := r.Render(&buf, Input{Page: page, Locale: "en", Menus: menus, Edit: &EditInfo{PageID: 1, AdminPath: "/admin"}}); err != nil {
		t.Fatalf("edit Render: %v", err)
	}
	out = buf.String()
	for _, want := range []string{
		`<li class="cms-nav-item" data-cms-menu-item="1">`,
		`<li class="cms-nav-item cms-nav-drop" data-cms-menu-item="2">`,
		`<li class="cms-nav-item" data-cms-menu-item="3">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("edit render missing %q:\n%s", want, out)
		}
	}
}

func TestRenderMenu(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}
	menus := map[string][]MenuEntry{
		"main": {{Label: "About <x>", URL: "/about", Active: true}},
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Locale: "en", Menus: menus}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `<a href="/about" class="act">About &lt;x&gt;</a>`) {
		t.Errorf("menu entry missing or unescaped:\n%s", out)
	}
}

func TestRenderSections(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}
	blocks := []content.Block{
		{Region: "extra", Kind: content.KindHTML, Sort: 0, Content: "<p>First</p>",
			Settings: map[string]string{"bg": "dark", "width": "full"}},
		{Region: "extra", Kind: content.KindHTML, Sort: 1, Content: "<p>Second</p>",
			Settings: map[string]string{"bg": "bogus-key", "width": "normal"}},
	}

	// Plain render: wrapper + container classes from settings, unknown
	// keys fall back to the first option, no editor attributes.
	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Blocks: blocks, Locale: "en"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `<section class="bg-slate-900"><div class="prose prose-slate max-w-none px-6 py-12 prose-invert"><p>First</p></div></section>`) {
		t.Errorf("dark/full section markup wrong:\n%s", out)
	}
	if !strings.Contains(out, `<section><div class="prose prose-slate mx-auto max-w-3xl px-6 py-12"><p>Second</p></div></section>`) {
		t.Errorf("fallback/default section markup wrong:\n%s", out)
	}
	if strings.Contains(out, "data-cms-section") || strings.Contains(out, "data-cms-sections") {
		t.Error("plain render leaked section edit markers")
	}

	// Edit render: container plus per-section markers with resolved keys.
	buf.Reset()
	if err := r.Render(&buf, Input{Page: page, Blocks: blocks, Locale: "en", Edit: &EditInfo{PageID: 1, AdminPath: "/admin", CSRFToken: "t", Locale: "en", Status: "draft"}}); err != nil {
		t.Fatalf("edit Render: %v", err)
	}
	out = buf.String()
	if !strings.Contains(out, `<div data-cms-sections="extra">`) {
		t.Errorf("edit render missing sections container:\n%s", out)
	}
	if !strings.Contains(out, `data-cms-section data-cms-bg="dark" data-cms-width="full"`) {
		t.Errorf("edit render missing section markers:\n%s", out)
	}
	if !strings.Contains(out, `data-cms-bg="default"`) {
		t.Errorf("unknown bg key not resolved to fallback in edit render:\n%s", out)
	}
	if !strings.Contains(out, "data-section-styles=") {
		t.Errorf("section styles JSON missing from script tag:\n%s", out)
	}
}

func TestRenderPostsData(t *testing.T) {
	fsys := fstest.MapFS{
		"pages/post.gohtml": &fstest.MapFile{Data: []byte(
			`{{with .Post}}<img src="{{.HeaderURL}}"><time>{{.PublishedAt.Format "2006-01-02"}}</time><i>{{.Author}}</i>{{end}}` +
				`<ul>{{range cmsPosts "blog" 5}}<li{{if .Draft}} class="draft"{{end}}><a href="{{.URL}}">{{.Title}}</a> {{.Summary}}</li>{{end}}</ul>`)},
	}
	r, err := New(fsys, nil, []PageTemplate{{File: "pages/post.gohtml", Label: "Post"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	page := &content.Page{ID: 1, TemplateName: "pages/post.gohtml", Title: "Launch"}
	post := &content.Post{
		Page:      content.Page{Slug: "blog/launch", Title: "Launch", Description: "We shipped", Status: content.StatusPublished},
		Feed:      content.FeedBlog,
		HeaderURL: "/cms/media/header.webp",
	}
	post.PublishedAt = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	post.AuthorName = "Pat Writer"

	lister := func(feed string, limit int) []PostInfo {
		if feed != "blog" || limit != 5 {
			t.Errorf("lister called with (%q, %d)", feed, limit)
		}
		return []PostInfo{
			{Title: "Launch", Summary: "We shipped", URL: "/blog/launch"},
			{Title: "WIP", URL: "/blog/wip", Draft: true},
		}
	}

	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Locale: "en", Post: PostInfoFor(post, ""), Posts: lister}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<img src="/cms/media/header.webp">`,
		`<time>2026-07-01</time>`,
		`<i>Pat Writer</i>`,
		`<li><a href="/blog/launch">Launch</a> We shipped</li>`,
		`<li class="draft"><a href="/blog/wip">WIP</a> </li>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// Without a lister (and without post data) the funcs are inert.
	buf.Reset()
	if err := r.Render(&buf, Input{Page: page, Locale: "en"}); err != nil {
		t.Fatalf("Render without post data: %v", err)
	}
	if got := buf.String(); got != "<ul></ul>" {
		t.Errorf("plain render: got %q, want empty list only", got)
	}
}

func TestLocaleURLsAndLinks(t *testing.T) {
	if got := LocalePrefix("en", "en"); got != "" {
		t.Errorf("LocalePrefix default: got %q", got)
	}
	if got := LocalePrefix("fr", "en"); got != "/fr" {
		t.Errorf("LocalePrefix fr: got %q", got)
	}
	cases := []struct{ slug, locale, want string }{
		{"", "en", "/"},
		{"", "fr", "/fr"},
		{"about", "en", "/about"},
		{"about", "fr", "/fr/about"},
		{"blog/post", "fr", "/fr/blog/post"},
	}
	for _, tc := range cases {
		if got := localeURL(tc.slug, tc.locale, "en"); got != tc.want {
			t.Errorf("localeURL(%q, %q) = %q, want %q", tc.slug, tc.locale, got, tc.want)
		}
	}

	in := Input{
		Page:    &content.Page{Slug: "about"},
		Locale:  "fr",
		Locales: []string{"en", "fr"},
	}
	links := localeLinks(in)
	if len(links) != 2 || links[0].URL != "/about" || links[0].Active ||
		links[1].URL != "/fr/about" || !links[1].Active {
		t.Errorf("localeLinks wrong: %+v", links)
	}
	if localeLinks(Input{Page: &content.Page{}, Locales: []string{"en"}}) != nil {
		t.Error("single-locale site should have no locale links")
	}
}

func TestBuildMenusLocale(t *testing.T) {
	slug := "about"
	status := content.StatusPublished
	items := []content.MenuItem{{
		ID: 1, Menu: "main", Label: "About",
		Labels:   map[string]string{"fr": "À propos"},
		PageID:   ptr(int64(7)),
		PageSlug: &slug, PageStatus: &status,
	}}

	fr := BuildMenus(items, "about", "fr", "en", false)["main"]
	if len(fr) != 1 || fr[0].Label != "À propos" || fr[0].URL != "/fr/about" || !fr[0].Active {
		t.Errorf("fr menu entry wrong: %+v", fr)
	}
	en := BuildMenus(items, "about", "en", "en", false)["main"]
	if len(en) != 1 || en[0].Label != "About" || en[0].URL != "/about" || !en[0].Active {
		t.Errorf("en menu entry wrong: %+v", en)
	}
	// No override falls back to the default label.
	items[0].Labels = nil
	fr = BuildMenus(items, "", "fr", "en", false)["main"]
	if fr[0].Label != "About" || fr[0].Active {
		t.Errorf("fr fallback label wrong: %+v", fr)
	}
}

func ptr[T any](v T) *T { return &v }

func TestEditRenderMarksFallbackRegions(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}
	blocks := []content.Block{
		{Region: "tagline", Kind: content.KindText, Locale: "en", Content: "English tagline"},
		{Region: "main", Kind: content.KindHTML, Locale: "fr", Content: "<p>Bonjour</p>"},
		{Region: "extra", Kind: content.KindHTML, Locale: "en", Content: "<p>Fallback section</p>"},
	}
	var buf bytes.Buffer
	err := r.Render(&buf, Input{Page: page, Blocks: blocks, Locale: "fr",
		Edit: &EditInfo{PageID: 1, AdminPath: "/admin", Locale: "fr"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `data-cms-region="tagline" data-cms-kind="text" data-cms-fallback="1"`) {
		t.Errorf("en-fallback text region not marked:\n%s", out)
	}
	if strings.Contains(out, `data-cms-region="main" data-cms-kind="html" data-cms-fallback`) {
		t.Errorf("localized region wrongly marked as fallback:\n%s", out)
	}
	if !strings.Contains(out, `data-cms-sections="extra" data-cms-fallback="1"`) {
		t.Errorf("en-fallback sections region not marked:\n%s", out)
	}

	// Public render (edit nil) never emits fallback markers.
	buf.Reset()
	if err := r.Render(&buf, Input{Page: page, Blocks: blocks, Locale: "fr"}); err != nil {
		t.Fatalf("public Render: %v", err)
	}
	if strings.Contains(buf.String(), "data-cms-fallback") {
		t.Error("public render leaked fallback markers")
	}
}
