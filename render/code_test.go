package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
)

// lookup is a CodeLookup over a fixed library, counting the keys it was
// asked for so a test can prove the renderer only asks about
// placeholders it actually found.
func lookup(library map[string]string, asked *[]string) CodeLookup {
	return func(key string) (string, bool) {
		if asked != nil {
			*asked = append(*asked, key)
		}
		html, ok := library[key]
		return html, ok && html != ""
	}
}

// TestExpandCodeFillsPlaceholder covers the shape the editor stores: an
// empty div naming a library key, which a public render fills with that
// entry's markup while keeping the div itself as the wrapper — the
// anchor a block's own script finds itself by.
func TestExpandCodeFillsPlaceholder(t *testing.T) {
	var asked []string
	lu := lookup(map[string]string{
		"widget": `<p>hi</p><script>console.log("go")</script>`,
	}, &asked)

	in := `<p>before</p><div class="cms-snippet cms-code" data-cms-code="widget"></div><p>after</p>`
	got := expandCode(in, lu)

	if !strings.Contains(got, `<script>console.log("go")</script>`) {
		t.Errorf("the library markup was not written into the page:\n%s", got)
	}
	if !strings.Contains(got, `<div class="cms-snippet cms-code" data-cms-code="widget"><p>hi</p>`) {
		t.Errorf("the placeholder's own tag should have survived as the wrapper:\n%s", got)
	}
	if !strings.Contains(got, "<p>before</p>") || !strings.Contains(got, "<p>after</p>") {
		t.Errorf("content around the block was disturbed:\n%s", got)
	}
	if len(asked) != 1 || asked[0] != "widget" {
		t.Errorf("keys looked up = %v, want [widget]", asked)
	}
}

// TestExpandCodeToleratesEditorFiller covers what a save actually
// stores. TinyMCE pads an empty block element when it serializes one, so
// the placeholder comes back holding a &nbsp; (and sometimes a <br>).
// That is filler, not content, and the block still has to expand — this
// is the case that broke a first pass at the pattern.
func TestExpandCodeToleratesEditorFiller(t *testing.T) {
	lu := lookup(map[string]string{"a": "<i>A</i>"}, nil)
	fillers := []string{
		"&nbsp;",
		"&#160;",
		"\n  ",
		"<br>",
		`<br data-mce-bogus="1">`,
		"&nbsp;<br>\n",
	}
	for _, f := range fillers {
		in := `<div class="cms-snippet cms-code" data-cms-code="a">` + f + `</div>`
		got := expandCode(in, lu)
		if !strings.Contains(got, "<i>A</i>") {
			t.Errorf("filler %q stopped the block expanding:\n%s", f, got)
		}
		if strings.Contains(got, f) && f != "\n  " {
			t.Errorf("filler %q survived into the page:\n%s", f, got)
		}
	}
}

// TestExpandCodeUnknownKey pins what a deleted (or never created) entry
// does: the placeholder stays as it is and renders as nothing, rather
// than the page failing or the key leaking into the output as text.
func TestExpandCodeUnknownKey(t *testing.T) {
	in := `<div class="cms-code" data-cms-code="gone"></div>`
	got := expandCode(in, lookup(map[string]string{}, nil))
	if got != in {
		t.Errorf("an unknown key changed the content:\ngot  %s\nwant %s", got, in)
	}
	// An entry that exists but is empty is the same case.
	got = expandCode(in, lookup(map[string]string{"gone": ""}, nil))
	if got != in {
		t.Errorf("an empty entry changed the content:\n%s", got)
	}
}

// TestExpandCodeRepeatedAndOrdered covers a page using the same block
// twice and two different blocks: every placeholder is filled, each in
// its own position.
func TestExpandCodeRepeatedAndOrdered(t *testing.T) {
	lu := lookup(map[string]string{"a": "<i>A</i>", "b": "<i>B</i>"}, nil)
	in := `<div data-cms-code="a"></div><div data-cms-code="b"></div><div data-cms-code="a"></div>`
	got := expandCode(in, lu)
	if n := strings.Count(got, "<i>A</i>"); n != 2 {
		t.Errorf("block a was expanded %d times, want 2:\n%s", n, got)
	}
	if !strings.Contains(got, "<i>A</i></div><div data-cms-code=\"b\"><i>B</i>") {
		t.Errorf("blocks came out in the wrong order:\n%s", got)
	}
}

