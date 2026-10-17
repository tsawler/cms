package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
)

// asciiLower must keep every byte where it was, or the offsets found in
// its output point somewhere else in the page. strings.ToLower moves
// them: "İ" and the Kelvin sign shrink when lowered, "Ⱥ" grows.
func TestASCIILowerKeepsOffsets(t *testing.T) {
	in := []byte("İstanbul K Ⱥ <BODY Class=\"X\"></Body>")
	got := asciiLower(in)
	if len(got) != len(in) {
		t.Fatalf("length %d, want %d", len(got), len(in))
	}
	if want := []byte("İstanbul K Ⱥ <body class=\"x\"></body>"); !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.ToLower(string(in)) == string(got) {
		t.Fatal("test text no longer changes length under strings.ToLower; pick characters that do")
	}
}

// Text whose lowercase is shorter or longer than itself — Turkish
// dotted capitals, which shrink, and "Ⱥ", which grows — sits ahead of
// </body> on plenty of real pages. The editor script must still land
// whole, right before </body>, with the page's own markup untouched.
func TestEditorScriptLandsBeforeBodyWhateverTheText(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}
	for name, text := range map[string]string{
		"shrinking": strings.Repeat("İstanbul İzmir İskenderun ", 20),
		"growing":   strings.Repeat("Ⱥ", 200),
	} {
		blocks := []content.Block{{Region: "main", Kind: content.KindHTML, Content: "<p>" + text + "</p>"}}
		var buf bytes.Buffer
		err := r.Render(&buf, Input{Page: page, Blocks: blocks, Locale: "en",
			Edit: &EditInfo{PageID: 1, AdminPath: "/admin", Locale: "en"}})
		if err != nil {
			t.Fatalf("%s: Render: %v", name, err)
		}
		out := buf.String()
		start := strings.Index(out, `<script src="`+EditorScriptPath()+`"`)
		if start < 0 {
			t.Fatalf("%s: editor script missing:\n%s", name, out)
		}
		rest := out[start:]
		end := strings.Index(rest, "</script>")
		if end < 0 || !strings.HasPrefix(rest[end+len("</script>"):], "</body>") {
			t.Errorf("%s: editor script not directly before </body>:\n%s", name, rest)
		}
		if !strings.Contains(out, "<p>"+text+"</p>") {
			t.Errorf("%s: the page's own text was cut into", name)
		}
	}
}

// The notice bar goes in just after <body>. Text ahead of it that changes
// length when lowered — a title in Turkish — must not shift where.
func TestBodyTagEndWhateverTheText(t *testing.T) {
	for name, head := range map[string]string{
		"shrinking": "<title>İİİİİİİİ</title>",
		"growing":   "<title>ȺȺȺȺȺȺȺȺ</title>",
	} {
		page := []byte("<html><head>" + head + `</head><BODY class="a>b"><p>hi</p></body></html>`)
		at := bodyTagEnd(page)
		if at < 0 || !strings.HasSuffix(string(page[:at]), `<BODY class="a>b">`) {
			t.Errorf("%s: bodyTagEnd = %d, want just past the body tag", name, at)
		}
	}
}
