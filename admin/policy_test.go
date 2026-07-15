package admin

import (
	"strings"
	"testing"
)

// The editor sanitization policy must strip active content but keep
// everything the in-place editor legitimately produces — including images
// inserted through the TinyMCE upload flow, which use app-relative URLs.
func TestEditorHTMLPolicy(t *testing.T) {
	in := `<h2>Title</h2>` +
		`<p style="text-align: center;">centered</p>` +
		`<p style="text-align: center; position: fixed;">sneaky</p>` +
		`<p style="color: red;">styled</p>` +
		`<p class="fancy">Text <strong>bold</strong> <em>it</em></p>` +
		`<ul><li>one</li></ul>` +
		`<blockquote>quote</blockquote>` +
		`<a href="https://example.com">link</a>` +
		`<a href="/cms/media/abc/q3-report.pdf">the report</a>` +
		`<figure class="not-prose"><blockquote>q</blockquote><figcaption>who</figcaption></figure>` +
		`<img src="/cms/media/abc123/web.jpg" alt="pic" width="800" height="400">` +
		`<img src="https://bucket.example.com/media/x/web.jpg" alt="ext">` +
		`<script>alert(1)</script>` +
		`<img src=x onerror=alert(2)>` +
		`<a href="javascript:alert(3)">bad</a>`

	out := editorHTMLPolicy.Sanitize(in)

	for _, want := range []string{
		"<h2>Title</h2>",
		"text-align: center", // alignment buttons emit this; must survive
		`class="fancy"`,
		"<strong>bold</strong>",
		"<ul><li>one</li></ul>",
		"<blockquote>quote</blockquote>",
		`src="/cms/media/abc123/web.jpg"`,
		`alt="pic"`,
		`src="https://bucket.example.com/media/x/web.jpg"`,
		`href="/cms/media/abc/q3-report.pdf"`,
		`<figure class="not-prose"><blockquote>q</blockquote><figcaption>who</figcaption></figure>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sanitized output lost %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"<script", "onerror", "javascript:", "position", "color"} {
		if strings.Contains(out, banned) {
			t.Errorf("sanitized output kept %q:\n%s", banned, out)
		}
	}
}
