package admin

// The Pages section's write handlers, end to end against a real store.
// What these pin down is the part that was never executed: what a
// submission does to stored content, what a rejected one does not do, and
// which of the form's fields a given user is allowed to move.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
)

// pageForm is a complete, valid page submission — the baseline each test
// varies one field of.
func pageForm() url.Values {
	return url.Values{
		"title":         {"About Us"},
		"slug":          {"about-us"},
		"template_name": {"page.gohtml"},
		"visibility":    {"public"},
	}
}

func TestPageCreate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, pageForm(), nil)
		s.pageCreate(rec, r)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303 (body %q)", rec.Code, rec.Body.String())
		}
		loc := rec.Header().Get("Location")
		if !strings.HasPrefix(loc, "/admin/pages/") {
			t.Fatalf("Location = %q, want the new page's edit form", loc)
		}

		stored, err := s.deps.Content.GetBySlug(context.Background(), "about-us", "en", false)
		if err != nil {
			t.Fatalf("the page was not stored: %v", err)
		}
		if stored.Title != "About Us" {
			t.Errorf("title = %q, want %q", stored.Title, "About Us")
		}
		if stored.TemplateName != "page.gohtml" {
			t.Errorf("template = %q, want page.gohtml", stored.TemplateName)
		}
		// A new page is a draft: creating one must not put it on the
		// public site before anyone has looked at it.
		if stored.Status != content.StatusDraft {
			t.Errorf("status = %q, want draft", stored.Status)
		}
		if flashOf(s, r) == "" {
			t.Error("no flash was set for the redirect")
		}
	})
}

// A brand-new page on an all-sections template opens with something
// visibly editable rather than an empty void; a template that has its own
// placeholders (a text region here) is left alone.
func TestPageCreateSeedsStarterSectionsOnlyOnABlankCanvas(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		form := pageForm()
		form.Set("slug", "seeded-canvas")
		rec := httptest.NewRecorder()
		s.pageCreate(rec, formReq(t, s, u, form, nil))

		page, err := s.deps.Content.GetBySlug(context.Background(), "seeded-canvas", "en", false)
		if err != nil {
			t.Fatalf("the page was not stored: %v", err)
		}
		if got := draftHTML(t, s, page.ID, "main", "en"); !strings.Contains(got, "Write your text here") {
			t.Errorf("main region = %q, want the starter section", got)
		}

		form = pageForm()
		form.Set("slug", "plain-page")
		form.Set("template_name", "plain.gohtml")
		rec = httptest.NewRecorder()
		s.pageCreate(rec, formReq(t, s, u, form, nil))

		plain, err := s.deps.Content.GetBySlug(context.Background(), "plain-page", "en", false)
		if err != nil {
			t.Fatalf("the plain page was not stored: %v", err)
		}
		if got := draftHTML(t, s, plain.ID, "tagline", "en"); got != "" {
			t.Errorf("tagline region = %q, want nothing seeded on a template with its own placeholders", got)
		}
	})
}

// A rejected submission re-renders the form with the field marked and
// stores nothing — the failure mode that matters is a half-written page.
func TestPageCreateRejectsBadMetadata(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		cases := map[string]struct {
			mutate func(url.Values)
			marker string
		}{
			"no title":         {func(f url.Values) { f.Set("title", "  ") }, "Title is required"},
			"bad slug":         {func(f url.Values) { f.Set("slug", "Not A Slug!") }, "lowercase letters"},
			"unknown template": {func(f url.Values) { f.Set("template_name", "nope.gohtml") }, "Choose a template"},
			// A slug whose first segment is a locale code would be
			// shadowed by the language prefix and never reachable.
			"locale collision": {func(f url.Values) { f.Set("slug", "fr/about") }, "language code"},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				form := pageForm()
				form.Set("slug", "reject-"+content.Slugify(name))
				c.mutate(form)

				rec := httptest.NewRecorder()
				s.pageCreate(rec, formReq(t, s, u, form, nil))

				if rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d, want 422", rec.Code)
				}
				if !strings.Contains(rec.Body.String(), c.marker) {
					t.Errorf("the re-rendered form does not mention %q", c.marker)
				}
				if _, err := s.deps.Content.GetBySlug(context.Background(),
					form.Get("slug"), "en", false); err == nil {
					t.Error("a rejected submission was stored anyway")
				}
			})
		}
	})
}