// TestExpandCodeLeavesOtherMarkup keeps the pattern narrow: a div that
// grew content is not a placeholder, an out-of-vocabulary key is not one
// either, and content with no placeholder at all is returned untouched.
func TestExpandCodeLeavesOtherMarkup(t *testing.T) {
	lu := lookup(map[string]string{"a": "<i>A</i>", "Bad Key": "<i>nope</i>"}, nil)
	cases := []string{
		`<div data-cms-code="a">someone typed here</div>`,
		`<div data-cms-code="Bad Key"></div>`,
		`<p>no code blocks here at all</p>`,
	}
	for _, in := range cases {
		if got := expandCode(in, lu); got != in {
			t.Errorf("content was rewritten:\ngot  %s\nwant %s", got, in)
		}
	}
}

// TestExpandCodeBlocksCopies proves the caller's blocks are not written
// through: the same slice is rendered publicly and, in edit mode, handed
// back to the editor, and the editor must still receive placeholders.
func TestExpandCodeBlocksCopies(t *testing.T) {
	blocks := []content.Block{
		{Region: "main", Content: `<div data-cms-code="a"></div>`},
		{Region: "side", Content: "<p>plain</p>"},
	}
	out := expandCodeBlocks(blocks, lookup(map[string]string{"a": "<i>A</i>"}, nil))

	if !strings.Contains(out[0].Content, "<i>A</i>") {
		t.Errorf("the block was not expanded: %s", out[0].Content)
	}
	if strings.Contains(blocks[0].Content, "<i>A</i>") {
		t.Errorf("the caller's slice was written through: %s", blocks[0].Content)
	}
	if out[1].Content != "<p>plain</p>" {
		t.Errorf("an untouched block changed: %s", out[1].Content)
	}
	// Nothing to expand hands the same slice straight back.
	plain := []content.Block{{Content: "<p>plain</p>"}}
	if got := expandCodeBlocks(plain, lookup(nil, nil)); &got[0] != &plain[0] {
		t.Error("a render with no code blocks should not copy the block slice")
	}
}

// TestExpandCodeNilLookup covers the host that configured no library:
// placeholders stay put rather than the renderer panicking.
func TestExpandCodeNilLookup(t *testing.T) {
	in := `<div data-cms-code="a"></div>`
	if got := expandCode(in, nil); got != in {
		t.Errorf("a nil lookup changed the content: %s", got)
	}
}

// TestRenderExpandsCodeBlocksOnlyWhenPublic is the whole contract end to
// end: a public render runs the block, an edit render of the same
// content hands the editor back the placeholder it saved — nothing
// executes in the page being edited, and a save cannot bake the expanded
// markup into stored content.
func TestRenderExpandsCodeBlocksOnlyWhenPublic(t *testing.T) {
	r := newTestRenderer(t)
	page := &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"}
	blocks := []content.Block{
		{Region: "main", Kind: content.KindHTML,
			Content: `<div class="cms-snippet cms-code" data-cms-code="widget"></div>`},
		{Region: "extra", Kind: content.KindHTML,
			Content: `<div class="cms-code" data-cms-code="widget"></div>`},
	}
	shared := []content.Block{
		{Region: "footer", Kind: content.KindHTML,
			Content: `<div class="cms-code" data-cms-code="widget"></div>`},
	}
	in := Input{
		Page: page, Blocks: blocks, Shared: shared, Locale: "en",
		CodeSnippets: lookup(map[string]string{"widget": `<script>ran()</script>`}, nil),
	}

	var buf bytes.Buffer
	if err := r.Render(&buf, in); err != nil {
		t.Fatalf("Render: %v", err)
	}
	// A region, a section, and a shared region: every place block
	// content reaches the page.
	if n := strings.Count(buf.String(), "<script>ran()</script>"); n != 3 {
		t.Errorf("public render ran the block %d times, want 3:\n%s", n, buf.String())
	}

	buf.Reset()
	in.Edit = &EditInfo{PageID: 1}
	if err := r.Render(&buf, in); err != nil {
		t.Fatalf("Render (edit): %v", err)
	}
	if strings.Contains(buf.String(), "<script>ran()</script>") {
		t.Errorf("an edit render expanded a code block:\n%s", buf.String())
	}
	if n := strings.Count(buf.String(), `data-cms-code="widget"`); n != 3 {
		t.Errorf("edit render shows %d placeholders, want 3:\n%s", n, buf.String())
	}
}
