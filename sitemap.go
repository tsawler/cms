package cms

import (
	"context"
	"encoding/xml"
	"net/http"
	"time"

	"github.com/tsawler/cms/render"
)

// sitemapPath is where the generated sitemap is served, when the site
// settings turn it on. The CMS claims the address only then, for the
// same reason it leaves /robots.txt alone by default: a host app that
// builds its own sitemap keeps answering there.
const sitemapPath = "/sitemap.xml"

// sitemapMaxURLs is the sitemap protocol's ceiling on one document —
// 50,000 <url> entries (and 50 MB uncompressed, which these never
// approach). A site past it needs a sitemap index, which is a different
// document; until one exists, the pages that fit are listed and the rest
// are logged rather than silently dropped.
const sitemapMaxURLs = 50_000

// sitemapCacheTTL is how long a rendered sitemap is reused. It exists to
// bound the cost, not to keep the document fresh: crawlers refetch on
// their own slow schedule, and a page published a few minutes ago can
// wait for the next one.
const sitemapCacheTTL = 5 * time.Minute

// The sitemap document. The xhtml namespace is what carries the
// hreflang alternates, which are only emitted on multi-locale sites —
// the same rule {{cmsHead}} follows for the <link rel="alternate"> tags
// in the page head, so the two describe the same set of URLs.
type sitemapDoc struct {
	XMLName xml.Name     `xml:"urlset"`
	NS      string       `xml:"xmlns,attr"`
	XHTML   string       `xml:"xmlns:xhtml,attr,omitempty"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string       `xml:"loc"`
	LastMod string       `xml:"lastmod,omitempty"`
	Alts    []sitemapAlt `xml:"xhtml:link,omitempty"`
}

type sitemapAlt struct {
	Rel      string `xml:"rel,attr"`
	HrefLang string `xml:"hreflang,attr"`
	Href     string `xml:"href,attr"`
}

const (
	sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"
	xhtmlNS   = "http://www.w3.org/1999/xhtml"
)

// serveSitemap answers /sitemap.xml with every published, publicly
// visible page, in every locale.
//
// The document is built from the site's base URL, which for an install
// without Config.SiteURL is the requesting host — so the cache is keyed
// by that base and a request arriving under another name rebuilds rather
// than being served someone else's hostnames.
func (c *CMS) serveSitemap(w http.ResponseWriter, r *http.Request) {
	base := c.siteBaseURL(r)

	c.sitemapMu.Lock()
	body, at := c.sitemapBody, c.sitemapAt
	if c.sitemapBase != base {
		body = nil
	}
	c.sitemapMu.Unlock()

	if body == nil || time.Since(at) >= sitemapCacheTTL {
		built, err := c.buildSitemap(r.Context(), base)
		if err != nil {
			c.cfg.Logger.Error("cms: building the sitemap", "err", err)
			// A stale copy beats an error page: the URLs in it are still
			// real, and a crawler that gets a 500 may back off for days.
			if body == nil {
				http.Error(w, "Something went wrong.", http.StatusInternalServerError)
				return
			}
		} else {
			body = built
			c.sitemapMu.Lock()
			c.sitemapBody, c.sitemapBase, c.sitemapAt = built, base, time.Now()
			c.sitemapMu.Unlock()
		}
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	// Unlike robots.txt, staleness here costs nothing: the document is a
	// hint about where to look, and crawlers reread it on their own
	// schedule anyway.
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}

// buildSitemap renders the whole document. It is built in memory rather
// than streamed: it has to be complete to be cached, and 50,000 URLs of
// the shape this emits stay comfortably inside the protocol's size cap.
func (c *CMS) buildSitemap(ctx context.Context, base string) ([]byte, error) {
	locales := c.cfg.Locales
	if len(locales) == 0 {
		locales = []string{""} // a CMS built without locales serves one
	}
	// Every page is listed once per locale, so the page ceiling is the
	// URL ceiling divided among them.
	maxPages := sitemapMaxURLs / len(locales)
	entries, err := c.content.SitemapPages(ctx, maxPages)
	if err != nil {
		return nil, err
	}
	if len(entries) == maxPages {
		c.cfg.Logger.Warn("cms: sitemap truncated at the protocol's URL limit — pages beyond it are not listed",
			"pages", maxPages, "locales", len(locales), "urls", sitemapMaxURLs)
	}

	def := locales[0]
	doc := sitemapDoc{NS: sitemapNS, URLs: make([]sitemapURL, 0, len(entries)*len(locales))}
	if len(locales) > 1 {
		doc.XHTML = xhtmlNS
	}
	for _, e := range entries {
		var alts []sitemapAlt
		if len(locales) > 1 {
			alts = make([]sitemapAlt, 0, len(locales)+1)
			for _, l := range locales {
				alts = append(alts, sitemapAlt{
					Rel: "alternate", HrefLang: l, Href: pageURL(base, e.Slug, l, def),
				})
			}
			alts = append(alts, sitemapAlt{
				Rel: "alternate", HrefLang: "x-default", Href: pageURL(base, e.Slug, def, def),
			})
		}
		var lastmod string
		if !e.UpdatedAt.IsZero() {
			lastmod = e.UpdatedAt.UTC().Format(time.RFC3339)
		}
		for _, l := range locales {
			doc.URLs = append(doc.URLs, sitemapURL{
				Loc:     pageURL(base, e.Slug, l, def),
				LastMod: lastmod,
				Alts:    alts,
			})
		}
	}

	out := make([]byte, 0, len(xml.Header)+len(doc.URLs)*128)
	out = append(out, xml.Header...)
	body, err := xml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append(out, body...), nil
}

// pageURL is a page's absolute address in one locale: the site base, the
// locale prefix every locale but the default carries, and the slug. The
// home page ("") is the base itself.
func pageURL(base, slug, locale, defaultLocale string) string {
	prefix := render.LocalePrefix(locale, defaultLocale)
	if slug == "" {
		if prefix == "" {
			return base + "/"
		}
		return base + prefix
	}
	return base + prefix + "/" + slug
}
