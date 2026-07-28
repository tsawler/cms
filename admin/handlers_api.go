package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dberr"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
)

// The JSON API backs the in-place editor. It lives under /api on the admin
// handler, so requests carry the admin session cookie; the CSRF middleware
// checks the X-CSRF-Token header on writes.

// maxRegionsBody bounds one region-save request (5 MB of content).
const maxRegionsBody = 5 << 20

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// apiSaveRegions stores in-place edits as draft blocks.
// POST /api/pages/{id}/regions  body: {"regions": {"name": "content", ...}}
func (s *server) apiSaveRegions(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}

	var body struct {
		Locale  string            `json:"locale"`
		Regions map[string]string `json:"regions"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegionsBody))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the edit — try again."))
		return
	}
	if len(body.Regions) == 0 {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	isAdmin := s.currentUser(r).Role.IsAdmin()
	if err := s.saveRegions(r.Context(), page.ID, page.TemplateName, body.Regions, isAdmin,
		s.requestLocale(body.Locale)); err != nil {
		s.deps.Logger.Error("cms admin: api saving regions", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiCreatePage creates a draft page from the editor's "new page" dialog:
// a title and a template choice; the slug is derived from the title, with
// a numeric suffix when taken.
// POST /api/pages  body: {"title": "...", "template": "..."}
func (s *server) apiCreatePage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title    string `json:"title"`
		Template string `json:"template"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the request — try again."))
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Give the page a name."))
		return
	}
	if !s.deps.Renderer.Knows(body.Template) {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Choose a page type."))
		return
	}

	base := content.Slugify(title)
	if base == "" {
		base = "page"
	}
	// A slug that starts like a locale prefix would be unreachable.
	if s.localeSlugCollision(base) {
		base += "-page"
	}
	page := &content.Page{Title: title, TemplateName: body.Template, Slug: base}
	var err error
	for i := 1; i <= 50; i++ {
		if i > 1 {
			page.Slug = base + "-" + strconv.Itoa(i)
		}
		_, err = s.deps.Content.Insert(r.Context(), page, s.deps.DefaultLocale)
		if err == nil {
			break
		}
		if !errors.Is(err, content.ErrDuplicateSlug) {
			s.deps.Logger.Error("cms admin: api creating page", "err", err)
			jsonError(w, http.StatusInternalServerError, s.tr(r, "Creating the page failed — try again."))
			return
		}
	}
	if err != nil {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Too many pages already use that name."))
		return
	}

	s.seedStarterSections(r.Context(), page.ID, page.TemplateName)
	s.contentChanged()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"id":   page.ID,
		"slug": page.Slug,
		"url":  "/" + page.Slug,
	})
}

