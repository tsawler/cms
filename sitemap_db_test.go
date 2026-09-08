package cms

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// The generated sitemap: what it lists, when the CMS claims the address
// at all, and how it reaches robots.txt.

// setSitemap turns the sitemap on or off and defeats the settings cache.
func setSitemap(t *testing.T, c *CMS, on bool) {
	t.Helper()
	if err := c.content.SetSitemap(context.Background(), on); err != nil {
		t.Fatalf("SetSitemap(%v): %v", on, err)
	}
	expireSiteCache(c)
}

// expireSitemapCache drops the rendered document, which would otherwise
// outlive a content change by minutes.
// expireSitemapCache drops both halves — the rows and the document
// rendered from them. Clearing only the document would leave the next
// request re-rendering rows read before the test changed anything, which
// is exactly the staleness these tests are checking for.
func expireSitemapCache(c *CMS) {
	c.sitemapMu.Lock()
	c.sitemapBody, c.sitemapBase = nil, ""
	c.sitemapPages, c.sitemapAt = nil, time.Time{}
	c.sitemapMu.Unlock()
}

// addPage inserts a page and puts it in the state the test wants.
func addPage(t *testing.T, c *CMS, slug string, publish bool, vis content.Visibility) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := c.content.Insert(ctx, &content.Page{
		Slug: slug, Title: slug, TemplateName: "templates/pages/standard.gohtml",
	}, "en")
	if err != nil {
		t.Fatalf("Insert(%q): %v", slug, err)
	}
	if vis != "" {
		if err := c.content.SetVisibility(ctx, id, vis); err != nil {
			t.Fatalf("SetVisibility(%q): %v", slug, err)
		}
	}
	if publish {
		if err := c.content.Publish(ctx, id); err != nil {
			t.Fatalf("Publish(%q): %v", slug, err)
		}
	}
	return id
}

// parsed sitemap, for assertions that don't depend on formatting.
type gotSitemap struct {
	XMLName xml.Name `xml:"urlset"`
	URLs    []struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod"`
		Alts    []struct {
			Rel      string `xml:"rel,attr"`
			HrefLang string `xml:"hreflang,attr"`
			Href     string `xml:"href,attr"`
		} `xml:"link"`
	} `xml:"url"`
}

func fetchSitemap(t *testing.T, h http.Handler) gotSitemap {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test/sitemap.xml", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sitemap.xml status %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("Content-Type = %q, want application/xml", ct)
	}
	var doc gotSitemap
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("parsing the sitemap: %v\n%s", err, rec.Body)
	}
	return doc
}

func locs(doc gotSitemap) []string {
	out := make([]string, 0, len(doc.URLs))
	for _, u := range doc.URLs {
		out = append(out, u.Loc)
	}
	return out
}

func TestSitemapListsPublishedPublicPages(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/standard.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		h := c.Pages()

		addPage(t, c, "about", true, content.VisibilityPublic)
		addPage(t, c, "draft-page", false, content.VisibilityPublic)
		addPage(t, c, "members", true, content.VisibilityPrivate)

		setMode(t, c, content.ModeProduction)
		setSitemap(t, c, true)

		doc := fetchSitemap(t, h)
		got := strings.Join(locs(doc), " ")
		// The home page is the base itself; every other page hangs off it.
		for _, want := range []string{"http://example.test/", "http://example.test/about"} {
			if !strings.Contains(got, want) {
				t.Errorf("sitemap is missing %q: %v", want, locs(doc))
			}
		}
		// A draft is not live, and a private page is not for the public —
		// neither belongs in a document written for search engines.
		for _, unwanted := range []string{"draft-page", "members"} {
			if strings.Contains(got, unwanted) {
				t.Errorf("sitemap lists %q: %v", unwanted, locs(doc))
			}
		}
		for _, u := range doc.URLs {
			if u.LastMod == "" {
				t.Errorf("%s has no lastmod", u.Loc)
			}
		}
		// One locale, so no alternates: the head emits none either.
		for _, u := range doc.URLs {
			if len(u.Alts) != 0 {
				t.Errorf("%s carries %d alternates on a single-locale site", u.Loc, len(u.Alts))
			}
		}
	})
}

// The CMS claims /sitemap.xml only when the setting says so, and never
// while the site is in development.
func TestSitemapClaimsPathOnlyWhenOn(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/standard.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		h := c.Pages()
		get := func() int {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test/sitemap.xml", nil))
			return rec.Code
		}

		setMode(t, c, content.ModeProduction)

		// Off: the address belongs to the host app, and an ordinary page
		// lookup finds nothing there.
		setSitemap(t, c, false)
		if code := get(); code != http.StatusNotFound {
			t.Errorf("sitemap off: status %d, want 404 (the CMS should not claim it)", code)
		}

		setSitemap(t, c, true)
		if code := get(); code != http.StatusOK {
			t.Errorf("sitemap on: status %d, want 200", code)
		}

		// A site asking not to be crawled does not publish a list of
		// every URL it has.
		setMode(t, c, content.ModeDevelopment)
		if code := get(); code != http.StatusNotFound {
			t.Errorf("development: status %d, want 404", code)
		}
	})
}

