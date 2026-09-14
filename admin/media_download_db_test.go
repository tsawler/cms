package admin

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/media"
)

// downloadStore is an in-memory ObjectStore, so the download runs through
// the real media manager without an S3 bucket. Its PublicURL is absolute
// on purpose: that is the shape that makes this route necessary, since a
// browser will not save a cross-origin link.
type downloadStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (s *downloadStore) Put(_ context.Context, key, _ string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	return nil
}

func (s *downloadStore) Get(_ context.Context, key string) (io.ReadCloser, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, "", media.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), "image/png", nil
}

func (s *downloadStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *downloadStore) PublicURL(key string) string {
	return "https://bucket.example.com/" + key
}

// smallPNG is a tiny real PNG: the manager decodes what it is given, so
// the upload has to be an image the standard library can read.
func smallPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for x := range 8 {
		img.Set(x, 1, color.RGBA{R: 200, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding a test PNG: %v", err)
	}
	return buf.Bytes()
}

func downloadRequest(id int64) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/admin/media/x/download", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(id, 10))
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// A download has to arrive as the file itself — the bytes that were
// uploaded, named the way they were uploaded, and marked for saving
// rather than showing. The public URL cannot promise any of that from a
// bucket, which is why the route exists.
func TestMediaDownloadServesTheOriginalAsAnAttachment(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		store := &downloadStore{objects: map[string][]byte{}}
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		s := &server{deps: Deps{
			Media:         media.NewManager(db, store, logger),
			DefaultLocale: "en",
			Logger:        logger,
		}}

		png := smallPNG(t)
		md, err := s.deps.Media.Upload(context.Background(), "sea kraken.png", png, 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}

		rec := httptest.NewRecorder()
		s.mediaDownload(rec, downloadRequest(md.ID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got, want := rec.Header().Get("Content-Disposition"),
			`attachment; filename="sea kraken.png"; filename*=UTF-8''sea%20kraken.png`; got != want {
			t.Errorf("Content-Disposition = %q, want %q", got, want)
		}
		if got := rec.Body.Bytes(); !bytes.Equal(got, png) {
			t.Errorf("body is %d bytes, want the %d uploaded — a rendition, not the original?",
				len(got), len(png))
		}

		// An id nobody uploaded is a 404, not a 500.
		rec = httptest.NewRecorder()
		s.mediaDownload(rec, downloadRequest(md.ID+10_000))
		if rec.Code != http.StatusNotFound {
			t.Errorf("downloading a missing item: status = %d, want 404", rec.Code)
		}
	})
}

// Several selected files come down as one zip holding each original
// under its own name, with a duplicate name told apart rather than
// overwritten, an id nobody uploaded left out, and a selection with
// nothing real in it refused before any archive is started.
func TestMediaBulkDownloadZipsTheOriginals(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		s, _ := mediaServerWithStore(t, db)
		u := formAdmin(t, s)
		png := smallPNG(t)

		a := uploadPNG(t, s, u, "zip-a.png")
		b := uploadPNG(t, s, u, "zip-b.png")
		// Same name as a: the archive must still hold both.
		rec := httptest.NewRecorder()
		s.mediaUpload(rec, uploadReq(t, s, u, "zip-a.png", png, nil))
		var c *media.Media
		items, err := s.deps.Media.All(context.Background(), "en", media.ListOptions{})
		if err != nil {
			t.Fatalf("listing media: %v", err)
		}
		for i := range items {
			if items[i].ID != a.ID && items[i].ID != b.ID {
				c = &items[i]
			}
		}
		if c == nil {
			t.Fatal("the duplicate-named upload is not in the library")
		}

		rec = httptest.NewRecorder()
		r := formReq(t, s, u, url.Values{"id": {itoa(a.ID), itoa(b.ID), itoa(c.ID), "999999"}}, nil)
		s.mediaBulkDownload(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
			t.Errorf("Content-Type = %q, want application/zip", ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="media-`) ||
			!strings.Contains(cd, `.zip"`) {
			t.Errorf("Content-Disposition = %q, want an attachment named media-<stamp>.zip", cd)
		}

		body := rec.Body.Bytes()
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("the body is not a zip: %v", err)
		}
		got := map[string][]byte{}
		for _, f := range zr.File {
			if f.Method != zip.Store {
				t.Errorf("%s is compressed with method %d, want stored", f.Name, f.Method)
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("opening %s: %v", f.Name, err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatalf("reading %s: %v", f.Name, err)
			}
			got[f.Name] = data
		}
		for _, name := range []string{"zip-a.png", "zip-b.png", "zip-a (2).png"} {
			if data, ok := got[name]; !ok {
				t.Errorf("zip is missing %q (has %v)", name, names(zr))
			} else if !bytes.Equal(data, png) {
				t.Errorf("%s is %d bytes, want the %d uploaded", name, len(data), len(png))
			}
		}
		if len(zr.File) != 3 {
			t.Errorf("zip holds %d entries, want 3: %v", len(zr.File), names(zr))
		}

		// Nothing real selected: a 404, not an empty archive.
		rec = httptest.NewRecorder()
		s.mediaBulkDownload(rec, formReq(t, s, u, url.Values{"id": {"999999"}}, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("downloading only missing ids: status = %d, want 404", rec.Code)
		}
	})
}

func names(zr *zip.Reader) []string {
	out := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func TestUniqueZipName(t *testing.T) {
	taken := map[string]bool{}
	want := []string{"a.png", "a (2).png", "A (3).png", "b", "b (2)", "a (2) (2).png"}
	for i, in := range []string{"a.png", "a.png", "A.png", "b", "b", "a (2).png"} {
		if got := uniqueZipName(taken, in); got != want[i] {
			t.Errorf("uniqueZipName(%q) #%d = %q, want %q", in, i, got, want[i])
		}
	}
}
