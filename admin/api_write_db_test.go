package admin

// The in-place editor's write endpoints. apiSaveSections is the one the
// product runs on — every edit made on the page itself arrives here — and
// it was the largest handler in the admin that no test had ever executed.
// The rest are the buttons around it: duplicate, delete, unpublish,
// discard, visibility, per-page code, and dropping a translation.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// jsonReq is formReq for the editor's API: a JSON body rather than a form,
// and the {id} parameter the handlers read.
func jsonReq(t *testing.T, s *server, u *auth.User, method, target, body string, id int64) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return withRouteAndSession(t, s, u, r, idParams(id))
}

// sectionsOf reads a region's draft sections back in order, which is what
// every apiSaveSections assertion is about.
func sectionsOf(t *testing.T, s *server, pageID int64, region, locale string) []content.Block {
	t.Helper()
	blocks, err := s.deps.Content.BlocksFor(context.Background(), pageID, locale, content.StatusDraft)
	if err != nil {
		t.Fatalf("reading blocks: %v", err)
	}
	var out []content.Block
	for _, b := range blocks {
		if b.Region == region {
			out = append(out, b)
		}
	}
	return out
}

func TestAPISaveSections(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "sections-save", Title: "Sections"})

		body := `{"region":"main","sections":[
			{"html":"<p>one</p>","bg":"dark","width":"wide"},
			{"html":"<p>two</p>","bg":"light","width":"full"}
		]}`
		rec := httptest.NewRecorder()
		s.apiSaveSections(rec, jsonReq(t, s, u, http.MethodPost, "/api/pages/x/sections", body, page.ID))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}

		got := sectionsOf(t, s, page.ID, "main", "en")
		if len(got) != 2 {
			t.Fatalf("stored %d sections, want 2", len(got))
		}
		// Order is the list's order: a section list that came back
		// shuffled would rearrange the page.
		if !strings.Contains(got[0].Content, "one") || !strings.Contains(got[1].Content, "two") {
			t.Errorf("sections = %q / %q, want them in submitted order",
				got[0].Content, got[1].Content)
		}
		if got[0].Settings["bg"] != "dark" || got[0].Settings["width"] != "wide" {
			t.Errorf("first section settings = %v, want bg=dark width=wide", got[0].Settings)
		}
	})
}

// A replace is a replace: the submitted list is the region, so a section
// dropped in the editor has to be gone from storage too.
func TestAPISaveSectionsReplacesTheWholeRegion(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "sections-replace", Title: "Replace"})

		three := `{"region":"main","sections":[{"html":"<p>a</p>"},{"html":"<p>b</p>"},{"html":"<p>c</p>"}]}`
		rec := httptest.NewRecorder()
		s.apiSaveSections(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", three, page.ID))
		if n := len(sectionsOf(t, s, page.ID, "main", "en")); n != 3 {
			t.Fatalf("stored %d sections, want 3", n)
		}

		one := `{"region":"main","sections":[{"html":"<p>only this</p>"}]}`
		rec = httptest.NewRecorder()
		s.apiSaveSections(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", one, page.ID))
		got := sectionsOf(t, s, page.ID, "main", "en")
		if len(got) != 1 {
			t.Fatalf("stored %d sections, want 1 after the shorter save", len(got))
		}
		if !strings.Contains(got[0].Content, "only this") {
			t.Errorf("section = %q, want the submitted one", got[0].Content)
		}

		// An empty list clears the region rather than being ignored.
		empty := `{"region":"main","sections":[]}`
		rec = httptest.NewRecorder()
		s.apiSaveSections(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", empty, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if n := len(sectionsOf(t, s, page.ID, "main", "en")); n != 0 {
			t.Errorf("stored %d sections, want the region cleared", n)
		}
	})
}

