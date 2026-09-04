// Page history against real database engines. The payload is a JSON
// document, so what these tests are really checking is that the round trip
// through it loses nothing the site needs — every locale, every region's
// order, and the settings a section carries — and that the hash comparison
// tells "republished unchanged" apart from "republished with an edit".
//
// Publishing is what writes history, so almost everything here drives the
// real path: Publish and PublishAs, not the snapshot machinery underneath.

package content_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/snippets"
)

// publishedPage seeds a page with one block and publishes it, which leaves
// it live with exactly one edition in its history.
func publishedPage(t *testing.T, s *content.Store, slug string) *content.Page {
	t.Helper()
	ctx := context.Background()
	p := seedPage(t, s, content.Page{Slug: slug, Title: "First title"}, defaultLocale)
	if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>first</p>"); err != nil {
		t.Fatalf("UpsertDraftBlock: %v", err)
	}
	if err := s.Publish(ctx, p.ID); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return p
}

// republish edits a page's body and publishes it, adding one edition.
func republish(t *testing.T, s *content.Store, pageID int64, body string) {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertDraftBlock(ctx, pageID, "body", defaultLocale, content.KindHTML, body); err != nil {
		t.Fatalf("UpsertDraftBlock(%q): %v", body, err)
	}
	if err := s.Publish(ctx, pageID); err != nil {
		t.Fatalf("Publish(%q): %v", body, err)
	}
}

func TestVersionSnapshotCapturesPublishedContent(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		p := seedPage(t, s, content.Page{
			Slug:         "history",
			TemplateName: "about.gohtml",
			HeadCSS:      "h1{color:red}",
			BodyJS:       "console.log(1)",
			Title:        "English title",
			Description:  "English summary",
		}, defaultLocale)

		// Two locales and two regions, one of them a sections stack, so the
		// snapshot has ordering and settings to get wrong.
		if err := s.UpsertDraftBlock(ctx, p.ID, "intro", defaultLocale, content.KindText, "hello"); err != nil {
			t.Fatalf("UpsertDraftBlock(intro): %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, p.ID, "intro", "fr", content.KindText, "bonjour"); err != nil {
			t.Fatalf("UpsertDraftBlock(intro, fr): %v", err)
		}
		if err := s.ReplaceDraftSections(ctx, p.ID, "main", defaultLocale, []content.SectionInput{
			{Content: "<p>one</p>", Settings: map[string]string{"bg": "paper", "width": "wide"}},
			{Content: "<p>two</p>", Settings: map[string]string{"bg": "spruce"}},
		}); err != nil {
			t.Fatalf("ReplaceDraftSections: %v", err)
		}
		if err := s.UpdateMeta(ctx, p.ID, "fr", content.PageMeta{
			Title:           "Titre français",
			Description:     "Résumé",
			MetaDescription: "Pour les moteurs",
		}); err != nil {
			t.Fatalf("UpdateMeta(fr): %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		// Read it back out of the stored edition, not from the live tables:
		// what matters is that the JSON round trip kept everything.
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("got %d editions after one publish, want 1", len(list))
		}
		_, snap, err := s.VersionSnapshot(ctx, p.ID, list[0].ID)
		if err != nil {
			t.Fatalf("VersionSnapshot: %v", err)
		}

		if snap.Page.TemplateName != "about.gohtml" {
			t.Errorf("template_name = %q, want %q", snap.Page.TemplateName, "about.gohtml")
		}
		if snap.Page.HeadCSS != "h1{color:red}" || snap.Page.BodyJS != "console.log(1)" {
			t.Errorf("page-level code = %+v, want the published CSS and JS", snap.Page)
		}

		// Both locales' metadata.
		if len(snap.Meta) != 2 {
			t.Fatalf("got %d metadata rows, want 2 (en and fr)", len(snap.Meta))
		}
		byLocale := map[string]content.SnapshotMeta{}
		for _, m := range snap.Meta {
			byLocale[m.Locale] = m
		}
		if got := byLocale["en"].Title; got != "English title" {
			t.Errorf("en title = %q, want %q", got, "English title")
		}
		if got := byLocale["fr"].Title; got != "Titre français" {
			t.Errorf("fr title = %q, want %q", got, "Titre français")
		}
		if got := byLocale["fr"].MetaDescription; got != "Pour les moteurs" {
			t.Errorf("fr meta_description = %q, want %q", got, "Pour les moteurs")
		}

		// Four blocks: the intro in two locales, plus the two sections.
		if len(snap.Blocks) != 4 {
			t.Fatalf("got %d blocks, want 4: %+v", len(snap.Blocks), snap.Blocks)
		}
		var sections []content.SnapshotBlock
		for _, b := range snap.Blocks {
			if b.Region == "main" {
				sections = append(sections, b)
			}
		}
		if len(sections) != 2 {
			t.Fatalf("got %d section blocks, want 2", len(sections))
		}
		// Section order is the content, not an incidental detail: an edition
		// that reorders them restores a different page.
		if sections[0].Content != "<p>one</p>" || sections[1].Content != "<p>two</p>" {
			t.Errorf("sections came back in the order %q, %q", sections[0].Content, sections[1].Content)
		}
		if sections[0].Sort != 0 || sections[1].Sort != 1 {
			t.Errorf("section sorts = %d, %d, want 0, 1", sections[0].Sort, sections[1].Sort)
		}
		if got := sections[0].Settings["bg"]; got != "paper" {
			t.Errorf("section 0 settings[bg] = %q, want %q", got, "paper")
		}
		if got := sections[0].Settings["width"]; got != "wide" {
			t.Errorf("section 0 settings[width] = %q, want %q", got, "wide")
		}
	})
}

