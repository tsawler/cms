package cms

import (
	"context"
	"net/http"
	"strings"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/render"
)

// DefaultSearchPath is where results are served when Config.SearchPath
// says nothing. A slug, not a path: it is routed exactly like a page, so
// it picks up locale prefixes for free — /search and /fr/search are the
// same handler.
const DefaultSearchPath = "search"

// searchEnabled reports whether site search is active: it needs page
// rendering and a configured results template, the same pair blog & news
// needs.
func (c *CMS) searchEnabled() bool {
	return c.renderer != nil && c.cfg.SearchTemplate.File != ""
}

// searchPathFor is the site-relative address of the results page in one
// locale, or "" when search is off. It is what the nav's magnifying glass
// links to and what the search forms submit to, so a visitor reading the
// French site searches it and stays in French.
func (c *CMS) searchPathFor(locale string) string {
	if !c.searchEnabled() {
		return ""
	}
	return render.LocalePrefix(locale, c.cfg.Locales[0]) + "/" + c.cfg.SearchPath
}

// searchLister is the {{cmsSearch}} data source for one render.
//
// Nothing here filters by publication state, and nothing should: the
// index holds documents only for pages the site serves to everyone, so
// the filtering happened when the page was published. Editors are shown
// the same results as visitors for the same reason — a draft has no
// document to find, so there is nothing extra to offer them.
func (c *CMS) searchLister(ctx context.Context, locale string) render.SearchLister {
	if !c.searchEnabled() {
		return nil
	}
	prefix := render.LocalePrefix(locale, c.cfg.Locales[0])
	return func(q string, limit, offset int, count bool) ([]render.SearchHit, int) {
		terms := content.ParseSearchQuery(q)
		if len(terms) == 0 {
			return nil, 0
		}
		total := 0
		if count {
			n, err := c.content.CountSearch(ctx, terms, locale)
			if err != nil {
				c.cfg.Logger.Error("cms: counting search results", "err", err)
				return nil, 0
			}
			total = n
		}
		results, err := c.content.Search(ctx, terms, locale, limit, offset)
		if err != nil {
			c.cfg.Logger.Error("cms: running site search", "err", err)
			return nil, 0
		}
		hits := make([]render.SearchHit, 0, len(results))
		for _, r := range results {
			hit := render.SearchHit{
				Kind:    r.Kind,
				IsPost:  r.Kind != "page",
				Title:   r.Title,
				Summary: r.Summary,
				Snippet: content.Snippet(r.Body, terms, 0),
				URL:     pageSlugURL(prefix, r.Slug),
			}
			if r.PublishedAt != nil {
				hit.PublishedAt = *r.PublishedAt
			}
			hits = append(hits, hit)
		}
		return hits, total
	}
}

// pageSlugURL is a slug's site-relative address under a locale prefix. The
// home page is the prefix alone, or "/" when there is none — the same rule
// render.localeURL follows for menu links.
func pageSlugURL(prefix, slug string) string {
	if slug == "" {
		if prefix == "" {
			return "/"
		}
		return prefix
	}
	return prefix + "/" + slug
}