func TestPageCreateDuplicateSlug(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		seedPage(t, s, &content.Page{Slug: "taken", Title: "Taken"})

		form := pageForm()
		form.Set("slug", "taken")
		rec := httptest.NewRecorder()
		s.pageCreate(rec, formReq(t, s, u, form, nil))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "already used") {
			t.Errorf("the form does not explain the collision: %q", rec.Body.String())
		}
	})
}

// The feed prefixes are the one namespace the pages form can reach into,
// and an editor who does not hold that feed must not be able to walk into
// it by typing an address.
func TestPageCreateFeedSlugsNeedTheFeedPermission(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		editor := formEditor(t, s, auth.PermPages)

		form := pageForm()
		form.Set("slug", "blog/sneaky")
		rec := httptest.NewRecorder()
		s.pageCreate(rec, formReq(t, s, editor, form, nil))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "reserved for blog and news") {
			t.Errorf("the form does not explain the refusal: %q", rec.Body.String())
		}

		// The same address is fine for someone who holds the feed.
		blogger := formUser(t, s, "form-blogger@example.com", auth.RoleEditor,
			auth.PermPages, auth.PermBlogs)
		rec = httptest.NewRecorder()
		s.pageCreate(rec, formReq(t, s, blogger, form, nil))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303 for a user holding blogs", rec.Code)
		}
	})
}

func TestPageUpdateSavesADraft(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "draft-me", Title: "Before",
			TemplateName: "plain.gohtml"})

		form := pageForm()
		form.Set("title", "After")
		form.Set("slug", "draft-me")
		form.Set("template_name", "plain.gohtml")
		form.Set("regions_template", "plain.gohtml")
		form.Set("region-body", "<p>fresh content</p>")
		form.Set("region-tagline", "A tagline")

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, form, idParams(page.ID))
		s.pageUpdate(rec, r)

		wantRedirect(t, rec, "/admin/pages/"+itoa(page.ID))
		if got := reloadPage(t, s, page.ID).Title; got != "After" {
			t.Errorf("title = %q, want After", got)
		}
		if got := draftHTML(t, s, page.ID, "body", "en"); !strings.Contains(got, "fresh content") {
			t.Errorf("body region = %q, want the submitted content", got)
		}
		if got := draftHTML(t, s, page.ID, "tagline", "en"); got != "A tagline" {
			t.Errorf("tagline region = %q, want the submitted text", got)
		}
		// Saving is not publishing: the page has to stay off the site
		// until someone says otherwise.
		if got := reloadPage(t, s, page.ID).Status; got != content.StatusDraft {
			t.Errorf("status = %q, want draft after a plain save", got)
		}
		if f := flashOf(s, r); !strings.Contains(f, "Draft saved") {
			t.Errorf("flash = %q, want the draft-saved message", f)
		}
	})
}

// "action=publish" is the other submit button on the same form: it saves
// and then puts the result on the site in one request.
func TestPageUpdatePublishAction(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "publish-me", Title: "Live",
			TemplateName: "plain.gohtml"})

		form := pageForm()
		form.Set("title", "Live")
		form.Set("slug", "publish-me")
		form.Set("template_name", "plain.gohtml")
		form.Set("action", "publish")
		form.Set("regions_template", "plain.gohtml")
		form.Set("region-body", "<p>going live</p>")

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, form, idParams(page.ID))
		s.pageUpdate(rec, r)

		wantRedirect(t, rec, "/admin/pages/"+itoa(page.ID))
		if got := reloadPage(t, s, page.ID).Status; got != content.StatusPublished {
			t.Errorf("status = %q, want published", got)
		}
		published, err := s.deps.Content.BlocksFor(context.Background(), page.ID, "en", content.StatusPublished)
		if err != nil {
			t.Fatalf("reading published blocks: %v", err)
		}
		var live strings.Builder
		for _, b := range published {
			live.WriteString(b.Content)
		}
		if !strings.Contains(live.String(), "going live") {
			t.Errorf("published content = %q, want the submitted content", live.String())
		}
		if f := flashOf(s, r); !strings.Contains(f, "published") {
			t.Errorf("flash = %q, want the published message", f)
		}
	})
}

