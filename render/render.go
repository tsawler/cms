// Package render executes the host application's Go templates with the
// CMS's template funcs (cmsText, cmsRegion, cmsHead, cmsScripts) bound to a
// specific page's content. The host owns all page markup and styling; the
// CMS only fills the editable holes the templates declare.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/editor"
	"github.com/tsawler/cms/internal/datefmt"
	"github.com/tsawler/cms/media"
)

// PageTemplate is one template the host application offers for pages. File
// is a path within the host's TemplateFS (e.g. "templates/pages/home.gohtml");
// Label is what content editors see when choosing a page type.
type PageTemplate struct {
	File  string `json:"file"`
	Label string `json:"label"`
}

// MenuEntry is one rendered navigation item, as templates receive it from
// {{cmsMenu "main"}} and as {{cmsNav "main"}} renders it. The data-only
// cmsMenu form supplies entries and the template owns the markup; cmsNav
// emits the CMS's own nav markup (which is what the in-place editor can
// edit in place).
type MenuEntry struct {
	ID       int64 // menu item id; the editor's edit-mode marker
	Label    string
	URL      string // empty for a dropdown parent (label-only)
	NewTab   bool
	Active   bool        // this entry links to the page being rendered
	External bool        // absolute http(s) URL rather than a site page
	Children []MenuEntry // one level of dropdown items
}

// LocalePrefix is the URL path prefix for a locale: "" for the default
// locale, "/fr" for a non-default "fr". Localized page URLs are
// prefix + "/" + slug (the prefix alone for the homepage).
func LocalePrefix(locale, defaultLocale string) string {
	if locale == "" || locale == defaultLocale {
		return ""
	}
	return "/" + locale
}

// localeURL builds the site-relative URL of a page slug in a locale.
func localeURL(slug, locale, defaultLocale string) string {
	prefix := LocalePrefix(locale, defaultLocale)
	if slug == "" {
		if prefix == "" {
			return "/"
		}
		return prefix
	}
	return prefix + "/" + slug
}

// buildEntry converts one stored item, resolving page links from the
// current slug. ok is false when the item should not render (vanished
// page, or draft or private page on a public render).
func buildEntry(item content.MenuItem, current, locale, defaultLocale string, includeDrafts bool) (MenuEntry, bool) {
	e := MenuEntry{ID: item.ID, Label: item.LabelFor(locale), NewTab: item.NewTab}
	if item.PageID != nil {
		if item.PageSlug == nil {
			return e, false // page vanished; FK cascade should prevent this
		}
		if !includeDrafts && (item.PageStatus == nil || *item.PageStatus != content.StatusPublished) {
			return e, false
		}
		if !includeDrafts && item.PageVisibility != nil && *item.PageVisibility == content.VisibilityPrivate {
			return e, false
		}
		e.URL = localeURL(*item.PageSlug, locale, defaultLocale)
		e.Active = e.URL == current
		return e, true
	}
	e.URL = strings.TrimSpace(item.URL)
	e.External = strings.HasPrefix(e.URL, "http://") || strings.HasPrefix(e.URL, "https://")
	e.Active = !e.External && e.URL != "" && e.URL == current
	return e, true
}

// BuildMenus turns stored menu items into render-ready entries grouped by
// menu key. Page-linked items resolve their URL from the page's current
// slug; items pointing at unpublished or private pages are dropped unless
// includeDrafts (editors see draft and private pages, so they see their
// menu items).
// Label-only top-level items are dropdown parents holding their Children;
// on public renders a dropdown with nothing visible in it is dropped,
// while editors keep it so they can fill it.
// Menu entries carry the locale's labels and locale-prefixed page URLs.
func BuildMenus(items []content.MenuItem, currentSlug, locale, defaultLocale string, includeDrafts bool) map[string][]MenuEntry {
	current := localeURL(currentSlug, locale, defaultLocale)
	menus := map[string][]MenuEntry{}
	// Rows arrive ordered by sort with children stored after their
	// parent, so parents are placed before their children attach.
	type parentPos struct {
		menu string
		idx  int
	}
	parents := map[int64]parentPos{}
	for _, item := range items {
		e, ok := buildEntry(item, current, locale, defaultLocale, includeDrafts)
		if !ok {
			continue
		}
		if item.ParentID != nil {
			if e.URL == "" {
				continue // label-only rows are only meaningful at top level
			}
			if p, found := parents[*item.ParentID]; found {
				list := menus[p.menu]
				list[p.idx].Children = append(list[p.idx].Children, e)
			}
			continue
		}
		if e.URL == "" {
			// Dropdown parent; remember where it lands for its children.
			parents[item.ID] = parentPos{menu: item.Menu, idx: len(menus[item.Menu])}
		}
		menus[item.Menu] = append(menus[item.Menu], e)
	}
	if !includeDrafts {
		for key, list := range menus {
			kept := list[:0]
			for _, e := range list {
				if e.URL == "" && len(e.Children) == 0 {
					continue // empty dropdown on a public render
				}
				kept = append(kept, e)
			}
			menus[key] = kept
		}
	}
	return menus
}

// PageData is the dot value passed to a page template.
type PageData struct {
	Title string
	// Description is the page's description, which on a post is its
	// summary — the words its listing card and feed entry show.
	Description string
	// MetaDescription is what cmsHead publishes to search engines: the
	// page's own meta description when it has one, otherwise
	// Description. Templates need it only to do something else with the
	// same words; the meta tag itself comes from cmsHead.
	MetaDescription string
	Slug            string
	Locale          string
	// Post is set when the page is a blog or news post's backing page —
	// the post template reads its date, author, and images from here.
	// Nil on ordinary pages.
	Post *PostInfo
}

// PostInfo describes one blog or news post to templates: the dot's .Post
// on a post page, and the entries {{cmsPosts "blog" 10}} returns for
// listing pages. Title and Summary are the backing page's title and
// description for the render's locale.
type PostInfo struct {
	ID          int64  // post id, for the editor's settings API
	Feed        string // "blog" or "news"
	Title       string
	Summary     string
	URL         string // site-relative, e.g. "/blog/launch-day"
	PublishedAt time.Time
	// Author is the byline to print: empty when the post has none, and
	// equally empty when the post is set to publish without one, so
	// {{with .Author}} is all a template needs for either.
	Author string
	// HideAuthor is that setting, and AuthorName the name behind it —
	// the recorded author whether or not it is shown. Templates want
	// .Author; these two are for the editor's settings gear, which has
	// to name the byline it is offering to switch off.
	HideAuthor bool
	AuthorName string

	// Thumbnail is the post's listing image with every rendition resolved
	// — src, srcset, and intrinsic size — and is nil when the post has
	// none. Prefer it to the bare URL: a listing card that uses
	// .Thumbnail gets an image sized for a card, where .ThumbnailURL
	// alone leaves the browser to download a full-width one and scale it
	// down. A post's banner is not here — it is a section in the post
	// template's header region.
	Thumbnail *media.Image
	// ThumbnailURL is that image's default src, for templates that want
	// one string. "" when the post has no thumbnail.
	ThumbnailURL string
	// The library id behind it, 0 when the image is external or unset.
	// The in-place editor's post-settings dialog round-trips it.
	ThumbnailMediaID int64

	Draft bool // only ever true on editor renders
}

// PostLister supplies a feed's posts, newest first, for {{cmsPosts}} and
// {{cmsFeed}}. offset skips that many posts from the newest; a
// non-positive limit means no limit (and no offset). total is how long the
// feed is in full, but only when count is set — pagination needs it to
// size its page links, and a plain {{cmsPosts}} does not, so the extra
// query is not worth running unasked. Nil disables both funcs.
type PostLister func(feed string, limit, offset int, count bool) (posts []PostInfo, total int)

// DefaultPostsPerPage is how many posts a paginated listing shows when
// neither the host's Config.PostsPerPage nor the template says otherwise.
const DefaultPostsPerPage = 10

// Pager is where one page of a list sits in the whole of it, and how to
// reach the others — the state any paginated list needs, whatever it is
// listing. FeedPage embeds one for {{cmsFeed}} and the admin builds them
// for its own tables, so every paginated list in the CMS draws the same
// bar from the same code.
type Pager struct {
	Page    int // 1-based, always within [1, TotalPages]
	PerPage int
	// Total is the list's full length and TotalPages how many pages that
	// makes. TotalPages is at least 1, so an empty list is one empty page
	// rather than a list with no pages at all.
	Total      int
	TotalPages int
	// PrevURL and NextURL are the adjacent pages, empty at either end.
	PrevURL string
	NextURL string
	// Links is the numbered bar: every page in a short list, and in a
	// long one the ends, the pages around the current one, and ellipses
	// for the gaps.
	Links []PageLink
	// PrevLabel and NextLabel caption the end buttons. NewPager writes
	// English; a caller with a translator to hand overwrites them (the
	// admin does, since its UI is bilingual).
	PrevLabel string
	NextLabel string
}

// NewPager works out the numbering for a list of total items shown perPage
// at a time. page is the one asked for, clamped into range — a number past
// the end lands on the last real page rather than on an empty one. url
// builds the link to a page number; nil yields a bare "?page=n".
//
// The count has to be known first, so the usual order is: count, NewPager,
// then fetch Offset()..PerPage. That way an out-of-range page is corrected
// before the window is read rather than after.
func NewPager(page, perPage, total int, url func(int) string) *Pager {
	if perPage <= 0 {
		perPage = DefaultPostsPerPage
	}
	if total < 0 {
		total = 0
	}
	p := &Pager{
		PerPage: perPage, Total: total, TotalPages: 1,
		PrevLabel: "Previous", NextLabel: "Next",
	}
	if total > 0 {
		p.TotalPages = (total + perPage - 1) / perPage
	}
	p.Page = min(max(page, 1), p.TotalPages)
	if url == nil {
		url = func(n int) string { return "?page=" + strconv.Itoa(n) }
	}
	if p.Page > 1 {
		p.PrevURL = url(p.Page - 1)
	}
	if p.Page < p.TotalPages {
		p.NextURL = url(p.Page + 1)
	}
	p.Links = pageLinks(p, url)
	return p
}

