// Package cms is an embeddable content management system for Go web
// applications. The host application supplies a pgx connection pool and its
// own page templates; the CMS supplies an admin area, authentication,
// content storage, and (in later phases) in-place editing, media handling,
// blog/news, and localization.
//
// Typical use:
//
//	c, err := cms.New(cms.Config{DB: pool})
//	if err != nil { ... }
//	if err := c.Migrate(ctx); err != nil { ... }
//
//	mux.Handle("/admin/", http.StripPrefix("/admin", c.Admin()))
//	mux.Handle("/", c.Pages())
package cms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tsawler/cms/admin"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/captcha"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/editor"
	"github.com/tsawler/cms/internal/sessiondata"
	"github.com/tsawler/cms/internal/sessionstore"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/migrations"
	"github.com/tsawler/cms/render"
	"github.com/tsawler/cms/snippets"
)

// PageTemplate is one template the host application offers for pages; see
// render.PageTemplate.
type PageTemplate = render.PageTemplate

// S3Config configures the S3-compatible object store for uploads; see
// media.S3Config.
type S3Config = media.S3Config

// EditorStyle is one entry in the in-place editor's Styles menu; see
// render.EditorStyle.
type EditorStyle = render.EditorStyle

// Snippet is one pre-written HTML block for the editor's palette; see
// snippets.Snippet.
type Snippet = snippets.Snippet

// SectionStyles is the curated set of section backgrounds and widths; see
// render.SectionStyles.
type SectionStyles = render.SectionStyles

// SectionOption is one background or width choice; see render.SectionOption.
type SectionOption = render.SectionOption

// CaptchaConfig locates a self-hosted Cap CAPTCHA server for the admin
// login form; see captcha.Config.
type CaptchaConfig = captcha.Config

// AdminSection is one host-registered admin page, mounted inside the admin
// area's login, session, and CSRF middleware; see admin.Section. Handlers
// use admin.UserFrom, admin.CSRFToken, admin.SetFlash, and admin.RenderPage
// to integrate with the admin chrome.
type AdminSection = admin.Section

