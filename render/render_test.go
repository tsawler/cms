package render

import (
	"bytes"
	"slices"
	"strconv"
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
			`<h1>{{cmsText "site-name"}}</h1>{{block "content" .}}{{end}}` +
			`<footer>{{cmsShared "footer" "<p>&copy; Acme</p>"}}</footer>{{cmsScripts}}</body></html>{{end}}`)},
	"pages/home.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}{{if .Title}}<p>{{cmsText "tagline"}}</p>{{end}}` +
			`<h2>{{cmsTitle}}</h2>` +
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
	if !strings.Contains(out, `src="`+EditorScriptPath()+`"`) || !strings.Contains(out, `data-csrf="tok123"`) {
		t.Errorf("editor script tag missing:\n%s", out)
	}
	// The script must land before </body>, inside the document.
	if strings.Index(out, EditorScriptPath()) > strings.LastIndex(strings.ToLower(out), "</body>") {
		t.Error("editor script injected after </body>")
	}

	// A plain render of the same page must carry no editor artifacts.
	buf.Reset()
	if err := r.Render(&buf, Input{Page: page, Blocks: blocks, Locale: "en"}); err != nil {
		t.Fatalf("plain Render: %v", err)
	}
	if strings.Contains(buf.String(), "data-cms-region") || strings.Contains(buf.String(), EditorScriptPath()) {
		t.Error("plain render leaked editor markers")
	}
}

// Unlisted templates parse and render like any other, but the editor's
// new-page dialog only offers them to superadmins.
func TestUnlistedTemplatesHiddenFromNonSuperadminDialog(t *testing.T) {
	r, err := New(testFS, []string{"base.gohtml"}, []PageTemplate{
		{File: "pages/home.gohtml", Label: "Home"},
		{File: "pages/home.gohtml", Label: "One-off", Unlisted: true},
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	listed := r.ListedPageTemplates()
	if len(listed) != 1 || listed[0].Label != "Home" {
		t.Fatalf("ListedPageTemplates = %v, want just Home", listed)
	}
	if len(r.PageTemplates()) != 2 {
		t.Fatalf("PageTemplates = %v, want both entries", r.PageTemplates())
	}

	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}
	renderFor := func(super bool) string {
		var buf bytes.Buffer
		err := r.Render(&buf, Input{Page: page, Locale: "en", Edit: &EditInfo{
			PageID: 1, AdminPath: "/admin", IsSuperadmin: super,
		}})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		return buf.String()
	}
	if out := renderFor(false); strings.Contains(out, "One-off") {
		t.Errorf("editor payload offers an unlisted template to a non-superadmin:\n%s", out)
	}
	if out := renderFor(true); !strings.Contains(out, "One-off") {
		t.Errorf("editor payload hides unlisted templates from a superadmin:\n%s", out)
	}
}

// cmsTitle prints the page's own title, and an edit render wraps it so
// the heading on the page is the thing an editor types into — the title
// field and the heading being two views of one value is the whole point
// of the func.
func TestRenderTitleIsPlainTextUntilEditMode(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 3, TemplateName: "pages/home.gohtml", Title: `Tools & "Toys"`}

	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Locale: "en"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if want := `<h2>Tools &amp; &#34;Toys&#34;</h2>`; !strings.Contains(buf.String(), want) {
		t.Errorf("plain render: want %s in:\n%s", want, buf.String())
	}
	if strings.Contains(buf.String(), "data-cms-title") {
		t.Error("plain render leaked the editable title marker")
	}

	buf.Reset()
	err := r.Render(&buf, Input{Page: page, Locale: "en", Edit: &EditInfo{
		PageID: 3, AdminPath: "/admin", CSRFToken: "tok", Locale: "en", Status: "draft",
	}})
	if err != nil {
		t.Fatalf("edit Render: %v", err)
	}
	want := `<h2><span data-cms-title>Tools &amp; &#34;Toys&#34;</span></h2>`
	if !strings.Contains(buf.String(), want) {
		t.Errorf("edit render: want %s in:\n%s", want, buf.String())
	}
}

