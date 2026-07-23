package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/media"
)

// uploadLimit is the request-body cap for media uploads: big enough for
// the largest allowed video, and never below the image/document limit.
func (s *server) uploadLimit() int64 {
	limit := int64(media.MaxImageDocBytes)
	if v := s.deps.Media.MaxVideoBytes(); v > limit {
		limit = v
	}
	return limit
}

func (s *server) uploadTooLargeMsg(r *http.Request) string {
	return fmt.Sprintf(s.tr(r, "That file is too large — images and documents must be under %d MB, videos under %d MB."),
		media.MaxImageDocBytes>>20, s.deps.Media.MaxVideoBytes()>>20)
}

const unsupportedTypeMsg = "That file type isn't supported. Use an image (JPEG, PNG, GIF, WebP, SVG), video (MP4, WebM), PDF, office document, text/CSV, or ZIP."

const unsafeSVGMsg = "That SVG can't be used — it contains scripts or other active content."

func (s *server) mediaList(w http.ResponseWriter, r *http.Request) {
	s.renderMediaList(w, r, http.StatusOK, "")
}

// parseFolderParam maps a folder query/form value to list options: "" means
// no filter, "root" means unfiled, digits mean a folder id.
func parseFolderParam(v string) (folderID *int64, unfiled bool) {
	if v == "root" {
		return nil, true
	}
	if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
		return &id, false
	}
	return nil, false
}

// mediaTab validates a tab query/form value; anything unknown lands on
// the images tab.
func mediaTab(v string) string {
	switch v {
	case "documents", "videos":
		return v
	}
	return "images"
}

// kindForTab maps a media tab to the kind whose items and folders it
// lists.
func kindForTab(tab string) media.Kind {
	switch tab {
	case "documents":
		return media.KindFile
	case "videos":
		return media.KindVideo
	}
	return media.KindImage
}

func (s *server) renderMediaList(w http.ResponseWriter, r *http.Request, status int, formError string) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	folderParam := r.URL.Query().Get("folder")
	folderID, _ := parseFolderParam(folderParam)
	tab := mediaTab(r.URL.Query().Get("tab"))

	allFolders, err := s.deps.Media.Folders(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}

	data := s.newTemplateData(r)
	kind := kindForTab(tab)

	// Directory-style browsing: without a search the page shows one
	// directory — the root (unfiled items plus the folder entries) or
	// one folder's contents. A search spans every folder and lists
	// results flat.
	opts := media.ListOptions{Query: query}
	if query == "" {
		if folderID != nil {
			// Only this tab's kind resolves: a folder link carried over
			// from another tab (or a stale URL) falls back to this tab's
			// root rather than showing a folder the tab doesn't have.
			for i := range allFolders {
				if allFolders[i].ID == *folderID && allFolders[i].Kind == kind {
					data.CurrentFolder = &allFolders[i]
					break
				}
			}
		}
		if data.CurrentFolder != nil {
			opts.FolderID = folderID
		} else {
			opts.Unfiled = true // root; stale folder links land here too
			folderParam = ""
		}
	}

	items, err := s.deps.Media.All(r.Context(), s.deps.DefaultLocale, opts)
	if err != nil {
		s.serverError(w, err)
		return
	}

	// Each tab lists only its own kind's folders, and new folders are
	// created in the kind being browsed.
	folders := make([]media.Folder, 0, len(allFolders))
	for _, f := range allFolders {
		if f.Kind == kind {
			folders = append(folders, f)
		}
	}
	for _, v := range s.deps.Media.Views(items) {
		switch v.Kind {
		case media.KindFile:
			data.Documents = append(data.Documents, v)
		case media.KindVideo:
			data.Videos = append(data.Videos, v)
		default:
			data.Media = append(data.Media, v)
		}
	}
	data.Folders = folders
	data.MediaQuery = query
	data.MediaFolder = folderParam
	data.MediaTab = tab
	data.MediaKind = string(kind)
	data.MaxVideoMB = s.deps.Media.MaxVideoBytes() >> 20
	data.Error = formError
	s.render(w, status, "media", data)
}

