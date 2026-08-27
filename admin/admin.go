// Package admin serves the CMS admin area: login, dashboard, and user
// management, with content, media, and settings arriving in later phases.
// All UI assets are embedded; the package has no external runtime files.
package admin

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
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

// Mailer sends the messages the CMS itself originates — today only the
// password reset email. The CMS authors the content; an implementation
// supplies only delivery, so it decides transport and From address and
// nothing else. Implementations must be safe for concurrent use.
//
// htmlBody may be empty, in which case the message is plain text only.
type Mailer interface {
	Send(ctx context.Context, to, subject, textBody, htmlBody string) error
}

// Deps is everything the admin area needs from the rest of the CMS.
type Deps struct {
	Sessions *scs.SessionManager
	Users    *auth.Store
	Content  *content.Store
	Renderer *render.Renderer // nil when the host has not configured templates
	// RequestFuncs binds the host's template functions
	// (Config.TemplateFuncs) to a request, for the page and post
	// previews. Previews run the same page templates the public site
	// does, so a function that reads the request's context has to be
	// bound here too or it behaves subtly differently under preview than
	// in front of a visitor. Nil when the host registered none.
	RequestFuncs func(*http.Request) template.FuncMap
	Media        *media.Manager // nil when the host has not configured an object store
	Snippets     *snippets.Store
	// CodeSnippets is the custom-code library — the markup-and-JavaScript
	// blocks pages reference by key. Nil leaves the editor's code API
	// unmounted, so the drawer offers no code blocks.
	CodeSnippets   *snippets.CodeStore
	Captcha        *captcha.Client       // nil when login CAPTCHA is not configured
	Mailer         Mailer                // nil disables the forgot-password flow
	ConfigSnippets []snippets.Snippet    // host-registered palette entries
	SectionStyles  *render.SectionStyles // curated section settings
	Sections       []Section             // host-registered admin pages, already validated
	Permissions    []PermissionDef       // host-declared custom permissions, already validated
	Logger         *slog.Logger
	AdminPath      string
	DefaultLocale  string
	Locales        []string // all configured locales, [0] = DefaultLocale

	// Stylesheets are extra stylesheet URLs the layout links on every
	// admin page, after the admin's own. See Config.AdminStylesheets.
	Stylesheets []string

	// Version is the CMS release the layout stamps in the footer of every
	// admin page (the host passes cms.Version()). Empty hides the footer,
	// which is what tests and direct package use get.
	Version string

	// PostTemplate is the template blog and news posts render with; the
	// zero value disables the Blog & News admin.
	PostTemplate render.PageTemplate

	// RememberFor is how long a "Remember me" login persists. The zero
	// value falls back to 30 days so a partially-populated Deps (tests,
	// direct package use) behaves sensibly.
	RememberFor time.Duration

	// PerPage is how many rows a paginated admin list shows on one page.
	// The zero value falls back to DefaultPerPage, like RememberFor.
	PerPage int

	// MaxRequestBytes caps the body of an unsafe request from a signed-in
	// user. It is enforced by the CSRF middleware, which is the first
	// thing to read the body and therefore the only place a cap can still
	// take effect — see readToken.
	//
	// Raise it above the largest upload the admin accepts, remembering
	// that a multipart post carries every field at once: a form taking
	// forty 32 MB photos needs more than 32 MB here. The zero value is
	// sized from the media manager's own limits, or DefaultMaxRequestBytes
	// when there is none. Requests with no session user are held to a much
	// smaller fixed ceiling regardless of this value.
	MaxRequestBytes int64

	// SiteBaseURL returns the site's absolute public base
	// ("scheme://host", no trailing slash) for the given request, so the
	// admin can offer links that work when pasted somewhere else. Nil
	// leaves such links site-relative, which is fine for tests and direct
	// package use.
	SiteBaseURL func(*http.Request) string

	// SiteDevelopment reports whether the site is in development mode
	// (content.SiteSettings.Mode), which the sidebar stamps so nobody
	// forgets a finished site is still hidden from search engines. Nil
	// leaves the stamp off, which is what tests and direct package use
	// want; the CMS supplies its own cached reader.
	SiteDevelopment func(context.Context) bool

	// SiteLocked reports whether the site is closed to everyone but
	// superadmins (content.SiteSettings.Locked). Two things depend on
	// it: the sidebar stamps every admin page so nobody forgets the site
	// is down, and login and the session check refuse anyone who is not
	// a superadmin — a locked site that still let its editors in would
	// be hiding the site from the public alone, which is not what the
	// switch says. Nil reads as never locked, which is what tests and
	// direct package use want; the CMS supplies its own cached reader.
	//
	// Enforcing it on the public site is a separate door, and the
	// host's to mount: see (*cms.CMS).Lockdown.
	SiteLocked func(context.Context) bool

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

// hostFuncs binds the host's template functions to a preview request, or
// returns nil so the renderer keeps the implementations the host declared.
func (s *server) hostFuncs(r *http.Request) template.FuncMap {
	if s.deps.RequestFuncs == nil {
		return nil
	}
	return s.deps.RequestFuncs(r)
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

	// The module's pagination styles, served as a real stylesheet rather
	// than inlined in the layout: the admin's CSP carries no
	// style-src 'unsafe-inline', so an inline <style> would be dropped by
	// the browser. Registered before the embedded static tree so this
	// literal path wins over its wildcard.
	r.Get(pagerCSSPath, servePagerCSS)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServerFS(static)))

	r.Get("/login", s.loginForm)
	r.Post("/login", s.login)
	// The two-factor code challenge, between password and session: its
	// visitor is authenticated-in-part, so it lives outside requireUser.
	r.Get("/login/2fa", s.twoFactorForm)
	r.Post("/login/2fa", s.twoFactorSubmit)

	// The forgot-password flow exists only when the host supplied a
	// Mailer — see the Mailer interface for why absent means off.
	if d.Mailer != nil {
		r.Get("/forgot-password", s.forgotForm)
		r.Post("/forgot-password", s.forgotRequest)
		r.Get("/reset-password", s.resetForm)
		r.Post("/reset-password", s.resetSubmit)
	}

	r.Group(func(r chi.Router) {
		r.Use(s.requireUser)
		r.Get("/", s.dashboard)
		r.Post("/logout", s.logout)
		r.Post("/lang", s.setLang)
		// Ends a superadmin's masquerade (started from the users list).
		// Behind requireUser alone: the session is signed in as the
		// masqueraded user, who is usually not a superadmin.
		r.Post("/masquerade/exit", s.masqueradeExit)

		// Every user's own account page: profile, password, two-factor.
		r.Get("/settings", s.settingsForm)
		r.Post("/settings/profile", s.settingsProfile)
		r.Post("/settings/password", s.settingsPassword)
		r.Post("/settings/2fa/setup", s.totpSetup)
		r.Post("/settings/2fa/confirm", s.totpConfirm)
		r.Post("/settings/2fa/cancel", s.totpCancel)
		r.Post("/settings/2fa/disable", s.totpDisable)

		if d.Renderer != nil {
			// The Pages section is superadmin-only, like snippets:
			// editors and admins do all their page work in place on
			// the public site, and a second door to the same features
			// only confuses. The pages permission lives on in the
			// editor API below, where it gates pages, menus, and site
			// settings.
			r.Group(func(r chi.Router) {
				r.Use(s.requireSuperadmin)
				r.Get("/pages", s.pagesList)
				r.Get("/pages/new", s.pageNew)
				r.Post("/pages/new", s.pageCreate)
				r.Get("/pages/{id}", s.pageEdit)
				r.Post("/pages/{id}", s.pageUpdate)
				r.Post("/pages/{id}/delete", s.pageDelete)
				r.Post("/pages/{id}/discard", s.pageDiscard)
				r.Post("/pages/{id}/unpublish", s.pageUnpublish)
				r.Get("/pages/{id}/preview", s.pagePreview)
			})

			// JSON API for the in-place editor. Reads stay open to
			// every logged-in user (the post editor's link dialog
			// lists pages), and the {id} mutations are gated inside
			// the handlers by the page's slug — a post's backing
			// page answers to its feed permission, not to pages.
			r.Get("/api/pages", s.apiListPages)
			r.With(s.requirePerm(auth.PermPages)).Post("/api/pages", s.apiCreatePage)
			r.Delete("/api/pages/{id}", s.apiDeletePage)
			r.Get("/api/menu", s.apiGetMenu)
			r.With(s.requirePerm(auth.PermPages)).Put("/api/menu", s.apiSaveMenu)
			r.Get("/api/settings", s.apiGetSettings)
			r.With(s.requirePerm(auth.PermPages)).Put("/api/settings", s.apiSaveSettings)
			r.Post("/api/pages/{id}/regions", s.apiSaveRegions)
			r.Post("/api/pages/{id}/sections", s.apiSaveSections)
			r.Get("/api/pages/{id}/meta", s.apiGetPageMeta)
			r.Put("/api/pages/{id}/meta", s.apiSavePageMeta)
			r.Post("/api/pages/{id}/revert-locale", s.apiRevertLocale)
			r.Post("/api/pages/{id}/duplicate", s.apiDuplicatePage)
			r.Post("/api/pages/{id}/publish", s.apiPublish)
			r.Post("/api/pages/{id}/unpublish", s.apiUnpublish)
			r.Post("/api/pages/{id}/discard", s.apiDiscard)
			r.Put("/api/pages/{id}/visibility", s.apiSetVisibility)
			r.Get("/api/snippets", s.apiSnippetsList)

			// Per-page CSS/JS is written raw into pages: admin-only.
			// So is the custom-code library, for the same reason — a
			// page's placeholder only names an entry, and this is the
			// door to what the entry actually runs.
			r.Group(func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Get("/api/pages/{id}/code", s.apiGetPageCode)
				r.Put("/api/pages/{id}/code", s.apiSavePageCode)
				if d.CodeSnippets != nil {
					r.Get("/api/code", s.apiCodeList)
					r.Post("/api/code", s.apiCodeCreate)
					r.Get("/api/code/{key}", s.apiCodeGet)
					r.Put("/api/code/{key}", s.apiCodeSave)
					r.Delete("/api/code/{key}", s.apiCodeDelete)
				}
			})

			// Blog & news, when a post template is configured. Either
			// feed permission opens the section; which feeds a user
			// can actually touch is enforced per-post in the handlers.
			if d.PostTemplate.File != "" {
				r.Group(func(r chi.Router) {
					r.Use(s.requireAnyPerm(auth.PermBlogs, auth.PermNews))
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
				})
			}

			// Snippet management (palette entries) is superadmin-only:
			// snippets are raw HTML injected into every editor.
			r.Group(func(r chi.Router) {
				r.Use(s.requireSuperadmin)
				r.Get("/snippets", s.snippetsList)
				r.Get("/snippets/new", s.snippetNew)
				r.Post("/snippets/new", s.snippetCreate)
				r.Get("/snippets/{id}", s.snippetEdit)
				r.Post("/snippets/{id}", s.snippetUpdate)
				r.Post("/snippets/{id}/delete", s.snippetDelete)
			})
		}

		// The media library, for anyone who edits something that carries
		// media — see mediaPermissions. Gated as a group rather than per
		// route: the picker's read endpoints matter as much as the bulk
		// delete, since a library listing is a list of what the site holds.
		if d.Media != nil {
			r.Group(func(r chi.Router) {
				r.Use(s.requireMedia)

				r.Get("/media", s.mediaList)
				r.Get("/media/{id}/download", s.mediaDownload)
				r.Post("/media/upload", s.mediaUpload)
				r.Post("/media/{id}/alt", s.mediaUpdateAlt)
				r.Post("/media/{id}/rename", s.mediaRename)
				r.Post("/media/{id}/delete", s.mediaDelete)
				r.Post("/media/{id}/move", s.mediaMove)
				r.Post("/media/bulk/move", s.mediaBulkMove)
				r.Post("/media/bulk/delete", s.mediaBulkDelete)
				r.Post("/media/folders/new", s.mediaFolderCreate)
				r.Post("/media/folders/{id}/delete", s.mediaFolderDelete)

				r.Get("/api/media", s.apiMediaList)
				r.Post("/api/media", s.apiMediaUpload)
				r.Post("/api/media/{id}/poster", s.apiMediaSetPoster)
				r.Get("/api/media/folders", s.apiFoldersList)
				r.Post("/api/media/folders", s.apiFolderCreate)
			})
		}

		// Host-registered sections, behind the same session, CSRF, and
		// login middleware as everything else in this group.
		for _, sec := range d.Sections {
			r.Mount(SectionPathPrefix+"/"+sec.Path, s.sectionHandler(sec))
		}

		r.Group(func(r chi.Router) {
			r.Use(s.requirePerm(auth.PermUsers))
			r.Get("/users", s.usersList)
			r.Get("/users/new", s.userNew)
			r.Post("/users/new", s.userCreate)
			r.Get("/users/{id}", s.userEdit)
			r.Post("/users/{id}", s.userUpdate)
			r.Post("/users/{id}/delete", s.userDelete)
			r.With(s.requireSuperadmin).Post("/users/{id}/masquerade", s.userMasquerade)
		})
	})

	return r
}