// Offset is how many items to skip to reach this page — the OFFSET of the
// query that fills it.
func (p *Pager) Offset() int {
	if p == nil || p.Page < 1 {
		return 0
	}
	return (p.Page - 1) * p.PerPage
}

// HasPages reports whether the list runs to more than one page — what a
// template asks before drawing pagination at all. Safe on a nil Pager.
func (p *Pager) HasPages() bool { return p != nil && p.TotalPages > 1 }

// FeedPage is one page of a paginated post listing — what
// {{cmsFeed "blog"}} returns. Posts holds that page's posts and the
// embedded Pager says where the page sits in the feed, so a template can
// draw its own pagination from the numbers ({{$feed.Page}},
// {{range $feed.Links}}) or hand the whole value to {{cmsPagination}}.
type FeedPage struct {
	Feed  string     // "blog" or "news"
	Posts []PostInfo // this page's posts, newest first
	Pager
}

// HasPages shadows the embedded Pager's so it stays safe on a nil
// *FeedPage, which is what {{cmsFeed}} yields for an unknown feed.
func (f *FeedPage) HasPages() bool { return f != nil && f.TotalPages > 1 }

// PageLink is one entry in the numbered part of a pagination bar. An
// Ellipsis entry stands for the pages skipped in a long list: it has no
// number and no URL.
type PageLink struct {
	Number   int
	URL      string
	Current  bool
	Ellipsis bool
}

// PostImages resolves a post's library image into the renditions a
// template can use. prefer names the rendition wanted as the default src.
// media.Manager.ImageFor is the implementation; nil (no media library
// configured) leaves posts with whatever URLs they stored.
type PostImages func(md *media.Media, prefer string) *media.Image

// PostInfoFor builds the template-facing view of a stored post.
// localePrefix ("" or e.g. "/fr", see LocalePrefix) localizes the URL, and
// images resolves the post's library images.
func PostInfoFor(p *content.Post, localePrefix string, images PostImages) *PostInfo {
	info := &PostInfo{
		ID:          p.PostID,
		Feed:        string(p.Feed),
		Title:       p.Title,
		Summary:     p.Description,
		URL:         localePrefix + "/" + p.Slug,
		PublishedAt: p.PublishedAt,
		// A post with its byline hidden reports no author at all, so
		// every template that already writes {{with .Author}} — the
		// shape a byline has, since a post can always have lost its
		// author to a deleted account — honours the setting with no
		// change. The stored author is not disturbed; it is simply not
		// something this render has.
		Author:           authorName(p),
		HideAuthor:       p.HideAuthor,
		AuthorName:       p.AuthorName,
		ThumbnailMediaID: p.ThumbnailMediaIDValue(),
		Draft:            p.Status != content.StatusPublished,
	}
	// A listing card is a few hundred pixels wide, so it takes the card
	// rung of the ladder rather than the full-width one.
	info.Thumbnail = postImage(p.Thumbnail, p.ThumbnailURL, "card", images)
	if info.Thumbnail != nil {
		info.ThumbnailURL = info.Thumbnail.URL
	}
	return info
}

// authorName is the byline a render should show: the post's author, or
// nothing when the post is set to publish without one.
func authorName(p *content.Post) string {
	if p.HideAuthor {
		return ""
	}
	return p.AuthorName
}

// postImage resolves one of a post's images, falling back to a stored URL
// for images the library does not hold — and for a library image the
// caller gave no resolver for, which is a CMS running without media.
func postImage(md *media.Media, fallbackURL, prefer string, images PostImages) *media.Image {
	if md != nil && images != nil {
		if img := images(md, prefer); img != nil {
			return img
		}
	}
	if fallbackURL == "" {
		return nil
	}
	return &media.Image{URL: fallbackURL}
}

// feedPage assembles what {{cmsFeed}} returns: the requested page of the
// feed and the links around it. override is the template's optional
// per-page argument, which beats the host's Config.PostsPerPage.
func feedPage(in Input, feed string, override []int) *FeedPage {
	per := in.PostsPerPage
	if len(override) > 0 && override[0] > 0 {
		per = override[0]
	}
	url := func(n int) string { return listingURL(in, n) }
	f := &FeedPage{Feed: feed}
	if in.Posts == nil {
		f.Pager = *NewPager(1, per, 0, url)
		return f
	}

	// The window and the count come from one lister call, so the usual
	// page costs one round of queries. Only a ?page= past the end — a
	// stale bookmark, a crawler counting up — pays for a second: the
	// pager clamps it to the last real page, and that page is fetched
	// rather than showing the empty window originally asked for.
	page := max(in.PageNumber, 1)
	posts, total := in.Posts(feed, per, (page-1)*per, true)
	f.Pager = *NewPager(page, per, total, url)
	if f.Page != page {
		posts, _ = in.Posts(feed, per, f.Offset(), false)
	}
	f.Posts = posts
	return f
}

// listingURL is the link to listing page n, from the host's builder or —
// with no request behind the render — a bare query string.
func listingURL(in Input, n int) string {
	if in.PageURL != nil {
		return in.PageURL(n)
	}
	return "?page=" + strconv.Itoa(n)
}

// pageLinks builds the numbered bar: every page in a short list, and in a
// long one the first page, the last, and pagerWindow pages either side of
// the current one, with an Ellipsis entry standing for each gap.
func pageLinks(p *Pager, url func(int) string) []PageLink {
	const pagerWindow = 2
	if p.TotalPages < 2 {
		return nil
	}
	links := make([]PageLink, 0, 2*pagerWindow+5)
	prev := 0
	for n := 1; n <= p.TotalPages; n++ {
		near := n >= p.Page-pagerWindow && n <= p.Page+pagerWindow
		if !near && n != 1 && n != p.TotalPages {
			continue
		}
		if prev != 0 && n != prev+1 {
			links = append(links, PageLink{Ellipsis: true})
		}
		links = append(links, PageLink{Number: n, URL: url(n), Current: n == p.Page})
		prev = n
	}
	return links
}

// HTML is the ready-made bar: previous, the numbered links, next. It
// renders nothing for a list that fits on one page. Like cmsNav it is
// plain classes over minimal markup (styled by PagerCSS, which
// {{cmsHead}} inlines on the public site and the admin layout inlines for
// its own tables), so a stylesheet can restyle it entirely; callers
// wanting different markup range over the Pager's fields instead.
//
// {{cmsPagination}} is this method, and the admin's lists call it
// directly, so both bars are the same bar.
func (f *Pager) HTML() template.HTML {
	if !f.HasPages() {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<nav class="cms-pager" aria-label="Pagination">`)
	writePagerStep(&sb, "cms-pager-prev", f.PrevURL, f.PrevLabel)
	sb.WriteString(`<ul class="cms-pager-list">`)
	for _, l := range f.Links {
		sb.WriteString(`<li class="cms-pager-item">`)
		switch {
		case l.Ellipsis:
			sb.WriteString(`<span class="cms-pager-gap" aria-hidden="true">&hellip;</span>`)
		case l.Current:
			// aria-current marks the page you are on; the number is not
			// a link because it goes nowhere.
			sb.WriteString(`<span class="cms-pager-link cms-pager-current" aria-current="page">` +
				strconv.Itoa(l.Number) + `</span>`)
		default:
			sb.WriteString(`<a class="cms-pager-link" href="` + html.EscapeString(l.URL) +
				`" aria-label="Page ` + strconv.Itoa(l.Number) + `">` + strconv.Itoa(l.Number) + `</a>`)
		}
		sb.WriteString(`</li>`)
	}
	sb.WriteString(`</ul>`)
	writePagerStep(&sb, "cms-pager-next", f.NextURL, f.NextLabel)
	sb.WriteString(`</nav>`)
	return template.HTML(sb.String())
}

// writePagerStep writes one of the two end buttons. At the end of the feed
// there is nowhere to go, and it becomes a dimmed span rather than
// vanishing, so the bar keeps its shape as the visitor pages through.
func writePagerStep(sb *strings.Builder, class, url, label string) {
	if url == "" {
		sb.WriteString(`<span class="cms-pager-step ` + class + ` cms-pager-off" aria-hidden="true">` +
			label + `</span>`)
		return
	}
	sb.WriteString(`<a class="cms-pager-step ` + class + `" href="` + html.EscapeString(url) + `" rel="` +
		strings.TrimPrefix(class, "cms-pager-") + `">` + label + `</a>`)
}

// FuncNamePrefix is the namespace the CMS reserves for its own template
// functions. Host functions may not start with it, so a host can add
// however many of its own as it likes without ever colliding with a func
// a later CMS release introduces.
const FuncNamePrefix = "cms"

// ValidateFuncNames checks a host's template functions for names the CMS
// cannot accept: anything inside the reserved cms* namespace, and anything
// that is not a legal template identifier (templates call funcs by name,
// so an unusable name is a silent no-op rather than an error the host
// would otherwise see).
func ValidateFuncNames(m template.FuncMap) error {
	for name := range m {
		if strings.HasPrefix(name, FuncNamePrefix) {
			return fmt.Errorf("render: template func %q uses the reserved %q prefix", name, FuncNamePrefix)
		}
		if !validFuncName(name) {
			return fmt.Errorf("render: template func %q is not a valid identifier", name)
		}
	}
	return nil
}

