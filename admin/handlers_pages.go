package admin

import (
	"context"
	"errors"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/microcosm-cc/bluemonday"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

// editorHTMLPolicy sanitizes region HTML saved by non-admin users.
// contenteditable output and hand-written HTML from editors is untrusted;
// UGCPolicy strips scripts, event handlers, and the like, while "class" is
// allowed so framework-styled markup (e.g. Tailwind) survives. Inline
// styles are stripped except the tightly-matched properties the editor's
// own tools emit: text-align (alignment buttons), the button gear's
// styles on links, and the block gear's styles on snippet roots.
var pixelHeightRe = regexp.MustCompile(`^[0-9]{1,4}px$`)

// mediaURLRe bounds the image gear's rendition-URL data attributes:
// absolute http(s) or app-relative paths only.
var mediaURLRe = regexp.MustCompile(`^(?:https?://|/)[^\s"'<>\\]+$`)

// embedURLRe bounds iframe sources to the exact embed URLs the editor's
// video slot generates: YouTube (privacy-enhanced host included) and
// Vimeo players, nothing else.
var embedURLRe = regexp.MustCompile(`^https://(?:www\.)?youtube(?:-nocookie)?\.com/embed/[\w-]{5,20}$` +
	`|^https://player\.vimeo\.com/video/[0-9]{4,15}$` +
	// Google Maps, both shapes the map slot emits: the official
	// Share > Embed URL, and the keyless q=…&output=embed form built
	// from a pasted link or typed address.
	`|^https://(?:www\.)?google\.com/maps/embed\?pb=[^\s"'<>\\]+$` +
	`|^https://(?:maps\.google\.com|(?:www\.)?google\.com)/maps\?[^\s"'<>\\]*output=embed[^\s"'<>\\]*$`)

// The button editor (editor.js) stores its settings as inline styles on
// <a class="cms-btn"> elements. Browsers serialize colors as rgb(...) in
// cssText, so both hex and rgb forms must pass.
var (
	cssColorRe   = regexp.MustCompile(`^(#[0-9a-fA-F]{6}|rgb\([0-9]{1,3}, [0-9]{1,3}, [0-9]{1,3}\))$`)
	cssBorderRe  = regexp.MustCompile(`^[0-9]px solid (#[0-9a-fA-F]{6}|rgb\([0-9]{1,3}, [0-9]{1,3}, [0-9]{1,3}\))$`)
	cssPxRe      = regexp.MustCompile(`^[0-9]{1,3}px$`)
	cssPaddingRe = regexp.MustCompile(`^[0-9]{1,3}px( [0-9]{1,3}px)?$`)
	btnSizeRe    = regexp.MustCompile(`^[sml]$`)
	// The block gear (snippet settings) stores curated spacing presets.
	snipSpacingRe = regexp.MustCompile(`^(compact|normal|roomy)$`)
	// The slider gear's two settings: the transition, and the autoplay
	// interval in milliseconds. The interval is bounded rather than any
	// number — sliderJS ignores anything under a second, and a bound
	// here means a hand-written value cannot ask for a slider that
	// flickers.
	sliderTransitionRe = regexp.MustCompile(`^(fade|slide)$`)
	sliderAutoRe       = regexp.MustCompile(`^([1-9][0-9]{3}|[1-5][0-9]{4}|60000)$`)
	// The gear's text color rides on the block root; descendants whose
	// classes set their own color get pinned to color:inherit so the
	// choice actually takes.
	cssColorInheritRe = regexp.MustCompile(`^(#[0-9a-fA-F]{6}|rgb\([0-9]{1,3}, [0-9]{1,3}, [0-9]{1,3}\)|inherit)$`)
	// Elements a snippet block's root can be (the gear applies to any
	// .cms-snippet, so its styles are scoped to these block tags).
	snipBlockEls = []string{"div", "p", "figure", "blockquote", "section"}
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
	// Likewise the photo slots from the imported library: the editor
	// swaps a clicked slot for a media-library <img>.
	p.AllowAttrs("data-cms-photo-slot").Matching(regexp.MustCompile(`^$`)).OnElements("div")
	// And the map slots: a click prompts for a Google Maps link or an
	// address and swaps in a bounded maps iframe.
	p.AllowAttrs("data-cms-map-slot").Matching(regexp.MustCompile(`^$`)).OnElements("div")
	// The slider block's two settings. Everything else a slider carries
	// at runtime — which slide is showing, the arrows, the dots — is
	// built by sliderJS and deliberately not allowed through: it is
	// generated chrome, and content that described it would be content
	// that could disagree with it. The editor strips it before every
	// save (stripSliderChrome); this is the server saying the same thing
	// to a request that arrived any other way.
	p.AllowAttrs("data-cms-slider").Matching(sliderTransitionRe).OnElements("div")
	p.AllowAttrs("data-cms-slider-auto").Matching(sliderAutoRe).OnElements("div")
	// External video embeds from the video slot: iframes strictly bounded
	// to YouTube/Vimeo player URLs, with only presentation attributes.
	p.AllowAttrs("src").Matching(embedURLRe).OnElements("iframe")
	p.AllowAttrs("title").Matching(regexp.MustCompile(`^[^<>"]{0,200}$`)).OnElements("iframe")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(?:lazy|eager)$`)).OnElements("iframe")
	p.AllowAttrs("allow").Matching(regexp.MustCompile(`^[a-z-;* ]*$`)).OnElements("iframe")
	p.AllowAttrs("allowfullscreen").Matching(regexp.MustCompile(`^(?:|allowfullscreen)$`)).OnElements("iframe")
	// Custom-code blocks: the placeholder names a library entry and
	// carries nothing executable itself, so it survives an editor's save
	// like any other markup — which is the point of storing the code out
	// of line. A key that no longer exists renders as the empty div it
	// is. The key vocabulary is the library's own, not a second copy.
	p.AllowAttrs("data-cms-code").Matching(snippets.CodeKeyPattern()).OnElements("div")
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
	// Block-gear styles (editor.js snippet settings), on block roots.
	p.AllowStyles("background-color").Matching(cssColorRe).OnElements(snipBlockEls...)
	p.AllowStyles("padding").Matching(cssPaddingRe).OnElements(snipBlockEls...)
	p.AllowStyles("margin-top", "margin-bottom", "border-radius").Matching(cssPxRe).OnElements(snipBlockEls...)
	p.AllowAttrs("data-cms-snip-spacing").Matching(snipSpacingRe).OnElements(snipBlockEls...)
	// Text color lands on the block root, but the color:inherit pins can
	// sit on any descendant — so the (tightly value-bound) property is
	// allowed everywhere. Named colors still don't pass.
	p.AllowStyles("color").Matching(cssColorInheritRe).Globally()
	// Open-in-new-tab from the button editor.
	p.AllowAttrs("target").Matching(regexp.MustCompile(`^_blank$`)).OnElements("a")
	p.AllowAttrs("rel").Matching(regexp.MustCompile(`^noopener( noreferrer)?$`)).OnElements("a")
	return p
}()

func (s *server) pagesList(w http.ResponseWriter, r *http.Request) {
	// Posts' backing pages are managed under Blog & News, not here — so
	// the count excludes them exactly as the window does, and the page
	// count describes what the table shows.
	total, err := s.deps.Content.CountNonPost(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	// Count, then clamp, then fetch — so a ?page= past the end reads the
	// last real page rather than a window off the end of the list.
	pager := render.NewPager(listPage(r), s.perPage(), total, s.listPageURL(r))
	pager.PrevLabel, pager.NextLabel = s.tr(r, "Previous"), s.tr(r, "Next")

	pages, err := s.deps.Content.AllNonPostPage(r.Context(), s.deps.DefaultLocale,
		pager.PerPage, pager.Offset())
	if err != nil {
		s.serverError(w, err)
		return
	}
	data := s.newTemplateData(r)
	data.Pages = pages
	data.PageTemplates = s.deps.Renderer.PageTemplates()
	data.Pager = pager
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

// sectionRegions lists a template's sections regions in source order —
// but only when every editable region it declares is one (the "blank
// canvas" shape). Templates with text or rich regions already have
// visible placeholders and want no seeded content, so they get nil.
//
// Order carries meaning once a template has more than one: the first is
// the banner above the content — the post template's header region — and
// the last is the main content area. That is what decides where a new
// page's starter section goes, and where a new post's banner does.
func (s *server) sectionRegions(templateName string) []string {
	var names []string
	for _, region := range s.deps.Renderer.Regions(templateName) {
		if region.Kind != "sections" {
			return nil
		}
		names = append(names, region.Name)
	}
	return names
}

// seedStarterSections gives a newly created page a first section holding
// a simple text snippet, in its main content region. Seeding failure is
// logged rather than fatal: the page exists and is fully usable either
// way.
//
// Only the content region is seeded. A banner region above it starts
// empty on purpose — a post with no image chosen has no banner until
// someone adds one, and an empty region still offers its own "Add
// section" button.
func (s *server) seedStarterSections(ctx context.Context, pageID int64, templateName string) {
	names := s.sectionRegions(templateName)
	if len(names) == 0 {
		return
	}
	main := names[len(names)-1]
	seed := []content.SectionInput{{
		Content: starterSectionHTML,
		Settings: map[string]string{
			"bg":    s.deps.SectionStyles.Background("").Key,
			"width": s.deps.SectionStyles.Width("").Key,
		},
	}}
	if err := s.deps.Content.ReplaceDraftSections(ctx, pageID, main, s.deps.DefaultLocale, seed); err != nil {
		s.deps.Logger.Error("cms admin: seeding starter section", "page", pageID, "region", main, "err", err)
	}
}

// bannerSeed is what a new post's banner section is built from: the
// picture, the words that start on top of it, and whether the picture is
// dark enough that those words need to be light.
type bannerSeed struct {
	URL   string
	Title string
	Dark  bool
}

// bannerSnippetHTML is a banner's starting block: the post's title,
// centered, in a colour that stands off the picture behind it.
//
// It is the page's <h1>. A banner showing the title is where a reader's
// eye starts, so it is the heading the page is about, and the template
// leaves the title to the banner when one is present (see
// cmsHasSections), printing its own <h1> only when there is no banner.
// Nothing here forces that on a host template, though — a template that
// ignores the banner keeps its own heading, and a page showing the title
// twice is untidy rather than broken.
//
// The colour and alignment are inline styles rather than classes. The
// editor's sanitizer allows exactly these two properties, so they
// survive an editor-role user re-saving the page — and a class would
// have to exist in whatever CSS the host site happens to be built with,
// which the CMS does not get to assume.
func bannerSnippetHTML(seed bannerSeed) string {
	color := "#111827"
	if seed.Dark {
		color = "#ffffff"
	}
	return `<h1 class="cms-snippet" style="text-align:center;color:` + color + `">` +
		html.EscapeString(seed.Title) + `</h1>`
}

// seedHeaderSection gives a newly created post the banner its creation
// form asked for: one section in the template's header region carrying
// the chosen image as its background. Choosing no image leaves the
// region empty, and its own "Add section" button is then how a banner
// gets added later.
//
// It starts with the post's title centered over the picture, both across
// and down, which is what a banner is; and it is a real snippet block, so
// clicking it brings up the block chrome — drag handle, source, gear,
// trash — where an empty region would only give a bare caret. Delete the
// line and the picture stands on its own.
//
// It is also given a height, so that deleting the line leaves a banner
// rather than nothing: an auto-height section with no content renders as
// no height at all, however good the picture. Everything about it —
// height, width, corners, where the image is anchored, the words and
// their colour — is editable on the page afterwards, which is the point
// of it being a section at all.
func (s *server) seedHeaderSection(ctx context.Context, pageID int64, templateName string, seed bannerSeed) {
	seed.URL = render.ValidBackgroundURL(seed.URL)
	if seed.URL == "" {
		return
	}
	names := s.sectionRegions(templateName)
	if len(names) < 2 {
		return // no region above the content to put a banner in
	}
	sections := []content.SectionInput{{
		Content: bannerSnippetHTML(seed),
		Settings: map[string]string{
			"bg":      s.deps.SectionStyles.Background("").Key,
			"width":   s.deps.SectionStyles.Width("").Key,
			"bgimage": seed.URL,
			"height":  "50",
			"valign":  "center",
		},
	}}
	if err := s.deps.Content.ReplaceDraftSections(ctx, pageID, names[0], s.deps.DefaultLocale, sections); err != nil {
		s.deps.Logger.Error("cms admin: seeding header section", "page", pageID, "region", names[0], "err", err)
	}
}

func (s *server) pageEdit(w http.ResponseWriter, r *http.Request) {
	page, ok := s.sitePageFromURL(w, r)
	if !ok {
		return
	}
	s.renderPageForm(w, r, page, false, nil)
}

func (s *server) pageUpdate(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.sitePageFromURL(w, r)
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
		// A page's description is its meta description; only posts, whose
		// description is a listing summary, carry a second one.
		meta := content.PageMeta{
			Title:       title,
			Description: strings.TrimSpace(r.PostFormValue("description")),
		}
		if err := s.deps.Content.UpdateMeta(r.Context(), existing.ID, locale, meta); err != nil {
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
// (save/publish), sets the flash, and redirects back to the form, keeping
// a non-default editing locale's tab selected.
//
// Unpublishing is deliberately not a form action: it posts to its own
// route, so taking content off the site can't drag whatever happens to be
// sitting in the form into the draft along with it.
func (s *server) finishContentSave(w http.ResponseWriter, r *http.Request, pageID int64, noun, formPath, locale string) {
	// Full sentences are the translation keys, so the noun switches
	// between complete messages rather than being concatenated.
	published := s.tr(r, "Page published.")
	if noun == "Post" {
		published = s.tr(r, "Post published.")
	}
	if r.PostFormValue("action") == "publish" {
		if err := s.publishWithShared(r.Context(), pageID); err != nil {
			s.serverError(w, err)
			return
		}
		s.flash(r, published)
	} else {
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
		if kind == content.KindHTML {
			// A custom-code block stores only its placeholder; whatever
			// the client had inside it is the widget's output, not
			// content (see render.CollapseCodePlaceholders).
			value = render.CollapseCodePlaceholders(value)
		}
		if err := s.deps.Content.UpsertDraftBlock(ctx, pageID, region.Name,
			locale, kind, value); err != nil {
			return err
		}
	}
	return nil
}

// splitSharedRegions separates the shared regions out of a submitted
// region map by their marker prefix (see render.SharedRegionPrefix),
// returning the page's own values and the shared ones under their bare
// names. The editor sends both in one request because it collects
// whatever regions were edited, and on any given page those can be a
// mixture of the page's and the site's.
func splitSharedRegions(values map[string]string) (page, shared map[string]string) {
	page = make(map[string]string, len(values))
	shared = map[string]string{}
	for name, value := range values {
		if bare, ok := render.SharedRegionName(name); ok {
			shared[bare] = value
			continue
		}
		page[name] = value
	}
	return page, shared
}

// saveSharedRegions writes shared-region values as draft blocks on the
// site page. Like saveRegions it stores only regions a template actually
// declares — the union across every template, since shared content has no
// template of its own — and sanitizes HTML from non-admins.
//
// Shared regions are rich HTML only: a footer is a block of content, and
// the text/image/sections kinds all exist to be placed by a page's own
// layout.
func (s *server) saveSharedRegions(ctx context.Context, values map[string]string, isAdmin bool, locale string) error {
	if len(values) == 0 {
		return nil
	}
	for _, region := range s.deps.Renderer.SharedRegions() {
		value, ok := values[region.Name]
		if !ok {
			continue
		}
		if !isAdmin {
			value = editorHTMLPolicy.Sanitize(value)
		}
		value = render.CollapseCodePlaceholders(value)
		if err := s.deps.Content.UpsertSharedBlock(ctx, region.Name, locale,
			content.KindHTML, value); err != nil {
			return err
		}
	}
	return nil
}

// publishWithShared makes a page's draft live, and the site's shared
// content along with it. Shared regions render on every page, so there is
// no page they could be published "on" — they ride along with whichever
// page the editor publishes, which is also the page they were edited on.
// Publishing twice over unchanged shared content is a no-op, so this costs
// nothing when nobody has touched the footer.
func (s *server) publishWithShared(ctx context.Context, pageID int64) error {
	if err := s.deps.Content.Publish(ctx, pageID); err != nil {
		return err
	}
	return s.deps.Content.PublishShared(ctx)
}

// validImageURL accepts empty (no image), app-relative, or http(s) URLs.
func validImageURL(v string) bool {
	return v == "" || strings.HasPrefix(v, "/") ||
		strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://")
}

func (s *server) pageDelete(w http.ResponseWriter, r *http.Request) {
	page, ok := s.sitePageFromURL(w, r)
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
	page, ok := s.sitePageFromURL(w, r)
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

// pageUnpublish takes the page off the public site. Content is untouched:
// both the draft and published block sets survive, so publishing again
// restores the page.
func (s *server) pageUnpublish(w http.ResponseWriter, r *http.Request) {
	page, ok := s.sitePageFromURL(w, r)
	if !ok {
		return
	}
	if err := s.deps.Content.Unpublish(r.Context(), page.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.contentChanged()
	s.flash(r, s.tr(r, "Page unpublished — it is no longer visible on the site."))
	http.Redirect(w, r, s.deps.AdminPath+"/pages/"+strconv.FormatInt(page.ID, 10), http.StatusSeeOther)
}

// pagePreview renders the page's draft content with the real site
// templates, so editors can see unpublished work exactly as it will appear.
func (s *server) pagePreview(w http.ResponseWriter, r *http.Request) {
	page, ok := s.sitePageFromURL(w, r)
	if !ok {
		return
	}
	locale := s.formLocale(r)
	blocks, shared, err := s.deps.Content.EffectiveBlocksWithShared(r.Context(), page.ID, locale, content.StatusDraft)
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
		Shared:  shared,
		Locale:  locale,
		Menus:   menus,
		Locales: s.deps.Locales,
		Site:    site,
		Funcs:   s.hostFuncs(r),
		// A preview is a public render with draft content, custom-code
		// blocks included: seeing the widget is most of why it exists.
		CodeSnippets: s.codeLookup(r),
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
	} else if !s.currentUser(r).Can(auth.PermissionForSlug(p.Slug)) {
		// Only the blog/ and news/ prefixes can trip this (any user
		// here holds the pages permission): a page may not be renamed
		// into a feed's namespace by someone who can't edit that feed.
		errs["slug"] = s.tr(r, "That address is reserved for blog and news posts.")
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

// sitePageFromURL is pageFromURL for the Pages section's own handlers:
// pages that back a blog or news post 404 here, because posts are
// managed under Blog & News. Without this, the pages routes would be a
// side door past the feed permissions — and past the post form's rule
// that feed and slug stay put.
func (s *server) sitePageFromURL(w http.ResponseWriter, r *http.Request) (*content.Page, bool) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return nil, false
	}
	_, err := s.deps.Content.PostByPageID(r.Context(), page.ID, s.deps.DefaultLocale, true)
	if err == nil {
		http.NotFound(w, r)
		return nil, false
	}
	if !errors.Is(err, content.ErrNotFound) {
		s.serverError(w, err)
		return nil, false
	}
	return page, true
}
