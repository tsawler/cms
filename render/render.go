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

	"github.com/tsawler/cms/content"
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
	Title       string
	Description string
	Slug        string
	Locale      string
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
	ID           int64  // post id, for the editor's settings API
	Feed         string // "blog" or "news"
	Title        string
	Summary      string
	URL          string // site-relative, e.g. "/blog/launch-day"
	PublishedAt  time.Time
	Author       string
	ThumbnailURL string // "" when no thumbnail was chosen
	HeaderURL    string // "" when no header image was chosen
	Draft        bool   // only ever true on editor renders
}

// PostLister supplies a feed's posts, newest first, for {{cmsPosts}}. A
// non-positive limit means no limit. Nil disables the func (it returns
// nothing).
type PostLister func(feed string, limit int) []PostInfo

// PostInfoFor builds the template-facing view of a stored post.
// localePrefix ("" or e.g. "/fr", see LocalePrefix) localizes the URL.
func PostInfoFor(p *content.Post, localePrefix string) *PostInfo {
	return &PostInfo{
		ID:           p.PostID,
		Feed:         string(p.Feed),
		Title:        p.Title,
		Summary:      p.Description,
		URL:          localePrefix + "/" + p.Slug,
		PublishedAt:  p.PublishedAt,
		Author:       p.AuthorName,
		ThumbnailURL: p.ThumbnailURL,
		HeaderURL:    p.HeaderURL,
		Draft:        p.Status != content.StatusPublished,
	}
}

// stubFuncs lets templates parse before real, page-bound funcs are attached
// at render time via Clone().Funcs().
var stubFuncs = template.FuncMap{
	"cmsText":     func(string) string { return "" },
	"cmsRegion":   func(string) template.HTML { return "" },
	"cmsImage":    func(string) string { return "" },
	"cmsSections": func(string) template.HTML { return "" },
	"cmsMenu":     func(string) []MenuEntry { return nil },
	"cmsNav":      func(string) template.HTML { return "" },
	"cmsBrand":    func(...string) template.HTML { return "" },
	"cmsHead":     func() template.HTML { return "" },
	"cmsScripts":  func() template.HTML { return "" },
	"cmsPosts":    func(string, int) []PostInfo { return nil },
	"cmsLocales":  func() []LocaleLink { return nil },
}

