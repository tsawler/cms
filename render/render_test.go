package render

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/content"
)

var testFS = fstest.MapFS{
	"base.tmpl": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><head>{{cmsHead}}</head><body>` +
			`<h1>{{cmsText "site-name"}}</h1>{{block "content" .}}{{end}}{{cmsScripts}}</body></html>{{end}}`)},
	"pages/home.tmpl": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}{{if .Title}}<p>{{cmsText "tagline"}}</p>{{end}}` +
			`<div>{{cmsRegion "main"}}</div>{{end}}`)},
}

func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := New(testFS, []string{"base.tmpl"}, []PageTemplate{{File: "pages/home.tmpl", Label: "Home"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestRegionsWalksIncludedTemplatesAndBranches(t *testing.T) {
	r := newTestRenderer(t)
	regions := r.Regions("pages/home.tmpl")

	want := map[string]string{"site-name": "text", "tagline": "text", "main": "html"}
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
		ID: 1, TemplateName: "pages/home.tmpl", Title: "Home",
		Description: `He said "hi" & left`, HeadCSS: "body{color:red}", BodyJS: "console.log(1)",
	}
	blocks := []content.Block{
		{Region: "site-name", Kind: content.KindText, Content: "Acme <script>alert(1)</script>"},
		{Region: "tagline", Kind: content.KindText, Content: "We build things"},
		{Region: "main", Kind: content.KindHTML, Content: "<p class=\"lead\">Hello</p>"},
	}

	var buf bytes.Buffer
	if err := r.Render(&buf, page, blocks, "en", nil); err != nil {
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
	page := &content.Page{ID: 1, TemplateName: "pages/home.tmpl", Title: "Home"}
	var buf bytes.Buffer
	if err := r.Render(&buf, page, nil, "en", nil); err != nil {
		t.Fatalf("Render with no blocks: %v", err)
	}
	if !strings.Contains(buf.String(), "<div></div>") {
		t.Errorf("empty region should render empty, got:\n%s", buf.String())
	}
}

func TestRenderEditModeMarksRegionsAndInjectsScript(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 7, TemplateName: "pages/home.tmpl", Title: "Home"}
	blocks := []content.Block{
		{Region: "site-name", Kind: content.KindText, Content: "Acme <sneaky>"},
		{Region: "main", Kind: content.KindHTML, Content: "<p>Hello</p>"},
	}

	var buf bytes.Buffer
	err := r.Render(&buf, page, blocks, "en", &EditInfo{
		PageID: 7, AdminPath: "/admin", CSRFToken: "tok123", Locale: "en",
		Status: "draft", MediaEnabled: true,
		Styles: []EditorStyle{{Label: "Red", Class: "text-red-600"}},
	})
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
	if err := r.Render(&buf, page, blocks, "en", nil); err != nil {
		t.Fatalf("plain Render: %v", err)
	}
	if strings.Contains(buf.String(), "data-cms-region") || strings.Contains(buf.String(), EditorScriptPath) {
		t.Error("plain render leaked editor markers")
	}
}

func TestNewRejectsUnknownPageTemplate(t *testing.T) {
	_, err := New(testFS, nil, []PageTemplate{{File: "pages/missing.tmpl", Label: "X"}})
	if err == nil {
		t.Fatal("expected error for missing page template file")
	}
}
