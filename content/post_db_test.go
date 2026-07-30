package content_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
)

// seedPost inserts a draft post with its backing page, defaulting fields the
// test does not care about.
func seedPost(t *testing.T, s *content.Store, p content.Post) *content.Post {
	t.Helper()
	if p.TemplateName == "" {
		p.TemplateName = "post.gohtml"
	}
	if p.Feed == "" {
		p.Feed = content.FeedBlog
	}
	if _, err := s.InsertPost(context.Background(), &p, defaultLocale); err != nil {
		t.Fatalf("seeding post %q: %v", p.Slug, err)
	}
	return &p
}

func TestPostInsertAndGet(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		// A real author, so the cms_users join has something to resolve.
		users := auth.NewStore(db)
		author := &auth.User{Email: "author@example.com", Name: "Ada Lovelace",
			PasswordHash: "x", Role: auth.RoleEditor, Active: true}
		if _, err := users.Insert(ctx, author); err != nil {
			t.Fatalf("seeding author: %v", err)
		}

		when := time.Date(2026, 3, 14, 15, 9, 26, 0, time.UTC)
		post := seedPost(t, s, content.Post{
			Page: content.Page{
				Slug:        "blog/launch-day",
				Title:       "Launch Day",
				Description: "We shipped it",
			},
			Feed:         content.FeedBlog,
			PublishedAt:  when,
			AuthorID:     &author.ID,
			ThumbnailURL: "/img/thumb.png",
		})
		if post.PostID == 0 {
			t.Fatal("InsertPost did not set the post id")
		}
		if post.ID == 0 {
			t.Fatal("InsertPost did not set the backing page id")
		}

		got, err := s.PostByID(ctx, post.PostID, defaultLocale)
		if err != nil {
			t.Fatalf("PostByID: %v", err)
		}
		if got.Slug != "blog/launch-day" {
			t.Errorf("slug = %q, want %q", got.Slug, "blog/launch-day")
		}
		if got.Title != "Launch Day" {
			t.Errorf("title = %q, want %q", got.Title, "Launch Day")
		}
		if got.Feed != content.FeedBlog {
			t.Errorf("feed = %q, want %q", got.Feed, content.FeedBlog)
		}
		if !got.PublishedAt.UTC().Equal(when) {
			t.Errorf("published_at = %v, want %v", got.PublishedAt.UTC(), when)
		}
		if got.AuthorName != "Ada Lovelace" {
			t.Errorf("author name = %q, want it resolved from cms_users", got.AuthorName)
		}
		if got.ThumbnailURL != "/img/thumb.png" {
			t.Errorf("thumbnail = %q, want %q", got.ThumbnailURL, "/img/thumb.png")
		}
		// A post's backing page starts as a draft, like any page.
		if got.Status != content.StatusDraft {
			t.Errorf("status = %q, want draft", got.Status)
		}

		byPage, err := s.PostByPageID(ctx, post.ID, defaultLocale, true)
		if err != nil {
			t.Fatalf("PostByPageID: %v", err)
		}
		if byPage.PostID != post.PostID {
			t.Errorf("PostByPageID returned post %d, want %d", byPage.PostID, post.PostID)
		}
	})
}

func TestPostDefaultsPublishedAt(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		before := time.Now().Add(-time.Minute)
		post := seedPost(t, s, content.Post{Page: content.Page{Slug: "blog/undated", Title: "Undated"}})
		got, err := s.PostByID(ctx, post.PostID, defaultLocale)
		if err != nil {
			t.Fatalf("PostByID: %v", err)
		}
		// A zero PublishedAt is documented to become "now".
		if got.PublishedAt.Before(before) {
			t.Errorf("published_at = %v, want roughly now", got.PublishedAt)
		}
	})
}

func TestPostNotFound(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		if _, err := s.PostByID(ctx, 4242, defaultLocale); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("PostByID(missing) = %v, want ErrNotFound", err)
		}
		// A plain page is not a post.
		p := seedPage(t, s, content.Page{Slug: "not-a-post"}, defaultLocale)
		if _, err := s.PostByPageID(ctx, p.ID, defaultLocale, true); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("PostByPageID(plain page) = %v, want ErrNotFound", err)
		}
	})
}