// backToMedia redirects to the referring media view, so the active tab
// and filters survive the round-trip; without a Referer (direct POST,
// privacy proxy) it falls back to the media page's default view.
func (s *server) backToMedia(w http.ResponseWriter, r *http.Request) {
	dest := r.Header.Get("Referer")
	if dest == "" {
		dest = s.deps.AdminPath + "/media"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (s *server) mediaUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.uploadLimit())
	file, header, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.renderMediaList(w, r, http.StatusRequestEntityTooLarge, s.uploadTooLargeMsg(r))
			return
		}
		s.renderMediaList(w, r, http.StatusUnprocessableEntity, s.tr(r, "Choose a file to upload."))
		return
	}
	defer file.Close()

	user := s.currentUser(r)
	folderID, _ := parseFolderParam(r.PostFormValue("folder"))
	md, err := s.deps.Media.UploadFrom(r.Context(), sanitizeFilename(header.Filename), file, header.Size, nil, user.ID, folderID)
	if err != nil {
		switch {
		case errors.Is(err, media.ErrTooLarge):
			s.renderMediaList(w, r, http.StatusRequestEntityTooLarge, s.uploadTooLargeMsg(r))
		case errors.Is(err, media.ErrUnsafeSVG):
			s.renderMediaList(w, r, http.StatusUnprocessableEntity, s.tr(r, unsafeSVGMsg))
		case errors.Is(err, media.ErrUnsupportedType) || strings.Contains(err.Error(), "decoding image") ||
			strings.Contains(err.Error(), "parsing svg"):
			s.renderMediaList(w, r, http.StatusUnprocessableEntity, s.tr(r, unsupportedTypeMsg))
		default:
			s.serverError(w, err)
		}
		return
	}

	alt := strings.TrimSpace(r.PostFormValue("alt"))
	if alt != "" {
		if err := s.deps.Media.UpdateAlt(r.Context(), md.ID, s.deps.DefaultLocale, alt); err != nil {
			s.serverError(w, err)
			return
		}
	}

	// Land on the tab that shows what was just uploaded.
	tab := "images"
	switch md.Kind {
	case media.KindVideo:
		s.flash(r, s.tr(r, "Video uploaded."))
		tab = "videos"
	case media.KindFile:
		s.flash(r, s.tr(r, "Document uploaded."))
		tab = "documents"
	default:
		s.flash(r, s.tr(r, "Image uploaded."))
	}
	http.Redirect(w, r, s.deps.AdminPath+"/media?tab="+tab, http.StatusSeeOther)
}

func (s *server) mediaUpdateAlt(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaIDFromURL(w, r)
	if !ok {
		return
	}
	alt := strings.TrimSpace(r.PostFormValue("alt"))
	if err := s.deps.Media.UpdateAlt(r.Context(), id, s.deps.DefaultLocale, alt); err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, s.tr(r, "Description saved."))
	s.backToMedia(w, r)
}

func (s *server) mediaDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaIDFromURL(w, r)
	if !ok {
		return
	}
	err := s.deps.Media.Delete(r.Context(), id, s.deps.DefaultLocale)
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, s.tr(r, "Media deleted."))
	s.backToMedia(w, r)
}

// mediaMove refiles one media item into a folder ("" form value = unfiled).
func (s *server) mediaMove(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaIDFromURL(w, r)
	if !ok {
		return
	}
	folderID, _ := parseFolderParam(r.PostFormValue("folder"))
	if err := s.deps.Media.Move(r.Context(), id, folderID); err != nil {
		s.serverError(w, err)
		return
	}
	s.backToMedia(w, r)
}

// mediaFolderCreate adds a folder from the admin media page, in the kind
// of the tab it was created on.
func (s *server) mediaFolderCreate(w http.ResponseWriter, r *http.Request) {
	kind := kindForTab(mediaTab(r.PostFormValue("tab")))
	_, err := s.deps.Media.CreateFolder(r.Context(), r.PostFormValue("name"), kind)
	if errors.Is(err, media.ErrDuplicateFolder) || errors.Is(err, media.ErrBadFolderName) {
		s.renderMediaList(w, r, http.StatusUnprocessableEntity, s.tr(r, friendlyFolderError(err)))
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, s.tr(r, "Folder created."))
	s.backToMedia(w, r)
}

// mediaFolderDelete removes a folder; its contents become unfiled.
func (s *server) mediaFolderDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.deps.Media.DeleteFolder(r.Context(), id); err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, s.tr(r, "Folder deleted — its files are now unfiled."))
	// The Referer points into the folder that just vanished, so go back
	// to the root view on the tab the delete form was on instead.
	http.Redirect(w, r, s.deps.AdminPath+"/media?tab="+mediaTab(r.PostFormValue("tab")), http.StatusSeeOther)
}

func friendlyFolderError(err error) string {
	if errors.Is(err, media.ErrDuplicateFolder) {
		return "A folder with that name already exists."
	}
	return "Folder names must be between 1 and 60 characters."
}

func (s *server) mediaIDFromURL(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// sanitizeFilename keeps just the final path element of a client-supplied
// filename, defensively.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		name = "upload"
	}
	return name
}
