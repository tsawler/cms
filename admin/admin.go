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
	"strings"
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
	Locales        []string // all configured locales, [0] = DefaultLocale

	// PostTemplate is the template blog and news posts render with; the
	// zero value disables the Blog & News admin.
	PostTemplate render.PageTemplate

	// RememberFor is how long a "Remember me" login persists. The zero
	// value falls back to 30 days so a partially-populated Deps (tests,
	// direct package use) behaves sensibly.
	RememberFor time.Duration

	// ContentChanged, when set, is called (without waiting) after any
	// mutation that can change which CSS classes stored content uses:
	// region/section saves, publish/discard, page create/delete, and
	// snippet changes. The CMS uses it to rebuild the generated
	// Tailwind stylesheet.
	ContentChanged func()
}

// requestLocale validates a submitted locale code against the configured
// list, falling back to the default for anything unknown or empty.
func (s *server) requestLocale(l string) string {
	for _, code := range s.deps.Locales {
		if l == code {
			return l
		}
	}
	return s.deps.DefaultLocale
}

// formLocale is requestLocale over the request's "locale" query or form
// value — how the admin forms and preview select their editing locale.
func (s *server) formLocale(r *http.Request) string {
	return s.requestLocale(r.FormValue("locale"))
}

// localeSlugCollision reports whether a page slug's first segment matches
// a configured non-default locale code, which would make the page
// unreachable (the URL prefix wins).
func (s *server) localeSlugCollision(slug string) bool {
	first, _, _ := strings.Cut(slug, "/")
	for _, code := range s.deps.Locales[1:] {
		if first == code {
			return true
		}
	}
	return false
}