// Settings arrive from a client and are resolved against the configured
// options, so an unknown key stores the fallback rather than junk that
// would render as a class nothing has styled.
func TestAPISaveSectionsResolvesSettingsAgainstTheConfiguredOptions(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "sections-settings", Title: "Settings"})

		body := `{"region":"main","sections":[{
			"html":"<p>x</p>","bg":"chartreuse","width":"gigantic",
			"corners":"nonsense","padding":"nonsense","size":"nonsense",
			"height":"not-a-height","valign":"sideways",
			"bgcolor":"javascript:alert(1)","bgimage":"javascript:alert(1)",
			"bgposition":"nowhere"
		}]}`
		rec := httptest.NewRecorder()
		s.apiSaveSections(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", body, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}

		got := sectionsOf(t, s, page.ID, "main", "en")
		if len(got) != 1 {
			t.Fatalf("stored %d sections, want 1", len(got))
		}
		set := got[0].Settings
		// The curated axes fall back to their first option.
		if set["bg"] != "default" {
			t.Errorf("bg = %q, want the default fallback", set["bg"])
		}
		if set["width"] != "normal" {
			t.Errorf("width = %q, want the default fallback", set["width"])
		}
		// A non-default choice is what gets stored on the optional axes,
		// so an unrecognized one stores nothing at all.
		for _, key := range []string{"corners", "padding", "size"} {
			if v, ok := set[key]; ok {
				t.Errorf("%s = %q, want the resolved default left unstored", key, v)
			}
		}
		// Free-form values are dropped when they do not validate — these
		// are the ones that reach a browser as inline styles.
		for _, key := range []string{"height", "valign", "bgcolor", "bgimage", "bgposition"} {
			if v, ok := set[key]; ok {
				t.Errorf("%s = %q, want an invalid value dropped", key, v)
			}
		}
	})
}

// Valid free-form values do land, and a background position only means
// anything when there is an image to position.
func TestAPISaveSectionsStoresValidCustomBackgrounds(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "sections-bg", Title: "Backgrounds"})

		// A position is the focal point as percentages, and "50% 50%"
		// is the default, so only an off-centre one is worth storing.
		body := `{"region":"main","sections":[
			{"html":"<p>with image</p>","bgimage":"/media/pic.jpg","bgposition":"20% 80%","bgcolor":"#112233"},
			{"html":"<p>no image</p>","bgposition":"20% 80%"},
			{"html":"<p>centred</p>","bgimage":"/media/pic.jpg","bgposition":"50% 50%"}
		]}`
		rec := httptest.NewRecorder()
		s.apiSaveSections(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", body, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}

		got := sectionsOf(t, s, page.ID, "main", "en")
		if len(got) != 3 {
			t.Fatalf("stored %d sections, want 3", len(got))
		}
		if got[0].Settings["bgimage"] != "/media/pic.jpg" {
			t.Errorf("bgimage = %q, want the submitted URL", got[0].Settings["bgimage"])
		}
		if got[0].Settings["bgcolor"] != "#112233" {
			t.Errorf("bgcolor = %q, want the submitted colour", got[0].Settings["bgcolor"])
		}
		if got[0].Settings["bgposition"] != "20% 80%" {
			t.Errorf("bgposition = %q, want it stored alongside the image", got[0].Settings["bgposition"])
		}
		if v, ok := got[1].Settings["bgposition"]; ok {
			t.Errorf("bgposition = %q on a section with no image, want it dropped", v)
		}
		if v, ok := got[2].Settings["bgposition"]; ok {
			t.Errorf("bgposition = %q for the centred default, want it left unstored", v)
		}
	})
}

// Section HTML is contenteditable output. An editor's is untrusted and
// gets sanitized; an admin's is stored as sent.
func TestAPISaveSectionsSanitizesForNonAdmins(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		editor := formEditor(t, s)
		admin := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "sections-sanitize", Title: "Sanitize"})

		body := `{"region":"main","sections":[{"html":"<p onclick=\"steal()\">hi</p><script>alert(1)</script>"}]}`

		rec := httptest.NewRecorder()
		s.apiSaveSections(rec, jsonReq(t, s, editor, http.MethodPost, "/api/x", body, page.ID))
		got := sectionsOf(t, s, page.ID, "main", "en")
		if len(got) != 1 {
			t.Fatalf("stored %d sections, want 1", len(got))
		}
		if strings.Contains(got[0].Content, "<script") || strings.Contains(got[0].Content, "onclick") {
			t.Errorf("an editor's section was stored unsanitized: %q", got[0].Content)
		}

		rec = httptest.NewRecorder()
		s.apiSaveSections(rec, jsonReq(t, s, admin, http.MethodPost, "/api/x", body, page.ID))
		got = sectionsOf(t, s, page.ID, "main", "en")
		if !strings.Contains(got[0].Content, "<script>") {
			t.Errorf("an admin's section = %q, want it stored as sent", got[0].Content)
		}
	})
}

