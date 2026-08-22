package admin

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
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
