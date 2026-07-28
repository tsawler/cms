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
		// Block-gear output: curated spacing preset, background color,
		// and corner roundness as inline styles on the snippet root.
		`<div class="cms-snippet" data-cms-snip-spacing="roomy" style="background-color: #112233; ` +
		`padding: 40px; margin-top: 40px; margin-bottom: 40px; border-radius: 12px; color: #ffee99">` +
		`<blockquote style="color: inherit">block</blockquote></div>` +
		`<div data-cms-snip-spacing="huge" style="padding: 41vh; margin-top: -20px">bad block</div>` +
		`<span style="background-color: #112233">not a block</span>` +
		`<a href="/z" data-cms-btn-size="huge">bad size</a>` +
		`<a href="https://example.com" target="_blank" rel="noopener">new tab</a>` +
		`<a href="/w" target="_evil" rel="opener stylesheet">bad target</a>` +
		`<script>alert(1)</script>` +
		`<img src=x onerror=alert(2)>` +
		`<a href="javascript:alert(3)">bad</a>` +
		// Videos inserted by the editor: sources bound to media-style
		// URLs, controls in both boolean serializations, bounded preload.
		`<video controls preload="metadata" src="/cms/media/vid789/original.mp4" poster="/cms/media/vid789/web.webp"></video>` +
		`<video controls="controls" src="https://bucket.example.com/media/v/original.webm" width="1280" height="720"></video>` +
		`<video src="javascript:alert(7)" poster="data:text/html,x" preload="evil" onplay="alert(8)"></video>` +
		// Video-slot placeholders and the embeds that replace them: only
		// YouTube/Vimeo player URLs may ride in an iframe.
		`<div class="cms-video-slot not-prose" data-cms-video-slot=""><p>Click to add a video</p></div>` +
		`<iframe class="w-full aspect-video" src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ" title="Video" loading="lazy" allow="fullscreen; picture-in-picture" allowfullscreen=""></iframe>` +
		`<iframe src="https://player.vimeo.com/video/76979871" allowfullscreen="allowfullscreen"></iframe>` +
		`<iframe src="https://evil.example.com/embed/xyz"></iframe>` +
		`<iframe src="https://www.youtube.com/embed/dQw4w9WgXcQ" srcdoc="<script>alert(9)</script>" sandbox="allow-scripts" onload="alert(10)"></iframe>`

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
		// Block-gear styles must survive on snippet block roots, and the
		// text-color override's inherit pins on any descendant.
		`data-cms-snip-spacing="roomy"`,
		`background-color: #112233`,
		`padding: 40px`,
		`margin-top: 40px`,
		`border-radius: 12px`,
		`color: #ffee99`,
		`<blockquote style="color: inherit">`,
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
		// Editor-inserted videos keep their player attributes.
		`src="/cms/media/vid789/original.mp4"`,
		`poster="/cms/media/vid789/web.webp"`,
		`preload="metadata"`,
		`src="https://bucket.example.com/media/v/original.webm"`,
		`width="1280"`,
		// Video slots and YouTube/Vimeo embeds survive.
		`data-cms-video-slot=""`,
		`src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"`,
		`allow="fullscreen; picture-in-picture"`,
		`allowfullscreen=""`,
		`src="https://player.vimeo.com/video/76979871"`,
		`src="https://www.youtube.com/embed/dQw4w9WgXcQ"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sanitized output lost %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"<script", "onerror", "javascript:", "position", "color: red", "50vh",
		`data-height="junk"`, "url(", `data-cms-btn-size="huge"`, "_evil", "opener stylesheet",
		`data-cms-snip-spacing="huge"`, "41vh", "-20px", "<span style",
		"onload", "data:text/html", `preload="evil"`, "onplay",
		"evil.example.com", "srcdoc", "sandbox"} {
		if strings.Contains(out, banned) {
			t.Errorf("sanitized output kept %q:\n%s", banned, out)
		}
	}
}
