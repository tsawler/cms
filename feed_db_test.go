package cms

// /blog/rss.xml and /news/rss.xml. A feed is read by machines that will
// not tolerate being nearly right — a link that resolves to nothing, a
// date in the wrong format, a GUID that moves between fetches and turns
// one post into a new one every time — so what these check is mostly the
// shape of what goes out the door.

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

var feedTestFS = fstest.MapFS{
	"templates/base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><body>{{block "content" .}}{{end}}</body></html>{{end}}`)},
	"templates/pages/standard.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}{{cmsRegion "main"}}{{end}}`)},
	"templates/pages/post.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}{{cmsSections "main"}}{{end}}`)},
}

// newFeedTestCMS builds a CMS with blog & news on unless posts is false,
// which is how a host turns the feature off — the feed addresses go with
// it.
func newFeedTestCMS(t *testing.T, db *sqldb.DB, posts bool, locales ...string) *CMS {
	t.Helper()
	cfg := Config{
		DB:              db.SQL(),
		Dialect:         db.Dialect().Name(),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		TemplateFS:      feedTestFS,
		SharedTemplates: []string{"templates/base.gohtml"},
		PageTemplates: []PageTemplate{
			{File: "templates/pages/standard.gohtml", Label: "Standard page"},
		},
		Locales: locales,
	}
	if posts {
		cfg.PostTemplate = PageTemplate{File: "templates/pages/post.gohtml", Label: "Post"}
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("cms.New: %v", err)
	}
	if err := c.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return c
}

// publishPost adds a post and publishes it, which is what puts it in the
// feed. An unpublished one is seeded the same way minus the publish.
func publishPost(t *testing.T, c *CMS, feed content.Feed, tail, title, summary string, when time.Time, publish bool) *content.Post {
	t.Helper()
	ctx := context.Background()
	p := &content.Post{Feed: feed, PublishedAt: when}
	p.Title = title
	p.Description = summary
	p.Slug = string(feed) + "/" + tail
	p.TemplateName = "templates/pages/post.gohtml"
	if _, err := c.content.InsertPost(ctx, p, "en"); err != nil {
		t.Fatalf("InsertPost(%q): %v", tail, err)
	}
	if publish {
		if err := c.content.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish(%q): %v", tail, err)
		}
	}
	return p
}

// fetchFeed drives the request through the real Handler and decodes the
// RSS, which is the only assertion that means anything: a feed reader
// parses it or it does not.
func fetchFeed(t *testing.T, c *CMS, path string) (*httptest.ResponseRecorder, rssDoc) {
	t.Helper()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://example.test"+path, nil)
	c.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200 (body %q)", path, rec.Code, rec.Body.String())
	}
	var doc rssDoc
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the feed is not parseable XML: %v\n%s", err, rec.Body.String())
	}
	return rec, doc
}

func TestServeFeed(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newFeedTestCMS(t, db, true)
		when := time.Date(2026, 3, 4, 15, 30, 0, 0, time.UTC)
		publishPost(t, c, content.FeedBlog, "first-post", "First Post", "A summary", when, true)

		rec, doc := fetchFeed(t, c, "/blog/rss.xml")

		if ct := rec.Header().Get("Content-Type"); ct != "application/rss+xml; charset=utf-8" {
			t.Errorf("Content-Type = %q, want the RSS type", ct)
		}
		// The XML declaration comes before the document, or strict
		// parsers refuse it.
		if !strings.HasPrefix(rec.Body.String(), xml.Header) {
			t.Errorf("the feed does not start with an XML declaration: %q", rec.Body.String()[:40])
		}
		if doc.Version != "2.0" {
			t.Errorf("rss version = %q, want 2.0", doc.Version)
		}
		if len(doc.Channel.Items) != 1 {
			t.Fatalf("the feed carries %d items, want 1", len(doc.Channel.Items))
		}

		item := doc.Channel.Items[0]
		if item.Title != "First Post" {
			t.Errorf("title = %q, want the post's", item.Title)
		}
		if item.Description != "A summary" {
			t.Errorf("description = %q, want the post's summary", item.Description)
		}
		// Absolute, because a feed is read away from the site.
		if want := "http://example.test/blog/first-post"; item.Link != want {
			t.Errorf("link = %q, want %q", item.Link, want)
		}
		// A GUID that moved between fetches would republish the post as a
		// new one every time.
		if item.GUID != item.Link {
			t.Errorf("guid = %q, want it to match the link %q", item.GUID, item.Link)
		}
		// RFC1123Z is the format RSS requires; the zone it is rendered in
		// is the server's, so the assertion is on the instant.
		at, err := time.Parse(time.RFC1123Z, item.PubDate)
		if err != nil {
			t.Errorf("pubDate = %q, want an RFC1123Z date: %v", item.PubDate, err)
		} else if !at.Equal(when) {
			t.Errorf("pubDate = %v, want %v", at.UTC(), when)
		}
		if !strings.HasPrefix(doc.Channel.Link, "http://example.test/") {
			t.Errorf("channel link = %q, want an absolute URL", doc.Channel.Link)
		}
	})
}