// Config holds everything the host application provides to the CMS.
type Config struct {
	// DB is the Postgres connection pool. Required. All CMS tables are
	// prefixed cms_, so the pool may point at a database shared with the
	// host application.
	DB *pgxpool.Pool

	// Locales lists the content locales the site supports, e.g.
	// []string{"en", "fr"}. The first entry is the default. Defaults to
	// []string{"en"}.
	Locales []string

	// AdminPath is the URL prefix the host application mounts Admin()
	// under, used when the admin UI generates links. Defaults to "/admin".
	AdminPath string

	// SessionLifetime is how long a login session lasts. Defaults to 24h.
	SessionLifetime time.Duration

	// RememberFor is how long a session lasts when the user ticks
	// "Remember me" at login: the cookie survives browser restarts and
	// the session deadline is extended to this duration. Without the
	// tick, the cookie dies when the browser closes (SessionLifetime
	// still bounds it server-side). Defaults to 24h.
	RememberFor time.Duration

	// SecureCookies marks the session cookie Secure so it is only sent
	// over HTTPS. Enable in production; leave off for local development
	// over plain HTTP.
	SecureCookies bool

	// TemplateFS holds the host application's page templates (often an
	// embed.FS). If nil, the public Pages handler serves a placeholder.
	TemplateFS fs.FS

	// SharedTemplates are glob patterns within TemplateFS for layouts and
	// partials parsed into every page's template set, e.g.
	// []string{"templates/base.gohtml", "templates/partials/*.gohtml"}.
	SharedTemplates []string

	// PageTemplates lists the templates editors may choose for a page.
	// Each entry's File is parsed together with SharedTemplates into its
	// own set, so different pages may define the same block names.
	PageTemplates []PageTemplate

	// PostTemplate is the page template blog and news posts render with.
	// A post is an ordinary page underneath — its slug lives under blog/
	// or news/ and its body is edited in place like any page, sections
	// and snippets included — so this is parsed like a PageTemplate, but
	// offered only through the Blog & News admin, never in the page
	// template choosers. The template's dot gets a non-nil .Post
	// (render.PostInfo) carrying the post's date, author, and images,
	// and any template may list posts with {{cmsPosts "blog" 10}}. The
	// zero value disables blog & news.
	PostTemplate PageTemplate

	// S3 configures the object store for image uploads. If nil, the
	// media library is disabled.
	S3 *S3Config

	// ObjectStore overrides the S3 object store with a custom
	// implementation (e.g. local disk for development). When set, S3 is
	// ignored.
	ObjectStore media.ObjectStore

	// MediaWebPQuality is the lossy WebP quality, in (0, 1], for the web
	// and thumbnail variants of uploaded images. Zero — the default —
	// uses 0.3, tuned for fast page loads; the untouched original is
	// always stored alongside. Other values are invalid.
	MediaWebPQuality float64

	// MediaMaxVideoMB caps video uploads, in megabytes. Zero — the
	// default — allows 512 MB; negative values are invalid. Images and
	// documents have a fixed 25 MB cap. Videos are stored exactly as
	// uploaded (no transcoding), so the practical ceiling is what
	// visitors' connections can stream.
	MediaMaxVideoMB int

	// EditorStyles populates the in-place editor's Styles menu — named,
	// on-brand text styles that apply CSS classes. Nil gets the
	// Tailwind-first defaults (render.DefaultEditorStyles); an empty
	// non-nil slice disables the menu. Classes used here must exist in
	// the site's CSS — with Tailwind, safelist them, since editor
	// content lives in the database where the source scanner can't see
	// it.
	EditorStyles []EditorStyle

	// Snippets are the host application's pre-written HTML blocks for
	// the editor's palette (per-customer components, versioned with the
	// code). Admins can add more in the admin UI; the palette shows
	// both. A snippet with Settings is a section preset — a one-click
	// starting point in the "Add a section" chooser (see
	// snippets.Snippet). Nil gets the Tailwind-first defaults
	// (snippets.DefaultSnippets plus snippets.DefaultSectionPresets); an
	// empty non-nil slice ships none. Snippet classes need safelisting
	// like editor styles do.
	Snippets []Snippet

	// SectionStyles are the curated background and width options for
	// sections regions ({{cmsSections "name"}}). Nil gets the
	// Tailwind-first defaults (render.DefaultSectionStyles). The classes
	// need safelisting like editor styles do.
	SectionStyles *SectionStyles

	// Tailwind, when set, makes the CMS rebuild a supplemental
	// stylesheet whenever stored content's class set changes, by running
	// the host's Tailwind CLI over a synthetic file of those classes.
	// Pages then link the result as /cms/content-<hash>.css via
	// {{cmsHead}}, so classes typed into content (e.g. by superadmins in
	// the HTML source view) get real CSS without a site redeploy. Nil
	// disables the feature; see TailwindConfig.
	Tailwind *TailwindConfig

	// Captcha, when set, protects the admin login form with a
	// proof-of-work CAPTCHA verified against a self-hosted Cap server
	// (docker image tiago2/cap). The challenge is solved invisibly in
	// the background by default; set CaptchaConfig.Visible for Cap's
	// checkbox widget. Nil disables the CAPTCHA; the built-in login
	// throttle and honeypot still apply.
	Captcha *CaptchaConfig

	// AdminSections are deployment-specific admin pages: each is an
	// http.Handler mounted at {AdminPath}/x/{Path}, behind the admin's
	// login, session, and CSRF middleware, with an optional link in the
	// admin nav. The /x/ namespace guarantees no collision with built-in
	// admin routes, now or after upgrades.
	AdminSections []AdminSection

	// Logger receives operational log output. Defaults to slog.Default().
	Logger *slog.Logger
}

