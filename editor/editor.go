// Package editor ships the in-place editing script injected into pages
// viewed by logged-in editors, plus a vendored copy of TinyMCE 6.8.6 (the
// final MIT-licensed release; see tinymce/license.txt) that provides the
// WYSIWYG behavior for rich HTML regions. TinyMCE runs in inline mode, so
// content is edited directly in the page using the page's own styles, and
// everything is self-hosted from the module — no CDN, and no build step
// for consumers.
//
// editor.js is generated: the source lives in src/ as ES modules and is
// bundled by the build tool in build/ (a nested Go module wrapping
// esbuild, so it never enters consumers' dependency graphs). After
// editing anything under src/, regenerate the committed artifact with
// `go generate ./editor` — or `go run -C editor/build . -watch` for a
// rebuild-on-save loop while developing.
//
// The glue script's own chrome (toolbar, media picker) lives in Shadow DOM,
// isolated from host-page CSS. TinyMCE's floating UI uses tox- prefixed
// classes in the light DOM. TinyMCE loads lazily, only when an editor
// actually presses "Edit page".
package editor

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
)

// PathPrefix is the public route the editor assets are served under.
const PathPrefix = "/cms/editor/"

//go:generate go run -C build .

//go:embed editor.js tinymce
var assets embed.FS

// versionLen is how much of the digest the URL carries. Eight bytes of
// SHA-256 is far past what a cache key needs and keeps the path readable.
const versionLen = 16

// Version is a content address for the whole editor bundle — the script
// and every vendored TinyMCE file — and it appears in the URL those
// assets are served under.
//
// It exists because the alternative does not work. Served at a fixed path
// with a max-age, the script is a mutable URL a browser was told it could
// keep: ship a fix and editors go on running the old one until their
// cache expires, with no symptom except behaviour that does not match the
// code. A hard refresh papers over it for whoever thinks to try one.
//
// With the digest in the path, a changed bundle is a different URL that
// no cache has seen, so the new script is fetched the first time a page
// asks for it — and the old one can be marked immutable honestly, because
// it never changes.
//
// Computed once, lazily: hashing half a megabyte of embedded assets is
// cheap but pointless in a process that never serves an edit render.
var Version = sync.OnceValue(func() string {
	h := sha256.New()
	err := fs.WalkDir(assets, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := assets.ReadFile(path)
		if err != nil {
			return err
		}
		// Paths as well as contents, so moving a file counts as a change.
		fmt.Fprintf(h, "%s\x00%d\x00", path, len(body))
		h.Write(body)
		return nil
	})
	if err != nil {
		// Unreachable: the tree is embedded in the binary. Falling back
		// to a constant would silently restore the stale-cache bug, so
		// prefer a value that is merely useless for caching.
		return "unversioned"
	}
	return hex.EncodeToString(h.Sum(nil))[:versionLen]
})

// ScriptPath is the versioned URL of the editor script, for the tag that
// loads it.
func ScriptPath() string { return PathPrefix + Version() + "/editor.js" }

// looksLikeVersion reports whether a path segment is one of our digests —
// current or from an older build. Used to recognise, and forgive, a URL
// minted by a previous version of the binary.
func looksLikeVersion(seg string) bool {
	if len(seg) != versionLen {
		return false
	}
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Handler serves the editor script and the vendored TinyMCE files. Mount it
// at PathPrefix.
//
// Assets live under a version segment — /cms/editor/<version>/editor.js —
// and TinyMCE's own files sit beside the script under the same segment, so
// its runtime base URL carries the version too and a stale skin cannot
// outlive the script that loads it.
//
// A request naming a *different* version is still served, from the current
// files, on a short cache. That address can only come from HTML minted by
// an older build — a page a browser cached, or one held by a CDN — and
// answering it with the working editor beats 404ing an editor who did
// nothing wrong.
func Handler() http.Handler {
	fileServer := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // files only, no directory listings
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, PathPrefix)
		current := false
		if seg, tail, ok := strings.Cut(rest, "/"); ok && looksLikeVersion(seg) {
			current = seg == Version()
			rest = tail
		}

		if current {
			// Immutable is the truth here, not an optimisation: this URL
			// names these bytes and will never name any others.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// An unversioned or outdated address. Short cache, so the
			// browser comes back for the versioned one soon.
			w.Header().Set("Cache-Control", "public, max-age=60")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		r.URL.Path = "/" + rest
		fileServer.ServeHTTP(w, r)
	})
}
