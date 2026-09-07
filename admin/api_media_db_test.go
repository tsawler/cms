package admin

// The media endpoints the in-place editor talks to: the picker's listing,
// the upload it accepts by drag-and-drop, its folders, and the poster
// frame the browser captures for a video the server cannot decode. These
// are the JSON siblings of the media page's form handlers and had never
// been executed either.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/media"
)

// decodeAPI reads a handler's JSON response, failing the test with the
// body when it is not JSON at all — which is what a 500 looks like.
func decodeAPI(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestAPIMediaListAndFolders(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()

		images, err := s.deps.Media.CreateFolder(ctx, "Pictures", media.KindImage)
		if err != nil {
			t.Fatalf("creating an image folder: %v", err)
		}
		if _, err := s.deps.Media.CreateFolder(ctx, "Papers", media.KindFile); err != nil {
			t.Fatalf("creating a document folder: %v", err)
		}
		filed := uploadPNG(t, s, u, "filed.png")
		uploadPNG(t, s, u, "loose.png")
		if err := s.deps.Media.Move(ctx, filed.ID, &images.ID); err != nil {
			t.Fatalf("filing an image: %v", err)
		}

		rec := httptest.NewRecorder()
		s.apiMediaList(rec, formReqTo(t, s, u, http.MethodGet, "/api/media?kind=image", nil, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if items, ok := decodeAPI(t, rec)["media"].([]any); !ok || len(items) != 2 {
			t.Errorf("listing returned %v, want both images", decodeAPI(t, rec)["media"])
		}

		// "root" is the unfiled view, which is a different thing from no
		// filter at all: it is the picker's own starting directory.
		rec = httptest.NewRecorder()
		s.apiMediaList(rec, formReqTo(t, s, u, http.MethodGet, "/api/media?kind=image&folder=root", nil, nil))
		items, _ := decodeAPI(t, rec)["media"].([]any)
		if len(items) != 1 {
			t.Errorf("the unfiled view returned %d items, want 1", len(items))
		}

		// A kind the library does not have is a bad request rather than
		// an empty listing, so a typo in the picker is visible.
		rec = httptest.NewRecorder()
		s.apiMediaList(rec, formReqTo(t, s, u, http.MethodGet, "/api/media?kind=hologram", nil, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d for an unknown kind, want 400", rec.Code)
		}

		// The picker asks for the folders of the kind it is browsing, so
		// the document folder must not come back for images.
		rec = httptest.NewRecorder()
		s.apiFoldersList(rec, formReqTo(t, s, u, http.MethodGet, "/api/media/folders?kind=image", nil, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		folders, _ := decodeAPI(t, rec)["folders"].([]any)
		if len(folders) != 1 {
			t.Fatalf("%d folders for images, want 1", len(folders))
		}
		f, _ := folders[0].(map[string]any)
		if f["name"] != "Pictures" {
			t.Errorf("folder = %v, want the image folder", f)
		}
		if f["count"] != float64(1) {
			t.Errorf("count = %v, want the one filed image", f["count"])
		}

		// No kind at all lists every folder.
		rec = httptest.NewRecorder()
		s.apiFoldersList(rec, formReqTo(t, s, u, http.MethodGet, "/api/media/folders", nil, nil))
		if folders, _ := decodeAPI(t, rec)["folders"].([]any); len(folders) != 2 {
			t.Errorf("%d folders unfiltered, want 2", len(folders))
		}
	})
}

func TestAPIMediaUpload(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		s.apiMediaUpload(rec, uploadReq(t, s, u, "dropped.png", smallPNG(t), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}

		out := decodeAPI(t, rec)
		if out["ok"] != true {
			t.Errorf("ok = %v, want true", out["ok"])
		}
		// The editor drops the returned record straight into the slot it
		// was dragged onto, so the response has to carry a usable one.
		md, _ := out["media"].(map[string]any)
		if md["kind"] != "image" {
			t.Errorf("media = %v, want an image record", md)
		}
		// The editor picks a rendition by name, so the record has to
		// carry the rungs rather than one bare address.
		for _, rung := range []string{"original", "web", "card", "thumb"} {
			if u, _ := md[rung].(string); u == "" {
				t.Errorf("media = %v, want a %s URL the editor can use", md, rung)
			}
		}

		items, err := s.deps.Media.All(context.Background(), "en", media.ListOptions{})
		if err != nil {
			t.Fatalf("listing media: %v", err)
		}
		if len(items) != 1 || items[0].Filename != "dropped.png" {
			t.Errorf("library = %v, want the upload stored", items)
		}
	})
}

// The JSON endpoint's refusals have to be JSON too: the editor shows what
// comes back, and an HTML error page would reach the user as nothing at
// all.
func TestAPIMediaUploadRejectionsAreJSON(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		s.apiMediaUpload(rec, uploadReq(t, s, u, "", nil, nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d for a missing file, want 422", rec.Code)
		}
		if out := decodeAPI(t, rec); out["error"] == nil {
			t.Errorf("body = %v, want an error message", out)
		}

		rec = httptest.NewRecorder()
		s.apiMediaUpload(rec, uploadReq(t, s, u, "notes.xyz", []byte("just some bytes"), nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d for an unsupported type, want 422", rec.Code)
		}
		if out := decodeAPI(t, rec); out["error"] == nil {
			t.Errorf("body = %v, want an error message", out)
		}
	})
}

// An upload dropped while the picker is inside a folder lands in it.
func TestAPIMediaUploadFilesIntoAFolder(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()
		folder, err := s.deps.Media.CreateFolder(ctx, "Dropped", media.KindImage)
		if err != nil {
			t.Fatalf("creating a folder: %v", err)
		}

		rec := httptest.NewRecorder()
		s.apiMediaUpload(rec, uploadReq(t, s, u, "into-folder.png", smallPNG(t),
			map[string]string{"folder": itoa(folder.ID)}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if out := decodeAPI(t, rec); out["filed"] != true {
			t.Errorf("filed = %v, want true", out["filed"])
		}

		items, err := s.deps.Media.All(ctx, "en", media.ListOptions{FolderID: &folder.ID})
		if err != nil {
			t.Fatalf("listing the folder: %v", err)
		}
		if len(items) != 1 {
			t.Errorf("the folder holds %d items, want the upload", len(items))
		}
	})
}

// A folder is created in the kind the picker is browsing, and the two
// refusals — a name already taken and a kind that is not a media kind —
// come back as messages rather than failures.
func TestAPIFolderCreate(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		s.apiFolderCreate(rec, jsonReq(t, s, u, http.MethodPost, "/api/media/folders",
			`{"name":"From the picker","kind":"image"}`, 0))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		folder, _ := decodeAPI(t, rec)["folder"].(map[string]any)
		if folder["name"] != "From the picker" {
			t.Errorf("folder = %v, want the submitted name", folder)
		}

		cases := map[string]struct {
			body   string
			status int
		}{
			"duplicate name":  {`{"name":"From the picker","kind":"image"}`, http.StatusUnprocessableEntity},
			"empty name":      {`{"name":"","kind":"image"}`, http.StatusUnprocessableEntity},
			"unknown kind":    {`{"name":"Elsewhere","kind":"hologram"}`, http.StatusUnprocessableEntity},
			"unreadable JSON": {`{"name":`, http.StatusBadRequest},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				s.apiFolderCreate(rec, jsonReq(t, s, u, http.MethodPost, "/api/media/folders", c.body, 0))
				if rec.Code != c.status {
					t.Errorf("status = %d, want %d (body %q)", rec.Code, c.status, rec.Body.String())
				}
				if out := decodeAPI(t, rec); out["error"] == nil {
					t.Errorf("body = %v, want an error message", out)
				}
			})
		}
	})
}