// parseTemplates builds one template set per page, each combining the shared
// layout with that page's {{define "content"}} block.
func parseTemplates() map[string]*template.Template {
	pages := []string{"login", "login_2fa", "forgot_password", "reset_password", "dashboard", "settings", "users", "user_form", "pages", "page_form", "posts", "post_form", "media", "snippets", "snippet_form", "custom"}
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

	// MasqueradeFrom is the superadmin a masquerading session belongs
	// to — the account the layout's banner switches back to. nil when
	// the session is its owner's own.
	MasqueradeFrom *auth.User

	// PageScript names an extra script in static/ that this page needs
	// (e.g. "media.js"), loaded after admin.js. Empty on pages whose
	// behavior the shared admin.js already covers.
	PageScript string
	// PageWide drops the main column's reading width for pages that are
	// browsers rather than documents.
	PageWide bool

	// Stylesheets are the host's extra admin stylesheets, linked after
	// the admin's own so their rules win at equal specificity.
	Stylesheets []string

	// PagerCSSPath is where the layout links the shared pagination
	// stylesheet from, relative to AdminPath. A field rather than a
	// template constant because only Go knows the route.
	PagerCSSPath string

	// Version is Deps.Version, for the footer; empty hides it.
	Version string

	// SiteBase is the site's absolute public base for this request; see
	// Abs. Empty when Deps.SiteBaseURL is unset, which leaves links
	// site-relative.
	SiteBase string

	// AdminLang is the admin UI language for this request ("en" or "fr");
	// templates translate their strings through T with it. LangToggle is
	// the language the topbar toggle switches to — empty when the site has
	// no French locale configured, which hides the toggle.
	AdminLang  string
	LangToggle string

	// Captcha is set when login CAPTCHA is configured; the login page
	// embeds the Cap widget with it.
	Captcha *captchaInfo

	// The forgot-password flow. ForgotEnabled shows the login page's
	// link (true when a Mailer is configured); the rest carry one
	// request's state through the two reset templates.
	ForgotEnabled bool
	ResetSent     bool   // forgot form: the confirmation state
	ResetToken    string // reset form: the live token, echoed into the form
	ResetInvalid  bool   // reset form: the dead-link state

	// RememberDays or RememberHours labels the login page's "Remember
	// me" checkbox: days when the duration is a whole number of days
	// (at least two, so the plural reads right), hours otherwise.
	RememberDays  int
	RememberHours int

	// User management pages. Users carries a per-row CanManage so the
	// table offers Edit exactly where the route allows it.
	Users      []userRow
	FormUser   *auth.User
	FormErrors map[string]string
	IsNew      bool
	// Permissions lists the host-declared custom permissions, for the
	// user form's grant checkboxes (built-ins are rendered literally).
	Permissions []PermissionDef

	// The account settings page. TwoFactorEnabled is the logged-in
	// user's current state; TOTPSecret and TOTPQR carry an enrollment in
	// progress — the pending secret (spaced for manual entry) and the
	// same secret as a scannable QR data URI. template.URL because
	// html/template would otherwise refuse a data: image.
	TwoFactorEnabled bool
	TOTPSecret       string
	TOTPQR           template.URL

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
	// Pager is the paging state of a list page, nil on pages that show
	// everything. Templates render it with {{.Pager.HTML}} — the same bar
	// the public site's {{cmsPagination}} draws.
	Pager *render.Pager
	// Small preview URL for the post form's image picker, resolved from
	// whichever the post holds — a library image's thumbnail rendition,
	// or an external URL as it stands. "" when unset.
	FormPostThumb string

	// Media pages. MediaEnabled means the library is both configured and
	// open to this user — it drives the sidebar link, and offering one the
	// route answers with a 403 would be worse than not offering it.
	MediaEnabled bool
	Media        []media.View // images
	Videos       []media.View
	Documents    []media.View // files (PDFs, office docs, ...)
	// Entries is the active tab's bucket — what the listing actually
	// renders. The three kind slices above stay populated so the tabs can
	// show a count for each without a second query.
	Entries []media.View
	Folders []media.Folder
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

	// SiteDevelopment stamps the sidebar when the public site is in
	// development mode and so is being kept out of search engines.
	SiteDevelopment bool

	// SiteLocked stamps the sidebar when the site is closed to everyone
	// but superadmins. Louder than the development badge, because this
	// one means the public is being turned away right now.
	SiteLocked bool

	// Dashboard page only: the host sections' cards and the public
	// site's seven-day traffic chart. Filled by the dashboard handler
	// alone, so their queries run nowhere else.
	DashCards []dashCard
	Traffic   *trafficChart
}

