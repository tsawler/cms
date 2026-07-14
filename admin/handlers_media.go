package admin

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/media"
)

// maxUploadBytes bounds one image upload (25 MB).
const maxUploadBytes = 25 << 20

func (s *server) mediaList(w http.ResponseWriter, r *http.Request) {
	s.renderMediaList(w, r, http.StatusOK, "")
}

func (s *server) renderMediaList(w http.ResponseWriter, r *http.Request, status int, formError string) {
	items, err := s.deps.Media.All(r.Context(), s.deps.DefaultLocale)
	if err != nil {
		s.serverError(w, err)
		return
	}
	data := s.newTemplateData(r)
	data.Media = s.deps.Media.Views(items)
	data.Error = formError
	s.render(w, status, "media", data)
}

func (s *server) mediaUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.renderMediaList(w, r, http.StatusRequestEntityTooLarge, "That file is too large — images must be under 25 MB.")
			return
		}
		s.renderMediaList(w, r, http.StatusUnprocessableEntity, "Choose an image file to upload.")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.renderMediaList(w, r, http.StatusRequestEntityTooLarge, "That file is too large — images must be under 25 MB.")
			return
		}
		s.serverError(w, err)
		return
	}

	user := s.currentUser(r)
	md, err := s.deps.Media.Upload(r.Context(), sanitizeFilename(header.Filename), data, user.ID)
	if err != nil {
		if errors.Is(err, media.ErrUnsupportedType) || strings.Contains(err.Error(), "decoding image") {
			s.renderMediaList(w, r, http.StatusUnprocessableEntity, "That doesn't look like an image. Use a JPEG, PNG, GIF, or WebP file.")
			return
		}
		s.serverError(w, err)
		return
	}

	alt := strings.TrimSpace(r.PostFormValue("alt"))
	if alt != "" {
		if err := s.deps.Media.UpdateAlt(r.Context(), md.ID, s.deps.DefaultLocale, alt); err != nil {
			s.serverError(w, err)
			return
		}
	}

	s.flash(r, "Image uploaded.")
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
	s.flash(r, "Image deleted.")
	http.Redirect(w, r, s.deps.AdminPath+"/media", http.StatusSeeOther)
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
