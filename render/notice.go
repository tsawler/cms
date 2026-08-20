// The site-wide notice bar: a thin strip above everything else on every
// page, carrying the one message the whole site has to say at once — a
// holiday closure, a delivery delay, a service interruption.
//
// The bar is split between the two stores it belongs in. Its *words* are
// an ordinary shared region (NoticeRegion), so they are edited in place,
// translated per locale, sanitized, and published exactly like a footer;
// its *switch and look* are site settings, because "is there a notice
// today" is a state, not content. That split is what keeps a bilingual
// site honest: a settings value has no locale, and a notice that could
// only ever be written once would be wrong on half a site.

package render

import (
	"bytes"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"

	"github.com/tsawler/cms/content"
)

// NoticeRegion is the shared region the notice bar's words live in.
// {{cmsShared "notice"}} reaches the same content — the bar simply
// renders it with its own markup around it.
const NoticeRegion = "notice"

// NoticeStyle is one of the notice bar's curated colour schemes. The key
// is what settings store and what becomes the bar's cms-notice-{key}
// class; the CSS ships with the CMS (noticeCSS) rather than coming from
// the host's Tailwind build, so a bar looks right on a site that has
// never heard of it. A host restyles the bar by overriding .cms-notice,
// the same bargain {{cmsNav}} offers.
type NoticeStyle struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// NoticeStyles are the schemes the site-settings dialog offers, in the
// order it offers them. The first is the default: an unset or unknown
// key resolves to it, so stored settings can never render a bar with no
// colours at all.
var NoticeStyles = []NoticeStyle{
	{Key: "dark", Label: "Dark"},
	{Key: "accent", Label: "Accent (blue)"},
	{Key: "warning", Label: "Warning (amber)"},
	{Key: "alert", Label: "Alert (red)"},
	{Key: "light", Label: "Light"},
}

// ValidNoticeStyle resolves a stored style key to one that exists,
// falling back to the first scheme. Unknown keys are normalized rather
// than refused, the way an unknown section setting is: a key that
// stopped existing should restyle the bar, not break the page.
func ValidNoticeStyle(key string) string {
	for _, s := range NoticeStyles {
		if s.Key == key {
			return s.Key
		}
	}
	return NoticeStyles[0].Key
}

// noticePlaceholder is what an editor sees in a bar that has been
// switched on but never written. It renders only on edit renders — a
// visitor sees no bar at all until there are words for it to carry — so
// it is a starting point to type over rather than something that can
// reach the public site. The editor only submits regions it saw typed
// in, so an untouched placeholder is never stored either.
const noticePlaceholder = `<p>Write the notice here — a holiday closure, a delivery delay, ` +
	`anything the whole site needs to say at once.</p>`

// notice is a page render's notice bar, built once before the template
// runs because three different places need it: {{cmsHead}} (the script
// that hides an already-dismissed bar before it can flash), the template
// or the post-render injection (the markup), and {{cmsScripts}} (the
// close button's handler).
type notice struct {
	// HTML is the bar's markup, or "" when this render has no bar: the
	// setting is off, or it is on with nothing written in it yet.
	HTML string
	// Key identifies the notice's current words, and is empty unless the
	// bar is actually rendering, dismissible, and being served to a
	// visitor. Dismissal is remembered against it, so rewriting the
	// notice shows it again to everyone who closed the last one — which
	// is the whole point of a bar that announces a *new* closure.
	Key string
	// Close wires the close button up at all. It is true wherever the
	// button is drawn, an edit render included: a logged-in editor
	// reading their own site is a visitor with extra powers, and a
	// button that visibly does nothing reads as a broken site rather
	// than as a considered exception. What differs there is Key —
	// nothing is remembered, so the bar is back on the next load and an
	// editor can never lock themselves out of the words they are
	// writing.
	Close bool
}