// PublishedSnapshot reads what is *live*, not the working copy: an
// unpublished edit must not appear in it, or history would record editions
// the site never served.
func TestVersionSnapshotIgnoresTheDraft(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := publishedPage(t, s, "live-only")

		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>unpublished</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		snap, err := s.PublishedSnapshot(ctx, p.ID)
		if err != nil {
			t.Fatalf("PublishedSnapshot: %v", err)
		}
		if len(snap.Blocks) != 1 {
			t.Fatalf("got %d blocks, want 1", len(snap.Blocks))
		}
		if snap.Blocks[0].Content != "<p>first</p>" {
			t.Errorf("snapshot block = %q, want the published content", snap.Blocks[0].Content)
		}
	})
}

// Publishing is what creates history, so a page that has gone live twice
// has two editions and the newest is what the site is serving.
func TestPublishRecordsAnEdition(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := publishedPage(t, s, "recorded")

		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("got %d editions after one publish, want 1", len(list))
		}
		first := list[0]
		if first.PageID != p.ID {
			t.Errorf("page_id = %d, want %d", first.PageID, p.ID)
		}
		if first.Kind != content.VersionPublish {
			t.Errorf("kind = %q, want %q", first.Kind, content.VersionPublish)
		}
		if first.SavedBy != nil {
			t.Errorf("saved_by = %v, want nil for an unattributed Publish", first.SavedBy)
		}
		if first.SavedAt.IsZero() {
			t.Error("saved_at was not set")
		}

		republish(t, s, p.ID, "<p>second</p>")
		list, err = s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("got %d editions after two publishes, want 2", len(list))
		}
		// Newest first, and the newest holds what was just published.
		if list[1].ID != first.ID {
			t.Errorf("editions listed as %d, %d, want the older one (%d) last",
				list[0].ID, list[1].ID, first.ID)
		}
		_, snap, err := s.VersionSnapshot(ctx, p.ID, list[0].ID)
		if err != nil {
			t.Fatalf("VersionSnapshot: %v", err)
		}
		if len(snap.Blocks) != 1 || snap.Blocks[0].Content != "<p>second</p>" {
			t.Errorf("newest edition = %+v, want the content just published", snap.Blocks)
		}
		// And the older one still holds what it held.
		_, older, err := s.VersionSnapshot(ctx, p.ID, first.ID)
		if err != nil {
			t.Fatalf("VersionSnapshot(older): %v", err)
		}
		if len(older.Blocks) != 1 || older.Blocks[0].Content != "<p>first</p>" {
			t.Errorf("older edition = %+v, want the content it went live with", older.Blocks)
		}
	})
}

// PublishAs records who did it; Publish is the same thing with nobody to
// name.
func TestPublishAsAttributesTheEdition(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		users := auth.NewStore(db)

		u := &auth.User{Email: "pub@example.com", Name: "Pat Publisher",
			PasswordHash: "x", Role: auth.RoleEditor, Active: true}
		if _, err := users.Insert(ctx, u); err != nil {
			t.Fatalf("seeding user: %v", err)
		}

		p := seedPage(t, s, content.Page{Slug: "attributed", Title: "Attributed"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>x</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.PublishAs(ctx, p.ID, &u.ID); err != nil {
			t.Fatalf("PublishAs: %v", err)
		}

		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("got %d editions, want 1", len(list))
		}
		if list[0].SavedBy == nil || *list[0].SavedBy != u.ID {
			t.Errorf("saved_by = %v, want %d", list[0].SavedBy, u.ID)
		}
		if list[0].SavedByName != "Pat Publisher" {
			t.Errorf("saved_by name = %q, want %q", list[0].SavedByName, "Pat Publisher")
		}
	})
}