// EditorScriptPath is the public route the in-place editor script is served
// from.
const EditorScriptPath = "/cms/editor/editor.js"

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
// actual appearance.
type SectionStyles struct {
	Backgrounds []SectionOption `json:"backgrounds"`
	Widths      []SectionOption `json:"widths"`
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

// ValidResourceURL returns the value if it is safe to use as an external
// stylesheet or script URL — the same rules as background URLs.
func ValidResourceURL(s string) string {
	return ValidBackgroundURL(s)
}

// resourceLinks splits a newline-separated URL list, dropping empties
// and anything that fails validation (defense against hand-edited rows).
func resourceLinks(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		if u := ValidResourceURL(strings.TrimSpace(line)); u != "" {
			out = append(out, u)
		}
	}
	return out
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
	if len(pages) == 0 {
		return nil, fmt.Errorf("render: at least one PageTemplate is required")
	}
	if sections == nil {
		sections = DefaultSectionStyles()
	}
	r := &Renderer{
		sets:      make(map[string]*template.Template, len(pages)+len(hidden)),
		templates: pages,
		sections:  sections,
	}
	for _, pt := range append(append([]PageTemplate{}, pages...), hidden...) {
		patterns := append(append([]string{}, shared...), pt.File)
		set, err := template.New(path.Base(pt.File)).Funcs(stubFuncs).ParseFS(fsys, patterns...)
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
	Locale string
	Menus  map[string][]MenuEntry
	// Edit produces the editable variant of the page; see EditInfo. Nil
	// for a plain public render.
	Edit *EditInfo
	// Post is set when the page backs a blog or news post; it becomes the
	// template dot's .Post.
	Post *PostInfo
	// Posts feeds {{cmsPosts}} on listing templates. Nil makes the func
	// return nothing.
	Posts PostLister
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

	funcs := template.FuncMap{
		"cmsText":   func(key string) string { return text(key) },
		"cmsRegion": func(key string) template.HTML { return template.HTML(region(key)) },
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
			return in.Posts(feed, limit)
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
	clone.Funcs(funcs)

	var buf bytes.Buffer
	if err := clone.ExecuteTemplate(&buf, path.Base(page.TemplateName), PageData{
		Title:       page.Title,
		Description: page.Description,
		Slug:        page.Slug,
		Locale:      locale,
		Post:        in.Post,
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
	bgColor := ValidBackgroundColor(b.Settings["bgcolor"])
	bgImage := ValidBackgroundURL(b.Settings["bgimage"])
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
	}
	if bg.Class != "" {
		sb.WriteString(` class="` + html.EscapeString(bg.Class) + `"`)
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
		style += "background-image:url('" + bgImage + "');background-size:cover;background-position:center;"
	}
	if style != "" {
		sb.WriteString(` style="` + html.EscapeString(style) + `"`)
	}
	sb.WriteString("><div")
	if edit {
		sb.WriteString(" data-cms-section-content")
	}
	if inner := strings.TrimSpace(w.Class + " " + bg.ContentClass); inner != "" {
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
			"id":           edit.Post.ID,
			"feed":         edit.Post.Feed,
			"summary":      edit.Post.Summary,
			"publishedAt":  edit.Post.PublishedAt.Local().Format("2006-01-02T15:04"),
			"thumbnailUrl": edit.Post.ThumbnailURL,
			"headerUrl":    edit.Post.HeaderURL,
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
	tag := `<script src="` + EditorScriptPath + `" defer` +
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
	`button.cms-nav-toggle{font:inherit;color:inherit;background:none;border:none;padding:0;cursor:pointer}` +
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
	sb.WriteString("<style>" + btnCSS + imgShadowCSS + navCSS + "</style>\n")
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
	// External stylesheets come before the inline CSS so the page's own
	// rules can override the library's; site-wide ones come before the
	// page's for the same reason.
	for _, u := range resourceLinks(in.Site.SiteCSSLinks) {
		sb.WriteString(`<link rel="stylesheet" href="`)
		sb.WriteString(html.EscapeString(u))
		sb.WriteString("\">\n")
	}
	for _, u := range resourceLinks(p.CSSLinks) {
		sb.WriteString(`<link rel="stylesheet" href="`)
		sb.WriteString(html.EscapeString(u))
		sb.WriteString("\">\n")
	}
	if p.Description != "" {
		sb.WriteString(`<meta name="description" content="`)
		sb.WriteString(html.EscapeString(p.Description))
		sb.WriteString("\">\n")
	}
	// Site-wide CSS before the page's own, so a page's HeadCSS can
	// override it. Written raw; editing it is restricted to admins.
	if in.Site.SiteCSS != "" {
		sb.WriteString("<style>\n")
		sb.WriteString(styleCloseRe.ReplaceAllString(in.Site.SiteCSS, `<\/style`))
		sb.WriteString("\n</style>\n")
	}
	if p.HeadCSS != "" {
		sb.WriteString("<style>\n")
		sb.WriteString(styleCloseRe.ReplaceAllString(p.HeadCSS, `<\/style`))
		sb.WriteString("\n</style>\n")
	}
	return template.HTML(sb.String())
}

// A literal closing tag inside the embedded code would end the wrapper
// element early and dump the rest onto the page. `<\/` is an equivalent
// escape inside JS strings, and the sequence has no legitimate use
// outside one — same for CSS.
var (
	styleCloseRe  = regexp.MustCompile(`(?i)</style`)
	scriptCloseRe = regexp.MustCompile(`(?i)</script`)
)

// scriptsHTML builds what {{cmsScripts}} emits before </body>: the page's
// external scripts, then its inline per-page JS (so the inline code can
// use what the external libraries define). Written raw; editing is
// restricted to admins.
func scriptsHTML(p *content.Page, site content.SiteSettings) template.HTML {
	var sb strings.Builder
	sb.WriteString("<script>" + navJS + "</script>\n")
	// Site-wide external scripts load before the page's, and all external
	// scripts before the inline code that may build on them.
	for _, u := range resourceLinks(site.SiteJSLinks) {
		sb.WriteString(`<script src="`)
		sb.WriteString(html.EscapeString(u))
		sb.WriteString("\"></script>\n")
	}
	for _, u := range resourceLinks(p.JSLinks) {
		sb.WriteString(`<script src="`)
		sb.WriteString(html.EscapeString(u))
		sb.WriteString("\"></script>\n")
	}
	// Site-wide JS before the page's own inline JS, so a page can build on
	// it. Written raw; editing it is restricted to admins.
	if site.SiteJS != "" {
		sb.WriteString("<script>\n")
		sb.WriteString(scriptCloseRe.ReplaceAllString(site.SiteJS, `<\/script`))
		sb.WriteString("\n</script>\n")
	}
	if p.BodyJS != "" {
		sb.WriteString("<script>\n")
		sb.WriteString(scriptCloseRe.ReplaceAllString(p.BodyJS, `<\/script`))
		sb.WriteString("\n</script>\n")
	}
	return template.HTML(sb.String())
}
