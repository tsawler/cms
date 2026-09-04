package admin

// The history screens end to end: the list, the preview of a stored
// edition through the real site templates, and the restore that puts one
// back. What matters most here is what the handlers refuse — a version id
// belonging to another page — and that a plain restore leaves the public
// site exactly as it was.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

// versionsServer wires a server to a real store and a template set whose
// page template renders one region, so a preview has something to draw.
func versionsServer(t *testing.T, db *sqldb.DB) *server {
	t.Helper()
	fsys := fstest.MapFS{
		"base.gohtml": &fstest.MapFile{Data: []byte(
			`{{define "base"}}<html><body><h1>{{.Title}}</h1>` +
				`{{block "content" .}}{{end}}</body></html>{{end}}`)},
		"page.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}{{cmsRegion "main"}}{{end}}`)},
	}
	r, err := render.New(fsys, []string{"base.gohtml"},
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
		AdminPath:     "/admin",
		DefaultLocale: "en",
		Locales:       []string{"en"},
	}}
}

// versionRequest builds a request carrying the {id} and {vid} URL
// parameters the handlers read, signed in as a superadmin — the Pages
// section's routes are superadmin-only, and the handlers read the user off
// the session to attribute a publish.
func versionRequest(t *testing.T, s *server, method, body string, pageID, versionID int64) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, "/admin/pages/x/versions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(pageID, 10))
	if versionID != 0 {
		rctx.URLParams.Add("vid", strconv.FormatInt(versionID, 10))
	}
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	ctx := context.Background()
	u, err := s.deps.Users.GetByEmail(ctx, "history@example.com")
	if errors.Is(err, auth.ErrNotFound) {
		u = &auth.User{Email: "history@example.com", Name: "Hera Historian",
			PasswordHash: "x", Role: auth.RoleSuperadmin, Active: true}
		_, err = s.deps.Users.Insert(ctx, u)
	}
	if err != nil {
		t.Fatalf("seeding the session user: %v", err)
	}
	sctx, err := s.deps.Sessions.Load(r.Context(), "")
	if err != nil {
		t.Fatalf("loading a session: %v", err)
	}
	s.deps.Sessions.Put(sctx, sessionKeyUserID, u.ID)
	return r.WithContext(sctx)
}

// seedHistory makes a page with two editions: "<p>old</p>", then
// "<p>new</p>", which is what the site is serving.
func seedHistory(t *testing.T, s *server, slug string) (*content.Page, []content.Version) {
	t.Helper()
	ctx := context.Background()
	page := &content.Page{Slug: slug, TemplateName: "page.gohtml", Title: "Original"}
	if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	for _, body := range []string{"<p>old</p>", "<p>new</p>"} {
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en", content.KindHTML, body); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.deps.Content.Publish(ctx, page.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	versions, err := s.deps.Content.Versions(ctx, page.ID)
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("seeded %d editions, want 2", len(versions))
	}
	return page, versions
}

func TestVersionPreviewRendersTheStoredEdition(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := versionsServer(t, db)
		page, versions := seedHistory(t, s, "history-preview")
		oldest := versions[1]

		rec := httptest.NewRecorder()
		s.pageVersionPreview(rec, versionRequest(t, s, "GET", "", page.ID, oldest.ID))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<p>old</p>") {
			t.Errorf("preview does not show the edition's content: %s", body)
		}
		if strings.Contains(body, "<p>new</p>") {
			t.Errorf("preview shows the live content instead of the edition's: %s", body)
		}
	})
}

// A version id from another page must not render under this page's URL.
func TestVersionPreviewRefusesAnotherPagesVersion(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := versionsServer(t, db)
		_, mine := seedHistory(t, s, "preview-mine")
		theirs, _ := seedHistory(t, s, "preview-theirs")

		rec := httptest.NewRecorder()
		s.pageVersionPreview(rec, versionRequest(t, s, "GET", "", theirs.ID, mine[1].ID))

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}

// A plain restore stages the old content and leaves the site alone.
func TestVersionRestoreStagesWithoutPublishing(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := versionsServer(t, db)
		page, versions := seedHistory(t, s, "restore-draft")
		oldest := versions[1]

		rec := httptest.NewRecorder()
		s.pageVersionRestore(rec, versionRequest(t, s, "POST", "action=draft", page.ID, oldest.ID))

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Location"); got != "/admin/pages/"+strconv.FormatInt(page.ID, 10) {
			t.Errorf("redirected to %q, want the page's form", got)
		}

		draft, err := s.deps.Content.BlocksFor(ctx, page.ID, "en", content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor(draft): %v", err)
		}
		if len(draft) != 1 || draft[0].Content != "<p>old</p>" {
			t.Errorf("draft = %+v, want the restored content", draft)
		}
		live, err := s.deps.Content.BlocksFor(ctx, page.ID, "en", content.StatusPublished)
		if err != nil {
			t.Fatalf("BlocksFor(published): %v", err)
		}
		if len(live) != 1 || live[0].Content != "<p>new</p>" {
			t.Errorf("published = %+v, want the site untouched by a plain restore", live)
		}
		// No new edition: restoring is an edit, and only publishing records.
		list, err := s.deps.Content.Versions(ctx, page.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 2 {
			t.Errorf("got %d editions after a restore, want the same 2", len(list))
		}
	})
}

