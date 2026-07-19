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
	"github.com/tsawler/cms/captcha"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Deps is everything the admin area needs from the rest of the CMS.
type Deps struct {
	Sessions       *scs.SessionManager
	Users          *auth.Store
	Content        *content.Store
	Renderer       *render.Renderer // nil when the host has not configured templates
	Media          *media.Manager   // nil when the host has not configured an object store
	Snippets       *snippets.Store
	Captcha        *captcha.Client       // nil when login CAPTCHA is not configured
	ConfigSnippets []snippets.Snippet    // host-registered palette entries
	SectionStyles  *render.SectionStyles // curated section settings
	Sections       []Section             // host-registered admin pages, already validated
	Logger         *slog.Logger
	AdminPath      string
	DefaultLocale  string

	// RememberFor is how long a "Remember me" login persists. The zero
	// value falls back to 24h so a partially-populated Deps (tests,
	// direct package use) behaves sensibly.
	RememberFor time.Duration
}

type server struct {
	deps      Deps
	templates map[string]*template.Template
	throttle  *auth.Throttle
}

// New returns the admin http.Handler. The host mounts it under
// Deps.AdminPath with the prefix stripped.
func New(d Deps) http.Handler {
	if d.RememberFor <= 0 {
		d.RememberFor = 24 * time.Hour
	}
	s := &server{
		deps:     d,
		throttle: auth.NewThrottle(5, 15*time.Minute),
	}
	s.templates = parseTemplates()

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(fmt.Sprintf("cms admin: embedded static assets missing: %v", err))
	}

	capOrigin := ""
	if d.Captcha != nil {
		capOrigin = d.Captcha.Origin()
	}

	r := chi.NewRouter()
	r.Use(d.Sessions.LoadAndSave)
	r.Use(s.csrf)
	r.Use(secureHeaders(capOrigin))

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServerFS(static)))

	r.Get("/login", s.loginForm)
	r.Post("/login", s.login)
	r.Get("/captcha.js", s.captchaConfigJS)

	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/", s.dashboard)
		r.Post("/logout", s.logout)

		if d.Renderer != nil {
			r.Get("/pages", s.pagesList)
			r.Get("/pages/new", s.pageNew)
			r.Post("/pages/new", s.pageCreate)
			r.Get("/pages/{id}", s.pageEdit)
			r.Post("/pages/{id}", s.pageUpdate)
			r.Post("/pages/{id}/delete", s.pageDelete)
			r.Post("/pages/{id}/discard", s.pageDiscard)
			r.Get("/pages/{id}/preview", s.pagePreview)

			// JSON API for the in-place editor.
			r.Get("/api/pages", s.apiListPages)
			r.Post("/api/pages", s.apiCreatePage)
			r.Delete("/api/pages/{id}", s.apiDeletePage)
			r.Get("/api/menu", s.apiGetMenu)
			r.Put("/api/menu", s.apiSaveMenu)
			r.Post("/api/pages/{id}/regions", s.apiSaveRegions)
			r.Post("/api/pages/{id}/sections", s.apiSaveSections)
			r.Post("/api/pages/{id}/publish", s.apiPublish)
			r.Post("/api/pages/{id}/discard", s.apiDiscard)
			r.Get("/api/snippets", s.apiSnippetsList)

			// Per-page CSS/JS is written raw into pages: admin-only.
			r.Group(func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Get("/api/pages/{id}/code", s.apiGetPageCode)
				r.Put("/api/pages/{id}/code", s.apiSavePageCode)
			})

			// Snippet management (palette entries) is admin-only.
			r.Group(func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Get("/snippets", s.snippetsList)
				r.Get("/snippets/new", s.snippetNew)
				r.Post("/snippets/new", s.snippetCreate)
				r.Get("/snippets/{id}", s.snippetEdit)
				r.Post("/snippets/{id}", s.snippetUpdate)
				r.Post("/snippets/{id}/delete", s.snippetDelete)
			})
		}

		if d.Media != nil {
			r.Get("/media", s.mediaList)
			r.Post("/media/upload", s.mediaUpload)
			r.Post("/media/{id}/alt", s.mediaUpdateAlt)
			r.Post("/media/{id}/delete", s.mediaDelete)
			r.Post("/media/{id}/move", s.mediaMove)
			r.Post("/media/folders/new", s.mediaFolderCreate)
			r.Post("/media/folders/{id}/delete", s.mediaFolderDelete)

			r.Get("/api/media", s.apiMediaList)
			r.Post("/api/media", s.apiMediaUpload)
			r.Get("/api/media/folders", s.apiFoldersList)
			r.Post("/api/media/folders", s.apiFolderCreate)
		}

		// Host-registered sections, behind the same session, CSRF, and
		// login middleware as everything else in this group.
		for _, sec := range d.Sections {
			r.Mount(SectionPathPrefix+"/"+sec.Path, s.sectionHandler(sec))
		}

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
	pages := []string{"login", "dashboard", "users", "user_form", "pages", "page_form", "media", "snippets", "snippet_form", "custom"}
	m := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		t, err := template.ParseFS(templateFS,
			"templates/layout.gohtml", "templates/"+page+".gohtml")
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

	// Captcha is set when login CAPTCHA is configured; the login page
	// embeds the Cap widget with it.
	Captcha *captchaInfo

	// RememberHours labels the login page's "Remember me" checkbox.
	RememberHours int

	// User management pages.
	Users      []auth.User
	FormUser   *auth.User
	FormErrors map[string]string
	IsNew      bool

	// Page management pages.
	PagesEnabled  bool
	Pages         []content.Page
	FormPage      *content.Page
	PageTemplates []render.PageTemplate
	Regions       []render.Region
	BlockContent  map[string]string // draft content keyed by region name
	HasDraftEdits bool              // draft blocks differ from the published set

	// Media pages.
	MediaEnabled bool
	Media        []media.View // images
	Documents    []media.View // files (PDFs, office docs, ...)
	Folders      []media.Folder
	MediaQuery   string // active search filter
	MediaFolder  string // active folder filter ("", "root", or an id)

	// Snippet pages.
	Snippets       []snippets.Snippet // admin-created
	ConfigSnippets []snippets.Snippet // registered in code
	FormSnippet    *snippets.Snippet

	// Host-registered sections: nav links visible to this user, and the
	// page content when rendering a custom page through RenderPage.
	NavSections []navLink
	Title       string
	Body        template.HTML
}

