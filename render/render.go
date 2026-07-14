// Package render executes the host application's Go templates with the
// CMS's template funcs (cmsText, cmsRegion, cmsHead, cmsScripts) bound to a
// specific page's content. The host owns all page markup and styling; the
// CMS only fills the editable holes the templates declare.
package render

import (
	"bytes"
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
// Label is what content editors see in the admin UI.
type PageTemplate struct {
	File  string
	Label string
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
	"cmsText":    func(string) string { return "" },
	"cmsRegion":  func(string) template.HTML { return "" },
	"cmsImage":   func(string) string { return "" },
	"cmsHead":    func() template.HTML { return "" },
	"cmsScripts": func() template.HTML { return "" },
}

// EditorScriptPath is the public route the in-place editor script is served
// from.
const EditorScriptPath = "/cms/editor/editor.js"

// EditInfo turns a render into an editable one: regions are wrapped in
// marker elements and the in-place editor script is injected before
// </body>. Pass nil for a plain public render.
type EditInfo struct {
	PageID       int64
	AdminPath    string
	CSRFToken    string
	Locale       string
	Status       string // "draft" or "published"
	MediaEnabled bool
}

// Renderer holds one parsed template set per page template: the shared
// templates (layouts, partials) plus that page's file. Per-file sets let
// every page define the same block names (e.g. "content") without
// colliding.
type Renderer struct {
	sets      map[string]*template.Template // keyed by PageTemplate.File
	templates []PageTemplate
}

// New parses each page template together with the shared globs and returns
// a Renderer. It fails fast on any template that doesn't parse.
func New(fsys fs.FS, shared []string, pages []PageTemplate) (*Renderer, error) {
	if len(pages) == 0 {
		return nil, fmt.Errorf("render: at least one PageTemplate is required")
	}
	r := &Renderer{
		sets:      make(map[string]*template.Template, len(pages)),
		templates: pages,
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
		out = injectEditorScript(out, edit)
	}
	_, err = w.Write(out)
	return err
}

// injectEditorScript inserts the in-place editor's script tag before the
// closing </body> tag (or at the end when there isn't one).
func injectEditorScript(page []byte, edit *EditInfo) []byte {
	mediaFlag := "0"
	if edit.MediaEnabled {
		mediaFlag = "1"
	}
	tag := `<script src="` + EditorScriptPath + `" defer` +
		` data-page-id="` + strconv.FormatInt(edit.PageID, 10) + `"` +
		` data-admin-path="` + html.EscapeString(edit.AdminPath) + `"` +
		` data-csrf="` + html.EscapeString(edit.CSRFToken) + `"` +
		` data-status="` + html.EscapeString(edit.Status) + `"` +
		` data-locale="` + html.EscapeString(edit.Locale) + `"` +
		` data-media="` + mediaFlag + `"></script>`

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