// A translation tab edits only what is per-locale. The slug and template
// belong to the default tab, and a French save that carried them would
// move the page for every language at once.
func TestPageUpdateInAnotherLocaleLeavesTheSlugAlone(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "locale-page", Title: "English title",
			TemplateName: "plain.gohtml"})

		form := url.Values{
			"title":            {"Titre français"},
			"description":      {"Description française"},
			"slug":             {"adresse-francaise"},
			"template_name":    {"page.gohtml"},
			"regions_template": {"plain.gohtml"},
			"region-body":      {"<p>contenu</p>"},
		}
		rec := httptest.NewRecorder()
		r := formReqTo(t, s, u, http.MethodPost, "/admin/x?locale=fr", form, idParams(page.ID))
		s.pageUpdate(rec, r)

		wantRedirect(t, rec, "/admin/pages/"+itoa(page.ID)+"?locale=fr")

		after := reloadPage(t, s, page.ID)
		if after.Slug != "locale-page" {
			t.Errorf("slug = %q, want it untouched by a French save", after.Slug)
		}
		if after.TemplateName != "plain.gohtml" {
			t.Errorf("template = %q, want it untouched by a French save", after.TemplateName)
		}
		if after.Title != "English title" {
			t.Errorf("default-locale title = %q, want it untouched", after.Title)
		}

		fr, err := s.deps.Content.MetaFor(context.Background(), page.ID, "fr")
		if err != nil {
			t.Fatalf("reading the French metadata: %v", err)
		}
		if fr.Title != "Titre français" {
			t.Errorf("French title = %q, want the submitted one", fr.Title)
		}
		if got := draftHTML(t, s, page.ID, "body", "fr"); !strings.Contains(got, "contenu") {
			t.Errorf("French body region = %q, want the submitted content", got)
		}
		// And the English content is still English.
		if got := draftHTML(t, s, page.ID, "body", "en"); strings.Contains(got, "contenu") {
			t.Errorf("English body region = %q, want the French save kept out of it", got)
		}
	})
}

// A translation still needs a title: an empty one would leave the page
// with a blank <title> in that language rather than falling back.
func TestPageUpdateInAnotherLocaleNeedsATitle(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "needs-title", Title: "English"})

		rec := httptest.NewRecorder()
		r := formReqTo(t, s, u, http.MethodPost, "/admin/x?locale=fr",
			url.Values{"title": {"   "}}, idParams(page.ID))
		s.pageUpdate(rec, r)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422", rec.Code)
		}
	})
}

// Per-page CSS and JS go into the public page raw, so they are admin-only.
// An editor's save has to leave whatever is stored exactly as it was —
// not clear it, which is what a form that never showed the fields would
// otherwise do.
func TestPageUpdateCodeFieldsAreAdminOnly(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		admin := formAdmin(t, s)
		editor := formEditor(t, s)
		page := seedPage(t, s, &content.Page{Slug: "code-page", Title: "Code"})

		form := pageForm()
		form.Set("title", "Code")
		form.Set("slug", "code-page")
		form.Set("head_css", ".a{color:red}")
		form.Set("body_js", "console.log(1)")

		rec := httptest.NewRecorder()
		s.pageUpdate(rec, formReq(t, s, admin, form, idParams(page.ID)))
		if got := reloadPage(t, s, page.ID).HeadCSS; got != ".a{color:red}" {
			t.Fatalf("admin head_css = %q, want it saved", got)
		}

		// Now the editor posts the same form with different code in it.
		form.Set("head_css", ".evil{}")
		form.Set("body_js", "steal()")
		rec = httptest.NewRecorder()
		s.pageUpdate(rec, formReq(t, s, editor, form, idParams(page.ID)))

		after := reloadPage(t, s, page.ID)
		if after.HeadCSS != ".a{color:red}" {
			t.Errorf("head_css = %q, want the admin's value to survive an editor's save", after.HeadCSS)
		}
		if after.BodyJS != "console.log(1)" {
			t.Errorf("body_js = %q, want the admin's value to survive an editor's save", after.BodyJS)
		}
	})
}

// A page that backs a post is managed under Blog & News. Reaching it
// through the pages routes would be a side door past the feed
// permissions, so it is a 404 here.
func TestPageHandlersRefusePostBackingPages(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		post := seedPost(t, s, content.FeedBlog, "A post", "a-post")

		for name, h := range map[string]http.HandlerFunc{
			"update":    s.pageUpdate,
			"delete":    s.pageDelete,
			"discard":   s.pageDiscard,
			"unpublish": s.pageUnpublish,
		} {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				h(rec, formReq(t, s, u, pageForm(), idParams(post.ID)))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
			})
		}
	})
}