// Multi-locale sites list every language, and each URL carries the
// hreflang alternates {{cmsHead}} promises.
func TestSitemapListsEveryLocale(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c, err := New(Config{
			DB:              db.SQL(),
			Dialect:         db.Dialect().Name(),
			Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
			TemplateFS:      seedTestFS,
			SharedTemplates: []string{"templates/base.gohtml"},
			PageTemplates: []PageTemplate{
				{File: "templates/pages/standard.gohtml", Label: "Standard page"},
			},
			Locales: []string{"en", "fr"},
		})
		if err != nil {
			t.Fatalf("cms.New: %v", err)
		}
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		h := c.Pages()

		addPage(t, c, "about", true, content.VisibilityPublic)
		setMode(t, c, content.ModeProduction)
		setSitemap(t, c, true)

		doc := fetchSitemap(t, h)
		got := locs(doc)
		// The default locale is unprefixed; the other lives under /fr.
		want := []string{"http://example.test/about", "http://example.test/fr/about"}
		for _, w := range want {
			if !strings.Contains(strings.Join(got, " "), w) {
				t.Errorf("sitemap is missing %q: %v", w, got)
			}
		}
		if len(got) != 2 {
			t.Errorf("sitemap has %d URLs, want 2 (one page × two locales): %v", len(got), got)
		}
		// en, fr, x-default on both.
		for _, u := range doc.URLs {
			var langs []string
			for _, a := range u.Alts {
				if a.Rel != "alternate" {
					t.Errorf("%s: alternate rel = %q", u.Loc, a.Rel)
				}
				langs = append(langs, a.HrefLang)
			}
			if strings.Join(langs, ",") != "en,fr,x-default" {
				t.Errorf("%s alternates = %v, want en, fr and x-default", u.Loc, langs)
			}
		}
	})
}

// Turning the sitemap on advertises it in a stored robots.txt, and never
// argues with a file that already names one.
func TestRobotsTxtGetsSitemapLine(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/standard.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		h := c.Pages()
		robots := func() (int, string) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test/robots.txt", nil))
			return rec.Code, rec.Body.String()
		}

		setMode(t, c, content.ModeProduction)
		setSitemap(t, c, true)

		// Nothing stored: the sitemap does not make the CMS claim
		// /robots.txt, which would take the address from the host app.
		setRobots(t, c, "")
		if code, _ := robots(); code != http.StatusNotFound {
			t.Errorf("empty robots.txt with the sitemap on: status %d, want 404", code)
		}

		setRobots(t, c, "User-agent: *\nDisallow: /private\n")
		code, body := robots()
		if code != http.StatusOK {
			t.Fatalf("status %d, want 200", code)
		}
		if !strings.Contains(body, "Sitemap: http://example.test/sitemap.xml") {
			t.Errorf("robots.txt did not advertise the sitemap:\n%s", body)
		}
		if !strings.Contains(body, "Disallow: /private") {
			t.Errorf("robots.txt lost what was stored:\n%s", body)
		}

		// An author who names their own sitemap keeps exactly that.
		setRobots(t, c, "User-agent: *\nSitemap: http://example.test/mine.xml\n")
		_, body = robots()
		if strings.Contains(body, "/sitemap.xml") {
			t.Errorf("a robots.txt naming its own sitemap got a second line:\n%s", body)
		}

		// Off again: no line, stored file otherwise untouched.
		setSitemap(t, c, false)
		setRobots(t, c, "User-agent: *\nDisallow: /private\n")
		_, body = robots()
		if strings.Contains(body, "Sitemap:") {
			t.Errorf("sitemap off but robots.txt still advertises one:\n%s", body)
		}
	})
}

