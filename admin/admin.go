// Package admin serves the CMS admin area: login, dashboard, and user
// management, with content, media, and settings arriving in later phases.
// All UI assets are embedded; the package has no external runtime files.
package admin

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/auth"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Deps is everything the admin area needs from the rest of the CMS.
type Deps struct {
	Sessions  *scs.SessionManager
	Users     *auth.Store
	Logger    *slog.Logger
	AdminPath string
}

type server struct {
	deps      Deps
	templates map[string]*template.Template
	throttle  *auth.Throttle
}

// New returns the admin http.Handler. The host mounts it under
// Deps.AdminPath with the prefix stripped.
func New(d Deps) http.Handler {
	s := &server{
		deps:     d,
		throttle: auth.NewThrottle(5, 15*time.Minute),
	}
	s.templates = parseTemplates()

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(fmt.Sprintf("cms admin: embedded static assets missing: %v", err))
	}

	r := chi.NewRouter()
	r.Use(d.Sessions.LoadAndSave)
	r.Use(s.csrf)
	r.Use(secureHeaders)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServerFS(static)))

	r.Get("/login", s.loginForm)
	r.Post("/login", s.login)

	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/", s.dashboard)
		r.Post("/logout", s.logout)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAdmin)
			r.Get("/users", s.usersList)
			r.Get("/users/new", s.userNew)
			r.Post("/users/new", s.userCreate)
			r.Get("/users/{id}", s.userEdit)
			r.Post("/users/{id}", s.userUpdate)
		})
	})

	return r
}

// parseTemplates builds one template set per page, each combining the shared
// layout with that page's {{define "content"}} block.
func parseTemplates() map[string]*template.Template {
	pages := []string{"login", "dashboard", "users", "user_form"}
	m := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		t, err := template.ParseFS(templateFS,
			"templates/layout.tmpl", "templates/"+page+".tmpl")
		if err != nil {
			panic(fmt.Sprintf("cms admin: parsing template %s: %v", page, err))
		}
		m[page] = t
	}
	return m
}

// templateData is passed to every admin template. Page-specific fields are
// simply unset on pages that don't use them.
type templateData struct {
	AdminPath string
	User      *auth.User // logged-in user; nil on the login page
	CSRFToken string
	Flash     string
	Error     string // form-level error message

	// User management pages.
	Users      []auth.User
	FormUser   *auth.User
	FormErrors map[string]string
	IsNew      bool
}

func (s *server) newTemplateData(r *http.Request) templateData {
	return templateData{
		AdminPath: s.deps.AdminPath,
		User:      s.currentUser(r),
		CSRFToken: s.deps.Sessions.GetString(r.Context(), sessionKeyCSRF),
		Flash:     s.deps.Sessions.PopString(r.Context(), sessionKeyFlash),
	}
}

func (s *server) render(w http.ResponseWriter, status int, page string, data templateData) {
	t, ok := s.templates[page]
	if !ok {
		s.serverError(w, fmt.Errorf("cms admin: unknown template %q", page))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "layout", data); err != nil {
		s.deps.Logger.Error("cms admin: rendering template", "page", page, "err", err)
	}
}

func (s *server) serverError(w http.ResponseWriter, err error) {
	s.deps.Logger.Error("cms admin: internal error", "err", err)
	http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
}

func (s *server) flash(r *http.Request, msg string) {
	s.deps.Sessions.Put(r.Context(), sessionKeyFlash, msg)
}
