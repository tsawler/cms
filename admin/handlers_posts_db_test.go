package admin

// The Blog & News section's write handlers. A post is a page with a feed,
// a date, and an address that is not entirely its own — the feed owns the
// prefix — so most of what these pin down is how a submission is turned
// into those, and who is allowed to say which feed it lands in.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// postFormValues is a complete, valid post submission — everything the
// form offers except the date, which is what a brand-new post leaves to
// InsertPost to fill in.
func postFormValues() url.Values {
	return url.Values{
		"title":       {"First Post"},
		"slug":        {"first-post"},
		"feed":        {"blog"},
		"show_author": {"on"},
	}
}

// postUpdateForm adds the date. The edit form always renders one — it is
// a datetime-local pre-filled from the stored value — so a save carries
// it back, and a save of an existing post is where the difference shows:
// unlike a create, an update writes the submitted date straight through.
func postUpdateForm() url.Values {
	f := postFormValues()
	f.Set("published_at", "2026-02-01T09:00")
	return f
}

func TestPostCreate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, postFormValues(), nil)
		s.postCreate(rec, r)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303 (body %q)", rec.Code, rec.Body.String())
		}

		page, err := s.deps.Content.GetBySlug(context.Background(), "blog/first-post", "en", false)
		if err != nil {
			t.Fatalf("the post was not stored: %v", err)
		}
		post, err := s.deps.Content.PostByPageID(context.Background(), page.ID, "en", true)
		if err != nil {
			t.Fatalf("no post row for the page: %v", err)
		}
		if post.Feed != content.FeedBlog {
			t.Errorf("feed = %q, want blog", post.Feed)
		}
		if post.TemplateName != s.deps.PostTemplate.File {
			t.Errorf("template = %q, want the configured post template", post.TemplateName)
		}
		// Whoever typed it is recorded as the author, which is what the
		// byline is drawn from.
		if post.AuthorID == nil || *post.AuthorID != u.ID {
			t.Errorf("author = %v, want the signed-in user %d", post.AuthorID, u.ID)
		}
		if post.HideAuthor {
			t.Error("the byline is hidden even though show_author was ticked")
		}
	})
}

// The address a post form offers is only the tail: the feed owns the
// prefix, and a post that could be given any address at all would be a
// page.
func TestPostCreateSlugIsAlwaysUnderItsFeed(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		cases := map[string]string{
			// A bare tail is prefixed.
			"plain tail": "news/spring-sale",
			// A pasted full address is tolerated rather than doubled.
			"pasted full address": "news/spring-sale",
			// An empty tail falls back to the title.
			"empty": "news/spring-sale",
		}
		submitted := map[string]string{
			"plain tail":          "spring-sale",
			"pasted full address": "news/spring-sale",
			"empty":               "",
		}
		for name, want := range cases {
			t.Run(name, func(t *testing.T) {
				form := postFormValues()
				form.Set("feed", "news")
				form.Set("title", "Spring Sale")
				form.Set("slug", submitted[name])

				got, errs := s.parsePostMeta(formReq(t, s, u, form, nil), nil)
				if len(errs) > 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				if got.Slug != want {
					t.Errorf("slug = %q, want %q", got.Slug, want)
				}
			})
		}
	})
}