// CMS is the root object of the module. Create one with New.
type CMS struct {
	cfg        Config
	sessions   *scs.SessionManager
	users      *auth.Store
	content    *content.Store
	renderer   *render.Renderer
	media      *media.Manager
	objects    media.ObjectStore
	admin      http.Handler
	cssBuilder *cssRebuilder // nil unless Config.Tailwind is set
	cssOnce    sync.Once     // schedules the initial build on first traffic
}

// New validates cfg, applies defaults, and returns a ready CMS. It does not
// touch the database; call Migrate before serving requests.
func New(cfg Config) (*CMS, error) {
	if cfg.DB == nil {
		return nil, errors.New("cms: Config.DB is required")
	}
	if len(cfg.Locales) == 0 {
		cfg.Locales = []string{"en"}
	}
	if err := validateLocales(cfg.Locales); err != nil {
		return nil, err
	}
	if cfg.AdminPath == "" {
		cfg.AdminPath = "/admin"
	}
	cfg.AdminPath = "/" + strings.Trim(cfg.AdminPath, "/")
	if cfg.SessionLifetime <= 0 {
		cfg.SessionLifetime = 24 * time.Hour
	}
	if cfg.RememberFor <= 0 {
		cfg.RememberFor = 24 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.EditorStyles == nil {
		cfg.EditorStyles = render.DefaultEditorStyles()
	}
	if cfg.Snippets == nil {
		cfg.Snippets = append(snippets.DefaultSnippets(), snippets.DefaultSectionPresets()...)
	}
	if cfg.SectionStyles == nil {
		cfg.SectionStyles = render.DefaultSectionStyles()
	}
	if err := admin.ValidateSections(cfg.AdminSections); err != nil {
		return nil, err
	}
	if err := validateTailwindConfig(cfg.Tailwind); err != nil {
		return nil, err
	}

	var capClient *captcha.Client
	if cfg.Captcha != nil {
		var err error
		capClient, err = captcha.New(*cfg.Captcha)
		if err != nil {
			return nil, err
		}
	}

	sessions := scs.New()
	sessions.Store = sessionstore.New(cfg.DB)
	sessions.Lifetime = cfg.SessionLifetime
	sessions.Cookie.Name = "cms_session"
	// Session cookies by default; ticking "Remember me" at login makes
	// that session's cookie persistent (scs's RememberMe mechanism).
	sessions.Cookie.Persist = false
	sessions.Cookie.HttpOnly = true
	sessions.Cookie.SameSite = http.SameSiteLaxMode
	sessions.Cookie.Secure = cfg.SecureCookies

	users := auth.NewStore(cfg.DB)
	contentStore := content.NewStore(cfg.DB, cfg.Locales[0])

	var renderer *render.Renderer
	if cfg.TemplateFS != nil {
		var hidden []render.PageTemplate
		if cfg.PostTemplate.File != "" {
			hidden = append(hidden, cfg.PostTemplate)
		}
		var err error
		renderer, err = render.New(cfg.TemplateFS, cfg.SharedTemplates, cfg.PageTemplates, cfg.SectionStyles, hidden...)
		if err != nil {
			return nil, err
		}
	}

	objects := cfg.ObjectStore
	if objects == nil && cfg.S3 != nil {
		var err error
		objects, err = media.NewS3Store(*cfg.S3)
		if err != nil {
			return nil, err
		}
	}
	var mediaManager *media.Manager
	if objects != nil {
		if cfg.MediaWebPQuality < 0 || cfg.MediaWebPQuality > 1 {
			return nil, fmt.Errorf("cms: MediaWebPQuality must be in (0, 1], got %g", cfg.MediaWebPQuality)
		}
		if cfg.MediaMaxVideoMB < 0 {
			return nil, fmt.Errorf("cms: MediaMaxVideoMB must be positive, got %d", cfg.MediaMaxVideoMB)
		}
		mediaManager = media.NewManager(cfg.DB, objects, cfg.Logger)
		if cfg.MediaWebPQuality != 0 {
			mediaManager.SetWebPQuality(cfg.MediaWebPQuality)
		}
		if cfg.MediaMaxVideoMB != 0 {
			mediaManager.SetMaxVideoBytes(int64(cfg.MediaMaxVideoMB) << 20)
		}
	}

	c := &CMS{
		cfg:      cfg,
		sessions: sessions,
		users:    users,
		content:  contentStore,
		renderer: renderer,
		media:    mediaManager,
		objects:  objects,
	}
	if cfg.Tailwind != nil {
		c.cssBuilder = &cssRebuilder{c: c}
	}
	c.admin = admin.New(admin.Deps{
		// Nil-safe: schedule is a no-op when Tailwind isn't configured.
		ContentChanged: c.cssBuilder.schedule,
		Sessions:       sessions,
		Users:          users,
		Content:        contentStore,
		Renderer:       renderer,
		Media:          mediaManager,
		Snippets:       snippets.NewStore(cfg.DB),
		Captcha:        capClient,
		ConfigSnippets: cfg.Snippets,
		SectionStyles:  cfg.SectionStyles,
		PostTemplate:   cfg.PostTemplate,
		Sections:       cfg.AdminSections,
		Logger:         cfg.Logger,
		AdminPath:      cfg.AdminPath,
		DefaultLocale:  cfg.Locales[0],
		Locales:        cfg.Locales,
		RememberFor:    cfg.RememberFor,
	})
	return c, nil
}

// Migrate creates or upgrades the CMS's database schema. It is safe to call
// on every startup and safe to call from multiple instances concurrently (a
// Postgres advisory lock serializes them).
func (c *CMS) Migrate(ctx context.Context) error {
	return migrations.Run(ctx, c.cfg.DB, c.cfg.Logger)
}

// SeedAdmin creates an initial administrator account if and only if no users
// exist yet. It returns true if the account was created. Call it after
// Migrate; it is a no-op on every startup after the first. The account gets
// the superadmin role — it belongs to whoever set the site up, and further
// users can be created with lesser roles from the admin area.
func (c *CMS) SeedAdmin(ctx context.Context, email, name, password string) (bool, error) {
	n, err := c.users.Count(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return false, err
	}
	_, err = c.users.Insert(ctx, &auth.User{
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		Role:         auth.RoleSuperadmin,
		Active:       true,
	})
	if err != nil {
		return false, err
	}
	c.cfg.Logger.Info("cms: created initial admin user", "email", email)
	return true, nil
}

// Admin returns the handler for the admin area (login, dashboard, user
// management, and — in later phases — content, media, and settings). Mount
// it under Config.AdminPath with the prefix stripped:
//
//	mux.Handle("/admin/", http.StripPrefix("/admin", c.Admin()))
func (c *CMS) Admin() http.Handler {
	return c.admin
}

// Pages returns the public site handler: it looks up the page for the
// request path and renders it with the host's templates. Anonymous visitors
// get the published version; logged-in CMS users get the draft version with
// the in-place editor injected. The handler also serves proxied media and
// the editor script under /cms/. When no TemplateFS is configured it serves
// a placeholder instead.
func (c *CMS) Pages() http.Handler {
	editorAssets := editor.Handler()
	withSession := c.sessions.LoadAndSave(http.HandlerFunc(c.servePage))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		// Media and the editor script are served outside the session
		// wrapper so their responses stay cacheable.
		if c.objects != nil && strings.HasPrefix(r.URL.Path, media.ProxyPathPrefix) {
			c.serveMedia(w, r)
			return
		}
		if c.renderer == nil {
			c.placeholder(w)
			return
		}
		if strings.HasPrefix(r.URL.Path, editor.PathPrefix) {
			editorAssets.ServeHTTP(w, r)
			return
		}
		if c.cssBuilder != nil && strings.HasPrefix(r.URL.Path, contentCSSPrefix) {
			if r.URL.Path == contentCSSCurrentPath {
				c.serveContentCSSCurrent(w, r)
				return
			}
			if strings.HasSuffix(r.URL.Path, ".css") {
				c.serveContentCSS(w, r)
				return
			}
		}
		withSession.ServeHTTP(w, r)
	})
}

