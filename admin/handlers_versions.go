package admin

// The history screens: a page's published editions, a preview of one
// through the real site templates, and the restore that puts one back.
//
// Pages and posts share every handler here. A post is a page underneath, so
// its history is its backing page's; the only differences are which loader
// enforces the permission (sitePageFromURL for pages, postFromURLCan for
// posts) and which URL the screen sits under, and both arrive as arguments.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/datefmt"
	"github.com/tsawler/cms/render"
)

// pageVersions lists a page's history.
// GET /pages/{id}/versions
func (s *server) pageVersions(w http.ResponseWriter, r *http.Request) {
	page, ok := s.sitePageFromURL(w, r)
	if !ok {
		return
	}
	s.renderVersions(w, r, page, s.pageBase(page))
}

// pageVersionPreview renders one edition of a page with the real site
// templates.
// GET /pages/{id}/versions/{vid}/preview
func (s *server) pageVersionPreview(w http.ResponseWriter, r *http.Request) {
	page, ok := s.sitePageFromURL(w, r)
	if !ok {
		return
	}
	s.renderVersionPreview(w, r, page, nil)
}

// pageVersionRestore puts one edition back into the page's draft.
// POST /pages/{id}/versions/{vid}/restore
func (s *server) pageVersionRestore(w http.ResponseWriter, r *http.Request) {
	page, ok := s.sitePageFromURL(w, r)
	if !ok {
		return
	}
	s.restoreVersion(w, r, page, s.pageBase(page))
}

// postVersions, postVersionPreview and postVersionRestore are the same
// three screens reached through Blog & News, where the URL carries the
// post's id and the permission is the feed's.
func (s *server) postVersions(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURLCan(w, r)
	if !ok {
		return
	}
	s.renderVersions(w, r, &post.Page, s.postBase(post))
}

func (s *server) postVersionPreview(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURLCan(w, r)
	if !ok {
		return
	}
	s.renderVersionPreview(w, r, &post.Page, post)
}

func (s *server) postVersionRestore(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURLCan(w, r)
	if !ok {
		return
	}
	s.restoreVersion(w, r, &post.Page, s.postBase(post))
}

// pageBase and postBase are the admin URL a history screen hangs off: the
// form it came from, and the prefix its preview and restore links extend.
// They differ because a post is addressed by its own id, not its backing
// page's.
func (s *server) pageBase(page *content.Page) string {
	return s.deps.AdminPath + "/pages/" + strconv.FormatInt(page.ID, 10)
}

func (s *server) postBase(post *content.Post) string {
	return s.deps.AdminPath + "/posts/" + strconv.FormatInt(post.PostID, 10)
}

// renderVersions draws the history list for a page, newest first.
func (s *server) renderVersions(w http.ResponseWriter, r *http.Request, page *content.Page, base string) {
	versions, err := s.deps.Content.Versions(r.Context(), page.ID)
	if err != nil {
		s.serverError(w, err)
		return
	}
	// Restoring overwrites the working copy, so a page holding edits
	// nobody has published yet says so before anyone clicks.
	var draftEdits bool
	if page.Status == content.StatusPublished {
		draftEdits, err = s.deps.Content.HasUnpublishedChanges(r.Context(), page.ID)
		if err != nil {
			s.serverError(w, err)
			return
		}
	}

	data := s.newTemplateData(r)
	data.FormPage = page
	data.Versions = versions
	data.HistoryBase = base
	data.HasDraftEdits = draftEdits
	s.render(w, http.StatusOK, "versions", data)
}

// versionFromURL loads the edition named by the {vid} URL parameter,
// scoped to page — a version id belonging to another page is not found
// here, not another page's content served under this one's address.
func (s *server) versionFromURL(w http.ResponseWriter, r *http.Request, page *content.Page) (*content.Version, *content.Snapshot, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "vid"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, nil, false
	}
	v, snap, err := s.deps.Content.VersionSnapshot(r.Context(), page.ID, id)
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return nil, nil, false
	}
	if err != nil {
		s.serverError(w, err)
		return nil, nil, false
	}
	return v, snap, true
}

