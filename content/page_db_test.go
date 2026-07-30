package content_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsawler/cms/internal/sqldb"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
)

const defaultLocale = "en"

// seedPage inserts a draft page for locale, defaulting the fields a test
// does not care about, and returns it with its id filled in.
func seedPage(t *testing.T, s *content.Store, p content.Page, locale string) *content.Page {
	t.Helper()
	if p.TemplateName == "" {
		p.TemplateName = "page.gohtml"
	}
	if locale == "" {
		locale = defaultLocale
	}
	if _, err := s.Insert(context.Background(), &p, locale); err != nil {
		t.Fatalf("seeding page %q: %v", p.Slug, err)
	}
	return &p
}

func TestPageInsertAndGet(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		want := seedPage(t, s, content.Page{
			Slug:         "about",
			TemplateName: "about.gohtml",
			HeadCSS:      "h1{color:red}",
			BodyJS:       "console.log(1)",
			Title:        "About Us",
			Description:  "Who we are",
		}, defaultLocale)
		if want.ID == 0 {
			t.Fatal("Insert did not set the page's id")
		}

		got, err := s.GetByID(ctx, want.ID, defaultLocale)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Slug != "about" {
			t.Errorf("slug = %q, want %q", got.Slug, "about")
		}
		if got.Title != "About Us" {
			t.Errorf("title = %q, want %q", got.Title, "About Us")
		}
		if got.Description != "Who we are" {
			t.Errorf("description = %q, want %q", got.Description, "Who we are")
		}
		if got.HeadCSS != "h1{color:red}" {
			t.Errorf("head_css = %q, want %q", got.HeadCSS, "h1{color:red}")
		}
		// Insert documents that new pages always start as drafts, public.
		if got.Status != content.StatusDraft {
			t.Errorf("status = %q, want %q", got.Status, content.StatusDraft)
		}
		if got.Visibility != content.VisibilityPublic {
			t.Errorf("visibility = %q, want %q", got.Visibility, content.VisibilityPublic)
		}
	})
}

func TestPageInsertDuplicateSlug(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		seedPage(t, s, content.Page{Slug: "taken"}, defaultLocale)

		dup := content.Page{Slug: "taken", TemplateName: "page.gohtml"}
		if _, err := s.Insert(ctx, &dup, defaultLocale); !errors.Is(err, content.ErrDuplicateSlug) {
			t.Fatalf("Insert(duplicate slug) = %v, want ErrDuplicateSlug", err)
		}
	})
}

func TestPageGetBySlugPublishedOnly(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "draft-page", Title: "Draft"}, defaultLocale)

		// A draft page is invisible to the published read path...
		if _, err := s.GetBySlug(ctx, "draft-page", defaultLocale, true); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("GetBySlug(draft, publishedOnly) = %v, want ErrNotFound", err)
		}
		// ...but the editor's working-copy read finds it.
		if _, err := s.GetBySlug(ctx, "draft-page", defaultLocale, false); err != nil {
			t.Errorf("GetBySlug(draft, working copy) = %v, want success", err)
		}

		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		got, err := s.GetBySlug(ctx, "draft-page", defaultLocale, true)
		if err != nil {
			t.Fatalf("GetBySlug(published) = %v, want success", err)
		}
		if got.Title != "Draft" {
			t.Errorf("title = %q, want %q", got.Title, "Draft")
		}
	})
}

func TestPageMetadataLocaleFallback(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{
			Slug:        "bilingual",
			Title:       "English Title",
			Description: "English description",
		}, defaultLocale)

		// French gets a title but deliberately no description.
		if err := s.UpdateMeta(ctx, p.ID, "fr", "Titre Français", ""); err != nil {
			t.Fatalf("UpdateMeta(fr): %v", err)
		}

		got, err := s.GetByID(ctx, p.ID, "fr")
		if err != nil {
			t.Fatalf("GetByID(fr): %v", err)
		}
		if got.Title != "Titre Français" {
			t.Errorf("fr title = %q, want the French override", got.Title)
		}
		// Fallback is per field, not per row: the empty French description
		// falls back to English even though a French row exists.
		if got.Description != "English description" {
			t.Errorf("fr description = %q, want the English fallback", got.Description)
		}
	})
}

