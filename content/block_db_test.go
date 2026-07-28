package content_test

import (
	"context"
	"testing"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
)

func TestBlockUpsertDraftBlock(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "blocks"}, defaultLocale)

		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>first</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(insert): %v", err)
		}
		blocks, err := s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("got %d blocks, want 1", len(blocks))
		}
		if blocks[0].Content != "<p>first</p>" || blocks[0].Kind != content.KindHTML {
			t.Errorf("block = %+v, want the inserted html content", blocks[0])
		}
		if blocks[0].Sort != 0 {
			t.Errorf("sort = %d, want 0", blocks[0].Sort)
		}

		// The second call must update the same row rather than adding one —
		// this is the upsert path under test.
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindText, "second"); err != nil {
			t.Fatalf("UpsertDraftBlock(update): %v", err)
		}
		blocks, err = s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(blocks) != 1 {
			t.Fatalf("upsert created a second row: got %d blocks, want 1", len(blocks))
		}
		if blocks[0].Content != "second" || blocks[0].Kind != content.KindText {
			t.Errorf("block after upsert = %+v, want the updated content and kind", blocks[0])
		}
	})
}

func TestBlockReplaceDraftSections(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "sections"}, defaultLocale)

		in := []content.SectionInput{
			{Content: "<p>one</p>", Settings: map[string]string{"bg": "paper", "width": "wide"}},
			{Content: "<p>two</p>", Settings: map[string]string{"bg": "spruce"}},
			{Content: "<p>three</p>"}, // nil settings must store as {}
		}
		if err := s.ReplaceDraftSections(ctx, p.ID, "main", defaultLocale, in); err != nil {
			t.Fatalf("ReplaceDraftSections: %v", err)
		}

		blocks, err := s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(blocks) != 3 {
			t.Fatalf("got %d blocks, want 3", len(blocks))
		}
		for i, want := range []string{"<p>one</p>", "<p>two</p>", "<p>three</p>"} {
			if blocks[i].Content != want {
				t.Errorf("block %d content = %q, want %q", i, blocks[i].Content, want)
			}
			if blocks[i].Sort != i {
				t.Errorf("block %d sort = %d, want %d", i, blocks[i].Sort, i)
			}
		}
		// The JSON settings column has to survive the round trip intact.
		if got := blocks[0].Settings["bg"]; got != "paper" {
			t.Errorf("block 0 settings[bg] = %q, want %q", got, "paper")
		}
		if got := blocks[0].Settings["width"]; got != "wide" {
			t.Errorf("block 0 settings[width] = %q, want %q", got, "wide")
		}
		if len(blocks[2].Settings) != 0 {
			t.Errorf("block 2 settings = %v, want empty", blocks[2].Settings)
		}

		// Replacing is wholesale, and an empty list clears the region.
		if err := s.ReplaceDraftSections(ctx, p.ID, "main", defaultLocale, nil); err != nil {
			t.Fatalf("ReplaceDraftSections(empty): %v", err)
		}
		blocks, err = s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(blocks) != 0 {
			t.Errorf("got %d blocks after clearing, want 0", len(blocks))
		}
	})
}

func TestBlockEffectiveBlocksLocaleFallback(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "fallback"}, defaultLocale)

		// English fills two regions; French overrides only one.
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>en body</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(en body): %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, p.ID, "aside", defaultLocale, content.KindHTML, "<p>en aside</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(en aside): %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", "fr", content.KindHTML, "<p>fr body</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(fr body): %v", err)
		}

		blocks, err := s.EffectiveBlocks(ctx, p.ID, "fr", content.StatusDraft)
		if err != nil {
			t.Fatalf("EffectiveBlocks: %v", err)
		}
		got := map[string]content.Block{}
		for _, b := range blocks {
			got[b.Region] = b
		}
		if len(got) != 2 {
			t.Fatalf("got regions %v, want body and aside", got)
		}
		// The localized region uses French...
		if got["body"].Content != "<p>fr body</p>" || got["body"].Locale != "fr" {
			t.Errorf("body = %+v, want the French block", got["body"])
		}
		// ...and the untranslated one falls back wholesale to English, with
		// its Locale field left as the fallback so callers can tell.
		if got["aside"].Content != "<p>en aside</p>" || got["aside"].Locale != defaultLocale {
			t.Errorf("aside = %+v, want the English fallback block", got["aside"])
		}
	})
}

