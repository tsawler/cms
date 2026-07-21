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

// buildEntry converts one stored item, resolving page links from the
// current slug. ok is false when the item should not render (vanished
// page, or draft page on a public render).
func buildEntry(item content.MenuItem, current string, includeDrafts bool) (MenuEntry, bool) {
	e := MenuEntry{ID: item.ID, Label: item.Label, NewTab: item.NewTab}
	if item.PageID != nil {
		if item.PageSlug == nil {
			return e, false // page vanished; FK cascade should prevent this
		}
		if !includeDrafts && (item.PageStatus == nil || *item.PageStatus != content.StatusPublished) {
			return e, false
		}
		e.URL = "/" + *item.PageSlug
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
// slug; items pointing at unpublished pages are dropped unless
// includeDrafts (editors see draft pages, so they see their menu items).
// Label-only top-level items are dropdown parents holding their Children;
// on public renders a dropdown with nothing visible in it is dropped,
// while editors keep it so they can fill it.
func BuildMenus(items []content.MenuItem, currentSlug string, includeDrafts bool) map[string][]MenuEntry {
	current := "/" + currentSlug
	menus := map[string][]MenuEntry{}
	// Rows arrive ordered by sort with children stored after their
	// parent, so parents are placed before their children attach.
	type parentPos struct {
		menu string
		idx  int
	}
	parents := map[int64]parentPos{}
	for _, item := range items {
		e, ok := buildEntry(item, current, includeDrafts)
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
	"cmsHead":     func() template.HTML { return "" },
	"cmsScripts":  func() template.HTML { return "" },
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
		{Label: "Muted", Class: "text-slate-500"},
		{Label: "Red", Class: "text-red-600"},
		{Label: "Green", Class: "text-emerald-600"},
		{Label: "Blue", Class: "text-blue-600"},
		{Label: "White", Class: "text-white"},
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
	PageID    int64
	Slug      string // "" identifies the home page (not deletable)
	AdminPath string
	CSRFToken string
	Locale    string
	Status    string // "draft" or "published"
	// HasUnpublished is true when a published page's draft content
	// differs from what is live — the editor shows "Unpublished changes"
	// and keeps Publish available.
	HasUnpublished bool
	MediaEnabled   bool
	// IsAdmin unlocks admin-only editor chrome (the page CSS & JS
	// panel); the server enforces the restriction regardless.
	IsAdmin  bool
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
}

// New parses each page template together with the shared globs and returns
// a Renderer. It fails fast on any template that doesn't parse. A nil
// sections gets the Tailwind-first defaults.
func New(fsys fs.FS, shared []string, pages []PageTemplate, sections *SectionStyles) (*Renderer, error) {
	if len(pages) == 0 {
		return nil, fmt.Errorf("render: at least one PageTemplate is required")
	}
	if sections == nil {
		sections = DefaultSectionStyles()
	}
	r := &Renderer{
		sets:      make(map[string]*template.Template, len(pages)),
		templates: pages,
		sections:  sections,
	}
	for _, pt := range pages {
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

// Render executes the page's template with the given blocks and menus and
// writes the result to w. Output is buffered so a template error never
// sends a partial page. A non-nil edit produces the editable variant of
// the page; see EditInfo.
func (r *Renderer) Render(w io.Writer, page *content.Page, blocks []content.Block, locale string, menus map[string][]MenuEntry, edit *EditInfo) error {
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
		"cmsMenu":    func(key string) []MenuEntry { return menus[key] },
		"cmsNav":     func(key string) template.HTML { return navHTML(key, menus[key], edit != nil) },
		"cmsHead":    func() template.HTML { return headHTML(page) },
		"cmsScripts": func() template.HTML { return scriptsHTML(page) },
	}
	if edit != nil {
		// Editable renders wrap region output in marker elements the
		// editor script finds. cmsText escapes its content itself here,
		// since the wrapper obliges it to return trusted HTML.
		funcs["cmsText"] = func(key string) template.HTML {
			return template.HTML(`<span data-cms-region="` + html.EscapeString(key) +
				`" data-cms-kind="text">` + html.EscapeString(text(key)) + `</span>`)
		}
		funcs["cmsRegion"] = func(key string) template.HTML {
			return template.HTML(`<div data-cms-region="` + html.EscapeString(key) +
				`" data-cms-kind="html">` + region(key) + `</div>`)
		}
		funcs["cmsSections"] = func(key string) template.HTML {
			var sb strings.Builder
			sb.WriteString(`<div data-cms-sections="`)
			sb.WriteString(html.EscapeString(key))
			sb.WriteString(`">`)
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
	tag := `<script src="` + EditorScriptPath + `" defer` +
		` data-page-id="` + strconv.FormatInt(edit.PageID, 10) + `"` +
		` data-slug="` + html.EscapeString(edit.Slug) + `"` +
		` data-admin-path="` + html.EscapeString(edit.AdminPath) + `"` +
		` data-csrf="` + html.EscapeString(edit.CSRFToken) + `"` +
		` data-status="` + html.EscapeString(edit.Status) + `"` +
		` data-unpublished="` + unpubFlag + `"` +
		` data-locale="` + html.EscapeString(edit.Locale) + `"` +
		` data-media="` + mediaFlag + `"` +
		` data-is-admin="` + adminFlag + `"` +
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
// cms-nav-* classes the host site styles, one dropdown level, and — on
// edit renders — the data-cms-menu-item markers the in-place editor uses
// for click-to-edit. The functional CSS and the dropdown-toggle
// script ship via cmsHead/cmsScripts. The editor re-renders this markup
// client-side after a menu save (editor/src/menu.js) — keep the two in
// sync.
func navHTML(key string, entries []MenuEntry, edit bool) template.HTML {
	var sb strings.Builder
	sb.WriteString(`<nav class="cms-nav" data-cms-menu="`)
	sb.WriteString(html.EscapeString(key))
	sb.WriteString(`"><ul class="cms-nav-list">`)
	for _, e := range entries {
		writeNavItem(&sb, e, edit)
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

// navCSS is the functional minimum for cmsNav markup: horizontal list,
// hidden dropdown panels shown on .cms-open, and a neutral panel look.
// Everything is plain classes the host stylesheet can override.
const navCSS = `.cms-nav ul{list-style:none;margin:0;padding:0}` +
	`.cms-nav-list{display:flex;flex-wrap:wrap;align-items:center;gap:.25em 1.25em}` +
	`.cms-nav-item{position:relative}` +
	`button.cms-nav-toggle{font:inherit;color:inherit;background:none;border:none;padding:0;cursor:pointer}` +
	`.cms-nav-caret::before{content:'';display:inline-block;margin-left:.4em;vertical-align:.15em;` +
	`border:.32em solid transparent;border-top-color:currentColor;border-bottom:none}` +
	`.cms-nav-sub{display:none;position:absolute;top:100%;left:0;z-index:40;min-width:11em;` +
	`margin-top:.4em;padding:.35em 0;background:#fff;color:#1a1a1a;` +
	`border:1px solid rgba(0,0,0,.12);border-radius:.5em;box-shadow:0 8px 24px rgba(0,0,0,.14)}` +
	`.cms-nav-drop.cms-open>.cms-nav-sub{display:block}` +
	`.cms-nav-sub .cms-nav-link{display:block;padding:.35em 1em;white-space:nowrap}`

// navJS toggles cmsNav dropdowns: click opens/closes (one at a time),
// clicking elsewhere or Escape closes. Delegated on document, so the
// editor's client-side nav re-renders keep working.
const navJS = `(function(){` +
	`function closeAll(except){document.querySelectorAll('.cms-nav-drop.cms-open').forEach(function(li){` +
	`if(li===except)return;li.classList.remove('cms-open');` +
	`var b=li.querySelector('.cms-nav-toggle');if(b)b.setAttribute('aria-expanded','false');});}` +
	`document.addEventListener('click',function(e){` +
	`if(e.target&&e.target.id==='cms-editor-host')return;` +
	`var t=e.target.closest?e.target.closest('.cms-nav-toggle'):null;` +
	`if(!t){closeAll(null);return;}` +
	`var li=t.closest('.cms-nav-drop');var open=!li.classList.contains('cms-open');` +
	`closeAll(li);li.classList.toggle('cms-open',open);` +
	`t.setAttribute('aria-expanded',open?'true':'false');});` +
	`document.addEventListener('keydown',function(e){if(e.key==='Escape')closeAll(null);});` +
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

// headHTML builds what {{cmsHead}} emits inside <head>: the CMS's own
// small stylesheet (button hover), the page's meta description, and its
// per-page CSS. HeadCSS is written raw; editing it is restricted to
// admins.
func headHTML(p *content.Page) template.HTML {
	var sb strings.Builder
	sb.WriteString("<style>" + btnCSS + imgShadowCSS + navCSS + "</style>\n")
	// External stylesheets come before the inline CSS so the page's own
	// rules can override the library's.
	for _, u := range resourceLinks(p.CSSLinks) {
		sb.WriteString(`<link rel="stylesheet" href="`);sb.WriteString(html.EscapeString(u));sb.WriteString("\">\n")
	}
	if p.Description != "" {
		sb.WriteString(`<meta name="description" content="`)
		sb.WriteString(html.EscapeString(p.Description))
		sb.WriteString("\">\n")
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
func scriptsHTML(p *content.Page) template.HTML {
	var sb strings.Builder
	sb.WriteString("<script>" + navJS + "</script>\n")
	for _, u := range resourceLinks(p.JSLinks) {
		sb.WriteString(`<script src="`)
		sb.WriteString(html.EscapeString(u))
		sb.WriteString("\"></script>\n")
	}
	if p.BodyJS != "" {
		sb.WriteString("<script>\n")
		sb.WriteString(scriptCloseRe.ReplaceAllString(p.BodyJS, `<\/script`))
		sb.WriteString("\n</script>\n")
	}
	return template.HTML(sb.String())
}