// MetaFor is the read behind an editing form, so what it must not do is
// resolve the fallback: a form that prefilled a translation with the
// default language would write that copy back on the next save and the
// page would stop tracking the original.
func TestPageMetaForReadsRawValues(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{
			Slug:        "bilingual",
			Title:       "English Title",
			Description: "English description",
		}, defaultLocale)
		if err := s.UpdateMeta(ctx, p.ID, "fr", "Titre Français", ""); err != nil {
			t.Fatalf("UpdateMeta(fr): %v", err)
		}

		fr, err := s.MetaFor(ctx, p.ID, "fr")
		if err != nil {
			t.Fatalf("MetaFor(fr): %v", err)
		}
		if fr.Title != "Titre Français" {
			t.Errorf("fr title = %q, want the stored French title", fr.Title)
		}
		// The French description is untranslated: empty as stored, with
		// the English alongside it as what the page falls back to.
		if fr.Description != "" {
			t.Errorf("fr description = %q, want empty — nothing is stored for fr", fr.Description)
		}
		if fr.InheritedDescription != "English description" {
			t.Errorf("fr inherited description = %q, want the English one", fr.InheritedDescription)
		}
		if fr.InheritedTitle != "English Title" {
			t.Errorf("fr inherited title = %q, want the English one", fr.InheritedTitle)
		}

		// The default locale inherits from nothing, so its own values are
		// never also reported as what it falls back to.
		en, err := s.MetaFor(ctx, p.ID, defaultLocale)
		if err != nil {
			t.Fatalf("MetaFor(en): %v", err)
		}
		if en.Title != "English Title" || en.Description != "English description" {
			t.Errorf("en meta = %+v, want the stored English values", en)
		}
		if en.InheritedTitle != "" || en.InheritedDescription != "" {
			t.Errorf("en inherited = %q/%q, want both empty", en.InheritedTitle, en.InheritedDescription)
		}

		// A locale with no row at all reads as entirely untranslated
		// rather than as an error.
		de, err := s.MetaFor(ctx, p.ID, "de")
		if err != nil {
			t.Fatalf("MetaFor(de): %v", err)
		}
		if de.Title != "" || de.Description != "" {
			t.Errorf("de meta = %+v, want empty — there is no de row", de)
		}
		if de.InheritedTitle != "English Title" {
			t.Errorf("de inherited title = %q, want the English one", de.InheritedTitle)
		}

		if _, err := s.MetaFor(ctx, p.ID+1000, defaultLocale); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("MetaFor(missing page) = %v, want ErrNotFound", err)
		}
	})
}

// MetaFor reads the working copy, so an edit staged for the next Publish
// is what the form shows — not what the site is currently serving.
func TestPageMetaForReadsTheDraft(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "staged", Title: "Live Title"}, defaultLocale)
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := s.UpdateMeta(ctx, p.ID, defaultLocale, "Edited Title", "New description"); err != nil {
			t.Fatalf("UpdateMeta: %v", err)
		}

		got, err := s.MetaFor(ctx, p.ID, defaultLocale)
		if err != nil {
			t.Fatalf("MetaFor: %v", err)
		}
		if got.Title != "Edited Title" {
			t.Errorf("title = %q, want the staged edit", got.Title)
		}
		live, err := s.GetBySlug(ctx, "staged", defaultLocale, true)
		if err != nil {
			t.Fatalf("GetBySlug(published): %v", err)
		}
		if live.Title != "Live Title" {
			t.Errorf("published title = %q, want the pre-edit one", live.Title)
		}
	})
}

