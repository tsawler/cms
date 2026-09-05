package cms

import (
	"context"
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

// The search route: what claims it, what it answers with, and what a page
// at the same address does to it.

var searchTestFS = fstest.MapFS{
	"templates/base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><body>{{block "content" .}}{{end}}</body></html>{{end}}`)},
	"templates/pages/standard.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<main>{{cmsRegion "main"}}</main>{{end}}`)},
	"templates/pages/search.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<div id="results">` +
			`{{$r := cmsSearch}}{{range $r.Hits}}<a href="{{.URL}}">{{.Title}}</a>{{end}}` +
			`</div><p id="q">{{$r.Query}}</p>{{end}}`)},
}

// newSearchTestCMS builds a CMS with a results template unless search is
// switched off, which is how a host turns the feature on and off.
func newSearchTestCMS(t *testing.T, db *sqldb.DB, search bool) *CMS {
	t.Helper()
	cfg := Config{
		DB:              db.SQL(),
		Dialect:         db.Dialect().Name(),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		TemplateFS:      searchTestFS,
		SharedTemplates: []string{"templates/base.gohtml"},
		PageTemplates: []PageTemplate{
			{File: "templates/pages/standard.gohtml", Label: "Standard page"},
		},
	}
	if search {
		cfg.SearchTemplate = PageTemplate{File: "templates/pages/search.gohtml", Label: "Search"}
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("cms.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := c.SeedHomePage(ctx, "templates/pages/standard.gohtml", "Welcome"); err != nil {
		t.Fatalf("SeedHomePage: %v", err)
	}
	return c
}

// publishSearchable adds a page with a body and publishes it, which is what
// puts it in the index.
func publishSearchable(t *testing.T, c *CMS, slug, title, body string) {
	t.Helper()
	ctx := context.Background()
	id, err := c.content.Insert(ctx, &content.Page{
		Slug: slug, Title: title, TemplateName: "templates/pages/standard.gohtml",
	}, "en")
	if err != nil {
		t.Fatalf("Insert(%q): %v", slug, err)
	}
	if err := c.content.UpsertDraftBlock(ctx, id, "main", "en", content.KindHTML, body); err != nil {
		t.Fatalf("UpsertDraftBlock(%q): %v", slug, err)
	}
	if err := c.content.Publish(ctx, id); err != nil {
		t.Fatalf("Publish(%q): %v", slug, err)
	}
}

// waitFor polls until cond holds, or fails the test. The search index
// backfill runs in a goroutine off the first request, so there is nothing
// to synchronize on but its effect.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the search index to be built")
}

func get(t *testing.T, h http.Handler, url string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

func TestSearchRouteAnswersWithResults(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newSearchTestCMS(t, db, true)
		publishSearchable(t, c, "hours", "Opening hours", "<p>We open at nine on zebracorn days.</p>")
		publishSearchable(t, c, "other", "Something else", "<p>Nothing to see.</p>")
		h := c.Pages()

		rec := get(t, h, "http://example.test/search?q=zebracorn")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /search status %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `<a href="/hours">Opening hours</a>`) {
			t.Errorf("results page does not list the matching page:\n%s", body)
		}
		if strings.Contains(body, "/other") {
			t.Errorf("results page lists a page that does not match:\n%s", body)
		}
		if !strings.Contains(body, `<p id="q">zebracorn</p>`) {
			t.Errorf("the query was not echoed back:\n%s", body)
		}
		// A results page is a different page for every query; there is
		// nothing there worth having in an index.
		if got := rec.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
			t.Errorf("X-Robots-Tag = %q, want it to carry noindex", got)
		}
	})
}

// Without a results template the CMS claims nothing: the address is the
// host application's, exactly as /robots.txt and /sitemap.xml are.
func TestSearchRouteUnclaimedWithoutATemplate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newSearchTestCMS(t, db, false)
		if rec := get(t, c.Pages(), "http://example.test/search?q=x"); rec.Code != http.StatusNotFound {
			t.Errorf("GET /search status %d with no search template, want 404", rec.Code)
		}
	})
}

