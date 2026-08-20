package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/tsawler/cms/auth"
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

// pageFromURLCan is pageFromURL plus authorization: 403 unless the user
// holds the permission the page's slug falls under (blog/… and news/…
// belong to their feeds, everything else to pages). The editor's
// page-mutating API routes serve posts' backing pages too, so they are
// gated here per page rather than on the route.
func (s *server) pageFromURLCan(w http.ResponseWriter, r *http.Request) (*content.Page, bool) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return nil, false
	}
	if !s.currentUser(r).Can(auth.PermissionForSlug(page.Slug)) {
		jsonError(w, http.StatusForbidden, s.tr(r, "You don't have permission to edit this page."))
		return nil, false
	}
	return page, true
}

// apiSaveRegions stores in-place edits as draft blocks. Region names
// carrying the shared prefix ("site:footer") are the site's, not the
// page's, and are stored against the site page instead — the editor sends
// whatever was edited on screen, and that can be a mixture of the two.
// POST /api/pages/{id}/regions  body: {"regions": {"name": "content", ...}}
func (s *server) apiSaveRegions(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURLCan(w, r)
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

	user := s.currentUser(r)
	isAdmin := user.Role.IsAdmin()
	locale := s.requestLocale(body.Locale)
	pageValues, sharedValues := splitSharedRegions(body.Regions)
	// Shared regions (the site footer and friends) appear on every page,
	// so they ride along in any page's save — but they are site
	// furniture, and editing them takes the pages permission even when
	// the page being saved is a post.
	if len(sharedValues) > 0 && !user.Can(auth.PermPages) {
		jsonError(w, http.StatusForbidden, s.tr(r, "Editing shared site areas needs the pages permission."))
		return
	}
	if err := s.saveRegions(r.Context(), page.ID, page.TemplateName, pageValues, isAdmin,
		locale); err != nil {
		s.deps.Logger.Error("cms admin: api saving regions", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving failed — try again."))
		return
	}
	if err := s.saveSharedRegions(r.Context(), sharedValues, isAdmin, locale); err != nil {
		s.deps.Logger.Error("cms admin: api saving shared regions", "err", err)
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
// templateByFile finds the page template registered under file.
func templateByFile(templates []render.PageTemplate, file string) (render.PageTemplate, bool) {
	for _, t := range templates {
		if t.File == file {
			return t, true
		}
	}
	return render.PageTemplate{}, false
}

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
	// The template must be one offered to this user: a page template (not
	// the hidden post template), and not an Unlisted one unless they are a
	// superadmin. Unlisted templates back one-off pages, so for everyone
	// else they are simply not a choice — same answer as an unknown name.
	tpl, known := templateByFile(s.deps.Renderer.PageTemplates(), body.Template)
	if !known || (tpl.Unlisted && !s.currentUser(r).Role.IsSuperadmin()) {
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
	page, ok := s.pageFromURLCan(w, r)
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
	page, ok := s.pageFromURLCan(w, r)
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

// validMenuURL accepts site-relative paths, same-page anchors, and the
// handful of absolute schemes a menu legitimately links to.
//
// A leading "#" is a fragment reference and can never become a scheme, so
// "#pricing" is safe to allow however it is written — but a bare "#" is
// a link to nowhere, and rejecting it catches the half-filled field
// rather than storing a nav item that quietly does nothing. Anchors are
// worth supporting because a one-page site's nav is made of them; on a
// site with more than one page "/#pricing" is usually meant instead,
// since a bare "#pricing" only resolves on the page that has the anchor.
func validMenuURL(u string) bool {
	return strings.HasPrefix(u, "/") ||
		strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") ||
		strings.HasPrefix(u, "mailto:") || strings.HasPrefix(u, "tel:") ||
		(strings.HasPrefix(u, "#") && len(u) > 1)
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
			"Custom links need a web address like https://…, a path like /contact, or an anchor like #pricing."
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
	// "" is stored as production; the dialog's select wants the name.
	mode := site.Mode
	if mode == "" {
		mode = content.ModeProduction
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"menuAlign":  site.MenuAlign,
		"siteName":   site.SiteName,
		"logoUrl":    site.LogoURL,
		"faviconUrl": site.FaviconURL,
		"loginInNav": site.LoginInNav,
		"siteCss":    site.SiteCSS,
		"siteJs":     site.SiteJS,
		// Everyone who can open the dialog is told the mode — the switch
		// itself is superadmin-only, but a save has to carry the stored
		// value back, and an editor seeing "Development" explains why the
		// site is not turning up in search.
		"mode": mode,
		// Likewise the robots.txt and the sitemap switch: editing them is
		// superadmin-only, but every save carries them through, and both
		// are visible to the whole internet in any case.
		"robotsTxt": site.RobotsTxt,
		"sitemap":   site.Sitemap,
		// The notice bar's switch and look. Its words are not settings —
		// they are the shared region render.NoticeRegion, edited in the
		// bar itself like any other shared content.
		"noticeBar":         site.NoticeBar,
		"noticeStyle":       render.ValidNoticeStyle(site.NoticeStyle),
		"noticeDismissible": site.NoticeDismissible,
		"noticeStyles":      render.NoticeStyles,
	})
}

// maxSiteCodeLen caps site-wide CSS and JS each, so a paste can't bloat
// every rendered page unboundedly.
const maxSiteCodeLen = 100_000

// maxRobotsLen caps the stored robots.txt. Real ones are a few lines;
// crawlers stop reading long before this (Google gives up past 500 KiB),
// so anything approaching it is a paste into the wrong field.
const maxRobotsLen = 10_000

// apiSaveSettings stores the site-wide settings. Like menus they have no
// draft state: the change is live immediately. Site-wide CSS/JS is
// written raw into every page, so only admins may change it — a non-admin
// editor's request keeps whatever is already stored.
// Whether the site is findable at all is a superadmin's call, so the
// mode, the site's robots.txt, and the sitemap switch are held to the
// same carry-through rule as the code fields.
// PUT /api/settings  body: {"menuAlign", "siteName", "logoUrl",
// "faviconUrl", "loginInNav", "siteCss", "siteJs", "mode", "robotsTxt",
// "sitemap", "noticeBar", "noticeStyle", "noticeDismissible"}
func (s *server) apiSaveSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MenuAlign  string `json:"menuAlign"`
		SiteName   string `json:"siteName"`
		LogoURL    string `json:"logoUrl"`
		FaviconURL string `json:"faviconUrl"`
		LoginInNav bool   `json:"loginInNav"`
		SiteCSS    string `json:"siteCss"`
		SiteJS     string `json:"siteJs"`
		// Pointers: absent and empty mean different things here — see
		// the carry-through comment below.
		Mode      *string `json:"mode"`
		RobotsTxt *string `json:"robotsTxt"`
		Sitemap   *bool   `json:"sitemap"`
		// Pointers for the same reason, and a sharper one: the Site CSS
		// & JS panel PUTs the settings it knows about, and a plain bool
		// missing from that body would read as false and switch off a
		// notice bar the site is relying on.
		NoticeBar         *bool   `json:"noticeBar"`
		NoticeStyle       *string `json:"noticeStyle"`
		NoticeDismissible *bool   `json:"noticeDismissible"`
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
	favicon := strings.TrimSpace(body.FaviconURL)
	if !validImageURL(favicon) {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "The favicon needs to be an uploaded image or a web address."))
		return
	}
	// Four fields here are not everyone's to change: site-wide CSS/JS is
	// injected raw, so it stays admin-only just like per-page code, and
	// the mode, the robots.txt, and the sitemap switch all decide how the
	// site meets search engines, which is a superadmin's call. Anyone may
	// still change the rest — their request carries the stored values
	// through unchanged rather than being refused, since the dialog sends
	// the whole object every time. So the stored settings are what every
	// field this request may not set falls back to, and are read on each
	// save.
	//
	// The superadmin fields carry through when they are merely absent
	// from the body, too, which is why they are pointers: the Site CSS &
	// JS panel PUTs the settings without them, and a missing mode read as
	// "" would mean production — silently pulling a development site into
	// search on an unrelated save.
	current, err := s.deps.Content.SiteSettings(r.Context())
	if err != nil {
		s.deps.Logger.Error("cms admin: api loading site settings", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving the site settings failed — try again."))
		return
	}
	u := s.currentUser(r)
	isAdmin := u != nil && u.Role.IsAdmin()
	isSuper := u != nil && u.Role.IsSuperadmin() // a superset of admin
	css, js := current.SiteCSS, current.SiteJS
	if isAdmin {
		css, js = body.SiteCSS, body.SiteJS
	}
	mode, robots, sitemap := current.Mode, current.RobotsTxt, current.Sitemap
	if isSuper {
		if body.Mode != nil {
			mode = *body.Mode
		}
		if body.RobotsTxt != nil {
			// CRLF in, LF out: the field is a browser textarea, which
			// submits CRLF, and the value is served byte-for-byte.
			robots = strings.ReplaceAll(*body.RobotsTxt, "\r\n", "\n")
		}
		if body.Sitemap != nil {
			sitemap = *body.Sitemap
		}
	}
	// The notice bar is site furniture like the menu alignment, so
	// anyone who can open the dialog may switch it on — the words it
	// carries go through the same save, sanitize, and publish path as
	// any other shared content.
	noticeBar, noticeStyle, noticeDismiss := current.NoticeBar, current.NoticeStyle, current.NoticeDismissible
	if body.NoticeBar != nil {
		noticeBar = *body.NoticeBar
	}
	if body.NoticeStyle != nil {
		if *body.NoticeStyle != render.ValidNoticeStyle(*body.NoticeStyle) {
			jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Unknown notice bar style."))
			return
		}
		noticeStyle = *body.NoticeStyle
	}
	if body.NoticeDismissible != nil {
		noticeDismiss = *body.NoticeDismissible
	}
	if isAdmin && (len(css) > maxSiteCodeLen || len(js) > maxSiteCodeLen) {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "The site-wide code is too long."))
		return
	}
	if len(robots) > maxRobotsLen {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "The robots.txt is too long."))
		return
	}
	if !content.ValidMode(mode) {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Unknown site mode."))
		return
	}
	if err := s.deps.Content.SaveSiteSettings(r.Context(), content.SiteSettings{
		MenuAlign:  body.MenuAlign,
		SiteName:   name,
		LogoURL:    logo,
		FaviconURL: favicon,
		LoginInNav: body.LoginInNav,
		SiteCSS:    css,
		SiteJS:     js,
		Mode:       mode,
		RobotsTxt:  robots,
		Sitemap:    sitemap,

		NoticeBar:         noticeBar,
		NoticeStyle:       noticeStyle,
		NoticeDismissible: noticeDismiss,
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
	page, ok := s.pageFromURLCan(w, r)
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
	page, ok := s.pageFromURLCan(w, r)
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
			Padding string `json:"padding"`
			Height  string `json:"height"`
			VAlign  string `json:"valign"`
			BGColor string `json:"bgcolor"`
			BGImage string `json:"bgimage"`
			BGPos   string `json:"bgposition"`
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
		// Vertical spacing, on the same terms: only a non-default choice
		// is stored, so content saved before the axis existed keeps
		// resolving to the first option and renders unchanged.
		if list := s.deps.SectionStyles.Paddings; len(list) > 0 {
			if p := s.deps.SectionStyles.Padding(sec.Padding); p.Key != list[0].Key {
				settings["padding"] = p.Key
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
		// Which part of a background image survives the crop only means
		// anything when there is one, and centered is the default, so
		// neither case is stored.
		if p := render.ValidBackgroundPosition(sec.BGPos); p != "" && settings["bgimage"] != "" {
			settings["bgposition"] = p
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

// maxMetaBody bounds a page-metadata save. A title and a description are
// a line of text each; the room above that is slack, not an invitation.
const maxMetaBody = 8192

// apiGetPageMeta returns the page's title and meta description for a
// locale, as stored rather than as resolved: a translation that has none
// of its own reads back empty, with the default locale's words alongside
// under "inherited" for the dialog to show as a placeholder.
// GET /api/pages/{id}/meta?locale=fr
func (s *server) apiGetPageMeta(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	locale := s.formLocale(r)
	meta, err := s.deps.Content.MetaFor(r.Context(), page.ID, locale)
	if err != nil {
		s.deps.Logger.Error("cms admin: api reading page meta", "page", page.ID, "locale", locale, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not load the page settings."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"locale":                   locale,
		"title":                    meta.Title,
		"description":              meta.Description,
		"metaDescription":          meta.MetaDescription,
		"inheritedTitle":           meta.InheritedTitle,
		"inheritedDescription":     meta.InheritedDescription,
		"inheritedMetaDescription": meta.InheritedMetaDescription,
	})
}

// apiSavePageMeta saves the page's title and meta description for one
// locale. Like the admin form's locale tabs it writes the working copy,
// so the change reaches the site on the next Publish.
//
// On a translation an empty field is meaningful: it clears this locale's
// own value and lets the page fall back to the default language again.
// The default locale has nothing to fall back to, so there a title is
// required.
//
// A field left out of the body entirely is left alone, which is not the
// same as sending it empty: the in-place title editor sends a title and
// nothing else, and must not take the page's description with it. The
// dialogs send every field they show and so overwrite them all.
// PUT /api/pages/{id}/meta  body: {"locale", "title", "description",
// "metaDescription"}
func (s *server) apiSavePageMeta(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURLCan(w, r)
	if !ok {
		return
	}
	var body struct {
		Locale          string  `json:"locale"`
		Title           *string `json:"title"`
		Description     *string `json:"description"`
		MetaDescription *string `json:"metaDescription"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMetaBody))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the edit — try again."))
		return
	}
	if s.requestLocale(body.Locale) != body.Locale {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Unknown language."))
		return
	}
	var meta content.PageMeta
	if body.Title == nil || body.Description == nil || body.MetaDescription == nil {
		// Only what the body omits is read back; a full save needs no
		// round trip.
		stored, err := s.deps.Content.MetaFor(r.Context(), page.ID, body.Locale)
		if err != nil {
			s.deps.Logger.Error("cms admin: api reading page meta to save", "page", page.ID, "locale", body.Locale, "err", err)
			jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving failed — try again."))
			return
		}
		meta = content.PageMeta{Title: stored.Title, Description: stored.Description,
			MetaDescription: stored.MetaDescription}
	}
	if body.Title != nil {
		meta.Title = strings.TrimSpace(*body.Title)
	}
	if body.Description != nil {
		meta.Description = strings.TrimSpace(*body.Description)
	}
	if body.MetaDescription != nil {
		meta.MetaDescription = strings.TrimSpace(*body.MetaDescription)
	}
	if meta.Title == "" && body.Locale == s.deps.DefaultLocale {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Title is required."))
		return
	}
	if err := s.deps.Content.UpdateMeta(r.Context(), page.ID, body.Locale, meta); err != nil {
		s.deps.Logger.Error("cms admin: api saving page meta", "page", page.ID, "locale", body.Locale, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving failed — try again."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiPublish makes the page's draft content live, and the site's shared
// content with it (see publishWithShared).
// POST /api/pages/{id}/publish
func (s *server) apiPublish(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURLCan(w, r)
	if !ok {
		return
	}
	if err := s.publishWithShared(r.Context(), page.ID); err != nil {
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
	page, ok := s.pageFromURLCan(w, r)
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
	page, ok := s.pageFromURLCan(w, r)
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
	page, ok := s.pageFromURLCan(w, r)
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
	// Card is the middle rung of the image ladder, for a slot that knows
	// it is smaller than a full-width one — a listing card, a tile in a
	// grid — and says so with data-cms-rendition. Images only, so it is
	// omitted rather than sent empty for files and videos.
	Card     string `json:"card,omitempty"`
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
		Card:     v.CardURL,
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

	filed, err := s.fileInto(r.Context(), md, folderID)
	if err != nil {
		s.deps.Logger.Error("cms admin: api media upload filing", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "That file could not be processed."))
		return
	}

	view := s.deps.Media.Views([]media.Media{*md})[0]
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "filed": filed, "media": toMediaJSON(view)})
}

// apiMediaSetPoster attaches a browser-captured poster frame to a video.
// The media page uses it to backfill a video uploaded without one: the
// server can't decode video, so the inspector captures a frame while the
// video is being previewed and posts it here.
// POST /api/media/{id}/poster  (multipart field "poster")
func (s *server) apiMediaSetPoster(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaIDFromURL(w, r)
	if !ok {
		return
	}
	// Same cap a poster riding along with an upload gets.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	pf, _, err := r.FormFile("poster")
	if err != nil {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "That file could not be processed."))
		return
	}
	defer pf.Close()
	poster, err := io.ReadAll(pf)
	if err != nil {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "That file could not be processed."))
		return
	}

	md, err := s.deps.Media.SetPoster(r.Context(), id, s.deps.DefaultLocale, poster)
	switch {
	case errors.Is(err, media.ErrNotFound):
		jsonError(w, http.StatusNotFound, s.tr(r, "That file could not be processed."))
		return
	case err != nil:
		s.deps.Logger.Error("cms admin: api setting video poster", "id", id, "err", err)
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "That file could not be processed."))
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