func TestBlockDeleteLocaleContent(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "delocalize", Title: "English"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>en</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(en): %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", "fr", content.KindHTML, "<p>fr</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(fr): %v", err)
		}
		if err := s.UpdateMeta(ctx, p.ID, "fr", "Français", "Description"); err != nil {
			t.Fatalf("UpdateMeta(fr): %v", err)
		}

		if err := s.DeleteLocaleContent(ctx, p.ID, "fr"); err != nil {
			t.Fatalf("DeleteLocaleContent: %v", err)
		}

		frBlocks, err := s.BlocksFor(ctx, p.ID, "fr", content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor(fr): %v", err)
		}
		if len(frBlocks) != 0 {
			t.Errorf("got %d French blocks, want 0", len(frBlocks))
		}
		// The page now reads as English fallback in French.
		fr, err := s.GetByID(ctx, p.ID, "fr")
		if err != nil {
			t.Fatalf("GetByID(fr): %v", err)
		}
		if fr.Title != "English" {
			t.Errorf("fr title = %q, want the English fallback", fr.Title)
		}
		// English content is untouched.
		enBlocks, err := s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor(en): %v", err)
		}
		if len(enBlocks) != 1 {
			t.Errorf("got %d English blocks, want 1", len(enBlocks))
		}
	})
}

func TestBlockHasUnpublishedChanges(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		mustBe := func(t *testing.T, pageID int64, want bool, when string) {
			t.Helper()
			got, err := s.HasUnpublishedChanges(ctx, pageID)
			if err != nil {
				t.Fatalf("HasUnpublishedChanges (%s): %v", when, err)
			}
			if got != want {
				t.Errorf("HasUnpublishedChanges (%s) = %v, want %v", when, got, want)
			}
		}

		p := seedPage(t, s, content.Page{Slug: "dirty", Title: "Title"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>v1</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		// A never-published page has draft rows and no published ones.
		mustBe(t, p.ID, true, "before first publish")

		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		mustBe(t, p.ID, false, "just published")

		// Block content differing.
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>v2</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(edit): %v", err)
		}
		mustBe(t, p.ID, true, "block edited")
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		mustBe(t, p.ID, false, "republished")

		// Metadata differing.
		if err := s.UpdateMeta(ctx, p.ID, defaultLocale, "New Title", ""); err != nil {
			t.Fatalf("UpdateMeta: %v", err)
		}
		mustBe(t, p.ID, true, "metadata edited")
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		mustBe(t, p.ID, false, "republished after metadata")

		// A staged page-level field differing (the IS DISTINCT FROM probe).
		p.HeadCSS = "body{}"
		if err := s.Update(ctx, p, defaultLocale); err != nil {
			t.Fatalf("Update: %v", err)
		}
		mustBe(t, p.ID, true, "page-level field edited")

		// And discarding brings it back to clean.
		if err := s.DiscardDraft(ctx, p.ID); err != nil {
			t.Fatalf("DiscardDraft: %v", err)
		}
		mustBe(t, p.ID, false, "after discard")

		// A block added in a second locale counts, since Publish snapshots
		// every locale at once.
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", "fr", content.KindHTML, "<p>fr</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(fr): %v", err)
		}
		mustBe(t, p.ID, true, "block added in another locale")
	})
}

func TestBlockPublishSnapshotsAllLocales(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "multi"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>en</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(en): %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", "fr", content.KindHTML, "<p>fr</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(fr): %v", err)
		}

		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		for _, locale := range []string{defaultLocale, "fr"} {
			blocks, err := s.BlocksFor(ctx, p.ID, locale, content.StatusPublished)
			if err != nil {
				t.Fatalf("BlocksFor(%s, published): %v", locale, err)
			}
			if len(blocks) != 1 {
				t.Errorf("locale %s: got %d published blocks, want 1", locale, len(blocks))
			}
		}
	})
}