// History outlives the account that made it: deleting the user must leave
// the edition listed, just without a name.
func TestVersionSurvivesItsAuthor(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		users := auth.NewStore(db)

		u := &auth.User{Email: "gone@example.com", Name: "Departed",
			PasswordHash: "x", Role: auth.RoleEditor, Active: true}
		if _, err := users.Insert(ctx, u); err != nil {
			t.Fatalf("seeding user: %v", err)
		}
		p := seedPage(t, s, content.Page{Slug: "orphaned", Title: "Orphaned"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>x</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.PublishAs(ctx, p.ID, &u.ID); err != nil {
			t.Fatalf("PublishAs: %v", err)
		}
		if err := users.Delete(ctx, u.ID); err != nil {
			t.Fatalf("deleting user: %v", err)
		}

		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("got %d editions after deleting the author, want 1", len(list))
		}
		if list[0].SavedBy != nil {
			t.Errorf("saved_by = %v, want nil once the account is gone", list[0].SavedBy)
		}
		if list[0].SavedByName != "" {
			t.Errorf("saved_by name = %q, want empty", list[0].SavedByName)
		}
		// The content is still readable — that is the whole point.
		if _, snap, err := s.VersionSnapshot(ctx, p.ID, list[0].ID); err != nil {
			t.Errorf("VersionSnapshot after the author was deleted: %v", err)
		} else if len(snap.Blocks) != 1 {
			t.Errorf("edition holds %d blocks, want 1", len(snap.Blocks))
		}
	})
}

// The dedupe seen from the publish path: republishing an untouched page —
// which happens on every publish for shared content — must not stack up
// identical editions.
func TestRepublishingUnchangedAddsNoEdition(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := publishedPage(t, s, "steady")

		for range 3 {
			if err := s.Publish(ctx, p.ID); err != nil {
				t.Fatalf("Publish: %v", err)
			}
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("got %d editions after republishing unchanged content 3 more times, want 1", len(list))
		}
	})
}

// The site page carries shared content, and PublishShared runs on every
// publish anywhere — so its history has to record footer edits and nothing
// else.
func TestPublishSharedRecordsOnlyRealEdits(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		siteID, err := s.SitePageID(ctx)
		if err != nil {
			t.Fatalf("SitePageID: %v", err)
		}
		if err := s.UpsertSharedBlock(ctx, "footer", defaultLocale, content.KindHTML, "<p>&copy; Acme</p>"); err != nil {
			t.Fatalf("UpsertSharedBlock: %v", err)
		}
		if err := s.PublishShared(ctx); err != nil {
			t.Fatalf("PublishShared: %v", err)
		}
		// Four more page publishes, each carrying shared content along.
		for range 4 {
			if err := s.PublishShared(ctx); err != nil {
				t.Fatalf("PublishShared: %v", err)
			}
		}
		list, err := s.Versions(ctx, siteID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("the site page has %d editions after one footer edit and five publishes, want 1", len(list))
		}

		// An actual footer edit is an edition of its own.
		if err := s.UpsertSharedBlock(ctx, "footer", defaultLocale, content.KindHTML, "<p>&copy; Acme Ltd</p>"); err != nil {
			t.Fatalf("UpsertSharedBlock: %v", err)
		}
		if err := s.PublishShared(ctx); err != nil {
			t.Fatalf("PublishShared: %v", err)
		}
		list, err = s.Versions(ctx, siteID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 2 {
			t.Errorf("the site page has %d editions after a footer edit, want 2", len(list))
		}
	})
}

// A version id is only readable through the page that owns it, so a route
// scoped to one page cannot reach another page's history.
func TestVersionSnapshotIsScopedToItsPage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		mine := publishedPage(t, s, "mine")
		theirs := publishedPage(t, s, "theirs")

		list, err := s.Versions(ctx, mine.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("got %d editions, want 1", len(list))
		}
		if _, _, err := s.VersionSnapshot(ctx, theirs.ID, list[0].ID); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("VersionSnapshot across pages = %v, want ErrNotFound", err)
		}
	})
}