// navLink is one host-registered section's entry in the admin top bar.
type navLink struct {
	URL   string
	Label string
}

// captchaInfo is what the login template needs to embed the Cap widget.
type captchaInfo struct {
	ScriptURL string // widget script, served by the Cap server
	Endpoint  string // data-cap-api-endpoint value
}

func (s *server) newTemplateData(r *http.Request) templateData {
	td := templateData{
		AdminPath:    s.deps.AdminPath,
		User:         s.currentUser(r),
		CSRFToken:    s.deps.Sessions.GetString(r.Context(), sessionKeyCSRF),
		Flash:        s.deps.Sessions.PopString(r.Context(), sessionKeyFlash),
		PagesEnabled: s.deps.Renderer != nil,
		MediaEnabled: s.deps.Media != nil,

		RememberHours: int(s.deps.RememberFor.Round(time.Hour) / time.Hour),
	}
	if s.deps.Captcha != nil {
		td.Captcha = &captchaInfo{
			ScriptURL: s.deps.Captcha.ScriptURL(),
			Endpoint:  s.deps.Captcha.WidgetEndpoint(),
		}
	}
	td.NavSections = navSectionsFor(s.deps.Sections, s.deps.AdminPath, td.IsAdmin())
	return td
}

// navSectionsFor returns the top-bar links for the host-registered sections
// a user with the given role may see.
func navSectionsFor(sections []Section, adminPath string, isAdmin bool) []navLink {
	var links []navLink
	for _, sec := range sections {
		if sec.NavLabel == "" || (sec.AdminOnly && !isAdmin) {
			continue
		}
		links = append(links, navLink{
			URL:   adminPath + SectionPathPrefix + "/" + sec.Path + "/",
			Label: sec.NavLabel,
		})
	}
	return links
}

// IsAdmin reports whether the logged-in user has the admin role; used by
// templates to show admin-only fields.
func (td templateData) IsAdmin() bool {
	return td.User != nil && td.User.Role == auth.RoleAdmin
}

// TemplateLabel returns the human label for a page template file, for the
// pages list.
func (td templateData) TemplateLabel(file string) string {
	for _, pt := range td.PageTemplates {
		if pt.File == file {
			return pt.Label
		}
	}
	return file
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