func TestPostInsertDuplicateSlug(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		seedPost(t, s, content.Post{Page: content.Page{Slug: "blog/taken", Title: "Taken"}})

		dup := &content.Post{
			Page: content.Page{Slug: "blog/taken", TemplateName: "post.gohtml", Title: "Dup"},
			Feed: content.FeedBlog,
		}
		if _, err := s.InsertPost(ctx, dup, defaultLocale); !errors.Is(err, content.ErrDuplicateSlug) {
			t.Fatalf("InsertPost(duplicate slug) = %v, want ErrDuplicateSlug", err)
		}
	})
}

func TestPostUpdate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		post := seedPost(t, s, content.Post{
			Page: content.Page{Slug: "blog/before", Title: "Before"},
			Feed: content.FeedBlog,
		})
		if err := s.Publish(ctx, post.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		when := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
		post.Slug = "news/after"
		post.Title = "After"
		post.Feed = content.FeedNews
		post.PublishedAt = when
		post.ThumbnailURL = "/img/new.png"
		if err := s.UpdatePost(ctx, post, defaultLocale); err != nil {
			t.Fatalf("UpdatePost: %v", err)
		}

		got, err := s.PostByID(ctx, post.PostID, defaultLocale)
		if err != nil {
			t.Fatalf("PostByID: %v", err)
		}
		// Slug and the cms_posts fields apply immediately...
		if got.Slug != "news/after" {
			t.Errorf("slug = %q, want %q", got.Slug, "news/after")
		}
		if got.Feed != content.FeedNews {
			t.Errorf("feed = %q, want %q", got.Feed, content.FeedNews)
		}
		if !got.PublishedAt.UTC().Equal(when) {
			t.Errorf("published_at = %v, want %v", got.PublishedAt.UTC(), when)
		}
		if got.ThumbnailURL != "/img/new.png" {
			t.Errorf("thumbnail = %q, want %q", got.ThumbnailURL, "/img/new.png")
		}
		// ...but the title is staged.
		if got.Title != "After" {
			t.Errorf("working-copy title = %q, want %q", got.Title, "After")
		}
		live, err := s.PostByPageID(ctx, post.ID, defaultLocale, false)
		if err != nil {
			t.Fatalf("PostByPageID(published): %v", err)
		}
		if live.Title != "Before" {
			t.Errorf("published title = %q, want the pre-edit %q", live.Title, "Before")
		}
	})
}

// A post carries two descriptions and a byline switch. The meta
// description is staged metadata like the title, so it reaches the site
// on Publish; the byline is a cms_posts field and applies at once.
func TestPostMetaDescriptionAndByline(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		post := seedPost(t, s, content.Post{
			Page: content.Page{Slug: "news/notice", Title: "Notice",
				Description: "What changed, for the listing"},
			Feed: content.FeedNews,
		})

		// A post that sets none publishes its summary; MetaTag is what
		// resolves that, so an unset field is not an empty meta tag.
		if got := post.MetaDescription; got != "" {
			t.Errorf("new post meta description = %q, want empty", got)
		}
		if got, err := s.PostByID(ctx, post.PostID, defaultLocale); err != nil {
			t.Fatalf("PostByID: %v", err)
		} else if got.MetaTag() != "What changed, for the listing" {
			t.Errorf("MetaTag = %q, want the summary", got.MetaTag())
		}

		if err := s.Publish(ctx, post.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		post.MetaDescription = "The notice in search-result words"
		post.HideAuthor = true
		if err := s.UpdatePost(ctx, post, defaultLocale); err != nil {
			t.Fatalf("UpdatePost: %v", err)
		}

		got, err := s.PostByID(ctx, post.PostID, defaultLocale)
		if err != nil {
			t.Fatalf("PostByID: %v", err)
		}
		if got.MetaDescription != "The notice in search-result words" {
			t.Errorf("working-copy meta description = %q", got.MetaDescription)
		}
		if got.MetaTag() != "The notice in search-result words" {
			t.Errorf("MetaTag = %q, want the post's own", got.MetaTag())
		}
		if got.Description != "What changed, for the listing" {
			t.Errorf("summary = %q, want it untouched", got.Description)
		}
		// The byline is not staged: hiding it takes effect on the live
		// post without a Publish, like the date.
		if !got.HideAuthor {
			t.Error("working copy still shows the byline")
		}
		live, err := s.PostByPageID(ctx, post.ID, defaultLocale, false)
		if err != nil {
			t.Fatalf("PostByPageID(published): %v", err)
		}
		if !live.HideAuthor {
			t.Error("published post still shows the byline")
		}
		// ...while the meta description waits for one.
		if live.MetaDescription != "" {
			t.Errorf("published meta description = %q, want the pre-edit empty", live.MetaDescription)
		}

		if err := s.Publish(ctx, post.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if live, err = s.PostByPageID(ctx, post.ID, defaultLocale, false); err != nil {
			t.Fatalf("PostByPageID(published): %v", err)
		}
		if live.MetaDescription != "The notice in search-result words" {
			t.Errorf("published meta description = %q, want the edited one", live.MetaDescription)
		}
	})
}