// Publishing prunes as it goes, so history stays bounded without anything
// having to sweep it.
func TestPublishPrunesOldEditions(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		s.SetVersionsKept(3)
		p := seedPage(t, s, content.Page{Slug: "bounded", Title: "Bounded"}, defaultLocale)

		for _, body := range []string{"a", "b", "c", "d", "e", "f"} {
			republish(t, s, p.ID, body)
		}

		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 3 {
			t.Fatalf("got %d editions with a limit of 3, want 3", len(list))
		}
		// The three kept are the newest three, in order.
		for i, want := range []string{"f", "e", "d"} {
			_, snap, err := s.VersionSnapshot(ctx, p.ID, list[i].ID)
			if err != nil {
				t.Fatalf("VersionSnapshot(%d): %v", i, err)
			}
			if len(snap.Blocks) != 1 || snap.Blocks[0].Content != want {
				t.Errorf("edition %d holds %+v, want the block %q", i, snap.Blocks, want)
			}
		}
	})
}

// PruneVersions on its own, for a host trimming history outside a publish.
func TestVersionPruneKeepsTheNewest(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "pruned", Title: "Pruned"}, defaultLocale)
		for _, body := range []string{"a", "b", "c", "d", "e"} {
			republish(t, s, p.ID, body)
		}
		newest, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(newest) != 5 {
			t.Fatalf("got %d editions, want 5", len(newest))
		}

		// Fewer editions than the limit: nothing is dropped.
		if err := s.PruneVersions(ctx, p.ID, 10); err != nil {
			t.Fatalf("PruneVersions(10): %v", err)
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 5 {
			t.Fatalf("got %d editions after pruning to 10, want 5", len(list))
		}

		if err := s.PruneVersions(ctx, p.ID, 2); err != nil {
			t.Fatalf("PruneVersions(2): %v", err)
		}
		list, err = s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("got %d editions after pruning to 2, want 2", len(list))
		}
		if list[0].ID != newest[0].ID || list[1].ID != newest[1].ID {
			t.Errorf("kept editions %d, %d, want the newest two %d, %d",
				list[0].ID, list[1].ID, newest[0].ID, newest[1].ID)
		}
	})
}

// Pruning one page's history must not touch another's.
func TestVersionPruneIsPerPage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		pruned := seedPage(t, s, content.Page{Slug: "prune-me", Title: "Prune me"}, defaultLocale)
		kept := seedPage(t, s, content.Page{Slug: "leave-me", Title: "Leave me"}, defaultLocale)

		for _, p := range []*content.Page{pruned, kept} {
			for _, body := range []string{"a", "b", "c"} {
				republish(t, s, p.ID, body)
			}
		}
		if err := s.PruneVersions(ctx, pruned.ID, 1); err != nil {
			t.Fatalf("PruneVersions: %v", err)
		}

		list, err := s.Versions(ctx, kept.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 3 {
			t.Errorf("the other page has %d editions after pruning, want 3", len(list))
		}
	})
}

// SetVersionsKept takes zero as "the default" rather than "keep none", so a
// host that leaves Config.PageVersionsKept unset keeps a history.
func TestVersionsKeptDefaultsOnZero(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		s.SetVersionsKept(0)
		p := publishedPage(t, s, "defaulted")

		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 1 {
			t.Errorf("got %d editions with the limit left unset, want 1 kept", len(list))
		}
	})
}

// A failed publish must leave no edition: the snapshot rides in the same
// transaction as the writes it describes.
func TestFailedPublishRecordsNothing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		if err := s.Publish(ctx, 987654); !errors.Is(err, content.ErrNotFound) {
			t.Fatalf("Publish of a missing page = %v, want ErrNotFound", err)
		}
		list, err := s.Versions(ctx, 987654)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("got %d editions for a page that never published, want none", len(list))
		}
	})
}

// Deleting a page takes its history with it — the editions are that page's
// content, and nothing else can reach them once it is gone.
func TestVersionsGoWithTheirPage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := publishedPage(t, s, "doomed")

		if err := s.Delete(ctx, p.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("got %d editions after deleting the page, want none", len(list))
		}
	})
}

// A page that was never published has nothing live to snapshot, and a page
// that does not exist is not found.
func TestVersionSnapshotOfMissingPage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		if _, err := s.PublishedSnapshot(ctx, 987654); !errors.Is(err, content.ErrNotFound) {
			t.Errorf("PublishedSnapshot of a missing page = %v, want ErrNotFound", err)
		}

		p := seedPage(t, s, content.Page{Slug: "never-live"}, defaultLocale)
		snap, err := s.PublishedSnapshot(ctx, p.ID)
		if err != nil {
			t.Fatalf("PublishedSnapshot of an unpublished page: %v", err)
		}
		if len(snap.Blocks) != 0 || len(snap.Meta) != 0 {
			t.Errorf("unpublished page snapshot = %+v, want no published content", snap)
		}
	})
}

