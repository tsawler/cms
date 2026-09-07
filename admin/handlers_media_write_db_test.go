package admin

// The media library's write handlers. The existing media tests check the
// markup the library renders and the permissions its routes sit behind;
// none of them ran the handlers that change what is in the library —
// upload, rename, describe, move, delete, and the folders those live in.

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/media"

	"github.com/tsawler/cms/auth"
)

// mediaServerWithStore is a server whose media library is backed by
// memory, so the handlers run through the real manager — which writes
// several objects per image — without an S3 bucket.
func mediaServerWithStore(t *testing.T, db *sqldb.DB) (*server, *downloadStore) {
	t.Helper()
	s := formServer(t, db)
	store := withMedia(s, db)
	return s, store
}

// uploadReq builds the multipart POST the upload form sends.
func uploadReq(t *testing.T, s *server, u *auth.User, filename string, data []byte,
	fields map[string]string) *http.Request {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("writing field %s: %v", k, err)
		}
	}
	if filename != "" {
		part, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("creating the file part: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("writing the file part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing the multipart writer: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/admin/media/upload", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return withRouteAndSession(t, s, u, r, nil)
}

// uploadPNG puts one image in the library through the real handler and
// returns it, for the tests that are about what happens to an item after
// it is there.
func uploadPNG(t *testing.T, s *server, u *auth.User, filename string) *media.Media {
	t.Helper()
	rec := httptest.NewRecorder()
	s.mediaUpload(rec, uploadReq(t, s, u, filename, smallPNG(t), nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload status = %d, want 303 (body %q)", rec.Code, rec.Body.String())
	}
	items, err := s.deps.Media.All(context.Background(), "en", media.ListOptions{})
	if err != nil {
		t.Fatalf("listing media: %v", err)
	}
	for i := range items {
		if items[i].Filename == filename {
			return &items[i]
		}
	}
	t.Fatalf("uploaded %q but it is not in the library", filename)
	return nil
}

func TestMediaUpload(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, store := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		r := uploadReq(t, s, u, "sea kraken.png", smallPNG(t), map[string]string{"alt": "A kraken"})
		s.mediaUpload(rec, r)

		// The redirect lands on the tab that shows what was just
		// uploaded, rather than wherever the form happened to be.
		wantRedirect(t, rec, "/admin/media?tab=images")
		if f := flashOf(s, r); !strings.Contains(f, "Image uploaded") {
			t.Errorf("flash = %q, want the image-uploaded message", f)
		}

		items, err := s.deps.Media.All(context.Background(), "en", media.ListOptions{})
		if err != nil {
			t.Fatalf("listing media: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("library holds %d items, want 1", len(items))
		}
		md := items[0]
		if md.Kind != media.KindImage {
			t.Errorf("kind = %q, want image", md.Kind)
		}
		if md.Alt != "A kraken" {
			t.Errorf("alt = %q, want the submitted description", md.Alt)
		}
		// The manager writes the original plus its renditions; what
		// matters here is that the handler's upload actually reached the
		// object store rather than only the database.
		if len(store.objects) == 0 {
			t.Error("nothing was written to the object store")
		}
	})
}

// A filename is client-supplied. Only its last element is kept, so a
// crafted path cannot reach outside the key the manager chose.
func TestMediaUploadSanitizesTheFilename(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		s.mediaUpload(rec, uploadReq(t, s, u, `..\..\..\etc\passwd.png`, smallPNG(t), nil))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303 (body %q)", rec.Code, rec.Body.String())
		}

		items, err := s.deps.Media.All(context.Background(), "en", media.ListOptions{})
		if err != nil {
			t.Fatalf("listing media: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("library holds %d items, want 1", len(items))
		}
		if strings.ContainsAny(items[0].Filename, `/\`) {
			t.Errorf("filename = %q, want the path stripped", items[0].Filename)
		}
	})
}

// The two ways an upload arrives with nothing usable in it: no file part
// at all, and bytes that are not a file type the library accepts. Both
// re-render the library with the reason on it rather than redirecting to
// a page that would not explain anything.
func TestMediaUploadRejections(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		rec := httptest.NewRecorder()
		s.mediaUpload(rec, uploadReq(t, s, u, "", nil, nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d for a missing file, want 422", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Choose a file") {
			t.Errorf("body does not ask for a file: %q", rec.Body.String())
		}

		rec = httptest.NewRecorder()
		s.mediaUpload(rec, uploadReq(t, s, u, "notes.xyz", []byte("just some bytes"), nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d for an unsupported type, want 422", rec.Code)
		}

		items, err := s.deps.Media.All(context.Background(), "en", media.ListOptions{})
		if err != nil {
			t.Fatalf("listing media: %v", err)
		}
		if len(items) != 0 {
			t.Errorf("library holds %d items, want a rejected upload stored nowhere", len(items))
		}
	})
}

func TestMediaRenameAndDescribe(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		md := uploadPNG(t, s, u, "before.png")

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, url.Values{"name": {"after.png"}}, idParams(md.ID))
		s.mediaRename(rec, r)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("rename status = %d, want 303", rec.Code)
		}

		after, err := s.deps.Media.GetByID(context.Background(), md.ID, "en")
		if err != nil {
			t.Fatalf("reloading: %v", err)
		}
		if after.Filename != "after.png" {
			t.Errorf("filename = %q, want after.png", after.Filename)
		}
		// Renaming moves nothing in the bucket, so every link that
		// already points at the file keeps working.
		if after.StoreKey != md.StoreKey {
			t.Errorf("store key moved from %q to %q", md.StoreKey, after.StoreKey)
		}

		rec = httptest.NewRecorder()
		s.mediaUpdateAlt(rec, formReq(t, s, u, url.Values{"alt": {"  A description  "}}, idParams(md.ID)))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("alt status = %d, want 303", rec.Code)
		}
		after, err = s.deps.Media.GetByID(context.Background(), md.ID, "en")
		if err != nil {
			t.Fatalf("reloading: %v", err)
		}
		if after.Alt != "A description" {
			t.Errorf("alt = %q, want it trimmed and saved", after.Alt)
		}
	})
}

// A name the media package will not take comes back as a message rather
// than a 500 — and the file keeps the name it had.
func TestMediaRenameRefusesABadName(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		md := uploadPNG(t, s, u, "keeper.png")

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, url.Values{"name": {""}}, idParams(md.ID))
		s.mediaRename(rec, r)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
		if f := flashOf(s, r); !strings.Contains(f, "between 1 and 200") {
			t.Errorf("flash = %q, want the explanation", f)
		}
		after, err := s.deps.Media.GetByID(context.Background(), md.ID, "en")
		if err != nil {
			t.Fatalf("reloading: %v", err)
		}
		if after.Filename != "keeper.png" {
			t.Errorf("filename = %q, want it unchanged", after.Filename)
		}
	})
}

// Every id-taking media handler reads the id off the URL, so a
// nonexistent one has to be a 404.
func TestMediaHandlersOnMissingItems(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)

		for name, h := range map[string]http.HandlerFunc{
			"rename": s.mediaRename,
			"delete": s.mediaDelete,
		} {
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				s := s
				h(rec, formReq(t, s, u, url.Values{"name": {"x.png"}}, idParams(999999)))
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
			})
		}
	})
}

// The listing sends its actions back to where they came from, so the tab
// and filters survive the round trip; a request with no Referer (a direct
// post, a privacy proxy) falls back to the library's default view.
func TestMediaActionsReturnToTheReferringView(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		md := uploadPNG(t, s, u, "returner.png")

		r := formReq(t, s, u, url.Values{"alt": {"x"}}, idParams(md.ID))
		r.Header.Set("Referer", "/admin/media?tab=images&folder=3")
		rec := httptest.NewRecorder()
		s.mediaUpdateAlt(rec, r)
		wantRedirect(t, rec, "/admin/media?tab=images&folder=3")

		rec = httptest.NewRecorder()
		s.mediaUpdateAlt(rec, formReq(t, s, u, url.Values{"alt": {"x"}}, idParams(md.ID)))
		wantRedirect(t, rec, "/admin/media")
	})
}

func TestMediaDelete(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		md := uploadPNG(t, s, u, "doomed.png")

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, nil, idParams(md.ID))
		s.mediaDelete(rec, r)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", rec.Code)
		}
		if _, err := s.deps.Media.GetByID(context.Background(), md.ID, "en"); err == nil {
			t.Error("the item is still in the library")
		}
		if f := flashOf(s, r); !strings.Contains(f, "deleted") {
			t.Errorf("flash = %q, want the deleted message", f)
		}
	})
}

func TestMediaFoldersAndMoving(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, url.Values{"name": {"Krakens"}, "tab": {"images"}}, nil)
		s.mediaFolderCreate(rec, r)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("folder create status = %d, want 303 (body %q)", rec.Code, rec.Body.String())
		}
		if f := flashOf(s, r); !strings.Contains(f, "Folder created") {
			t.Errorf("flash = %q, want the created message", f)
		}

		folders, err := s.deps.Media.Folders(ctx)
		if err != nil {
			t.Fatalf("listing folders: %v", err)
		}
		if len(folders) != 1 {
			t.Fatalf("%d folders, want 1", len(folders))
		}
		folder := folders[0]
		if folder.Kind != media.KindImage {
			t.Errorf("folder kind = %q, want the tab it was created on", folder.Kind)
		}

		// A second folder of the same name on the same tab is refused,
		// with the library re-rendered and the reason on it.
		rec = httptest.NewRecorder()
		s.mediaFolderCreate(rec, formReq(t, s, u, url.Values{"name": {"Krakens"}, "tab": {"images"}}, nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("duplicate folder status = %d, want 422", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "already exists") {
			t.Errorf("body = %q, want it to say the name is taken", rec.Body.String())
		}

		md := uploadPNG(t, s, u, "filed.png")
		rec = httptest.NewRecorder()
		s.mediaMove(rec, formReq(t, s, u, url.Values{"folder": {itoa(folder.ID)}}, idParams(md.ID)))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("move status = %d, want 303", rec.Code)
		}
		after, err := s.deps.Media.GetByID(ctx, md.ID, "en")
		if err != nil {
			t.Fatalf("reloading: %v", err)
		}
		if after.FolderID == nil || *after.FolderID != folder.ID {
			t.Errorf("folder = %v, want %d", after.FolderID, folder.ID)
		}

		// A folder with something in it is not deletable — the template
		// disables the button, so reaching here means a stale page.
		rec = httptest.NewRecorder()
		r = formReq(t, s, u, url.Values{"tab": {"images"}}, idParams(folder.ID))
		s.mediaFolderDelete(rec, r)
		wantRedirect(t, rec, "/admin/media")
		if f := flashOf(s, r); !strings.Contains(f, "isn't empty") {
			t.Errorf("flash = %q, want the not-empty explanation", f)
		}

		// Empty it — "" is unfiled — and the folder goes.
		rec = httptest.NewRecorder()
		s.mediaMove(rec, formReq(t, s, u, url.Values{"folder": {""}}, idParams(md.ID)))
		rec = httptest.NewRecorder()
		r = formReq(t, s, u, url.Values{"tab": {"images"}}, idParams(folder.ID))
		s.mediaFolderDelete(rec, r)
		// The Referer would point into the folder that just vanished, so
		// the redirect goes to the tab's root instead.
		wantRedirect(t, rec, "/admin/media?tab=images")
		if f := flashOf(s, r); !strings.Contains(f, "Folder deleted") {
			t.Errorf("flash = %q, want the deleted message", f)
		}
	})
}

// The listing lets several rows be ticked at once, so its actions act on
// the selection. An unparseable id is dropped rather than failing the
// whole action, and an id that is already gone is not an error: two tabs
// open on the same folder can both submit it.
func TestMediaBulkActions(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		ctx := context.Background()

		folder, err := s.deps.Media.CreateFolder(ctx, "Bulk", media.KindImage)
		if err != nil {
			t.Fatalf("creating a folder: %v", err)
		}
		a := uploadPNG(t, s, u, "bulk-a.png")
		b := uploadPNG(t, s, u, "bulk-b.png")

		form := url.Values{
			"id":     {itoa(a.ID), itoa(b.ID), "not-a-number", "-1"},
			"folder": {itoa(folder.ID)},
		}
		rec := httptest.NewRecorder()
		r := formReq(t, s, u, form, nil)
		s.mediaBulkMove(rec, r)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("bulk move status = %d, want 303", rec.Code)
		}
		if f := flashOf(s, r); !strings.Contains(f, "2 files moved") {
			t.Errorf("flash = %q, want a count of the two real ids", f)
		}
		for _, id := range []int64{a.ID, b.ID} {
			md, err := s.deps.Media.GetByID(ctx, id, "en")
			if err != nil {
				t.Fatalf("reloading %d: %v", id, err)
			}
			if md.FolderID == nil || *md.FolderID != folder.ID {
				t.Errorf("item %d folder = %v, want %d", id, md.FolderID, folder.ID)
			}
		}

		// Delete both, plus an id that no longer exists.
		rec = httptest.NewRecorder()
		r = formReq(t, s, u, url.Values{"id": {itoa(a.ID), itoa(b.ID), "999999"}}, nil)
		s.mediaBulkDelete(rec, r)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("bulk delete status = %d, want 303", rec.Code)
		}
		if f := flashOf(s, r); !strings.Contains(f, "2 files deleted") {
			t.Errorf("flash = %q, want only the rows that were really there counted", f)
		}
		items, err := s.deps.Media.All(ctx, "en", media.ListOptions{})
		if err != nil {
			t.Fatalf("listing media: %v", err)
		}
		if len(items) != 0 {
			t.Errorf("library holds %d items, want them all deleted", len(items))
		}
	})
}

// One selected row reads as one, not as "1 files" — and an empty
// selection says nothing at all rather than "0 files moved".
func TestMediaBulkActionCounts(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		md := uploadPNG(t, s, u, "single.png")

		rec := httptest.NewRecorder()
		r := formReq(t, s, u, url.Values{"id": {itoa(md.ID)}, "folder": {""}}, nil)
		s.mediaBulkMove(rec, r)
		if f := flashOf(s, r); f != "1 file moved." {
			t.Errorf("flash = %q, want the singular", f)
		}

		rec = httptest.NewRecorder()
		r = formReq(t, s, u, url.Values{}, nil)
		s.mediaBulkMove(rec, r)
		if f := flashOf(s, r); f != "" {
			t.Errorf("flash = %q, want nothing said about an empty selection", f)
		}
	})
}

func TestParseFolderParam(t *testing.T) {
	if id, unfiled := parseFolderParam("root"); id != nil || !unfiled {
		t.Errorf(`parseFolderParam("root") = %v, %v; want nil, true`, id, unfiled)
	}
	if id, unfiled := parseFolderParam("7"); id == nil || *id != 7 || unfiled {
		t.Errorf(`parseFolderParam("7") = %v, %v; want 7, false`, id, unfiled)
	}
	// "" is no filter at all, which is a third thing: neither a folder
	// nor the unfiled root.
	for _, v := range []string{"", "0", "-3", "abc"} {
		if id, unfiled := parseFolderParam(v); id != nil || unfiled {
			t.Errorf("parseFolderParam(%q) = %v, %v; want nil, false", v, id, unfiled)
		}
	}
}

func TestMediaTabAndKind(t *testing.T) {
	cases := map[string]struct {
		tab  string
		kind media.Kind
	}{
		"documents": {"documents", media.KindFile},
		"videos":    {"videos", media.KindVideo},
		"images":    {"images", media.KindImage},
		// Anything unknown lands on images rather than on an empty view.
		"":         {"images", media.KindImage},
		"nonsense": {"images", media.KindImage},
	}
	for in, want := range cases {
		if got := mediaTab(in); got != want.tab {
			t.Errorf("mediaTab(%q) = %q, want %q", in, got, want.tab)
		}
		if got := kindForTab(mediaTab(in)); got != want.kind {
			t.Errorf("kindForTab(mediaTab(%q)) = %q, want %q", in, got, want.kind)
		}
	}
}

// A folder link carried over from another tab names a folder this tab
// does not have, so the view falls back to the tab's root rather than
// showing a folder that belongs to a different kind of file.
func TestMediaListFallsBackForAFolderFromAnotherTab(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		docs, err := s.deps.Media.CreateFolder(context.Background(), "Papers", media.KindFile)
		if err != nil {
			t.Fatalf("creating a folder: %v", err)
		}
		uploadPNG(t, s, u, "unfiled.png")

		rec := httptest.NewRecorder()
		r := formReqTo(t, s, u, http.MethodGet,
			"/admin/media?tab=images&folder="+itoa(docs.ID), nil, nil)
		s.mediaList(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		// The unfiled image is what the root shows, so seeing it is how
		// we know the stale folder link did not take us somewhere else.
		if !strings.Contains(rec.Body.String(), "unfiled.png") {
			t.Errorf("the images root does not list its own unfiled item: %q", rec.Body.String())
		}
	})
}

// posterReq builds the multipart POST the media inspector sends when it
// has captured a frame from a video the server could not decode.
func posterReq(t *testing.T, s *server, u *auth.User, poster []byte, id int64) *http.Request {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("poster", "poster.png")
	if err != nil {
		t.Fatalf("creating the poster part: %v", err)
	}
	if _, err := part.Write(poster); err != nil {
		t.Fatalf("writing the poster part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing the multipart writer: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/api/media/x/poster", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return withRouteAndSession(t, s, u, r, idParams(id))
}
