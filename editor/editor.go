// Package editor ships the in-place editing script injected into pages
// viewed by logged-in editors, plus a vendored copy of TinyMCE 6.8.6 (the
// final MIT-licensed release; see tinymce/license.txt) that provides the
// WYSIWYG behavior for rich HTML regions. TinyMCE runs in inline mode, so
// content is edited directly in the page using the page's own styles, and
// everything is self-hosted from the module — no CDN, no build step.
//
// The glue script's own chrome (toolbar, media picker) lives in Shadow DOM,
// isolated from host-page CSS. TinyMCE's floating UI uses tox- prefixed
// classes in the light DOM. TinyMCE loads lazily, only when an editor
// actually presses "Edit page".
package editor

import (
	"embed"
	"net/http"
	"strings"
)

// PathPrefix is the public route the editor assets are served under.
const PathPrefix = "/cms/editor/"

//go:embed editor.js tinymce
var assets embed.FS

// Handler serves the editor script and the vendored TinyMCE files. Mount it
// at PathPrefix.
func Handler() http.Handler {
	fileServer := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // files only, no directory listings
			return
		}
		// The vendored files change only with a module upgrade; cache
		// modestly so upgrades still roll out quickly.
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		r.URL.Path = strings.TrimPrefix(r.URL.Path, strings.TrimSuffix(PathPrefix, "/"))
		fileServer.ServeHTTP(w, r)
	})
}
