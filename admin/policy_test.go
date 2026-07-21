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
		`<div class="cms-spacer" data-height="120px" style="height: 120px"></div>` +
		`<div style="height: 50vh">viewport tricks</div>` +
		`<div data-height="junk">bad badge</div>` +
		`<img src="/cms/media/abc123/web.jpg" alt="pic" width="800" height="400">` +
		`<img src="https://bucket.example.com/media/x/web.jpg" alt="ext">` +
		// Image gear output: alignment/width/style classes on the img,
		// a wrapping link, rendition data attributes, lazy loading, and
		// a caption figure.
		`<figure><a href="/contact" target="_blank" rel="noopener">` +
		`<img src="/cms/media/def456/web.webp" alt="Our office" class="block mx-auto w-1/2 h-auto rounded-lg shadow-md" ` +
		`width="552" height="368" loading="lazy" ` +
		`data-cms-web="/cms/media/def456/web.webp" data-cms-orig="/cms/media/def456/original.jpg"></a>` +
		`<figcaption>Our office in winter</figcaption></figure>` +
		`<img src=y loading="onload=alert(5)" data-cms-orig="javascript:alert(6)" data-cms-web="data:text/html,x">` +
		`<a href="/x" class="cms-btn" data-cms-btn-size="l" style="background-color: rgb(17, 34, 51); ` +
		`color: #ffee88; border: 2px solid #336699; border-radius: 24px; padding: 14px 28px; font-size: 18px;">styled button</a>` +
		`<a href="/y" style="background-color: url(javascript:alert(4)); border-radius: 50vh;">bad button</a>` +
		`<div style="background-color: #112233">not a link</div>` +
		`<a href="/z" data-cms-btn-size="huge">bad size</a>` +
		`<a href="https://example.com" target="_blank" rel="noopener">new tab</a>` +
		`<a href="/w" target="_evil" rel="opener stylesheet">bad target</a>` +
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
		`data-height="120px"`,
		`height: 120px`,
		// Button-editor styles must survive on links (rgb and hex forms).
		`background-color: rgb(17, 34, 51)`,
		`color: #ffee88`,
		`border: 2px solid #336699`,
		`border-radius: 24px`,
		`padding: 14px 28px`,
		`font-size: 18px`,
		`data-cms-btn-size="l"`,
		`target="_blank"`,
		// UGCPolicy appends nofollow to every link's rel.
		`rel="noopener nofollow"`,
		// Image gear: linked, captioned image with alt, style classes,
		// dimensions, lazy loading, and rendition data attributes.
		`href="/contact"`,
		`alt="Our office"`,
		`class="block mx-auto w-1/2 h-auto rounded-lg shadow-md"`,
		`width="552"`,
		`loading="lazy"`,
		`data-cms-web="/cms/media/def456/web.webp"`,
		`data-cms-orig="/cms/media/def456/original.jpg"`,
		`<figcaption>Our office in winter</figcaption>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sanitized output lost %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"<script", "onerror", "javascript:", "position", "color: red", "50vh",
		`data-height="junk"`, "url(", `data-cms-btn-size="huge"`, `<div style`, "_evil", "opener stylesheet",
		"onload", "data:text/html"} {
		if strings.Contains(out, banned) {
			t.Errorf("sanitized output kept %q:\n%s", banned, out)
		}
	}
}