// "Restore & publish" does both, and the edition it records carries the
// user who clicked it.
func TestVersionRestoreAndPublishGoesLive(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := versionsServer(t, db)
		page, versions := seedHistory(t, s, "restore-live")
		oldest := versions[1]

		rec := httptest.NewRecorder()
		s.pageVersionRestore(rec, versionRequest(t, s, "POST", "action=publish", page.ID, oldest.ID))

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
		}
		live, err := s.deps.Content.BlocksFor(ctx, page.ID, "en", content.StatusPublished)
		if err != nil {
			t.Fatalf("BlocksFor(published): %v", err)
		}
		if len(live) != 1 || live[0].Content != "<p>old</p>" {
			t.Errorf("published = %+v, want the restored content live", live)
		}

		list, err := s.deps.Content.Versions(ctx, page.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		if len(list) != 3 {
			t.Fatalf("got %d editions after restoring and publishing, want 3", len(list))
		}
		if list[0].SavedBy == nil {
			t.Error("the edition a restore-and-publish created is unattributed")
		}
		if list[0].SavedByName != "Hera Historian" {
			t.Errorf("saved_by name = %q, want the user who published", list[0].SavedByName)
		}
	})
}

func TestVersionRestoreRefusesAnotherPagesVersion(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := versionsServer(t, db)
		_, mine := seedHistory(t, s, "restore-mine")
		theirs, _ := seedHistory(t, s, "restore-theirs")

		rec := httptest.NewRecorder()
		s.pageVersionRestore(rec, versionRequest(t, s, "POST", "action=draft", theirs.ID, mine[1].ID))

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		draft, err := s.deps.Content.BlocksFor(ctx, theirs.ID, "en", content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor: %v", err)
		}
		if len(draft) != 1 || draft[0].Content != "<p>new</p>" {
			t.Errorf("draft = %+v, want it untouched", draft)
		}
	})
}

// The list screen renders, newest first, and flags a page whose draft
// would be overwritten.
func TestVersionsListWarnsAboutUnpublishedEdits(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := versionsServer(t, db)
		s.templates = parseTemplates()
		page, _ := seedHistory(t, s, "history-list")

		rec := httptest.NewRecorder()
		s.pageVersions(rec, versionRequest(t, s, "GET", "", page.ID, 0))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "unpublished draft changes") {
			t.Error("a page with no draft edits warned about them anyway")
		}

		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>work in progress</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		rec = httptest.NewRecorder()
		s.pageVersions(rec, versionRequest(t, s, "GET", "", page.ID, 0))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "unpublished draft changes") {
			t.Error("a page with draft edits did not warn that a restore would replace them")
		}
	})
}

