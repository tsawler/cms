package render

import (
	"html"
	"html/template"
	"time"
)

// SearchQueryParam is the query string key a search reads what to look for
// from. It is the one every search box on the web uses, which matters more
// than it sounds: a visitor who has bookmarked or shared a results URL, and
// a browser that offers the site as a search engine, both expect ?q=.
const SearchQueryParam = "q"

// SearchHit is one result, as a results template receives it.
type SearchHit struct {
	// Kind is "page", "blog" or "news" — enough to badge a result or to
	// group the posts apart from the rest of the site.
	Kind string
	// IsPost is that same fact as the question a template usually wants to
	// ask, since blog and news behave alike here.
	IsPost bool
	Title  string
	// Summary is the page's own description: a post's summary, an
	// ordinary page's meta description, and empty on a page with neither.
	// Prefer it to Snippet when it is there — it was written for a reader
	// deciding whether to click, which is exactly this moment.
	Summary string
	// Snippet is the words around the match, cut out of the page's text,
	// with an ellipsis on whichever side was cut. It is plain text: the
	// markup was stripped on the way into the index, so there is nothing
	// here for a template to have to trust.
	Snippet string
	// URL is site-relative and locale-prefixed, e.g. "/fr/about".
	URL string
	// PublishedAt is a post's display date; the zero time on an ordinary
	// page, so {{if not .PublishedAt.IsZero}} is the test.
	PublishedAt time.Time
}

// SearchLister supplies one window of results for a query, best match
// first. offset skips that many from the top; a non-positive limit means
// no limit. total is how many results there are in all, but only when
// count is set — the pager needs it and nothing else does. Nil disables
// {{cmsSearch}}.
type SearchLister func(query string, limit, offset int, count bool) (hits []SearchHit, total int)

// DefaultSearchPerPage is how many results one page shows when the host
// says nothing. Ten, like a post listing and like every search engine a
// visitor has used.
const DefaultSearchPerPage = 10

// SearchPage is one page of results — what {{cmsSearch}} returns. The
// embedded Pager reaches the rest, so a results template can draw its own
// pagination or hand the whole thing to {{cmsPagination}} like a feed.
type SearchPage struct {
	// Query is what the visitor typed, for echoing back above the results
	// ("3 results for …") and into the search box.
	Query string
	Hits  []SearchHit
	Pager
}

// Searched reports whether there was a query to run at all, which is the
// difference between "no results" and "nothing asked yet" — two pages that
// should not say the same thing. Safe on a nil *SearchPage.
func (s *SearchPage) Searched() bool { return s != nil && s.Query != "" }

// HasPages shadows the embedded Pager's so it stays safe on the nil
// *SearchPage a render without search yields.
func (s *SearchPage) HasPages() bool { return s != nil && s.TotalPages > 1 }

// searchPage assembles {{cmsSearch}}'s answer, the way feedPage does for a
// post listing and for the same reasons: the window and the count come
// from one lister call, so an ordinary results page costs one round of
// queries, and only a ?page= past the end — a stale link, a crawler
// counting up — pays for a second, landing on the last real page rather
// than on the empty window it asked for.
func searchPage(in Input, perPage []int) *SearchPage {
	if in.Search == nil {
		return nil
	}
	size := in.SearchPerPage
	if len(perPage) > 0 && perPage[0] > 0 {
		size = perPage[0]
	}
	if size <= 0 {
		size = DefaultSearchPerPage
	}
	url := func(n int) string { return listingURL(in, n) }
	out := &SearchPage{Query: in.SearchQuery}
	if in.SearchQuery == "" {
		// No query, no queries: an empty box is not a search that found
		// nothing, and it should not cost the database a round trip to
		// say so.
		out.Pager = *NewPager(1, size, 0, url)
		return out
	}
	page := max(in.PageNumber, 1)
	hits, total := in.Search(in.SearchQuery, size, (page-1)*size, true)
	out.Pager = *NewPager(page, size, total, url)
	if out.Page != page {
		hits, _ = in.Search(in.SearchQuery, size, out.Offset(), false)
	}
	out.Hits = hits
	return out
}