// A real page at the address wins. An install that built its own /search
// before the CMS had one keeps it.
func TestAPageAtTheSearchPathWins(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newSearchTestCMS(t, db, true)
		publishSearchable(t, c, "search", "Our own search page", "<p>hand built</p>")

		body := get(t, c.Pages(), "http://example.test/search?q=zebracorn").Body.String()
		if !strings.Contains(body, "hand built") {
			t.Errorf("the stored page did not win the address:\n%s", body)
		}
		if strings.Contains(body, `id="results"`) {
			t.Errorf("the CMS answered over a stored page:\n%s", body)
		}
	})
}

// Config.SearchPath moves the address, and the nav's link and the form
// move with it.
func TestSearchPathIsConfigurable(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newSearchTestCMS(t, db, true)
		c.cfg.SearchPath = "find"
		publishSearchable(t, c, "hours", "Opening hours", "<p>zebracorn</p>")
		h := c.Pages()

		if rec := get(t, h, "http://example.test/search?q=zebracorn"); rec.Code != http.StatusNotFound {
			t.Errorf("the default address still answers: status %d, want 404", rec.Code)
		}
		body := get(t, h, "http://example.test/find?q=zebracorn").Body.String()
		if !strings.Contains(body, "/hours") {
			t.Errorf("the configured address does not answer:\n%s", body)
		}
	})
}

// The index is built off the first request on an install that has content
// and none — which is every install upgrading into this feature.
func TestSearchIndexIsBuiltOnFirstTraffic(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSearchTestCMS(t, db, true)
		publishSearchable(t, c, "hours", "Opening hours", "<p>zebracorn</p>")

		// Empty the index behind the CMS's back, the state an upgrade
		// arrives in: rows in cms_pages, none in cms_search_docs.
		if _, err := db.Exec(ctx, "DELETE FROM cms_search_docs"); err != nil {
			t.Fatalf("emptying the index: %v", err)
		}
		if empty, err := c.content.SearchIndexEmpty(ctx); err != nil || !empty {
			t.Fatalf("SearchIndexEmpty = %v, %v; want true, nil", empty, err)
		}

		// The rebuild runs in the background off the first request, so
		// the first search may be too early; what matters is that it
		// happens without anyone asking.
		get(t, c.Pages(), "http://example.test/")
		waitFor(t, func() bool {
			empty, err := c.content.SearchIndexEmpty(ctx)
			return err == nil && !empty
		})
		body := get(t, c.Pages(), "http://example.test/search?q=zebracorn").Body.String()
		if !strings.Contains(body, "/hours") {
			t.Errorf("the rebuilt index does not answer:\n%s", body)
		}
	})
}

// A region a template no longer draws keeps its blocks — that is what
// makes reworking a layout reversible — but those words are on no page,
// so a search must not find them there.
func TestSearchIgnoresBlocksNoTemplateDraws(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSearchTestCMS(t, db, true)

		id, err := c.content.Insert(ctx, &content.Page{
			Slug: "beliefs", Title: "Beliefs", TemplateName: "templates/pages/standard.gohtml",
		}, "en")
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		// "main" is the only region standard.gohtml declares; "band" is
		// what an earlier version of the template used to.
		if err := c.content.UpsertDraftBlock(ctx, id, "main", "en",
			content.KindHTML, "<p>what we believe</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(main): %v", err)
		}
		if err := c.content.UpsertDraftBlock(ctx, id, "band", "en",
			content.KindHTML, "<h1>zebracorn</h1>"); err != nil {
			t.Fatalf("UpsertDraftBlock(band): %v", err)
		}
		if err := c.content.Publish(ctx, id); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		body := get(t, c.Pages(), "http://example.test/beliefs").Body.String()
		if strings.Contains(body, "zebracorn") {
			t.Fatal("the orphaned region is on the page after all; the test proves nothing")
		}
		if !strings.Contains(body, "what we believe") {
			t.Fatalf("the drawn region is missing from the page:\n%s", body)
		}

		results := get(t, c.Pages(), "http://example.test/search?q=zebracorn").Body.String()
		if strings.Contains(results, "/beliefs") {
			t.Errorf("a search found words that are on no page:\n%s", results)
		}
		// And the words that are on it are still findable.
		results = get(t, c.Pages(), "http://example.test/search?q=believe").Body.String()
		if !strings.Contains(results, "/beliefs") {
			t.Errorf("filtering the index dropped the page's real content:\n%s", results)
		}
	})
}