func TestAPISaveSectionsRejectsBadRequests(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "sections-reject", Title: "Reject"})

		var manySections strings.Builder
		manySections.WriteString(`{"region":"main","sections":[`)
		for i := range 101 {
			if i > 0 {
				manySections.WriteString(",")
			}
			manySections.WriteString(`{"html":"<p>x</p>"}`)
		}
		manySections.WriteString(`]}`)

		cases := map[string]struct {
			body   string
			status int
			marker string
		}{
			"unreadable JSON": {`{"region":`, http.StatusBadRequest, "Could not read"},
			// A region the page's template does not declare — or one it
			// declares as something other than sections — is not a place
			// sections can be stored.
			"unknown region":    {`{"region":"nope","sections":[]}`, http.StatusBadRequest, "Unknown sections area"},
			"too many sections": {manySections.String(), http.StatusBadRequest, "Too many sections"},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				s.apiSaveSections(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", c.body, page.ID))
				if rec.Code != c.status {
					t.Errorf("status = %d, want %d (body %q)", rec.Code, c.status, rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), c.marker) {
					t.Errorf("body = %q, want it to mention %q", rec.Body.String(), c.marker)
				}
			})
		}
	})
}

// A save made while editing French belongs in French, and must leave the
// default language exactly as it was.
func TestAPISaveSectionsIsPerLocale(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "sections-locale", Title: "Locale"})

		en := `{"region":"main","sections":[{"html":"<p>English</p>"}]}`
		fr := `{"locale":"fr","region":"main","sections":[{"html":"<p>Français</p>"}]}`
		for _, body := range []string{en, fr} {
			rec := httptest.NewRecorder()
			s.apiSaveSections(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", body, page.ID))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
			}
		}

		if got := draftHTML(t, s, page.ID, "main", "en"); !strings.Contains(got, "English") {
			t.Errorf("English sections = %q, want the English save", got)
		}
		if got := draftHTML(t, s, page.ID, "main", "fr"); !strings.Contains(got, "Français") {
			t.Errorf("French sections = %q, want the French save", got)
		}
		if got := draftHTML(t, s, page.ID, "main", "en"); strings.Contains(got, "Français") {
			t.Errorf("English sections = %q, want the French save kept out", got)
		}
	})
}

// An unknown locale falls back to the default rather than creating a
// language nobody configured — the same rule every other locale-carrying
// endpoint follows.
func TestAPISaveSectionsFallsBackForAnUnknownLocale(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "sections-badlocale", Title: "Bad locale"})

		body := `{"locale":"xx","region":"main","sections":[{"html":"<p>somewhere</p>"}]}`
		rec := httptest.NewRecorder()
		s.apiSaveSections(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", body, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := draftHTML(t, s, page.ID, "main", "en"); !strings.Contains(got, "somewhere") {
			t.Errorf("default-locale sections = %q, want the save to have landed there", got)
		}
	})
}

func TestAPIDeletePage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "api-delete", Title: "Delete"})

		rec := httptest.NewRecorder()
		s.apiDeletePage(rec, jsonReq(t, s, u, http.MethodDelete, "/api/x", "", page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if _, err := s.deps.Content.GetByID(context.Background(), page.ID, "en"); err == nil {
			t.Error("the page is still there")
		}

		// The home page has to keep answering at "/".
		home := seedPage(t, s, &content.Page{Slug: "", Title: "Home"})
		rec = httptest.NewRecorder()
		s.apiDeletePage(rec, jsonReq(t, s, u, http.MethodDelete, "/api/x", "", home.ID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422 for the home page", rec.Code)
		}
		if _, err := s.deps.Content.GetByID(context.Background(), home.ID, "en"); err != nil {
			t.Errorf("the home page was deleted anyway: %v", err)
		}
	})
}

