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

func (s *server) uploadTooLargeMsg() string {
	return fmt.Sprintf("That file is too large — images and documents must be under %d MB, videos under %d MB.",
		media.MaxImageDocBytes>>20, s.deps.Media.MaxVideoBytes()>>20)
}

const unsupportedTypeMsg = "That file type isn't supported. Use an image (JPEG, PNG, GIF, WebP), video (MP4, WebM), PDF, office document, text/CSV, or ZIP."

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

func (s *server) renderMediaList(w http.ResponseWriter, r *http.Request, status int, formError string) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	folderParam := r.URL.Query().Get("folder")
	folderID, unfiled := parseFolderParam(folderParam)

	items, err := s.deps.Media.All(r.Context(), s.deps.DefaultLocale, media.ListOptions{
		Query:    query,
		FolderID: folderID,
		Unfiled:  unfiled,
	})
	if err != nil {
		s.serverError(w, err)
		return
	}
	folders, err := s.deps.Media.Folders(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}

	data := s.newTemplateData(r)
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
	data.MaxVideoMB = s.deps.Media.MaxVideoBytes() >> 20
	data.Error = formError
	s.render(w, status, "media", data)
}

func (s *server) mediaUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.uploadLimit())
	file, header, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.renderMediaList(w, r, http.StatusRequestEntityTooLarge, s.uploadTooLargeMsg())
			return
		}
		s.renderMediaList(w, r, http.StatusUnprocessableEntity, "Choose a file to upload.")
		return
	}
	defer file.Close()

	user := s.currentUser(r)
	folderID, _ := parseFolderParam(r.PostFormValue("folder"))
	md, err := s.deps.Media.UploadFrom(r.Context(), sanitizeFilename(header.Filename), file, header.Size, nil, user.ID, folderID)
	if err != nil {
		switch {
		case errors.Is(err, media.ErrTooLarge):
			s.renderMediaList(w, r, http.StatusRequestEntityTooLarge, s.uploadTooLargeMsg())
		case errors.Is(err, media.ErrUnsupportedType) || strings.Contains(err.Error(), "decoding image"):
			s.renderMediaList(w, r, http.StatusUnprocessableEntity, unsupportedTypeMsg)
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

	switch md.Kind {
	case media.KindVideo:
		s.flash(r, "Video uploaded.")
	case media.KindFile:
		s.flash(r, "Document uploaded.")
	default:
		s.flash(r, "Image uploaded.")
	}
	http.Redirect(w, r, s.deps.AdminPath+"/media", http.StatusSeeOther)
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
	s.flash(r, "Description saved.")
	http.Redirect(w, r, s.deps.AdminPath+"/media", http.StatusSeeOther)
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
	s.flash(r, "Media deleted.")
	http.Redirect(w, r, s.deps.AdminPath+"/media", http.StatusSeeOther)
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
	dest := r.Header.Get("Referer")
	if dest == "" {
		dest = s.deps.AdminPath + "/media"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// mediaFolderCreate adds a folder from the admin media page.
func (s *server) mediaFolderCreate(w http.ResponseWriter, r *http.Request) {
	_, err := s.deps.Media.CreateFolder(r.Context(), r.PostFormValue("name"))
	if errors.Is(err, media.ErrDuplicateFolder) || errors.Is(err, media.ErrBadFolderName) {
		s.renderMediaList(w, r, http.StatusUnprocessableEntity, friendlyFolderError(err))
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, "Folder created.")
	http.Redirect(w, r, s.deps.AdminPath+"/media", http.StatusSeeOther)
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
	s.flash(r, "Folder deleted — its files are now unfiled.")
	http.Redirect(w, r, s.deps.AdminPath+"/media", http.StatusSeeOther)
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