// Abs makes a site-relative URL absolute against the site's public base,
// for links meant to be pasted somewhere else — "Copy link" produces
// something that works in an email, not just in this tab.
//
// URLs that are already absolute come back untouched: with an S3
// PublicBaseURL or a public bucket, media already lives on its own
// domain, and prefixing that would only break it.
func (td templateData) Abs(u string) string {
	if u == "" || td.SiteBase == "" ||
		strings.Contains(u, "://") || strings.HasPrefix(u, "//") {
		return u
	}
	if !strings.HasPrefix(u, "/") {
		return td.SiteBase + "/" + u
	}
	return td.SiteBase + u
}

// navLink is one host-registered section's entry in the admin sidebar.
// Confirm carries the section's Confirm message; non-empty means the
// link asks (via the shared dialog) before the browser follows it.
// After is the section's NavAfter anchor; the layout groups links by it.
// HasCount marks a link whose section supplies a NavCount, so the
// layout gives it the built-in entries' leader line and number; Count
// is filled per render by fillNavCounts.
type navLink struct {
	URL      string
	Label    string
	Confirm  string
	After    string
	Active   bool
	HasCount bool
	Count    int

	count func(context.Context) (int, error)
}

// NavSectionsAt returns the host-registered nav links anchored after the
// named built-in sidebar entry; "" is the default group, rendered after
// the built-in entries. The layout calls it once per anchor position.
func (td templateData) NavSectionsAt(anchor string) []navLink {
	var links []navLink
	for _, l := range td.NavSections {
		if l.After == anchor {
			links = append(links, l)
		}
	}
	return links
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

	// WasmURL and PakoURL redirect the widget's two CDN dependencies at
	// copies the CSP admits: the solver's WebAssembly binary (served by the
	// Cap server) and the pako library (vendored into the admin's static
	// assets). Nonce is the CSP nonce for the inline <script> that sets
	// them, and which the widget reuses for its own instrumentation frame.
	WasmURL string
	PakoURL string
	Nonce   string
}