// searchIcon is the magnifying glass on the nav's search toggle. Inline
// SVG rather than an emoji or a font: it inherits currentColor, so it is
// legible on whatever the host's header is, and it does not depend on the
// visitor having a glyph for it.
const searchIcon = `<svg class="cms-nav-search-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">` +
	`<path d="M10 2a8 8 0 105.3 14l5.4 5.4 1.4-1.4-5.4-5.4A8 8 0 0010 2zm0 2a6 6 0 110 12 6 6 0 010-12z"/></svg>`

// searchNavItem renders the nav's search control: a magnifying glass that
// opens a panel holding a real search form.
//
// The toggle is a link to the results page, not a button, and that is the
// whole of the no-JavaScript story. With the script running it is a
// disclosure — the click is intercepted, the panel opens, the box takes
// focus. Without it, it is what it says it is, and the visitor lands on a
// search page that has a box of its own. A <button> here would have been
// the tidier disclosure widget and would have done nothing at all when the
// script failed to load.
//
// The panel lives inside the <li> rather than beside the list: it then
// drops directly under the icon on a wide screen and flows in place inside
// the burger panel on a narrow one, which is the same arrangement
// .cms-nav-sub uses for dropdowns.
//
// The <li> deliberately does not carry .cms-nav-item, which every real
// menu item has. That class is how the in-place editor recognizes
// something as a menu entry — to open its settings modal, to drag it, to
// count positions — and this is not one. The editor's own admin-tools
// button sits in the same list on the same terms.
//
// on is the site setting. A public render is only given the item at all
// when it is set, but an edit render always carries it — switched off, it
// is emitted hidden, so that turning the setting on in the site-settings
// dialog reveals markup the server built rather than markup the editor
// had to reinvent. That is the same problem menus have (editor/src/menu.js
// mirrors navHTML), solved by not having it.
func searchNavItem(path, query string, on bool) string {
	if path == "" {
		return ""
	}
	cls := "cms-nav-search"
	if !on {
		cls += " cms-nav-search-off"
	}
	esc := html.EscapeString(path)
	return `<li class="` + cls + `">` +
		`<a class="cms-nav-link cms-nav-search-toggle" href="` + esc + `"` +
		` aria-expanded="false" aria-controls="cms-nav-search-panel"` +
		` aria-label="Search" title="Search">` + searchIcon + `</a>` +
		`<div class="cms-nav-search-panel" id="cms-nav-search-panel">` +
		searchFormMarkup(path, query, "cms-search cms-nav-search-form") +
		`</div></li>`
}

// SearchFormHTML renders {{cmsSearchForm}}: the search box on its own, for
// a results template or for a site that wants one somewhere the nav is
// not. Same markup and same classes as the one in the nav panel, so a host
// stylesheet written for either styles both.
func SearchFormHTML(path, query string) template.HTML {
	if path == "" {
		return ""
	}
	return template.HTML(searchFormMarkup(path, query, "cms-search"))
}

// searchFormMarkup is the form both callers emit. A GET form, because a
// search is a request for a page and its result should be linkable,
// bookmarkable, and reachable with the back button.
//
// type="search" rather than type="text" gives the box the browser's own
// clear button and, on a phone, a keyboard with a search key on it.
func searchFormMarkup(path, query, class string) string {
	return `<form class="` + class + `" role="search" method="get" action="` +
		html.EscapeString(path) + `">` +
		`<input class="cms-search-input" type="search" name="` + SearchQueryParam + `"` +
		` value="` + html.EscapeString(query) + `"` +
		` placeholder="Search…" aria-label="Search this site"` +
		` autocomplete="off" spellcheck="false">` +
		`<button class="cms-search-go" type="submit">Search</button>` +
		`</form>`
}