func TestPageUpdateStagesContentButNotSlug(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "original", Title: "Original"}, defaultLocale)
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		p.Slug = "moved"
		p.Title = "Edited"
		p.TemplateName = "other.gohtml"
		if err := s.Update(ctx, p, defaultLocale); err != nil {
			t.Fatalf("Update: %v", err)
		}

		// The slug is not staged — the move is immediate.
		live, err := s.GetBySlug(ctx, "moved", defaultLocale, true)
		if err != nil {
			t.Fatalf("GetBySlug(moved, published): %v", err)
		}
		// ...but the title is staged, so the site still shows the old one.
		if live.Title != "Original" {
			t.Errorf("published title = %q, want the pre-edit %q", live.Title, "Original")
		}
		if live.TemplateName != "page.gohtml" {
			t.Errorf("published template = %q, want the pre-edit one", live.TemplateName)
		}

		// The working copy shows the edit immediately.
		draft, err := s.GetByID(ctx, p.ID, defaultLocale)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if draft.Title != "Edited" {
			t.Errorf("draft title = %q, want %q", draft.Title, "Edited")
		}
		if draft.TemplateName != "other.gohtml" {
			t.Errorf("draft template = %q, want %q", draft.TemplateName, "other.gohtml")
		}

		// Publishing promotes the staged fields.
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		live, err = s.GetBySlug(ctx, "moved", defaultLocale, true)
		if err != nil {
			t.Fatalf("GetBySlug after publish: %v", err)
		}
		if live.Title != "Edited" {
			t.Errorf("published title after publish = %q, want %q", live.Title, "Edited")
		}
		if live.TemplateName != "other.gohtml" {
			t.Errorf("published template after publish = %q, want %q", live.TemplateName, "other.gohtml")
		}
	})
}

func TestPageUpdateDuplicateSlug(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		seedPage(t, s, content.Page{Slug: "first"}, defaultLocale)
		mover := seedPage(t, s, content.Page{Slug: "second"}, defaultLocale)

		mover.Slug = "first"
		if err := s.Update(ctx, mover, defaultLocale); !errors.Is(err, content.ErrDuplicateSlug) {
			t.Errorf("Update(duplicate slug) = %v, want ErrDuplicateSlug", err)
		}
	})
}

func TestPageUpdateMissing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		ghost := &content.Page{ID: 4242, Slug: "ghost", TemplateName: "page.gohtml"}
		if err := s.Update(ctx, ghost, defaultLocale); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("Update(missing) = %v, want ErrNotFound", err)
		}
		if err := s.Delete(ctx, 4242); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("Delete(missing) = %v, want ErrNotFound", err)
		}
		if err := s.Publish(ctx, 4242); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("Publish(missing) = %v, want ErrNotFound", err)
		}
		if err := s.Unpublish(ctx, 4242); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("Unpublish(missing) = %v, want ErrNotFound", err)
		}
		if err := s.SetVisibility(ctx, 4242, content.VisibilityPrivate); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("SetVisibility(missing) = %v, want ErrNotFound", err)
		}
	})
}

func TestPagePublishUnpublishAndVisibility(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "cycle", Title: "Cycle"}, defaultLocale)

		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		got, _ := s.GetByID(ctx, p.ID, defaultLocale)
		if got.Status != content.StatusPublished {
			t.Errorf("status after Publish = %q, want published", got.Status)
		}

		if err := s.SetVisibility(ctx, p.ID, content.VisibilityPrivate); err != nil {
			t.Fatalf("SetVisibility: %v", err)
		}
		got, _ = s.GetByID(ctx, p.ID, defaultLocale)
		if got.Visibility != content.VisibilityPrivate {
			t.Errorf("visibility = %q, want private", got.Visibility)
		}
		// Visibility is documented as independent of publication status.
		if got.Status != content.StatusPublished {
			t.Errorf("SetVisibility changed status to %q, want it untouched", got.Status)
		}

		if err := s.Unpublish(ctx, p.ID); err != nil {
			t.Fatalf("Unpublish: %v", err)
		}
		got, _ = s.GetByID(ctx, p.ID, defaultLocale)
		if got.Status != content.StatusDraft {
			t.Errorf("status after Unpublish = %q, want draft", got.Status)
		}
		if _, err := s.GetBySlug(ctx, "cycle", defaultLocale, true); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("unpublished page is still served: %v", err)
		}
	})
}