func TestAPIUnpublishAndDiscard(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()
		page := seedPage(t, s, &content.Page{Slug: "api-unpublish", Title: "Live"})

		// Discarding before anything is live is a conflict, not a no-op:
		// there is nothing to revert to.
		rec := httptest.NewRecorder()
		s.apiDiscard(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", "", page.ID))
		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409 for an unpublished page", rec.Code)
		}

		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>live</p>"); err != nil {
			t.Fatalf("seeding content: %v", err)
		}
		if err := s.deps.Content.Publish(ctx, page.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>rewrite</p>"); err != nil {
			t.Fatalf("seeding a draft edit: %v", err)
		}

		rec = httptest.NewRecorder()
		s.apiDiscard(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", "", page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("discard status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if got := draftHTML(t, s, page.ID, "main", "en"); strings.Contains(got, "rewrite") {
			t.Errorf("draft = %q, want the edit discarded", got)
		}

		rec = httptest.NewRecorder()
		s.apiUnpublish(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", "", page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("unpublish status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding %q: %v", rec.Body.String(), err)
		}
		if out["status"] != "draft" {
			t.Errorf("status field = %v, want draft", out["status"])
		}
		if got := reloadPage(t, s, page.ID).Status; got == content.StatusPublished {
			t.Error("the page is still published")
		}
	})
}

func TestAPISetVisibility(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "api-visibility", Title: "Visibility"})

		rec := httptest.NewRecorder()
		s.apiSetVisibility(rec, jsonReq(t, s, u, http.MethodPut, "/api/x", `{"visibility":"private"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if got := reloadPage(t, s, page.ID).Visibility; got != content.VisibilityPrivate {
			t.Errorf("visibility = %q, want private", got)
		}

		// Anything that is not one of the two known values is refused
		// rather than stored — the renderer has to understand whatever
		// is in that column.
		for _, bad := range []string{`{"visibility":"secret"}`, `{"visibility":""}`, `not json`} {
			rec = httptest.NewRecorder()
			s.apiSetVisibility(rec, jsonReq(t, s, u, http.MethodPut, "/api/x", bad, page.ID))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d for %q, want 400", rec.Code, bad)
			}
		}
		if got := reloadPage(t, s, page.ID).Visibility; got != content.VisibilityPrivate {
			t.Errorf("visibility = %q, want the refused values to have changed nothing", got)
		}
	})
}

func TestAPIDuplicatePage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()
		page := seedPage(t, s, &content.Page{Slug: "api-duplicate", Title: "Original"})
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>copy me</p>"); err != nil {
			t.Fatalf("seeding content: %v", err)
		}

		rec := httptest.NewRecorder()
		s.apiDuplicatePage(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", `{"title":"Copy Of It"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding %q: %v", rec.Body.String(), err)
		}
		if out["slug"] != "copy-of-it" {
			t.Errorf("slug = %v, want it derived from the title", out["slug"])
		}
		copyPage, err := s.deps.Content.GetBySlug(ctx, "copy-of-it", "en", false)
		if err != nil {
			t.Fatalf("the copy was not stored: %v", err)
		}
		if got := draftHTML(t, s, copyPage.ID, "main", "en"); !strings.Contains(got, "copy me") {
			t.Errorf("the copy's content = %q, want the original's", got)
		}

		// A second copy under the same name gets a suffix rather than
		// failing on the taken address.
		rec = httptest.NewRecorder()
		s.apiDuplicatePage(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", `{"title":"Copy Of It"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("second duplicate status = %d, want 200", rec.Code)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding %q: %v", rec.Body.String(), err)
		}
		if out["slug"] != "copy-of-it-2" {
			t.Errorf("second slug = %v, want a numeric suffix", out["slug"])
		}
	})
}

