package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
)

// The forms' image <select> lists the newest images only, and keeps the
// one already chosen whatever its age: an option missing for it would
// leave the browser selecting "no image", and saving the form would
// clear the choice — which is what a region holding an image from
// outside the library got even before the list was capped.
func TestFormImageListsKeepTheCurrentChoice(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		was := formImageChoices
		formImageChoices = 2
		defer func() { formImageChoices = was }()

		ctx := context.Background()
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		var ups []*media.Media
		for i := range 3 {
			ups = append(ups, uploadPNG(t, s, u, fmt.Sprintf("img%d.png", i)))
		}
		oldest := s.deps.Media.Views([]media.Media{*ups[0]})[0]

		// A page template with an image region, for the page form.
		r, err := render.New(fstest.MapFS{"pictured.gohtml": &fstest.MapFile{
			Data: []byte(`{{define "pictured.gohtml"}}<img src="{{cmsImage "hero"}}">{{end}}`)}},
			nil, []render.PageTemplate{{File: "pictured.gohtml", Label: "Pictured"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		s.deps.Renderer = r
		page := seedPage(t, s, &content.Page{Slug: "pictured", Title: "Pictured", TemplateName: "pictured.gohtml"})
		edit := func() string {
			t.Helper()
			rec := httptest.NewRecorder()
			s.pageEdit(rec, formReqTo(t, s, u, http.MethodGet, "/admin/pages/x", nil, idParams(page.ID)))
			if rec.Code != http.StatusOK {
				t.Fatalf("page form: status %d", rec.Code)
			}
			return rec.Body.String()
		}

		for _, current := range []string{oldest.WebURL, "https://elsewhere.example/photo.jpg"} {
			if err := s.deps.Content.UpsertDraftBlock(ctx, page.ID, "hero", "en", content.KindImage, current); err != nil {
				t.Fatal(err)
			}
			form := edit()
			if want := `<option value="` + current + `" selected>(current image)</option>`; !strings.Contains(form, want) {
				t.Errorf("current image %q not kept as the selected option:\n%s", current, form)
			}
			if n := strings.Count(form, "(8×4)</option>"); n != 2 {
				t.Errorf("the select lists %d library images, want the newest 2", n)
			}
			if !strings.Contains(form, "Only the newest images are listed here") {
				t.Error("no hint that the list is not the whole library")
			}
		}

		// The post form keeps a library thumbnail by id.
		post := &content.Post{Feed: content.FeedBlog, ThumbnailMediaID: &ups[0].ID}
		post.Title, post.Slug, post.TemplateName = "With thumb", "blog/with-thumb", s.deps.PostTemplate.File
		if _, err := s.deps.Content.InsertPost(ctx, post, "en"); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		s.postEdit(rec, formReqTo(t, s, u, http.MethodGet, "/admin/posts/x", nil, idParams(post.PostID)))
		form := rec.Body.String()
		if want := `<option value="` + strconv.FormatInt(ups[0].ID, 10) + `" selected>`; !strings.Contains(form, want) {
			t.Errorf("the post's older thumbnail is not a selected option (status %d)", rec.Code)
		}
	})
}
