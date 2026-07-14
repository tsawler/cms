package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/microcosm-cc/bluemonday"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/media"
)

// editorHTMLPolicy sanitizes region HTML saved by non-admin users.
// contenteditable output and hand-written HTML from editors is untrusted;
// UGCPolicy strips scripts, event handlers, and the like, while "class" is
// allowed so framework-styled markup (e.g. Tailwind) survives.
var editorHTMLPolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Globally()
	return p
}()

func (s *server) pagesList(w http.ResponseWriter, r *http.Request) {
	pages, err := s.deps.Content.All(r.Context(), s.deps.DefaultLocale)
	if err != nil {
		s.serverError(w, err)
		return
	}
	data := s.newTemplateData(r)
	data.Pages = pages
	data.PageTemplates = s.deps.Renderer.PageTemplates()
	s.render(w, http.StatusOK, "pages", data)
}

func (s *server) pageNew(w http.ResponseWriter, r *http.Request) {
	data := s.newTemplateData(r)
	data.IsNew = true
	data.FormPage = &content.Page{}
	data.PageTemplates = s.deps.Renderer.PageTemplates()
	s.render(w, http.StatusOK, "page_form", data)
}

func (s *server) pageCreate(w http.ResponseWriter, r *http.Request) {
	form, errs := s.parsePageMeta(r)
	if len(errs) > 0 {
		s.renderPageForm(w, r, form, true, errs)
		return
	}

	id, err := s.deps.Content.Insert(r.Context(), form, s.deps.DefaultLocale)
	if err != nil {
		if errors.Is(err, content.ErrDuplicateSlug) {
			s.renderPageForm(w, r, form, true, map[string]string{"slug": "That address is already used by another page."})
			return
		}
		s.serverError(w, err)
		return
	}

	s.flash(r, "Page created — now add your content below.")
	http.Redirect(w, r, s.deps.AdminPath+"/pages/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *server) pageEdit(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	s.renderPageForm(w, r, page, false, nil)
}

func (s *server) pageUpdate(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}

	form, errs := s.parsePageMeta(r)
	form.ID = existing.ID
	form.Status = existing.Status

	// Per-page CSS/JS can inject arbitrary code into the public site, so
	// only admins may change it; for editors the existing values persist.
	if s.currentUser(r).Role == auth.RoleAdmin {
		form.HeadCSS = r.PostFormValue("head_css")
		form.BodyJS = r.PostFormValue("body_js")
	} else {
		form.HeadCSS = existing.HeadCSS
		form.BodyJS = existing.BodyJS
	}

	if len(errs) > 0 {
		s.renderPageForm(w, r, form, false, errs)
		return
	}

	if err := s.deps.Content.Update(r.Context(), form, s.deps.DefaultLocale); err != nil {
		if errors.Is(err, content.ErrDuplicateSlug) {
			s.renderPageForm(w, r, form, false, map[string]string{"slug": "That address is already used by another page."})
			return
		}
		s.serverError(w, err)
		return
	}

	if err := s.saveRegionContent(r, form); err != nil {
		s.serverError(w, err)
		return
	}

	switch r.PostFormValue("action") {
	case "publish":
		if err := s.deps.Content.Publish(r.Context(), form.ID); err != nil {
			s.serverError(w, err)
			return
		}
		s.flash(r, "Page published.")
	case "unpublish":
		if err := s.deps.Content.Unpublish(r.Context(), form.ID); err != nil {
			s.serverError(w, err)
			return
		}
		s.flash(r, "Page unpublished — it is no longer visible on the site.")
	default:
		s.flash(r, "Draft saved. Publish when you're ready to make it live.")
	}

	http.Redirect(w, r, s.deps.AdminPath+"/pages/"+strconv.FormatInt(form.ID, 10), http.StatusSeeOther)
}

// saveRegionContent stores the submitted region fields as draft blocks. The
// regions_template hidden field names the template whose regions the form
// displayed, which may differ from a newly selected template.
func (s *server) saveRegionContent(r *http.Request, page *content.Page) error {
	regionsTemplate := r.PostFormValue("regions_template")
	if !s.deps.Renderer.Knows(regionsTemplate) {
		return nil
	}
	values := map[string]string{}
	for _, region := range s.deps.Renderer.Regions(regionsTemplate) {
		values[region.Name] = r.PostFormValue("region-" + region.Name)
	}
	isAdmin := s.currentUser(r).Role == auth.RoleAdmin
	return s.saveRegions(r.Context(), page.ID, regionsTemplate, values, isAdmin)
}

