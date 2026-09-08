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

// What development mode does to the public site: every response carries
// the noindex header — including the ones no <meta> tag can reach — and
// /robots.txt tells crawlers to stay out. Production says none of it, and
// leaves /robots.txt to the host.

// setMode stores a mode and defeats the settings cache, which would
// otherwise hold the previous answer for a few seconds.
func setMode(t *testing.T, c *CMS, mode string) {
	t.Helper()
	if err := c.content.SetSiteMode(context.Background(), mode); err != nil {
		t.Fatalf("SetSiteMode(%q): %v", mode, err)
	}
	expireSiteCache(c)
}

// setRobots stores a site robots.txt, leaving the rest of the settings
// alone, and defeats the cache the same way.
func setRobots(t *testing.T, c *CMS, body string) {
	t.Helper()
	ctx := context.Background()
	site, err := c.content.SiteSettings(ctx)
	if err != nil {
		t.Fatalf("SiteSettings: %v", err)
	}
	site.RobotsTxt = body
	if err := c.content.SaveSiteSettings(ctx, site); err != nil {
		t.Fatalf("SaveSiteSettings: %v", err)
	}
	expireSiteCache(c)
}

func expireSiteCache(c *CMS) {
	c.siteMu.Lock()
	c.siteAt = time.Time{}
	c.siteMu.Unlock()
}

func TestPagesRobotsFollowSiteMode(t *testing.T) {
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

		get := func(path string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			return rec
		}

		setMode(t, c, content.ModeDevelopment)

		// The header rides on everything, not only on rendered pages:
		// the RSS feeds and the media proxy serve things search engines
		// index in their own right, and a <meta> tag cannot reach them.
		for _, path := range []string{"/", "/nothing-here", "/robots.txt"} {
			if got := get(path).Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
				t.Errorf("development: GET %s X-Robots-Tag = %q, want %q",
					path, got, "noindex, nofollow")
			}
		}

		rec := get("/robots.txt")
		if rec.Code != http.StatusOK {
			t.Fatalf("development: GET /robots.txt status %d, want 200", rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "Disallow: /") {
			t.Errorf("development: /robots.txt = %q, want a Disallow", body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("development: /robots.txt Content-Type = %q, want text/plain", ct)
		}
		// A cached copy would outlive the switch to production.
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("development: /robots.txt Cache-Control = %q, want no-store", cc)
		}

		setMode(t, c, content.ModeProduction)

		if got := get("/").Header().Get("X-Robots-Tag"); got != "" {
			t.Errorf("production: X-Robots-Tag = %q, want no header at all", got)
		}
		// With nothing stored, the CMS does not claim /robots.txt in
		// production, so a host serving its own keeps serving it once the
		// site goes live. Unclaimed, the path is an ordinary page lookup,
		// and no page has that slug.
		if rec := get("/robots.txt"); rec.Code != http.StatusNotFound {
			t.Errorf("production: GET /robots.txt status %d, want 404 (the CMS should not claim it)",
				rec.Code)
		}
	})
}

// A robots.txt stored in the site settings is served verbatim once the
// site is live — and is ignored while it is not, where the development
// Disallow has to win.
func TestPagesServeStoredRobotsTxt(t *testing.T) {
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

		get := func(path string) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			return rec
		}

		const stored = "User-agent: *\nDisallow: /admin\n\nSitemap: https://example.com/sitemap.xml\n"

		setMode(t, c, content.ModeProduction)
		setRobots(t, c, stored)

		rec := get("/robots.txt")
		if rec.Code != http.StatusOK {
			t.Fatalf("production: GET /robots.txt status %d, want 200", rec.Code)
		}
		if body := rec.Body.String(); body != stored {
			t.Errorf("production: /robots.txt = %q, want the stored file %q", body, stored)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("production: /robots.txt Content-Type = %q, want text/plain", ct)
		}
		// An edit has to take effect when it is made.
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("production: /robots.txt Cache-Control = %q, want no-store", cc)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "" {
			t.Errorf("production: X-Robots-Tag = %q, want no header at all", got)
		}

		// Back into development: the stored file is written for the live
		// site and must not be handed to a crawler while the site is
		// being hidden.
		setMode(t, c, content.ModeDevelopment)

		rec = get("/robots.txt")
		if rec.Code != http.StatusOK {
			t.Fatalf("development: GET /robots.txt status %d, want 200", rec.Code)
		}
		if body := rec.Body.String(); body != robotsTxt {
			t.Errorf("development: /robots.txt = %q, want the development Disallow %q", body, robotsTxt)
		}

		// Clearing it gives the path back to the host.
		setMode(t, c, content.ModeProduction)
		setRobots(t, c, "")
		if rec := get("/robots.txt"); rec.Code != http.StatusNotFound {
			t.Errorf("production: GET /robots.txt status %d after clearing, want 404", rec.Code)
		}
	})
}