// The rendered document is cached, so two things have to hold: a
// request arriving under another host name must not be served the first
// one's URLs, and the cache must eventually rebuild.
func TestSitemapCacheIsPerHostAndRebuilds(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/standard.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		h := c.Pages()
		body := func(host string) string {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/sitemap.xml", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d, want 200", host, rec.Code)
			}
			return rec.Body.String()
		}

		setMode(t, c, content.ModeProduction)
		setSitemap(t, c, true)

		first := body("example.test")
		if !strings.Contains(first, "http://example.test/") {
			t.Fatalf("sitemap does not use the requesting host:\n%s", first)
		}
		// No Config.SiteURL here, so the base comes from the request —
		// serving the cached copy would hand out the wrong hostnames.
		second := body("other.test")
		if strings.Contains(second, "example.test") {
			t.Errorf("a request to other.test was served example.test URLs:\n%s", second)
		}
		if !strings.Contains(second, "http://other.test/") {
			t.Errorf("sitemap for other.test:\n%s", second)
		}

		// A page published after the last render appears once the cached
		// copy is dropped.
		addPage(t, c, "later", true, content.VisibilityPublic)
		expireSitemapCache(c)
		if got := body("example.test"); !strings.Contains(got, "http://example.test/later") {
			t.Errorf("a page published after the first render never appeared:\n%s", got)
		}
	})
}

// A brand-new site publishes a sitemap; an upgrade of an existing site
// must not start claiming an address the host app may already answer.
func TestSeedAdminEnablesSitemap(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		site, err := c.content.SiteSettings(ctx)
		if err != nil {
			t.Fatalf("SiteSettings: %v", err)
		}
		if site.Sitemap {
			t.Fatal("a migrated site with no seeding publishes a sitemap; upgrades must not claim /sitemap.xml")
		}

		if _, err := c.SeedAdmin(ctx, "boss@example.com", "Boss", "correct horse battery"); err != nil {
			t.Fatalf("SeedAdmin: %v", err)
		}
		site, err = c.content.SiteSettings(ctx)
		if err != nil {
			t.Fatalf("SiteSettings: %v", err)
		}
		if !site.Sitemap {
			t.Error("a freshly seeded site does not publish a sitemap")
		}
	})
}

// An install without Config.SiteURL takes the sitemap's base from the
// request, and the request's Host is chosen by whoever sent it. Keyed on
// that alone, the cache is one an anonymous client empties on demand — a
// fresh hostname per request, each one paying for a query over every page
// on the site.
//
// The rows do not depend on the base, so they are cached apart from the
// document rendered out of them. This test drives the attack and checks
// the expensive half stayed put, while the cheap half still answers each
// host correctly.
func TestSitemapRowsAreNotRebuiltPerHost(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/standard.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		setMode(t, c, content.ModeProduction)
		setSitemap(t, c, true)

		h := c.Pages()
		body := func(host string) string {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/sitemap.xml", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status %d, want 200", host, rec.Code)
			}
			return rec.Body.String()
		}

		body("first.test")

		// Published between the first request and the rest. If a novel
		// Host re-reads the rows, it shows up — which is the query this
		// is meant to have made unreachable.
		addPage(t, c, "published-later", true, content.VisibilityPublic)

		c.sitemapMu.Lock()
		readAt := c.sitemapAt
		c.sitemapMu.Unlock()

		for _, host := range []string{"a.test", "b.test", "c.test", "d.test", "e.test"} {
			got := body(host)
			if strings.Contains(got, "published-later") {
				t.Fatalf("a request from %s re-read every page in the site — "+
					"varying Host still costs a query each time", host)
			}
			// The document is still right for the host that asked: the
			// fix is about what is recomputed, not about serving one
			// host's URLs to another.
			if !strings.Contains(got, "http://"+host+"/") {
				t.Errorf("%s was served someone else's hostnames:\n%s", host, got)
			}
		}

		c.sitemapMu.Lock()
		stillAt := c.sitemapAt
		c.sitemapMu.Unlock()
		if !stillAt.Equal(readAt) {
			t.Errorf("the page query ran again: read at %v, now %v", readAt, stillAt)
		}

		// And the rows do refresh on their own schedule, so this is a
		// cache rather than a one-shot read.
		expireSitemapCache(c)
		if got := body("first.test"); !strings.Contains(got, "published-later") {
			t.Errorf("the sitemap never picked up a page published earlier:\n%s", got)
		}
	})
}

// With Config.SiteURL set the base is constant, so every request — however
// it addresses the site — is a cache hit and the sitemap names the
// canonical host rather than whatever the request claimed.
func TestSitemapWithSiteURLIgnoresTheRequestHost(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)
		c.cfg.SiteURL = "https://canonical.example"
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/standard.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		setMode(t, c, content.ModeProduction)
		setSitemap(t, c, true)

		h := c.Pages()
		for _, host := range []string{"canonical.example", "attacker.test"} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/sitemap.xml", nil))
			got := rec.Body.String()
			if !strings.Contains(got, "https://canonical.example/") {
				t.Errorf("request as %s: sitemap does not use the configured base:\n%s", host, got)
			}
			if strings.Contains(got, "attacker.test") {
				t.Errorf("request as %s put the request's own host in the sitemap:\n%s", host, got)
			}
		}
	})
}