func TestParsePostMeta(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		t.Run("an unchecked byline box hides the author", func(t *testing.T) {
			form := postFormValues()
			form.Del("show_author")
			got, _ := s.parsePostMeta(formReq(t, s, u, form, nil), nil)
			if !got.HideAuthor {
				t.Error("HideAuthor = false, want true when the box is unticked")
			}
		})

		t.Run("a date is read in local time", func(t *testing.T) {
			form := postFormValues()
			form.Set("published_at", "2026-03-04T15:30")
			got, errs := s.parsePostMeta(formReq(t, s, u, form, nil), nil)
			if len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			want := time.Date(2026, 3, 4, 15, 30, 0, 0, time.Local)
			if !got.PublishedAt.Equal(want) {
				t.Errorf("published_at = %v, want %v", got.PublishedAt, want)
			}
		})

		t.Run("an unreadable date is an error, not a silent now()", func(t *testing.T) {
			form := postFormValues()
			form.Set("published_at", "last Tuesday")
			_, errs := s.parsePostMeta(formReq(t, s, u, form, nil), nil)
			if errs["published_at"] == "" {
				t.Error("no error for an unparseable date")
			}
		})

		t.Run("an unknown feed is rejected", func(t *testing.T) {
			form := postFormValues()
			form.Set("feed", "gossip")
			got, errs := s.parsePostMeta(formReq(t, s, u, form, nil), nil)
			if errs["feed"] == "" {
				t.Error("no error for an unknown feed")
			}
			// It still falls back to a real feed, so the re-rendered form
			// has something valid selected.
			if got.Feed != content.FeedBlog {
				t.Errorf("feed = %q, want the blog fallback", got.Feed)
			}
		})

		t.Run("a missing title is an error", func(t *testing.T) {
			form := postFormValues()
			form.Set("title", "   ")
			// With no title and no slug there is nothing to build an
			// address from either, so both fields are marked.
			form.Set("slug", "")
			_, errs := s.parsePostMeta(formReq(t, s, u, form, nil), nil)
			if errs["title"] == "" {
				t.Error("no error for a missing title")
			}
			if errs["slug"] == "" {
				t.Error("no error for an address that cannot be derived")
			}
		})
	})
}

// Feeds are separately grantable, and the form's feed picker is the one
// place a post can be moved between them — so the check has to be on the
// submitted feed, not on the one the post is already in.
func TestPostFeedNeedsItsPermission(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		blogger := formUser(t, s, "form-blogs-only@example.com", auth.RoleEditor, auth.PermBlogs)

		form := postFormValues()
		form.Set("feed", "news")
		_, errs := s.parsePostMeta(formReq(t, s, blogger, form, nil), nil)
		if errs["feed"] == "" {
			t.Error("a blogs-only editor was allowed to publish to news")
		}

		form.Set("feed", "blog")
		if _, errs := s.parsePostMeta(formReq(t, s, blogger, form, nil), nil); len(errs) > 0 {
			t.Errorf("a blogs-only editor was refused their own feed: %v", errs)
		}
	})
}

func TestPostCreateRejectsAndStoresNothing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		form := postFormValues()
		form.Set("title", "")
		form.Set("slug", "")

		rec := httptest.NewRecorder()
		s.postCreate(rec, formReq(t, s, u, form, nil))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		posts, err := s.deps.Content.Posts(context.Background(), content.FeedBlog, "en", false, 100)
		if err != nil {
			t.Fatalf("listing posts: %v", err)
		}
		for _, p := range posts {
			if p.Title == "" {
				t.Error("a rejected submission was stored anyway")
			}
		}
	})
}

func TestPostCreateDuplicateSlug(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		seedPost(t, s, content.FeedBlog, "Taken", "taken")

		form := postFormValues()
		form.Set("slug", "taken")
		rec := httptest.NewRecorder()
		s.postCreate(rec, formReq(t, s, u, form, nil))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "already used") {
			t.Errorf("the form does not explain the collision: %q", rec.Body.String())
		}
	})
}

func TestPostUpdate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post := seedPost(t, s, content.FeedBlog, "Before", "update-me")

		form := postUpdateForm()
		form.Set("title", "After")
		form.Set("slug", "update-me")
		form.Set("description", "A summary")

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, form, idParams(post.PostID))
		s.postUpdate(rec, r)

		wantRedirect(t, rec, "/admin/posts/"+itoa(post.PostID))
		after := reloadPost(t, s, post.PostID)
		if after.Title != "After" {
			t.Errorf("title = %q, want After", after.Title)
		}
		if after.Description != "A summary" {
			t.Errorf("description = %q, want the submitted summary", after.Description)
		}
		if after.Status == content.StatusPublished {
			t.Error("a plain save published the post")
		}
	})
}

