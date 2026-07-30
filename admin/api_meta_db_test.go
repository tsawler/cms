package admin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// The metadata API is what the in-place editor's Page settings and Post
// settings dialogs read and write. Both are per-locale, and the whole
// point of the endpoints is that they stay that way: a save made while
// editing French must land in the French row and leave the default
// language alone.

// metaServer returns a server wired to a real store, with English as the
// default locale and French alongside it. Only the pieces these handlers
// touch are populated — they neither render templates nor read the
// session.
func metaServer(db *sqldb.DB) *server {
	return &server{deps: Deps{
		Content:       content.NewStore(db, "en"),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		DefaultLocale: "en",
		Locales:       []string{"en", "fr"},
	}}
}

// apiRequest builds a request carrying the {id} URL parameter the
// handlers read, plus an optional query string ("locale=fr").
func apiRequest(method, query, body string, id int64) *http.Request {
	target := "/api/x"
	if query != "" {
		target += "?" + query
	}
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(id, 10))
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestAPIGetPageMeta(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := metaServer(db)
		ctx := context.Background()
		page := &content.Page{Slug: "about", TemplateName: "page.gohtml",
			Title: "About Us", Description: "Who we are"}
		if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		rec := httptest.NewRecorder()
		s.apiGetPageMeta(rec, apiRequest(http.MethodGet, "locale=fr", "", page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
		}
		body := decodeJSON(t, rec)

		// Untranslated reads as empty, with the English offered
		// separately — the dialog shows it as a placeholder rather than
		// prefilling a copy of it into the French row.
		if body["title"] != "" || body["description"] != "" {
			t.Errorf("fr title/description = %q/%q, want both empty", body["title"], body["description"])
		}
		if body["inheritedTitle"] != "About Us" {
			t.Errorf("inheritedTitle = %q, want %q", body["inheritedTitle"], "About Us")
		}
		if body["inheritedDescription"] != "Who we are" {
			t.Errorf("inheritedDescription = %q, want %q", body["inheritedDescription"], "Who we are")
		}
		if body["locale"] != "fr" {
			t.Errorf("locale = %q, want fr", body["locale"])
		}

		// An unconfigured locale is not an error — it falls back to the
		// default, which is the locale the caller then edits.
		rec = httptest.NewRecorder()
		s.apiGetPageMeta(rec, apiRequest(http.MethodGet, "locale=de", "", page.ID))
		if body := decodeJSON(t, rec); body["locale"] != "en" || body["title"] != "About Us" {
			t.Errorf("unknown locale read = %+v, want the English default", body)
		}

		rec = httptest.NewRecorder()
		s.apiGetPageMeta(rec, apiRequest(http.MethodGet, "", "", page.ID+1000))
		if rec.Code != http.StatusNotFound {
			t.Errorf("missing page: status %d, want 404", rec.Code)
		}
	})
}

