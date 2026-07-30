// Pagination shared by the admin's list pages. The bar itself is
// render.Pager — the same type and the same stylesheet the public site's
// {{cmsPagination}} uses — so there is one pagination implementation in the
// codebase rather than an admin one and a site one that drift.
//
// A list handler pages in three steps, in this order: count the rows,
// NewPager to clamp the requested page against that count, then fetch
// Offset()..PerPage. Counting first is what makes a ?page= past the end
// land on the last real page instead of reading a window off the end.

package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tsawler/cms/render"
)

// DefaultPerPage is how many rows a paginated admin list shows when Deps
// does not say. An editor's table is a working list rather than a page of
// a site, so it holds rather more than a public listing does.
const DefaultPerPage = 25

// perPage is how many rows this deployment's paginated admin lists show.
func (s *server) perPage() int {
	if s.deps.PerPage > 0 {
		return s.deps.PerPage
	}
	return DefaultPerPage
}

// listPage is the ?page= an admin list was asked for, defaulting to 1.
// Anything unparseable is page one rather than a 404 — the list is
// perfectly valid whatever junk rode in on the query string.
func listPage(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// listPageURL builds an admin list's page links: this same URL with the
// page number swapped and everything else left alone, so a filter on the
// list (the posts list's ?feed=blog tab) survives paging. Page one carries
// no page= at all, keeping the plain list URL the canonical one.
//
// The admin runs with its mount prefix stripped, so r.URL.Path here is
// "/posts" rather than "/admin/posts"; AdminPath goes back on the front or
// the links point at the public site.
func (s *server) listPageURL(r *http.Request) func(int) string {
	return func(n int) string {
		q := r.URL.Query()
		if n <= 1 {
			q.Del("page")
		} else {
			q.Set("page", strconv.Itoa(n))
		}
		u := url.URL{Path: s.deps.AdminPath + r.URL.Path, RawQuery: q.Encode()}
		return u.String()
	}
}

// pagerCSSPath serves render.PagerCSS, the styles for the pagination bar
// the admin's lists and the public site's {{cmsPagination}} share. The
// route (rather than an inline <style>, or a copy in admin.css) is what
// keeps one definition of that CSS while staying inside the admin's CSP,
// which allows no inline styles. The name is stable so the browser can
// cache it across pages; the module is a library, so there is no build
// step to hash it into.
const pagerCSSPath = "/static/pager.css"

// servePagerCSS writes the shared pagination stylesheet. It closes over no
// request state — the bytes are a compile-time constant — so it needs no
// server receiver and is safe to serve before the login check, exactly as
// the rest of /static is.
func servePagerCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	http.ServeContent(w, r, "pager.css", pagerCSSModTime, strings.NewReader(render.PagerCSS))
}

// pagerCSSModTime dates the stylesheet for conditional requests. The
// content only ever changes with the module's own code, so the build's
// start is as good a version as a hash and costs nothing to compute.
var pagerCSSModTime = time.Now()