func (s *server) newTemplateData(r *http.Request) templateData {
	td := templateData{
		AdminPath:     s.deps.AdminPath,
		User:          s.currentUser(r),
		CSRFToken:     s.deps.Sessions.GetString(r.Context(), sessionKeyCSRF),
		Flash:         s.deps.Sessions.PopString(r.Context(), sessionKeyFlash),
		PagesEnabled:  s.deps.Renderer != nil,
		PostsEnabled:  s.deps.Renderer != nil && s.deps.PostTemplate.File != "",
		MediaEnabled:  s.deps.Media != nil && s.canUseMedia(r),
		ForgotEnabled: s.deps.Mailer != nil,
		Locales:       s.deps.Locales,
		EditLocale:    s.deps.DefaultLocale,
		PagerCSSPath:  pagerCSSPath,
		Stylesheets:   s.deps.Stylesheets,
		Version:       s.deps.Version,
	}
	if s.deps.SiteBaseURL != nil {
		td.SiteBase = s.deps.SiteBaseURL(r)
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
			WasmURL:   s.deps.Captcha.WasmURL(),
			PakoURL:   s.deps.AdminPath + captcha.PakoPath,
			Nonce:     scriptNonce(r),
		}
	}
	td.NavSections = navSectionsFor(s.deps.Sections, s.deps.AdminPath, td.User, r.URL.Path)
	// Outside the block below, unlike the development stamp: the login
	// page has no user and is exactly where this has to show — someone
	// standing at a door that will not open for them should be told so
	// before they type, not after.
	td.SiteLocked = s.siteLocked(r.Context())
	if td.User != nil {
		td.NavCounts = s.navCounts(r, td.User)
		s.fillNavCounts(r.Context(), td.NavSections)
		td.NavCurrent = navCurrent(r.URL.Path)
		if s.deps.SiteDevelopment != nil {
			td.SiteDevelopment = s.deps.SiteDevelopment(r.Context())
		}
		// A masquerading session banners the way back on every page. A
		// vanished owner leaves the banner off; masqueradeExit deals with
		// that state when asked.
		if fromID := s.deps.Sessions.GetInt64(r.Context(), sessionKeyMasqueradeFrom); fromID != 0 && fromID != td.User.ID {
			from, err := s.deps.Users.GetByID(r.Context(), fromID)
			if err == nil {
				td.MasqueradeFrom = from
			} else if !errors.Is(err, auth.ErrNotFound) {
				s.deps.Logger.Error("cms admin: loading masquerade owner", "err", err)
			}
		}
	}
	// Inside a host section, the mount prefix has been stripped, so the
	// path-based matching above is looking at section-relative paths: "/"
	// reads as the dashboard, and no section link matches. The section's
	// browser-facing base is in the context, and it is the truth.
	if base := SectionPath(r); base != "" {
		td.NavCurrent = ""
		for i := range td.NavSections {
			td.NavSections[i].Active = td.NavSections[i].URL == base
		}
	}
	return td
}