// The server cannot decode video, so a poster frame is captured in the
// browser and posted here. A poster for something that is not there, or
// with no file part, has to come back as JSON like everything else.
func TestAPIMediaSetPosterRefusals(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		r := uploadReq(t, s, u, "", nil, nil)
		r = withRouteAndSession(t, s, u, r, idParams(999999))
		s.apiMediaSetPoster(rec, r)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d for a missing poster part, want 422", rec.Code)
		}
		if out := decodeAPI(t, rec); out["error"] == nil {
			t.Errorf("body = %v, want an error message", out)
		}

		// A poster part that is present, for an id that is not.
		rec = httptest.NewRecorder()
		r = posterReq(t, s, u, smallPNG(t), 999999)
		s.apiMediaSetPoster(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d for a missing item, want 404 (body %q)", rec.Code, rec.Body.String())
		}
	})
}

// The post form's image picker stores either a library id or a bare URL,
// never both, and an id naming something that is not a library image is
// treated as no image rather than failing the save — it can only come
// from a tampered form or a picture deleted while the form was open.
func TestParsePostImage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		img := uploadPNG(t, s, u, "thumbnail.png")

		type want struct {
			id  *int64
			url string
		}
		cases := map[string]struct {
			form map[string]string
			want want
		}{
			"a library image": {
				map[string]string{"thumbnail_media_id": itoa(img.ID)},
				want{id: &img.ID},
			},
			"nothing chosen": {
				map[string]string{"thumbnail_media_id": ""},
				want{},
			},
			"keeping an image the library does not hold": {
				map[string]string{"thumbnail_media_id": "keep", "thumbnail_url": "/uploads/legacy.jpg"},
				want{url: "/uploads/legacy.jpg"},
			},
			"keeping an unusable URL": {
				map[string]string{"thumbnail_media_id": "keep", "thumbnail_url": "javascript:alert(1)"},
				want{},
			},
			"an id that is not a number": {
				map[string]string{"thumbnail_media_id": "seven"},
				want{},
			},
			"an id nothing answers to": {
				map[string]string{"thumbnail_media_id": "999999"},
				want{},
			},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				form := postFormValues()
				for k, v := range c.form {
					form.Set(k, v)
				}
				id, url := s.parsePostImage(formReq(t, s, u, form, nil), "thumbnail")

				switch {
				case c.want.id == nil && id != nil:
					t.Errorf("id = %d, want none", *id)
				case c.want.id != nil && id == nil:
					t.Errorf("id = nil, want %d", *c.want.id)
				case c.want.id != nil && *id != *c.want.id:
					t.Errorf("id = %d, want %d", *id, *c.want.id)
				}
				if url != c.want.url {
					t.Errorf("url = %q, want %q", url, c.want.url)
				}
			})
		}
	})
}