// contentChanged notifies the host that stored content changed; safe to
// call whether or not a listener is configured.
func (s *server) contentChanged() {
	if s.deps.ContentChanged != nil {
		s.deps.ContentChanged()
	}
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
		d.RememberFor = 30 * 24 * time.Hour
	}
	if len(d.Locales) == 0 {
		if d.DefaultLocale == "" {
			d.DefaultLocale = "en"
		}
		d.Locales = []string{d.DefaultLocale}
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
		r.Post("/lang", s.setLang)

		if d.Renderer != nil {
			r.Get("/pages", s.pagesList)
			r.Get("/pages/new", s.pageNew)
			r.Post("/pages/new", s.pageCreate)
			r.Get("/pages/{id}", s.pageEdit)
			r.Post("/pages/{id}", s.pageUpdate)
			r.Post("/pages/{id}/delete", s.pageDelete)
			r.Post("/pages/{id}/discard", s.pageDiscard)
			r.Post("/pages/{id}/unpublish", s.pageUnpublish)
			r.Get("/pages/{id}/preview", s.pagePreview)

			// JSON API for the in-place editor.
			r.Get("/api/pages", s.apiListPages)
			r.Post("/api/pages", s.apiCreatePage)
			r.Delete("/api/pages/{id}", s.apiDeletePage)
			r.Get("/api/menu", s.apiGetMenu)
			r.Put("/api/menu", s.apiSaveMenu)
			r.Get("/api/settings", s.apiGetSettings)
			r.Put("/api/settings", s.apiSaveSettings)
			r.Post("/api/pages/{id}/regions", s.apiSaveRegions)
			r.Post("/api/pages/{id}/sections", s.apiSaveSections)
			r.Post("/api/pages/{id}/revert-locale", s.apiRevertLocale)
			r.Post("/api/pages/{id}/duplicate", s.apiDuplicatePage)
			r.Post("/api/pages/{id}/publish", s.apiPublish)
			r.Post("/api/pages/{id}/unpublish", s.apiUnpublish)
			r.Post("/api/pages/{id}/discard", s.apiDiscard)
			r.Put("/api/pages/{id}/visibility", s.apiSetVisibility)
			r.Get("/api/snippets", s.apiSnippetsList)

			// Per-page CSS/JS is written raw into pages: admin-only.
			r.Group(func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Get("/api/pages/{id}/code", s.apiGetPageCode)
				r.Put("/api/pages/{id}/code", s.apiSavePageCode)
			})

			// Blog & news, when a post template is configured.
			if d.PostTemplate.File != "" {
				r.Get("/posts", s.postsList)
				r.Get("/posts/new", s.postNew)
				r.Post("/posts/new", s.postCreate)
				r.Get("/posts/{id}", s.postEdit)
				r.Post("/posts/{id}", s.postUpdate)
				r.Post("/posts/{id}/delete", s.postDelete)
				r.Post("/posts/{id}/discard", s.postDiscard)
				r.Post("/posts/{id}/unpublish", s.postUnpublish)
				r.Get("/posts/{id}/preview", s.postPreview)
				r.Post("/api/posts", s.apiCreatePost)
				r.Put("/api/posts/{id}", s.apiUpdatePostSettings)
			}

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
	pages := []string{"login", "dashboard", "users", "user_form", "pages", "page_form", "posts", "post_form", "media", "snippets", "snippet_form", "custom"}
	m := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		t, err := template.ParseFS(templateFS,
			"templates/layout.gohtml", "templates/form_regions.gohtml", "templates/"+page+".gohtml")
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

	// AdminLang is the admin UI language for this request ("en" or "fr");
	// templates translate their strings through T with it. LangToggle is
	// the language the topbar toggle switches to — empty when the site has
	// no French locale configured, which hides the toggle.
	AdminLang  string
	LangToggle string

	// Captcha is set when login CAPTCHA is configured; the login page
	// embeds the Cap widget with it.
	Captcha *captchaInfo

	// RememberDays or RememberHours labels the login page's "Remember
	// me" checkbox: days when the duration is a whole number of days
	// (at least two, so the plural reads right), hours otherwise.
	RememberDays  int
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
	// RegionsTemplate names the template whose regions the form_regions
	// partial edits (the page's template, or the post template).
	RegionsTemplate string
	// EditLocale is the locale tab the page/post form is editing;
	// Locales lists every configured locale ([0] = default). Tabs render
	// only when there is more than one.
	EditLocale string
	Locales    []string

	// Blog & news pages.
	PostsEnabled bool
	Posts        []content.Post
	FormPost     *content.Post
	FeedFilter   string // active feed filter on the posts list ("", "blog", "news")

	// Media pages.
	MediaEnabled bool
	Media        []media.View // images
	Videos       []media.View
	Documents    []media.View // files (PDFs, office docs, ...)
	Folders      []media.Folder
	// CurrentFolder is the folder being browsed, nil at the root (which
	// lists unfiled items and the folder tiles) and during a search.
	CurrentFolder *media.Folder
	MediaQuery    string // active search filter
	MediaFolder   string // active folder filter ("", "root", or an id)
	MediaTab      string // active media tab ("images", "documents", "videos")
	MediaKind     string // the tab's media kind ("image", "file", "video")
	MaxVideoMB    int64  // video upload cap, for the upload hint

	// Snippet pages.
	Snippets       []snippets.Snippet // admin-created
	ConfigSnippets []snippets.Snippet // registered in code
	FormSnippet    *snippets.Snippet
	SectionStyles  *render.SectionStyles // preset options on the snippet form

	// Host-registered sections: nav links visible to this user, and the
	// page content when rendering a custom page through RenderPage.
	NavSections []navLink
	Title       string
	Body        template.HTML

	// NavCounts holds the item totals the sidebar's contents list shows
	// beside each section; zero-valued when nobody is logged in.
	// NavCurrent names the built-in nav entry the request falls under
	// ("dashboard", "pages", ... — empty on host-registered section
	// pages, which carry their own Active flag).
	NavCounts  navCounts
	NavCurrent string
}

// navLink is one host-registered section's entry in the admin sidebar.
type navLink struct {
	URL    string
	Label  string
	Active bool
}

// navCounts is the sidebar's table-of-contents numbers: how many items
// each admin section currently holds.
type navCounts struct {
	Pages, Posts, Media, Snippets, Users int
}

// captchaInfo is what the login template needs to embed the Cap widget.
type captchaInfo struct {
	ScriptURL string // widget script, served by the Cap server
	Endpoint  string // data-cap-api-endpoint value
	Visible   bool   // show the checkbox widget instead of solving invisibly
}

