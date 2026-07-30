package cms

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The ?page= a listing was asked for. Anything that is not a page number
// is page one: the page itself is valid either way, and refusing to render
// it over a junk query parameter would hand crawlers and stale links a
// broken site.
func TestListingPage(t *testing.T) {
	for target, want := range map[string]int{
		"/blog":               1,
		"/blog?page=1":        1,
		"/blog?page=4":        4,
		"/blog?page=0":        1,
		"/blog?page=-2":       1,
		"/blog?page=":         1,
		"/blog?page=two":      1,
		"/blog?page=3.5":      1,
		"/blog?page=9e9":      1,
		"/blog?other=7":       1,
		"/fr/blog?page=2":     2,
		"/blog?tag=go&page=3": 3,
	} {
		if got := listingPage(httptest.NewRequest(http.MethodGet, target, nil)); got != want {
			t.Errorf("listingPage(%q) = %d, want %d", target, got, want)
		}
	}
}

// Page links keep the path and any other query parameters, and page one
// drops page= entirely so the canonical listing URL stays the bare one.
func TestListingPageURL(t *testing.T) {
	cases := []struct {
		target string
		n      int
		want   string
	}{
		{"/blog", 2, "/blog?page=2"},
		{"/blog", 1, "/blog"},
		{"/blog?page=5", 1, "/blog"},
		{"/blog?page=5", 0, "/blog"},
		{"/blog?page=5", 6, "/blog?page=6"},
		{"/fr/blog?page=2", 3, "/fr/blog?page=3"},
		// Other parameters ride along, on every page including the first.
		{"/blog?tag=go", 2, "/blog?page=2&tag=go"},
		{"/blog?tag=go&page=2", 1, "/blog?tag=go"},
		// A path needing escaping stays escaped.
		{"/blog/a%20b", 2, "/blog/a%20b?page=2"},
	}
	for _, c := range cases {
		got := listingPageURL(httptest.NewRequest(http.MethodGet, c.target, nil))(c.n)
		if got != c.want {
			t.Errorf("listingPageURL(%q)(%d) = %q, want %q", c.target, c.n, got, c.want)
		}
	}
}