func (c *CMS) servePage(w http.ResponseWriter, r *http.Request) {
	if c.cssBuilder != nil {
		// First traffic covers a cold start (content changed while the
		// process was down, or the feature was just enabled)...
		c.cssOnce.Do(c.cssBuilder.schedule)
		// ...and each render refreshes the {{cmsHead}} link from the
		// database (TTL-cached), so instances that didn't run a rebuild
		// still pick it up.
		c.cssBuilder.current(r.Context())
	}
	// Locale routing: "/" serves the default locale; "/fr/..." (any
	// configured non-default locale as first path segment) serves that
	// locale with the prefix stripped before slug lookup. Locale codes
	// can't collide with pages — slug validation rejects them.
	locale, slug := c.splitLocalePath(strings.Trim(r.URL.Path, "/"))

	// /blog/rss.xml and /news/rss.xml (dots can't appear in page slugs,
	// so these paths are free). Localized feeds live under the prefix.
	if c.postsEnabled() {
		if feed, ok := strings.CutSuffix(slug, "/rss.xml"); ok && content.ValidFeed(feed) {
			c.serveFeed(w, r, content.Feed(feed), locale)
			return
		}
	}

	user := c.sessionUser(r)
	editing := user != nil

	// Editors see draft pages and draft content; the public sees only
	// what is published.
	page, err := c.content.GetBySlug(r.Context(), slug, locale, !editing)
	if errors.Is(err, content.ErrNotFound) {
		c.notFound(w)
		return
	}
	if err != nil {
		c.cfg.Logger.Error("cms: loading page", "slug", slug, "err", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}

	blockStatus := content.StatusPublished
	if editing {
		blockStatus = content.StatusDraft
	}
	blocks, err := c.content.EffectiveBlocks(r.Context(), page.ID, locale, blockStatus)
	if err != nil {
		c.cfg.Logger.Error("cms: loading blocks", "slug", slug, "err", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}

	menuItems, err := c.content.MenuItems(r.Context(), "")
	if err != nil {
		// Navigation failing shouldn't take the page down; render without.
		c.cfg.Logger.Error("cms: loading menus", "err", err)
		menuItems = nil
	}
	menus := render.BuildMenus(menuItems, page.Slug, locale, c.cfg.Locales[0], editing)

	site, err := c.content.SiteSettings(r.Context())
	if err != nil {
		// Like menus: settings failing shouldn't take the page down.
		c.cfg.Logger.Error("cms: loading site settings", "err", err)
	}

	// When the page backs a blog/news post, hand the template its .Post.
	var post *render.PostInfo
	if c.postsEnabled() && (strings.HasPrefix(slug, "blog/") || strings.HasPrefix(slug, "news/")) {
		p, err := c.content.PostByPageID(r.Context(), page.ID, locale)
		switch {
		case err == nil:
			post = render.PostInfoFor(p, render.LocalePrefix(locale, c.cfg.Locales[0]))
		case !errors.Is(err, content.ErrNotFound):
			c.cfg.Logger.Error("cms: loading post", "slug", slug, "err", err)
		}
	}

	var edit *render.EditInfo
	if editing {
		csrf, err := sessiondata.EnsureCSRF(r.Context(), c.sessions)
		if err != nil {
			c.cfg.Logger.Error("cms: generating csrf token", "err", err)
			http.Error(w, "Something went wrong.", http.StatusInternalServerError)
			return
		}
		hasUnpublished := false
		if page.Status == content.StatusPublished {
			hasUnpublished, err = c.content.HasUnpublishedChanges(r.Context(), page.ID)
			if err != nil {
				c.cfg.Logger.Error("cms: checking unpublished changes", "slug", slug, "err", err)
				hasUnpublished = false
			}
		}
		edit = &render.EditInfo{
			PageID:         page.ID,
			Slug:           page.Slug,
			AdminPath:      c.cfg.AdminPath,
			CSRFToken:      csrf,
			Locale:         locale,
			Status:         string(page.Status),
			HasUnpublished: hasUnpublished,
			MediaEnabled:   c.media != nil,
			IsAdmin:        user.Role.IsAdmin(),
			IsSuperadmin:   user.Role.IsSuperadmin(),
			PostsEnabled:   c.postsEnabled(),
			Post:           post,
			Locales:        c.cfg.Locales,
			Styles:         c.cfg.EditorStyles,
			Sections:       c.cfg.SectionStyles,
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.renderer.Render(w, render.Input{
		Page:    page,
		Blocks:  blocks,
		Locale:  locale,
		Menus:   menus,
		Edit:    edit,
		Post:    post,
		Posts:   c.postLister(r.Context(), locale, editing),
		Locales: c.cfg.Locales,
		BaseURL: siteBaseURL(r),
		Site:    site,
	}); err != nil {
		c.cfg.Logger.Error("cms: rendering page", "slug", slug, "err", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
	}
}

// sessionUser returns the logged-in, active CMS user for a public page
// request, or nil for ordinary visitors.
func (c *CMS) sessionUser(r *http.Request) *auth.User {
	id := c.sessions.GetInt64(r.Context(), sessiondata.KeyUserID)
	if id == 0 {
		return nil
	}
	u, err := c.users.GetByID(r.Context(), id)
	if err != nil || !u.Active {
		return nil
	}
	return u
}

func (c *CMS) placeholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>CMS</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 40rem; margin: 4rem auto;">
<h1>It works</h1>
<p>Configure Config.TemplateFS and Config.PageTemplates to render pages here.
The admin area is available at <a href="` + c.cfg.AdminPath + `/">` + c.cfg.AdminPath + `/</a>.</p>
</body></html>`))
}

// serveMedia streams an uploaded object from the (possibly private) bucket.
// Objects are immutable — every upload gets a fresh key — so responses are
// cached hard.
func (c *CMS) serveMedia(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, media.ProxyPathPrefix)
	if rest == "" || strings.Contains(rest, "..") {
		http.NotFound(w, r)
		return
	}
	key := c.media.KeyRoot() + rest

	// Range requests make proxied video seekable (and playable at all in
	// Safari, which probes with one). They are forwarded to the object
	// store when it supports them; other stores keep working range-less.
	ranger, canRange := c.objects.(media.RangeGetter)
	if canRange {
		w.Header().Set("Accept-Ranges", "bytes")
	}

	var (
		body                      io.ReadCloser
		contentType, contentRange string
		length                    int64
		err                       error
	)
	if rangeSpec := r.Header.Get("Range"); canRange && rangeSpec != "" {
		body, contentType, contentRange, length, err = ranger.GetRange(r.Context(), key, rangeSpec)
	} else {
		body, contentType, err = c.objects.Get(r.Context(), key)
		length = -1
	}
	if errors.Is(err, media.ErrObjectNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, media.ErrInvalidRange) {
		http.Error(w, "Requested range not satisfiable.", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if err != nil {
		c.cfg.Logger.Error("cms: serving media", "key", key, "err", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	defer body.Close()

	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if length >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	}
	if contentRange != "" {
		w.Header().Set("Content-Range", contentRange)
		w.WriteHeader(http.StatusPartialContent)
	}
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, body); err != nil {
		c.cfg.Logger.Debug("cms: streaming media interrupted", "key", key, "err", err)
	}
}

func (c *CMS) notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Page not found</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 40rem; margin: 4rem auto;">
<h1>Page not found</h1><p>The page you're looking for doesn't exist or isn't published.</p>
</body></html>`))
}