func TestPostsFiltering(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		older := seedPost(t, s, content.Post{
			Page:        content.Page{Slug: "blog/older", Title: "Older"},
			Feed:        content.FeedBlog,
			PublishedAt: base,
		})
		newer := seedPost(t, s, content.Post{
			Page:        content.Page{Slug: "blog/newer", Title: "Newer"},
			Feed:        content.FeedBlog,
			PublishedAt: base.Add(48 * time.Hour),
		})
		newsItem := seedPost(t, s, content.Post{
			Page:        content.Page{Slug: "news/item", Title: "News Item"},
			Feed:        content.FeedNews,
			PublishedAt: base.Add(24 * time.Hour),
		})

		slugsOf := func(posts []content.Post) []string {
			out := make([]string, len(posts))
			for i, p := range posts {
				out[i] = p.Slug
			}
			return out
		}
		equal := func(got, want []string) bool {
			if len(got) != len(want) {
				return false
			}
			for i := range want {
				if got[i] != want[i] {
					return false
				}
			}
			return true
		}

		// An empty feed returns both, newest first.
		all, err := s.Posts(ctx, "", defaultLocale, false, 0)
		if err != nil {
			t.Fatalf("Posts(all): %v", err)
		}
		if want := []string{"blog/newer", "news/item", "blog/older"}; !equal(slugsOf(all), want) {
			t.Errorf("Posts(all) = %v, want %v", slugsOf(all), want)
		}

		// Filtering by feed.
		blog, err := s.Posts(ctx, content.FeedBlog, defaultLocale, false, 0)
		if err != nil {
			t.Fatalf("Posts(blog): %v", err)
		}
		if want := []string{"blog/newer", "blog/older"}; !equal(slugsOf(blog), want) {
			t.Errorf("Posts(blog) = %v, want %v", slugsOf(blog), want)
		}

		// Limiting.
		limited, err := s.Posts(ctx, "", defaultLocale, false, 2)
		if err != nil {
			t.Fatalf("Posts(limit 2): %v", err)
		}
		if len(limited) != 2 {
			t.Errorf("Posts(limit 2) returned %d posts, want 2", len(limited))
		}

		// publishedOnly hides drafts; nothing is published yet.
		published, err := s.Posts(ctx, "", defaultLocale, true, 0)
		if err != nil {
			t.Fatalf("Posts(publishedOnly): %v", err)
		}
		if len(published) != 0 {
			t.Errorf("Posts(publishedOnly) = %v, want none while all are drafts", slugsOf(published))
		}

		// Publish two, and make one of them private.
		if err := s.Publish(ctx, newer.ID); err != nil {
			t.Fatalf("Publish(newer): %v", err)
		}
		if err := s.Publish(ctx, newsItem.ID); err != nil {
			t.Fatalf("Publish(news): %v", err)
		}
		if err := s.SetVisibility(ctx, newsItem.ID, content.VisibilityPrivate); err != nil {
			t.Fatalf("SetVisibility: %v", err)
		}
		_ = older

		published, err = s.Posts(ctx, "", defaultLocale, true, 0)
		if err != nil {
			t.Fatalf("Posts(publishedOnly): %v", err)
		}
		// Private posts are omitted from the public view even when published.
		if want := []string{"blog/newer"}; !equal(slugsOf(published), want) {
			t.Errorf("Posts(publishedOnly) = %v, want %v", slugsOf(published), want)
		}
	})
}