func TestAPIDuplicatePageRefusals(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "api-dup-refuse", Title: "Original"})

		rec := httptest.NewRecorder()
		s.apiDuplicatePage(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", `{"title":"  "}`, page.ID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d for a nameless copy, want 422", rec.Code)
		}

		// A post is more than its backing page — feed, date, author — so
		// copying just the page would strand half a post.
		post := seedPost(t, s, content.FeedBlog, "A post", "dup-post")
		rec = httptest.NewRecorder()
		s.apiDuplicatePage(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", `{"title":"Copy"}`, post.ID))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d for duplicating a post, want 400", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "can't be duplicated") {
			t.Errorf("body = %q, want it to say why", rec.Body.String())
		}
	})
}

// A title whose slug would start with a locale code would be shadowed by
// the language prefix, so the copy gets an address that is reachable.
func TestAPIDuplicatePageAvoidsALocalePrefix(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "api-dup-locale", Title: "Original"})

		rec := httptest.NewRecorder()
		s.apiDuplicatePage(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", `{"title":"fr"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding %q: %v", rec.Body.String(), err)
		}
		if out["slug"] == "fr" {
			t.Error(`slug = "fr", want it moved off the locale prefix`)
		}
	})
}

func TestAPIRevertLocale(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()
		page := seedPage(t, s, &content.Page{Slug: "api-revert", Title: "English"})
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>English</p>"); err != nil {
			t.Fatalf("seeding English: %v", err)
		}
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "fr",
			content.KindHTML, "<p>Français</p>"); err != nil {
			t.Fatalf("seeding French: %v", err)
		}

		rec := httptest.NewRecorder()
		s.apiRevertLocale(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", `{"locale":"fr"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if got := draftHTML(t, s, page.ID, "main", "fr"); got != "" {
			t.Errorf("French content = %q, want it dropped", got)
		}
		// Reverting a translation must not touch the language it falls
		// back to.
		if got := draftHTML(t, s, page.ID, "main", "en"); !strings.Contains(got, "English") {
			t.Errorf("English content = %q, want it untouched", got)
		}

		// The default locale is what everything falls back to, so it is
		// not a translation that can be dropped.
		for _, bad := range []string{`{"locale":"en"}`, `{"locale":"xx"}`, `not json`} {
			rec = httptest.NewRecorder()
			s.apiRevertLocale(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", bad, page.ID))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d for %q, want 400", rec.Code, bad)
			}
		}
	})
}

// Per-page CSS and JS are written into the public page raw, which is why
// the route is admin-only. The handler is the other half of the code
// drawer: what it reads back has to be what was saved.
func TestAPIPageCodeRoundTrip(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "api-code", Title: "Code"})

		rec := httptest.NewRecorder()
		s.apiSavePageCode(rec, jsonReq(t, s, u, http.MethodPut, "/api/x",
			`{"css":".a{color:red}","js":"console.log(1)"}`, page.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}

		rec = httptest.NewRecorder()
		s.apiGetPageCode(rec, jsonReq(t, s, u, http.MethodGet, "/api/x", "", page.ID))
		var out map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding %q: %v", rec.Body.String(), err)
		}
		if out["css"] != ".a{color:red}" || out["js"] != "console.log(1)" {
			t.Errorf("read back %v, want what was saved", out)
		}

		rec = httptest.NewRecorder()
		s.apiSavePageCode(rec, jsonReq(t, s, u, http.MethodPut, "/api/x", `{"css":`, page.ID))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d for unreadable JSON, want 400", rec.Code)
		}
	})
}

// Every one of these endpoints reads its page out of the URL, so a
// nonexistent id has to be a 404 rather than a 500 from a nil page.
func TestAPIWriteHandlersOnAMissingPage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		const missing = 999999

		for name, h := range map[string]http.HandlerFunc{
			"save sections":  s.apiSaveSections,
			"delete":         s.apiDeletePage,
			"unpublish":      s.apiUnpublish,
			"discard":        s.apiDiscard,
			"set visibility": s.apiSetVisibility,
			"duplicate":      s.apiDuplicatePage,
			"revert locale":  s.apiRevertLocale,
			"save code":      s.apiSavePageCode,
		} {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				h(rec, jsonReq(t, s, u, http.MethodPost, "/api/x", `{}`, missing))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
			})
		}
	})
}