// The editor's History dialog reads this: editions newest first, dates
// already formatted (the editor has no translation table of its own), and
// a flag for whether restoring is about to overwrite unpublished work.
func TestAPIPageVersions(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := versionsServer(t, db)
		page, versions := seedHistory(t, s, "api-history")

		rec := httptest.NewRecorder()
		s.apiPageVersions(rec, versionRequest(t, s, "GET", "", page.ID, 0))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Versions []struct {
				ID    int64  `json:"id"`
				Label string `json:"label"`
				By    string `json:"by"`
			} `json:"versions"`
			HasUnpublished bool `json:"has_unpublished"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding %q: %v", rec.Body.String(), err)
		}
		if len(body.Versions) != 2 {
			t.Fatalf("got %d editions, want 2", len(body.Versions))
		}
		if body.Versions[0].ID != versions[0].ID || body.Versions[1].ID != versions[1].ID {
			t.Errorf("editions came back as %d, %d, want %d, %d newest first",
				body.Versions[0].ID, body.Versions[1].ID, versions[0].ID, versions[1].ID)
		}
		// The label is a rendered date, not a timestamp for the editor to
		// parse — the year is enough to tell the two apart.
		if !strings.Contains(body.Versions[0].Label, strconv.Itoa(versions[0].SavedAt.Year())) {
			t.Errorf("label = %q, want a formatted date", body.Versions[0].Label)
		}
		if body.HasUnpublished {
			t.Error("has_unpublished is true for a page with no draft edits")
		}

		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>unsaved work</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		rec = httptest.NewRecorder()
		s.apiPageVersions(rec, versionRequest(t, s, "GET", "", page.ID, 0))
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding %q: %v", rec.Body.String(), err)
		}
		if !body.HasUnpublished {
			t.Error("has_unpublished is false while the page holds draft edits")
		}
	})
}

// The editor's restore stages and nothing more: it publishes nothing, and
// the reload it triggers lands on the restored draft.
func TestAPIRestoreVersion(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := versionsServer(t, db)
		page, versions := seedHistory(t, s, "api-restore")

		rec := httptest.NewRecorder()
		s.apiRestoreVersion(rec, versionRequest(t, s, "POST", "", page.ID, versions[1].ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		draft, err := s.deps.Content.BlocksFor(ctx, page.ID, "en", content.StatusDraft)
		if err != nil {
			t.Fatalf("BlocksFor(draft): %v", err)
		}
		if len(draft) != 1 || draft[0].Content != "<p>old</p>" {
			t.Errorf("draft = %+v, want the restored content", draft)
		}
		live, err := s.deps.Content.BlocksFor(ctx, page.ID, "en", content.StatusPublished)
		if err != nil {
			t.Fatalf("BlocksFor(published): %v", err)
		}
		if len(live) != 1 || live[0].Content != "<p>new</p>" {
			t.Errorf("published = %+v, want the site untouched", live)
		}
	})
}

// A version id belonging to another page is refused with JSON, not with
// somebody else's content.
func TestAPIRestoreVersionRefusesAnotherPagesVersion(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := versionsServer(t, db)
		_, mine := seedHistory(t, s, "api-mine")
		theirs, _ := seedHistory(t, s, "api-theirs")

		rec := httptest.NewRecorder()
		s.apiRestoreVersion(rec, versionRequest(t, s, "POST", "", theirs.ID, mine[1].ID))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404: %s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type = %q, want JSON so the editor can read the error", ct)
		}
	})
}

// The restore handlers tell whoever clicked what became of the page's
// custom-code blocks: the flash on the admin screen, a note in the JSON
// the editor reads before it reloads.
func TestVersionRestoreReportsCodeBlocks(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := versionsServer(t, db)
		code := snippets.NewCodeStore(db)

		for _, c := range []snippets.CodeSnippet{
			{Key: "gone", Name: "Deleted later", HTML: "<b>gone v1</b>"},
			{Key: "moved", Name: "Changed later", HTML: "<b>moved v1</b>"},
		} {
			if _, err := code.Insert(ctx, &c); err != nil {
				t.Fatalf("seeding %q: %v", c.Key, err)
			}
		}

		page := &content.Page{Slug: "code-report", TemplateName: "page.gohtml", Title: "Code"}
		if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		markup := `<div data-cms-code="gone"></div><div data-cms-code="moved"></div>`
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, markup); err != nil {
			t.Fatalf("UpsertDraftBlock: %v", err)
		}
		if err := s.deps.Content.Publish(ctx, page.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		versions, err := s.deps.Content.Versions(ctx, page.ID)
		if err != nil {
			t.Fatalf("Versions: %v", err)
		}
		edition := versions[0]

		// One block is deleted, the other rewritten, and the page moves on.
		if err := code.Delete(ctx, "gone"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := code.Update(ctx, &snippets.CodeSnippet{
			Key: "moved", Name: "Changed later", HTML: "<b>moved v2</b>"}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>no blocks</p>"); err != nil {
			t.Fatalf("UpsertDraftBlock(second): %v", err)
		}
		if err := s.deps.Content.Publish(ctx, page.ID); err != nil {
			t.Fatalf("Publish(second): %v", err)
		}

		// The editor's JSON carries the note, so it can be read before the
		// reload that would swallow a toast.
		rec := httptest.NewRecorder()
		s.apiRestoreVersion(rec, versionRequest(t, s, "POST", "", page.ID, edition.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Note string `json:"note"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding %q: %v", rec.Body.String(), err)
		}
		if !strings.Contains(body.Note, "gone") {
			t.Errorf("note = %q, want it to name the block that was put back", body.Note)
		}
		if !strings.Contains(body.Note, "moved") {
			t.Errorf("note = %q, want it to name the block that had changed", body.Note)
		}

		// The deleted block is back; the changed one was left alone.
		back, err := code.ByKey(ctx, "gone")
		if err != nil {
			t.Fatalf("ByKey(gone): %v", err)
		}
		if back.HTML != "<b>gone v1</b>" {
			t.Errorf("recreated block = %q, want the body the edition froze", back.HTML)
		}
		still, err := code.ByKey(ctx, "moved")
		if err != nil {
			t.Fatalf("ByKey(moved): %v", err)
		}
		if still.HTML != "<b>moved v2</b>" {
			t.Errorf("shared block = %q, want a restore to have left it alone", still.HTML)
		}
	})
}