func TestPostsPageWindowsAndCounts(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		// Seven blog posts a day apart, plus one news post that must not
		// leak into a blog page or its count.
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := range 7 {
			seedPost(t, s, content.Post{
				Page:        content.Page{Slug: "blog/post-" + strconv.Itoa(i), Title: "Post " + strconv.Itoa(i)},
				Feed:        content.FeedBlog,
				PublishedAt: base.Add(time.Duration(i) * 24 * time.Hour),
			})
		}
		seedPost(t, s, content.Post{
			Page:        content.Page{Slug: "news/item", Title: "News"},
			Feed:        content.FeedNews,
			PublishedAt: base.Add(100 * 24 * time.Hour),
		})

		slugsOf := func(posts []content.Post) []string {
			out := make([]string, len(posts))
			for i, p := range posts {
				out[i] = p.Slug
			}
			return out
		}
		page := func(limit, offset int) []string {
			t.Helper()
			posts, err := s.PostsPage(ctx, content.FeedBlog, defaultLocale, false, limit, offset)
			if err != nil {
				t.Fatalf("PostsPage(%d, %d): %v", limit, offset, err)
			}
			return slugsOf(posts)
		}

		// Newest first, three to a page, and the offset slides the window
		// without repeating or skipping a post.
		if want := []string{"blog/post-6", "blog/post-5", "blog/post-4"}; !slices.Equal(page(3, 0), want) {
			t.Errorf("page 1 = %v, want %v", page(3, 0), want)
		}
		if want := []string{"blog/post-3", "blog/post-2", "blog/post-1"}; !slices.Equal(page(3, 3), want) {
			t.Errorf("page 2 = %v, want %v", page(3, 3), want)
		}
		// The last page is short, and past the end there is nothing.
		if want := []string{"blog/post-0"}; !slices.Equal(page(3, 6), want) {
			t.Errorf("page 3 = %v, want %v", page(3, 6), want)
		}
		if got := page(3, 9); len(got) != 0 {
			t.Errorf("page past the end = %v, want none", got)
		}
		// Every page together is every post, in order and once each.
		var walked []string
		for off := 0; off < 7; off += 3 {
			walked = append(walked, page(3, off)...)
		}
		if want := []string{"blog/post-6", "blog/post-5", "blog/post-4", "blog/post-3",
			"blog/post-2", "blog/post-1", "blog/post-0"}; !slices.Equal(walked, want) {
			t.Errorf("walking every page = %v, want %v", walked, want)
		}
		// An offset without a limit is meaningless and ignored.
		if got := page(0, 3); len(got) != 7 {
			t.Errorf("offset without limit returned %d posts, want all 7", len(got))
		}

		// Counts match what the pages hold, per feed and overall.
		count := func(feed content.Feed, publishedOnly bool) int {
			t.Helper()
			n, err := s.CountPosts(ctx, feed, publishedOnly)
			if err != nil {
				t.Fatalf("CountPosts(%q, %v): %v", feed, publishedOnly, err)
			}
			return n
		}
		if got := count(content.FeedBlog, false); got != 7 {
			t.Errorf("CountPosts(blog) = %d, want 7", got)
		}
		if got := count(content.FeedNews, false); got != 1 {
			t.Errorf("CountPosts(news) = %d, want 1", got)
		}
		if got := count("", false); got != 8 {
			t.Errorf("CountPosts(all) = %d, want 8", got)
		}
		// Nothing is published yet, so the public count is zero...
		if got := count(content.FeedBlog, true); got != 0 {
			t.Errorf("CountPosts(blog, published) = %d, want 0 while all are drafts", got)
		}
		// ...and publishing two, one of them private, counts only the one
		// the public listing would show — the same filter the page uses.
		published := []string{"blog/post-6", "blog/post-5"}
		for _, slug := range published {
			p, err := s.GetBySlug(ctx, slug, defaultLocale, false)
			if err != nil {
				t.Fatalf("GetBySlug(%q): %v", slug, err)
			}
			if err := s.Publish(ctx, p.ID); err != nil {
				t.Fatalf("Publish(%q): %v", slug, err)
			}
			if slug == "blog/post-5" {
				if err := s.SetVisibility(ctx, p.ID, content.VisibilityPrivate); err != nil {
					t.Fatalf("SetVisibility(%q): %v", slug, err)
				}
			}
		}
		if got := count(content.FeedBlog, true); got != 1 {
			t.Errorf("CountPosts(blog, published) = %d, want 1", got)
		}
		pub, err := s.PostsPage(ctx, content.FeedBlog, defaultLocale, true, 3, 0)
		if err != nil {
			t.Fatalf("PostsPage(published): %v", err)
		}
		if want := []string{"blog/post-6"}; !slices.Equal(slugsOf(pub), want) {
			t.Errorf("public page 1 = %v, want %v", slugsOf(pub), want)
		}
	})
}