// Restoring writes the working copy and leaves the site alone — the whole
// contract of the feature. The draft comes back as the old edition, the
// published rows and the page's status do not move.
func TestRestoreVersionWritesTheDraftOnly(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := publishedPage(t, s, "restorable")
		republish(t, s, p.ID, "<p>second</p>")

		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("got %d editions, want 2", len(list))
		}
		oldest := list[1]

		if _, err := s.RestoreVersion(ctx, p.ID, oldest.ID); err != nil {
			t.Fatalf("RestoreVersion: %v", err)
		}

		draft, err := s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor(draft): %v", err)
		}
		if len(draft) != 1 || draft[0].Content != "<p>first</p>" {
			t.Errorf("draft after restore = %+v, want the restored content", draft)
		}
		// The site is still serving what it was serving.
		live, err := s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusPublished)
		if err != nil {
			t.Fatalf("BlocksFor(published): %v", err)
		}
		if len(live) != 1 || live[0].Content != "<p>second</p>" {
			t.Errorf("published blocks after restore = %+v, want them untouched", live)
		}
		page, err := s.GetByID(ctx, p.ID, defaultLocale)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if page.Status != content.StatusPublished {
			t.Errorf("status after restore = %q, want it unchanged", page.Status)
		}
		// And the restore itself is an unpublished change, so the editor's
		// chip and the discard button both light up.
		changed, err := s.HasUnpublishedChanges(ctx, p.ID)
		if err != nil {
			t.Fatalf("HasUnpublishedChanges: %v", err)
		}
		if !changed {
			t.Error("a restored draft does not read as an unpublished change")
		}

		// Restoring is not itself an edition; publishing it is.
		list, err = s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("got %d editions after a restore, want the same 2", len(list))
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		list, err = s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 3 {
			t.Errorf("got %d editions after publishing the restore, want 3", len(list))
		}
	})
}

// Everything an edition holds has to come back, not just the blocks:
// metadata in every locale, the section order and settings, and the
// page-level template and code.
func TestRestoreVersionBringsBackEverything(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)

		p := seedPage(t, s, content.Page{
			Slug: "full", TemplateName: "about.gohtml",
			HeadCSS: "h1{color:red}", BodyJS: "console.log(1)",
			Title: "Original", Description: "Original summary",
		}, defaultLocale)
		if err := s.ReplaceDraftSections(ctx, p.ID, "main", defaultLocale, []content.SectionInput{
			{Content: "<p>one</p>", Settings: map[string]string{"bg": "paper"}},
			{Content: "<p>two</p>", Settings: map[string]string{"bg": "spruce"}},
		}); err != nil {
			t.Fatalf("ReplaceDraftSections: %v", err)
		}
		if err := s.UpdateMeta(ctx, p.ID, "fr", content.PageMeta{Title: "Originale"}); err != nil {
			t.Fatalf("UpdateMeta(fr): %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		original := list[0]

		// Change everything, and publish so the edition above is genuinely
		// in the past.
		changed := *p
		changed.TemplateName = "page.gohtml"
		changed.HeadCSS, changed.BodyJS = "", ""
		changed.Title, changed.Description = "Rewritten", "New summary"
		if err := s.Update(ctx, &changed, defaultLocale); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if err := s.ReplaceDraftSections(ctx, p.ID, "main", defaultLocale, []content.SectionInput{
			{Content: "<p>only</p>"},
		}); err != nil {
			t.Fatalf("ReplaceDraftSections(second): %v", err)
		}
		if err := s.UpdateMeta(ctx, p.ID, "fr", content.PageMeta{Title: "Réécrite"}); err != nil {
			t.Fatalf("UpdateMeta(fr, second): %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish(second): %v", err)
		}

		if _, err := s.RestoreVersion(ctx, p.ID, original.ID); err != nil {
			t.Fatalf("RestoreVersion: %v", err)
		}

		// Page-level fields and default-locale metadata read from the
		// working copy, which is what the admin form shows.
		got, err := s.GetByID(ctx, p.ID, defaultLocale)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.TemplateName != "about.gohtml" {
			t.Errorf("template = %q, want %q", got.TemplateName, "about.gohtml")
		}
		if got.HeadCSS != "h1{color:red}" || got.BodyJS != "console.log(1)" {
			t.Errorf("page code = %q / %q, want the restored CSS and JS", got.HeadCSS, got.BodyJS)
		}
		if got.Title != "Original" || got.Description != "Original summary" {
			t.Errorf("metadata = %q / %q, want the restored words", got.Title, got.Description)
		}
		// Slug is not part of an edition, so it stays where it is.
		if got.Slug != "full" {
			t.Errorf("slug = %q, want it untouched by a restore", got.Slug)
		}

		fr, err := s.MetaFor(ctx, p.ID, "fr")
		if err != nil {
			t.Fatalf("MetaFor(fr): %v", err)
		}
		if fr.Title != "Originale" {
			t.Errorf("fr title = %q, want %q", fr.Title, "Originale")
		}

		blocks, err := s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(blocks) != 2 {
			t.Fatalf("got %d blocks after restore, want the 2 sections", len(blocks))
		}
		if blocks[0].Content != "<p>one</p>" || blocks[1].Content != "<p>two</p>" {
			t.Errorf("sections = %q, %q, want them back in order",
				blocks[0].Content, blocks[1].Content)
		}
		if blocks[0].Sort != 0 || blocks[1].Sort != 1 {
			t.Errorf("section sorts = %d, %d, want 0, 1", blocks[0].Sort, blocks[1].Sort)
		}
		if got := blocks[0].Settings["bg"]; got != "paper" {
			t.Errorf("section 0 settings[bg] = %q, want %q", got, "paper")
		}
	})
}

