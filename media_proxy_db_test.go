package cms

// The media proxy: /cms/media/... streams an uploaded object out of a
// bucket the browser may have no access to at all. It is the one public
// route that hands out bytes rather than markup, and everything about it
// — the range handling video needs, the headers an SVG needs, the
// rebuild that covers a rendition an old upload never had — is there for
// a specific failure it prevents.

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
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/internal/dbtest"
	"github.com/tsawler/cms/internal/sqldb"
	"github.com/tsawler/cms/media"
)

// proxyStore is an in-memory ObjectStore with no range support, which is
// the shape of a host's own simple store — proxied images and documents
// work, video cannot seek. failOn makes one key return a hard error, for
// the path that is neither "missing" nor "fine".
type proxyStore struct {
	mu      sync.Mutex
	root    string // the manager's key root, which a proxied URL leaves out
	objects map[string][]byte
	types   map[string]string
	failOn  string
	failErr error
}

// setRoot is called once the manager exists, since a proxied URL is
// relative to the key root and the proxy re-adds it — that is what keeps a
// deployment prefix out of page URLs.
func (s *proxyStore) setRoot(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.root = root
}

func newProxyStore() *proxyStore {
	return &proxyStore{objects: map[string][]byte{}, types: map[string]string{}}
}

func (s *proxyStore) Put(_ context.Context, key, contentType string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = data
	s.types[key] = contentType
	return nil
}

func (s *proxyStore) Get(_ context.Context, key string) (io.ReadCloser, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == s.failOn {
		return nil, "", s.failErr
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, "", media.ErrObjectNotFound
	}
	ct := s.types[key]
	if ct == "" {
		ct = "application/octet-stream"
	}
	return io.NopCloser(bytes.NewReader(data)), ct, nil
}

func (s *proxyStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *proxyStore) PublicURL(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return media.ProxyPathPrefix + strings.TrimPrefix(key, s.root)
}

func (s *proxyStore) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[key]
	return ok
}

// rangingStore is proxyStore plus RangeGetter — an S3-like store, where
// proxied video seeks and Safari can play it at all.
type rangingStore struct{ *proxyStore }

func (s *rangingStore) GetRange(_ context.Context, key, spec string) (io.ReadCloser, string, string, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, "", "", 0, media.ErrObjectNotFound
	}
	// Only the one form the proxy has to get right is parsed here:
	// "bytes=<start>-<end>", with an open end. Anything else is treated
	// the way a backend that ignored the range would be.
	spec, ok = strings.CutPrefix(spec, "bytes=")
	if !ok {
		return io.NopCloser(bytes.NewReader(data)), s.types[key], "", int64(len(data)), nil
	}
	startStr, endStr, _ := strings.Cut(spec, "-")
	start, err := strconv.Atoi(startStr)
	if err != nil || start >= len(data) {
		return nil, "", "", 0, media.ErrInvalidRange
	}
	end := len(data) - 1
	if endStr != "" {
		if end, err = strconv.Atoi(endStr); err != nil || end < start {
			return nil, "", "", 0, media.ErrInvalidRange
		}
		end = min(end, len(data)-1)
	}
	part := data[start : end+1]
	contentRange := "bytes " + strconv.Itoa(start) + "-" + strconv.Itoa(end) + "/" + strconv.Itoa(len(data))
	return io.NopCloser(bytes.NewReader(part)), s.types[key], contentRange, int64(len(part)), nil
}

var proxyTestFS = fstest.MapFS{
	"templates/base.gohtml": &fstest.MapFile{Data: []byte(
		`{{define "base"}}<html><body>{{block "content" .}}{{end}}</body></html>{{end}}`)},
	"templates/pages/standard.gohtml": &fstest.MapFile{Data: []byte(
		`{{template "base" .}}{{define "content"}}{{cmsRegion "main"}}{{end}}`)},
}

// newProxyTestCMS builds a CMS whose media lives in the given store, and
// returns it ready to serve.
// A nil store gives a CMS with no media at all, which is how a host that
// configured none is set up.
func newProxyTestCMS(t *testing.T, db *sqldb.DB, store media.ObjectStore) *CMS {
	t.Helper()
	cfg := Config{
		DB:              db.SQL(),
		Dialect:         db.Dialect().Name(),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		TemplateFS:      proxyTestFS,
		SharedTemplates: []string{"templates/base.gohtml"},
		PageTemplates: []PageTemplate{
			{File: "templates/pages/standard.gohtml", Label: "Standard page"},
		},
	}
	if store != nil {
		cfg.ObjectStore = store
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("cms.New: %v", err)
	}
	if err := c.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if rs, ok := store.(interface{ setRoot(string) }); ok {
		rs.setRoot(c.media.KeyRoot())
	}

	return c
}