func TestPostAllNonPostExcludesPosts(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		seedPage(t, s, content.Page{Slug: "about"}, defaultLocale)
		seedPage(t, s, content.Page{Slug: "contact"}, defaultLocale)
		seedPost(t, s, content.Post{Page: content.Page{Slug: "blog/post", Title: "Post"}})

		pages, err := s.AllNonPost(ctx, defaultLocale)
		if err != nil {
			t.Fatalf("AllNonPost: %v", err)
		}
		var got []string
		for _, p := range pages {
			got = append(got, p.Slug)
		}
		want := []string{"about", "contact"}
		if len(got) != len(want) {
			t.Fatalf("AllNonPost = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("AllNonPost = %v, want %v", got, want)
			}
		}
	})
}

// The admin Pages list's window and count. Posts' backing pages must stay
// out of both, or the page count promises rows the table will not show.
func TestAllNonPostPageWindowsAndCounts(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		// Seven ordinary pages, named so slug order is predictable, plus
		// two posts whose backing pages must not appear.
		for i := range 7 {
			seedPage(t, s, content.Page{Slug: "page-" + strconv.Itoa(i)}, defaultLocale)
		}
		seedPost(t, s, content.Post{Page: content.Page{Slug: "blog/b", Title: "B"}, Feed: content.FeedBlog})
		seedPost(t, s, content.Post{Page: content.Page{Slug: "news/n", Title: "N"}, Feed: content.FeedNews})

		page := func(limit, offset int) []string {
			t.Helper()
			pages, err := s.AllNonPostPage(ctx, defaultLocale, limit, offset)
			if err != nil {
				t.Fatalf("AllNonPostPage(%d, %d): %v", limit, offset, err)
			}
			out := make([]string, len(pages))
			for i, p := range pages {
				out[i] = p.Slug
			}
			return out
		}

		// Slug order, three to a page, the window sliding cleanly.
		if want := []string{"page-0", "page-1", "page-2"}; !slices.Equal(page(3, 0), want) {
			t.Errorf("page 1 = %v, want %v", page(3, 0), want)
		}
		if want := []string{"page-3", "page-4", "page-5"}; !slices.Equal(page(3, 3), want) {
			t.Errorf("page 2 = %v, want %v", page(3, 3), want)
		}
		if want := []string{"page-6"}; !slices.Equal(page(3, 6), want) {
			t.Errorf("page 3 = %v, want %v", page(3, 6), want)
		}
		if got := page(3, 9); len(got) != 0 {
			t.Errorf("page past the end = %v, want none", got)
		}
		// Every page together is every page, once each and in order — and
		// no post's backing page among them.
		var walked []string
		for off := 0; off < 7; off += 3 {
			walked = append(walked, page(3, off)...)
		}
		want := []string{"page-0", "page-1", "page-2", "page-3", "page-4", "page-5", "page-6"}
		if !slices.Equal(walked, want) {
			t.Errorf("walking every page = %v, want %v", walked, want)
		}
		// An offset without a limit is meaningless and ignored.
		if got := page(0, 3); len(got) != 7 {
			t.Errorf("offset without limit returned %d pages, want all 7", len(got))
		}

		// The count matches, and likewise excludes the posts.
		n, err := s.CountNonPost(ctx)
		if err != nil {
			t.Fatalf("CountNonPost: %v", err)
		}
		if n != 7 {
			t.Errorf("CountNonPost = %d, want 7 (posts excluded)", n)
		}
		// AllNonPost keeps its old meaning: everything, unwindowed.
		all, err := s.AllNonPost(ctx, defaultLocale)
		if err != nil {
			t.Fatalf("AllNonPost: %v", err)
		}
		if len(all) != n {
			t.Errorf("AllNonPost returned %d pages but CountNonPost said %d", len(all), n)
		}
	})
}

func TestPostDeletingPageDeletesPost(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		post := seedPost(t, s, content.Post{Page: content.Page{Slug: "blog/doomed", Title: "Doomed"}})

		if err := s.Delete(ctx, post.ID); err != nil {
			t.Fatalf("Delete(backing page): %v", err)
		}
		if _, err := s.PostByID(ctx, post.PostID, defaultLocale); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("PostByID after deleting the page = %v, want ErrNotFound", err)
		}
	})
}