// The author is fixed at creation: a later save by somebody else edits the
// post, it does not take it over.
func TestPostUpdateKeepsTheOriginalAuthor(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		author := formUser(t, s, "form-author@example.com", auth.RoleAdmin)
		other := formUser(t, s, "form-other@example.com", auth.RoleAdmin)

		form := postUpdateForm()
		form.Set("slug", "authored")
		rec := httptest.NewRecorder()
		s.postCreate(rec, formReq(t, s, author, form, nil))

		page, err := s.deps.Content.GetBySlug(context.Background(), "blog/authored", "en", false)
		if err != nil {
			t.Fatalf("the post was not stored: %v", err)
		}
		post, err := s.deps.Content.PostByPageID(context.Background(), page.ID, "en", true)
		if err != nil {
			t.Fatalf("no post row: %v", err)
		}

		form.Set("title", "Edited by someone else")
		rec = httptest.NewRecorder()
		s.postUpdate(rec, formReq(t, s, other, form, idParams(post.PostID)))

		after := reloadPost(t, s, post.PostID)
		if after.AuthorID == nil || *after.AuthorID != author.ID {
			t.Errorf("author = %v, want the original author %d", after.AuthorID, author.ID)
		}
	})
}

// A translation tab edits the title, the summary, and the meta
// description. The feed, the address, and the date are locale-independent
// and stay on the default tab.
func TestPostUpdateInAnotherLocale(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post := seedPost(t, s, content.FeedBlog, "English", "locale-post")

		form := url.Values{
			"title":            {"Titre"},
			"description":      {"Résumé"},
			"meta_description": {"Description pour les moteurs"},
			"feed":             {"news"},
			"slug":             {"autre-adresse"},
		}
		rec := httptest.NewRecorder()
		r := formReqTo(t, s, u, http.MethodPost, "/admin/x?locale=fr", form, idParams(post.PostID))
		s.postUpdate(rec, r)

		wantRedirect(t, rec, "/admin/posts/"+itoa(post.PostID)+"?locale=fr")

		after := reloadPost(t, s, post.PostID)
		if after.Slug != "blog/locale-post" {
			t.Errorf("slug = %q, want it untouched by a French save", after.Slug)
		}
		if after.Feed != content.FeedBlog {
			t.Errorf("feed = %q, want it untouched by a French save", after.Feed)
		}

		fr, err := s.deps.Content.MetaFor(context.Background(), post.ID, "fr")
		if err != nil {
			t.Fatalf("reading the French metadata: %v", err)
		}
		if fr.Title != "Titre" || fr.Description != "Résumé" {
			t.Errorf("French metadata = %+v, want the submitted values", fr)
		}
		if fr.MetaDescription != "Description pour les moteurs" {
			t.Errorf("French meta description = %q, want the submitted value", fr.MetaDescription)
		}
	})
}

func TestPostUpdateInAnotherLocaleNeedsATitle(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post := seedPost(t, s, content.FeedBlog, "English", "fr-needs-title")

		rec := httptest.NewRecorder()
		r := formReqTo(t, s, u, http.MethodPost, "/admin/x?locale=fr",
			url.Values{"title": {" "}}, idParams(post.PostID))
		s.postUpdate(rec, r)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422", rec.Code)
		}
	})
}

func TestPostUpdatePublishAction(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post := seedPost(t, s, content.FeedBlog, "Publish me", "publish-post")

		form := postUpdateForm()
		form.Set("title", "Publish me")
		form.Set("slug", "publish-post")
		form.Set("action", "publish")

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, form, idParams(post.PostID))
		s.postUpdate(rec, r)

		wantRedirect(t, rec, "/admin/posts/"+itoa(post.PostID))
		if got := reloadPost(t, s, post.PostID).Status; got != content.StatusPublished {
			t.Errorf("status = %q, want published", got)
		}
		if f := flashOf(s, r); !strings.Contains(f, "Post published") {
			t.Errorf("flash = %q, want the post-specific published message", f)
		}
	})
}