func TestPageDeleteCascades(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "doomed", Title: "Doomed"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>bye</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}

		if err := s.Delete(ctx, p.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := s.GetByID(ctx, p.ID, defaultLocale); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("GetByID after Delete = %v, want ErrNotFound", err)
		}
		// The blocks should have gone with it, via ON DELETE CASCADE.
		blocks, err := s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(blocks) != 0 {
			t.Errorf("page delete left %d orphaned blocks, want 0", len(blocks))
		}
	})
}

func TestPageAllOrderedBySlug(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		seedPage(t, s, content.Page{Slug: "gamma"}, defaultLocale)
		seedPage(t, s, content.Page{Slug: "alpha"}, defaultLocale)
		seedPage(t, s, content.Page{Slug: "beta"}, defaultLocale)

		pages, err := s.All(ctx, defaultLocale)
		if err != nil {
			t.Fatalf("All: %v", err)
		}
		var got []string
		for _, p := range pages {
			got = append(got, p.Slug)
		}
		want := []string{"alpha", "beta", "gamma"}
		if len(got) != len(want) {
			t.Fatalf("All returned %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("All order = %v, want %v", got, want)
			}
		}
	})
}

func TestPageDuplicate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		src := seedPage(t, s, content.Page{
			Slug:         "source",
			TemplateName: "custom.gohtml",
			HeadCSS:      "p{margin:0}",
			Title:        "Source Page",
			Description:  "Source description",
		}, defaultLocale)
		if err := s.UpdateMeta(ctx, src.ID, "fr", "Page Source", "Description source"); err != nil {
			t.Fatalf("UpdateMeta(fr): %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, src.ID, "body", defaultLocale, content.KindHTML, "<p>copy me</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.Publish(ctx, src.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		copyID, err := s.Duplicate(ctx, src.ID, "source-copy", "Copy Of Source", defaultLocale)
		if err != nil {
			t.Fatalf("Duplicate: %v", err)
		}

		got, err := s.GetByID(ctx, copyID, defaultLocale)
		if err != nil {
			t.Fatalf("GetByID(copy): %v", err)
		}
		if got.Slug != "source-copy" {
			t.Errorf("copy slug = %q, want %q", got.Slug, "source-copy")
		}
		if got.Title != "Copy Of Source" {
			t.Errorf("copy title = %q, want the supplied title", got.Title)
		}
		if got.TemplateName != "custom.gohtml" {
			t.Errorf("copy template = %q, want the source's", got.TemplateName)
		}
		if got.HeadCSS != "p{margin:0}" {
			t.Errorf("copy head_css = %q, want the source's", got.HeadCSS)
		}
		// Duplicate documents that a copy always starts as a draft, even
		// when the source is published.
		if got.Status != content.StatusDraft {
			t.Errorf("copy status = %q, want draft", got.Status)
		}

		// Other locales keep the source's titles.
		fr, err := s.GetByID(ctx, copyID, "fr")
		if err != nil {
			t.Fatalf("GetByID(copy, fr): %v", err)
		}
		if fr.Title != "Page Source" {
			t.Errorf("copy fr title = %q, want the source's French title", fr.Title)
		}

		// Draft blocks come across; published ones do not, since the copy
		// has never been published.
		draftBlocks, err := s.BlocksFor(ctx, copyID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor(draft): %v", err)
		}
		if len(draftBlocks) != 1 || draftBlocks[0].Content != "<p>copy me</p>" {
			t.Errorf("copy draft blocks = %+v, want the source's one block", draftBlocks)
		}
		pubBlocks, err := s.BlocksFor(ctx, copyID, defaultLocale, content.StatusPublished)
		if err != nil {
			t.Fatalf("BlocksFor(published): %v", err)
		}
		if len(pubBlocks) != 0 {
			t.Errorf("copy has %d published blocks, want 0", len(pubBlocks))
		}
	})
}