func validFuncName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || unicode.IsLetter(r):
		case i > 0 && unicode.IsDigit(r):
		default:
			return false
		}
	}
	return true
}

// mergeFuncs copies src over dst, skipping the reserved namespace. New
// validates host funcs up front, so a reserved name reaching here means a
// caller assembled a FuncMap by hand; dropping it keeps the CMS's own
// funcs working rather than letting a page silently lose {{cmsHead}}.
func mergeFuncs(dst, src template.FuncMap) {
	for name, fn := range src {
		if strings.HasPrefix(name, FuncNamePrefix) {
			continue
		}
		dst[name] = fn
	}
}

// stubFuncs lets templates parse before real, page-bound funcs are attached
// at render time via Clone().Funcs().
var stubFuncs = template.FuncMap{
	"cmsText":        func(string) string { return "" },
	"cmsRegion":      func(string) template.HTML { return "" },
	"cmsShared":      func(string, ...string) template.HTML { return "" },
	"cmsImage":       func(string) string { return "" },
	"cmsSections":    func(string) template.HTML { return "" },
	"cmsHasSections": func(string) bool { return false },
	"cmsTitle":       func() template.HTML { return "" },
	"cmsDate":        func(time.Time, ...string) string { return "" },
	"cmsMenu":        func(string) []MenuEntry { return nil },
	"cmsNav":         func(string) template.HTML { return "" },
	"cmsBrand":       func(...string) template.HTML { return "" },
	"cmsHead":        func() template.HTML { return "" },
	"cmsScripts":     func() template.HTML { return "" },
	"cmsPosts":       func(string, int) []PostInfo { return nil },
	"cmsFeed":        func(string, ...int) *FeedPage { return nil },
	"cmsPagination":  func(*FeedPage) template.HTML { return "" },
	"cmsLocales":     func() []LocaleLink { return nil },
}

// CheckTemplate parses src as a page template, with the cms template funcs
// stubbed, and returns the parse error if there is one. It needs no
// database, request, or rendered page, which is what makes it usable from
// a test.
//
// Page templates are parsed when the server first renders them, so a
// malformed one compiles perfectly well and fails only in front of a
// visitor. Host applications can point this at their own templates to
// find that at build time instead; the scaffold uses it on the templates
// `cms init` writes.
//
// A host that registers its own template functions (Config.TemplateFuncs)
// must use CheckTemplateFuncs instead, or every template calling one of
// them reports a spurious "function not defined".
func CheckTemplate(name, src string) error {
	return CheckTemplateFuncs(name, src, nil)
}

// CheckTemplateFuncs is CheckTemplate with the host's own template
// functions declared alongside the cms ones. Only the names and
// signatures matter here — the funcs are never called — so a build-time
// check can pass the same map the server does.
func CheckTemplateFuncs(name, src string, host template.FuncMap) error {
	_, err := template.New(name).Funcs(parseFuncs(host)).Parse(src)
	return err
}

// parseFuncs is the name set templates parse against: the CMS's stubs plus
// whatever the host declared.
func parseFuncs(host template.FuncMap) template.FuncMap {
	if len(host) == 0 {
		return stubFuncs
	}
	out := make(template.FuncMap, len(stubFuncs)+len(host))
	for name, fn := range stubFuncs {
		out[name] = fn
	}
	mergeFuncs(out, host)
	return out
}

// EditorScriptPath is the public route the in-place editor script is
// served from. It carries a digest of the editor bundle, so shipping a
// new one changes the address and no browser can go on running the copy
// it cached. See editor.Version.
func EditorScriptPath() string { return editor.ScriptPath() }

// SharedRegionPrefix namespaces shared regions in the marker attributes an
// edit render emits: {{cmsShared "footer"}} becomes
// data-cms-region="site:footer". The prefix is what tells the editor to
// save that region to the site rather than to the page it is standing on,
// and it keeps a page region named "footer" distinct from the shared one.
const SharedRegionPrefix = "site:"

// SharedRegionName strips the marker prefix from a region name, reporting
// whether it was there — the server side of SharedRegionPrefix.
func SharedRegionName(marker string) (string, bool) {
	return strings.CutPrefix(marker, SharedRegionPrefix)
}

// EditorStyle is one entry in the in-place editor's "Styles" menu. Styles
// apply CSS classes — never inline styles — so the host site's stylesheet
// stays the single source of design truth and a redesign can restyle
// existing content. Class may hold several space-separated classes.
type EditorStyle struct {
	Label string `json:"label"`
	Class string `json:"class"`
	// Block applies the style to the whole surrounding block, converted
	// to this element ("p", "h2", ...). Empty applies the style inline
	// to the selected text.
	Block string `json:"block,omitempty"`
	// Group nests the entry in a submenu with this title. Entries
	// sharing a Group are folded together; the submenu sits where its
	// first member appears, among any ungrouped top-level entries.
	// Empty keeps the entry at the top level.
	Group string `json:"group,omitempty"`
}

// SectionOption is one choice in a section setting (a background or a
// content width): a stable Key stored with the content, a Label editors
// see, and the classes it applies. Class goes on the <section> wrapper for
// backgrounds and on the inner content container for widths; ContentClass
// (backgrounds only) is added to the content container — e.g. a dark
// background pairing with prose-invert.
type SectionOption struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	Class        string `json:"class"`
	ContentClass string `json:"contentClass,omitempty"`
}

// SectionStyles is the curated set of section settings editors choose
// from. Like editor styles, everything is classes — the host CSS owns the
// actual appearance. Corner classes go on the <section> wrapper like
// background classes (the wrapper is what paints the background, so the
// radius must clip it there).
type SectionStyles struct {
	Backgrounds []SectionOption `json:"backgrounds"`
	Widths      []SectionOption `json:"widths"`
	Corners     []SectionOption `json:"corners"`

	// Paddings is the vertical breathing room around a section's
	// content, as its own axis.
	//
	// It is separate from Widths because they answer different
	// questions, and bundling them — the obvious shortcut, since a width
	// preset is already a class string and can just carry a py-* — makes
	// one unanswerable: "the same measure, but tighter" then has no
	// expression except a second width option, and the option list
	// multiplies by every spacing anyone wants.
	//
	// It is also separate from the height setting, which is a min-height
	// and can only ever make a section taller. An editor looking for less
	// space reaches for height first, finds "Auto" already selected, and
	// concludes the CMS cannot do it.
	//
	// Optional: a nil list leaves sections exactly as they render today,
	// so hosts that keep their padding in the width presets are
	// unaffected. When set, the first entry is the default for content
	// saved before the axis existed — make it match whatever padding the
	// width presets used to carry, and nothing shifts.
	Paddings []SectionOption `json:"paddings"`
}

