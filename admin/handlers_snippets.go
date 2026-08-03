package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

func (s *server) snippetsList(w http.ResponseWriter, r *http.Request) {
	stored, err := s.deps.Snippets.All(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	data := s.newTemplateData(r)
	data.Snippets = stored
	data.ConfigSnippets = s.deps.ConfigSnippets
	s.render(w, http.StatusOK, "snippets", data)
}

func (s *server) snippetNew(w http.ResponseWriter, r *http.Request) {
	s.renderSnippetForm(w, r, &snippets.Snippet{}, true, nil)
}

func (s *server) snippetCreate(w http.ResponseWriter, r *http.Request) {
	form, errs := s.parseSnippetForm(r)
	if len(errs) > 0 {
		s.renderSnippetForm(w, r, form, true, errs)
		return
	}
	if _, err := s.deps.Snippets.Insert(r.Context(), form); err != nil {
		s.serverError(w, err)
		return
	}
	s.contentChanged()
	s.flash(r, s.tr(r, "Snippet created — it's now available in the editor's palette."))
	http.Redirect(w, r, s.deps.AdminPath+"/snippets", http.StatusSeeOther)
}

func (s *server) snippetEdit(w http.ResponseWriter, r *http.Request) {
	sn, ok := s.snippetFromURL(w, r)
	if !ok {
		return
	}
	s.renderSnippetForm(w, r, sn, false, nil)
}

func (s *server) snippetUpdate(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.snippetFromURL(w, r)
	if !ok {
		return
	}
	form, errs := s.parseSnippetForm(r)
	form.ID = existing.ID
	if len(errs) > 0 {
		s.renderSnippetForm(w, r, form, false, errs)
		return
	}
	if err := s.deps.Snippets.Update(r.Context(), form); err != nil {
		s.serverError(w, err)
		return
	}
	s.contentChanged()
	s.flash(r, s.tr(r, "Snippet saved."))
	http.Redirect(w, r, s.deps.AdminPath+"/snippets", http.StatusSeeOther)
}

func (s *server) snippetDelete(w http.ResponseWriter, r *http.Request) {
	sn, ok := s.snippetFromURL(w, r)
	if !ok {
		return
	}
	if err := s.deps.Snippets.Delete(r.Context(), sn.ID); err != nil {
		s.serverError(w, err)
		return
	}
	s.contentChanged()
	s.flash(r, s.tr(r, "Snippet deleted. Copies already inserted into pages are unchanged."))
	http.Redirect(w, r, s.deps.AdminPath+"/snippets", http.StatusSeeOther)
}

// parseSnippetForm reads the shared new/edit form. A "Section preset"
// type gives the snippet a settings map — keys resolved against the
// configured section styles exactly like the editor's ⚙ dialog, so
// unknown or tampered values become the defaults rather than junk.
func (s *server) parseSnippetForm(r *http.Request) (*snippets.Snippet, map[string]string) {
	errs := map[string]string{}
	sn := &snippets.Snippet{
		Name: strings.TrimSpace(r.PostFormValue("name")),
		HTML: strings.TrimSpace(r.PostFormValue("html")),
	}
	if sn.Name == "" {
		errs["name"] = s.tr(r, "Name is required.")
	}
	if sn.HTML == "" {
		errs["html"] = s.tr(r, "The snippet needs some HTML.")
	}
	if r.PostFormValue("kind") == "preset" {
		// bg and width are always stored so the map is never empty (an
		// empty map would read as a plain block downstream); height,
		// valign, and corners only when they differ from the natural
		// defaults.
		sn.Settings = map[string]string{
			"bg":    s.deps.SectionStyles.Background(r.PostFormValue("set_bg")).Key,
			"width": s.deps.SectionStyles.Width(r.PostFormValue("set_width")).Key,
		}
		if h := render.ValidSectionHeight(r.PostFormValue("set_height")); h != "" {
			sn.Settings["height"] = h
		}
		if v := render.ValidSectionVAlign(r.PostFormValue("set_valign")); v != "" {
			sn.Settings["valign"] = v
		}
		if list := s.deps.SectionStyles.Corners; len(list) > 0 {
			if c := s.deps.SectionStyles.Corner(r.PostFormValue("set_corners")); c.Key != list[0].Key {
				sn.Settings["corners"] = c.Key
			}
		}
		if list := s.deps.SectionStyles.Paddings; len(list) > 0 {
			if p := s.deps.SectionStyles.Padding(r.PostFormValue("set_padding")); p.Key != list[0].Key {
				sn.Settings["padding"] = p.Key
			}
		}
	}
	return sn, errs
}

func (s *server) renderSnippetForm(w http.ResponseWriter, r *http.Request, sn *snippets.Snippet, isNew bool, errs map[string]string) {
	status := http.StatusOK
	if len(errs) > 0 {
		status = http.StatusUnprocessableEntity
	}
	data := s.newTemplateData(r)
	data.FormSnippet = sn
	data.IsNew = isNew
	data.FormErrors = errs
	data.SectionStyles = s.deps.SectionStyles
	s.render(w, status, "snippet_form", data)
}

func (s *server) snippetFromURL(w http.ResponseWriter, r *http.Request) (*snippets.Snippet, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	sn, err := s.deps.Snippets.GetByID(r.Context(), id)
	if errors.Is(err, snippets.ErrNotFound) {
		http.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		s.serverError(w, err)
		return nil, false
	}
	return sn, true
}

// apiSnippetsList returns every snippet — config-registered first, then
// admin-created — for the editor's palette.
// GET /api/snippets
func (s *server) apiSnippetsList(w http.ResponseWriter, r *http.Request) {
	stored, err := s.deps.Snippets.All(r.Context())
	if err != nil {
		s.deps.Logger.Error("cms admin: api listing snippets", "err", err)
		jsonError(w, http.StatusInternalServerError, s.tr(r, "Could not load snippets."))
		return
	}
	type snippetJSON struct {
		Name string `json:"name"`
		HTML string `json:"html"`
		// Non-nil for section presets: the section settings the editor
		// applies when this snippet starts a new section.
		Settings map[string]string `json:"settings,omitempty"`
	}
	out := make([]snippetJSON, 0, len(s.deps.ConfigSnippets)+len(stored))
	for _, sn := range s.deps.ConfigSnippets {
		out = append(out, snippetJSON{Name: sn.Name, HTML: sn.HTML, Settings: sn.Settings})
	}
	for _, sn := range stored {
		out = append(out, snippetJSON{Name: sn.Name, HTML: sn.HTML, Settings: sn.Settings})
	}
	writeJSON(w, http.StatusOK, map[string]any{"snippets": out})
}
