package content_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// Shared content is stored once for the site and comes back beside — never
// mixed into — the page's own blocks.
func TestSharedBlocksTravelWithThePage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "about"}, defaultLocale)

		if err := s.UpsertDraftBlock(ctx, p.ID, "main", defaultLocale, content.KindHTML, "<p>page</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.UpsertSharedBlock(ctx, "footer", defaultLocale, content.KindHTML, "<p>site</p>"); err != nil {
			t.Fatalf("UpsertSharedBlock: %v", err)
		}

		blocks, shared, err := s.EffectiveBlocksWithShared(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("EffectiveBlocksWithShared: %v", err)
		}
		if len(blocks) != 1 || blocks[0].Content != "<p>page</p>" {
			t.Errorf("page blocks = %+v, want just the page's own", blocks)
		}
		if len(shared) != 1 || shared[0].Content != "<p>site</p>" {
			t.Errorf("shared blocks = %+v, want just the site's", shared)
		}

		// The same shared content shows up on every other page too — that
		// is the whole point of it.
		other := seedPage(t, s, content.Page{Slug: "contact"}, defaultLocale)
		_, shared, err = s.EffectiveBlocksWithShared(ctx, other.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("EffectiveBlocksWithShared(other): %v", err)
		}
		if len(shared) != 1 || shared[0].Content != "<p>site</p>" {
			t.Errorf("shared blocks on a second page = %+v, want the same site content", shared)
		}

		// A page region and a shared region may share a name without
		// either one standing in for the other.
		if err := s.UpsertDraftBlock(ctx, p.ID, "footer", defaultLocale, content.KindHTML, "<p>page footer</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(footer): %v", err)
		}
		blocks, shared, err = s.EffectiveBlocksWithShared(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("EffectiveBlocksWithShared: %v", err)
		}
		if len(blocks) != 2 {
			t.Errorf("page blocks = %+v, want the page's main and footer", blocks)
		}
		if len(shared) != 1 || shared[0].Content != "<p>site</p>" {
			t.Errorf("shared blocks = %+v, want the site footer untouched", shared)
		}
	})
}

// Locale fallback is decided per page and region, so a translated shared
// footer does not stop an untranslated page region of the same name from
// falling back, or the reverse.
func TestSharedBlocksFallBackPerRegion(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "about"}, defaultLocale)

		if err := s.UpsertDraftBlock(ctx, p.ID, "footer", defaultLocale, content.KindHTML, "<p>page en</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.UpsertSharedBlock(ctx, "footer", defaultLocale, content.KindHTML, "<p>site en</p>"); err != nil {
			t.Fatalf("UpsertSharedBlock(en): %v", err)
		}
		if err := s.UpsertSharedBlock(ctx, "footer", "fr", content.KindHTML, "<p>site fr</p>"); err != nil {
			t.Fatalf("UpsertSharedBlock(fr): %v", err)
		}

		blocks, shared, err := s.EffectiveBlocksWithShared(ctx, p.ID, "fr", content.StatusDraft)
		if err != nil {
			t.Fatalf("EffectiveBlocksWithShared: %v", err)
		}
		if len(blocks) != 1 || blocks[0].Content != "<p>page en</p>" || blocks[0].Locale != defaultLocale {
			t.Errorf("page blocks = %+v, want the untranslated page footer falling back to en", blocks)
		}
		if len(shared) != 1 || shared[0].Content != "<p>site fr</p>" {
			t.Errorf("shared blocks = %+v, want only the French shared footer", shared)
		}
	})
}

// Shared content rides the ordinary draft/publish workflow: saved edits are
// invisible to the public until a publish, which any page's publish
// performs.
func TestSharedContentPublishes(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		if err := s.UpsertSharedBlock(ctx, "footer", defaultLocale, content.KindHTML, "<p>draft</p>"); err != nil {
			t.Fatalf("UpsertSharedBlock: %v", err)
		}
		live, err := s.SharedBlocks(ctx, defaultLocale, content.StatusPublished)
		if err != nil {
			t.Fatalf("SharedBlocks: %v", err)
		}
		if len(live) != 0 {
			t.Errorf("published shared blocks = %+v, want none before publishing", live)
		}
		changed, err := s.HasSharedUnpublishedChanges(ctx)
		if err != nil {
			t.Fatalf("HasSharedUnpublishedChanges: %v", err)
		}
		if !changed {
			t.Error("a saved shared edit did not read as an unpublished change")
		}

		if err := s.PublishShared(ctx); err != nil {
			t.Fatalf("PublishShared: %v", err)
		}
		live, err = s.SharedBlocks(ctx, defaultLocale, content.StatusPublished)
		if err != nil {
			t.Fatalf("SharedBlocks: %v", err)
		}
		if len(live) != 1 || live[0].Content != "<p>draft</p>" {
			t.Errorf("published shared blocks = %+v, want the published draft", live)
		}
		changed, err = s.HasSharedUnpublishedChanges(ctx)
		if err != nil {
			t.Fatalf("HasSharedUnpublishedChanges: %v", err)
		}
		if changed {
			t.Error("shared content still reads as changed right after publishing")
		}
	})
}

// The site page is not a page: it must not surface in any listing, count,
// or slug lookup, and it must not be deletable.
func TestSitePageIsInvisible(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		seedPage(t, s, content.Page{Slug: "about"}, defaultLocale)
		siteID, err := s.SitePageID(ctx)
		if err != nil {
			t.Fatalf("SitePageID: %v", err)
		}

		all, err := s.All(ctx, defaultLocale)
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		if len(all) != 1 || all[0].Slug != "about" {
			t.Errorf("All = %+v, want only the real page", all)
		}
		nonPost, err := s.AllNonPost(ctx, defaultLocale)
		if err != nil {
			t.Fatalf("AllNonPost: %v", err)
		}
		if len(nonPost) != 1 {
			t.Errorf("AllNonPost returned %d pages, want 1", len(nonPost))
		}
		if n, err := s.CountNonPost(ctx); err != nil || n != 1 {
			t.Errorf("CountNonPost = %d, %v; want 1", n, err)
		}
		if pages, _, err := s.Counts(ctx); err != nil || pages != 1 {
			t.Errorf("Counts pages = %d, %v; want 1", pages, err)
		}
		if _, err := s.GetBySlug(ctx, content.SiteSlug, defaultLocale, false); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("GetBySlug(%q) = %v, want ErrNotFound — the site page is not routable", content.SiteSlug, err)
		}
		if err := s.Delete(ctx, siteID); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("Delete(site page) = %v, want ErrNotFound — it must not be deletable", err)
		}
	})
}

// The site page is recreated on demand, so a database emptied behind the
// CMS's back (a test harness truncating, a hand-cleaned install) can still
// save shared content.
func TestSitePageIsRecreatedWhenMissing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		first, err := s.SitePageID(ctx)
		if err != nil {
			t.Fatalf("SitePageID: %v", err)
		}
		if _, err := db.Exec(ctx, "DELETE FROM cms_pages WHERE id = $1", first); err != nil {
			t.Fatalf("deleting the site page: %v", err)
		}
		second, err := s.SitePageID(ctx)
		if err != nil {
			t.Fatalf("SitePageID after deletion: %v", err)
		}
		if second == 0 {
			t.Fatal("SitePageID returned no id after the row was deleted")
		}
		if err := s.UpsertSharedBlock(ctx, "footer", defaultLocale, content.KindHTML, "<p>back</p>"); err != nil {
			t.Fatalf("UpsertSharedBlock after recreation: %v", err)
		}
	})
}
