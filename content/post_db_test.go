package content_test

import (
	"context"
	"errors"
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
			HeaderURL:    "/img/header.png",
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
