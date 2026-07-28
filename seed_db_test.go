package cms

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// seedTestFS is the minimum a host must supply for pages to render: a shared
// layout and one page template.
var seedTestFS = fstest.MapFS{
	"templates/base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><body>{{block "content" .}}{{end}}</body></html>{{end}}`)},
	"templates/pages/canvas.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<h1>{{.Title}}</h1>{{cmsSections "sections"}}{{end}}`)},
	"templates/pages/standard.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<main>{{cmsRegion "main"}}</main>{{end}}`)},
}

// newSeedTestCMS returns a CMS with templates configured, so SeedHomePage
// has something valid to point a page at.
func newSeedTestCMS(t *testing.T, db *sqldb.DB) *CMS {
	t.Helper()
	c, err := New(Config{
		DB:              db.SQL(),
		Dialect:         db.Dialect().Name(),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		TemplateFS:      seedTestFS,
		SharedTemplates: []string{"templates/base.gohtml"},
		PageTemplates: []PageTemplate{
			{File: "templates/pages/canvas.gohtml", Label: "Blank canvas"},
			{File: "templates/pages/standard.gohtml", Label: "Standard page"},
		},
	})
	if err != nil {
		t.Fatalf("cms.New: %v", err)
	}
	return c
}

func TestSeedHomePage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)

		created, err := c.SeedHomePage(ctx, "templates/pages/canvas.gohtml", "Welcome")
		if err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		if !created {
			t.Fatal("SeedHomePage reported no page created on an empty site")
		}

		// The empty slug is what "/" resolves to, and it must be published
		// or a fresh install still serves a 404 there.
		page, err := c.content.GetBySlug(ctx, "", "en", true)
		if err != nil {
			t.Fatalf("GetBySlug(\"\", publishedOnly): %v", err)
		}
		if page.Title != "Welcome" {
			t.Errorf("title = %q, want %q", page.Title, "Welcome")
		}
		if page.TemplateName != "templates/pages/canvas.gohtml" {
			t.Errorf("template = %q, want the blank canvas", page.TemplateName)
		}
		if page.Status != "published" {
			t.Errorf("status = %q, want published", page.Status)
		}
	})
}

func TestSeedHomePageIsIdempotent(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)

		if _, err := c.SeedHomePage(ctx, "", "First"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		// A second call must not create a duplicate, and must not overwrite
		// whatever the site has done with the page since.
		created, err := c.SeedHomePage(ctx, "", "Second")
		if err != nil {
			t.Fatalf("SeedHomePage(second): %v", err)
		}
		if created {
			t.Error("SeedHomePage created a second home page")
		}
		page, err := c.content.GetBySlug(ctx, "", "en", false)
		if err != nil {
			t.Fatalf("GetBySlug: %v", err)
		}
		if page.Title != "First" {
			t.Errorf("title = %q, want the original %q — the re-run overwrote it", page.Title, "First")
		}
		pages, _, err := c.content.Counts(ctx)
		if err != nil {
			t.Fatalf("Counts: %v", err)
		}
		if pages != 1 {
			t.Errorf("page count = %d, want 1", pages)
		}
	})
}

func TestSeedHomePageSkipsNonEmptySite(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)

		// A site that already has content is not a fresh install, even when
		// nothing occupies the home slug. Seeding there would be a surprise.
		existing := &content.Page{Slug: "about", TemplateName: "templates/pages/standard.gohtml", Title: "About"}
		if _, err := c.content.Insert(ctx, existing, "en"); err != nil {
			t.Fatalf("seeding an existing page: %v", err)
		}

		created, err := c.SeedHomePage(ctx, "", "Welcome")
		if err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		if created {
			t.Error("SeedHomePage created a home page on a site that already had content")
		}
	})
}

func TestSeedHomePageRejectsUnconfiguredTemplate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)

		// A page naming a template the host has not configured fails to
		// render, so refusing up front beats writing an unrenderable row.
		_, err := c.SeedHomePage(ctx, "templates/pages/nope.gohtml", "Welcome")
		if err == nil {
			t.Fatal("SeedHomePage accepted a template that is not configured")
		}
		if !strings.Contains(err.Error(), "not one of Config.PageTemplates") {
			t.Errorf("error = %v, want it to name the problem", err)
		}
		if _, _, err := c.content.Counts(ctx); err != nil {
			t.Fatalf("Counts: %v", err)
		}
		if _, err := c.content.GetBySlug(ctx, "", "en", false); err == nil {
			t.Error("a page was written despite the rejected template")
		}
	})
}

func TestSeedHomePageDefaultsToFirstTemplate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c := newSeedTestCMS(t, db)

		if _, err := c.SeedHomePage(ctx, "", "Welcome"); err != nil {
			t.Fatalf("SeedHomePage: %v", err)
		}
		page, err := c.content.GetBySlug(ctx, "", "en", true)
		if err != nil {
			t.Fatalf("GetBySlug: %v", err)
		}
		if page.TemplateName != "templates/pages/canvas.gohtml" {
			t.Errorf("template = %q, want the first configured one", page.TemplateName)
		}
	})
}

func TestSeedHomePageNeedsATemplate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		c, err := New(Config{
			DB:      db.SQL(),
			Dialect: db.Dialect().Name(),
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("cms.New: %v", err)
		}
		if _, err := c.SeedHomePage(ctx, "", "Welcome"); err == nil {
			t.Error("SeedHomePage succeeded with no Config.PageTemplates")
		}
	})
}
