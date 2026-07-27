package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
)

// datetimeLocalFormat is what <input type="datetime-local"> submits.
const datetimeLocalFormat = "2006-01-02T15:04"

func (s *server) postsList(w http.ResponseWriter, r *http.Request) {
	feed := r.URL.Query().Get("feed")
	if !content.ValidFeed(feed) {
		feed = ""
	}
	posts, err := s.deps.Content.Posts(r.Context(), content.Feed(feed), s.deps.DefaultLocale, false, 0)
	if err != nil {
		s.serverError(w, err)
		return
	}
	data := s.newTemplateData(r)
	data.Posts = posts
	data.FeedFilter = feed
	s.render(w, http.StatusOK, "posts", data)
}

func (s *server) postNew(w http.ResponseWriter, r *http.Request) {
	s.renderPostForm(w, r, &content.Post{Feed: content.FeedBlog, PublishedAt: time.Now()}, true, nil)
}

func (s *server) postCreate(w http.ResponseWriter, r *http.Request) {
	form, errs := s.parsePostMeta(r)
	if len(errs) > 0 {
		s.renderPostForm(w, r, form, true, errs)
		return
	}
	if u := s.currentUser(r); u != nil {
		form.AuthorID = &u.ID
	}

	id, err := s.deps.Content.InsertPost(r.Context(), form, s.deps.DefaultLocale)
	if err != nil {
		if errors.Is(err, content.ErrDuplicateSlug) {
			s.renderPostForm(w, r, form, true, map[string]string{"slug": s.tr(r, "That address is already used by another page or post.")})
			return
		}
		s.serverError(w, err)
		return
	}

	s.seedStarterSections(r.Context(), form.ID, form.TemplateName)
	s.contentChanged()

	s.flash(r, s.tr(r, "Post created — now add your content below, or open it on the site to edit in place."))
	http.Redirect(w, r, s.deps.AdminPath+"/posts/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *server) postEdit(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURL(w, r)
	if !ok {
		return
	}
	s.renderPostForm(w, r, post, false, nil)
}

func (s *server) postUpdate(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.postFromURL(w, r)
	if !ok {
		return
	}
	locale := s.formLocale(r)

	// Non-default locale tabs edit only the per-locale data: title,
	// summary, and region content. Feed, address, date, and images are
	// locale-independent and live on the default tab.
	if locale != s.deps.DefaultLocale {
		title := strings.TrimSpace(r.PostFormValue("title"))
		if title == "" {
			s.renderPostForm(w, r, existing, false, map[string]string{"title": s.tr(r, "Title is required.")})
			return
		}
		desc := strings.TrimSpace(r.PostFormValue("description"))
		if err := s.deps.Content.UpdateMeta(r.Context(), existing.ID, locale, title, desc); err != nil {
			s.serverError(w, err)
			return
		}
		if err := s.saveRegionContent(r, &existing.Page, locale); err != nil {
			s.serverError(w, err)
			return
		}
		s.finishContentSave(w, r, existing.ID, "Post",
			"posts/"+strconv.FormatInt(existing.PostID, 10), locale)
		return
	}

	form, errs := s.parsePostMeta(r)
	form.ID = existing.ID
	form.PostID = existing.PostID
	form.Status = existing.Status
	form.AuthorID = existing.AuthorID
	form.AuthorName = existing.AuthorName

	// Same rules as pages: per-page CSS/JS is admin-only.
	if s.currentUser(r).Role.IsAdmin() {
		form.HeadCSS = r.PostFormValue("head_css")
		form.BodyJS = r.PostFormValue("body_js")
	} else {
		form.HeadCSS = existing.HeadCSS
		form.BodyJS = existing.BodyJS
	}

	if len(errs) > 0 {
		s.renderPostForm(w, r, form, false, errs)
		return
	}

	if err := s.deps.Content.UpdatePost(r.Context(), form, s.deps.DefaultLocale); err != nil {
		if errors.Is(err, content.ErrDuplicateSlug) {
			s.renderPostForm(w, r, form, false, map[string]string{"slug": s.tr(r, "That address is already used by another page or post.")})
			return
		}
		s.serverError(w, err)
		return
	}

	if err := s.saveRegionContent(r, &form.Page, s.deps.DefaultLocale); err != nil {
		s.serverError(w, err)
		return
	}

	s.finishContentSave(w, r, form.ID, "Post",
		"posts/"+strconv.FormatInt(form.PostID, 10), s.deps.DefaultLocale)
}

func (s *server) postDelete(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURL(w, r)
	if !ok {
		return
	}
	// Deleting the backing page cascades to the post row and its blocks.
	if err := s.deps.Content.Delete(r.Context(), post.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.contentChanged()
	s.flash(r, s.tr(r, "Post deleted."))
	http.Redirect(w, r, s.deps.AdminPath+"/posts", http.StatusSeeOther)
}

func (s *server) postDiscard(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURL(w, r)
	if !ok {
		return
	}
	if post.Status != content.StatusPublished {
		s.flash(r, s.tr(r, "There are no published changes to revert to — this post hasn't been published yet."))
		http.Redirect(w, r, s.deps.AdminPath+"/posts/"+strconv.FormatInt(post.PostID, 10), http.StatusSeeOther)
		return
	}
	if err := s.deps.Content.DiscardDraft(r.Context(), post.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.contentChanged()
	s.flash(r, s.tr(r, "Draft changes discarded — the editor now matches the published post."))
	http.Redirect(w, r, s.deps.AdminPath+"/posts/"+strconv.FormatInt(post.PostID, 10), http.StatusSeeOther)
}

// postPreview renders the post's draft content with the real site
// templates, post data included.
func (s *server) postPreview(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURL(w, r)
	if !ok {
		return
	}
	locale := s.formLocale(r)
	blocks, err := s.deps.Content.EffectiveBlocks(r.Context(), post.ID, locale, content.StatusDraft)
	if err != nil {
		s.serverError(w, err)
		return
	}
	menuItems, err := s.deps.Content.MenuItems(r.Context(), "")
	if err != nil {
		menuItems = nil
	}
	menus := render.BuildMenus(menuItems, post.Slug, locale, s.deps.DefaultLocale, true)
	site, _ := s.deps.Content.SiteSettings(r.Context()) // preview renders fine with defaults
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = s.deps.Renderer.Render(w, render.Input{
		Page:    &post.Page,
		Blocks:  blocks,
		Locale:  locale,
		Menus:   menus,
		Post:    render.PostInfoFor(post, render.LocalePrefix(locale, s.deps.DefaultLocale)),
		Locales: s.deps.Locales,
		Site:    site,
	})
	if err != nil {
		s.serverError(w, err)
	}
}

// parsePostMeta reads and validates the post form's metadata fields. The
// address field holds only the part after the feed prefix; the stored slug
// is always "<feed>/<address>".
func (s *server) parsePostMeta(r *http.Request) (*content.Post, map[string]string) {
	errs := map[string]string{}

	p := &content.Post{}
	p.Title = strings.TrimSpace(r.PostFormValue("title"))
	p.Description = strings.TrimSpace(r.PostFormValue("description"))
	p.TemplateName = s.deps.PostTemplate.File

	feed := r.PostFormValue("feed")
	if !content.ValidFeed(feed) {
		errs["feed"] = s.tr(r, "Choose Blog or News.")
		feed = string(content.FeedBlog)
	}
	p.Feed = content.Feed(feed)

	tail := content.NormalizeSlug(r.PostFormValue("slug"))
	tail = strings.TrimPrefix(tail, feed+"/") // tolerate a pasted full address
	if tail == "" {
		tail = content.Slugify(p.Title)
	}
	if p.Title == "" {
		errs["title"] = s.tr(r, "Title is required.")
	}
	if tail == "" || !content.ValidSlug(tail) {
		errs["slug"] = s.tr(r, "Use only lowercase letters, numbers, and hyphens, e.g. my-first-post.")
	}
	p.Slug = feed + "/" + tail

	if v := strings.TrimSpace(r.PostFormValue("published_at")); v != "" {
		t, err := time.ParseInLocation(datetimeLocalFormat, v, time.Local)
		if err != nil {
			errs["published_at"] = s.tr(r, "Enter a valid date and time.")
		} else {
			p.PublishedAt = t
		}
	}

	if u := strings.TrimSpace(r.PostFormValue("thumbnail_url")); validImageURL(u) {
		p.ThumbnailURL = u
	}
	if u := strings.TrimSpace(r.PostFormValue("header_url")); validImageURL(u) {
		p.HeaderURL = u
	}

	return p, errs
}

func (s *server) renderPostForm(w http.ResponseWriter, r *http.Request, post *content.Post, isNew bool, errs map[string]string) {
	status := http.StatusOK
	if len(errs) > 0 {
		status = http.StatusUnprocessableEntity
	}

	data := s.newTemplateData(r)
	data.FormPost = post
	data.IsNew = isNew
	data.FormErrors = errs
	data.RegionsTemplate = s.deps.PostTemplate.File
	data.EditLocale = s.formLocale(r)

	if !isNew {
		data.Regions = s.deps.Renderer.Regions(s.deps.PostTemplate.File)
		blocks, err := s.deps.Content.EffectiveBlocks(r.Context(), post.ID, data.EditLocale, content.StatusDraft)
		if err != nil {
			s.serverError(w, err)
			return
		}
		data.BlockContent = make(map[string]string, len(blocks))
		for _, b := range blocks {
			data.BlockContent[b.Region] = b.Content
		}

		if post.Status == content.StatusPublished {
			changed, err := s.deps.Content.HasUnpublishedChanges(r.Context(), post.ID)
			if err != nil {
				s.serverError(w, err)
				return
			}
			data.HasDraftEdits = changed
		}
	}

	// The thumbnail and header pickers (and any image regions) choose
	// from the media library.
	if s.deps.Media != nil {
		items, err := s.deps.Media.All(r.Context(), s.deps.DefaultLocale, media.ListOptions{Kind: media.KindImage})
		if err != nil {
			s.serverError(w, err)
			return
		}
		data.Media = s.deps.Media.Views(items)
	}

	s.render(w, status, "post_form", data)
}

// apiCreatePost creates a draft post from the editor's "new post" dialog:
// the title names it, the feed places it, the slug is derived from the
// title (numeric suffix when taken), and the creating user becomes the
// author. Summary, date, and images are optional.
// POST /api/posts {"title", "feed", "summary", "published_at",
// "thumbnail_url", "header_url"} -> {ok, id, url}
func (s *server) apiCreatePost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title        string `json:"title"`
		Feed         string `json:"feed"`
		Summary      string `json:"summary"`
		PublishedAt  string `json:"published_at"` // datetime-local format; "" = now
		ThumbnailURL string `json:"thumbnail_url"`
		HeaderURL    string `json:"header_url"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the request — try again."))
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Give the post a title."))
		return
	}
	if !content.ValidFeed(body.Feed) {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Choose Blog or News."))
		return
	}

	base := content.Slugify(title)
	if base == "" {
		base = "post"
	}
	post := &content.Post{Feed: content.Feed(body.Feed)}
	post.Title = title
	post.Description = strings.TrimSpace(body.Summary)
	post.TemplateName = s.deps.PostTemplate.File
	if u := s.currentUser(r); u != nil {
		post.AuthorID = &u.ID
	}
	if v := strings.TrimSpace(body.PublishedAt); v != "" {
		if t, err := time.ParseInLocation(datetimeLocalFormat, v, time.Local); err == nil {
			post.PublishedAt = t
		}
	}
	if u := strings.TrimSpace(body.ThumbnailURL); u != "" && validImageURL(u) {
		post.ThumbnailURL = u
	}
	if u := strings.TrimSpace(body.HeaderURL); u != "" && validImageURL(u) {
		post.HeaderURL = u
	}
	var err error
	for i := 1; i <= 50; i++ {
		tail := base
		if i > 1 {
			tail = base + "-" + strconv.Itoa(i)
		}
		post.Slug = body.Feed + "/" + tail
		_, err = s.deps.Content.InsertPost(r.Context(), post, s.deps.DefaultLocale)
		if err == nil {
			break
		}
		if !errors.Is(err, content.ErrDuplicateSlug) {
			s.deps.Logger.Error("cms admin: api creating post", "err", err)
			jsonError(w, http.StatusInternalServerError, s.tr(r, "Creating the post failed — try again."))
			return
		}
	}
	if err != nil {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Too many posts already use that name."))
		return
	}

	s.seedStarterSections(r.Context(), post.ID, post.TemplateName)
	s.contentChanged()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"id":  post.PostID,
		"url": "/" + post.Slug,
	})
}

// apiUpdatePostSettings saves the fields behind the in-place editor's
// post-settings gear: date, summary, thumbnail, and header image. Title,
// feed, and slug stay as they are (the admin form owns those). These
// fields have no draft state — like menus, they are live at once.
// PUT /api/posts/{id} {"summary", "published_at", "thumbnail_url",
// "header_url"} -> {ok}
func (s *server) apiUpdatePostSettings(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURL(w, r)
	if !ok {
		return
	}
	var body struct {
		Summary      string `json:"summary"`
		PublishedAt  string `json:"published_at"`
		ThumbnailURL string `json:"thumbnail_url"`
		HeaderURL    string `json:"header_url"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the request — try again."))
		return
	}

	post.Description = strings.TrimSpace(body.Summary)
	if v := strings.TrimSpace(body.PublishedAt); v != "" {
		t, err := time.ParseInLocation(datetimeLocalFormat, v, time.Local)
		if err != nil {
			jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Enter a valid date and time."))
			return
		}
		post.PublishedAt = t
	}
	if u := strings.TrimSpace(body.ThumbnailURL); u == "" || validImageURL(u) {
		post.ThumbnailURL = u
	}
	if u := strings.TrimSpace(body.HeaderURL); u == "" || validImageURL(u) {
		post.HeaderURL = u
	}

	if err := s.deps.Content.UpdatePost(r.Context(), post, s.deps.DefaultLocale); err != nil {
		s.deps.Logger.Error("cms admin: api updating post settings", "post", post.PostID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving the post settings failed — try again."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// postFromURL loads the post identified by the {id} URL parameter, writing
// a 404 and returning ok=false when it is missing or malformed.
func (s *server) postFromURL(w http.ResponseWriter, r *http.Request) (*content.Post, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	post, err := s.deps.Content.PostByID(r.Context(), id, s.formLocale(r))
	if errors.Is(err, content.ErrNotFound) {
		http.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		s.serverError(w, err)
		return nil, false
	}
	return post, true
}
