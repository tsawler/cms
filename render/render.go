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
	"path"
	"strconv"
	"strings"

	"github.com/tsawler/cms/content"
)

// PageTemplate is one template the host application offers for pages. File
// is a path within the host's TemplateFS (e.g. "templates/pages/home.tmpl");
// Label is what content editors see when choosing a page type.
type PageTemplate struct {
	File  string `json:"file"`
	Label string `json:"label"`
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
	PageID       int64
	Slug         string // "" identifies the home page (not deletable)
	AdminPath    string
	CSRFToken    string
	Locale       string
	Status       string // "draft" or "published"
	MediaEnabled bool
	Styles       []EditorStyle  // entries for the editor's Styles menu
	Sections     *SectionStyles // options for section settings
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

// Render executes the page's template with the given blocks and writes the
// result to w. Output is buffered so a template error never sends a partial
// page. A non-nil edit produces the editable variant of the page; see
// EditInfo.
func (r *Renderer) Render(w io.Writer, page *content.Page, blocks []content.Block, locale string, edit *EditInfo) error {
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
			sb.WriteString(`<div data-cms-sections="` + html.EscapeString(key) + `">`)
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

	var sb strings.Builder
	sb.WriteString("<section")
	if edit {
		sb.WriteString(` data-cms-section data-cms-bg="` + html.EscapeString(bg.Key) +
			`" data-cms-width="` + html.EscapeString(w.Key) + `"`)
	}
	if bg.Class != "" {
		sb.WriteString(` class="` + html.EscapeString(bg.Class) + `"`)
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
		` data-locale="` + html.EscapeString(edit.Locale) + `"` +
		` data-media="` + mediaFlag + `"` +
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

// headHTML builds what {{cmsHead}} emits inside <head>: the page's meta
// description and its per-page CSS. HeadCSS is written raw; editing it is
// restricted to admins.
func headHTML(p *content.Page) template.HTML {
	var sb strings.Builder
	if p.Description != "" {
		sb.WriteString(`<meta name="description" content="`)
		sb.WriteString(html.EscapeString(p.Description))
		sb.WriteString("\">\n")
	}
	if p.HeadCSS != "" {
		sb.WriteString("<style>\n")
		sb.WriteString(p.HeadCSS)
		sb.WriteString("\n</style>\n")
	}
	return template.HTML(sb.String())
}

// scriptsHTML builds what {{cmsScripts}} emits before </body>: the page's
// per-page JS. Written raw; editing it is restricted to admins.
func scriptsHTML(p *content.Page) template.HTML {
	if p.BodyJS == "" {
		return ""
	}
	return template.HTML("<script>\n" + p.BodyJS + "\n</script>\n")
}