// saveRegions writes region values as draft blocks. Only regions the
// template actually declares are stored — unknown names in values are
// ignored — and each value is treated per its region's kind: text is stored
// raw (escaped at render time), image URLs are validated, and HTML from
// non-admins is sanitized. Shared by the page form and the in-place editor
// API.
func (s *server) saveRegions(ctx context.Context, pageID int64, templateName string, values map[string]string, isAdmin bool) error {
	for _, region := range s.deps.Renderer.Regions(templateName) {
		value, ok := values[region.Name]
		if !ok {
			continue
		}
		kind := content.KindHTML
		switch {
		case region.Kind == "text":
			kind = content.KindText
		case region.Kind == "image":
			kind = content.KindImage
			if !validImageURL(value) {
				continue
			}
		case !isAdmin:
			value = editorHTMLPolicy.Sanitize(value)
		}
		if err := s.deps.Content.UpsertDraftBlock(ctx, pageID, region.Name,
			s.deps.DefaultLocale, kind, value); err != nil {
			return err
		}
	}
	return nil
}

// validImageURL accepts empty (no image), app-relative, or http(s) URLs.
func validImageURL(v string) bool {
	return v == "" || strings.HasPrefix(v, "/") ||
		strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://")
}

func (s *server) pageDelete(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	if err := s.deps.Content.Delete(r.Context(), page.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, "Page deleted.")
	http.Redirect(w, r, s.deps.AdminPath+"/pages", http.StatusSeeOther)
}

// pagePreview renders the page's draft content with the real site
// templates, so editors can see unpublished work exactly as it will appear.
func (s *server) pagePreview(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	blocks, err := s.deps.Content.BlocksFor(r.Context(), page.ID, s.deps.DefaultLocale, content.StatusDraft)
	if err != nil {
		s.serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.deps.Renderer.Render(w, page, blocks, s.deps.DefaultLocale, nil); err != nil {
		s.serverError(w, err)
	}
}

// parsePageMeta reads and validates the metadata fields shared by the new
// and edit forms.
func (s *server) parsePageMeta(r *http.Request) (*content.Page, map[string]string) {
	errs := map[string]string{}

	p := &content.Page{
		Title:        strings.TrimSpace(r.PostFormValue("title")),
		Description:  strings.TrimSpace(r.PostFormValue("description")),
		Slug:         content.NormalizeSlug(r.PostFormValue("slug")),
		TemplateName: r.PostFormValue("template_name"),
	}

	if p.Title == "" {
		errs["title"] = "Title is required."
	}
	if !content.ValidSlug(p.Slug) {
		errs["slug"] = "Use only lowercase letters, numbers, and hyphens, e.g. about-us."
	}
	if !s.deps.Renderer.Knows(p.TemplateName) {
		errs["template_name"] = "Choose a template."
	}

	return p, errs
}

func (s *server) renderPageForm(w http.ResponseWriter, r *http.Request, page *content.Page, isNew bool, errs map[string]string) {
	status := http.StatusOK
	if len(errs) > 0 {
		status = http.StatusUnprocessableEntity
	}

	data := s.newTemplateData(r)
	data.FormPage = page
	data.IsNew = isNew
	data.FormErrors = errs
	data.PageTemplates = s.deps.Renderer.PageTemplates()

	if !isNew {
		data.Regions = s.deps.Renderer.Regions(page.TemplateName)
		blocks, err := s.deps.Content.BlocksFor(r.Context(), page.ID, s.deps.DefaultLocale, content.StatusDraft)
		if err != nil {
			s.serverError(w, err)
			return
		}
		data.BlockContent = make(map[string]string, len(blocks))
		for _, b := range blocks {
			data.BlockContent[b.Region] = b.Content
		}

		// Image regions render a picker, which needs the media library.
		if s.deps.Media != nil {
			for _, region := range data.Regions {
				if region.Kind == "image" {
					items, err := s.deps.Media.All(r.Context(), s.deps.DefaultLocale, media.ListOptions{Kind: media.KindImage})
					if err != nil {
						s.serverError(w, err)
						return
					}
					data.Media = s.deps.Media.Views(items)
					break
				}
			}
		}
	}

	s.render(w, status, "page_form", data)
}

// pageFromURL loads the page identified by the {id} URL parameter, writing
// a 404 and returning ok=false when it is missing or malformed.
func (s *server) pageFromURL(w http.ResponseWriter, r *http.Request) (*content.Page, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	page, err := s.deps.Content.GetByID(r.Context(), id, s.deps.DefaultLocale)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		s.serverError(w, err)
		return nil, false
	}
	return page, true
}