// navSectionsFor returns the sidebar links for the host-registered sections
// the user may see (nil on the login page: no sections). reqPath is the
// request's path within the admin, for marking the section being viewed.
func navSectionsFor(sections []Section, adminPath string, u *auth.User, reqPath string) []navLink {
	var links []navLink
	for _, sec := range sections {
		if sec.NavLabel == "" || !sectionVisibleTo(sec, u) {
			continue
		}
		prefix := SectionPathPrefix + "/" + sec.Path
		links = append(links, navLink{
			URL:      adminPath + prefix + "/",
			Label:    sec.NavLabel,
			Confirm:  sec.Confirm,
			After:    sec.NavAfter,
			Active:   reqPath == prefix || strings.HasPrefix(reqPath, prefix+"/"),
			HasCount: sec.NavCount != nil,
			count:    sec.NavCount,
		})
	}
	return links
}

// sectionVisibleTo reports whether the user may see the section — the one
// answer behind both its nav link and its dashboard card. It mirrors what
// sectionHandler enforces: SuperadminOnly first, then AdminOnly, then
// Permission, which reads as an explicit grant when AdminsNeedGrant is
// set. Can and HasGrant are nil-safe, so the login page's nil user simply
// sees nothing gated.
func sectionVisibleTo(sec Section, u *auth.User) bool {
	if sec.SuperadminOnly && (u == nil || !u.Role.IsSuperadmin()) {
		return false
	}
	if sec.AdminOnly && (u == nil || !u.Role.IsAdmin()) {
		return false
	}
	if sec.Permission != "" {
		if sec.AdminsNeedGrant {
			return u.HasGrant(auth.Permission(sec.Permission))
		}
		return u.Can(auth.Permission(sec.Permission))
	}
	return true
}