// Restoring replaces the draft rather than merging into it: content added
// since the edition has to go, or a restore would leave a page that never
// existed.
func TestRestoreVersionReplacesTheDraft(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := publishedPage(t, s, "replaced")
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		edition := list[0]

		// A region the edition knows nothing about, saved but not published.
		if err := s.UpsertDraftBlock(ctx, p.ID, "aside", defaultLocale, content.KindHTML, "<p>new</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if _, err := s.RestoreVersion(ctx, p.ID, edition.ID); err != nil {
			t.Fatalf("RestoreVersion: %v", err)
		}
		blocks, err := s.BlocksFor(ctx, p.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(blocks) != 1 || blocks[0].Region != "body" {
			t.Errorf("draft after restore = %+v, want only the edition's own blocks", blocks)
		}
	})
}

// A version id from another page must not restore through this one.
func TestRestoreVersionIsScopedToItsPage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		mine := publishedPage(t, s, "keep-mine")
		theirs := publishedPage(t, s, "keep-theirs")
		list, err := s.Versions(ctx, mine.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}

		if _, err := s.RestoreVersion(ctx, theirs.ID, list[0].ID); !errors.Is(err, content.ErrNotFound) {
			t.Fatalf("RestoreVersion across pages = %v, want ErrNotFound", err)
		}
		// And nothing was written on the way to refusing.
		blocks, err := s.BlocksFor(ctx, theirs.ID, defaultLocale, content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(blocks) != 1 || blocks[0].Content != "<p>first</p>" {
			t.Errorf("the other page's draft = %+v, want it untouched", blocks)
		}
	})
}

// BlocksFor is how an edition is previewed, so it has to apply the same
// region-level locale fallback the live read does.
func TestSnapshotBlocksForFallsBackByRegion(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{Slug: "translated", Title: "Translated"}, defaultLocale)

		// "intro" is translated; "body" is not.
		if err := s.UpsertDraftBlock(ctx, p.ID, "intro", defaultLocale, content.KindText, "hello"); err != nil {
			t.Fatalf("UpsertDraftBlock(intro, en): %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, p.ID, "intro", "fr", content.KindText, "bonjour"); err != nil {
			t.Fatalf("UpsertDraftBlock(intro, fr): %v", err)
		}
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML, "<p>english body</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(body, en): %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		_, snap, err := s.VersionSnapshot(ctx, p.ID, list[0].ID)
		if err != nil {
			t.Fatalf("VersionSnapshot: %v", err)
		}

		// The French read gets the French intro and the English body, and
		// never both introductions.
		fr := snap.BlocksFor(p.ID, "fr", defaultLocale)
		byRegion := map[string]content.Block{}
		for _, b := range fr {
			if prev, dup := byRegion[b.Region]; dup {
				t.Fatalf("region %q came back twice: %q and %q", b.Region, prev.Content, b.Content)
			}
			byRegion[b.Region] = b
			if b.PageID != p.ID {
				t.Errorf("block page id = %d, want %d", b.PageID, p.ID)
			}
		}
		if got := byRegion["intro"].Content; got != "bonjour" {
			t.Errorf("fr intro = %q, want the translation", got)
		}
		if got := byRegion["body"].Content; got != "<p>english body</p>" {
			t.Errorf("fr body = %q, want the default locale's, by fallback", got)
		}

		// The default-locale read is only ever its own rows.
		en := snap.BlocksFor(p.ID, defaultLocale, defaultLocale)
		if len(en) != 2 {
			t.Fatalf("en read got %d blocks, want 2", len(en))
		}
		for _, b := range en {
			if b.Locale != defaultLocale {
				t.Errorf("en read included a %q block", b.Locale)
			}
		}
	})
}