// renderVersionPreview renders a stored edition through the real site
// templates, so an editor sees what restoring would bring back before
// bringing it back. post is non-nil when the page backs one, so the post
// template gets the byline and images it expects.
//
// Two things deliberately come from now rather than from the edition. The
// shared regions are the site's current chrome: they are the __site page's
// content, with a history of their own, and an edition of this page never
// held them. And a template the host application has since dropped falls
// back to the page's current one — an old edition is still worth looking
// at when the layout it was written for is gone.
func (s *server) renderVersionPreview(w http.ResponseWriter, r *http.Request, page *content.Page, post *content.Post) {
	_, snap, ok := s.versionFromURL(w, r, page)
	if !ok {
		return
	}
	locale := s.formLocale(r)

	shown := *page
	if s.deps.Renderer.Knows(snap.Page.TemplateName) {
		shown.TemplateName = snap.Page.TemplateName
	}
	shown.HeadCSS, shown.BodyJS = snap.Page.HeadCSS, snap.Page.BodyJS
	meta := snap.MetaFor(locale, s.deps.DefaultLocale)
	shown.Title, shown.Description = meta.Title, meta.Description
	shown.MetaDescription = meta.MetaDescription

	shared, err := s.deps.Content.SharedBlocks(r.Context(), locale, content.StatusPublished)
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

	in := render.Input{
		Page:         &shown,
		Blocks:       snap.BlocksFor(page.ID, locale, s.deps.DefaultLocale),
		Shared:       shared,
		Locale:       locale,
		Menus:        menus,
		Locales:      s.deps.Locales,
		Site:         site,
		Funcs:        s.hostFuncs(r),
		CodeSnippets: s.codeLookup(r),
	}
	if post != nil {
		shownPost := *post
		shownPost.Page = shown
		in.Post = render.PostInfoFor(&shownPost, render.LocalePrefix(locale, s.deps.DefaultLocale), s.postImages())
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.deps.Renderer.Render(w, in); err != nil {
		s.serverError(w, err)
	}
}

// restoreVersion puts a stored edition back into the page's draft, and
// publishes it too when the form asked for that.
//
// The plain restore leaves the site alone: the editor and the preview show
// the restored content, and it goes live on the next publish like any other
// edit. "Restore and publish" exists for the other case — something is
// wrong on the live site now — and does both in one click rather than
// making someone find the publish button while the site is broken.
func (s *server) restoreVersion(w http.ResponseWriter, r *http.Request, page *content.Page, base string) {
	v, _, ok := s.versionFromURL(w, r, page)
	if !ok {
		return
	}
	res, err := s.deps.Content.RestoreVersion(r.Context(), page.ID, v.ID)
	if err != nil {
		s.serverError(w, err)
		return
	}
	msg := s.tr(r, "Version restored into the draft. Publish when you're ready to make it live.")
	if r.PostFormValue("action") == "publish" {
		if err := s.publishWithShared(r.Context(), page.ID, s.actingUserID(r)); err != nil {
			s.serverError(w, err)
			return
		}
		msg = s.tr(r, "Version restored and published — it is live on the site again.")
	}
	s.contentChanged()
	s.flash(r, msg+s.codeNote(r, res))
	http.Redirect(w, r, base, http.StatusSeeOther)
}

// codeNote is the sentence a restore adds when the page's custom-code
// blocks were not simply still there — either put back because the
// library had lost them, or left alone because the library has moved on
// and rewriting a shared block is not this button's business. The keys
// ride outside the translated sentence: they are the library's own
// lowercase names, the same in any language.
func (s *server) codeNote(r *http.Request, res *content.RestoreResult) string {
	if res == nil {
		return ""
	}
	var out string
	if len(res.CodeRecreated) > 0 {
		out += " " + s.tr(r, "Custom-code blocks this version used had been deleted and were put back:") +
			" " + strings.Join(res.CodeRecreated, ", ") + "."
	}
	if len(res.CodeChanged) > 0 {
		out += " " + s.tr(r, "These custom-code blocks have changed since and were left as they are:") +
			" " + strings.Join(res.CodeChanged, ", ") + "."
	}
	return out
}

// versionJSON is one edition as the in-place editor's History dialog reads
// it. The date arrives preformatted: the editor has no translation table
// of its own, and the admin language is decided here anyway.
type versionJSON struct {
	ID    int64  `json:"id"`
	Label string `json:"label"` // "Jul 30, 2026, 2:14 PM"
	By    string `json:"by"`    // "" when unattributed or the account is gone
}

// apiPageVersions lists a page's editions for the editor's History dialog,
// newest first. The first entry is what the site is serving.
// GET /api/pages/{id}/versions
func (s *server) apiPageVersions(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURLCan(w, r)
	if !ok {
		return
	}
	versions, err := s.deps.Content.Versions(r.Context(), page.ID)
	if err != nil {
		s.deps.Logger.Error("cms admin: api listing versions", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Loading the history failed — try again."))
		return
	}
	lang := s.adminLang(r)
	out := make([]versionJSON, 0, len(versions))
	for _, v := range versions {
		out = append(out, versionJSON{
			ID:    v.ID,
			Label: datefmt.ShortTime(v.SavedAt, lang),
			By:    v.SavedByName,
		})
	}
	// hasUnpublished tells the dialog whether restoring is about to throw
	// away work, so the warning is the editor's to show rather than a
	// surprise after the fact.
	changed := false
	if page.Status == content.StatusPublished {
		changed, err = s.deps.Content.HasUnpublishedChanges(r.Context(), page.ID)
		if err != nil {
			s.deps.Logger.Error("cms admin: api checking draft edits", "page", page.ID, "err", err)
			jsonError(w, http.StatusInternalServerError, s.tr(r, "Loading the history failed — try again."))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out, "has_unpublished": changed})
}

// apiRestoreVersion puts one edition back into the page's draft. Like the
// admin screen's plain Restore it publishes nothing: the editor reloads
// onto the restored draft, which is what the editor shows anyway, and
// Publish or Discard draft decides its fate from there.
// POST /api/pages/{id}/versions/{vid}/restore
func (s *server) apiRestoreVersion(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURLCan(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "vid"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the edit — try again."))
		return
	}
	res, err := s.deps.Content.RestoreVersion(r.Context(), page.ID, id)
	if err != nil {
		if errors.Is(err, content.ErrNotFound) {
			jsonError(w, http.StatusNotFound, s.tr(r, "That version is no longer available."))
			return
		}
		s.deps.Logger.Error("cms admin: api restoring version", "page", page.ID, "version", id, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Restoring failed — try again."))
		return
	}
	s.contentChanged()
	// The editor reloads onto the restored draft, so anything it should
	// say about the code blocks has to be said before that — hence a note
	// in the response rather than a flash nobody would see.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"note": strings.TrimSpace(s.codeNote(r, res)),
	})
}
