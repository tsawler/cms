package admin

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/microcosm-cc/bluemonday"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
)

// editorHTMLPolicy sanitizes region HTML saved by non-admin users.
// contenteditable output and hand-written HTML from editors is untrusted;
// UGCPolicy strips scripts, event handlers, and the like, while "class" is
// allowed so framework-styled markup (e.g. Tailwind) survives. Inline
// styles are stripped except text-align with known values, which is how
// the editor's alignment buttons work.
var pixelHeightRe = regexp.MustCompile(`^[0-9]{1,4}px$`)

// mediaURLRe bounds the image gear's rendition-URL data attributes:
// absolute http(s) or app-relative paths only.
var mediaURLRe = regexp.MustCompile(`^(?:https?://|/)[^\s"'<>\\]+$`)

// embedURLRe bounds iframe sources to the exact embed URLs the editor's
// video slot generates: YouTube (privacy-enhanced host included) and
// Vimeo players, nothing else.
var embedURLRe = regexp.MustCompile(`^https://(?:www\.)?youtube(?:-nocookie)?\.com/embed/[\w-]{5,20}$|^https://player\.vimeo\.com/video/[0-9]{4,15}$`)

// The button editor (editor.js) stores its settings as inline styles on
// <a class="cms-btn"> elements. Browsers serialize colors as rgb(...) in
// cssText, so both hex and rgb forms must pass.
var (
	cssColorRe   = regexp.MustCompile(`^(#[0-9a-fA-F]{6}|rgb\([0-9]{1,3}, [0-9]{1,3}, [0-9]{1,3}\))$`)
	cssBorderRe  = regexp.MustCompile(`^[0-9]px solid (#[0-9a-fA-F]{6}|rgb\([0-9]{1,3}, [0-9]{1,3}, [0-9]{1,3}\))$`)
	cssPxRe      = regexp.MustCompile(`^[0-9]{1,3}px$`)
	cssPaddingRe = regexp.MustCompile(`^[0-9]{1,3}px [0-9]{1,3}px$`)
	btnSizeRe    = regexp.MustCompile(`^[sml]$`)
)

var editorHTMLPolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Globally()
	p.AllowStyles("text-align").MatchingEnum("left", "right", "center", "justify").Globally()
	// Used by snippets (e.g. quote cards).
	p.AllowElements("figure", "figcaption")
	// The image gear stores both rendition URLs on the image (so the
	// compressed/original choice survives re-editing) and lazy-loads
	// embedded images. URLs must look like http(s) or app-relative —
	// no javascript: or data: schemes.
	p.AllowAttrs("data-cms-web", "data-cms-orig").Matching(mediaURLRe).OnElements("img")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(?:lazy|eager)$`)).OnElements("img")
	// Videos inserted by the editor: a plain <video> playing a
	// media-library file. Sources are bounded like image URLs; the
	// boolean controls attribute may serialize empty or mirrored.
	p.AllowAttrs("src", "poster").Matching(mediaURLRe).OnElements("video")
	p.AllowAttrs("controls").Matching(regexp.MustCompile(`^(?:controls)?$`)).OnElements("video")
	p.AllowAttrs("preload").Matching(regexp.MustCompile(`^(?:none|metadata|auto)$`)).OnElements("video")
	p.AllowAttrs("width", "height").Matching(regexp.MustCompile(`^[0-9]{1,4}$`)).OnElements("video")
	// Unfilled video-slot placeholders from the video snippets survive
	// saves; the editor turns them into a <video> or an embed on click.
	p.AllowAttrs("data-cms-video-slot").Matching(regexp.MustCompile(`^$`)).OnElements("div")
	// External video embeds from the video slot: iframes strictly bounded
	// to YouTube/Vimeo player URLs, with only presentation attributes.
	p.AllowAttrs("src").Matching(embedURLRe).OnElements("iframe")
	p.AllowAttrs("title").Matching(regexp.MustCompile(`^[^<>"]{0,200}$`)).OnElements("iframe")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(?:lazy|eager)$`)).OnElements("iframe")
	p.AllowAttrs("allow").Matching(regexp.MustCompile(`^[a-z-;* ]*$`)).OnElements("iframe")
	p.AllowAttrs("allowfullscreen").Matching(regexp.MustCompile(`^(?:|allowfullscreen)$`)).OnElements("iframe")
	// The "Flexible space" snippet stores its height on the element.
	p.AllowStyles("height").Matching(pixelHeightRe).Globally()
	p.AllowAttrs("data-height").Matching(pixelHeightRe).OnElements("div")
	// Button-editor styles, on links only.
	p.AllowStyles("background-color", "border-color", "color").Matching(cssColorRe).OnElements("a")
	p.AllowStyles("border").Matching(cssBorderRe).OnElements("a")
	p.AllowStyles("border-width", "border-radius", "font-size").Matching(cssPxRe).OnElements("a")
	p.AllowStyles("border-style").MatchingEnum("solid").OnElements("a")
	p.AllowStyles("padding").Matching(cssPaddingRe).OnElements("a")
	p.AllowAttrs("data-cms-btn-size").Matching(btnSizeRe).OnElements("a")
	// Open-in-new-tab from the button editor.
	p.AllowAttrs("target").Matching(regexp.MustCompile(`^_blank$`)).OnElements("a")
	p.AllowAttrs("rel").Matching(regexp.MustCompile(`^noopener( noreferrer)?$`)).OnElements("a")
	return p
}()