// apiDuplicatePage copies a page — template, per-page CSS/JS, every
// locale's metadata and draft blocks — as a new draft page named title.
// The slug is derived from the title, with a numeric suffix when taken;
// title becomes the copy's title in the given locale.
// POST /api/pages/{id}/duplicate  body: {"title": "...", "locale": "en"}
func (s *server) apiDuplicatePage(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	var body struct {
		Title  string `json:"title"`
		Locale string `json:"locale"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the request — try again."))
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Give the page a name."))
		return
	}
	// Posts are more than their backing page (feed, date, author…);
	// duplicating just the page would strand a half-post.
	if _, err := s.deps.Content.PostByPageID(r.Context(), page.ID, s.deps.DefaultLocale, true); err == nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Posts can't be duplicated."))
		return
	} else if !errors.Is(err, content.ErrNotFound) {
		s.deps.Logger.Error("cms admin: api duplicating page", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Duplicating the page failed — try again."))
		return
	}
	locale := s.requestLocale(body.Locale)

	base := content.Slugify(title)
	if base == "" {
		base = "page"
	}
	// A slug that starts like a locale prefix would be unreachable.
	if s.localeSlugCollision(base) {
		base += "-page"
	}
	slug := base
	var id int64
	var err error
	for i := 1; i <= 50; i++ {
		if i > 1 {
			slug = base + "-" + strconv.Itoa(i)
		}
		id, err = s.deps.Content.Duplicate(r.Context(), page.ID, slug, title, locale)
		if err == nil {
			break
		}
		if !errors.Is(err, content.ErrDuplicateSlug) {
			s.deps.Logger.Error("cms admin: api duplicating page", "page", page.ID, "err", err)
			jsonError(w, http.StatusInternalServerError, s.tr(r, "Duplicating the page failed — try again."))
			return
		}
	}
	if err != nil {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Too many pages already use that name."))
		return
	}
	s.contentChanged()

	url := "/" + slug
	if locale != s.deps.DefaultLocale {
		url = "/" + locale + url
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"id":   id,
		"slug": slug,
		"url":  url,
	})
}

// apiRevertLocale deletes a page's draft content and metadata for one
// non-default locale, so the page falls back to the default language.
// Like any edit, the block-side effect goes live on the next Publish.
// POST /api/pages/{id}/revert-locale  body: {"locale": "fr"}
func (s *server) apiRevertLocale(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	var body struct {
		Locale string `json:"locale"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the request — try again."))
		return
	}
	if body.Locale == s.deps.DefaultLocale || s.requestLocale(body.Locale) != body.Locale {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Choose a translation language to revert."))
		return
	}
	if err := s.deps.Content.DeleteLocaleContent(r.Context(), page.ID, body.Locale); err != nil {
		s.deps.Logger.Error("cms admin: api reverting locale", "page", page.ID, "locale", body.Locale, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Reverting failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiListPages returns every page for the editor's pickers (menu items,
// etc.): id, title, slug, and status.
// GET /api/pages
func (s *server) apiListPages(w http.ResponseWriter, r *http.Request) {
	pages, err := s.deps.Content.All(r.Context(), s.deps.DefaultLocale)
	if err != nil {
		s.deps.Logger.Error("cms admin: api listing pages", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not load the pages."))
		return
	}
	type pageJSON struct {
		ID     int64  `json:"id"`
		Title  string `json:"title"`
		Slug   string `json:"slug"`
		Status string `json:"status"`
	}
	out := make([]pageJSON, len(pages))
	for i, p := range pages {
		out[i] = pageJSON{ID: p.ID, Title: p.Title, Slug: p.Slug, Status: string(p.Status)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": out})
}

var menuKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

type menuItemJSON struct {
	ID    int64  `json:"id,omitempty"` // stable handle for the editor's menu UI
	Label string `json:"label"`        // default-locale label
	// Labels holds per-locale overrides, e.g. {"fr": "À propos"}. The
	// editor round-trips the whole map and edits only its active locale,
	// so a save made in one language never loses the others.
	Labels map[string]string `json:"labels,omitempty"`
	PageID int64             `json:"pageId"` // 0 = custom URL item or dropdown parent
	URL    string            `json:"url"`
	NewTab bool              `json:"newTab"`
	// Dropdown marks a label-only parent whose Children render as a
	// one-level submenu. Stored as a row with no page and no URL.
	Dropdown bool           `json:"dropdown,omitempty"`
	Children []menuItemJSON `json:"children,omitempty"`
}

// apiGetMenu returns a menu's items, as a one-level tree, for the
// editor's in-place menu UI.
// GET /api/menu?menu=main
func (s *server) apiGetMenu(w http.ResponseWriter, r *http.Request) {
	menu := r.URL.Query().Get("menu")
	if menu == "" {
		menu = "main"
	}
	if !menuKeyRe.MatchString(menu) {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Unknown menu."))
		return
	}
	items, err := s.deps.Content.MenuItems(r.Context(), menu)
	if err != nil {
		s.deps.Logger.Error("cms admin: api loading menu", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not load the menu."))
		return
	}
	out := []menuItemJSON{}
	byID := map[int64]int{} // item id -> index in out, for attaching children
	for _, item := range items {
		j := menuItemJSON{ID: item.ID, Label: item.Label, Labels: item.Labels, URL: item.URL, NewTab: item.NewTab}
		if item.PageID != nil {
			j.PageID = *item.PageID
		}
		if item.ParentID != nil {
			// Rows are ordered by sort and children are stored after
			// their parent, so the parent is always already in out.
			if pi, ok := byID[*item.ParentID]; ok {
				out[pi].Children = append(out[pi].Children, j)
			}
			continue
		}
		j.Dropdown = j.PageID == 0 && j.URL == ""
		byID[item.ID] = len(out)
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"menu": menu, "items": out})
}

// validMenuURL accepts site-relative paths and the handful of absolute
// schemes a menu legitimately links to.
func validMenuURL(u string) bool {
	return strings.HasPrefix(u, "/") ||
		strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") ||
		strings.HasPrefix(u, "mailto:") || strings.HasPrefix(u, "tel:")
}

// menuItemInput validates one submitted menu item and converts it for
// the store. Dropdown parents are label-only; everything else needs a
// page or a valid URL. Returns a user-facing error message, or "".
func (s *server) menuItemInput(item menuItemJSON) (content.MenuItemInput, string) {
	label := strings.TrimSpace(item.Label)
	if label == "" {
		return content.MenuItemInput{}, "Every menu item needs a label."
	}
	// Keep only configured non-default locales' overrides; empty values
	// mean "no override" and are dropped.
	labels := map[string]string{}
	for _, code := range s.deps.Locales[1:] {
		if l := strings.TrimSpace(item.Labels[code]); l != "" {
			labels[code] = l
		}
	}
	in := content.MenuItemInput{Label: label, Labels: labels, NewTab: item.NewTab}
	if item.Dropdown {
		return in, ""
	}
	if item.PageID > 0 {
		id := item.PageID
		in.PageID = &id
		return in, ""
	}
	url := strings.TrimSpace(item.URL)
	if !validMenuURL(url) {
		return content.MenuItemInput{},
			"Custom links need a web address like https://… or a path like /contact."
	}
	in.URL = url
	return in, ""
}

// apiSaveMenu replaces a menu's items with the submitted ordered tree
// (one level of nesting under dropdown parents). Menus have no draft
// state: the change is live immediately.
// PUT /api/menu  body: {"menu": "main", "items": [{label, pageId, url,
// newTab, dropdown, children: […]}]}
func (s *server) apiSaveMenu(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Menu  string         `json:"menu"`
		Items []menuItemJSON `json:"items"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the menu — try again."))
		return
	}
	if !menuKeyRe.MatchString(body.Menu) {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Unknown menu."))
		return
	}
	total := 0
	for _, item := range body.Items {
		total += 1 + len(item.Children)
	}
	if total > 100 {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Too many menu items."))
		return
	}

	inputs := make([]content.MenuItemInput, len(body.Items))
	for i, item := range body.Items {
		in, msg := s.menuItemInput(item)
		if msg != "" {
			jsonError(w, http.StatusUnprocessableEntity, s.tr(r, msg))
			return
		}
		if len(item.Children) > 0 && !item.Dropdown {
			jsonError(w, http.StatusBadRequest, s.tr(r, "Only dropdown items can hold other items."))
			return
		}
		for _, child := range item.Children {
			if child.Dropdown || len(child.Children) > 0 {
				jsonError(w, http.StatusBadRequest, s.tr(r, "Dropdown menus can only go one level deep."))
				return
			}
			cin, msg := s.menuItemInput(child)
			if msg != "" {
				jsonError(w, http.StatusUnprocessableEntity, s.tr(r, msg))
				return
			}
			in.Children = append(in.Children, cin)
		}
		inputs[i] = in
	}

	if err := s.deps.Content.ReplaceMenu(r.Context(), body.Menu, inputs); err != nil {
		if dberr.IsForeignKeyViolation(err) {
			jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "One of the linked pages no longer exists — reload and try again."))
			return
		}
		s.deps.Logger.Error("cms admin: api saving menu", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving the menu failed — try again."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiGetSettings returns the site-wide settings for the editor's
// site-settings dialog.
// GET /api/settings
func (s *server) apiGetSettings(w http.ResponseWriter, r *http.Request) {
	site, err := s.deps.Content.SiteSettings(r.Context())
	if err != nil {
		s.deps.Logger.Error("cms admin: api loading site settings", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not load the site settings."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"menuAlign":  site.MenuAlign,
		"siteName":   site.SiteName,
		"logoUrl":    site.LogoURL,
		"loginInNav": site.LoginInNav,
		"siteCss":    site.SiteCSS,
		"siteJs":     site.SiteJS,
	})
}

// maxSiteCodeLen caps site-wide CSS and JS each, so a paste can't bloat
// every rendered page unboundedly.
const maxSiteCodeLen = 100_000

// apiSaveSettings stores the site-wide settings. Like menus they have no
// draft state: the change is live immediately. Site-wide CSS/JS is
// written raw into every page, so only admins may change it — a non-admin
// editor's request keeps whatever is already stored.
// PUT /api/settings  body: {"menuAlign", "siteName", "logoUrl",
// "loginInNav", "siteCss", "siteJs"}
func (s *server) apiSaveSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MenuAlign  string `json:"menuAlign"`
		SiteName   string `json:"siteName"`
		LogoURL    string `json:"logoUrl"`
		LoginInNav bool   `json:"loginInNav"`
		SiteCSS    string `json:"siteCss"`
		SiteJS     string `json:"siteJs"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegionsBody))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the request — try again."))
		return
	}
	switch body.MenuAlign {
	case "", "left", "center", "right":
	default:
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Unknown menu alignment."))
		return
	}
	name := strings.TrimSpace(body.SiteName)
	if len(name) > 200 {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "The site name is too long."))
		return
	}
	logo := strings.TrimSpace(body.LogoURL)
	if !validImageURL(logo) {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "The logo needs to be an uploaded image or a web address."))
		return
	}
	// Site-wide CSS/JS is injected raw, so it stays admin-only just like
	// per-page code. Non-admins can still change the other settings; their
	// request carries the stored code through unchanged.
	css, js := body.SiteCSS, body.SiteJS
	if u := s.currentUser(r); u == nil || !u.Role.IsAdmin() {
		current, err := s.deps.Content.SiteSettings(r.Context())
		if err != nil {
			s.deps.Logger.Error("cms admin: api loading site settings", "err", err)
			jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving the site settings failed — try again."))
			return
		}
		css, js = current.SiteCSS, current.SiteJS
	} else if len(css) > maxSiteCodeLen || len(js) > maxSiteCodeLen {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "The site-wide code is too long."))
		return
	}
	if err := s.deps.Content.SaveSiteSettings(r.Context(), content.SiteSettings{
		MenuAlign:  body.MenuAlign,
		SiteName:   name,
		LogoURL:    logo,
		LoginInNav: body.LoginInNav,
		SiteCSS:    css,
		SiteJS:     js,
	}); err != nil {
		s.deps.Logger.Error("cms admin: api saving site settings", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving the site settings failed — try again."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiDeletePage removes a page and all of its content. The home page
// (empty slug) is not deletable — a site must always answer at /.
// DELETE /api/pages/{id}
func (s *server) apiDeletePage(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	if page.Slug == "" {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "The home page can't be deleted."))
		return
	}
	if err := s.deps.Content.Delete(r.Context(), page.ID); err != nil {
		s.deps.Logger.Error("cms admin: api deleting page", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Deleting the page failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": "/"})
}

// apiSaveSections replaces a sections region's draft blocks with the
// submitted ordered list.
// POST /api/pages/{id}/sections
// body: {"region": "...", "sections": [{"bg": "...", "width": "...", "html": "..."}]}
func (s *server) apiSaveSections(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}

	var body struct {
		Locale   string `json:"locale"`
		Region   string `json:"region"`
		Sections []struct {
			BG      string `json:"bg"`
			Width   string `json:"width"`
			Corners string `json:"corners"`
			Height  string `json:"height"`
			VAlign  string `json:"valign"`
			BGColor string `json:"bgcolor"`
			BGImage string `json:"bgimage"`
			HTML    string `json:"html"`
		} `json:"sections"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegionsBody))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the edit — try again."))
		return
	}
	if len(body.Sections) > 100 {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Too many sections on one page."))
		return
	}

	// The target region must be a sections region the page's template
	// actually declares.
	valid := false
	for _, region := range s.deps.Renderer.Regions(page.TemplateName) {
		if region.Name == body.Region && region.Kind == "sections" {
			valid = true
			break
		}
	}
	if !valid {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Unknown sections area."))
		return
	}

	isAdmin := s.currentUser(r).Role.IsAdmin()
	inputs := make([]content.SectionInput, len(body.Sections))
	for i, sec := range body.Sections {
		html := sec.HTML
		if !isAdmin {
			html = editorHTMLPolicy.Sanitize(html)
		}
		// Resolve settings against the configured options so unknown
		// keys are stored as the fallback rather than junk.
		settings := map[string]string{
			"bg":    s.deps.SectionStyles.Background(sec.BG).Key,
			"width": s.deps.SectionStyles.Width(sec.Width).Key,
		}
		// Corner rounding stores only a non-default choice, so hosts
		// without corner options never see the key.
		if list := s.deps.SectionStyles.Corners; len(list) > 0 {
			if c := s.deps.SectionStyles.Corner(sec.Corners); c.Key != list[0].Key {
				settings["corners"] = c.Key
			}
		}
		// Custom backgrounds and height are free-form values; invalid
		// ones are dropped rather than stored.
		if h := render.ValidSectionHeight(sec.Height); h != "" {
			settings["height"] = h
		}
		if v := render.ValidSectionVAlign(sec.VAlign); v != "" {
			settings["valign"] = v
		}
		if c := render.ValidBackgroundColor(sec.BGColor); c != "" {
			settings["bgcolor"] = c
		}
		if u := render.ValidBackgroundURL(sec.BGImage); u != "" {
			settings["bgimage"] = u
		}
		inputs[i] = content.SectionInput{Content: html, Settings: settings}
	}

	if err := s.deps.Content.ReplaceDraftSections(r.Context(), page.ID, body.Region, s.requestLocale(body.Locale), inputs); err != nil {
		s.deps.Logger.Error("cms admin: api saving sections", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiGetPageCode returns the page's custom head CSS and body JS.
// GET /api/pages/{id}/code  (admin only; routed behind requireAdmin)
func (s *server) apiGetPageCode(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"css": page.HeadCSS, "js": page.BodyJS,
	})
}

// apiSavePageCode replaces the page's custom head CSS and body JS. Both
// are written raw into rendered pages — plain code or full <style>,
// <link>, and <script> tags — which is why the route is admin-only.
// PUT /api/pages/{id}/code  body: {"css": "...", "js": "..."}
func (s *server) apiSavePageCode(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	var body struct {
		CSS string `json:"css"`
		JS  string `json:"js"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegionsBody))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the edit — try again."))
		return
	}
	page.HeadCSS = body.CSS
	page.BodyJS = body.JS
	if err := s.deps.Content.Update(r.Context(), page, s.deps.DefaultLocale); err != nil {
		s.deps.Logger.Error("cms admin: api saving page code", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving failed — try again."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiPublish makes the page's draft content live.
// POST /api/pages/{id}/publish
func (s *server) apiPublish(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	if err := s.deps.Content.Publish(r.Context(), page.ID); err != nil {
		s.deps.Logger.Error("cms admin: api publishing", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Publishing failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "published"})
}

// apiUnpublish takes the page off the public site. Content is untouched —
// draft and published both survive — so publishing again restores it.
// POST /api/pages/{id}/unpublish
func (s *server) apiUnpublish(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	if err := s.deps.Content.Unpublish(r.Context(), page.ID); err != nil {
		s.deps.Logger.Error("cms admin: api unpublishing", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Unpublishing failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "draft"})
}

// apiSetVisibility changes who may view the page on the public site.
// PUT /api/pages/{id}/visibility  body: {"visibility": "public" | "private"}
func (s *server) apiSetVisibility(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	var body struct {
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || !content.ValidVisibility(body.Visibility) {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the edit — try again."))
		return
	}
	if err := s.deps.Content.SetVisibility(r.Context(), page.ID, content.Visibility(body.Visibility)); err != nil {
		s.deps.Logger.Error("cms admin: api setting visibility", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "visibility": body.Visibility})
}

// apiDiscard throws away the page's unpublished draft edits, reverting its
// draft content to match what is currently published.
// POST /api/pages/{id}/discard
func (s *server) apiDiscard(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	if page.Status != content.StatusPublished {
		jsonError(w, http.StatusConflict, s.tr(r, "There are no published changes to revert to — this page hasn't been published yet."))
		return
	}
	if err := s.deps.Content.DiscardDraft(r.Context(), page.ID); err != nil {
		s.deps.Logger.Error("cms admin: api discarding draft", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Discarding failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type mediaJSON struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	Alt      string `json:"alt"`
	Size     string `json:"size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Thumb    string `json:"thumb"`
	Web      string `json:"web"`
	Original string `json:"original"`
	Poster   string `json:"poster,omitempty"` // videos: full-size poster frame, if any
}

func toMediaJSON(v media.View) mediaJSON {
	return mediaJSON{
		ID:       v.ID,
		Kind:     string(v.Kind),
		Filename: v.Filename,
		Alt:      v.Alt,
		Size:     v.SizeHuman(),
		Width:    v.Width,
		Height:   v.Height,
		Thumb:    v.ThumbURL,
		Web:      v.WebURL,
		Original: v.OriginalURL,
		Poster:   v.PosterURL,
	}
}

// apiMediaList returns the media library for the editor's pickers.
// GET /api/media?kind=image|file|video&q=term&folder=<id|root>
func (s *server) apiMediaList(w http.ResponseWriter, r *http.Request) {
	kind := media.Kind(r.URL.Query().Get("kind"))
	if kind != "" && kind != media.KindImage && kind != media.KindFile && kind != media.KindVideo {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Unknown media kind."))
		return
	}
	folderID, unfiled := parseFolderParam(r.URL.Query().Get("folder"))
	items, err := s.deps.Media.All(r.Context(), s.deps.DefaultLocale, media.ListOptions{
		Kind:     kind,
		Query:    strings.TrimSpace(r.URL.Query().Get("q")),
		FolderID: folderID,
		Unfiled:  unfiled,
	})
	if err != nil {
		s.deps.Logger.Error("cms admin: api listing media", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not load the media library."))
		return
	}
	views := s.deps.Media.Views(items)
	out := make([]mediaJSON, len(views))
	for i, v := range views {
		out[i] = toMediaJSON(v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"media": out})
}

// apiMediaUpload accepts a multipart media upload and returns its record.
// POST /api/media  (multipart field "file"; optional "poster", a
// client-captured still for video uploads)
func (s *server) apiMediaUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.uploadLimit())
	file, header, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			jsonError(w, http.StatusRequestEntityTooLarge, s.uploadTooLargeMsg(r))
			return
		}
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Choose a file to upload."))
		return
	}
	defer file.Close()

	// A poster over the cap is silently dropped, like an undecodable one:
	// it is decorative, never worth failing the upload over.
	var poster []byte
	if pf, _, err := r.FormFile("poster"); err == nil {
		poster, err = io.ReadAll(io.LimitReader(pf, 8<<20))
		pf.Close()
		if err != nil {
			poster = nil
		}
	}

	folderID, _ := parseFolderParam(r.PostFormValue("folder"))
	md, err := s.deps.Media.UploadFrom(r.Context(), sanitizeFilename(header.Filename), file, header.Size, poster, s.currentUser(r).ID, folderID)
	if err != nil {
		switch {
		case errors.Is(err, media.ErrTooLarge):
			jsonError(w, http.StatusRequestEntityTooLarge, s.uploadTooLargeMsg(r))
		case errors.Is(err, media.ErrUnsafeSVG):
			jsonError(w, http.StatusUnprocessableEntity, s.tr(r, unsafeSVGMsg))
		case errors.Is(err, media.ErrUnsupportedType), strings.Contains(err.Error(), "parsing svg"):
			jsonError(w, http.StatusUnprocessableEntity, s.tr(r, unsupportedTypeMsg))
		default:
			s.deps.Logger.Error("cms admin: api media upload", "err", err)
			jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "That file could not be processed."))
		}
		return
	}

	view := s.deps.Media.Views([]media.Media{*md})[0]
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "media": toMediaJSON(view)})
}

// apiFoldersList returns media folders with item counts, filtered to one
// kind when the picker asks for it.
// GET /api/media/folders?kind=image|file|video
func (s *server) apiFoldersList(w http.ResponseWriter, r *http.Request) {
	folders, err := s.deps.Media.Folders(r.Context())
	if err != nil {
		s.deps.Logger.Error("cms admin: api listing folders", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not load folders."))
		return
	}
	kind := media.Kind(r.URL.Query().Get("kind"))
	type folderJSON struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	out := []folderJSON{}
	for _, f := range folders {
		if kind != "" && f.Kind != kind {
			continue
		}
		out = append(out, folderJSON{ID: f.ID, Name: f.Name, Count: f.Count})
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": out})
}

// apiFolderCreate makes a folder from the editor's picker, in the kind
// the picker is browsing.
// POST /api/media/folders  body: {"name": "...", "kind": "image"}
func (s *server) apiFolderCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the folder name."))
		return
	}
	f, err := s.deps.Media.CreateFolder(r.Context(), body.Name, media.Kind(body.Kind))
	if errors.Is(err, media.ErrBadFolderKind) {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Unknown media kind."))
		return
	}
	if errors.Is(err, media.ErrDuplicateFolder) || errors.Is(err, media.ErrBadFolderName) {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, friendlyFolderError(err)))
		return
	}
	if err != nil {
		s.deps.Logger.Error("cms admin: api creating folder", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not create the folder."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true,
		"folder": map[string]any{"id": f.ID, "name": f.Name, "count": 0}})
}