func TestPageDelete(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "delete-me", Title: "Doomed"})

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, nil, idParams(page.ID))
		s.pageDelete(rec, r)

		wantRedirect(t, rec, "/admin/pages")
		if _, err := s.deps.Content.GetByID(context.Background(), page.ID, "en"); err == nil {
			t.Error("the page is still there")
		}
		if f := flashOf(s, r); !strings.Contains(f, "deleted") {
			t.Errorf("flash = %q, want the deleted message", f)
		}
	})
}

// A site must always answer at "/", so the home page is not deletable —
// and saying so beats a 500 from a foreign key somewhere downstream.
func TestPageDeleteRefusesTheHomePage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		home := seedPage(t, s, &content.Page{Slug: "", Title: "Home"})

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, nil, idParams(home.ID))
		s.pageDelete(rec, r)

		wantRedirect(t, rec, "/admin/pages/"+itoa(home.ID))
		if _, err := s.deps.Content.GetByID(context.Background(), home.ID, "en"); err != nil {
			t.Errorf("the home page was deleted anyway: %v", err)
		}
		if f := flashOf(s, r); !strings.Contains(f, "can't be deleted") {
			t.Errorf("flash = %q, want the refusal", f)
		}
	})
}

// Discarding reverts the draft to whatever is live, so it only means
// something once there is something live to revert to.
func TestPageDiscard(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()
		page := seedPage(t, s, &content.Page{Slug: "discard-me", Title: "Kept"})

		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>published</p>"); err != nil {
			t.Fatalf("seeding published content: %v", err)
		}
		if err := s.deps.Content.Publish(ctx, page.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>unsaved rewrite</p>"); err != nil {
			t.Fatalf("seeding draft content: %v", err)
		}

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, nil, idParams(page.ID))
		s.pageDiscard(rec, r)

		wantRedirect(t, rec, "/admin/pages/"+itoa(page.ID))
		if got := draftHTML(t, s, page.ID, "main", "en"); !strings.Contains(got, "published") {
			t.Errorf("draft = %q, want it back to the published content", got)
		}
		if got := draftHTML(t, s, page.ID, "main", "en"); strings.Contains(got, "unsaved rewrite") {
			t.Errorf("draft = %q, want the discarded edit gone", got)
		}
	})
}

func TestPageDiscardRefusesAnUnpublishedPage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "never-live", Title: "Draft only"})

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, nil, idParams(page.ID))
		s.pageDiscard(rec, r)

		wantRedirect(t, rec, "/admin/pages/"+itoa(page.ID))
		if f := flashOf(s, r); !strings.Contains(f, "hasn't been published") {
			t.Errorf("flash = %q, want the explanation", f)
		}
	})
}

// Unpublishing takes the page off the site without touching content, so
// publishing again brings back exactly what was there.
func TestPageUnpublish(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()
		page := seedPage(t, s, &content.Page{Slug: "unpublish-me", Title: "Live"})
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>live words</p>"); err != nil {
			t.Fatalf("seeding content: %v", err)
		}
		if err := s.deps.Content.Publish(ctx, page.ID); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, nil, idParams(page.ID))
		s.pageUnpublish(rec, r)

		wantRedirect(t, rec, "/admin/pages/"+itoa(page.ID))
		if got := reloadPage(t, s, page.ID).Status; got == content.StatusPublished {
			t.Error("the page is still published")
		}
		// The draft survived, which is what makes this reversible.
		if got := draftHTML(t, s, page.ID, "main", "en"); !strings.Contains(got, "live words") {
			t.Errorf("draft = %q, want the content untouched", got)
		}
	})
}

func TestPageNewRendersAnEmptyForm(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		s.pageNew(rec, formReqTo(t, s, u, http.MethodGet, "/admin/pages/new", nil, nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `name="slug"`) || !strings.Contains(body, `name="template_name"`) {
			t.Error("the new-page form is missing its fields")
		}
	})
}

// The preview runs the real site templates over the draft, which is the
// whole reason it exists: what an editor checks before publishing has to
// be the page, not an approximation of it.
func TestPagePreviewRendersTheDraft(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()
		page := seedPage(t, s, &content.Page{Slug: "preview-me", Title: "Preview"})
		if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "main", "en",
			content.KindHTML, "<p>not yet live</p>"); err != nil {
			t.Fatalf("seeding draft content: %v", err)
		}

		rec := httptest.NewRecorder()
		s.pagePreview(rec, formReqTo(t, s, u, http.MethodGet, "/admin/pages/x/preview", nil, idParams(page.ID)))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "not yet live") {
			t.Errorf("the preview does not show the unpublished content: %q", rec.Body.String())
		}
	})
}