// serveSearch renders the results page.
//
// The page it hands the renderer is synthesized rather than stored, which
// is the one liberty this route takes. A results page has no content of
// its own — it is a template plus a query — and giving it a row in
// cms_pages would mean a page in the admin's list that nobody may edit,
// delete, or unpublish without breaking the address. The cost is that
// {{cmsRegion}} and friends have nothing to save to on this template,
// which Config.SearchTemplate says out loud.
//
// The title is deliberately plain and untranslated ("Search"). It is the
// <title> of a page that exists to hold a box, and a host that wants
// otherwise writes it in the template, where {{cmsSearch}}'s .Query is to
// hand.
func (c *CMS) serveSearch(w http.ResponseWriter, r *http.Request, locale string) {
	query := strings.TrimSpace(r.URL.Query().Get(render.SearchQueryParam))

	site, err := c.content.SiteSettings(r.Context())
	if err != nil {
		// Like every other render: settings failing shouldn't take the
		// page down.
		c.cfg.Logger.Error("cms: loading site settings", "err", err)
	}
	menuItems, err := c.content.MenuItems(r.Context(), "")
	if err != nil {
		c.cfg.Logger.Error("cms: loading menus", "err", err)
		menuItems = nil
	}
	// The results page is not in any menu, so nothing on the bar is
	// current while a visitor is on it — which is what passing a slug no
	// page has says.
	menus := render.BuildMenus(menuItems, c.cfg.SearchPath, locale, c.cfg.Locales[0], false)

	// The site's shared regions — the footer and the notice bar the
	// results page draws like every other page. It has no blocks of its
	// own: there is no stored page here for anyone to have filled.
	shared, err := c.content.SharedBlocks(r.Context(), locale, content.StatusPublished)
	if err != nil {
		c.cfg.Logger.Error("cms: loading shared blocks for search", "err", err)
		shared = nil
	}

	page := &content.Page{
		Slug:         c.cfg.SearchPath,
		TemplateName: c.cfg.SearchTemplate.File,
		Status:       content.StatusPublished,
		Visibility:   content.VisibilityPublic,
		Title:        "Search",
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// A results page is a different page for every query and worth
	// nothing in an index — it is the site's own content that should be
	// found, not a page listing it. The header is the half that reaches
	// a crawler whatever the template says.
	w.Header().Set("X-Robots-Tag", "noindex, follow")
	if err := c.renderer.Render(w, render.Input{
		Page:          page,
		Shared:        shared,
		Locale:        locale,
		Menus:         menus,
		Posts:         c.postLister(r.Context(), locale, false),
		Search:        c.searchLister(r.Context(), locale),
		SearchQuery:   query,
		SearchPath:    c.searchPathFor(locale),
		SearchPerPage: c.cfg.SearchPerPage,
		Locales:       c.cfg.Locales,
		BaseURL:       c.siteBaseURL(r),
		Site:          site,
		AdminPath:     c.cfg.AdminPath,
		CodeSnippets:  c.codeLookup(r.Context()),
		PostsPerPage:  c.cfg.PostsPerPage,
		PageNumber:    listingPage(r),
		PageURL:       listingPageURL(r),
		Funcs:         c.requestFuncs(r),
	}); err != nil {
		c.cfg.Logger.Error("cms: rendering search results", "err", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
	}
}

// ReindexSearch rebuilds the site search index from every published,
// public page, and reports how many it visited. It is safe to run at any
// time and safe to run twice.
//
// Ordinary operation never needs it: publishing a page indexes it, and
// unpublishing or hiding one takes it back out, both inside the
// transaction that did it. Two moments do — an install that had content
// before it had a search index, and an upgrade that changes how text is
// extracted — and neither can be done by a SQL migration, since the
// extraction is Go.
//
// The first of those is handled without anyone having to know: Handler
// runs this once, in the background, when the index is empty and the site
// has published pages. This is exported for the second, and for a host
// that would rather do it on its own schedule.
func (c *CMS) ReindexSearch(ctx context.Context) (int, error) {
	return c.content.ReindexAll(ctx)
}

// backfillSearchIndex builds the index once on a site that has content and
// no index — an install upgrading into this feature.
//
// It runs in the background off the first request rather than from New,
// because it walks every published page and a host application's startup
// should not wait on that. It runs at most once per process, and a second
// process doing it at the same time is harmless: each page is written by a
// single statement replacing what was there.
//
// A site that is genuinely empty takes the same path and finishes
// immediately, which is why "the index is empty" is a good enough test —
// the alternative, a marker row saying it has been done, would be a piece
// of state to keep true.
func (c *CMS) backfillSearchIndex() {
	go func() {
		ctx := context.Background()
		empty, err := c.content.SearchIndexEmpty(ctx)
		if err != nil {
			c.cfg.Logger.Error("cms: checking the search index", "err", err)
			return
		}
		if !empty {
			return
		}
		n, err := c.content.ReindexAll(ctx)
		if err != nil {
			c.cfg.Logger.Error("cms: building the search index", "pages", n, "err", err)
			return
		}
		if n > 0 {
			c.cfg.Logger.Info("cms: built the site search index", "pages", n)
		}
	}()
}