func (s *server) pagesList(w http.ResponseWriter, r *http.Request) {
	// Posts' backing pages are managed under Blog & News, not here.
	pages, err := s.deps.Content.AllNonPost(r.Context(), s.deps.DefaultLocale)
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
			s.renderPageForm(w, r, form, true, map[string]string{"slug": s.tr(r, "That address is already used by another page.")})
			return
		}
		s.serverError(w, err)
		return
	}

	s.seedStarterSections(r.Context(), id, form.TemplateName)
	s.contentChanged()

	s.flash(r, s.tr(r, "Page created — now add your content below."))
	http.Redirect(w, r, s.deps.AdminPath+"/pages/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// starterSectionHTML is the content seeded into a brand-new page on a
// sections-only template: the same simple text snippet as the default
// "Text" palette entry, so the page opens with something visibly
// editable instead of an empty void.
const starterSectionHTML = `<p class="cms-snippet">Write your text here.</p>`

// seedStarterSections gives a newly created page a first section holding
// a simple text snippet — but only when the template's editable regions
// are all sections regions (a "blank canvas" shape). Templates with text
// or rich regions already have visible placeholders, so they're left
// alone. Seeding failure is logged rather than fatal: the page exists
// and is fully usable either way.
func (s *server) seedStarterSections(ctx context.Context, pageID int64, templateName string) {
	var sectionRegions []string
	for _, region := range s.deps.Renderer.Regions(templateName) {
		if region.Kind != "sections" {
			return
		}
		sectionRegions = append(sectionRegions, region.Name)
	}
	seed := []content.SectionInput{{
		Content: starterSectionHTML,
		Settings: map[string]string{
			"bg":    s.deps.SectionStyles.Background("").Key,
			"width": s.deps.SectionStyles.Width("").Key,
		},
	}}
	for _, name := range sectionRegions {
		if err := s.deps.Content.ReplaceDraftSections(ctx, pageID, name, s.deps.DefaultLocale, seed); err != nil {
			s.deps.Logger.Error("cms admin: seeding starter section", "page", pageID, "region", name, "err", err)
		}
	}
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
	locale := s.formLocale(r)

	// Non-default locale tabs edit only per-locale data: metadata and
	// region content. Slug, template, and code fields belong to the
	// default tab (they are locale-independent).
	if locale != s.deps.DefaultLocale {
		title := strings.TrimSpace(r.PostFormValue("title"))
		if title == "" {
			s.renderPageForm(w, r, existing, false, map[string]string{"title": s.tr(r, "Title is required.")})
			return
		}
		desc := strings.TrimSpace(r.PostFormValue("description"))
		if err := s.deps.Content.UpdateMeta(r.Context(), existing.ID, locale, title, desc); err != nil {
			s.serverError(w, err)
			return
		}
		if err := s.saveRegionContent(r, existing, locale); err != nil {
			s.serverError(w, err)
			return
		}
		s.finishContentSave(w, r, existing.ID, "Page",
			"pages/"+strconv.FormatInt(existing.ID, 10), locale)
		return
	}

	form, errs := s.parsePageMeta(r)
	form.ID = existing.ID
	form.Status = existing.Status

	// Per-page CSS/JS can inject arbitrary code into the public site, so
	// only admins may change it; for editors the existing values persist.
	if s.currentUser(r).Role.IsAdmin() {
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
			s.renderPageForm(w, r, form, false, map[string]string{"slug": s.tr(r, "That address is already used by another page.")})
			return
		}
		s.serverError(w, err)
		return
	}

	if err := s.saveRegionContent(r, form, s.deps.DefaultLocale); err != nil {
		s.serverError(w, err)
		return
	}

	s.finishContentSave(w, r, form.ID, "Page", "pages/"+strconv.FormatInt(form.ID, 10), s.deps.DefaultLocale)
}