func TestAPISavePageMeta(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := metaServer(db)
		ctx := context.Background()
		page := &content.Page{Slug: "about", TemplateName: "page.gohtml",
			Title: "About Us", Description: "Who we are"}
		if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		rec := httptest.NewRecorder()
		s.apiSavePageMeta(rec, apiRequest(http.MethodPut, "",
			`{"locale":"fr","title":"À propos","description":"Qui nous sommes"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
		}

		fr, err := s.deps.Content.MetaFor(ctx, page.ID, "fr")
		if err != nil {
			t.Fatalf("MetaFor(fr): %v", err)
		}
		if fr.Title != "À propos" || fr.Description != "Qui nous sommes" {
			t.Errorf("fr meta = %+v, want the French values", fr)
		}
		// The English row is not collateral damage.
		en, err := s.deps.Content.MetaFor(ctx, page.ID, "en")
		if err != nil {
			t.Fatalf("MetaFor(en): %v", err)
		}
		if en.Title != "About Us" || en.Description != "Who we are" {
			t.Errorf("en meta = %+v, want the English values untouched", en)
		}

		// Clearing a translation's title is how it goes back to showing
		// the default language, so it must be allowed.
		rec = httptest.NewRecorder()
		s.apiSavePageMeta(rec, apiRequest(http.MethodPut, "",
			`{"locale":"fr","title":"  ","description":""}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("clearing fr: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if fr, err = s.deps.Content.MetaFor(ctx, page.ID, "fr"); err != nil {
			t.Fatalf("MetaFor(fr): %v", err)
		}
		if fr.Title != "" {
			t.Errorf("fr title = %q, want empty so the page inherits again", fr.Title)
		}
		resolved, err := s.deps.Content.GetByID(ctx, page.ID, "fr")
		if err != nil {
			t.Fatalf("GetByID(fr): %v", err)
		}
		if resolved.Title != "About Us" {
			t.Errorf("resolved fr title = %q, want the English fallback", resolved.Title)
		}

		// The default locale has nothing to fall back to, so an empty
		// title there is a mistake rather than an instruction.
		rec = httptest.NewRecorder()
		s.apiSavePageMeta(rec, apiRequest(http.MethodPut, "",
			`{"locale":"en","title":"","description":"x"}`, page.ID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("empty default-locale title: status %d, want 422", rec.Code)
		}
		if en, err = s.deps.Content.MetaFor(ctx, page.ID, "en"); err != nil {
			t.Fatalf("MetaFor(en): %v", err)
		}
		if en.Title != "About Us" {
			t.Errorf("en title = %q, want the rejected save to have changed nothing", en.Title)
		}

		// An unconfigured locale is rejected outright rather than
		// silently redirected onto the default language's row.
		rec = httptest.NewRecorder()
		s.apiSavePageMeta(rec, apiRequest(http.MethodPut, "",
			`{"locale":"de","title":"Über uns"}`, page.ID))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("unknown locale: status %d, want 400", rec.Code)
		}
	})
}