// MetaFor falls back field by field, so a translation that only ever had a
// title still previews with the default locale's description.
func TestSnapshotMetaForFallsBackPerField(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		p := seedPage(t, s, content.Page{
			Slug: "partly", Title: "English title", Description: "English summary",
		}, defaultLocale)
		if err := s.UpdateMeta(ctx, p.ID, "fr", content.PageMeta{Title: "Titre"}); err != nil {
			t.Fatalf("UpdateMeta(fr): %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		_, snap, err := s.VersionSnapshot(ctx, p.ID, list[0].ID)
		if err != nil {
			t.Fatalf("VersionSnapshot: %v", err)
		}

		fr := snap.MetaFor("fr", defaultLocale)
		if fr.Title != "Titre" {
			t.Errorf("fr title = %q, want the translation", fr.Title)
		}
		if fr.Description != "English summary" {
			t.Errorf("fr description = %q, want the default locale's, by fallback", fr.Description)
		}
		if fr.Locale != "fr" {
			t.Errorf("locale = %q, want fr", fr.Locale)
		}

		// A locale with no rows at all reads as the default's throughout.
		de := snap.MetaFor("de", defaultLocale)
		if de.Title != "English title" || de.Description != "English summary" {
			t.Errorf("untranslated locale = %+v, want the default locale's words", de)
		}
	})
}

// codeBlock returns the placeholder a page stores for a custom-code
// block: an inert div naming a library key.
func codeBlock(key string) string {
	return `<div class="cms-snippet cms-code" data-cms-code="` + key + `"></div>`
}

// An edition freezes the custom-code blocks its markup names, so the
// widgets a page showed are recoverable along with the page.
func TestVersionFreezesReferencedCode(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		code := snippets.NewCodeStore(db)

		for _, c := range []snippets.CodeSnippet{
			{Key: "signup", Name: "Signup form", HTML: "<form>v1</form>"},
			{Key: "chart", Name: "Sales chart", HTML: "<canvas>v1</canvas>"},
			{Key: "unused", Name: "Unused", HTML: "<p>nobody</p>"},
		} {
			if _, err := code.Insert(ctx, &c); err != nil {
				t.Fatalf("seeding %q: %v", c.Key, err)
			}
		}

		p := seedPage(t, s, content.Page{Slug: "widgets", Title: "Widgets"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML,
			"<p>before</p>"+codeBlock("signup")+"<p>and</p>"+codeBlock("chart")); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		// A key the library does not hold: the page renders it as nothing,
		// and there is no body to freeze.
		if err := s.UpsertDraftBlock(ctx, p.ID, "aside", defaultLocale, content.KindHTML,
			codeBlock("ghost")); err != nil {
			t.Fatalf("UpsertDraftBlock(aside): %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		_, snap, err := s.VersionSnapshot(ctx, p.ID, list[0].ID)
		if err != nil {
			t.Fatalf("VersionSnapshot: %v", err)
		}
		if len(snap.Code) != 2 {
			t.Fatalf("froze %d code blocks, want the 2 the page names and holds: %+v", len(snap.Code), snap.Code)
		}
		// Ordered by key, so the payload's bytes are stable.
		if snap.Code[0].Key != "chart" || snap.Code[1].Key != "signup" {
			t.Errorf("frozen keys = %q, %q, want them ordered by key", snap.Code[0].Key, snap.Code[1].Key)
		}
		if snap.Code[1].HTML != "<form>v1</form>" || snap.Code[1].Name != "Signup form" {
			t.Errorf("frozen signup = %+v, want its body and name", snap.Code[1])
		}
	})
}

// The point of freezing: a code block deleted from the library comes back
// with the page that used it.
func TestRestoreVersionPutsBackDeletedCode(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		code := snippets.NewCodeStore(db)

		seeded := snippets.CodeSnippet{Key: "signup", Name: "Signup form", HTML: "<form>v1</form>"}
		if _, err := code.Insert(ctx, &seeded); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		p := seedPage(t, s, content.Page{Slug: "with-widget", Title: "With widget"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML,
			codeBlock("signup")); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		edition := list[0]

		// The block is deleted from the library and the page moves on.
		if err := code.Delete(ctx, "signup"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		republish(t, s, p.ID, "<p>no widget any more</p>")

		res, err := s.RestoreVersion(ctx, p.ID, edition.ID)
		if err != nil {
			t.Fatalf("RestoreVersion: %v", err)
		}
		if len(res.CodeRecreated) != 1 || res.CodeRecreated[0] != "signup" {
			t.Errorf("CodeRecreated = %v, want [signup]", res.CodeRecreated)
		}
		if len(res.CodeChanged) != 0 {
			t.Errorf("CodeChanged = %v, want none", res.CodeChanged)
		}
		back, err := code.ByKey(ctx, "signup")
		if err != nil {
			t.Fatalf("ByKey after restore: %v", err)
		}
		if back.HTML != "<form>v1</form>" || back.Name != "Signup form" {
			t.Errorf("recreated block = %+v, want the body and name it had", back)
		}
	})
}

// The library is shared, so restoring one page must not rewrite a block
// other pages are showing. A changed block is reported, not overwritten.
func TestRestoreVersionLeavesChangedCodeAlone(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		code := snippets.NewCodeStore(db)

		seeded := snippets.CodeSnippet{Key: "signup", Name: "Signup form", HTML: "<form>v1</form>"}
		if _, err := code.Insert(ctx, &seeded); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		p := seedPage(t, s, content.Page{Slug: "shared-widget", Title: "Shared widget"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML,
			codeBlock("signup")); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		edition := list[0]

		// Somebody rewrites the block — for every page that shows it.
		seeded.HTML = "<form>v2</form>"
		if err := code.Update(ctx, &seeded); err != nil {
			t.Fatalf("Update: %v", err)
		}
		republish(t, s, p.ID, "<p>moved on</p>")

		res, err := s.RestoreVersion(ctx, p.ID, edition.ID)
		if err != nil {
			t.Fatalf("RestoreVersion: %v", err)
		}
		if len(res.CodeChanged) != 1 || res.CodeChanged[0] != "signup" {
			t.Errorf("CodeChanged = %v, want [signup]", res.CodeChanged)
		}
		if len(res.CodeRecreated) != 0 {
			t.Errorf("CodeRecreated = %v, want none", res.CodeRecreated)
		}
		live, err := code.ByKey(ctx, "signup")
		if err != nil {
			t.Fatalf("ByKey: %v", err)
		}
		if live.HTML != "<form>v2</form>" {
			t.Errorf("library block = %q, want a restore to have left it alone", live.HTML)
		}
	})
}

