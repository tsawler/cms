package cms

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/render"
)

// postsEnabled reports whether blog & news is active: it needs both page
// rendering and a configured post template.
func (c *CMS) postsEnabled() bool {
	return c.renderer != nil && c.cfg.PostTemplate.File != ""
}

// postLister returns the {{cmsPosts}} data source for one render: the
// public sees published posts only, editors also see drafts (flagged, so
// listing templates can badge them). Nil when blog & news is disabled.
func (c *CMS) postLister(ctx context.Context, locale string, editing bool) render.PostLister {
	if !c.postsEnabled() {
		return nil
	}
	prefix := render.LocalePrefix(locale, c.cfg.Locales[0])
	return func(feed string, limit int) []render.PostInfo {
		if !content.ValidFeed(feed) {
			return nil
		}
		posts, err := c.content.Posts(ctx, content.Feed(feed), locale, !editing, limit)
		if err != nil {
			c.cfg.Logger.Error("cms: listing posts", "feed", feed, "err", err)
			return nil
		}
		out := make([]render.PostInfo, 0, len(posts))
		for i := range posts {
			out = append(out, *render.PostInfoFor(&posts[i], prefix))
		}
		return out
	}
}

// RSS 2.0 documents, written by serveFeed.
type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description,omitempty"`
}

// serveFeed writes the RSS feed for /blog/rss.xml or /news/rss.xml: the
// twenty newest published posts. The channel takes its title and
// description from the published listing page at the feed's slug when one
// exists, so the feed describes itself the way the site does.
func (c *CMS) serveFeed(w http.ResponseWriter, r *http.Request, feed content.Feed, locale string) {
	posts, err := c.content.Posts(r.Context(), feed, locale, true, 20)
	if err != nil {
		c.cfg.Logger.Error("cms: building rss feed", "feed", feed, "err", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}

	base := c.siteBaseURL(r) + render.LocalePrefix(locale, c.cfg.Locales[0])
	ch := rssChannel{
		Title: r.Host + " — " + string(feed),
		Link:  base + "/" + string(feed),
	}
	listing, err := c.content.GetBySlug(r.Context(), string(feed), locale, true)
	if err == nil {
		if listing.Title != "" {
			ch.Title = listing.Title
		}
		ch.Description = listing.Description
	} else if !errors.Is(err, content.ErrNotFound) {
		c.cfg.Logger.Error("cms: loading feed listing page", "feed", feed, "err", err)
	}

	for _, p := range posts {
		link := base + "/" + p.Slug
		ch.Items = append(ch.Items, rssItem{
			Title:       p.Title,
			Link:        link,
			GUID:        link,
			PubDate:     p.PublishedAt.Format(time.RFC1123Z),
			Description: p.Description,
		})
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		return
	}
	if err := xml.NewEncoder(w).Encode(rssDoc{Version: "2.0", Channel: ch}); err != nil {
		c.cfg.Logger.Debug("cms: writing rss feed interrupted", "feed", feed, "err", err)
	}
}

// siteBaseURL is the absolute base ("scheme://host", no trailing slash)
// for links that have to work away from this request: feed items,
// hreflang alternates, and the media library's copyable links.
//
// Config.SiteURL wins when the host set one — a proxy that rewrites Host,
// or an admin reached by a different name than the public site, both make
// the request a bad guess. Otherwise it comes from the request, honoring
// a proxy's X-Forwarded-Proto.
func (c *CMS) siteBaseURL(r *http.Request) string {
	if c.cfg.SiteURL != "" {
		return c.cfg.SiteURL
	}
	return requestBaseURL(r)
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// normalizeSiteURL tidies a configured Config.SiteURL: no trailing slash,
// and a bare host name ("example.com") is taken to mean https, since a
// canonical public URL essentially always is.
func normalizeSiteURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	u = strings.TrimRight(u, "/")
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	return u
}
