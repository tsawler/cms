package render

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/content"
)

// searchFS is the test renderer's own results template: it calls the two
// search funcs the way a host's would.
var searchFS = fstest.MapFS{
	"base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><head>{{cmsHead}}</head><body>{{cmsNav "main"}}` +
			`{{block "content" .}}{{end}}{{cmsScripts}}</body></html>{{end}}`)},
	"pages/home.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<h1>{{cmsTitle}}</h1>{{end}}`)},
	"pages/search.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}{{$r := cmsSearch}}` +
			`{{cmsSearchForm}}` +
			`<p id="q">{{$r.Query}}</p><p id="total">{{$r.Total}}</p>` +
			`<p id="searched">{{$r.Searched}}</p>` +
			`<ul>{{range $r.Hits}}<li><a href="{{.URL}}">{{.Title}}</a>{{.Snippet}}</li>{{end}}</ul>` +
			`{{cmsPagination $r}}{{end}}`)},
}

func searchRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := New(searchFS, []string{"base.gohtml"},
		[]PageTemplate{{File: "pages/home.gohtml", Label: "Home"}}, nil,
		PageTemplate{File: "pages/search.gohtml", Label: "Search"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func renderTo(t *testing.T, r *Renderer, in Input) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.Render(&buf, in); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

func homeInput() Input {
	return Input{
		Page:   &content.Page{ID: 1, TemplateName: "pages/home.gohtml", Title: "Home"},
		Locale: "en",
		Menus:  map[string][]MenuEntry{"main": {{Label: "About", URL: "/about"}}},
	}
}

// The magnifying glass appears only when the site setting is on and the
// host has a results page for it to lead to. Either one missing and the nav
// carries nothing: an icon that opens a box that goes nowhere is worse than
// no icon.
func TestNavSearchNeedsBothTheSettingAndAPath(t *testing.T) {
	r := searchRenderer(t)

	tests := []struct {
		name string
		in   Input
		want bool
	}{
		{"neither", homeInput(), false},
		{"setting only", func() Input {
			in := homeInput()
			in.Site.SearchInNav = true
			return in
		}(), false},
		{"path only", func() Input {
			in := homeInput()
			in.SearchPath = "/search"
			return in
		}(), false},
		{"both", func() Input {
			in := homeInput()
			in.Site.SearchInNav, in.SearchPath = true, "/search"
			return in
		}(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderTo(t, r, tt.in)
			if got := strings.Contains(out, `class="cms-nav-search"`); got != tt.want {
				t.Errorf("nav search item present = %v, want %v", got, tt.want)
			}
			// The styles and the toggle script ride along with the item,
			// so a site without search carries neither.
			if got := strings.Contains(out, ".cms-nav-search-panel"); got != tt.want {
				t.Errorf("search CSS emitted = %v, want %v", got, tt.want)
			}
			if got := strings.Contains(out, "cms-nav-search-toggle')"); got != tt.want {
				t.Errorf("search JS emitted = %v, want %v", got, tt.want)
			}
		})
	}
}

// Without JavaScript the magnifying glass still has to do something, so it
// is a real link to the results page rather than a button.
func TestNavSearchTogglesFromALinkToTheResultsPage(t *testing.T) {
	in := homeInput()
	in.Site.SearchInNav, in.SearchPath = true, "/fr/search"
	out := renderTo(t, searchRenderer(t), in)

	if !strings.Contains(out, `<a class="cms-nav-link cms-nav-search-toggle" href="/fr/search"`) {
		t.Errorf("the toggle is not a link to the results page:\n%s", out)
	}
	if !strings.Contains(out, `aria-expanded="false"`) ||
		!strings.Contains(out, `aria-controls="cms-nav-search-panel"`) {
		t.Errorf("the toggle is missing its disclosure attributes:\n%s", out)
	}
	// And the panel holds a real GET form, which works with the script or
	// without it.
	if !strings.Contains(out, `<form class="cms-search cms-nav-search-form" role="search" method="get" action="/fr/search">`) {
		t.Errorf("the panel does not hold a usable form:\n%s", out)
	}
}

// The item deliberately does not carry .cms-nav-item: that class is how the
// in-place editor recognizes a menu entry, and this is not one.
func TestNavSearchIsNotAMenuItem(t *testing.T) {
	in := homeInput()
	in.Site.SearchInNav, in.SearchPath = true, "/search"
	out := renderTo(t, searchRenderer(t), in)

	// The <li> as emitted, not the stylesheet that mentions the same class.
	if !strings.Contains(out, `<li class="cms-nav-search">`) {
		t.Errorf("the search <li> is not exactly class=\"cms-nav-search\":\n%s", out)
	}
	if strings.Contains(out, `cms-nav-item cms-nav-search`) {
		t.Error("the search <li> carries cms-nav-item, which the editor reads as a menu entry")
	}
}

// An edit render carries the item whether or not the setting is on — hidden
// when it is off — so switching it on in the settings dialog reveals markup
// the server built rather than markup the editor had to reinvent.
func TestEditRenderCarriesTheSearchItemSwitchedOff(t *testing.T) {
	in := homeInput()
	in.SearchPath = "/search"
	in.Edit = &EditInfo{PageID: 1, Slug: "", AdminPath: "/admin", CSRFToken: "t", Locale: "en"}
	out := renderTo(t, searchRenderer(t), in)

	if !strings.Contains(out, `<li class="cms-nav-search cms-nav-search-off">`) {
		t.Errorf("an edit render does not carry the hidden search item:\n%s", out)
	}
	if !strings.Contains(out, ".cms-nav-search-off{display:none}") {
		t.Error("the rule that hides it was not emitted")
	}

	// With the setting on it is the same markup without the class. (The
	// stylesheet still names it, which is why this looks at the <li>.)
	in.Site.SearchInNav = true
	out = renderTo(t, searchRenderer(t), in)
	if !strings.Contains(out, `<li class="cms-nav-search">`) {
		t.Errorf("the item is still hidden with the setting on:\n%s", out)
	}
}

// One magnifying glass per page, in the first nav — the same rule the
// editor's own admin-tools button follows.
func TestNavSearchGoesInTheFirstNavOnly(t *testing.T) {
	fsys := fstest.MapFS{
		"base.gohtml": &fstest.MapFile{Data: []byte(
			`{{define "base"}}<html><head>{{cmsHead}}</head><body>` +
				`{{cmsNav "main"}}{{cmsNav "footer"}}{{cmsScripts}}</body></html>{{end}}`)},
		"pages/home.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}`)},
	}
	r, err := New(fsys, []string{"base.gohtml"},
		[]PageTemplate{{File: "pages/home.gohtml", Label: "Home"}}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in := Input{
		Page:   &content.Page{ID: 1, TemplateName: "pages/home.gohtml"},
		Locale: "en",
		Menus: map[string][]MenuEntry{
			"main":   {{Label: "About", URL: "/about"}},
			"footer": {{Label: "Terms", URL: "/terms"}},
		},
		SearchPath: "/search",
	}
	in.Site.SearchInNav = true
	out := renderTo(t, r, in)
	if n := strings.Count(out, `class="cms-nav-search"`); n != 1 {
		t.Errorf("page carries %d search boxes, want 1:\n%s", n, out)
	}
}

// The results page: the query is echoed back, the hits are listed, and the
// count is the one the pager was sized with.
func TestCmsSearchRendersResults(t *testing.T) {
	calls := 0
	in := Input{
		Page:        &content.Page{ID: 0, TemplateName: "pages/search.gohtml", Title: "Search"},
		Locale:      "en",
		SearchQuery: "opening hours",
		SearchPath:  "/search",
		PageNumber:  1,
		Search: func(q string, limit, offset int, count bool) ([]SearchHit, int) {
			calls++
			if q != "opening hours" {
				t.Errorf("lister asked for %q, want the request's query", q)
			}
			return []SearchHit{
				{Kind: "page", Title: "Opening hours", URL: "/hours", Snippet: "We open at nine"},
				{Kind: "news", IsPost: true, Title: "New hours", URL: "/news/new-hours"},
			}, 3
		},
	}
	out := renderTo(t, searchRenderer(t), in)

	for _, want := range []string{
		`<p id="q">opening hours</p>`,
		`<p id="total">3</p>`,
		`<p id="searched">true</p>`,
		`<a href="/hours">Opening hours</a>We open at nine`,
		`<a href="/news/new-hours">New hours</a>`,
		// The box on the results page carries the query back, so it is
		// still there to edit.
		`value="opening hours"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("results page is missing %q:\n%s", want, out)
		}
	}
	// One call, not two: the window and the count come back together, so
	// an ordinary results page costs one round of queries.
	if calls != 1 {
		t.Errorf("the lister was called %d times, want 1", calls)
	}
}

// An empty box is not a search that found nothing, and it must not cost a
// query to say so.
func TestCmsSearchAsksNothingWithoutAQuery(t *testing.T) {
	called := false
	in := Input{
		Page:       &content.Page{TemplateName: "pages/search.gohtml", Title: "Search"},
		Locale:     "en",
		SearchPath: "/search",
		Search: func(string, int, int, bool) ([]SearchHit, int) {
			called = true
			return nil, 0
		},
	}
	out := renderTo(t, searchRenderer(t), in)
	if called {
		t.Error("an empty query still ran a search")
	}
	if !strings.Contains(out, `<p id="searched">false</p>`) {
		t.Errorf("the page does not know it was never searched:\n%s", out)
	}
}

// A results URL is shareable, so the box has to be a GET form pointed at
// the results path, and its field has to be the ?q= everyone expects.
func TestSearchFormIsAGetFormOnTheQueryParam(t *testing.T) {
	got := string(SearchFormHTML("/search", `a "quoted" & <tag>`))
	if !strings.Contains(got, `method="get"`) || !strings.Contains(got, `action="/search"`) {
		t.Errorf("form = %q, want a GET to the results path", got)
	}
	if !strings.Contains(got, `name="`+SearchQueryParam+`"`) {
		t.Errorf("form = %q, want the query in ?%s=", got, SearchQueryParam)
	}
	// The echoed query is attribute-escaped: it came from the URL.
	if strings.Contains(got, `value="a "quoted"`) || strings.Contains(got, "<tag>") {
		t.Errorf("the echoed query was not escaped: %q", got)
	}
	if SearchFormHTML("", "x") != "" {
		t.Error("a site with no results page still rendered a form")
	}
}

// cmsPagination draws the bar for a search page as readily as for a feed,
// so a site has one pagination bar rather than two that drift.
func TestPaginationTakesASearchPage(t *testing.T) {
	in := Input{
		Page:          &content.Page{TemplateName: "pages/search.gohtml", Title: "Search"},
		Locale:        "en",
		SearchQuery:   "x",
		SearchPath:    "/search",
		SearchPerPage: 2,
		PageNumber:    1,
		Search: func(q string, limit, offset int, count bool) ([]SearchHit, int) {
			return []SearchHit{{Title: "One", URL: "/one"}}, 5
		},
	}
	out := renderTo(t, searchRenderer(t), in)
	if !strings.Contains(out, `class="cms-pager"`) {
		t.Errorf("no pagination bar for a five-result search:\n%s", out)
	}
}