// The ordinary case reports nothing: the blocks are all still exactly as
// the edition knew them.
func TestRestoreVersionSaysNothingAboutUnchangedCode(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		code := snippets.NewCodeStore(db)

		seeded := snippets.CodeSnippet{Key: "signup", Name: "Signup form", HTML: "<form>v1</form>"}
		if _, err := code.Insert(ctx, &seeded); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		p := seedPage(t, s, content.Page{Slug: "steady-widget", Title: "Steady"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML,
			codeBlock("signup")+"<p>one</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		republish(t, s, p.ID, codeBlock("signup")+"<p>two</p>")

		res, err := s.RestoreVersion(ctx, p.ID, list[0].ID)
		if err != nil {
			t.Fatalf("RestoreVersion: %v", err)
		}
		if len(res.CodeRecreated) != 0 || len(res.CodeChanged) != 0 {
			t.Errorf("result = %+v, want nothing to report", res)
		}
	})
}

// Rewriting a code block changes what a page publishes, so the next
// publish of that page is a new edition even though the page itself was
// not touched.
func TestCodeChangeMakesTheNextPublishAnEdition(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := content.NewStore(db, defaultLocale)
		code := snippets.NewCodeStore(db)

		seeded := snippets.CodeSnippet{Key: "signup", Name: "Signup form", HTML: "<form>v1</form>"}
		if _, err := code.Insert(ctx, &seeded); err != nil {
			t.Fatalf("seeding: %v", err)
		}
		p := seedPage(t, s, content.Page{Slug: "code-moves", Title: "Code moves"}, defaultLocale)
		if err := s.UpsertDraftBlock(ctx, p.ID, "body", defaultLocale, content.KindHTML,
			codeBlock("signup")); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		// Republishing an untouched page is still a no-op.
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish(again): %v", err)
		}
		list, err := s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("got %d editions, want 1", len(list))
		}

		seeded.HTML = "<form>v2</form>"
		if err := code.Update(ctx, &seeded); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if err := s.Publish(ctx, p.ID); err != nil {
			t.Fatalf("Publish(after code change): %v", err)
		}
		list, err = s.Versions(ctx, p.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("got %d editions after the block changed, want 2", len(list))
		}
		_, snap, err := s.VersionSnapshot(ctx, p.ID, list[0].ID)
		if err != nil {
			t.Fatalf("VersionSnapshot: %v", err)
		}
		if len(snap.Code) != 1 || snap.Code[0].HTML != "<form>v2</form>" {
			t.Errorf("newest edition froze %+v, want the block's new body", snap.Code)
		}
	})
}
