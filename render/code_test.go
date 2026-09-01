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

// TestRenderParksCodeBlockScriptsWhileEditing is the whole contract end
// to end: both renders fill the block in, so a logged-in editor sees the
// same page a visitor does — but an edit render parks the scripts under
// a type no browser runs, so nothing executes in the page being edited.
// The placeholder's own tag survives either way, which is what lets the
// editor empty the block again and save back what it was handed.
func TestRenderParksCodeBlockScriptsWhileEditing(t *testing.T) {
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
		CodeSnippets: lookup(map[string]string{
			"widget": `<b>hi</b><script>ran()</script>`,
		}, nil),
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
	out := buf.String()
	if strings.Contains(out, "<script>ran()</script>") {
		t.Errorf("an edit render left a code block's script runnable:\n%s", out)
	}
	if n := strings.Count(out, `<script type="`+InertScriptType+`">ran()</script>`); n != 3 {
		t.Errorf("edit render parked %d scripts, want 3:\n%s", n, out)
	}
	// The markup around the script still renders — that is the point of
	// filling the block in at all.
	if n := strings.Count(out, "<b>hi</b>"); n != 3 {
		t.Errorf("edit render showed the block's markup %d times, want 3:\n%s", n, out)
	}
	if n := strings.Count(out, `data-cms-code="widget"`); n != 3 {
		t.Errorf("edit render shows %d placeholders, want 3:\n%s", n, out)
	}
}

// TestInertScripts covers the tag rewriting itself: the type an author
// wrote is preserved for the editor to restore, everything else about
// the tag is left alone, and markup with no script is not touched.
func TestInertScripts(t *testing.T) {
	parked := `<script type="` + InertScriptType + `"`
	cases := []struct {
		name string
		in   string
		want []string
		not  []string
	}{
		{
			name: "bare script",
			in:   `<p>a</p><script>go()</script>`,
			want: []string{`<p>a</p>` + parked + `>go()</script>`},
		},
		{
			name: "keeps other attributes",
			in:   `<script src="/w.js" defer></script>`,
			want: []string{parked + ` src="/w.js" defer>`},
		},
		{
			name: "stashes the author's type",
			in:   `<script type="module">go()</script>`,
			want: []string{parked + ` data-cms-type="module">go()</script>`},
			not:  []string{`<script type="module"`},
		},
		{
			name: "single-quoted and spaced type",
			in:   `<script TYPE = 'text/javascript' id="w">go()</script>`,
			want: []string{`data-cms-type="text/javascript"`, `id="w"`},
		},
		{
			name: "no script, no change",
			in:   `<div class="x">plain</div>`,
			want: []string{`<div class="x">plain</div>`},
			not:  []string{InertScriptType},
		},
		{
			name: "every script in the block",
			in:   `<script>a()</script><b>x</b><script>b()</script>`,
			want: []string{parked + `>a()</script>`, parked + `>b()</script>`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inertScripts(tc.in)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("inertScripts(%q) = %q, want it to contain %q", tc.in, got, w)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("inertScripts(%q) = %q, should not contain %q", tc.in, got, n)
				}
			}
		})
	}
}

// TestInertCodePassesMissingKeysThrough keeps the wrapper honest: a key
// the library does not have still reports false, so the placeholder is
// left exactly as it was rather than filled with nothing.
func TestInertCodePassesMissingKeysThrough(t *testing.T) {
	lu := inertCode(lookup(map[string]string{"a": "<script>x()</script>"}, nil))
	if _, ok := lu("gone"); ok {
		t.Error("a missing key resolved")
	}
	html, ok := lu("a")
	if !ok {
		t.Fatal("a known key did not resolve")
	}
	if !strings.Contains(html, InertScriptType) {
		t.Errorf("a resolved key came back unparked: %q", html)
	}
}

func TestCollapseCodePlaceholdersEmptiesFilledBlocks(t *testing.T) {
	// What a save after Done used to send: the library markup, the
	// button its script made, and the script itself, all inside the
	// placeholder — with nested divs to get past.
	in := `<div class="grid"><p>Words</p>` +
		`<div class="cms-snippet cms-code" data-cms-code="estimate"><div class="cms-code-body"><div>x</div></div>` +
		`<button>Request Estimate</button><script src="https://example.com/w.js"></script></div>` +
		`<p>After</p></div>`
	want := `<div class="grid"><p>Words</p>` +
		`<div class="cms-snippet cms-code" data-cms-code="estimate"></div>` +
		`<p>After</p></div>`
	if got := CollapseCodePlaceholders(in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestCollapseCodePlaceholdersRepeatedAndAlreadyEmpty(t *testing.T) {
	in := `<div data-cms-code="a"></div><p>x</p><DIV class="cms-code" data-cms-code="b">` +
		`<span>y</span></DIV><div data-cms-code="a">&nbsp;</div>`
	want := `<div data-cms-code="a"></div><p>x</p><DIV class="cms-code" data-cms-code="b"></div><div data-cms-code="a"></div>`
	if got := CollapseCodePlaceholders(in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	// Then the render can expand every one of them again.
	lookup := func(key string) (string, bool) { return "<i>" + key + "</i>", true }
	got := expandCode(CollapseCodePlaceholders(in), lookup)
	if strings.Count(got, "<i>a</i>") != 2 || strings.Count(got, "<i>b</i>") != 1 {
		t.Errorf("after collapsing, expansion gave %s", got)
	}
}

func TestCollapseCodePlaceholdersLeavesOtherMarkup(t *testing.T) {
	for _, in := range []string{
		"",
		"<p>no blocks here</p>",
		`<div class="cms-snippet"><div>ordinary nested divs</div></div>`,
		// Unbalanced: nothing to do safely, so nothing is done.
		`<div data-cms-code="a"><div>never closed`,
	} {
		if got := CollapseCodePlaceholders(in); got != in {
			t.Errorf("CollapseCodePlaceholders(%q) = %q, want unchanged", in, got)
		}
	}
}