// Typing over the heading on the page saves a title and nothing else.
// The description is not on screen there, so a body without one means
// "leave it alone" — sending it as empty would quietly delete the page's
// meta description every time someone fixed a typo in the heading.
func TestAPISavePageMetaLeavesOutOmittedFields(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := metaServer(db)
		ctx := context.Background()
		page := &content.Page{Slug: "about", TemplateName: "page.gohtml",
			Title: "About Us", Description: "Who we are"}
		if _, err := s.deps.Content.Insert(ctx, page, "en"); err != nil {
			t.Fatalf("Insert: %v", err)
		}

		rec := httptest.NewRecorder()
		s.apiSavePageMeta(rec, apiRequest(http.MethodPut, "",
			`{"locale":"en","title":"About the team"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
		}
		en, err := s.deps.Content.MetaFor(ctx, page.ID, "en")
		if err != nil {
			t.Fatalf("MetaFor(en): %v", err)
		}
		if en.Title != "About the team" {
			t.Errorf("en title = %q, want the new one", en.Title)
		}
		if en.Description != "Who we are" {
			t.Errorf("en description = %q, want it left alone", en.Description)
		}

		// The other way round holds too: the dialogs are free to save a
		// description without restating the title.
		rec = httptest.NewRecorder()
		s.apiSavePageMeta(rec, apiRequest(http.MethodPut, "",
			`{"locale":"en","description":"The people behind it"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("description-only save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if en, err = s.deps.Content.MetaFor(ctx, page.ID, "en"); err != nil {
			t.Fatalf("MetaFor(en): %v", err)
		}
		if en.Title != "About the team" || en.Description != "The people behind it" {
			t.Errorf("en meta = %+v, want the kept title and the new description", en)
		}

		// Sending a field empty still means empty — the dialogs clear a
		// translation that way.
		rec = httptest.NewRecorder()
		s.apiSavePageMeta(rec, apiRequest(http.MethodPut, "",
			`{"locale":"en","description":""}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("clearing description: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if en, err = s.deps.Content.MetaFor(ctx, page.ID, "en"); err != nil {
			t.Fatalf("MetaFor(en): %v", err)
		}
		if en.Description != "" {
			t.Errorf("en description = %q, want it cleared", en.Description)
		}

		// A title-only save on a translation is judged on the title it
		// sends, not on the empty description it doesn't.
		rec = httptest.NewRecorder()
		s.apiSavePageMeta(rec, apiRequest(http.MethodPut, "",
			`{"locale":"fr","title":"À propos"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("fr title-only save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		fr, err := s.deps.Content.MetaFor(ctx, page.ID, "fr")
		if err != nil {
			t.Fatalf("MetaFor(fr): %v", err)
		}
		if fr.Title != "À propos" {
			t.Errorf("fr title = %q, want the French one", fr.Title)
		}
	})
}

// The post-settings gear saves the title and summary of whichever locale
// the editor is rendered in. Writing them to the default locale instead
// puts French words in the English metadata and leaves the French page
// still showing English.
func TestAPIUpdatePostSettingsWritesTheRequestLocale(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := metaServer(db)
		ctx := context.Background()
		post := &content.Post{
			Page: content.Page{Slug: "blog/launch-day", TemplateName: "post.gohtml",
				Title: "Launch Day", Description: "We shipped it"},
			Feed: content.FeedBlog,
		}
		if _, err := s.deps.Content.InsertPost(ctx, post, "en"); err != nil {
			t.Fatalf("InsertPost: %v", err)
		}

		rec := httptest.NewRecorder()
		s.apiUpdatePostSettings(rec, apiRequest(http.MethodPut, "locale=fr",
			`{"title":"Jour du lancement","summary":"Nous l'avons lancé"}`, post.PostID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
		}

		fr, err := s.deps.Content.MetaFor(ctx, post.ID, "fr")
		if err != nil {
			t.Fatalf("MetaFor(fr): %v", err)
		}
		if fr.Title != "Jour du lancement" || fr.Description != "Nous l'avons lancé" {
			t.Errorf("fr meta = %+v, want the French title and summary", fr)
		}
		en, err := s.deps.Content.MetaFor(ctx, post.ID, "en")
		if err != nil {
			t.Fatalf("MetaFor(en): %v", err)
		}
		if en.Title != "Launch Day" || en.Description != "We shipped it" {
			t.Errorf("en meta = %+v, want the English title and summary untouched", en)
		}

		// Without a locale the save is the default language's, as before.
		rec = httptest.NewRecorder()
		s.apiUpdatePostSettings(rec, apiRequest(http.MethodPut, "",
			`{"title":"Launch Day!","summary":"We shipped it at last"}`, post.PostID))
		if rec.Code != http.StatusOK {
			t.Fatalf("default-locale save: status %d, want 200: %s", rec.Code, rec.Body)
		}
		if en, err = s.deps.Content.MetaFor(ctx, post.ID, "en"); err != nil {
			t.Fatalf("MetaFor(en): %v", err)
		}
		if en.Title != "Launch Day!" || en.Description != "We shipped it at last" {
			t.Errorf("en meta = %+v, want the edited English values", en)
		}
		if fr, err = s.deps.Content.MetaFor(ctx, post.ID, "fr"); err != nil {
			t.Fatalf("MetaFor(fr): %v", err)
		}
		if fr.Title != "Jour du lancement" {
			t.Errorf("fr title = %q, want the French left alone", fr.Title)
		}

		// A post still needs a title in the default language.
		rec = httptest.NewRecorder()
		s.apiUpdatePostSettings(rec, apiRequest(http.MethodPut, "",
			`{"title":"","summary":"x"}`, post.PostID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("empty default-locale title: status %d, want 422", rec.Code)
		}
	})
}
