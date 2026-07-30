package admin

import (
	"context"
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
	// The admin sees drafts and private posts, so it counts them too:
	// publishedOnly is false here exactly as it is for the window below,
	// which keeps the page count describing what the table shows.
	total, err := s.deps.Content.CountPosts(r.Context(), content.Feed(feed), false)
	if err != nil {
		s.serverError(w, err)
		return
	}
	// Count, then clamp, then fetch — so a ?page= past the end reads the
	// last real page rather than a window off the end of the list.
	pager := render.NewPager(listPage(r), s.perPage(), total, s.listPageURL(r))
	pager.PrevLabel, pager.NextLabel = s.tr(r, "Previous"), s.tr(r, "Next")

	posts, err := s.deps.Content.PostsPage(r.Context(), content.Feed(feed),
		s.deps.DefaultLocale, false, pager.PerPage, pager.Offset())
	if err != nil {
		s.serverError(w, err)
		return
	}
	data := s.newTemplateData(r)
	data.Posts = posts
	data.FeedFilter = feed
	data.Pager = pager
	s.render(w, http.StatusOK, "posts", data)
}

func (s *server) postNew(w http.ResponseWriter, r *http.Request) {
	s.renderPostForm(w, r, &content.Post{Feed: content.FeedBlog, PublishedAt: time.Now()}, true, nil)
}

func (s *server) postCreate(w http.ResponseWriter, r *http.Request) {
	form, errs := s.parsePostMeta(r, nil)
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
	s.seedHeaderSection(r.Context(), form.ID, form.TemplateName, s.postBanner(r.Context(), form))
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

	form, errs := s.parsePostMeta(r, existing)
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

// postUnpublish takes the post off the public site — out of the feed and
// the listing as well as its own URL. Content is untouched, so publishing
// again restores it.
func (s *server) postUnpublish(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURL(w, r)
	if !ok {
		return
	}
	if err := s.deps.Content.Unpublish(r.Context(), post.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.contentChanged()
	s.flash(r, s.tr(r, "Post unpublished — it is no longer visible on the site."))
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
		Post:    render.PostInfoFor(post, render.LocalePrefix(locale, s.deps.DefaultLocale), s.postImages()),
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
//
// existing is the stored post when editing and nil when creating. It
// supplies the fields the form did not offer: with no media library the
// image pickers are absent, and a save must leave the post's images alone
// rather than clearing them.
func (s *server) parsePostMeta(r *http.Request, existing *content.Post) (*content.Post, map[string]string) {
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

	switch {
	case s.deps.Media != nil:
		p.ThumbnailMediaID, p.ThumbnailURL = s.parsePostImage(r, "thumbnail")
	case existing != nil:
		p.ThumbnailMediaID, p.ThumbnailURL = existing.ThumbnailMediaID, existing.ThumbnailURL
	}

	return p, errs
}

// parsePostImage reads the post form's image picker, whose select holds
// a library id, "keep" (hold on to an image that is not in the library,
// carried in the hidden field beside it), or "" for none. An id
// naming something that is not a library image is treated as none rather
// than failing the save: it can only come from a tampered form or an image
// deleted while the form was open.
func (s *server) parsePostImage(r *http.Request, field string) (*int64, string) {
	switch choice := strings.TrimSpace(r.PostFormValue(field + "_media_id")); choice {
	case "":
		return nil, ""
	case "keep":
		if u := strings.TrimSpace(r.PostFormValue(field + "_url")); validImageURL(u) {
			return nil, u
		}
		return nil, ""
	default:
		id, err := strconv.ParseInt(choice, 10, 64)
		if err != nil || !s.isLibraryImage(r.Context(), id) {
			return nil, ""
		}
		return &id, ""
	}
}

// isLibraryImage reports whether id names an image in the media library —
// the check that keeps a post's foreign key pointing at something real.
func (s *server) isLibraryImage(ctx context.Context, id int64) bool {
	if s.deps.Media == nil || id <= 0 {
		return false
	}
	md, err := s.deps.Media.GetByID(ctx, id, s.deps.DefaultLocale)
	return err == nil && md.Kind == media.KindImage
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

	// The thumbnail picker (and any image regions) chooses from the
	// media library.
	if s.deps.Media != nil {
		items, err := s.deps.Media.All(r.Context(), s.deps.DefaultLocale, media.ListOptions{Kind: media.KindImage})
		if err != nil {
			s.serverError(w, err)
			return
		}
		data.Media = s.deps.Media.Views(items)
		data.FormPostThumb = s.postImagePreview(r.Context(), post.Thumbnail, post.ThumbnailMediaID, post.ThumbnailURL)
	}

	s.render(w, status, "post_form", data)
}

// postBanner describes the banner a newly created post should start
// with, from the image it was created with. The address is the full-width
// rendition — a section stores its background as a plain URL rather than
// a library id, so the rung has to be chosen here rather than at render
// time — and the darkness decides what colour the title over it starts
// out. An image the library does not hold keeps its address and is taken
// to be light, since there is nothing to measure.
func (s *server) postBanner(ctx context.Context, p *content.Post) bannerSeed {
	seed := bannerSeed{URL: p.ThumbnailURL, Title: p.Title}
	if p.ThumbnailMediaID == nil || s.deps.Media == nil {
		return seed
	}
	md, err := s.deps.Media.GetByID(ctx, *p.ThumbnailMediaID, s.deps.DefaultLocale)
	if err != nil {
		return seed
	}
	if img := s.deps.Media.ImageFor(md, "web"); img != nil {
		seed.URL = img.URL
	}
	// A picture too odd to measure — a vector, a rendition that never got
	// made — leaves the title in the site's own colour rather than
	// guessing at white.
	dark, err := s.deps.Media.IsDark(ctx, md)
	if err != nil {
		s.deps.Logger.Debug("cms admin: measuring banner image", "media", md.ID, "err", err)
	}
	seed.Dark = dark
	return seed
}

// postImagePreview is the URL for the small preview beside the post
// form's image picker: a library image's thumbnail rendition, or an
// external URL as it stands.
//
// md is nil on a form re-rendered after a validation error, which was
// parsed from the submission and so carries the id without the joined
// record; the record is fetched then, so the picture the editor chose is
// still on screen beside the message telling them what to fix.
func (s *server) postImagePreview(ctx context.Context, md *media.Media, id *int64, fallbackURL string) string {
	if md == nil && id != nil {
		if loaded, err := s.deps.Media.GetByID(ctx, *id, s.deps.DefaultLocale); err == nil {
			md = loaded
		}
	}
	if md != nil {
		if img := s.deps.Media.ImageFor(md, "thumb"); img != nil {
			return img.URL
		}
	}
	return fallbackURL
}

// postImages resolves posts' library images for renders the admin drives
// itself (the post preview). Nil without a media library.
func (s *server) postImages() render.PostImages {
	if s.deps.Media == nil {
		return nil
	}
	return s.deps.Media.ImageFor
}

// apiCreatePost creates a draft post from the editor's "new post" dialog:
// the title names it, the feed places it, the slug is derived from the
// title (numeric suffix when taken), and the creating user becomes the
// author. Summary, date, and thumbnail are optional.
// POST /api/posts {"title", "feed", "summary", "published_at",
// "thumbnail_media_id", "thumbnail_url"} -> {ok, id, url}
func (s *server) apiCreatePost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title            string `json:"title"`
		Feed             string `json:"feed"`
		Summary          string `json:"summary"`
		PublishedAt      string `json:"published_at"` // datetime-local format; "" = now
		ThumbnailMediaID int64  `json:"thumbnail_media_id"`
		ThumbnailURL     string `json:"thumbnail_url"`
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
	post.ThumbnailMediaID, post.ThumbnailURL = s.apiPostImage(r.Context(), body.ThumbnailMediaID, body.ThumbnailURL)
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
	s.seedHeaderSection(r.Context(), post.ID, post.TemplateName, s.postBanner(r.Context(), post))
	s.contentChanged()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"id":  post.PostID,
		"url": "/" + post.Slug,
	})
}

