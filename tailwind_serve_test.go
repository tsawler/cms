package cms

// The serving half of the content-stylesheet pipeline: the cache a
// rebuild publishes into, the URL derived from the bytes in it, and the
// two routes that hand it to a browser. None of it needs a database — a
// freshly-set cache is served straight out of memory, which is the whole
// point of it — so these run in the fast lane.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/render"
)

// cssTestCMS is a CMS with just the parts the cache and the two routes
// touch: a renderer whose {{cmsHead}} link follows the cache, and a
// logger. There is no database, which is deliberate — a test that reached
// one here would be testing something other than the in-memory path.
func cssTestCMS(t *testing.T) (*CMS, *cssRebuilder) {
	t.Helper()
	fsys := fstest.MapFS{
		"base.gohtml": &fstest.MapFile{Data: []byte(
			`{{define "base"}}<html><head>{{cmsHead}}</head><body>` +
				`{{block "content" .}}{{end}}</body></html>{{end}}`)},
		"page.gohtml": &fstest.MapFile{Data: []byte(
			`{{template "base" .}}{{define "content"}}{{cmsRegion "main"}}{{end}}`)},
	}
	r, err := render.New(fsys, []string{"base.gohtml"},
		[]render.PageTemplate{{File: "page.gohtml", Label: "Page"}}, nil)
	if err != nil {
		t.Fatalf("render.New: %v", err)
	}
	c := &CMS{
		renderer: r,
		cfg:      Config{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
	}
	c.cssBuilder = &cssRebuilder{c: c}
	return c, c.cssBuilder
}

// headLink is the stylesheet {{cmsHead}} actually emits — the link a
// browser gets, which is the thing the cache exists to keep current. The
// renderer's own accessor is unexported, and asserting on the markup is
// closer to the question anyway.
func headLink(t *testing.T, c *CMS) string {
	t.Helper()
	var buf strings.Builder
	err := c.renderer.Render(&buf, render.Input{
		Page:   &content.Page{Slug: "css-probe", TemplateName: "page.gohtml", Title: "Probe"},
		Locale: "en",
	})
	if err != nil {
		t.Fatalf("rendering the probe page: %v", err)
	}
	m := contentCSSLinkRe.FindStringSubmatch(buf.String())
	if m == nil {
		return ""
	}
	return m[1]
}

// contentCSSLinkRe picks the generated stylesheet's link out of a rendered
// <head>; it is the only one pointing under the content-CSS prefix.
var contentCSSLinkRe = regexp.MustCompile(
	`<link rel="stylesheet" href="(` + regexp.QuoteMeta(contentCSSPrefix) + `[^"]*)">`)

// The URL a stylesheet is served under is the hash of its own bytes, not
// of the build that produced them. That is what makes "immutable" true:
// the address moves exactly when the file does — including for a Tailwind
// upgrade that recompiles identical inputs into different CSS, which a
// build-keyed URL would serve forever out of every browser cache.
func TestSetCacheAddressesTheBytes(t *testing.T) {
	c, b := cssTestCMS(t)

	const css = ".cms-a{color:red}"
	b.setCache(css)

	hash, got := b.current(t.Context())
	if got != css {
		t.Errorf("css = %q, want %q", got, css)
	}
	if hash != cssHash(css) {
		t.Errorf("hash = %q, want the hash of the bytes %q", hash, cssHash(css))
	}
	if want := contentCSSPrefix + hash + ".css"; headLink(t, c) != want {
		t.Errorf("head link = %q, want %q", headLink(t, c), want)
	}

	// Different bytes, different address.
	b.setCache(".cms-a{color:blue}")
	if second, _ := b.current(t.Context()); second == hash {
		t.Error("different stylesheets were given the same address")
	}

	// The same bytes, the same address — on any instance, with no clock
	// in it, which is what lets a multi-instance deployment agree.
	_, ob := cssTestCMS(t)
	ob.setCache(css)
	if h, _ := ob.current(t.Context()); h != hash {
		t.Errorf("a second instance addressed identical CSS as %q, want %q", h, hash)
	}
}

// A build that produced nothing leaves no link at all, rather than one
// pointing at an empty file.
func TestSetCacheEmptyClearsTheLink(t *testing.T) {
	c, b := cssTestCMS(t)

	b.setCache(".cms-a{color:red}")
	if headLink(t, c) == "" {
		t.Fatal("no link after a stylesheet was published")
	}

	b.setCache("")
	if href := headLink(t, c); href != "" {
		t.Errorf("head link = %q, want it cleared", href)
	}
}

// A fresh cache is served without touching the database — this CMS has
// none, so a call that reached for one would panic rather than pass.
func TestCurrentServesTheFreshCacheWithoutADatabase(t *testing.T) {
	_, b := cssTestCMS(t)
	b.setCache(".cms-a{color:red}")

	for range 3 {
		if _, css := b.current(t.Context()); css != ".cms-a{color:red}" {
			t.Fatalf("css = %q, want the cached copy", css)
		}
	}
}

func TestServeContentCSS(t *testing.T) {
	c, b := cssTestCMS(t)
	const css = ".cms-a{color:red}"
	b.setCache(css)
	hash := cssHash(css)

	t.Run("the current hash is immutable", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c.serveContentCSS(rec, httptest.NewRequest(http.MethodGet, contentCSSPrefix+hash+".css", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/css; charset=utf-8" {
			t.Errorf("Content-Type = %q, want CSS", ct)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("Cache-Control = %q, want immutable", cc)
		}
		if rec.Body.String() != css {
			t.Errorf("body = %q, want the stylesheet", rec.Body.String())
		}
	})

	// A page cached before the last rebuild still names the old hash. It
	// gets working styles — the current ones — rather than a 404 that
	// would leave the page unstyled, but on a short cache rather than
	// forever, since that address is not what the bytes are called.
	t.Run("a stale hash gets the current stylesheet on a short cache", func(t *testing.T) {
		stale := "0123456789abcdef"
		if stale == hash {
			t.Fatal("pick a different stale hash")
		}
		rec := httptest.NewRecorder()
		c.serveContentCSS(rec, httptest.NewRequest(http.MethodGet, contentCSSPrefix+stale+".css", nil))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if rec.Body.String() != css {
			t.Errorf("body = %q, want the current stylesheet", rec.Body.String())
		}
		cc := rec.Header().Get("Cache-Control")
		if strings.Contains(cc, "immutable") {
			t.Errorf("Cache-Control = %q, want a short cache for an address that isn't current", cc)
		}
		if !strings.Contains(cc, "max-age=60") {
			t.Errorf("Cache-Control = %q, want max-age=60", cc)
		}
	})

	// Anything that is not a 16-hex-digit hash is not an address this
	// route issues, so it is not one it answers.
	t.Run("a malformed hash is not found", func(t *testing.T) {
		for _, bad := range []string{"", "zz", "0123456789abcdefg", "0123456789ABCDEF", "../../etc/passwd"} {
			rec := httptest.NewRecorder()
			c.serveContentCSS(rec, httptest.NewRequest(http.MethodGet, contentCSSPrefix+bad+".css", nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("hash %q: status = %d, want 404", bad, rec.Code)
			}
		}
	})
}

// The editor polls this after a save to hot-swap the page's <link> once
// an asynchronous rebuild lands, so it must never be cached: telling a
// browser to remember the answer is telling it the URL never changes,
// which is the one thing this route exists to contradict.
func TestServeContentCSSCurrent(t *testing.T) {
	c, b := cssTestCMS(t)
	const css = ".cms-a{color:red}"
	b.setCache(css)

	rec := httptest.NewRecorder()
	c.serveContentCSSCurrent(rec, httptest.NewRequest(http.MethodGet, contentCSSCurrentPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	var out struct {
		Href string `json:"href"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
	if want := contentCSSPrefix + cssHash(css) + ".css"; out.Href != want {
		t.Errorf("href = %q, want %q", out.Href, want)
	}
	// The answer is the same thing {{cmsHead}} is linking, or the editor
	// would swap the page onto a different stylesheet than a reload gives.
	if link := headLink(t, c); out.Href != link {
		t.Errorf("href = %q, want the head link %q", out.Href, link)
	}
}

// schedule is wired straight into admin.Deps.ContentChanged, which is
// called on every content change whether or not Tailwind is configured —
// so the nil case is the ordinary one, not an edge.
func TestScheduleIsNilSafe(t *testing.T) {
	var b *cssRebuilder
	b.schedule()
}

// The cache is read by every render and written by a background build, so
// the two have to be safe together. Run with -race this is the test that
// says so.
func TestCacheIsSafeUnderConcurrentUse(t *testing.T) {
	_, b := cssTestCMS(t)
	b.setCache(".cms-seed{}")

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 50 {
				if i%2 == 0 {
					b.setCache(".cms-a{color:red}")
				} else {
					if _, css := b.current(t.Context()); css == "" {
						t.Errorf("read %d: the cache went empty under concurrent writes", j)
						return
					}
				}
			}
		})
	}
	wg.Wait()
}
