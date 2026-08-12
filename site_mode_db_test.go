package cms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// What development mode does to the public site: every response carries
// the noindex header — including the ones no <meta> tag can reach — and
// /robots.txt tells crawlers to stay out. Production says none of it, and
// leaves /robots.txt to the host.

// setMode stores a mode and defeats the mode cache, which would
// otherwise hold the previous answer for a few seconds.
func setMode(t *testing.T, c *CMS, mode string) {
	t.Helper()
	if err := c.content.SetSiteMode(context.Background(), mode); err != nil {
		t.Fatalf("SetSiteMode(%q): %v", mode, err)
	}
	c.modeMu.Lock()
	c.modeAt = time.Time{}
	c.modeMu.Unlock()
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
		// The CMS claims /robots.txt only while the site is in
		// development, so a host serving its own keeps serving it once
		// the site goes live. Unclaimed, the path is an ordinary page
		// lookup, and no page has that slug.
		if rec := get("/robots.txt"); rec.Code != http.StatusNotFound {
			t.Errorf("production: GET /robots.txt status %d, want 404 (the CMS should not claim it)",
				rec.Code)
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