// A post created with a picture opens with a banner: the picture as the
// section's background, the title over it, and a text colour chosen from
// how dark the picture is.
func TestPostCreateSeedsABannerFromItsImage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		img := uploadPNG(t, s, u, "banner.png")

		form := postFormValues()
		form.Set("title", "Bannered")
		form.Set("slug", "bannered")
		form.Set("thumbnail_media_id", itoa(img.ID))

		rec := httptest.NewRecorder()
		s.postCreate(rec, formReq(t, s, u, form, nil))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303 (body %q)", rec.Code, rec.Body.String())
		}

		page, err := s.deps.Content.GetBySlug(context.Background(), "blog/bannered", "en", false)
		if err != nil {
			t.Fatalf("the post was not stored: %v", err)
		}
		header := sectionsOf(t, s, page.ID, "header", "en")
		if len(header) != 1 {
			t.Fatalf("the header region holds %d sections, want the seeded banner", len(header))
		}
		if header[0].Settings["bgimage"] == "" {
			t.Errorf("banner settings = %v, want the picture as its background", header[0].Settings)
		}
		if header[0].Settings["height"] == "" {
			t.Errorf("banner settings = %v, want a height so it reads as a banner", header[0].Settings)
		}
		// The title starts on top of it, as the page's <h1>.
		if !strings.Contains(header[0].Content, "Bannered") {
			t.Errorf("banner content = %q, want the post's title", header[0].Content)
		}
		if !strings.Contains(header[0].Content, "<h1") {
			t.Errorf("banner content = %q, want it to be the page heading", header[0].Content)
		}
	})
}