// proxyPNG is a real PNG: the manager decodes what it is given, so an
// upload has to be an image the standard library can read.
func proxyPNG(t *testing.T) []byte {
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

// proxyKey is the bucket key behind a proxied URL: the proxy re-adds the
// key root the URL leaves out.
func proxyKey(c *CMS, url string) string {
	return c.media.KeyRoot() + strings.TrimPrefix(url, media.ProxyPathPrefix)
}

// proxyGet drives a request through the real Handler, so the route that
// claims the media prefix is exercised alongside the handler behind it.
func proxyGet(t *testing.T, c *CMS, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, r)
	return rec
}

func TestServeMediaStreamsAnObject(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		store := newProxyStore()
		c := newProxyTestCMS(t, db, store)

		md, err := c.media.Upload(context.Background(), "kraken.png", proxyPNG(t), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		url := c.media.URL(md, "original")
		if !strings.HasPrefix(url, media.ProxyPathPrefix) {
			t.Fatalf("public URL = %q, want it proxied", url)
		}

		rec := proxyGet(t, c, http.MethodGet, url, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Body.Bytes(); !bytes.Equal(got, proxyPNG(t)) {
			t.Errorf("body is %d bytes, want the %d uploaded", len(got), len(proxyPNG(t)))
		}
		// Every upload gets a fresh key, so an object at a given key never
		// changes and can be cached for as long as a browser likes.
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("Cache-Control = %q, want immutable", cc)
		}
		// Bytes from a bucket, served on the site's own origin: the
		// browser must not be left to guess what they are.
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff",
				rec.Header().Get("X-Content-Type-Options"))
		}
		if ct := rec.Header().Get("Content-Type"); ct == "" {
			t.Error("no Content-Type")
		}
	})
}

// A HEAD is how a browser or a CDN checks an object without pulling it,
// so it has to answer with the headers and nothing else.
func TestServeMediaHead(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		store := newProxyStore()
		c := newProxyTestCMS(t, db, store)
		md, err := c.media.Upload(context.Background(), "kraken.png", proxyPNG(t), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}

		rec := proxyGet(t, c, http.MethodHead, c.media.URL(md, "original"), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("HEAD returned %d bytes of body", rec.Body.Len())
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("Cache-Control = %q, want the same headers a GET sets", cc)
		}
	})
}

// The key is built from the request path, so the path is the one thing
// here that an attacker chooses. It may not climb out of the media root.
func TestServeMediaRefusesPathsOutsideTheMediaRoot(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newProxyTestCMS(t, db, newProxyStore())

		for _, path := range []string{
			media.ProxyPathPrefix,
			media.ProxyPathPrefix + "../secrets.env",
			media.ProxyPathPrefix + "abc/../../secrets.env",
			media.ProxyPathPrefix + "..%2Fsecrets.env",
		} {
			rec := proxyGet(t, c, http.MethodGet, path, nil)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: status = %d, want 404", path, rec.Code)
			}
		}
	})
}

func TestServeMediaMissingObject(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newProxyTestCMS(t, db, newProxyStore())

		rec := proxyGet(t, c, http.MethodGet, media.ProxyPathPrefix+"nothing/here.png", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})
}

// A store failure that is not "missing" is the site's problem, not the
// visitor's: a 500 rather than a 404 that would tell a crawler the file
// is gone for good.
func TestServeMediaStoreFailureIsAServerError(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		store := newProxyStore()
		c := newProxyTestCMS(t, db, store)
		md, err := c.media.Upload(context.Background(), "kraken.png", proxyPNG(t), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		url := c.media.URL(md, "original")

		store.mu.Lock()
		store.failOn = proxyKey(c, url)
		store.failErr = context.DeadlineExceeded
		store.mu.Unlock()

		rec := proxyGet(t, c, http.MethodGet, url, nil)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})
}

// Viewed directly rather than inside an <img>, an SVG is a document on
// this origin — so it is served under a policy that allows it to draw and
// nothing else, whatever the upload-time scan may have missed.
func TestServeMediaLocksDownSVG(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		store := newProxyStore()
		c := newProxyTestCMS(t, db, store)

		svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8">` +
			`<rect width="8" height="8" fill="red"/></svg>`)
		md, err := c.media.Upload(context.Background(), "logo.svg", svg, 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}

		rec := proxyGet(t, c, http.MethodGet, c.media.URL(md, "original"), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("CSP = %q, want scripting blocked", csp)
		}
		// Inline styles stay allowed, or the graphic does not fully draw.
		if !strings.Contains(csp, "style-src 'unsafe-inline'") {
			t.Errorf("CSP = %q, want inline styles still allowed", csp)
		}
	})
}