// finishContentSave applies the page/post form's submit action
// (save/publish/unpublish), sets the flash, and redirects back to the
// form, keeping a non-default editing locale's tab selected.
func (s *server) finishContentSave(w http.ResponseWriter, r *http.Request, pageID int64, noun, formPath, locale string) {
	// Full sentences are the translation keys, so the noun switches
	// between complete messages rather than being concatenated.
	published := s.tr(r, "Page published.")
	unpublished := s.tr(r, "Page unpublished — it is no longer visible on the site.")
	if noun == "Post" {
		published = s.tr(r, "Post published.")
		unpublished = s.tr(r, "Post unpublished — it is no longer visible on the site.")
	}
	switch r.PostFormValue("action") {
	case "publish":
		if err := s.deps.Content.Publish(r.Context(), pageID); err != nil {
			s.serverError(w, err)
			return
		}
		s.flash(r, published)
	case "unpublish":
		if err := s.deps.Content.Unpublish(r.Context(), pageID); err != nil {
			s.serverError(w, err)
			return
		}
		s.flash(r, unpublished)
	default:
		s.flash(r, s.tr(r, "Draft saved. Publish when you're ready to make it live."))
	}
	s.contentChanged()

	url := s.deps.AdminPath + "/" + formPath
	if locale != s.deps.DefaultLocale {
		url += "?locale=" + locale
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// saveRegionContent stores the submitted region fields as draft blocks
// for the given locale. The regions_template hidden field names the
// template whose regions the form displayed, which may differ from a
// newly selected template.
func (s *server) saveRegionContent(r *http.Request, page *content.Page, locale string) error {
	regionsTemplate := r.PostFormValue("regions_template")
	if !s.deps.Renderer.Knows(regionsTemplate) {
		return nil
	}
	values := map[string]string{}
	for _, region := range s.deps.Renderer.Regions(regionsTemplate) {
		values[region.Name] = r.PostFormValue("region-" + region.Name)
	}
	isAdmin := s.currentUser(r).Role.IsAdmin()
	return s.saveRegions(r.Context(), page.ID, regionsTemplate, values, isAdmin, locale)
}

// saveRegions writes region values as draft blocks. Only regions the
// template actually declares are stored — unknown names in values are
// ignored — and each value is treated per its region's kind: text is stored
// raw (escaped at render time), image URLs are validated, and HTML from
// non-admins is sanitized. Shared by the page form and the in-place editor
// API.
func (s *server) saveRegions(ctx context.Context, pageID int64, templateName string, values map[string]string, isAdmin bool, locale string) error {
	for _, region := range s.deps.Renderer.Regions(templateName) {
		value, ok := values[region.Name]
		if !ok {
			continue
		}
		kind := content.KindHTML
		switch {
		case region.Kind == "sections":
			// Sections save through their own endpoint; a single-block
			// upsert here would clobber the section list.
			continue
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
			locale, kind, value); err != nil {
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
	if page.Slug == "" {
		s.flash(r, s.tr(r, "The home page can't be deleted."))
		http.Redirect(w, r, s.deps.AdminPath+"/pages/"+strconv.FormatInt(page.ID, 10), http.StatusSeeOther)
		return
	}
	if err := s.deps.Content.Delete(r.Context(), page.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.contentChanged()
	s.flash(r, s.tr(r, "Page deleted."))
	http.Redirect(w, r, s.deps.AdminPath+"/pages", http.StatusSeeOther)
}

// pageDiscard throws away the page's unpublished draft edits, reverting its
// draft content to match what is currently published.
func (s *server) pageDiscard(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	if page.Status != content.StatusPublished {
		s.flash(r, s.tr(r, "There are no published changes to revert to — this page hasn't been published yet."))
		http.Redirect(w, r, s.deps.AdminPath+"/pages/"+strconv.FormatInt(page.ID, 10), http.StatusSeeOther)
		return
	}
	if err := s.deps.Content.DiscardDraft(r.Context(), page.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.contentChanged()
	s.flash(r, s.tr(r, "Draft changes discarded — the editor now matches the published page."))
	http.Redirect(w, r, s.deps.AdminPath+"/pages/"+strconv.FormatInt(page.ID, 10), http.StatusSeeOther)
}

// pagePreview renders the page's draft content with the real site
// templates, so editors can see unpublished work exactly as it will appear.
func (s *server) pagePreview(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	locale := s.formLocale(r)
	blocks, err := s.deps.Content.EffectiveBlocks(r.Context(), page.ID, locale, content.StatusDraft)
	if err != nil {
		s.serverError(w, err)
		return
	}
	menuItems, err := s.deps.Content.MenuItems(r.Context(), "")
	if err != nil {
		menuItems = nil
	}
	menus := render.BuildMenus(menuItems, page.Slug, locale, s.deps.DefaultLocale, true)
	site, _ := s.deps.Content.SiteSettings(r.Context()) // preview renders fine with defaults
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.deps.Renderer.Render(w, render.Input{
		Page:    page,
		Blocks:  blocks,
		Locale:  locale,
		Menus:   menus,
		Locales: s.deps.Locales,
		Site:    site,
	}); err != nil {
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
		Visibility:   content.Visibility(r.PostFormValue("visibility")),
	}
	// The form offers exactly two visibilities; anything else (including a
	// missing field) falls back to the safe default.
	if !content.ValidVisibility(string(p.Visibility)) {
		p.Visibility = content.VisibilityPublic
	}

	if p.Title == "" {
		errs["title"] = s.tr(r, "Title is required.")
	}
	if !content.ValidSlug(p.Slug) {
		errs["slug"] = s.tr(r, "Use only lowercase letters, numbers, and hyphens, e.g. about-us.")
	} else if s.localeSlugCollision(p.Slug) {
		errs["slug"] = s.tr(r, "That address starts with a language code, which is reserved for translated pages.")
	}
	if !s.deps.Renderer.Knows(p.TemplateName) {
		errs["template_name"] = s.tr(r, "Choose a template.")
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
	data.EditLocale = s.formLocale(r)

	data.RegionsTemplate = page.TemplateName

	if !isNew {
		data.Regions = s.deps.Renderer.Regions(page.TemplateName)
		blocks, err := s.deps.Content.EffectiveBlocks(r.Context(), page.ID, data.EditLocale, content.StatusDraft)
		if err != nil {
			s.serverError(w, err)
			return
		}
		data.BlockContent = make(map[string]string, len(blocks))
		for _, b := range blocks {
			data.BlockContent[b.Region] = b.Content
		}

		// A published page whose draft differs can revert to what's live.
		if page.Status == content.StatusPublished {
			changed, err := s.deps.Content.HasUnpublishedChanges(r.Context(), page.ID)
			if err != nil {
				s.serverError(w, err)
				return
			}
			data.HasDraftEdits = changed
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
	page, err := s.deps.Content.GetByID(r.Context(), id, s.formLocale(r))
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