// A post created with no picture has no banner, and the region's own
// "Add section" button is how one gets added later.
func TestPostCreateWithoutAnImageHasNoBanner(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		form := postFormValues()
		form.Set("title", "Bare")
		form.Set("slug", "bare")

		rec := httptest.NewRecorder()
		s.postCreate(rec, formReq(t, s, u, form, nil))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303 (body %q)", rec.Code, rec.Body.String())
		}

		page, err := s.deps.Content.GetBySlug(context.Background(), "blog/bare", "en", false)
		if err != nil {
			t.Fatalf("the post was not stored: %v", err)
		}
		if n := len(sectionsOf(t, s, page.ID, "header", "en")); n != 0 {
			t.Errorf("the header region holds %d sections, want none", n)
		}
		// The content region is still seeded, so the post is not empty.
		if got := draftHTML(t, s, page.ID, "main", "en"); !strings.Contains(got, "Write your text here") {
			t.Errorf("main region = %q, want the starter section", got)
		}
	})
}

// Light and dark pictures get different title colours, which is the whole
// reason the banner measures the image at all.
func TestBannerSnippetHTMLColoursTheTitleForItsBackground(t *testing.T) {
	light := bannerSnippetHTML(bannerSeed{Title: "Over a pale photo"})
	dark := bannerSnippetHTML(bannerSeed{Title: "Over a night sky", Dark: true})

	if !strings.Contains(dark, "#ffffff") {
		t.Errorf("dark banner = %q, want light text", dark)
	}
	if strings.Contains(light, "#ffffff") {
		t.Errorf("light banner = %q, want dark text", light)
	}
	// A title is user input on its way into markup.
	hostile := bannerSnippetHTML(bannerSeed{Title: `<script>alert(1)</script>`})
	if strings.Contains(hostile, "<script>") {
		t.Errorf("banner = %q, want the title escaped", hostile)
	}
	// Both are still the page's heading, and both are snippet blocks so
	// the editor gives them the block chrome.
	for _, out := range []string{light, dark} {
		if !strings.HasPrefix(out, `<h1 class="cms-snippet"`) {
			t.Errorf("banner = %q, want an h1 snippet block", out)
		}
	}
}

// Ensure the post preview still renders once a media library is wired:
// postImages is what resolves a post's library images for it.
func TestPostPreviewWithAMediaLibrary(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		post := seedPost(t, s, content.FeedBlog, "With media", "with-media")
		if err := s.deps.Content.UpsertDraftBlock(context.Background(), post.ID, "main", "en",
			content.KindHTML, "<p>previewed</p>"); err != nil {
			t.Fatalf("seeding draft content: %v", err)
		}

		rec := httptest.NewRecorder()
		s.postPreview(rec, formReqTo(t, s, u, http.MethodGet, "/admin/posts/x/preview",
			nil, idParams(post.PostID)))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "previewed") {
			t.Errorf("the preview does not show the draft: %q", rec.Body.String())
		}
	})
}

// The picture beside the image picker. On a form re-rendered after a
// validation error the post was parsed from the submission, so it carries
// the id without the joined record — the record is fetched then, and the
// picture the editor chose is still on screen next to the message telling
// them what to fix.
func TestPostFormShowsTheChosenImage(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		img := uploadPNG(t, s, u, "chosen.png")

		form := postFormValues()
		form.Set("title", "") // rejected, so the form comes back
		form.Set("thumbnail_media_id", itoa(img.ID))

		rec := httptest.NewRecorder()
		s.postCreate(rec, formReq(t, s, u, form, nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		// The thumbnail rendition, since the preview is a small square.
		if !strings.Contains(rec.Body.String(), "/thumb.webp") {
			t.Errorf("the re-rendered form lost the chosen picture: %q", rec.Body.String())
		}
	})
}

// An image the library does not hold has no rendition to show, so its own
// address is what the preview uses.
func TestPostFormShowsAnImageFromOutsideTheLibrary(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		form := postFormValues()
		form.Set("title", "")
		form.Set("thumbnail_media_id", "keep")
		form.Set("thumbnail_url", "/uploads/legacy.jpg")

		rec := httptest.NewRecorder()
		s.postCreate(rec, formReq(t, s, u, form, nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "/uploads/legacy.jpg") {
			t.Errorf("the re-rendered form lost the external picture: %q", rec.Body.String())
		}
	})
}