func TestPageDuplicateErrors(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		src := seedPage(t, s, content.Page{Slug: "dup-src"}, defaultLocale)
		seedPage(t, s, content.Page{Slug: "dup-taken"}, defaultLocale)

		if _, err := s.Duplicate(ctx, src.ID, "dup-taken", "x", defaultLocale); !errors.Is(err, content.ErrDuplicateSlug) {
			t.Errorf("Duplicate(taken slug) = %v, want ErrDuplicateSlug", err)
		}
		if _, err := s.Duplicate(ctx, 4242, "dup-new", "x", defaultLocale); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("Duplicate(missing source) = %v, want ErrNotFound", err)
		}
	})
}

func TestPageCounts(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		pages, posts, err := s.Counts(ctx)
		if err != nil {
			t.Fatalf("Counts(empty): %v", err)
		}
		if pages != 0 || posts != 0 {
			t.Fatalf("Counts(empty) = %d, %d, want 0, 0", pages, posts)
		}

		seedPage(t, s, content.Page{Slug: "plain-one"}, defaultLocale)
		seedPage(t, s, content.Page{Slug: "plain-two"}, defaultLocale)
		post := &content.Post{
			Page: content.Page{Slug: "blog/hello", TemplateName: "post.gohtml", Title: "Hello"},
			Feed: content.FeedBlog,
		}
		if _, err := s.InsertPost(ctx, post, defaultLocale); err != nil {
			t.Fatalf("InsertPost: %v", err)
		}

		pages, posts, err = s.Counts(ctx)
		if err != nil {
			t.Fatalf("Counts: %v", err)
		}
		// A post's backing page must not be counted as a plain page.
		if pages != 2 {
			t.Errorf("page count = %d, want 2 (posts excluded)", pages)
		}
		if posts != 1 {
			t.Errorf("post count = %d, want 1", posts)
		}
	})
}

func TestPageDiscardDraft(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{
			Slug:         "revertible",
			TemplateName: "live.gohtml",
			HeadCSS:      "live{}",
			Title:        "Live Title",
		}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>live</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		// Stage edits to every kind of staged field, then throw them away.
		p.Title = "Edited Title"
		p.TemplateName = "edited.gohtml"
		p.HeadCSS = "edited{}"
		if err := s.Update(ctx, p, defaultLocale); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>edited</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(edit): %v", err)
		}

		if err := s.DiscardDraft(ctx, p.ID); err != nil {
			t.Fatalf("DiscardDraft: %v", err)
		}

		got, err := s.GetByID(ctx, p.ID, defaultLocale)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Title != "Live Title" {
			t.Errorf("title after discard = %q, want the live %q", got.Title, "Live Title")
		}
		if got.TemplateName != "live.gohtml" {
			t.Errorf("template after discard = %q, want the live one", got.TemplateName)
		}
		if got.HeadCSS != "live{}" {
			t.Errorf("head_css after discard = %q, want the live one", got.HeadCSS)
		}
		blocks, err := s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(blocks) != 1 || blocks[0].Content != "<p>live</p>" {
			t.Errorf("draft blocks after discard = %+v, want the live content", blocks)
		}
		// DiscardDraft is documented to leave publication status alone.
		if got.Status != content.StatusPublished {
			t.Errorf("status after discard = %q, want it untouched", got.Status)
		}
	})
}