func TestPostDelete(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post := seedPost(t, s, content.FeedBlog, "Doomed", "delete-post")

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, nil, idParams(post.PostID))
		s.postDelete(rec, r)

		wantRedirect(t, rec, "/admin/posts")
		// Deleting the backing page cascades to the post row.
		if _, err := s.deps.Content.PostByID(context.Background(), post.PostID, "en"); err == nil {
			t.Error("the post row is still there")
		}
		if _, err := s.deps.Content.GetByID(context.Background(), post.ID, "en"); err == nil {
			t.Error("the backing page is still there")
		}
	})
}

func TestPostDiscardAndUnpublish(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()
		post := seedPost(t, s, content.FeedBlog, "Live post", "live-post")

		// Discarding an unpublished post has nothing to revert to.
		rec := httptest.NewRecorder()
		r := formReq(t, s, u, nil, idParams(post.PostID))
		s.postDiscard(rec, r)
		wantRedirect(t, rec, "/admin/posts/"+itoa(post.PostID))
		if f := flashOf(s, r); !strings.Contains(f, "hasn't been published") {
			t.Errorf("flash = %q, want the explanation", f)
		}

		if err := s.deps.Content.UpsertDraftBlock(ctx, post.ID, "main", "en",
			content.KindHTML, "<p>published words</p>"); err != nil {
			t.Fatalf("seeding content: %v", err)
		}
		if err := s.deps.Content.Publish(ctx, post.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := s.deps.Content.UpsertDraftBlock(ctx, post.ID, "main", "en",
			content.KindHTML, "<p>unsaved rewrite</p>"); err != nil {
			t.Fatalf("seeding a draft edit: %v", err)
		}

		rec = httptest.NewRecorder()
		s.postDiscard(rec, formReq(t, s, u, nil, idParams(post.PostID)))
		wantRedirect(t, rec, "/admin/posts/"+itoa(post.PostID))
		if got := draftHTML(t, s, post.ID, "main", "en"); strings.Contains(got, "unsaved rewrite") {
			t.Errorf("draft = %q, want the discarded edit gone", got)
		}

		rec = httptest.NewRecorder()
		r = formReq(t, s, u, nil, idParams(post.PostID))
		s.postUnpublish(rec, r)
		wantRedirect(t, rec, "/admin/posts/"+itoa(post.PostID))
		if got := reloadPost(t, s, post.PostID).Status; got == content.StatusPublished {
			t.Error("the post is still published")
		}
		// Off the site, but still there to publish again.
		if got := draftHTML(t, s, post.ID, "main", "en"); !strings.Contains(got, "published words") {
			t.Errorf("draft = %q, want the content untouched", got)
		}
	})
}

// A new-post form opens on a feed the user can actually publish to, so
// somebody who only holds news does not start every post in the blog and
// have it refused on save.
func TestPostNewOpensOnAFeedTheUserHolds(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		newsOnly := formUser(t, s, "form-news-only@example.com", auth.RoleEditor, auth.PermNews)

		rec := httptest.NewRecorder()
		s.postNew(rec, formReqTo(t, s, newsOnly, http.MethodGet, "/admin/posts/new", nil, nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		newsSelected := strings.Contains(body, `value="news" selected`) ||
			strings.Contains(body, `value="news" checked`)
		if !newsSelected {
			t.Errorf("the form does not start on news for a news-only editor: %q", body)
		}
	})
}

func TestPostPreviewRendersTheDraft(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post := seedPost(t, s, content.FeedBlog, "Preview post", "preview-post")
		if err := s.deps.Content.UpsertDraftBlock(context.Background(), post.ID, "main", "en",
			content.KindHTML, "<p>draft only</p>"); err != nil {
			t.Fatalf("seeding draft content: %v", err)
		}

		rec := httptest.NewRecorder()
		s.postPreview(rec, formReqTo(t, s, u, http.MethodGet, "/admin/posts/x/preview",
			nil, idParams(post.PostID)))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "draft only") {
			t.Errorf("the preview does not show the unpublished content: %q", rec.Body.String())
		}
	})
}