// The editor script tag carries the user's permission flags: the JS
// decides which tools to offer from these, so they must mirror EditInfo
// exactly.
func TestEditorScriptCarriesPermissionFlags(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 3, TemplateName: "pages/home.gohtml", Title: "Home"}

	var buf bytes.Buffer
	err := r.Render(&buf, Input{Page: page, Locale: "en", Edit: &EditInfo{
		PageID: 3, AdminPath: "/admin", CSRFToken: "tok", Locale: "en", Status: "draft",
		CanBlogs: true,
	}})
	if err != nil {
		t.Fatalf("edit Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`data-can-blogs="1"`, `data-can-news="0"`, `data-can-pages="0"`,
		`data-is-admin="0"`, `data-is-superadmin="0"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("edit render missing %s in script tag:\n%s", want, out)
		}
	}

	buf.Reset()
	err = r.Render(&buf, Input{Page: page, Locale: "en", Edit: &EditInfo{
		PageID: 3, AdminPath: "/admin", CSRFToken: "tok", Locale: "en", Status: "draft",
		IsAdmin: true, CanPages: true, CanBlogs: true, CanNews: true,
	}})
	if err != nil {
		t.Fatalf("edit Render: %v", err)
	}
	out = buf.String()
	for _, want := range []string{
		`data-can-blogs="1"`, `data-can-news="1"`, `data-can-pages="1"`, `data-is-admin="1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("admin edit render missing %s in script tag:\n%s", want, out)
		}
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
	private := content.VisibilityPrivate

	items := []content.MenuItem{
		{ID: 1, Menu: "main", Label: "Home", PageID: pid(1), PageSlug: slug(""), PageStatus: status(content.StatusPublished)},
		{ID: 10, Menu: "main", Label: "Members", PageID: pid(5), PageSlug: slug("members"), PageStatus: status(content.StatusPublished), PageVisibility: &private},
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

	// Public render of /about: draft and private items dropped (top-level
	// and inside the dropdown), the empty dropdown dropped, Active on About.
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

	// Editors see draft-page and private-page items and empty dropdowns
	// (to fill them).
	editorMain := BuildMenus(items, "", "en", "en", true)["main"]
	if len(editorMain) != 7 {
		t.Fatalf("editor main menu = %d items %v, want 7", len(editorMain), editorMain)
	}
	if !editorMain[0].Active {
		t.Error("home entry should be active when rendering the home page")
	}
	if editorMain[1].Label != "Members" || editorMain[1].URL != "/members" {
		t.Errorf("editor should keep the private-page entry: %+v", editorMain[1])
	}
	if len(editorMain[5].Children) != 2 {
		t.Errorf("editor dropdown should keep its draft child: %+v", editorMain[5])
	}
	if editorMain[6].Label != "Empty" || len(editorMain[6].Children) != 0 {
		t.Errorf("editor should keep the empty dropdown: %+v", editorMain[6])
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
		{Region: "extra", Kind: content.KindHTML, Sort: 2, Content: "<p>Third</p>",
			Settings: map[string]string{"bg": "dark", "width": "full", "corners": "medium"}},
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
	if !strings.Contains(out, `<section class="bg-slate-900 rounded-2xl">`) {
		t.Errorf("corner class missing from rounded section wrapper:\n%s", out)
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
	if !strings.Contains(out, `data-cms-corners="medium"`) {
		t.Errorf("edit render missing corners marker:\n%s", out)
	}
	if !strings.Contains(out, `data-cms-corners="none"`) {
		t.Errorf("edit render missing default corners marker:\n%s", out)
	}
	if !strings.Contains(out, "data-section-styles=") {
		t.Errorf("section styles JSON missing from script tag:\n%s", out)
	}
}

func TestRenderPostsData(t *testing.T) {
	fsys := fstest.MapFS{
		"pages/post.gohtml": &fstest.MapFile{Data: []byte(
			`{{with .Post}}<img src="{{.ThumbnailURL}}"><time>{{.PublishedAt.Format "2006-01-02"}}</time><i>{{.Author}}</i>{{end}}` +
				`<ul>{{range cmsPosts "blog" 5}}<li{{if .Draft}} class="draft"{{end}}><a href="{{.URL}}">{{.Title}}</a> {{.Summary}}</li>{{end}}</ul>`)},
	}
	r, err := New(fsys, nil, []PageTemplate{{File: "pages/post.gohtml", Label: "Post"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	page := &content.Page{ID: 1, TemplateName: "pages/post.gohtml", Title: "Launch"}
	post := &content.Post{
		Page:         content.Page{Slug: "blog/launch", Title: "Launch", Description: "We shipped", Status: content.StatusPublished},
		Feed:         content.FeedBlog,
		ThumbnailURL: "/cms/media/thumb.webp",
	}
	post.PublishedAt = time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	post.AuthorName = "Pat Writer"

	// cmsPosts is the unpaginated func: no offset, and no count, since
	// nothing downstream of it needs the feed's length.
	lister := func(feed string, limit, offset int, count bool) ([]PostInfo, int) {
		if feed != "blog" || limit != 5 || offset != 0 || count {
			t.Errorf("lister called with (%q, %d, %d, %v)", feed, limit, offset, count)
		}
		return []PostInfo{
			{Title: "Launch", Summary: "We shipped", URL: "/blog/launch"},
			{Title: "WIP", URL: "/blog/wip", Draft: true},
		}, 0
	}

	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Locale: "en", Post: PostInfoFor(post, "", nil), Posts: lister}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<img src="/cms/media/thumb.webp">`,
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

// A post set to publish without a byline reports no author at all, so
// the {{with .Author}} every template already writes drops the byline
// and keeps the date. The name stays recorded — the editor's gear needs
// it to say whose byline is switched off.
func TestRenderPostWithoutByline(t *testing.T) {
	post := &content.Post{
		Page: content.Page{Slug: "news/notice", Title: "Notice"},
		Feed: content.FeedNews,
	}
	post.AuthorName = "Pat Writer"
	post.PublishedAt = time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	info := PostInfoFor(post, "", nil)
	if info.Author != "Pat Writer" {
		t.Errorf("bylined post: Author = %q, want the author", info.Author)
	}

	post.HideAuthor = true
	info = PostInfoFor(post, "", nil)
	if info.Author != "" {
		t.Errorf("Author = %q, want empty so {{with .Author}} prints nothing", info.Author)
	}
	if info.AuthorName != "Pat Writer" || !info.HideAuthor {
		t.Errorf("AuthorName/HideAuthor = %q/%v, want the recorded author and the flag",
			info.AuthorName, info.HideAuthor)
	}
	if !info.PublishedAt.Equal(post.PublishedAt) {
		t.Error("hiding the byline changed the date")
	}
}

// cmsDate writes the date in the language the page is rendered in. A
// French page formatting its date with Go's own .Format would show the
// month in English, which is the bug the func exists to remove.
func TestRenderDateFollowsLocale(t *testing.T) {
	fsys := fstest.MapFS{
		"pages/post.gohtml": &fstest.MapFile{Data: []byte(
			`{{with .Post}}<time>{{cmsDate .PublishedAt}}</time><b>{{cmsDate .PublishedAt "short"}}</b>{{end}}`)},
	}
	r, err := New(fsys, nil, []PageTemplate{{File: "pages/post.gohtml", Label: "Post"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	page := &content.Page{ID: 1, TemplateName: "pages/post.gohtml", Title: "Launch"}
	post := &content.Post{Page: content.Page{Slug: "blog/launch", Title: "Launch"}, Feed: content.FeedBlog}
	post.PublishedAt = time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)

	for locale, want := range map[string]string{
		"en": "<time>July 30, 2026</time><b>Jul 30, 2026</b>",
		"fr": "<time>30 juillet 2026</time><b>30 juil. 2026</b>",
	} {
		var buf bytes.Buffer
		if err := r.Render(&buf, Input{Page: page, Locale: locale, Post: PostInfoFor(post, "", nil)}); err != nil {
			t.Fatalf("Render(%s): %v", locale, err)
		}
		if got := buf.String(); got != want {
			t.Errorf("locale %s: got %q, want %q", locale, got, want)
		}
	}
}

// A post's meta description is what the page publishes to search
// engines; its summary is what listings show. cmsHead prints the first
// and falls back to the second, so a post that sets none is unchanged.
func TestRenderMetaDescriptionPrefersItsOwn(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 5, TemplateName: "pages/home.gohtml", Title: "Launch",
		Description: "We shipped it"}

	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Locale: "en"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), `<meta name="description" content="We shipped it">`) {
		t.Errorf("without its own meta description, want the summary:\n%s", buf.String())
	}

	page.MetaDescription = "How we shipped, and what broke"
	buf.Reset()
	if err := r.Render(&buf, Input{Page: page, Locale: "en"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `<meta name="description" content="How we shipped, and what broke">`) {
		t.Errorf("want the page's own meta description:\n%s", out)
	}
	if strings.Contains(out, `content="We shipped it"`) {
		t.Error("the summary was published as the meta description too")
	}
}

// pagedRenderer parses a template that exercises cmsFeed's numbers and
// cmsPagination's markup together.
func pagedRenderer(t *testing.T) *Renderer {
	t.Helper()
	fsys := fstest.MapFS{
		"pages/blog.gohtml": &fstest.MapFile{Data: []byte(
			`{{$f := cmsFeed "blog"}}<p>page {{$f.Page}}/{{$f.TotalPages}} of {{$f.Total}} per {{$f.PerPage}}</p>` +
				`<ul>{{range $f.Posts}}<li>{{.Title}}</li>{{end}}</ul>{{cmsPagination $f}}`)},
	}
	r, err := New(fsys, nil, []PageTemplate{{File: "pages/blog.gohtml", Label: "Blog"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// pagedLister stands in for a feed of total posts, returning the window it
// is asked for and recording the calls so a test can assert the offset.
func pagedLister(total int, calls *[][3]int) PostLister {
	return func(feed string, limit, offset int, count bool) ([]PostInfo, int) {
		*calls = append(*calls, [3]int{limit, offset, btoi(count)})
		var out []PostInfo
		for i := offset; i < offset+limit && i < total; i++ {
			out = append(out, PostInfo{Title: "Post " + strconv.Itoa(i)})
		}
		if !count {
			return out, 0
		}
		return out, total
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestRenderFeedPagination(t *testing.T) {
	r := pagedRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/blog.gohtml", Title: "Blog"}
	render := func(in Input) string {
		t.Helper()
		in.Page, in.Locale = page, "en"
		var buf bytes.Buffer
		if err := r.Render(&buf, in); err != nil {
			t.Fatalf("Render: %v", err)
		}
		return buf.String()
	}

	// Page two of 25 posts, five to a page: the lister is asked for the
	// right window, and asked to count, exactly once.
	var calls [][3]int
	out := render(Input{Posts: pagedLister(25, &calls), PostsPerPage: 5, PageNumber: 2})
	if len(calls) != 1 || calls[0] != [3]int{5, 5, 1} {
		t.Errorf("lister calls = %v, want one (limit 5, offset 5, count)", calls)
	}
	if !strings.Contains(out, "<p>page 2/5 of 25 per 5</p>") {
		t.Errorf("wrong page numbers:\n%s", out)
	}
	for _, want := range []string{
		`<li>Post 5</li>`, `<li>Post 9</li>`,
		`<a class="cms-pager-step cms-pager-prev" href="?page=1" rel="prev">Previous</a>`,
		`<a class="cms-pager-step cms-pager-next" href="?page=3" rel="next">Next</a>`,
		`<span class="cms-pager-link cms-pager-current" aria-current="page">2</span>`,
		`<a class="cms-pager-link" href="?page=4" aria-label="Page 4">4</a>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Post 4") || strings.Contains(out, "Post 10") {
		t.Errorf("page 2 leaked a neighbouring page's posts:\n%s", out)
	}

	// The ends of the feed have no link to step to, and say so without
	// dropping the button.
	calls = nil
	out = render(Input{Posts: pagedLister(25, &calls), PostsPerPage: 5, PageNumber: 1})
	if !strings.Contains(out, `<span class="cms-pager-step cms-pager-prev cms-pager-off" aria-hidden="true">Previous</span>`) {
		t.Errorf("first page should have a dead Previous:\n%s", out)
	}
	calls = nil
	out = render(Input{Posts: pagedLister(25, &calls), PostsPerPage: 5, PageNumber: 5})
	if !strings.Contains(out, `cms-pager-next cms-pager-off`) {
		t.Errorf("last page should have a dead Next:\n%s", out)
	}

	// A ?page= past the end clamps to the last real page and refetches,
	// rather than showing an empty listing.
	calls = nil
	out = render(Input{Posts: pagedLister(25, &calls), PostsPerPage: 5, PageNumber: 99})
	if !strings.Contains(out, "<p>page 5/5 of 25 per 5</p>") || !strings.Contains(out, "<li>Post 20</li>") {
		t.Errorf("page past the end did not clamp to the last page:\n%s", out)
	}
	if len(calls) != 2 || calls[1] != [3]int{5, 20, 0} {
		t.Errorf("clamping calls = %v, want a second fetch at offset 20", calls)
	}

	// A feed that fits on one page is one page, with no bar at all.
	calls = nil
	out = render(Input{Posts: pagedLister(3, &calls), PostsPerPage: 5})
	if !strings.Contains(out, "<p>page 1/1 of 3 per 5</p>") || strings.Contains(out, "cms-pager") {
		t.Errorf("single-page feed should render no pagination:\n%s", out)
	}

	// An empty feed is still one page, not zero.
	calls = nil
	if out = render(Input{Posts: pagedLister(0, &calls), PostsPerPage: 5}); !strings.Contains(out, "<p>page 1/1 of 0 per 5</p>") {
		t.Errorf("empty feed:\n%s", out)
	}

	// Without a lister the funcs are inert but must not panic on the
	// FeedPage they hand back.
	if out = render(Input{}); !strings.Contains(out, "<p>page 1/1 of 0 per "+strconv.Itoa(DefaultPostsPerPage)+"</p>") {
		t.Errorf("render without a lister:\n%s", out)
	}
}

func TestRenderFeedPerPageAndPageURL(t *testing.T) {
	// The template's own page size beats Input.PostsPerPage, and PageURL
	// builds the links.
	fsys := fstest.MapFS{
		"pages/blog.gohtml": &fstest.MapFile{Data: []byte(
			`{{$f := cmsFeed "blog" 2}}{{$f.PerPage}}|{{$f.TotalPages}}|{{$f.PrevURL}}|{{$f.NextURL}}` +
				`{{range $f.Links}}[{{if .Ellipsis}}...{{else}}{{.Number}}={{.URL}}{{end}}]{{end}}`)},
	}
	r, err := New(fsys, nil, []PageTemplate{{File: "pages/blog.gohtml", Label: "Blog"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var calls [][3]int
	var buf bytes.Buffer
	if err := r.Render(&buf, Input{
		Page: &content.Page{ID: 1, TemplateName: "pages/blog.gohtml"}, Locale: "en",
		Posts: pagedLister(9, &calls), PostsPerPage: 100, PageNumber: 3,
		PageURL: func(n int) string { return "/blog?p=" + strconv.Itoa(n) },
	}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	// 9 posts at the template's 2 per page is 5 pages, not 1 at 100.
	if !strings.HasPrefix(out, "2|5|/blog?p=2|/blog?p=4") {
		t.Errorf("per-page override or step URLs wrong:\n%s", out)
	}
	if want := "[1=/blog?p=1][2=/blog?p=2][3=/blog?p=3][4=/blog?p=4][5=/blog?p=5]"; !strings.Contains(out, want) {
		t.Errorf("links = %s, want %s", out, want)
	}
}

// NewPager is the shared entry point both the public listings and the
// admin's tables build their bar from, so its arithmetic — page clamping,
// offsets, the empty case — has to hold on its own.
func TestNewPager(t *testing.T) {
	url := func(n int) string { return "/x?page=" + strconv.Itoa(n) }

	// 47 items at 10 a page is 5 pages, the last one short.
	p := NewPager(3, 10, 47, url)
	if p.Page != 3 || p.TotalPages != 5 || p.Offset() != 20 || p.Total != 47 {
		t.Errorf("page 3 of 47/10: %+v (offset %d)", p, p.Offset())
	}
	if p.PrevURL != "/x?page=2" || p.NextURL != "/x?page=4" {
		t.Errorf("step URLs: prev=%q next=%q", p.PrevURL, p.NextURL)
	}
	if !p.HasPages() {
		t.Error("5 pages should report HasPages")
	}

	// Both ends: no link off the edge.
	if first := NewPager(1, 10, 47, url); first.PrevURL != "" || first.NextURL == "" {
		t.Errorf("first page: prev=%q next=%q", first.PrevURL, first.NextURL)
	}
	if last := NewPager(5, 10, 47, url); last.NextURL != "" || last.PrevURL == "" {
		t.Errorf("last page: prev=%q next=%q", last.PrevURL, last.NextURL)
	}

	// Out of range in either direction clamps into it, so Offset never
	// points off the end of the list (or before its start).
	for _, asked := range []int{6, 99, 0, -4} {
		got := NewPager(asked, 10, 47, url)
		if got.Page < 1 || got.Page > got.TotalPages {
			t.Errorf("page %d clamped to %d, outside 1..%d", asked, got.Page, got.TotalPages)
		}
		if got.Offset() < 0 || got.Offset() >= got.Total {
			t.Errorf("page %d gave offset %d, outside 0..%d", asked, got.Offset(), got.Total)
		}
	}

	// An empty list is one empty page, not zero pages, and draws no bar.
	empty := NewPager(1, 10, 0, url)
	if empty.TotalPages != 1 || empty.Page != 1 || empty.Offset() != 0 {
		t.Errorf("empty list: %+v", empty)
	}
	if empty.HasPages() || empty.HTML() != "" {
		t.Errorf("empty list should draw no bar, got %q", empty.HTML())
	}

	// A nonsense page size falls back rather than dividing by zero.
	if got := NewPager(1, 0, 30, url); got.PerPage != DefaultPostsPerPage {
		t.Errorf("perPage 0 fell back to %d, want %d", got.PerPage, DefaultPostsPerPage)
	}
	// A nil URL builder still yields usable links.
	if got := NewPager(1, 10, 30, nil); got.NextURL != "?page=2" {
		t.Errorf("nil url builder gave next=%q, want ?page=2", got.NextURL)
	}
	// Default labels are English; the admin overwrites them.
	if got := NewPager(1, 10, 30, url); got.PrevLabel != "Previous" || got.NextLabel != "Next" {
		t.Errorf("labels = %q/%q", got.PrevLabel, got.NextLabel)
	}
}

func TestPageLinksEllipsizeLongFeeds(t *testing.T) {
	// Twenty pages: the ends, the current page and two either side, and
	// an ellipsis for each gap.
	p := &Pager{Page: 10, TotalPages: 20}
	var got []string
	for _, l := range pageLinks(p, func(n int) string { return "" }) {
		if l.Ellipsis {
			got = append(got, "...")
			continue
		}
		got = append(got, strconv.Itoa(l.Number))
	}
	want := []string{"1", "...", "8", "9", "10", "11", "12", "...", "20"}
	if !slices.Equal(got, want) {
		t.Errorf("pageLinks(10 of 20) = %v, want %v", got, want)
	}

	// Near the start there is no gap to elide on the left.
	p = &Pager{Page: 2, TotalPages: 20}
	got = nil
	for _, l := range pageLinks(p, func(n int) string { return "" }) {
		if l.Ellipsis {
			got = append(got, "...")
			continue
		}
		got = append(got, strconv.Itoa(l.Number))
	}
	want = []string{"1", "2", "3", "4", "...", "20"}
	if !slices.Equal(got, want) {
		t.Errorf("pageLinks(2 of 20) = %v, want %v", got, want)
	}

	// A short feed shows every page, ungapped.
	p = &Pager{Page: 1, TotalPages: 4}
	if links := pageLinks(p, func(n int) string { return "" }); len(links) != 4 {
		t.Errorf("pageLinks(1 of 4) returned %d links, want 4", len(links))
	}
	// One page is no bar.
	if links := pageLinks(&Pager{Page: 1, TotalPages: 1}, func(int) string { return "" }); links != nil {
		t.Errorf("pageLinks(1 of 1) = %v, want none", links)
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

// A shared region is the site's content rather than the page's: it renders
// on every page from one stored copy, falls back to the template's own
// markup while nobody has filled it, and never appears among the page's
// regions.
func TestRenderSharedRegion(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 4, TemplateName: "pages/home.gohtml", Title: "Home"}

	// Nothing stored: the template's fallback markup stands in.
	var buf bytes.Buffer
	if err := r.Render(&buf, Input{Page: page, Locale: "en"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "<p>&copy; Acme</p>") {
		t.Errorf("empty shared region did not fall back to the template's markup:\n%s", buf.String())
	}

	// Stored content replaces it, and comes from Shared rather than Blocks.
	shared := []content.Block{{PageID: 99, Region: "footer", Kind: content.KindHTML,
		Content: `<p>Call <a href="/contact">us</a></p>`}}
	buf.Reset()
	if err := r.Render(&buf, Input{Page: page, Shared: shared, Locale: "en"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `<p>Call <a href="/contact">us</a></p>`) {
		t.Errorf("shared content missing:\n%s", out)
	}
	if strings.Contains(out, "&copy; Acme") {
		t.Error("fallback rendered even though the region has content")
	}

	// A page region named "footer" is a different region: page blocks must
	// not leak into the shared one.
	buf.Reset()
	err := r.Render(&buf, Input{Page: page, Locale: "en",
		Blocks: []content.Block{{Region: "footer", Kind: content.KindHTML, Content: "<p>page footer</p>"}}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(buf.String(), "page footer") {
		t.Error("a page block filled the shared region")
	}
}

// In edit mode a shared region is marked like any other, but its name
// carries the shared prefix — that is what tells the editor to save it to
// the site instead of to the page it is standing on.
func TestRenderSharedRegionEditMarkers(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 4, TemplateName: "pages/home.gohtml", Title: "Home"}
	shared := []content.Block{{Region: "footer", Kind: content.KindHTML, Locale: "en", Content: "<p>Bye</p>"}}

	var buf bytes.Buffer
	err := r.Render(&buf, Input{Page: page, Shared: shared, Locale: "en",
		Edit: &EditInfo{PageID: 4, AdminPath: "/admin", Locale: "en"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), `<div data-cms-region="site:footer" data-cms-kind="html"><p>Bye</p></div>`) {
		t.Errorf("shared region marker missing or unprefixed:\n%s", buf.String())
	}

	// Untranslated shared content is badged the same way page content is.
	buf.Reset()
	err = r.Render(&buf, Input{Page: page, Shared: shared, Locale: "fr",
		Edit: &EditInfo{PageID: 4, AdminPath: "/admin", Locale: "fr"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), `data-cms-region="site:footer" data-cms-kind="html" data-cms-fallback="1"`) {
		t.Errorf("untranslated shared region not badged:\n%s", buf.String())
	}
}

// Shared regions are collected across every template, because the content
// they name is reached from whichever page an editor happens to be on —
// and they are kept out of any one page's region list.
func TestSharedRegionsAreSiteWideAndNotPageRegions(t *testing.T) {
	fsys := fstest.MapFS{
		"base.gohtml": &fstest.MapFile{Data: []byte(
			`{{define "base"}}<html><body>{{block "content" .}}{{end}}` +
				`<footer>{{cmsShared "footer"}}</footer></body></html>{{end}}`)},
		"pages/a.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}{{cmsRegion "main"}}{{end}}`)},
		"pages/b.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}{{cmsShared "contact-strip"}}{{cmsRegion "main"}}{{end}}`)},
	}
	r, err := New(fsys, []string{"base.gohtml"}, []PageTemplate{
		{File: "pages/a.gohtml", Label: "A"}, {File: "pages/b.gohtml", Label: "B"},
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, file := range []string{"pages/a.gohtml", "pages/b.gohtml"} {
		for _, region := range r.Regions(file) {
			if region.Kind == KindShared {
				t.Errorf("%s: Regions returned shared region %q", file, region.Name)
			}
		}
	}

	var names []string
	for _, region := range r.SharedRegions() {
		if region.Kind != KindShared {
			t.Errorf("SharedRegions returned kind %q", region.Kind)
		}
		names = append(names, region.Name)
	}
	// "footer" is declared by the layout both pages share, so it must come
	// back once rather than once per template.
	want := []string{"contact-strip", "footer"}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Errorf("SharedRegions: got %v, want %v", names, want)
	}
}

func TestEmbedCode(t *testing.T) {
	tests := []struct {
		name, code, tag, want string
	}{
		{"empty", "", "style", ""},
		{"whitespace only", "  \n\t", "script", ""},
		{"plain CSS wrapped", "body{color:red}", "style",
			"<style>\nbody{color:red}\n</style>\n"},
		{"plain JS wrapped", "console.log(1)", "script",
			"<script>\nconsole.log(1)\n</script>\n"},
		{"close tag in plain code escaped", `var s = "</script>";`, "script",
			"<script>\nvar s = \"<\\/script>\";\n</script>\n"},
		{"style block verbatim", "<style>body{color:red}</style>", "style",
			"<style>body{color:red}</style>\n"},
		{"link tag verbatim", `<link rel="stylesheet" href="/a.css">`, "style",
			`<link rel="stylesheet" href="/a.css">` + "\n"},
		{"script src verbatim", `<script src="/a.js"></script>`, "script",
			`<script src="/a.js"></script>` + "\n"},
		{"mixed markup and tags verbatim",
			"<script src=\"/lib.js\"></script>\n<script>use(lib)</script>", "script",
			"<script src=\"/lib.js\"></script>\n<script>use(lib)</script>\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closeRe := styleCloseRe
			if tt.tag == "script" {
				closeRe = scriptCloseRe
			}
			if got := embedCode(tt.code, tt.tag, closeRe); got != tt.want {
				t.Errorf("embedCode(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}
