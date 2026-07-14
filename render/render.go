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
	"cmsHead":    func() template.HTML { return "" },
	"cmsScripts": func() template.HTML { return "" },
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
// page.
func (r *Renderer) Render(w io.Writer, page *content.Page, blocks []content.Block, locale string) error {
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

	clone.Funcs(template.FuncMap{
		"cmsText": func(key string) string {
			for _, b := range byRegion[key] {
				if b.Kind == content.KindText {
					return b.Content
				}
			}
			return ""
		},
		"cmsRegion": func(key string) template.HTML {
			var sb strings.Builder
			for _, b := range byRegion[key] {
				sb.WriteString(b.Content)
			}
			return template.HTML(sb.String())
		},
		"cmsHead":    func() template.HTML { return headHTML(page) },
		"cmsScripts": func() template.HTML { return scriptsHTML(page) },
	})

	var buf bytes.Buffer
	if err := clone.ExecuteTemplate(&buf, path.Base(page.TemplateName), PageData{
		Title:       page.Title,
		Description: page.Description,
		Slug:        page.Slug,
		Locale:      locale,
	}); err != nil {
		return fmt.Errorf("render: executing %s: %w", page.TemplateName, err)
	}
	_, err = buf.WriteTo(w)
	return err
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