// buildNotice assembles the bar. text is the notice region's stored
// content, fallbackAttr the untranslated-content marker an edit render
// puts on regions showing the default language, and edit whether this is
// an editable render.
func buildNotice(site content.SiteSettings, text, fallbackAttr string, edit bool) notice {
	if !site.NoticeBar {
		return notice{}
	}
	blank := noticeBlank(text)
	if blank && !edit {
		return notice{}
	}
	if blank {
		text = noticePlaceholder
	}

	cls := "cms-notice cms-notice-" + ValidNoticeStyle(site.NoticeStyle)
	if site.NoticeDismissible {
		cls += " cms-notice-closable"
	}

	var sb strings.Builder
	sb.WriteString(`<div class="` + cls + `" data-cms-notice><div class="cms-notice-inner">`)
	sb.WriteString(`<div class="cms-notice-text"`)
	if edit {
		// The same marker any shared region carries, so the editor
		// attaches TinyMCE to the bar and saves it to the site without
		// knowing the bar exists.
		sb.WriteString(` data-cms-region="` + SharedRegionPrefix + NoticeRegion + `" data-cms-kind="html"`)
		sb.WriteString(fallbackAttr)
	}
	sb.WriteString(`>` + text + `</div>`)
	if site.NoticeDismissible {
		sb.WriteString(`<button type="button" class="cms-notice-close" ` +
			`aria-label="Dismiss this notice">&times;</button>`)
	}
	sb.WriteString(`</div></div>`)

	out := notice{HTML: sb.String(), Close: site.NoticeDismissible}
	if site.NoticeDismissible && !edit && !blank {
		out.Key = noticeKey(text)
	}
	return out
}

// noticeTagRe strips markup, for the "is there anything in here?" test.
var noticeTagRe = regexp.MustCompile(`(?s)<[^>]*>`)

// noticeBlank reports whether stored notice content amounts to nothing.
// Emptying the bar in the editor rarely leaves an empty string: TinyMCE
// keeps the paragraph the words were in and pads it, so what arrives is
// "<p><br></p>" or "<p>&nbsp;</p>". Rendering a bar for that would leave
// a coloured strip across every page with nothing in it, which reads as
// a bug rather than as a notice nobody has written.
func noticeBlank(s string) bool {
	if strings.Contains(s, "<img") {
		return false // a picture is content, even with no words beside it
	}
	s = noticeTagRe.ReplaceAllString(s, "")
	s = strings.NewReplacer("&nbsp;", " ", "&#160;", " ", "&#xa0;", " ").Replace(s)
	return strings.TrimSpace(s) == ""
}

// noticeKey is a short digest of the notice's words: the name a
// dismissal is remembered under. Any edit to the notice changes it, so
// the new message reaches people who closed the old one; it is not a
// security boundary, just an identity, hence FNV rather than a real
// hash.
func noticeKey(text string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return strconv.FormatUint(h.Sum64(), 36)
}

// noticeHideCSS is what an already-dismissed bar is hidden with. It runs
// from <head>, before the bar has been parsed, so a returning visitor
// never sees the notice flash up and vanish — which is what would happen
// if the close was replayed from the bottom of the page.
func noticeHideJS(key string) string {
	return `(function(){try{if(localStorage.getItem('cms-notice')!==` + strconv.Quote(key) + `)return;` +
		`var s=document.createElement('style');` +
		`s.textContent='.cms-notice{display:none!important}';` +
		`document.head.appendChild(s);}catch(e){}})();`
}

// noticeCloseJS wires the bar's close button: take the bar out of the
// page and, when there is a key to remember it under, keep it out.
//
// An empty key is an edit render — the button works, but the dismissal
// is this pageview only. The guard is the other half of that: while
// edit mode is on the bar is a region someone is typing in, and taking
// it out from under them would strand the words and the editor instance
// attached to them. Closing the bar is for reading the page, which is
// what leaving edit mode means.
func noticeCloseJS(key string) string {
	remember := ""
	if key != "" {
		remember = `try{localStorage.setItem('cms-notice',` + strconv.Quote(key) + `);}catch(e){}`
	}
	return `(function(){var n=document.querySelector('.cms-notice');if(!n)return;` +
		`var b=n.querySelector('.cms-notice-close');if(!b)return;` +
		`b.addEventListener('click',function(){` +
		`if(document.body.classList.contains('cms-editing'))return;` +
		remember +
		`n.remove();});})();`
}