// fillNavCounts runs each visible section link's NavCount and stores the
// result on the link, mirroring the built-in counts' contract: a failed
// count is logged and rendered as zero rather than failing the page.
// Only called with a logged-in user — the sidebar doesn't render without
// one, so the login page never runs host queries.
func (s *server) fillNavCounts(ctx context.Context, links []navLink) {
	for i := range links {
		if links[i].count == nil {
			continue
		}
		n, err := links[i].count(ctx)
		if err != nil {
			s.deps.Logger.Error("cms admin: counting section items", "section", links[i].Label, "err", err)
			continue
		}
		links[i].Count = n
	}
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

// navCounts gathers the sidebar's table-of-contents numbers, skipping
// areas the user's permissions hide from the nav. A failed count is
// logged and rendered as zero rather than failing the page.
func (s *server) navCounts(r *http.Request, u *auth.User) navCounts {
	ctx := r.Context()
	var c navCounts
	if s.deps.Renderer != nil && s.deps.Content != nil {
		var err error
		if c.Pages, c.Posts, err = s.deps.Content.Counts(ctx); err != nil {
			s.deps.Logger.Error("cms admin: counting pages and posts", "err", err)
		}
	}
	if s.deps.Renderer != nil && s.deps.Snippets != nil && u != nil && u.Role.IsSuperadmin() {
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
	if u.Can(auth.PermUsers) && s.deps.Users != nil {
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

// IsSuperadmin reports whether the logged-in user has the superadmin
// role; used by templates for superadmin-only areas (snippets).
func (td templateData) IsSuperadmin() bool {
	return td.User != nil && td.User.Role.IsSuperadmin()
}

// Can reports whether the logged-in user holds the permission; used by
// templates to decide which navigation entries and controls to show.
func (td templateData) Can(perm string) bool {
	return td.User.Can(auth.Permission(perm))
}

// GrantChecked reports whether the user form's grant checkbox for key
// should render ticked — the form user (not the viewer) holds it.
func (td templateData) GrantChecked(key string) bool {
	return td.FormUser != nil && slices.Contains(td.FormUser.Permissions, auth.Permission(key))
}

// MayGrant reports whether the viewer may change the grant checkbox for
// a declared permission — the render-side face of mergeGrants: what they
// hold they may give, and for a grant that binds admins too, holding
// means the explicit grant, so an admin switched out of a section finds
// the box disabled rather than a tick that silently does not stick.
func (td templateData) MayGrant(d PermissionDef) bool {
	if d.AdminsNeedGrant {
		return td.User.HasGrant(d.Key)
	}
	return td.User.Can(d.Key)
}

// IsDefaultLocale reports whether the form is on its default-locale tab,
// where the locale-independent fields (address, template, feed, ...) are
// editable.
func (td templateData) IsDefaultLocale() bool {
	return len(td.Locales) == 0 || td.EditLocale == td.Locales[0]
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
