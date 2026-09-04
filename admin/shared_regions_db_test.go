package admin

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/render"
)

// A shared region is edited on whichever page the editor happens to be
// standing on, so its save arrives in the same request as that page's
// regions. What keeps the two apart is the marker prefix.

func TestSplitSharedRegions(t *testing.T) {
	page, shared := splitSharedRegions(map[string]string{
		"main":            "<p>page</p>",
		"site:footer":     "<p>site</p>",
		"footer":          "<p>this page's own footer</p>",
		"site:contact":    "<p>call us</p>",
		"nested:site:odd": "<p>not shared</p>",
	})
	wantPage := map[string]string{
		"main":            "<p>page</p>",
		"footer":          "<p>this page's own footer</p>",
		"nested:site:odd": "<p>not shared</p>",
	}
	wantShared := map[string]string{"footer": "<p>site</p>", "contact": "<p>call us</p>"}
	for name, want := range wantPage {
		if page[name] != want {
			t.Errorf("page[%q] = %q, want %q", name, page[name], want)
		}
	}
	if len(page) != len(wantPage) {
		t.Errorf("page regions = %v, want %v", page, wantPage)
	}
	for name, want := range wantShared {
		if shared[name] != want {
			t.Errorf("shared[%q] = %q, want %q", name, shared[name], want)
		}
	}
	if len(shared) != len(wantShared) {
		t.Errorf("shared regions = %v, want %v", shared, wantShared)
	}
}

// sharedServer wires a server to a real store and a template set whose
// layout declares one shared region.
func sharedServer(t *testing.T, db *sqldb.DB) *server {
	t.Helper()
	fsys := fstest.MapFS{
		"base.gohtml": &fstest.MapFile{Data: []byte(
			`{{define "base"}}<html><body>{{block "content" .}}{{end}}` +
				`<footer>{{cmsShared "footer"}}</footer></body></html>{{end}}`)},
		"page.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}{{cmsRegion "main"}}{{end}}`)},
	}
	r, err := render.New(fsys, []string{"base.gohtml"},
		[]render.PageTemplate{{File: "page.gohtml", Label: "Page"}}, nil)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	return &server{deps: Deps{
		Content:       content.NewStore(db, "en"),
		Renderer:      r,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		DefaultLocale: "en",
		Locales:       []string{"en"},
	}}
}

func TestSaveSharedRegionsStoresAgainstTheSite(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sharedServer(t, db)
		ctx := context.Background()
		page := &content.Page{Slug: "about", TemplateName: "page.gohtml"}
		if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		if err := s.saveSharedRegions(ctx, map[string]string{
			"footer": "<p>Shared</p>",
			// Not declared by any template, so not storable.
			"invented": "<p>nope</p>",
		}, true, "en"); err != nil {
			t.Fatalf("saveSharedRegions: %v", err)
		}

		_, shared, err := s.deps.Content.EffectiveBlocksWithShared(ctx, page.ID, "en", content.StatusDraft)
		if err != nil {
			t.Fatalf("EffectiveBlocksWithShared: %v", err)
		}
		if len(shared) != 1 || shared[0].Region != "footer" || shared[0].Content != "<p>Shared</p>" {
			t.Fatalf("shared blocks = %+v, want only the declared footer", shared)
		}

		// Non-admins' HTML is sanitized on the shared path as it is on the
		// page path — the footer is on every page, so it is the last place
		// that should be a hole in the policy.
		if err := s.saveSharedRegions(ctx, map[string]string{
			"footer": `<p onclick="steal()">Hi</p><script>evil()</script>`,
		}, false, "en"); err != nil {
			t.Fatalf("saveSharedRegions(non-admin): %v", err)
		}
		_, shared, err = s.deps.Content.EffectiveBlocksWithShared(ctx, page.ID, "en", content.StatusDraft)
		if err != nil {
			t.Fatalf("EffectiveBlocksWithShared: %v", err)
		}
		if len(shared) != 1 {
			t.Fatalf("shared blocks = %+v, want one", shared)
		}
		if got := shared[0].Content; got != "<p>Hi</p>" {
			t.Errorf("sanitized shared content = %q, want %q", got, "<p>Hi</p>")
		}
	})
}

// Publishing a page publishes the site's shared content with it: shared
// regions render on every page, so there is no page of their own to
// publish them from.
func TestPublishWithSharedGoesLiveTogether(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := sharedServer(t, db)
		ctx := context.Background()
		page := &content.Page{Slug: "about", TemplateName: "page.gohtml"}
		if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en", content.KindHTML, "<p>Page</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.saveSharedRegions(ctx, map[string]string{"footer": "<p>Shared</p>"}, true, "en"); err != nil {
			t.Fatalf("saveSharedRegions: %v", err)
		}

		if err := s.publishWithShared(ctx, page.ID, nil); err != nil {
			t.Fatalf("publishWithShared: %v", err)
		}

		blocks, shared, err := s.deps.Content.EffectiveBlocksWithShared(ctx, page.ID, "en", content.StatusPublished)
		if err != nil {
			t.Fatalf("EffectiveBlocksWithShared: %v", err)
		}
		if len(blocks) != 1 || blocks[0].Content != "<p>Page</p>" {
			t.Errorf("published page blocks = %+v, want the page's content", blocks)
		}
		if len(shared) != 1 || shared[0].Content != "<p>Shared</p>" {
			t.Errorf("published shared blocks = %+v, want the footer live too", shared)
		}
		changed, err := s.deps.Content.HasSharedUnpublishedChanges(ctx)
		if err != nil {
			t.Fatalf("HasSharedUnpublishedChanges: %v", err)
		}
		if changed {
			t.Error("shared content still reads as unpublished after a page publish")
		}
	})
}