// A store without range support is not broken, it is just range-less: it
// must not advertise ranges, and a Range header a browser sends anyway
// gets the whole object with a 200 rather than a malformed 206.
func TestServeMediaWithoutRangeSupport(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		store := newProxyStore()
		c := newProxyTestCMS(t, db, store)
		md, err := c.media.Upload(context.Background(), "kraken.png", proxyPNG(t), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		url := c.media.URL(md, "original")

		rec := proxyGet(t, c, http.MethodGet, url, nil)
		if ar := rec.Header().Get("Accept-Ranges"); ar != "" {
			t.Errorf("Accept-Ranges = %q, want nothing advertised", ar)
		}

		rec = proxyGet(t, c, http.MethodGet, url, map[string]string{"Range": "bytes=0-3"})
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 — the range is simply not honored", rec.Code)
		}
		if rec.Body.Len() != len(proxyPNG(t)) {
			t.Errorf("body is %d bytes, want the whole object", rec.Body.Len())
		}
	})
}

// With a store that can range, the proxy is what makes proxied video
// seekable — and playable at all in Safari, which probes with a Range
// request before it will start.
func TestServeMediaRangeRequests(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		store := &rangingStore{newProxyStore()}
		c := newProxyTestCMS(t, db, store)
		full := proxyPNG(t)
		md, err := c.media.Upload(context.Background(), "kraken.png", full, 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		url := c.media.URL(md, "original")

		rec := proxyGet(t, c, http.MethodGet, url, nil)
		if ar := rec.Header().Get("Accept-Ranges"); ar != "bytes" {
			t.Errorf("Accept-Ranges = %q, want bytes", ar)
		}

		rec = proxyGet(t, c, http.MethodGet, url, map[string]string{"Range": "bytes=0-3"})
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("status = %d, want 206", rec.Code)
		}
		if got := rec.Body.Bytes(); !bytes.Equal(got, full[:4]) {
			t.Errorf("body = %v, want the first four bytes %v", got, full[:4])
		}
		if cr := rec.Header().Get("Content-Range"); cr != "bytes 0-3/"+strconv.Itoa(len(full)) {
			t.Errorf("Content-Range = %q, want the range and the total size", cr)
		}
		// A player needs the length of what it just got, not of the file.
		if cl := rec.Header().Get("Content-Length"); cl != "4" {
			t.Errorf("Content-Length = %q, want 4", cl)
		}

		// Asking past the end is the client's error, and 416 is what tells
		// it so — a 200 with the whole file would look like success.
		rec = proxyGet(t, c, http.MethodGet, url,
			map[string]string{"Range": "bytes=" + strconv.Itoa(len(full)+100) + "-"})
		if rec.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Errorf("status = %d, want 416", rec.Code)
		}
	})
}

// The rung an old upload never had. The rendition ladder gains sizes over
// time, and an image uploaded before one exists has no object at that
// key — so the first request for it builds it, and every later request is
// an ordinary hit.
func TestServeMediaRebuildsAMissingRendition(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		store := newProxyStore()
		c := newProxyTestCMS(t, db, store)
		ctx := context.Background()
		md, err := c.media.Upload(ctx, "kraken.png", proxyPNG(t), 0, nil)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}

		url := c.media.URL(md, "thumb")
		key := proxyKey(c, url)
		if !store.has(key) {
			t.Fatalf("the upload stored no thumb at %q", key)
		}
		// Stand in for an image that predates the rung: the record is
		// there, the object is not.
		if err := store.Delete(ctx, key); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		rec := proxyGet(t, c, http.MethodGet, url, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — the rendition should have been rebuilt", rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Error("the rebuilt rendition served no bytes")
		}
		if !store.has(key) {
			t.Error("the rebuilt rendition was not written back to the store")
		}

		// The second request is a plain hit, not another rebuild.
		rec = proxyGet(t, c, http.MethodGet, url, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d on the second request, want 200", rec.Code)
		}
	})
}

// A key that names no media record at all has nothing to rebuild from, so
// it stays a 404 rather than becoming a server error.
func TestServeMediaDoesNotRebuildWhatItCannotIdentify(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newProxyTestCMS(t, db, newProxyStore())

		for _, rest := range []string{"unknown-item/thumb.webp", "single-segment.png", "abc/not-a-rung.webp"} {
			rec := proxyGet(t, c, http.MethodGet, media.ProxyPathPrefix+rest, nil)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: status = %d, want 404", rest, rec.Code)
			}
		}
	})
}

// Without an object store the route belongs to the host application: the
// CMS claims an address only when it has something to answer with.
func TestMediaProxyIsUnclaimedWithoutAnObjectStore(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, db *sqldb.DB) {
		c := newProxyTestCMS(t, db, nil)
		if c.objects != nil {
			t.Fatal("a CMS with no ObjectStore has one anyway")
		}

		// The path falls through to the ordinary page lookup, which is
		// what leaves the address to the host application: nothing here
		// answers it as media.
		rec := proxyGet(t, c, http.MethodGet, media.ProxyPathPrefix+"anything.png", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want the page handler's 404", rec.Code)
		}
	})
}