// apiUpdatePostSettings saves the fields behind the in-place editor's
// post-settings gear: date, summary, and thumbnail. Title, feed, and slug
// stay as they are (the admin form owns those), and the banner at the top
// of the post is a section, saved with the rest of the page. These fields
// have no draft state — like menus, they are live at once.
// PUT /api/posts/{id} {"summary", "published_at", "thumbnail_media_id",
// "thumbnail_url"} -> {ok}
func (s *server) apiUpdatePostSettings(w http.ResponseWriter, r *http.Request) {
	post, ok := s.postFromURL(w, r)
	if !ok {
		return
	}
	var body struct {
		Summary          string `json:"summary"`
		PublishedAt      string `json:"published_at"`
		ThumbnailMediaID int64  `json:"thumbnail_media_id"`
		ThumbnailURL     string `json:"thumbnail_url"`
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
	post.ThumbnailMediaID, post.ThumbnailURL = s.apiPostImage(r.Context(), body.ThumbnailMediaID, body.ThumbnailURL)

	if err := s.deps.Content.UpdatePost(r.Context(), post, s.deps.DefaultLocale); err != nil {
		s.deps.Logger.Error("cms admin: api updating post settings", "post", post.PostID, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving the post settings failed — try again."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// apiPostImage resolves one image field of the post JSON APIs. A library
// id wins; failing that an address is kept when it looks like an image
// URL, which is how an image from outside the library survives a save.
// Neither means the post has no such image.
func (s *server) apiPostImage(ctx context.Context, id int64, rawURL string) (*int64, string) {
	if s.isLibraryImage(ctx, id) {
		return &id, ""
	}
	if u := strings.TrimSpace(rawURL); u != "" && validImageURL(u) {
		return nil, u
	}
	return nil, ""
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