// SearchCSS is the functional minimum for the search markup: a panel that
// is hidden until the toggle opens it, and a box-and-button row inside it.
//
// It states as few colors as it can, for the reason PagerCSS gives — the
// CMS does not know the host's palette. The panel is the exception and has
// to be: it floats over the page, so it needs a background of its own or
// the words underneath show through the box. It takes the same white panel
// .cms-nav-sub uses, so a host that has restyled its dropdowns can restyle
// this to match with the rule it already wrote.
const SearchCSS =
// The item stands in for .cms-nav-item, which it deliberately does not
// carry (see searchNavItem): the panel is positioned against it.
`.cms-nav-search{position:relative;display:flex;align-items:center}` +
	// The switched-off item an edit render carries so the settings dialog
	// has something to reveal. Never emitted on a public render.
	`.cms-nav-search-off{display:none}` +
	`.cms-nav-search-toggle{display:inline-flex;align-items:center;cursor:pointer}` +
	`.cms-nav-search-icon{display:block;width:1.15em;height:1.15em;fill:currentColor}` +
	`.cms-nav-search-panel{display:none;position:absolute;top:100%;right:0;z-index:45;` +
	`box-sizing:border-box;width:min(20rem,calc(100vw - 2em));` +
	`margin-top:.4em;padding:.6em;background:#fff;color:#1a1a1a;` +
	`border:1px solid rgba(0,0,0,.12);border-radius:.5em;box-shadow:0 8px 24px rgba(0,0,0,.14)}` +
	`.cms-nav-search.cms-open>.cms-nav-search-panel{display:block}` +
	// The row itself: the box takes the space, the button takes what it
	// needs. min-width:0 is what stops a long placeholder from pushing the
	// button out of a flex row.
	`.cms-search{display:flex;gap:.4em;align-items:stretch}` +
	`.cms-search-input{flex:1 1 auto;min-width:0;box-sizing:border-box;font:inherit;color:inherit;` +
	`padding:.5em .65em;background:transparent;border:1px solid rgba(128,128,128,.5);border-radius:.3em}` +
	`.cms-search-go{flex:0 0 auto;font:inherit;color:inherit;cursor:pointer;` +
	`padding:.5em .9em;background:transparent;border:1px solid rgba(128,128,128,.5);border-radius:.3em}` +
	`.cms-search-go:hover{background:rgba(128,128,128,.12)}` +
	// Inside the burger panel the search sits in the flow like any other
	// item, rather than floating over the list it is part of.
	`@media (max-width:768px){` +
	`.cms-nav-search{display:block}` +
	`.cms-nav-search-panel{position:static;width:auto;margin:0;padding:.5em 1em .7em;` +
	`background:none;color:inherit;border:none;border-radius:0;box-shadow:none}` +
	`}`

// searchJS turns the nav's search link into a disclosure: the click opens
// the panel instead of following the link, the box takes focus, and
// Escape or a click elsewhere closes it again.
//
// Delegated on document for the same reason navJS is — the in-place
// editor re-renders the nav from JavaScript after a menu save, and a
// listener bound to the old element would go with it.
//
// The link keeps working when this does not run: nothing here is what
// makes the control reachable, only what makes it expand in place.
const searchJS = `(function(){` +
	`function close(){document.querySelectorAll('.cms-nav-search.cms-open').forEach(function(li){` +
	`li.classList.remove('cms-open');` +
	`var a=li.querySelector('.cms-nav-search-toggle');if(a)a.setAttribute('aria-expanded','false');});}` +
	`document.addEventListener('click',function(e){` +
	`if(!e.target.closest)return;` +
	`var t=e.target.closest('.cms-nav-search-toggle');` +
	`if(!t){` +
	// A click inside the open panel is someone using it, not leaving it.
	`if(!e.target.closest('.cms-nav-search'))close();` +
	`return;}` +
	`e.preventDefault();` +
	`var li=t.closest('.cms-nav-search');var open=!li.classList.contains('cms-open');` +
	`close();li.classList.toggle('cms-open',open);` +
	`t.setAttribute('aria-expanded',open?'true':'false');` +
	`if(open){var box=li.querySelector('.cms-search-input');if(box)box.focus();}});` +
	`document.addEventListener('keydown',function(e){if(e.key!=='Escape')return;` +
	// Focus goes back to the toggle, so Escape does not strand the
	// keyboard at the top of the document.
	`var open=document.querySelector('.cms-nav-search.cms-open');if(!open)return;` +
	`close();var a=open.querySelector('.cms-nav-search-toggle');if(a)a.focus();});` +
	`})();`
