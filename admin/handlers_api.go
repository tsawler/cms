package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/media"
)

// The JSON API backs the in-place editor. It lives under /api on the admin
// handler, so requests carry the admin session cookie; the CSRF middleware
// checks the X-CSRF-Token header on writes.

// maxRegionsBody bounds one region-save request (5 MB of content).
const maxRegionsBody = 5 << 20

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// apiSaveRegions stores in-place edits as draft blocks.
// POST /api/pages/{id}/regions  body: {"regions": {"name": "content", ...}}
func (s *server) apiSaveRegions(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}

	var body struct {
		Regions map[string]string `json:"regions"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRegionsBody))
	if err := dec.Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "Could not read the edit — try again.")
		return
	}
	if len(body.Regions) == 0 {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	isAdmin := s.currentUser(r).Role == auth.RoleAdmin
	if err := s.saveRegions(r.Context(), page.ID, page.TemplateName, body.Regions, isAdmin); err != nil {
		s.deps.Logger.Error("cms admin: api saving regions", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, "Saving failed — try again.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiPublish makes the page's draft content live.
// POST /api/pages/{id}/publish
func (s *server) apiPublish(w http.ResponseWriter, r *http.Request) {
	page, ok := s.pageFromURL(w, r)
	if !ok {
		return
	}
	if err := s.deps.Content.Publish(r.Context(), page.ID); err != nil {
		s.deps.Logger.Error("cms admin: api publishing", "page", page.ID, "err", err)
		jsonError(w, http.StatusInternalServerError, "Publishing failed — try again.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "published"})
}

type mediaJSON struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Filename string `json:"filename"`
	Alt      string `json:"alt"`
	Size     string `json:"size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Thumb    string `json:"thumb"`
	Web      string `json:"web"`
	Original string `json:"original"`
}

func toMediaJSON(v media.View) mediaJSON {
	return mediaJSON{
		ID:       v.ID,
		Kind:     string(v.Kind),
		Filename: v.Filename,
		Alt:      v.Alt,
		Size:     v.SizeHuman(),
		Width:    v.Width,
		Height:   v.Height,
		Thumb:    v.ThumbURL,
		Web:      v.WebURL,
		Original: v.OriginalURL,
	}
}

// apiMediaList returns the media library for the editor's pickers.
// GET /api/media?kind=image|file&q=term&folder=<id|root>
func (s *server) apiMediaList(w http.ResponseWriter, r *http.Request) {
	kind := media.Kind(r.URL.Query().Get("kind"))
	if kind != "" && kind != media.KindImage && kind != media.KindFile {
		jsonError(w, http.StatusBadRequest, "Unknown media kind.")
		return
	}
	folderID, unfiled := parseFolderParam(r.URL.Query().Get("folder"))
	items, err := s.deps.Media.All(r.Context(), s.deps.DefaultLocale, media.ListOptions{
		Kind:     kind,
		Query:    strings.TrimSpace(r.URL.Query().Get("q")),
		FolderID: folderID,
		Unfiled:  unfiled,
	})
	if err != nil {
		s.deps.Logger.Error("cms admin: api listing media", "err", err)
		jsonError(w, http.StatusInternalServerError, "Could not load the media library.")
		return
	}
	views := s.deps.Media.Views(items)
	out := make([]mediaJSON, len(views))
	for i, v := range views {
		out[i] = toMediaJSON(v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"media": out})
}

// apiMediaUpload accepts a multipart image upload and returns its record.
// POST /api/media  (multipart field "file")
func (s *server) apiMediaUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			jsonError(w, http.StatusRequestEntityTooLarge, "That file is too large — images must be under 25 MB.")
			return
		}
		jsonError(w, http.StatusUnprocessableEntity, "Choose an image file to upload.")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		jsonError(w, http.StatusRequestEntityTooLarge, "That file is too large — images must be under 25 MB.")
		return
	}

	folderID, _ := parseFolderParam(r.PostFormValue("folder"))
	md, err := s.deps.Media.Upload(r.Context(), sanitizeFilename(header.Filename), data, s.currentUser(r).ID, folderID)
	if err != nil {
		if errors.Is(err, media.ErrUnsupportedType) {
			jsonError(w, http.StatusUnprocessableEntity, "That file type isn't supported. Use an image (JPEG, PNG, GIF, WebP), PDF, office document, text/CSV, or ZIP.")
			return
		}
		s.deps.Logger.Error("cms admin: api media upload", "err", err)
		jsonError(w, http.StatusUnprocessableEntity, "That file could not be processed.")
		return
	}

	view := s.deps.Media.Views([]media.Media{*md})[0]
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "media": toMediaJSON(view)})
}

// apiFoldersList returns all media folders with item counts.
// GET /api/media/folders
func (s *server) apiFoldersList(w http.ResponseWriter, r *http.Request) {
	folders, err := s.deps.Media.Folders(r.Context())
	if err != nil {
		s.deps.Logger.Error("cms admin: api listing folders", "err", err)
		jsonError(w, http.StatusInternalServerError, "Could not load folders.")
		return
	}
	type folderJSON struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	out := make([]folderJSON, len(folders))
	for i, f := range folders {
		out[i] = folderJSON{ID: f.ID, Name: f.Name, Count: f.Count}
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": out})
}

// apiFolderCreate makes a folder from the editor's picker.
// POST /api/media/folders  body: {"name": "..."}
func (s *server) apiFolderCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "Could not read the folder name.")
		return
	}
	f, err := s.deps.Media.CreateFolder(r.Context(), body.Name)
	if errors.Is(err, media.ErrDuplicateFolder) || errors.Is(err, media.ErrBadFolderName) {
		jsonError(w, http.StatusUnprocessableEntity, friendlyFolderError(err))
		return
	}
	if err != nil {
		s.deps.Logger.Error("cms admin: api creating folder", "err", err)
		jsonError(w, http.StatusInternalServerError, "Could not create the folder.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true,
		"folder": map[string]any{"id": f.ID, "name": f.Name, "count": 0}})
}