// seedImage inserts a cms_media row directly — enough for the post joins to
// have something to resolve, without standing up an object store.
func seedImage(t *testing.T, db *sqldb.DB, storeKey, filename string, w, h int) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := db.InsertID(ctx, `
		INSERT INTO cms_media (kind, store_key, filename, mime, ext, variant_ext, width, height, size, created_at)
		VALUES ('image', $1, $2, 'image/jpeg', '.jpg', '.webp', $3, $4, 1024, now())`,
		storeKey, filename, w, h)
	if err != nil {
		t.Fatalf("seeding media %q: %v", filename, err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO cms_media_meta (media_id, locale, alt_text) VALUES ($1, $2, $3)`,
		id, defaultLocale, "Alt for "+filename); err != nil {
		t.Fatalf("seeding alt text: %v", err)
	}
	return id
}

// A post's thumbnail is a library reference, and reads join enough of the
// media row for the renderer to build every rendition from it.
func TestPostImagesJoinTheLibrary(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		thumbID := seedImage(t, db, "aaaaaaaaaaaaaaaaaaaaaaaa", "card.jpg", 2000, 1000)

		post := seedPost(t, s, content.Post{
			Page:             content.Page{Slug: "blog/with-images", Title: "With images"},
			ThumbnailMediaID: &thumbID,
		})

		got, err := s.PostByID(ctx, post.PostID, defaultLocale)
		if err != nil {
			t.Fatalf("PostByID: %v", err)
		}
		if got.ThumbnailMediaIDValue() != thumbID {
			t.Fatalf("media id = %d, want %d", got.ThumbnailMediaIDValue(), thumbID)
		}
		if got.Thumbnail == nil {
			t.Fatal("the joined media record did not come back")
		}
		if got.Thumbnail.StoreKey != "aaaaaaaaaaaaaaaaaaaaaaaa" || got.Thumbnail.VariantExt != ".webp" {
			t.Errorf("thumbnail joined %q/%q, want the seeded key and variant ext",
				got.Thumbnail.StoreKey, got.Thumbnail.VariantExt)
		}
		if got.Thumbnail.Width != 2000 || got.Thumbnail.Height != 1000 {
			t.Errorf("thumbnail dimensions = %dx%d, want 2000x1000",
				got.Thumbnail.Width, got.Thumbnail.Height)
		}
		if got.Thumbnail.Alt != "Alt for card.jpg" {
			t.Errorf("thumbnail alt = %q, want the library's", got.Thumbnail.Alt)
		}
		// A listing read walks the same joins.
		posts, err := s.Posts(ctx, content.FeedBlog, defaultLocale, false, 0)
		if err != nil {
			t.Fatalf("Posts: %v", err)
		}
		if len(posts) != 1 || posts[0].Thumbnail == nil {
			t.Fatalf("listing did not join the thumbnail: %+v", posts)
		}
	})
}

// A post with no images at all still scans: the joins simply miss.
func TestPostWithoutImages(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		post := seedPost(t, s, content.Post{
			Page: content.Page{Slug: "blog/plain", Title: "Plain"},
		})
		got, err := s.PostByID(ctx, post.PostID, defaultLocale)
		if err != nil {
			t.Fatalf("PostByID: %v", err)
		}
		if got.Thumbnail != nil {
			t.Error("a post with no images came back with joined media")
		}
		if got.ThumbnailMediaID != nil {
			t.Error("a post with no images came back with a media id")
		}
	})
}

// Deleting a library image used to leave posts pointing at a dead URL. The
// foreign key now clears the reference instead.
func TestDeletingMediaClearsPostImages(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		id := seedImage(t, db, "cccccccccccccccccccccccc", "doomed.jpg", 800, 600)
		post := seedPost(t, s, content.Post{
			Page:             content.Page{Slug: "blog/doomed", Title: "Doomed"},
			ThumbnailMediaID: &id,
		})

		if _, err := db.Exec(ctx, "DELETE FROM cms_media WHERE id = $1", id); err != nil {
			t.Fatalf("deleting media: %v", err)
		}
		got, err := s.PostByID(ctx, post.PostID, defaultLocale)
		if err != nil {
			t.Fatalf("PostByID: %v", err)
		}
		if got.ThumbnailMediaID != nil || got.Thumbnail != nil {
			t.Errorf("the post still references the deleted image: %v", got.ThumbnailMediaID)
		}
	})
}