// noticeCSS is the bar's own styling, shipped by {{cmsHead}} alongside
// the nav's. Everything is plain classes with ordinary specificity, so a
// host stylesheet overrides any of it.
//
// The words arrive from a rich region, which means they arrive wrapped
// in the block element TinyMCE stored them in — a <p>, and occasionally
// a heading or a list if someone reached for the Styles menu. Flattening
// those margins is what actually keeps the bar one line tall; without it
// the strip inherits a paragraph's vertical rhythm and stops being thin.
const noticeCSS = `.cms-notice{width:100%;box-sizing:border-box;font-size:.875rem;line-height:1.45}` +
	`.cms-notice-inner{position:relative;display:flex;align-items:center;justify-content:center;` +
	`gap:.75rem;max-width:80rem;margin:0 auto;padding:.45rem 1rem}` +
	// Room for the close button at either end, so a long notice cannot
	// run underneath it and the words stay centred between the two.
	`.cms-notice-closable .cms-notice-inner{padding-left:2.5rem;padding-right:2.5rem}` +
	`.cms-notice-text{min-width:0;text-align:center}` +
	`.cms-notice-text :is(p,h1,h2,h3,h4,h5,h6,ul,ol,blockquote){margin:0;padding:0;` +
	`font-size:inherit;line-height:inherit}` +
	`.cms-notice-text :is(ul,ol){list-style:none;display:inline}` +
	`.cms-notice-text :is(ul,ol) li{display:inline;margin-right:.75em}` +
	`.cms-notice a{color:inherit;text-decoration:underline}` +
	`.cms-notice-close{position:absolute;right:.35rem;top:50%;transform:translateY(-50%);` +
	`display:flex;align-items:center;justify-content:center;width:1.75rem;height:1.75rem;` +
	`padding:0;border:0;border-radius:9999px;background:none;color:inherit;font:inherit;` +
	`font-size:1.15rem;line-height:1;cursor:pointer;opacity:.65}` +
	// A grey that reads as a highlight on every scheme, dark or light,
	// rather than one hover colour per scheme.
	`.cms-notice-close:hover{opacity:1;background:rgba(127,127,127,.28)}` +
	// The schemes. Underlines rather than borders: an inset shadow adds
	// no height, and a bar that grows by a pixel when its colours change
	// would shift the whole page down with it.
	`.cms-notice-dark{background:#0f172a;color:#f8fafc}` +
	`.cms-notice-accent{background:#1d4ed8;color:#eff6ff}` +
	`.cms-notice-warning{background:#fde68a;color:#78350f;box-shadow:inset 0 -1px 0 #f0c33c}` +
	`.cms-notice-alert{background:#b91c1c;color:#fef2f2}` +
	`.cms-notice-light{background:#f1f5f9;color:#0f172a;box-shadow:inset 0 -1px 0 #e2e8f0}`

// insertAfterBodyTag puts markup immediately after the page's opening
// <body> tag — where the notice bar goes on a site whose template never
// places {{cmsNotice}} itself, which is every site that predates the
// bar. A page with no <body> at all (a fragment template) gets the
// markup at the front, which is the same intent.
func insertAfterBodyTag(page []byte, markup string) []byte {
	at := bodyTagEnd(page)
	if at < 0 {
		return append([]byte(markup), page...)
	}
	var out bytes.Buffer
	out.Grow(len(page) + len(markup))
	out.Write(page[:at])
	out.WriteString(markup)
	out.Write(page[at:])
	return out.Bytes()
}

// bodyTagEnd returns the offset just past the opening <body> tag, or -1.
// The scan skips quoted attribute values, so a ">" inside one — rare,
// but legal, and the kind of thing a data attribute holding JSON does —
// cannot be mistaken for the end of the tag.
func bodyTagEnd(page []byte) int {
	lower := strings.ToLower(string(page))
	for i := 0; i < len(lower); {
		j := strings.Index(lower[i:], "<body")
		if j < 0 {
			return -1
		}
		k := i + j + len("<body")
		// <bodyguard> is not the body tag.
		if k < len(lower) && !isTagBreak(lower[k]) {
			i = k
			continue
		}
		var quote byte
		for ; k < len(lower); k++ {
			c := lower[k]
			switch {
			case quote != 0:
				if c == quote {
					quote = 0
				}
			case c == '"' || c == '\'':
				quote = c
			case c == '>':
				return k + 1
			}
		}
		return -1
	}
	return -1
}

// isTagBreak reports whether c can follow an element name inside a tag.
func isTagBreak(c byte) bool {
	switch c {
	case '>', '/', ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return false
}