// The feed describes itself the way the site does: when the listing page
// at the feed's own address is published, its title and summary become the
// channel's. Without one there is still a title, because a channel with
// none is not a feed.
func TestFeedChannelDescribesItselfFromTheListingPage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newFeedTestCMS(t, db, true)
		ctx := context.Background()
		publishPost(t, c, content.FeedBlog, "a-post", "A Post", "", time.Now(), true)

		_, doc := fetchFeed(t, c, "/blog/rss.xml")
		if doc.Channel.Title == "" {
			t.Error("no channel title without a listing page")
		}
		if doc.Channel.Description != "" {
			t.Errorf("channel description = %q, want none without a listing page", doc.Channel.Description)
		}

		listing := &content.Page{
			Slug: "blog", Title: "The Kraken Chronicle",
			Description:  "Dispatches from the deep",
			TemplateName: "templates/pages/standard.gohtml",
		}
		id, err := c.content.Insert(ctx, listing, "en")
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}

		// A draft listing page is not what the site shows, so it is not
		// what the feed says either.
		_, doc = fetchFeed(t, c, "/blog/rss.xml")
		if doc.Channel.Title == "The Kraken Chronicle" {
			t.Error("the channel took its title from an unpublished listing page")
		}

		if err := c.content.Publish(ctx, id); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		_, doc = fetchFeed(t, c, "/blog/rss.xml")
		if doc.Channel.Title != "The Kraken Chronicle" {
			t.Errorf("channel title = %q, want the listing page's", doc.Channel.Title)
		}
		if doc.Channel.Description != "Dispatches from the deep" {
			t.Errorf("channel description = %q, want the listing page's", doc.Channel.Description)
		}
	})
}

// A feed carries what the site serves: published posts of that feed, and
// nothing else. A draft in a feed would publish it to every subscriber.
func TestFeedCarriesOnlyItsOwnPublishedPosts(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newFeedTestCMS(t, db, true)
		now := time.Now()
		publishPost(t, c, content.FeedBlog, "live", "Live blog post", "", now, true)
		publishPost(t, c, content.FeedBlog, "draft", "Draft blog post", "", now, false)
		publishPost(t, c, content.FeedNews, "newsy", "Live news post", "", now, true)

		_, blog := fetchFeed(t, c, "/blog/rss.xml")
		titles := feedTitles(blog)
		if len(titles) != 1 || titles[0] != "Live blog post" {
			t.Errorf("blog feed = %v, want only the published blog post", titles)
		}

		_, news := fetchFeed(t, c, "/news/rss.xml")
		titles = feedTitles(news)
		if len(titles) != 1 || titles[0] != "Live news post" {
			t.Errorf("news feed = %v, want only the published news post", titles)
		}
	})
}

func feedTitles(doc rssDoc) []string {
	out := make([]string, 0, len(doc.Channel.Items))
	for _, item := range doc.Channel.Items {
		out = append(out, item.Title)
	}
	return out
}

