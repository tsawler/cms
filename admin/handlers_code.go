package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

// maxCodeSnippetLen caps one custom-code block. Generous for a widget;
// short of a paste that would bloat every page it appears on.
const maxCodeSnippetLen = 100_000

// maxCodeBody bounds a code-snippet save: the markup plus the small
// envelope around it.
const maxCodeBody = maxCodeSnippetLen + 4096

// codeSnippetJSON is one library entry as the editor sees it. The list
// endpoint leaves HTML out — the drawer only needs names to choose from,
// and the code is fetched when one is opened.
type codeSnippetJSON struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	HTML string `json:"html,omitempty"`
}

// apiCodeList returns the custom-code library, names only.
// GET /api/code  (admin only)
func (s *server) apiCodeList(w http.ResponseWriter, r *http.Request) {
	all, err := s.deps.CodeSnippets.All(r.Context())
	if err != nil {
		s.deps.Logger.Error("cms admin: api listing code snippets", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not load the code blocks."))
		return
	}
	out := make([]codeSnippetJSON, 0, len(all))
	for _, c := range all {
		out = append(out, codeSnippetJSON{Key: c.Key, Name: c.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": out})
}

// apiCodeGet returns one code block, markup and all.
// GET /api/code/{key}  (admin only)
func (s *server) apiCodeGet(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if !snippets.ValidCodeKey(key) {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Unknown code block."))
		return
	}
	c, err := s.deps.CodeSnippets.ByKey(r.Context(), key)
	if errors.Is(err, snippets.ErrNotFound) {
		jsonError(w, http.StatusNotFound, s.tr(r, "Unknown code block."))
		return
	}
	if err != nil {
		s.deps.Logger.Error("cms admin: api reading code snippet", "key", key, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not load the code block."))
		return
	}
	writeJSON(w, http.StatusOK, codeSnippetJSON{Key: c.Key, Name: c.Name, HTML: c.HTML})
}

// codeBody is what a create or a save sends.
type codeBody struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	HTML string `json:"html"`
}

// readCodeBody decodes and validates a create/save body, answering the
// request itself when something is wrong.
func (s *server) readCodeBody(w http.ResponseWriter, r *http.Request) (codeBody, bool) {
	var body codeBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCodeBody))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Could not read the edit — try again."))
		return body, false
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "Give the code block a name."))
		return body, false
	}
	if len(body.HTML) > maxCodeSnippetLen {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "That code block is too long."))
		return body, false
	}
	return body, true
}

// apiCodeCreate adds a library entry. The key comes from the submitted
// one or, failing that, from the name; either way it must be free.
// POST /api/code  body: {"key": "...", "name": "...", "html": "..."}  (admin only)
func (s *server) apiCodeCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readCodeBody(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(body.Key)
	if key == "" {
		key = snippets.CodeKeyFor(body.Name)
	}
	if !snippets.ValidCodeKey(key) {
		jsonError(w, http.StatusUnprocessableEntity,
			s.tr(r, "A code block's key can hold lowercase letters, digits, and hyphens."))
		return
	}
	c := &snippets.CodeSnippet{Key: key, Name: body.Name, HTML: body.HTML}
	_, err := s.deps.CodeSnippets.Insert(r.Context(), c)
	if errors.Is(err, snippets.ErrDuplicateCodeKey) {
		jsonError(w, http.StatusUnprocessableEntity, s.tr(r, "That key is already used by another code block."))
		return
	}
	if err != nil {
		s.deps.Logger.Error("cms admin: api creating code snippet", "key", key, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, codeSnippetJSON{Key: c.Key, Name: c.Name, HTML: c.HTML})
}

// apiCodeSave replaces a library entry's name and markup. Every page
// using the key shows the new code on its next render.
// PUT /api/code/{key}  body: {"name": "...", "html": "..."}  (admin only)
func (s *server) apiCodeSave(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if !snippets.ValidCodeKey(key) {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Unknown code block."))
		return
	}
	body, ok := s.readCodeBody(w, r)
	if !ok {
		return
	}
	err := s.deps.CodeSnippets.Update(r.Context(), &snippets.CodeSnippet{
		Key: key, Name: body.Name, HTML: body.HTML,
	})
	if errors.Is(err, snippets.ErrNotFound) {
		jsonError(w, http.StatusNotFound, s.tr(r, "Unknown code block."))
		return
	}
	if err != nil {
		s.deps.Logger.Error("cms admin: api saving code snippet", "key", key, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Saving failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiCodeDelete removes a library entry. Placeholders still naming it are
// left where they are and render as nothing, so this is undone by
// creating the key again.
// DELETE /api/code/{key}  (admin only)
func (s *server) apiCodeDelete(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if !snippets.ValidCodeKey(key) {
		jsonError(w, http.StatusBadRequest, s.tr(r, "Unknown code block."))
		return
	}
	err := s.deps.CodeSnippets.Delete(r.Context(), key)
	if errors.Is(err, snippets.ErrNotFound) {
		jsonError(w, http.StatusNotFound, s.tr(r, "Unknown code block."))
		return
	}
	if err != nil {
		s.deps.Logger.Error("cms admin: api deleting code snippet", "key", key, "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Deleting failed — try again."))
		return
	}
	s.contentChanged()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// codeLookup resolves custom-code keys for a preview render, the same way
// the public site does. Nil when no library is configured, which leaves
// placeholders unexpanded.
func (s *server) codeLookup(r *http.Request) render.CodeLookup {
	if s.deps.CodeSnippets == nil {
		return nil
	}
	return s.deps.CodeSnippets.Lookup(r.Context(), func(key string, err error) {
		s.deps.Logger.Error("cms admin: loading custom code block", "key", key, "err", err)
	})
}
