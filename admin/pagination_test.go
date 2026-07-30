package admin

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/render"
)

// A missing or junk ?page= is page one. An admin list is a perfectly valid
// page whatever rides in on the query string, so nothing here 404s.
func TestListPage(t *testing.T) {
	for target, want := range map[string]int{
		"/admin/posts":                  1,
		"/admin/posts?page=3":           3,
		"/admin/posts?page=0":           1,
		"/admin/posts?page=-1":          1,
		"/admin/posts?page=":            1,
		"/admin/posts?page=lots":        1,
		"/admin/posts?feed=blog":        1,
		"/admin/posts?feed=blog&page=2": 2,
	} {
		if got := listPage(httptest.NewRequest(http.MethodGet, target, nil)); got != want {
			t.Errorf("listPage(%q) = %d, want %d", target, got, want)
		}
	}
}

// Page links must carry the feed tab across pages — paging the Blog tab
// must not silently drop you back into All — and must be prefixed with the
// admin mount path. The admin handler runs with that prefix stripped, so
// the request path here is "/posts"; a link built from it verbatim would
// point at the public site instead.
func TestListPageURLKeepsPrefixAndFilter(t *testing.T) {
	cases := []struct {
		adminPath, target string
		n                 int
		want              string
	}{
		{"/admin", "/posts", 2, "/admin/posts?page=2"},
		{"/admin", "/posts?page=4", 1, "/admin/posts"},
		{"/admin", "/posts?feed=news", 3, "/admin/posts?feed=news&page=3"},
		{"/admin", "/posts?feed=news&page=3", 2, "/admin/posts?feed=news&page=2"},
		// Back to page one: page= goes, the tab stays.
		{"/admin", "/posts?feed=blog&page=2", 1, "/admin/posts?feed=blog"},
		// A host that mounted the admin somewhere else gets its own path.
		{"/manage", "/posts", 2, "/manage/posts?page=2"},
		{"/back/office", "/posts?feed=blog", 2, "/back/office/posts?feed=blog&page=2"},
	}
	for _, c := range cases {
		s := &server{deps: Deps{AdminPath: c.adminPath}}
		got := s.listPageURL(httptest.NewRequest(http.MethodGet, c.target, nil))(c.n)
		if got != c.want {
			t.Errorf("listPageURL(%q, %q)(%d) = %q, want %q",
				c.adminPath, c.target, c.n, got, c.want)
		}
	}
}

// Both paginated list templates must actually render the bar. A handler
// can set data.Pager correctly and the page still show nothing if the
// template forgot {{with .Pager}}, which no handler test would catch.
func TestListTemplatesRenderThePager(t *testing.T) {
	s := &server{deps: Deps{AdminPath: "/admin"}, templates: parseTemplates()}
	pager := render.NewPager(2, 5, 40, func(n int) string { return "/admin/x?page=" + strconv.Itoa(n) })
	pager.PrevLabel, pager.NextLabel = "Previous", "Next"

	for _, page := range []string{"posts", "pages"} {
		rec := httptest.NewRecorder()
		s.render(rec, http.StatusOK, page, templateData{
			AdminPath: "/admin", User: &auth.User{Name: "Ed", Role: auth.RoleEditor},
			PagerCSSPath: pagerCSSPath, Pager: pager,
		})
		body := rec.Body.String()
		if !strings.Contains(body, `<nav class="cms-pager"`) {
			t.Errorf("%s.gohtml did not render the pagination bar:\n%s", page, body)
		}
		if !strings.Contains(body, `aria-current="page">2</span>`) {
			t.Errorf("%s.gohtml pager is missing the current page marker", page)
		}
		// And it must link the served stylesheet, or the bar is unstyled.
		if !strings.Contains(body, `href="/admin`+pagerCSSPath+`"`) {
			t.Errorf("%s.gohtml does not link the pager stylesheet", page)
		}
	}
}

func TestPerPageFallsBackToDefault(t *testing.T) {
	if got := (&server{deps: Deps{PerPage: 40}}).perPage(); got != 40 {
		t.Errorf("configured perPage = %d, want 40", got)
	}
	// An unset (or nonsensical) Deps.PerPage must not page the admin zero
	// rows at a time.
	for _, n := range []int{0, -5} {
		if got := (&server{deps: Deps{PerPage: n}}).perPage(); got != DefaultPerPage {
			t.Errorf("perPage with Deps.PerPage=%d = %d, want %d", n, got, DefaultPerPage)
		}
	}
}

// The shared pagination CSS has to arrive as a stylesheet the browser will
// actually apply. The admin's CSP carries no style-src 'unsafe-inline', so
// an inline <style> is fetched, parsed, and silently dropped — the bar
// renders as a bulleted list. Serving it from a route is what makes it
// work, and this test pins that: the route must exist, be CSS, carry the
// rules, and be reachable without logging in (the login page uses the same
// layout).
func TestPagerCSSIsServedNotInlined(t *testing.T) {
	h := New(Deps{Sessions: scs.New(), AdminPath: "/admin"})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pagerCSSPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", pagerCSSPath, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("GET %s: Content-Type %q, want text/css", pagerCSSPath, ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, ".cms-pager{") || body != render.PagerCSS {
		t.Errorf("GET %s did not serve render.PagerCSS verbatim (%d bytes)", pagerCSSPath, len(body))
	}

	// The layout must link that route and inline no styles of its own —
	// the CSP would drop them.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	page := rec.Body.String()
	if !strings.Contains(page, `<link rel="stylesheet" href="/admin`+pagerCSSPath+`">`) {
		t.Errorf("login page does not link %s:\n%s", pagerCSSPath, page)
	}
	if strings.Contains(page, "<style>") {
		t.Error("layout inlines a <style> block, which the admin's CSP drops")
	}
	// And the policy that makes all this necessary must stay strict.
	if csp := rec.Header().Get("Content-Security-Policy"); strings.Contains(csp, "unsafe-inline") {
		t.Errorf("admin CSP now allows unsafe-inline styles: %q", csp)
	}
}

// The admin's bar is render.Pager's, so the markup and classes are the
// same ones the public site's {{cmsPagination}} emits — including the
// translated end labels the admin sets.
func TestAdminPagerRendersSharedMarkup(t *testing.T) {
	s := &server{deps: Deps{AdminPath: "/admin"}}
	p := render.NewPager(2, 25, 120, s.listPageURL(
		httptest.NewRequest(http.MethodGet, "/posts?feed=blog&page=2", nil)))
	p.PrevLabel, p.NextLabel = "Précédent", "Suivant"

	if p.TotalPages != 5 || p.Offset() != 25 {
		t.Errorf("pager = page %d/%d offset %d, want page 2/5 offset 25",
			p.Page, p.TotalPages, p.Offset())
	}
	out := string(p.HTML())
	for _, want := range []string{
		`<nav class="cms-pager" aria-label="Pagination">`,
		`<a class="cms-pager-step cms-pager-prev" href="/admin/posts?feed=blog" rel="prev">Précédent</a>`,
		`<span class="cms-pager-link cms-pager-current" aria-current="page">2</span>`,
		`href="/admin/posts?feed=blog&amp;page=3"`,
		`rel="next">Suivant</a>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pager HTML missing %q:\n%s", want, out)
		}
	}

	// One page of posts needs no bar at all.
	if got := render.NewPager(1, 25, 10, nil).HTML(); got != "" {
		t.Errorf("single-page pager rendered %q, want nothing", got)
	}
}