// A site being set up for the first time starts in development, so it
// cannot be indexed while it is being built. An existing site — one that
// already has users — is left in production, where it has been all along.
func TestSeedAdminStartsSiteInDevelopment(t *testing.T) {
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
		if site.Development() {
			t.Fatal("a migrated site with no seeding is in development; upgrades must not deindex a live site")
		}

		created, err := c.SeedAdmin(ctx, "boss@example.com", "Boss", "correct horse battery")
		if err != nil {
			t.Fatalf("SeedAdmin: %v", err)
		}
		if !created {
			t.Fatal("SeedAdmin created no user on an empty site")
		}
		site, err = c.content.SiteSettings(ctx)
		if err != nil {
			t.Fatalf("SiteSettings: %v", err)
		}
		if !site.Development() {
			t.Errorf("a freshly seeded site is in mode %q, want development", site.Mode)
		}

		// Go live, then seed again: a no-op call must not drag the site
		// back into development.
		setMode(t, c, content.ModeProduction)
		created, err = c.SeedAdmin(ctx, "other@example.com", "Other", "correct horse battery")
		if err != nil {
			t.Fatalf("second SeedAdmin: %v", err)
		}
		if created {
			t.Fatal("SeedAdmin created a second user")
		}
		site, err = c.content.SiteSettings(ctx)
		if err != nil {
			t.Fatalf("SiteSettings: %v", err)
		}
		if site.Development() {
			t.Error("SeedAdmin put a live site back into development")
		}
	})
}

// The settings used to be read twice on the way through a page render:
// once from a short-lived cache, to stamp the response headers, and once
// straight from the database, to render the page. Two readings of one
// table moments apart, which bought nothing — the page path paid for the
// query anyway — and could disagree inside a single response, the header
// saying noindex from the cached copy while the freshly-read one rendered
// no meta tag to match.
//
// This drives that window: change the mode without expiring the cache, so
// the two readings would have come from different moments, and check the
// response agrees with itself either way.
func TestPageHeadersAndBodyAgreeOnTheSiteMode(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		// A base template that calls {{cmsHead}}, which is what emits the
		// robots meta tag — the body half of the agreement.
		fsys := fstest.MapFS{
			"templates/base.gohtml": &fstest.MapFile{Data: []byte(
				`{{define "base"}}<html><head>{{cmsHead}}</head><body>{{block "content" .}}{{end}}</body></html>{{end}}`)},
			"templates/pages/standard.gohtml": &fstest.MapFile{Data: []byte(
				`{{template "base" .}}{{define "content"}}<main>{{cmsRegion "main"}}</main>{{end}}`)},
		}
		c, err := New(Config{
			DB:              db.SQL(),
			Dialect:         db.Dialect().Name(),
			Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
			TemplateFS:      fsys,
			SharedTemplates: []string{"templates/base.gohtml"},
			PageTemplates:   []PageTemplate{{File: "templates/pages/standard.gohtml", Label: "Standard"}},
		})
		if err != nil {
			t.Fatalf("cms.New: %v", err)
		}
		if err := c.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "templates/pages/standard.gohtml", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		h := c.Pages()

		// header says noindex, body carries the robots meta tag.
		get := func() (bool, bool) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200", rec.Code)
			}
			return rec.Header().Get("X-Robots-Tag") == robotsDirective,
				strings.Contains(rec.Body.String(), `name="robots"`)
		}

		setMode(t, c, content.ModeDevelopment)
		if header, body := get(); !header || !body {
			t.Fatalf("development: header says noindex = %v, body carries the meta tag = %v; want both", header, body)
		}

		// The window: the stored mode changes, the cached copy does not.
		// Whichever answer the response settles on, both halves must give
		// the same one.
		setModeOnly(t, c, content.ModeProduction)
		if header, body := get(); header != body {
			t.Errorf("the response disagrees with itself: header says noindex = %v, body carries the meta tag = %v",
				header, body)
		}

		// And once the cache turns over, both follow.
		expireSiteCache(c)
		if header, body := get(); header || body {
			t.Errorf("production: still asking not to be indexed (header %v, meta %v)", header, body)
		}
	})
}

// setModeOnly changes the stored mode without expiring the cached copy,
// so a test can stand inside the window where the two readings disagreed.
func setModeOnly(t *testing.T, c *CMS, mode string) {
	t.Helper()
	if err := c.content.SetSiteMode(context.Background(), mode); err != nil {
		t.Fatalf("SetSiteMode(%q): %v", mode, err)
	}
}