// Newest first, and capped: a feed reader wants the recent past, not the
// archive, and an unbounded feed grows until it is refused.
func TestFeedIsNewestFirstAndCappedAtTwenty(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newFeedTestCMS(t, db, true)
		base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		for i := range 25 {
			publishPost(t, c, content.FeedBlog,
				"post-"+strings.Repeat("x", i%3)+itoaFeed(i),
				"Post "+itoaFeed(i), "", base.Add(time.Duration(i)*time.Hour), true)
		}

		_, doc := fetchFeed(t, c, "/blog/rss.xml")
		if len(doc.Channel.Items) != 20 {
			t.Fatalf("the feed carries %d items, want 20", len(doc.Channel.Items))
		}
		if got := doc.Channel.Items[0].Title; got != "Post 24" {
			t.Errorf("first item = %q, want the newest post", got)
		}
		var prev time.Time
		for i, item := range doc.Channel.Items {
			at, err := time.Parse(time.RFC1123Z, item.PubDate)
			if err != nil {
				t.Fatalf("item %d has an unparseable date %q: %v", i, item.PubDate, err)
			}
			if i > 0 && at.After(prev) {
				t.Errorf("item %d (%v) is newer than the one before it (%v)", i, at, prev)
			}
			prev = at
		}
	})
}

func itoaFeed(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// A localized feed lives under its language prefix and its links stay
// inside it, or every item would send the reader to the default language.
func TestLocalizedFeedKeepsItsLanguagePrefix(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newFeedTestCMS(t, db, true, "en", "fr")
		publishPost(t, c, content.FeedBlog, "bonjour", "Bonjour", "", time.Now(), true)

		_, doc := fetchFeed(t, c, "/fr/blog/rss.xml")
		if len(doc.Channel.Items) != 1 {
			t.Fatalf("the French feed carries %d items, want 1", len(doc.Channel.Items))
		}
		if want := "http://example.test/fr/blog/bonjour"; doc.Channel.Items[0].Link != want {
			t.Errorf("link = %q, want %q", doc.Channel.Items[0].Link, want)
		}
		if !strings.HasPrefix(doc.Channel.Link, "http://example.test/fr/") {
			t.Errorf("channel link = %q, want it under the language prefix", doc.Channel.Link)
		}
	})
}

// Config.SiteURL is the canonical public address, and it has to win: a
// proxy that rewrites Host, or an admin reached by a different name than
// the site, both make the request a bad guess — and a feed's links are
// exactly where a bad guess ends up in somebody's reader.
func TestFeedLinksUseTheConfiguredSiteURL(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newFeedTestCMS(t, db, true)
		c.cfg.SiteURL = "https://kraken.example"
		publishPost(t, c, content.FeedBlog, "canonical", "Canonical", "", time.Now(), true)

		_, doc := fetchFeed(t, c, "/blog/rss.xml")
		if want := "https://kraken.example/blog/canonical"; doc.Channel.Items[0].Link != want {
			t.Errorf("link = %q, want %q", doc.Channel.Items[0].Link, want)
		}
	})
}

// Only the two real feeds have addresses. Anything else shaped like one is
// an ordinary page lookup, which is what leaves the address free.
func TestOnlyRealFeedsHaveAddresses(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newFeedTestCMS(t, db, true)

		for _, path := range []string{"/gossip/rss.xml", "/rss.xml", "/blog/atom.xml"} {
			rec := httptest.NewRecorder()
			c.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: status = %d, want 404", path, rec.Code)
			}
		}
	})
}

// Blog & news off takes the feed addresses with it: without a post
// template there are no posts to syndicate, and the address goes back to
// the host application.
func TestFeedsAreUnclaimedWithoutAPostTemplate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newFeedTestCMS(t, db, false)
		if c.postsEnabled() {
			t.Fatal("posts are enabled without a post template")
		}

		rec := httptest.NewRecorder()
		c.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/blog/rss.xml", nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}

// A post's title and summary are typed by a person and go into XML
// unescaped at their peril: one ampersand in a headline breaks the whole
// document for every reader subscribed to it.
func TestFeedEscapesPostText(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newFeedTestCMS(t, db, true)
		publishPost(t, c, content.FeedBlog, "ampersands",
			"Salt & Pepper <tags>", "Sea & sky", time.Now(), true)

		rec, doc := fetchFeed(t, c, "/blog/rss.xml")
		if strings.Contains(rec.Body.String(), "Salt & Pepper") {
			t.Error("an ampersand went into the feed unescaped")
		}
		// It survives the round trip, which is the other half: escaped,
		// not mangled.
		if got := doc.Channel.Items[0].Title; got != "Salt & Pepper <tags>" {
			t.Errorf("title = %q, want it to decode back to what was typed", got)
		}
		if got := doc.Channel.Items[0].Description; got != "Sea & sky" {
			t.Errorf("description = %q, want it to decode back", got)
		}
	})
}