// Sections save through the editor's own endpoint, one block per section.
// The page form displays the same region name, so a form save that
// upserted it as a single block would collapse the whole section list
// into one — losing every section but the first and its settings with it.
func TestPageUpdateLeavesSectionsRegionsAlone(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()
		page := seedPage(t, s, &content.Page{Slug: "sectioned", Title: "Sectioned"})

		sections := []content.SectionInput{
			{Content: "<p>first</p>", Settings: map[string]string{"bg": "white"}},
			{Content: "<p>second</p>", Settings: map[string]string{"bg": "white"}},
		}
		if err := s.deps.Content.ReplaceDraftSections(ctx, page.ID, "main", "en", sections); err != nil {
			t.Fatalf("seeding sections: %v", err)
		}

		form := pageForm()
		form.Set("title", "Sectioned")
		form.Set("slug", "sectioned")
		form.Set("regions_template", "page.gohtml")
		form.Set("region-main", "<p>a form field pretending to be the region</p>")

		rec := httptest.NewRecorder()
		s.pageUpdate(rec, formReq(t, s, u, form, idParams(page.ID)))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}

		blocks, err := s.deps.Content.BlocksFor(ctx, page.ID, "en", content.StatusDraft)
		if err != nil {
			t.Fatalf("reading blocks: %v", err)
		}
		var inMain int
		for _, b := range blocks {
			if b.Region == "main" {
				inMain++
			}
		}
		if inMain != 2 {
			t.Errorf("main holds %d blocks, want the 2 sections untouched", inMain)
		}
		if got := draftHTML(t, s, page.ID, "main", "en"); strings.Contains(got, "pretending") {
			t.Errorf("main region = %q, want the form's value ignored", got)
		}
	})
}

// Rich-text regions carry whatever the editor's contenteditable produced,
// which for a non-admin is untrusted markup. An admin's HTML is stored as
// typed — writing raw markup is what the role is for.
func TestPageUpdateSanitizesRegionHTMLForNonAdmins(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		editor := formEditor(t, s)
		admin := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "sanitize-me", Title: "Sanitize",
			TemplateName: "plain.gohtml"})

		const hostile = `<p onclick="steal()">hi</p><script>alert(1)</script>`
		form := pageForm()
		form.Set("title", "Sanitize")
		form.Set("slug", "sanitize-me")
		form.Set("template_name", "plain.gohtml")
		form.Set("regions_template", "plain.gohtml")
		form.Set("region-body", hostile)

		rec := httptest.NewRecorder()
		s.pageUpdate(rec, formReq(t, s, editor, form, idParams(page.ID)))

		got := draftHTML(t, s, page.ID, "body", "en")
		if strings.Contains(got, "<script") || strings.Contains(got, "onclick") {
			t.Errorf("an editor's HTML was stored unsanitized: %q", got)
		}
		if !strings.Contains(got, "hi") {
			t.Errorf("sanitizing took the content with it: %q", got)
		}

		rec = httptest.NewRecorder()
		s.pageUpdate(rec, formReq(t, s, admin, form, idParams(page.ID)))
		if got := draftHTML(t, s, page.ID, "body", "en"); !strings.Contains(got, "<script>") {
			t.Errorf("an admin's markup = %q, want it stored as typed", got)
		}
	})
}

// A template the renderer does not know is not a template whose regions
// can be trusted to name anything, so the whole region save is skipped
// rather than half-applied.
func TestPageUpdateIgnoresAnUnknownRegionsTemplate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s := formServer(t, db)
		u := formAdmin(t, s)
		page := seedPage(t, s, &content.Page{Slug: "unknown-regions", Title: "Unknown",
			TemplateName: "plain.gohtml"})

		form := pageForm()
		form.Set("title", "Unknown")
		form.Set("slug", "unknown-regions")
		form.Set("template_name", "plain.gohtml")
		form.Set("regions_template", "not-a-template.gohtml")
		form.Set("region-body", "<p>should not land</p>")

		rec := httptest.NewRecorder()
		s.pageUpdate(rec, formReq(t, s, u, form, idParams(page.ID)))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
		if got := draftHTML(t, s, page.ID, "body", "en"); got != "" {
			t.Errorf("body region = %q, want nothing stored", got)
		}
	})
}