func (s *server) newTemplateData(r *http.Request) templateData {
	td := templateData{
		AdminPath:    s.deps.AdminPath,
		User:         s.currentUser(r),
		CSRFToken:    s.deps.Sessions.GetString(r.Context(), sessionKeyCSRF),
		Flash:        s.deps.Sessions.PopString(r.Context(), sessionKeyFlash),
		PagesEnabled: s.deps.Renderer != nil,
		PostsEnabled: s.deps.Renderer != nil && s.deps.PostTemplate.File != "",
		MediaEnabled: s.deps.Media != nil,
		Locales:      s.deps.Locales,
		EditLocale:   s.deps.DefaultLocale,
	}
	hours := int(s.deps.RememberFor.Round(time.Hour) / time.Hour)
	if hours >= 48 && hours%24 == 0 {
		td.RememberDays = hours / 24
	} else {
		td.RememberHours = hours
	}
	td.AdminLang = s.adminLang(r)
	if s.frEnabled() {
		if td.AdminLang == "fr" {
			td.LangToggle = "en"
		} else {
			td.LangToggle = "fr"
		}
	}
	if s.deps.Captcha != nil {
		td.Captcha = &captchaInfo{
			ScriptURL: s.deps.Captcha.ScriptURL(),
			Endpoint:  s.deps.Captcha.WidgetEndpoint(),
			Visible:   s.deps.Captcha.Visible(),
		}
	}
	td.NavSections = navSectionsFor(s.deps.Sections, s.deps.AdminPath, td.IsAdmin(), r.URL.Path)
	if td.User != nil {
		td.NavCounts = s.navCounts(r, td.IsAdmin())
		td.NavCurrent = navCurrent(r.URL.Path)
	}
	return td
}

// navSectionsFor returns the sidebar links for the host-registered sections
// a user with the given role may see. reqPath is the request's path within
// the admin, for marking the section being viewed.
func navSectionsFor(sections []Section, adminPath string, isAdmin bool, reqPath string) []navLink {
	var links []navLink
	for _, sec := range sections {
		if sec.NavLabel == "" || (sec.AdminOnly && !isAdmin) {
			continue
		}
		prefix := SectionPathPrefix + "/" + sec.Path
		links = append(links, navLink{
			URL:    adminPath + prefix + "/",
			Label:  sec.NavLabel,
			Active: reqPath == prefix || strings.HasPrefix(reqPath, prefix+"/"),
		})
	}
	return links
}

// navCurrent names the built-in sidebar entry a request path (within the
// admin) falls under. Host-registered sections are matched per-link in
// navSectionsFor instead.
func navCurrent(path string) string {
	seg := strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	switch seg {
	case "":
		return "dashboard"
	case "pages", "posts", "media", "snippets", "users":
		return seg
	}
	return ""
}

// navCounts gathers the sidebar's table-of-contents numbers. A failed
// count is logged and rendered as zero rather than failing the page.
func (s *server) navCounts(r *http.Request, isAdmin bool) navCounts {
	ctx := r.Context()
	var c navCounts
	if s.deps.Renderer != nil && s.deps.Content != nil {
		var err error
		if c.Pages, c.Posts, err = s.deps.Content.Counts(ctx); err != nil {
			s.deps.Logger.Error("cms admin: counting pages and posts", "err", err)
		}
	}
	if s.deps.Renderer != nil && s.deps.Snippets != nil {
		c.Snippets = len(s.deps.ConfigSnippets)
		if n, err := s.deps.Snippets.Count(ctx); err != nil {
			s.deps.Logger.Error("cms admin: counting snippets", "err", err)
		} else {
			c.Snippets += n
		}
	}
	if s.deps.Media != nil {
		var err error
		if c.Media, err = s.deps.Media.Count(ctx); err != nil {
			s.deps.Logger.Error("cms admin: counting media", "err", err)
		}
	}
	if isAdmin && s.deps.Users != nil {
		var err error
		if c.Users, err = s.deps.Users.Count(ctx); err != nil {
			s.deps.Logger.Error("cms admin: counting users", "err", err)
		}
	}
	return c
}

// IsAdmin reports whether the logged-in user has admin powers (admin or
// superadmin); used by templates to show admin-only fields.
func (td templateData) IsAdmin() bool {
	return td.User != nil && td.User.Role.IsAdmin()
}

// IsDefaultLocale reports whether the form is on its default-locale tab,
// where the locale-independent fields (address, template, feed, ...) are
// editable.
func (td templateData) IsDefaultLocale() bool {
	return len(td.Locales) == 0 || td.EditLocale == td.Locales[0]
}

// MediaContains reports whether url is one of the listed media items' web
// URLs. The post form's image selects use it to keep a stored URL that
// isn't in the library (an original variant, or a URL set by code) from
// being silently wiped on the next save.
func (td templateData) MediaContains(url string) bool {
	for _, m := range td.Media {
		if m.WebURL == url {
			return true
		}
	}
	return false
}

// PostSlugTail is the post form's editable address portion: the backing
// page's slug with the feed prefix removed.
func (td templateData) PostSlugTail() string {
	if td.FormPost == nil {
		return ""
	}
	return strings.TrimPrefix(td.FormPost.Slug, string(td.FormPost.Feed)+"/")
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