// joinClasses joins non-empty class strings with single spaces, so an
// unset axis leaves no trace in the rendered attribute.
func joinClasses(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func pickOption(list []SectionOption, key string) SectionOption {
	for _, o := range list {
		if o.Key == key {
			return o
		}
	}
	if len(list) > 0 {
		return list[0]
	}
	return SectionOption{}
}

// Background resolves a stored background key, falling back to the first
// option for unknown keys.
func (ss *SectionStyles) Background(key string) SectionOption { return pickOption(ss.Backgrounds, key) }

// Width resolves a stored width key, falling back to the first option.
func (ss *SectionStyles) Width(key string) SectionOption { return pickOption(ss.Widths, key) }

// Corner resolves a stored corner-rounding key, falling back to the first
// option. A nil Corners list resolves everything to a zero option, so
// hosts that ship no corner choices render sections unchanged.
func (ss *SectionStyles) Corner(key string) SectionOption { return pickOption(ss.Corners, key) }

// Padding resolves a stored vertical-spacing key, falling back to the
// first option. A nil Paddings list resolves to a zero option, which
// contributes no class — so a host that never configured the axis
// renders exactly as it did before it existed.
func (ss *SectionStyles) Padding(key string) SectionOption { return pickOption(ss.Paddings, key) }

// DefaultSectionStyles is the Tailwind-first default set of section
// settings. The classes need safelisting like editor styles do.
func DefaultSectionStyles() *SectionStyles {
	return &SectionStyles{
		Backgrounds: []SectionOption{
			{Key: "default", Label: "Default", Class: ""},
			{Key: "light", Label: "Light gray", Class: "bg-slate-50"},
			{Key: "dark", Label: "Dark", Class: "bg-slate-900", ContentClass: "prose-invert"},
			{Key: "accent", Label: "Accent", Class: "bg-blue-700", ContentClass: "prose-invert"},
		},
		Widths: []SectionOption{
			{Key: "normal", Label: "Normal", Class: "prose prose-slate mx-auto max-w-3xl px-6 py-12"},
			{Key: "wide", Label: "Wide", Class: "prose prose-slate mx-auto max-w-5xl px-6 py-12"},
			{Key: "full", Label: "Full width", Class: "prose prose-slate max-w-none px-6 py-12"},
		},
		Corners: []SectionOption{
			{Key: "none", Label: "None (square)", Class: ""},
			{Key: "small", Label: "Small", Class: "rounded-lg"},
			{Key: "medium", Label: "Medium", Class: "rounded-2xl"},
			{Key: "large", Label: "Large", Class: "rounded-3xl"},
		},
	}
}

// Custom section backgrounds — a color and/or image picked freely in the
// editor — are the two section settings that aren't curated classes; they
// render as inline styles. Values are validated on save and again at
// render time, in case older or hand-edited rows hold junk.
var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidBackgroundColor returns the value if it is a safe #rrggbb color,
// or "" otherwise.
func ValidBackgroundColor(s string) string {
	if hexColorRe.MatchString(s) {
		return s
	}
	return ""
}

// ValidBackgroundURL returns the value if it is safe to embed in a CSS
// url('…') inside an HTML attribute: an http(s) or site-relative URL
// containing none of the characters that could break out of either
// context. Returns "" otherwise.
func ValidBackgroundURL(s string) string {
	if s == "" || len(s) > 2048 || strings.ContainsAny(s, "\"'\\<>() \t\r\n") {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if u.Scheme == "" && !strings.HasPrefix(s, "/") {
		return ""
	}
	return s
}

// ValidSectionHeight returns the value if it is one of the fixed
// viewport-height options ("50", "75", "100"), or "" otherwise ("auto"
// and anything unknown mean no minimum height).
func ValidSectionHeight(s string) string {
	switch s {
	case "50", "75", "100":
		return s
	}
	return ""
}

// ValidSectionVAlign returns the value if it is a non-default vertical
// alignment ("center" or "bottom"), or "" otherwise ("top" is the
// default flow and needs no styles).
func ValidSectionVAlign(s string) string {
	switch s {
	case "center", "bottom":
		return s
	}
	return ""
}

// A section's background image is cropped to cover the section, so where
// it is anchored decides which part of it survives — the difference
// between a portrait's face and its shoulder. The anchor is stored as
// the CSS itself: a pair of percentages, which is the whole of what the
// editor's two sliders can express. "0% 0%" puts the image's top-left
// corner against the section's, "100% 100%" its bottom-right.
var sectionBGPositionRe = regexp.MustCompile(`^([0-9]{1,3})% ([0-9]{1,3})%$`)

// centeredBackground is the default anchor, and is deliberately never
// stored: it is where a background image sits anyway, so leaving it out
// keeps a section's settings describing only what was actually chosen.
const centeredBackground = "50% 50%"

// ValidBackgroundPosition returns the value if it is a pair of
// percentages in range and not the centered default, or "" otherwise.
// "" renders centered.
func ValidBackgroundPosition(s string) string {
	m := sectionBGPositionRe.FindStringSubmatch(s)
	if m == nil || s == centeredBackground {
		return ""
	}
	for _, pct := range m[1:] {
		if n, err := strconv.Atoi(pct); err != nil || n > 100 {
			return ""
		}
	}
	return s
}

// DefaultEditorStyles is the Tailwind-first default Styles menu, used when
// the host does not configure its own. Every class here must be safelisted
// in the site's Tailwind build (see the README) — editor content lives in
// the database, which Tailwind's source scanner never sees.
func DefaultEditorStyles() []EditorStyle {
	return []EditorStyle{
		{Label: "Muted", Class: "text-slate-500", Group: "Color"},
		{Label: "Red", Class: "text-red-600", Group: "Color"},
		{Label: "Green", Class: "text-emerald-600", Group: "Color"},
		{Label: "Blue", Class: "text-blue-600", Group: "Color"},
		{Label: "White", Class: "text-white", Group: "Color"},
		{Label: "Highlight", Class: "bg-yellow-200"},
		{Label: "Serif", Class: "font-serif"},
		{Label: "Monospace", Class: "font-mono"},
		{Label: "Lead paragraph", Class: "text-lg text-slate-600", Block: "p"},
		{Label: "Small print", Class: "text-sm text-slate-500"},
	}
}

// EditInfo turns a render into an editable one: regions are wrapped in
// marker elements and the in-place editor script is injected before
// </body>. Pass nil for a plain public render.
type EditInfo struct {
	PageID     int64
	Slug       string // "" identifies the home page (not deletable)
	AdminPath  string
	CSRFToken  string
	Locale     string
	Status     string // "draft" or "published"
	Visibility string // "public" or "private" — who may view the page
	// HasUnpublished is true when a published page's draft content
	// differs from what is live — the editor shows "Unpublished changes"
	// and keeps Publish available.
	HasUnpublished bool
	MediaEnabled   bool
	// IsAdmin unlocks admin-only editor chrome (the page CSS & JS
	// panel); the server enforces the restriction regardless.
	IsAdmin bool
	// IsSuperadmin unlocks the whole-page HTML source view on the
	// editor's tool rail.
	IsSuperadmin bool
	// PostsEnabled shows the tool rail's "New post" button (blog & news
	// configured on the host).
	PostsEnabled bool
	// Locales is the site's configured locale list ([0] = default); more
	// than one entry shows the edit bar's locale switcher.
	Locales []string
	// Post, set when the page backs a post, enables the edit bar's
	// post-settings gear (date, summary, thumbnail, header image).
	Post     *PostInfo
	Styles   []EditorStyle  // entries for the editor's Styles menu
	Sections *SectionStyles // options for section settings
}

// Renderer holds one parsed template set per page template: the shared
// templates (layouts, partials) plus that page's file. Per-file sets let
// every page define the same block names (e.g. "content") without
// colliding.
type Renderer struct {
	sets      map[string]*template.Template // keyed by PageTemplate.File
	templates []PageTemplate
	sections  *SectionStyles
	// hostFuncs are the host application's own template functions, as
	// declared at construction: they name what templates may call, and
	// stand in on any render that supplies no per-request replacement.
	hostFuncs template.FuncMap
	// contentCSS is the href of the CMS-generated content stylesheet
	// {{cmsHead}} links (see SetContentCSSHref), "" for none. Atomic:
	// a background rebuild updates it while renders read it.
	contentCSS atomic.Value
}

// New parses each page template together with the shared globs and returns
// a Renderer. It fails fast on any template that doesn't parse. A nil
// sections gets the Tailwind-first defaults. Hidden templates are parsed
// like page templates but left out of PageTemplates(), so they never
// appear in the admin's or editor's template choosers — the post template
// is one.
func New(fsys fs.FS, shared []string, pages []PageTemplate, sections *SectionStyles, hidden ...PageTemplate) (*Renderer, error) {
	return NewWithFuncs(fsys, shared, pages, sections, nil, hidden...)
}

// NewWithFuncs is New with the host application's own template functions
// declared alongside the CMS's. Templates parse against their names, so
// any number of them may be registered and called like the cms* funcs —
// typically to reach data the CMS does not own (a product catalogue, a
// vehicle table) from inside a CMS-managed page.
//
// The map given here is what templates parse against, and what renders
// use unless Input.Funcs supplies a per-request replacement. Names inside
// the reserved cms* namespace are rejected; see ValidateFuncNames.
func NewWithFuncs(fsys fs.FS, shared []string, pages []PageTemplate, sections *SectionStyles, host template.FuncMap, hidden ...PageTemplate) (*Renderer, error) {
	if len(pages) == 0 {
		return nil, fmt.Errorf("render: at least one PageTemplate is required")
	}
	if err := ValidateFuncNames(host); err != nil {
		return nil, err
	}
	if sections == nil {
		sections = DefaultSectionStyles()
	}
	r := &Renderer{
		sets:      make(map[string]*template.Template, len(pages)+len(hidden)),
		templates: pages,
		sections:  sections,
		hostFuncs: host,
	}
	parse := parseFuncs(host)
	for _, pt := range append(append([]PageTemplate{}, pages...), hidden...) {
		patterns := append(append([]string{}, shared...), pt.File)
		set, err := template.New(path.Base(pt.File)).Funcs(parse).ParseFS(fsys, patterns...)
		if err != nil {
			return nil, fmt.Errorf("render: parsing page template %s: %w", pt.File, err)
		}
		if set.Lookup(path.Base(pt.File)) == nil {
			return nil, fmt.Errorf("render: page template %s did not define %q", pt.File, path.Base(pt.File))
		}
		r.sets[pt.File] = set
	}
	return r, nil
}

// PageTemplates returns the templates available for pages, for the admin UI.
func (r *Renderer) PageTemplates() []PageTemplate {
	return r.templates
}

// Knows reports whether file is a registered page template.
func (r *Renderer) Knows(file string) bool {
	_, ok := r.sets[file]
	return ok
}

// SetContentCSSHref sets (or, with "", clears) the stylesheet link
// {{cmsHead}} emits for CMS-generated content CSS. Safe to call
// concurrently with renders.
func (r *Renderer) SetContentCSSHref(href string) {
	r.contentCSS.Store(href)
}

func (r *Renderer) contentCSSHref() string {
	if v, ok := r.contentCSS.Load().(string); ok {
		return v
	}
	return ""
}

// Input is everything one page render needs. Page, Blocks, and Locale are
// required; the rest is optional.
type Input struct {
	Page   *content.Page
	Blocks []content.Block
	// Shared holds the site's shared-region blocks — what {{cmsShared}}
	// renders. They are the same on every page, so they arrive alongside
	// the page's own rather than being looked up per region.
	Shared []content.Block
	Locale string
	Menus  map[string][]MenuEntry
	// Edit produces the editable variant of the page; see EditInfo. Nil
	// for a plain public render.
	Edit *EditInfo
	// Post is set when the page backs a blog or news post; it becomes the
	// template dot's .Post.
	Post *PostInfo
	// Posts feeds {{cmsPosts}} and {{cmsFeed}} on listing templates. Nil
	// makes both funcs return nothing.
	Posts PostLister
	// PostsPerPage sizes a {{cmsFeed}} page. Zero uses
	// DefaultPostsPerPage; a template may override it per listing.
	PostsPerPage int
	// PageNumber is the listing page this request asked for (?page=N).
	// Zero and below mean the first page, and so does a number past the
	// end — {{cmsFeed}} clamps it to a page that exists.
	PageNumber int
	// PageURL builds the URL of listing page n, for {{cmsFeed}}'s
	// prev/next and numbered links. Nil falls back to a bare "?page=n",
	// which is right for a render with no request behind it.
	PageURL func(n int) string
	// Locales is the site's configured locale list ([0] = default), for
	// {{cmsLocales}} and hreflang links. Nil or single-entry disables
	// both.
	Locales []string
	// BaseURL ("scheme://host", no trailing slash) makes hreflang
	// alternate links absolute; empty omits them.
	BaseURL string
	// Site is the stored site-wide settings ({{cmsBrand}}, nav
	// alignment). The zero value means "all defaults".
	Site content.SiteSettings
	// AdminPath is the URL prefix the admin area is mounted at, used to
	// build the optional "Log in" nav link (Site.LoginInNav). Empty
	// disables the link even when the setting is on.
	AdminPath string
	// Funcs replaces the host template functions declared at construction
	// (NewWithFuncs) for this render, entry by entry: a name it does not
	// carry keeps the declared implementation. This is how a host binds
	// its functions to the request — a database query wants the request's
	// context, and a locale-aware one wants Locale — without the renderer
	// having to know what a request is.
	//
	// Only names the constructor declared can actually be called: the
	// templates were parsed against that set, so a name appearing here
	// for the first time is unreachable. Reserved cms* names are ignored.
	Funcs template.FuncMap
}

// LocaleLink is one entry {{cmsLocales}} returns: the current page's URL
// in each configured locale, for host-rendered language switchers.
type LocaleLink struct {
	Code   string // e.g. "en", "fr"
	URL    string // this page in that locale, e.g. "/fr/about"
	Active bool   // the locale of the current render
}

// Render executes the page's template and writes the result to w. Output
// is buffered so a template error never sends a partial page.
func (r *Renderer) Render(w io.Writer, in Input) error {
	page, blocks, locale, menus, edit := in.Page, in.Blocks, in.Locale, in.Menus, in.Edit
	set, ok := r.sets[page.TemplateName]
	if !ok {
		return fmt.Errorf("render: page %d uses unknown template %q", page.ID, page.TemplateName)
	}
	clone, err := set.Clone()
	if err != nil {
		return fmt.Errorf("render: cloning template set: %w", err)
	}

	byRegion := make(map[string][]content.Block)
	for _, b := range blocks {
		byRegion[b.Region] = append(byRegion[b.Region], b)
	}

	text := func(key string) string {
		for _, b := range byRegion[key] {
			if b.Kind == content.KindText {
				return b.Content
			}
		}
		return ""
	}
	region := func(key string) string {
		var sb strings.Builder
		for _, b := range byRegion[key] {
			sb.WriteString(b.Content)
		}
		return sb.String()
	}

	bySharedRegion := make(map[string][]content.Block)
	for _, b := range in.Shared {
		bySharedRegion[b.Region] = append(bySharedRegion[b.Region], b)
	}
	// A shared region the site has never filled renders the template's own
	// fallback markup, so a footer says something sensible from the first
	// request rather than leaving an empty band until someone edits it.
	// In edit mode the fallback lands inside the editable wrapper, which
	// makes it the starting point for the first edit — the same trick
	// untranslated regions use.
	sharedRegion := func(key string, fallback []string) string {
		var sb strings.Builder
		for _, b := range bySharedRegion[key] {
			sb.WriteString(b.Content)
		}
		if sb.Len() == 0 && len(fallback) > 0 {
			return fallback[0]
		}
		return sb.String()
	}

	funcs := template.FuncMap{
		"cmsText":   func(key string) string { return text(key) },
		"cmsRegion": func(key string) template.HTML { return template.HTML(region(key)) },
		// cmsShared is cmsRegion for content the whole site shares — a
		// footer, a contact strip — so a template can carry one on every
		// page without an editor having to create it on each. The optional
		// second argument is the markup to show while the region is empty.
		"cmsShared": func(key string, fallback ...string) template.HTML {
			return template.HTML(sharedRegion(key, fallback))
		},
		"cmsImage": func(key string) string {
			for _, b := range byRegion[key] {
				if b.Kind == content.KindImage {
					return b.Content
				}
			}
			return ""
		},
		"cmsSections": func(key string) template.HTML {
			var sb strings.Builder
			for _, b := range byRegion[key] {
				if b.Kind != content.KindHTML {
					continue
				}
				sb.WriteString(r.sectionHTML(b, false))
			}
			return template.HTML(sb.String())
		},
		// cmsHasSections answers whether a sections area actually holds
		// anything, which cmsSections cannot be used for: on an edit
		// render it always returns its wrapper, empty area or not. A
		// template asks when the shape of the rest of the page depends on
		// it — a post whose banner carries the title needs its own
		// heading only when there is no banner.
		"cmsHasSections": func(key string) bool { return len(byRegion[key]) > 0 },
		// cmsTitle prints the page's own title — the same words the
		// admin's title field and the <title> tag carry — as the page's
		// visible heading. It is not a region: there is one title per
		// page per locale, stored on the page rather than in a block, so
		// it takes no key. An edit render wraps it so it can be typed
		// over in place (see the override below); a plain render is the
		// escaped text and nothing else, which is why it belongs in the
		// body and .Title still belongs in <title>.
		"cmsTitle": func() template.HTML { return template.HTML(html.EscapeString(page.Title)) },
		// cmsDate writes a date in the language the page is being read
		// in: "July 30, 2026" in English, "30 juillet 2026" in French.
		// Go's own .Format names months in English whatever the page is
		// written in, which leaves the date as the one line of a
		// translated page still in the wrong language. Pass "short" for
		// the abbreviated month, where a listing's line is tight.
		"cmsDate": func(t time.Time, style ...string) string {
			if len(style) > 0 && style[0] == "short" {
				return datefmt.Short(t, locale)
			}
			return datefmt.Long(t, locale)
		},
		"cmsMenu": func(key string) []MenuEntry { return menus[key] },
		"cmsNav": func(key string) template.HTML {
			// The login link shows only to logged-out visitors (edit == nil)
			// when the setting is on and an admin path is known.
			loginURL := ""
			if in.Site.LoginInNav && edit == nil && in.AdminPath != "" {
				loginURL = in.AdminPath + "/login"
			}
			return navHTML(key, menus[key], in.Site.MenuAlign, edit != nil, loginURL)
		},
		"cmsBrand":   func(fallback ...string) template.HTML { return brandHTML(in.Site, fallback) },
		"cmsHead":    func() template.HTML { return headHTML(page, r.contentCSSHref(), in) },
		"cmsScripts": func() template.HTML { return scriptsHTML(page, in.Site) },
		"cmsPosts": func(feed string, limit int) []PostInfo {
			if in.Posts == nil {
				return nil
			}
			posts, _ := in.Posts(feed, limit, 0, false)
			return posts
		},
		"cmsFeed": func(feed string, perPage ...int) *FeedPage {
			return feedPage(in, feed, perPage)
		},
		"cmsPagination": func(f *FeedPage) template.HTML {
			if f == nil {
				return ""
			}
			return f.HTML()
		},
		"cmsLocales": func() []LocaleLink { return localeLinks(in) },
	}
	if edit != nil {
		// Editable renders wrap region output in marker elements the
		// editor script finds. cmsText escapes its content itself here,
		// since the wrapper obliges it to return trusted HTML. A region
		// whose blocks came from the default locale rather than the
		// render's locale is flagged data-cms-fallback so the editor can
		// badge untranslated content.
		fallback := func(key string) string {
			blocks := byRegion[key]
			if len(blocks) == 0 || blocks[0].Locale == "" || blocks[0].Locale == locale {
				return ""
			}
			return ` data-cms-fallback="1"`
		}
		funcs["cmsText"] = func(key string) template.HTML {
			return template.HTML(`<span data-cms-region="` + html.EscapeString(key) +
				`" data-cms-kind="text"` + fallback(key) + `>` + html.EscapeString(text(key)) + `</span>`)
		}
		funcs["cmsRegion"] = func(key string) template.HTML {
			return template.HTML(`<div data-cms-region="` + html.EscapeString(key) +
				`" data-cms-kind="html"` + fallback(key) + `>` + region(key) + `</div>`)
		}
		// A shared region is marked like any other, but its name carries
		// the shared prefix: the editor saves it to the site rather than
		// to this page, and a page region of the same name stays a
		// different region.
		sharedFallback := func(key string) string {
			blocks := bySharedRegion[key]
			if len(blocks) == 0 || blocks[0].Locale == "" || blocks[0].Locale == locale {
				return ""
			}
			return ` data-cms-fallback="1"`
		}
		funcs["cmsShared"] = func(key string, fb ...string) template.HTML {
			return template.HTML(`<div data-cms-region="` + html.EscapeString(SharedRegionPrefix+key) +
				`" data-cms-kind="html"` + sharedFallback(key) + `>` + sharedRegion(key, fb) + `</div>`)
		}
		// The title is page metadata rather than a region, so its marker
		// carries no region name: the editor saves it to the page's
		// title field for the locale being edited, the same field the
		// page-settings dialog writes.
		funcs["cmsTitle"] = func() template.HTML {
			return template.HTML(`<span data-cms-title>` + html.EscapeString(page.Title) + `</span>`)
		}
		funcs["cmsSections"] = func(key string) template.HTML {
			var sb strings.Builder
			sb.WriteString(`<div data-cms-sections="`)
			sb.WriteString(html.EscapeString(key))
			sb.WriteString(`"`)
			sb.WriteString(fallback(key))
			sb.WriteString(`>`)
			for _, b := range byRegion[key] {
				if b.Kind != content.KindHTML {
					continue
				}
				sb.WriteString(r.sectionHTML(b, true))
			}
			sb.WriteString(`</div>`)
			return template.HTML(sb.String())
		}
	}
	// Host funcs last, so they are bound for this render: the declared
	// implementations first, then whatever the request supplied over
	// them. mergeFuncs drops reserved names, so nothing here can shadow
	// {{cmsHead}} or {{cmsScripts}}.
	mergeFuncs(funcs, r.hostFuncs)
	mergeFuncs(funcs, in.Funcs)

	clone.Funcs(funcs)

	var buf bytes.Buffer
	if err := clone.ExecuteTemplate(&buf, path.Base(page.TemplateName), PageData{
		Title:           page.Title,
		Description:     page.Description,
		MetaDescription: page.MetaTag(),
		Slug:            page.Slug,
		Locale:          locale,
		Post:            in.Post,
	}); err != nil {
		return fmt.Errorf("render: executing %s: %w", page.TemplateName, err)
	}

	out := buf.Bytes()
	if edit != nil {
		out = r.injectEditorScript(out, edit)
	}
	_, err = w.Write(out)
	return err
}

// sectionHTML renders one section block: a full-width <section> wrapper
// carrying the background classes around an inner content container
// carrying the width classes. Edit renders add the marker attributes the
// editor script uses for per-section controls.
func (r *Renderer) sectionHTML(b content.Block, edit bool) string {
	bg := r.sections.Background(b.Settings["bg"])
	w := r.sections.Width(b.Settings["width"])
	corner := r.sections.Corner(b.Settings["corners"])
	pad := r.sections.Padding(b.Settings["padding"])
	bgColor := ValidBackgroundColor(b.Settings["bgcolor"])
	bgImage := ValidBackgroundURL(b.Settings["bgimage"])
	bgPos := ValidBackgroundPosition(b.Settings["bgposition"])
	height := ValidSectionHeight(b.Settings["height"])
	valign := ValidSectionVAlign(b.Settings["valign"])

	var sb strings.Builder
	sb.WriteString("<section")
	if edit {
		sb.WriteString(` data-cms-section data-cms-bg="`)
		sb.WriteString(html.EscapeString(bg.Key))
		sb.WriteString(`" data-cms-width="`)
		sb.WriteString(html.EscapeString(w.Key))
		sb.WriteString(`"`)
		if corner.Key != "" {
			sb.WriteString(` data-cms-corners="` + html.EscapeString(corner.Key) + `"`)
		}
		if pad.Key != "" {
			sb.WriteString(` data-cms-padding="` + html.EscapeString(pad.Key) + `"`)
		}
		if height != "" {
			sb.WriteString(` data-cms-height="`)
			sb.WriteString(height)
			sb.WriteString(`"`)
		}
		if valign != "" {
			sb.WriteString(` data-cms-valign="` + valign + `"`)
		}
		if bgColor != "" {
			sb.WriteString(` data-cms-bgcolor="` + bgColor + `"`)
		}
		if bgImage != "" {
			sb.WriteString(` data-cms-bgimage="` + html.EscapeString(bgImage) + `"`)
		}
		if bgPos != "" {
			sb.WriteString(` data-cms-bgposition="` + bgPos + `"`)
		}
	}
	if wrapCls := strings.TrimSpace(bg.Class + " " + corner.Class); wrapCls != "" {
		sb.WriteString(` class="` + html.EscapeString(wrapCls) + `"`)
	}
	var style string
	if height != "" {
		style = "min-height:" + height + "vh;"
	}
	if valign != "" {
		style += "display:flex;flex-direction:column;justify-content:"
		if valign == "center" {
			style += "center;"
		} else {
			style += "flex-end;"
		}
	}
	if bgColor != "" {
		style += "background-color:" + bgColor + ";"
	}
	if bgImage != "" {
		pos := bgPos
		if pos == "" {
			pos = "center"
		}
		style += "background-image:url('" + bgImage + "');background-size:cover;background-position:" + pos + ";"
	}
	if style != "" {
		sb.WriteString(` style="` + html.EscapeString(style) + `"`)
	}
	sb.WriteString("><div")
	if edit {
		sb.WriteString(" data-cms-section-content")
	}
	// Padding last, so a host moving its py-* out of the width presets
	// gets the spacing option winning on source order rather than having
	// to think about which class Tailwind emits first. Joined rather
	// than concatenated because any of the three may be empty, and
	// concatenation leaves the gaps behind in the attribute.
	if inner := joinClasses(w.Class, bg.ContentClass, pad.Class); inner != "" {
		sb.WriteString(` class="` + html.EscapeString(inner) + `"`)
	}
	sb.WriteString(">")
	sb.WriteString(b.Content)
	sb.WriteString("</div></section>")
	return sb.String()
}

// injectEditorScript inserts the in-place editor's script tag before the
// closing </body> tag (or at the end when there isn't one).
func (r *Renderer) injectEditorScript(page []byte, edit *EditInfo) []byte {
	mediaFlag := "0"
	if edit.MediaEnabled {
		mediaFlag = "1"
	}
	unpubFlag := "0"
	if edit.HasUnpublished {
		unpubFlag = "1"
	}
	adminFlag := "0"
	if edit.IsAdmin {
		adminFlag = "1"
	}
	superFlag := "0"
	if edit.IsSuperadmin {
		superFlag = "1"
	}
	postsFlag := "0"
	if edit.PostsEnabled {
		postsFlag = "1"
	}
	// The post-settings dialog wants the date preformatted for its
	// <input type="datetime-local">, so this is a bespoke object rather
	// than a PostInfo marshal.
	postJSON := ""
	if edit.Post != nil {
		b, err := json.Marshal(map[string]any{
			"id":          edit.Post.ID,
			"feed":        edit.Post.Feed,
			"summary":     edit.Post.Summary,
			"publishedAt": edit.Post.PublishedAt.Local().Format("2006-01-02T15:04"),
			// The byline toggle, and the name it would show — the gear
			// says whose byline it is offering to leave off.
			"hideAuthor": edit.Post.HideAuthor,
			"authorName": edit.Post.AuthorName,
			// Both forms of the thumbnail: the id is what a save writes
			// back, the URL is what the dialog shows as a preview.
			"thumbnailMediaId": edit.Post.ThumbnailMediaID,
			"thumbnailUrl":     edit.Post.ThumbnailURL,
		})
		if err == nil {
			postJSON = string(b)
		}
	}
	stylesJSON, err := json.Marshal(edit.Styles)
	if err != nil || edit.Styles == nil {
		stylesJSON = []byte("[]")
	}
	sectionsCfg := edit.Sections
	if sectionsCfg == nil {
		sectionsCfg = DefaultSectionStyles()
	}
	sectionsJSON, err := json.Marshal(sectionsCfg)
	if err != nil {
		sectionsJSON = []byte("null")
	}
	templatesJSON, err := json.Marshal(r.templates)
	if err != nil {
		templatesJSON = []byte("[]")
	}
	localesJSON, err := json.Marshal(edit.Locales)
	if err != nil || edit.Locales == nil {
		localesJSON = []byte("[]")
	}
	tag := `<script src="` + EditorScriptPath() + `" defer` +
		` data-page-id="` + strconv.FormatInt(edit.PageID, 10) + `"` +
		` data-slug="` + html.EscapeString(edit.Slug) + `"` +
		` data-admin-path="` + html.EscapeString(edit.AdminPath) + `"` +
		` data-csrf="` + html.EscapeString(edit.CSRFToken) + `"` +
		` data-status="` + html.EscapeString(edit.Status) + `"` +
		` data-visibility="` + html.EscapeString(edit.Visibility) + `"` +
		` data-unpublished="` + unpubFlag + `"` +
		` data-locale="` + html.EscapeString(edit.Locale) + `"` +
		` data-locales="` + html.EscapeString(string(localesJSON)) + `"` +
		` data-media="` + mediaFlag + `"` +
		` data-is-admin="` + adminFlag + `"` +
		` data-is-superadmin="` + superFlag + `"` +
		` data-posts="` + postsFlag + `"` +
		` data-post="` + html.EscapeString(postJSON) + `"` +
		` data-styles="` + html.EscapeString(string(stylesJSON)) + `"` +
		` data-section-styles="` + html.EscapeString(string(sectionsJSON)) + `"` +
		` data-page-templates="` + html.EscapeString(string(templatesJSON)) + `"></script>`

	idx := strings.LastIndex(strings.ToLower(string(page)), "</body>")
	if idx < 0 {
		return append(page, []byte(tag)...)
	}
	var out bytes.Buffer
	out.Grow(len(page) + len(tag))
	out.Write(page[:idx])
	out.WriteString(tag)
	out.Write(page[idx:])
	return out.Bytes()
}

// navHTML renders {{cmsNav "main"}}: complete nav markup with stable
// cms-nav-* classes the host site styles, one dropdown level, a
// hamburger toggle that takes over on narrow screens, and — on
// edit renders — the data-cms-menu-item markers the in-place editor uses
// for click-to-edit. The functional CSS and the dropdown-toggle
// script ship via cmsHead/cmsScripts. The editor re-renders this markup
// client-side after a menu save (editor/src/menu.js) — keep the two in
// sync.
func navHTML(key string, entries []MenuEntry, align string, edit bool, loginURL string) template.HTML {
	cls := "cms-nav"
	switch align {
	case "left", "center", "right":
		cls += " cms-nav-" + align
	}
	var sb strings.Builder
	sb.WriteString(`<nav class="` + cls + `" data-cms-menu="`)
	sb.WriteString(html.EscapeString(key))
	sb.WriteString(`"><button type="button" class="cms-nav-burger" aria-expanded="false" aria-label="Menu">`)
	sb.WriteString(`<span class="cms-nav-burger-bar" aria-hidden="true"></span></button>`)
	sb.WriteString(`<ul class="cms-nav-list">`)
	for _, e := range entries {
		writeNavItem(&sb, e, edit)
	}
	// The optional "Log in" link (Site.LoginInNav), for logged-out
	// visitors only — the caller passes "" otherwise.
	if loginURL != "" {
		sb.WriteString(`<li class="cms-nav-item cms-nav-login"><a class="cms-nav-link" href="`)
		sb.WriteString(html.EscapeString(loginURL))
		sb.WriteString(`">Log in</a></li>`)
	}
	sb.WriteString(`</ul></nav>`)
	return template.HTML(sb.String())
}

func writeNavItem(sb *strings.Builder, e MenuEntry, edit bool) {
	marker := ""
	if edit {
		marker = ` data-cms-menu-item="` + strconv.FormatInt(e.ID, 10) + `"`
	}
	if e.URL == "" { // label-only dropdown parent
		sb.WriteString(`<li class="cms-nav-item cms-nav-drop"`)
		sb.WriteString(marker)
		sb.WriteString(`>`)
		sb.WriteString(`<button type="button" class="cms-nav-link cms-nav-toggle" aria-expanded="false" aria-haspopup="true">`)
		sb.WriteString(html.EscapeString(e.Label))
		sb.WriteString(`<span class="cms-nav-caret" aria-hidden="true"></span></button>`)
		sb.WriteString(`<ul class="cms-nav-sub">`)
		for _, c := range e.Children {
			writeNavItem(sb, c, edit)
		}
		sb.WriteString(`</ul></li>`)
		return
	}
	sb.WriteString(`<li class="cms-nav-item"` + marker + `><a class="cms-nav-link`)
	if e.Active {
		sb.WriteString(` cms-active`)
	}
	sb.WriteString(`" href="` + html.EscapeString(e.URL) + `"`)
	if e.Active {
		sb.WriteString(` aria-current="page"`)
	}
	if e.NewTab {
		sb.WriteString(` target="_blank" rel="noopener"`)
	}
	sb.WriteString(`>` + html.EscapeString(e.Label) + `</a></li>`)
}

// brandHTML renders {{cmsBrand}}: the stored site logo and/or site name
// inside a span.cms-brand the host styles and the editor's site-settings
// dialog updates in place after a save. The optional fallback argument
// shows until a name or logo has been saved; it rides along in a data
// attribute so the editor can restore it when both are cleared.
func brandHTML(site content.SiteSettings, fallback []string) template.HTML {
	name := site.SiteName
	logo := ValidBackgroundURL(site.LogoURL)
	def := ""
	if len(fallback) > 0 {
		def = fallback[0]
	}
	if name == "" && logo == "" {
		name = def
	}
	var sb strings.Builder
	sb.WriteString(`<span class="cms-brand"`)
	if def != "" {
		sb.WriteString(` data-cms-default="` + html.EscapeString(def) + `"`)
	}
	sb.WriteString(`>`)
	if logo != "" {
		alt := name
		if alt == "" {
			alt = def
		}
		sb.WriteString(`<img class="cms-brand-logo" src="` + html.EscapeString(logo) +
			`" alt="` + html.EscapeString(alt) + `">`)
	}
	if name != "" {
		sb.WriteString(`<span class="cms-brand-text">` + html.EscapeString(name) + `</span>`)
	}
	sb.WriteString(`</span>`)
	return template.HTML(sb.String())
}

// navCSS is the functional minimum for cmsNav markup: horizontal list,
// hidden dropdown panels shown on .cms-open, a neutral panel look, and
// — under 768px — a hamburger toggle (.cms-nav-burger, hidden on
// desktop) that collapses the list until .cms-nav-open is set on the
// nav. Everything is plain classes the host stylesheet can override.
const navCSS = `.cms-nav ul{list-style:none;margin:0;padding:0}` +
	`.cms-nav-list{display:flex;flex-wrap:wrap;align-items:center;gap:.25em 1.25em}` +
	// Alignment classes from the site settings. flex-grow makes the nav
	// claim the free space in the host's header flexbox so the list has
	// room to justify within; without a class nothing changes and the
	// host layout stays in charge. The margins align the mobile burger
	// the same way (it is the nav's only visible block child then).
	`.cms-nav-left,.cms-nav-center,.cms-nav-right{flex:1 1 auto}` +
	`.cms-nav-center .cms-nav-list{justify-content:center}` +
	`.cms-nav-right .cms-nav-list{justify-content:flex-end}` +
	`.cms-nav-center .cms-nav-burger{margin-left:auto;margin-right:auto}` +
	`.cms-nav-right .cms-nav-burger{margin-left:auto}` +
	// The brand emitted by {{cmsBrand}}: logo and text sit on one line;
	// the logo scales to the surrounding text size unless the host
	// styles .cms-brand-logo itself.
	`.cms-brand{display:inline-flex;align-items:center;gap:.5em}` +
	`.cms-brand-logo{height:1.6em;width:auto}` +
	`.cms-nav-item{position:relative}` +
	// A dropdown's parent is a <button> so it can be operated from the
	// keyboard, which means undoing the browser's button styling — its
	// own font, background and border — before the site's own rules land.
	//
	// :where() is what makes that safe. The reset has to name the element
	// to beat the user agent, but `button.cms-nav-toggle` is *more
	// specific* than `.cms-nav-link`, which is the class every one of
	// these carries and the hook the host is told to style. The reset
	// therefore won, and a dropdown's label came out in the page's body
	// font while its plain-link siblings used the site's display font.
	// Wrapped in :where() the reset has zero specificity: it still
	// outranks the user agent, because author styles always do, and it
	// loses to any rule the host writes — which is the whole point of
	// emitting a class for the host to write rules against.
	`:where(button.cms-nav-toggle){font:inherit;color:inherit;background:none;border:none;padding:0;cursor:pointer}` +
	`.cms-nav-caret::before{content:'';display:inline-block;margin-left:.4em;vertical-align:.15em;` +
	`border:.32em solid transparent;border-top-color:currentColor;border-bottom:none}` +
	`.cms-nav-sub{display:none;position:absolute;top:100%;left:0;z-index:40;min-width:11em;` +
	`margin-top:.4em;padding:.35em 0;background:#fff;color:#1a1a1a;` +
	`border:1px solid rgba(0,0,0,.12);border-radius:.5em;box-shadow:0 8px 24px rgba(0,0,0,.14)}` +
	`.cms-nav-drop.cms-open>.cms-nav-sub{display:block}` +
	`.cms-nav-sub .cms-nav-link{display:block;padding:.35em 1em;white-space:nowrap}` +
	`.cms-nav-burger{display:none;font:inherit;color:inherit;background:none;border:none;` +
	`padding:.35em 0;cursor:pointer}` +
	`.cms-nav-burger-bar{display:block;position:relative;width:1.4em;height:2px;` +
	`background:currentColor;border-radius:1px}` +
	`.cms-nav-burger-bar::before,.cms-nav-burger-bar::after{content:'';position:absolute;left:0;` +
	`width:100%;height:2px;background:currentColor;border-radius:1px}` +
	`.cms-nav-burger-bar::before{top:-.45em}` +
	`.cms-nav-burger-bar::after{top:.45em}` +
	// The open list is an absolutely-positioned panel rather than in-flow:
	// stacking it in place would stretch the nav (and whatever header the
	// host centers it in), shoving the site brand around. Anchored under
	// the nav, the header keeps its height and the panel drops over the
	// content, items left-aligned.
	`@media (max-width:768px){` +
	`.cms-nav{position:relative}` +
	`.cms-nav-burger{display:block}` +
	`.cms-nav-list{display:none;position:absolute;top:100%;right:0;z-index:40;` +
	`flex-direction:column;align-items:stretch;gap:0;` +
	`width:max-content;min-width:13em;max-width:calc(100vw - 2em);` +
	`margin-top:.4em;padding:.35em 0;background:#fff;color:#1a1a1a;text-align:left;` +
	`border:1px solid rgba(0,0,0,.12);border-radius:.5em;box-shadow:0 8px 24px rgba(0,0,0,.14)}` +
	`.cms-nav.cms-nav-open>.cms-nav-list{display:flex}` +
	`.cms-nav-list .cms-nav-link{display:block;width:100%;text-align:left;` +
	`padding:.45em 1em;white-space:normal}` +
	`.cms-nav-sub{position:static;min-width:0;margin:0;padding:0 0 0 1em;` +
	`background:none;color:inherit;border:none;border-radius:0;box-shadow:none}` +
	`}`

// PagerCSS is the functional minimum for cmsPagination markup: one
// centered row of evenly sized targets, with the current page and the dead
// end buttons told apart from live links.
//
// It states no colors of its own. Everything is currentColor mixed with
// transparency, so the bar inherits the host's palette and stays legible
// on a dark background as readily as a light one — the CMS knows neither.
// The current page is outlined rather than filled for the same reason: a
// fill needs a second color to contrast against. Plain classes throughout,
// so a host stylesheet can take the whole thing over.
const PagerCSS = `.cms-pager{display:flex;flex-wrap:wrap;align-items:center;justify-content:center;` +
	`gap:.35em;margin:2.5em 0;font-size:.9375em;line-height:1}` +
	`.cms-pager ul{list-style:none;margin:0;padding:0}` +
	`.cms-pager-list{display:flex;flex-wrap:wrap;align-items:center;gap:.25em}` +
	// One shared box for numbers and end buttons: the min-width keeps
	// single- and double-digit pages the same size, so the row does not
	// jitter as the visitor pages past 9.
	`.cms-pager-link,.cms-pager-step{display:inline-block;box-sizing:border-box;min-width:2.5em;` +
	`padding:.6em .85em;border:1px solid transparent;border-radius:.4em;` +
	`text-align:center;text-decoration:none;color:inherit}` +
	// Borders appear on hover rather than sitting on every number, which
	// keeps a long bar from reading as a row of boxes. The grey pair is a
	// fallback for browsers without color-mix.
	`a.cms-pager-link:hover,a.cms-pager-step:hover{border-color:rgba(128,128,128,.35);` +
	`background:rgba(128,128,128,.12)}` +
	`a.cms-pager-link:hover,a.cms-pager-step:hover{` +
	`border-color:color-mix(in srgb,currentColor 30%,transparent);` +
	`background:color-mix(in srgb,currentColor 10%,transparent)}` +
	`.cms-pager-current{font-weight:700;border-color:rgba(128,128,128,.55)}` +
	`.cms-pager-current{border-color:color-mix(in srgb,currentColor 55%,transparent)}` +
	// The end buttons are wider than a digit and should not be padded out
	// to a square, and at the end of the feed they dim in place.
	`.cms-pager-step{min-width:0;font-weight:500}` +
	`.cms-pager-off{opacity:.35}` +
	`.cms-pager-gap{display:inline-block;padding:.6em .2em;opacity:.5}`

// navJS toggles cmsNav dropdowns and the mobile hamburger: click
// opens/closes (one dropdown at a time), clicking elsewhere or Escape
// closes. The hamburger sets .cms-nav-open on its nav; clicking outside
// any nav or pressing Escape collapses open navs too. Delegated on
// document, so the editor's client-side nav re-renders keep working.
const navJS = `(function(){` +
	`function closeAll(except){document.querySelectorAll('.cms-nav-drop.cms-open').forEach(function(li){` +
	`if(li===except)return;li.classList.remove('cms-open');` +
	`var b=li.querySelector('.cms-nav-toggle');if(b)b.setAttribute('aria-expanded','false');});}` +
	`function closeBurgers(){document.querySelectorAll('.cms-nav.cms-nav-open').forEach(function(n){` +
	`n.classList.remove('cms-nav-open');` +
	`var b=n.querySelector('.cms-nav-burger');if(b)b.setAttribute('aria-expanded','false');});}` +
	`document.addEventListener('click',function(e){` +
	`if(e.target&&e.target.id==='cms-editor-host')return;` +
	`var g=e.target.closest?e.target.closest('.cms-nav-burger'):null;` +
	`if(g){var nav=g.closest('.cms-nav');var on=!nav.classList.contains('cms-nav-open');` +
	`closeBurgers();closeAll(null);nav.classList.toggle('cms-nav-open',on);` +
	`g.setAttribute('aria-expanded',on?'true':'false');return;}` +
	`if(!e.target.closest||!e.target.closest('.cms-nav'))closeBurgers();` +
	`var t=e.target.closest?e.target.closest('.cms-nav-toggle'):null;` +
	`if(!t){closeAll(null);return;}` +
	`var li=t.closest('.cms-nav-drop');var open=!li.classList.contains('cms-open');` +
	`closeAll(li);li.classList.toggle('cms-open',open);` +
	`t.setAttribute('aria-expanded',open?'true':'false');});` +
	`document.addEventListener('keydown',function(e){if(e.key==='Escape'){closeAll(null);closeBurgers();}});` +
	`})();`

// btnCSS gives button-snippet links (a.cms-btn) a uniform hover and
// press effect with no per-button configuration. Brightness shifts work
// over any background the button editor sets, unlike hardcoded hover
// colors. It also strips the link underline: buttons living in prose
// scope (the Hero/CTA presets, or a prose link the button editor
// converted) would otherwise inherit Tailwind Typography's `.prose a`
// underline, and a button should never read as an underlined link.
const btnCSS = `a.cms-btn{transition:filter .15s ease;text-decoration:none}` +
	`a.cms-btn:hover{filter:brightness(.9)}` +
	`a.cms-btn:active{filter:brightness(.8)}`

// imgShadowCSS backs the image gear's Shadow presets. CMS-owned classes
// rather than Tailwind's shadow scale: Tailwind's stock shadows are
// 10–25% black — nearly invisible next to a photo on a bright display —
// and every utility class would need safelisting in the host's build.
const imgShadowCSS = `.cms-shadow-subtle{box-shadow:0 4px 12px rgba(0,0,0,.2),0 2px 4px rgba(0,0,0,.14)}` +
	`.cms-shadow-strong{box-shadow:0 16px 40px rgba(0,0,0,.34),0 4px 12px rgba(0,0,0,.22)}` +
	// A captioned linked image nests as figure > a > img, one level
	// deeper than Tailwind typography's figure>* margin reset reaches —
	// without this the image keeps its 2em prose margins inside the
	// figure, stranding the caption far below. The :not() lifts
	// specificity above typography's .prose :where(img) (and names an
	// opt-out class for hosts that want the margins back).
	// Vertical only: margin:0 would also beat .mx-auto and undo
	// horizontal centering.
	`figure>a>img:not(.cms-keep-margins){margin-top:0;margin-bottom:0}`

// localeLinks builds {{cmsLocales}}: the current page's URL in every
// configured locale. Nil unless more than one locale is configured.
func localeLinks(in Input) []LocaleLink {
	if len(in.Locales) < 2 {
		return nil
	}
	def := in.Locales[0]
	out := make([]LocaleLink, 0, len(in.Locales))
	for _, code := range in.Locales {
		out = append(out, LocaleLink{
			Code:   code,
			URL:    localeURL(in.Page.Slug, code, def),
			Active: code == in.Locale,
		})
	}
	return out
}

// headHTML builds what {{cmsHead}} emits inside <head>: the CMS's own
// small stylesheet (button hover), the generated content-CSS link (when
// the Tailwind rebuild feature is active), hreflang alternates on
// multi-locale sites, the page's meta description, and its per-page CSS.
// HeadCSS is written raw; editing it is restricted to admins.
func headHTML(p *content.Page, contentCSS string, in Input) template.HTML {
	var sb strings.Builder
	sb.WriteString("<style>" + btnCSS + imgShadowCSS + navCSS + PagerCSS + "</style>\n")
	// hreflang alternates need absolute URLs, so they require BaseURL.
	if in.BaseURL != "" && len(in.Locales) > 1 {
		for _, l := range localeLinks(in) {
			sb.WriteString(`<link rel="alternate" hreflang="` + html.EscapeString(l.Code) +
				`" href="` + html.EscapeString(in.BaseURL+l.URL) + "\">\n")
		}
		sb.WriteString(`<link rel="alternate" hreflang="x-default" href="` +
			html.EscapeString(in.BaseURL+localeURL(p.Slug, in.Locales[0], in.Locales[0])) + "\">\n")
	}
	// Utilities generated from stored content. Before the page's own
	// links and inline CSS, so page-specific rules can override them.
	if contentCSS != "" {
		sb.WriteString(`<link rel="stylesheet" href="`)
		sb.WriteString(html.EscapeString(contentCSS))
		sb.WriteString("\">\n")
	}
	// A post's own meta description when it has one, otherwise the
	// summary it shares with its listing card (MetaTag).
	if desc := p.MetaTag(); desc != "" {
		sb.WriteString(`<meta name="description" content="`)
		sb.WriteString(html.EscapeString(desc))
		sb.WriteString("\">\n")
	}
	// Site-wide CSS before the page's own, so a page's HeadCSS can
	// override it. Written raw; editing it is restricted to admins.
	sb.WriteString(embedCode(in.Site.SiteCSS, "style", styleCloseRe))
	sb.WriteString(embedCode(p.HeadCSS, "style", styleCloseRe))
	return template.HTML(sb.String())
}

// A literal closing tag inside wrapped plain code would end the wrapper
// element early and dump the rest onto the page. `<\/` is an equivalent
// escape inside JS strings, and the sequence has no legitimate use
// outside one — same for CSS.
var (
	styleCloseRe  = regexp.MustCompile(`(?i)</style`)
	scriptCloseRe = regexp.MustCompile(`(?i)</script`)
)

// markupRe decides how an admin code field is embedded: a field that
// carries its own <style>, <link>, or <script> tags is written into the
// page verbatim; anything else is plain code and gets wrapped.
var markupRe = regexp.MustCompile(`(?i)<(style|link|script)\b`)

// embedCode renders one admin code field (page or site scope) for
// injection into the page. Plain code is wrapped in tag with literal
// closing tags escaped; code that already contains markup is emitted
// as-is, in the order the admin wrote it.
func embedCode(code, tag string, closeRe *regexp.Regexp) string {
	if strings.TrimSpace(code) == "" {
		return ""
	}
	if markupRe.MatchString(code) {
		return code + "\n"
	}
	return "<" + tag + ">\n" + closeRe.ReplaceAllString(code, `<\/`+tag) + "\n</" + tag + ">\n"
}

// scriptsHTML builds what {{cmsScripts}} emits before </body>: the
// site-wide JS before the page's own, so a page can build on it. Both
// are written raw (plain code gets a <script> wrapper, markup passes
// through verbatim); editing is restricted to admins.
func scriptsHTML(p *content.Page, site content.SiteSettings) template.HTML {
	var sb strings.Builder
	sb.WriteString("<script>" + navJS + "</script>\n")
	sb.WriteString(embedCode(site.SiteJS, "script", scriptCloseRe))
	sb.WriteString(embedCode(p.BodyJS, "script", scriptCloseRe))
	return template.HTML(sb.String())
}
