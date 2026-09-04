package admin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/alexedwards/scs/v2"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/render"
)

// The search index through the endpoints an editor actually uses, rather
// than through the store: save is what the in-place editor POSTs on every
// edit, and publishWithShared is what its Publish button runs. Between the
// two, what a search finds must be what the site is serving — the words
// that are live, never the ones sitting in a draft.

var indexFS = fstest.MapFS{
	"base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><body>{{block "content" .}}{{end}}` +
			`<footer>{{cmsShared "footer"}}</footer></body></html>{{end}}`)},
	"page.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}<main>{{cmsRegion "main"}}</main>{{end}}`)},
}

func indexServer(t *testing.T, db *sqldb.DB) *server {
	t.Helper()
	r, err := render.New(indexFS, []string{"base.gohtml"},
		[]render.PageTemplate{{File: "page.gohtml", Label: "Page"}}, nil)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	return &server{deps: Deps{
		Sessions:      scs.New(),
		Users:         auth.NewStore(db),
		Content:       content.NewStore(db, "en"),
		Renderer:      r,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		DefaultLocale: "en",
		Locales:       []string{"en"},
	}}
}

// saveRegion posts one region's new content the way the editor's save
// does, and fails on anything but a clean 200.
func saveRegion(t *testing.T, s *server, pageID int64, region, html string) {
	t.Helper()
	body := `{"locale":"en","regions":{"` + region + `":` + jsonString(html) + `}}`
	rec := httptest.NewRecorder()
	s.apiSaveRegions(rec, apiRequest(t, s, http.MethodPost, "", body, pageID))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST regions status %d, want 200: %s", rec.Code, rec.Body)
	}
}

// jsonString quotes a small HTML fragment for the request bodies above.
func jsonString(s string) string {
	out := []byte{'"'}
	for i := range len(s) {
		switch c := s[i]; c {
		case '"', '\\':
			out = append(out, '\\', c)
		default:
			out = append(out, c)
		}
	}
	return string(append(out, '"'))
}

// findable reports whether a search for q returns the page.
func findable(t *testing.T, s *server, q string, pageID int64) bool {
	t.Helper()
	results, err := s.deps.Content.Search(context.Background(),
		content.ParseSearchQuery(q), "en", 0, 0)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	for _, r := range results {
		if r.PageID == pageID {
			return true
		}
	}
	return false
}

// The everyday cycle: an editor changes the words on a live page, saves,
// and publishes. Until they publish, the site is still serving the old
// words and a search has to keep finding those; after, the reverse.
func TestEditingAPageKeepsTheSearchIndexInStep(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := indexServer(t, db)

		page := &content.Page{Slug: "hours", TemplateName: "page.gohtml", Title: "Opening hours"}
		if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		saveRegion(t, s, page.ID, "main", "<p>We open at <strong>nine</strong> on zebracorn days.</p>")
		if err := s.publishWithShared(ctx, page.ID, nil); err != nil {
			t.Fatalf("publishWithShared: %v", err)
		}
		if !findable(t, s, "zebracorn", page.ID) {
			t.Fatal("a page published through the admin is not searchable")
		}

		// An edit is saved but not published: the site still says
		// "zebracorn", so a search must still say so too.
		saveRegion(t, s, page.ID, "main", "<p>We open at ten on quinoa days.</p>")
		if findable(t, s, "quinoa", page.ID) {
			t.Error("an unpublished edit is already searchable")
		}
		if !findable(t, s, "zebracorn", page.ID) {
			t.Error("saving a draft took the live words out of the index")
		}

		// Published: the new words are live and the old ones are not.
		if err := s.publishWithShared(ctx, page.ID, nil); err != nil {
			t.Fatalf("publishWithShared: %v", err)
		}
		if !findable(t, s, "quinoa", page.ID) {
			t.Error("publishing an edit did not put its words in the index")
		}
		if findable(t, s, "zebracorn", page.ID) {
			t.Error("the replaced words are still findable")
		}

		// And it survives being done again — the index is replaced on
		// each publish, not appended to.
		saveRegion(t, s, page.ID, "main", "<p>Closed on Tuesdays.</p>")
		if err := s.publishWithShared(ctx, page.ID, nil); err != nil {
			t.Fatalf("publishWithShared: %v", err)
		}
		if !findable(t, s, "Tuesdays", page.ID) || findable(t, s, "quinoa", page.ID) {
			t.Error("a second round of edits did not replace the index cleanly")
		}
	})
}

// Editing the footer publishes the site's shared content along with the
// page, and none of it belongs in the index: a footer is on every page, so
// indexing it would make the whole site match its copyright line.
func TestEditingSharedContentStaysOutOfTheIndex(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := indexServer(t, db)

		page := &content.Page{Slug: "about", TemplateName: "page.gohtml", Title: "About"}
		if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		saveRegion(t, s, page.ID, "main", "<p>ordinary words</p>")
		saveRegion(t, s, page.ID, render.SharedRegionPrefix+"footer",
			"<p>zebracorn holdings ltd</p>")
		if err := s.publishWithShared(ctx, page.ID, nil); err != nil {
			t.Fatalf("publishWithShared: %v", err)
		}

		results, err := s.deps.Content.Search(ctx,
			content.ParseSearchQuery("zebracorn"), "en", 0, 0)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("shared footer content is searchable: %+v", results)
		}
		if !findable(t, s, "ordinary words", page.ID) {
			t.Error("publishing with shared content did not index the page itself")
		}
	})
}

// Unpublishing from the admin takes the page out of the index at once —
// the button an editor reaches for when something went live by mistake.
func TestUnpublishingFromTheAdminEmptiesTheIndex(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := indexServer(t, db)

		page := &content.Page{Slug: "oops", TemplateName: "page.gohtml", Title: "Oops"}
		if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		saveRegion(t, s, page.ID, "main", "<p>zebracorn</p>")
		if err := s.publishWithShared(ctx, page.ID, nil); err != nil {
			t.Fatalf("publishWithShared: %v", err)
		}
		if !findable(t, s, "zebracorn", page.ID) {
			t.Fatal("the page was never indexed")
		}
		if err := s.deps.Content.Unpublish(ctx, page.ID); err != nil {
			t.Fatalf("Unpublish: %v", err)
		}
		if findable(t, s, "zebracorn", page.ID) {
			t.Error("an unpublished page is still searchable")
		}
	})
}
