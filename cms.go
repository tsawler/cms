// Package cms is an embeddable content management system for Go web
// applications. The host application supplies a database pool (Postgres,
// MySQL, or MariaDB) and its
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
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/redis/go-redis/v9"
	"github.com/tsawler/cms/admin"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/captcha"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/editor"
	"github.com/tsawler/cms/internal/dialect"
	"github.com/tsawler/cms/internal/redisstore"
	"github.com/tsawler/cms/internal/sessiondata"
	"github.com/tsawler/cms/internal/sessionstore"
	"github.com/tsawler/cms/internal/sqldb"
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

// MediaAdoptMode controls rebuilding the media library from the object
// store; see media.AdoptMode and Config.MediaAdopt.
type MediaAdoptMode = media.AdoptMode

// Media adoption modes, aliasing the media package's; see Config.MediaAdopt.
const (
	// MediaAdoptWhenEmpty rebuilds the library from the bucket when the
	// database holds no media. It is the zero value, and so the default.
	MediaAdoptWhenEmpty = media.AdoptWhenEmpty
	// MediaAdoptOff never reads the bucket's manifests.
	MediaAdoptOff = media.AdoptOff
	// MediaAdoptReconcile adopts anything the database is missing on every
	// startup, not only the first.
	MediaAdoptReconcile = media.AdoptReconcile
)

// RedisConfig locates a Redis server for session storage; see Config.Redis.
type RedisConfig struct {
	// Addr is the server address, host:port, e.g. "localhost:6379".
	Addr string

	// Password authenticates to the server. Leave empty when the server
	// has no AUTH configured.
	Password string

	// DB is the logical database number. The default, 0, is right unless
	// the instance is shared and sessions should live in their own
	// database.
	DB int
}

// EditorStyle is one entry in the in-place editor's Styles menu; see
// render.EditorStyle.
type EditorStyle = render.EditorStyle

// Snippet is one pre-written HTML block for the editor's palette; see
// snippets.Snippet.
type Snippet = snippets.Snippet

// SectionStyles is the curated set of section backgrounds, widths, and
// corner roundings; see render.SectionStyles.
type SectionStyles = render.SectionStyles

// SectionOption is one background, width, or corner choice; see
// render.SectionOption.
type SectionOption = render.SectionOption

// CaptchaConfig locates a self-hosted Cap CAPTCHA server for the admin
// login form; see captcha.Config.
type CaptchaConfig = captcha.Config

// AdminSection is one host-registered admin page, mounted inside the admin
// area's login, session, and CSRF middleware; see admin.Section. Handlers
// use admin.UserFrom, admin.CSRFToken, admin.SetFlash, and admin.RenderPage
// to integrate with the admin chrome.
type AdminSection = admin.Section

// DashboardCard puts a card for an admin section on the admin dashboard;
// see admin.DashboardCard and AdminSection.Dashboard.
type DashboardCard = admin.DashboardCard

// PermissionDef declares a deployment-specific permission; see
// admin.PermissionDef and Config.Permissions.
type PermissionDef = admin.PermissionDef

// Mailer sends the messages the CMS itself originates; see admin.Mailer
// and Config.Mailer.
type Mailer = admin.Mailer

// Config holds everything the host application provides to the CMS.
type Config struct {
	// DB is the database connection pool. Required. All CMS tables are
	// prefixed cms_, so the pool may point at a database shared with the
	// host application.
	//
	// Open it with a driver matching Dialect: "pgx" (from
	// github.com/jackc/pgx/v5/stdlib) for Postgres, "mysql" (from
	// github.com/go-sql-driver/mysql) for MySQL or MariaDB. A host that
	// already holds a *pgxpool.Pool can convert it with
	// stdlib.OpenDBFromPool.
	DB *sql.DB

	// Dialect selects the SQL the CMS generates: "postgres" (the default),
	// or "mysql" for both MySQL 8.0.31+ and MariaDB 10.6+. It must match the
	// driver DB was opened with — database/sql does not expose that, so it
	// cannot be detected.
	//
	// MySQL DSNs must set parseTime=true so timestamps scan into time.Time;
	// the CMS sets the session time zone to UTC itself.
	Dialect string

	// Locales lists the content locales the site supports, e.g.
	// []string{"en", "fr"}. The first entry is the default. Defaults to
	// []string{"en"}.
	Locales []string

	// AdminPath is the URL prefix the host application mounts Admin()
	// under, used when the admin UI generates links. Defaults to "/admin".
	AdminPath string

	// SiteURL is the site's canonical public base URL, e.g.
	// "https://example.com" — scheme and host, no trailing slash and no
	// path. It is what the CMS uses wherever a link has to work outside
	// this request: the media library's "Copy link" buttons, RSS feed
	// links, and hreflang alternates.
	//
	// Optional. When empty each request's own scheme and Host header are
	// used instead, which is right for development and for a site served
	// under one name. Set it when that guess would be wrong — behind a
	// proxy that rewrites Host, or when the admin is reached by a
	// different name than the public site.
	//
	// A value with no scheme is assumed to be https.
	SiteURL string

	// SessionLifetime is how long a login session lasts. Defaults to 24h.
	SessionLifetime time.Duration

	// RememberFor is how long a session lasts when the user ticks
	// "Remember me" at login: the cookie survives browser restarts and
	// the session deadline is extended to this duration. Without the
	// tick, the cookie dies when the browser closes (SessionLifetime
	// still bounds it server-side). Defaults to 30 days.
	RememberFor time.Duration

	// SecureCookies marks the session cookie Secure so it is only sent
	// over HTTPS. Enable in production; leave off for local development
	// over plain HTTP.
	SecureCookies bool

	// Redis moves session storage from the cms_sessions table to Redis.
	// When set, sessions live under cms_session: keys — the prefix keeps
	// them distinct in an instance shared with the host application — and
	// Redis's own key expiry replaces the hourly cleanup sweep. When nil,
	// the default, sessions stay in the database.
	//
	// Nothing else moves: users, content, and settings remain in DB, so
	// Redis here is purely a performance/locality choice for the
	// per-request session lookup. Set it from the environment with
	// CMS_SESSION_REDIS_ADDR; see ConfigFromEnv. Like DB, the connection
	// is not dialed or verified by New; a wrong address surfaces on the
	// first request that touches a session.
	Redis *RedisConfig

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
	// An entry with Unlisted set is offered only to superadmins when
	// creating a page — for one-off templates that back a single page.
	PageTemplates []PageTemplate

	// PostsPerPage is how many posts a paginated listing shows on one
	// page — {{cmsFeed "blog"}} and the ?page= links it builds. Zero, the
	// default, uses render.DefaultPostsPerPage (10); negative values are
	// invalid. A listing template can override it per feed with
	// {{cmsFeed "blog" 6}}, and {{cmsPosts}} ignores it entirely, being
	// the unpaginated "newest N" func. Set it from the environment with
	// CMS_POSTS_PER_PAGE; see ConfigFromEnv.
	PostsPerPage int

	// AdminPerPage is how many rows a paginated admin list shows on one
	// page — Blog & News, and Pages. Zero, the default, uses
	// admin.DefaultPerPage (25); negative values are invalid. Set it from
	// the environment with CMS_ADMIN_PER_PAGE; see ConfigFromEnv.
	//
	// It is deliberately separate from PostsPerPage: that one sizes the
	// public listing, where the number is a design decision about the
	// site, and an editor's table wants far more rows than a blog page
	// does. Tuning one must not disturb the other.
	AdminPerPage int

	// AdminMaxRequestBytes caps the body of an unsafe admin request from a
	// signed-in user. Zero, the default, is sized from the media manager's
	// limits; negative values are invalid.
	//
	// Raise it when the admin accepts an upload larger than that — a
	// host section taking many files in one multipart post is the usual
	// reason, since the cap covers the whole body rather than any one
	// file. Requests carrying no session are held to a much smaller fixed
	// ceiling whatever this says, so raising it does not widen what a
	// signed-out caller can send.
	AdminMaxRequestBytes int64

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

	// MediaAdopt controls whether Migrate rebuilds the media library from
	// the bucket. Every upload writes a manifest describing it, so a
	// bucket carries everything needed to recreate the cms_media rows —
	// filenames, alt text, folders, dimensions and all.
	//
	// The zero value, MediaAdoptWhenEmpty, adopts a bucket's media when
	// the database has none: point a fresh deployment at a bucket that
	// already holds content and the library comes back. That is the case
	// worth having for disaster recovery, and for a staging environment
	// pointed at a copy of production's bucket. Set MediaAdoptReconcile to
	// check on every startup instead, or MediaAdoptOff to never look.
	//
	// Adoption only ever inserts. It will not delete rows whose objects
	// have gone, because every way listing a bucket can fail looks exactly
	// like that.
	MediaAdopt MediaAdoptMode

	// TemplateFuncs are the host application's own template functions,
	// callable from page templates alongside the cms* funcs. Register as
	// many as the site needs; the usual reason is to reach data the CMS
	// does not own — a product catalogue, a vehicle table — from inside a
	// CMS-managed page:
	//
	//	TemplateFuncs: template.FuncMap{
	//	    "featuredVehicles": func(n int) []Vehicle { ... },
	//	    "vehicleCount":     func() int { ... },
	//	}
	//
	// and then, in a page template:
	//
	//	{{range featuredVehicles 3}}<h3>{{.Name}}</h3>{{end}}
	//
	// Templates parse against this map, so it must name every function
	// they call. Names inside the reserved cms* namespace are refused, so
	// a later CMS release can add funcs without breaking a host.
	//
	// The implementations here are used as-is unless RequestFuncs
	// replaces them per request — which is what a function that queries a
	// database usually wants, for the request's context. A function
	// registered only here is shared by every render and must be safe for
	// concurrent use.
	//
	// These functions run inside the page template with the host's full
	// trust: one returning template.HTML bypasses the editor's content
	// sanitizer entirely, so never interpolate untrusted input into it.
	TemplateFuncs template.FuncMap

	// RequestFuncs binds TemplateFuncs to a request. It is called once
	// per page render, and whatever it returns replaces the matching
	// entries in TemplateFuncs for that render alone; names it omits keep
	// their declared implementation. This is where a function gets the
	// request's context for a query it must not outlive, or the URL it
	// needs to read a query parameter:
	//
	//	RequestFuncs: func(r *http.Request) template.FuncMap {
	//	    return template.FuncMap{
	//	        "featuredVehicles": func(n int) []Vehicle {
	//	            return store.Featured(r.Context(), n)
	//	        },
	//	    }
	//	}
	//
	// Only names TemplateFuncs declared can be called — page templates
	// were parsed against that set — so this needs TemplateFuncs to be
	// set, and New refuses the combination that isn't.
	RequestFuncs func(*http.Request) template.FuncMap

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

	// SectionStyles are the curated background, width, and rounded-corner
	// options for sections regions ({{cmsSections "name"}}). Nil gets the
	// Tailwind-first defaults (render.DefaultSectionStyles); a config with
	// a nil Corners list gets the default corner options (an empty non-nil
	// slice ships none and hides the setting). The classes need
	// safelisting like editor styles do.
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

	// Mailer, when set, delivers the email the CMS itself originates —
	// today only the password reset the login page's "Forgot your
	// password?" flow sends. The CMS authors the message (subject and
	// both bodies); the host supplies only the transport, which is where
	// delivery policy — SMTP or an API, which From address, a
	// development mail sink — already lives.
	//
	// Nil turns the feature off entirely: the login page shows no
	// forgot-password link and the reset routes answer 404. That is the
	// honest failure mode — a reset form that could never send its link
	// would look broken rather than be off. A host that wants the flow
	// without real delivery (development, tests) can pass a Mailer that
	// logs.
	Mailer Mailer

	// AdminSections are deployment-specific admin pages: each is an
	// http.Handler mounted at {AdminPath}/x/{Path}, behind the admin's
	// login, session, and CSRF middleware, with an optional link in the
	// admin nav. The /x/ namespace guarantees no collision with built-in
	// admin routes, now or after upgrades.
	AdminSections []AdminSection

	// Permissions declares deployment-specific permissions beyond the
	// built-ins (blogs, news, pages, users), for functionality the host
	// gates itself with auth.User.Can — e.g. in-place editing of its own
	// records. Each appears as a grant checkbox on the admin's user form.
	// An AdminSection with a Permission set is declared automatically
	// (labelled with its NavLabel); list it here only to override the
	// label. Grants live in the cms_user_permissions table; admin and
	// superadmin roles hold every permission implicitly.
	Permissions []PermissionDef

	// Logger receives operational log output. Defaults to slog.Default().
	Logger *slog.Logger
}

// pageViewRetentionDays is how long the public site's daily page-view
// counters are kept before Migrate prunes them: a season of history for
// a dashboard that charts a week, at a negligible storage cost.
const pageViewRetentionDays = 90

// CMS is the root object of the module. Create one with New.
type CMS struct {
	cfg Config
	// db wraps cfg.DB with the configured dialect; everything inside the
	// CMS talks to this rather than to cfg.DB directly.
	db       *sqldb.DB
	sessions *scs.SessionManager
	users    *auth.Store
	content  *content.Store
	renderer *render.Renderer
	// code is the custom-code library: the markup-and-JavaScript blocks
	// pages reference by key rather than store inline. See
	// snippets.CodeSnippet.
	code       *snippets.CodeStore
	media      *media.Manager
	objects    media.ObjectStore
	admin      http.Handler
	cssBuilder *cssRebuilder // nil unless Config.Tailwind is set
	cssOnce    sync.Once     // schedules the initial build on first traffic

	// The public-facing settings, cached together: every public response
	// consults the site mode, including the media and asset routes that
	// read no settings otherwise, and the rest come from the one read.
	// See siteFlags.
	siteMu  sync.Mutex
	siteVal siteFacts
	siteAt  time.Time

	// The rendered sitemap, cached: it costs a query over every page, and
	// nothing stops a client from asking for it in a loop. See
	// serveSitemap.
	sitemapMu   sync.Mutex
	sitemapBody []byte
	sitemapBase string // the base URL sitemapBody was built for
	sitemapAt   time.Time
}

// New validates cfg, applies defaults, and returns a ready CMS. It does not
// touch the database; call Migrate before serving requests.
func New(cfg Config) (*CMS, error) {
	if cfg.DB == nil {
		return nil, errors.New("cms: Config.DB is required")
	}
	d := dialect.For(cfg.Dialect)
	if d == nil {
		return nil, fmt.Errorf("cms: unknown Config.Dialect %q (want \"postgres\" or \"mysql\")", cfg.Dialect)
	}
	db := sqldb.New(cfg.DB, d)
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
	cfg.SiteURL = normalizeSiteURL(cfg.SiteURL)
	if cfg.SessionLifetime <= 0 {
		cfg.SessionLifetime = 24 * time.Hour
	}
	if cfg.RememberFor <= 0 {
		cfg.RememberFor = 30 * 24 * time.Hour
	}
	if cfg.PostsPerPage < 0 {
		return nil, fmt.Errorf("cms: PostsPerPage must not be negative, got %d", cfg.PostsPerPage)
	}
	if cfg.PostsPerPage == 0 {
		cfg.PostsPerPage = render.DefaultPostsPerPage
	}
	if cfg.AdminPerPage < 0 {
		return nil, fmt.Errorf("cms: AdminPerPage must not be negative, got %d", cfg.AdminPerPage)
	}
	if cfg.AdminPerPage == 0 {
		cfg.AdminPerPage = admin.DefaultPerPage
	}
	if cfg.AdminMaxRequestBytes < 0 {
		return nil, fmt.Errorf("cms: AdminMaxRequestBytes must not be negative, got %d", cfg.AdminMaxRequestBytes)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.EditorStyles == nil {
		cfg.EditorStyles = render.DefaultEditorStyles()
	}
	if cfg.Snippets == nil {
		cfg.Snippets = append(snippets.DefaultSnippets(), snippets.LibrarySnippets()...)
		cfg.Snippets = append(cfg.Snippets, snippets.DefaultSectionPresets()...)
		cfg.Snippets = append(cfg.Snippets, snippets.LibrarySectionPresets()...)
	}
	if cfg.SectionStyles == nil {
		cfg.SectionStyles = render.DefaultSectionStyles()
	} else if cfg.SectionStyles.Corners == nil {
		// Hosts configured before corner rounding existed keep getting
		// the default choices; an empty non-nil slice opts out.
		cfg.SectionStyles.Corners = render.DefaultSectionStyles().Corners
	}
	// Paddings is deliberately NOT backfilled the way Corners is. Corner
	// rounding sets a property nothing else claims, so handing it to a
	// host that never asked is free. Vertical spacing is the opposite:
	// the arrangement that predates the axis is a py-* inside each width
	// preset, and appending a default py-12 to those would fight the
	// spacing the host already chose. A host with its own SectionStyles
	// opts in by declaring Paddings and taking the py-* out of Widths.
	// Host template funcs: reject reserved names here rather than at the
	// first render, and refuse RequestFuncs on its own — templates parse
	// against TemplateFuncs, so a name declared only per request is one
	// no template could ever have compiled a call to.
	if err := render.ValidateFuncNames(cfg.TemplateFuncs); err != nil {
		return nil, err
	}
	if cfg.RequestFuncs != nil && len(cfg.TemplateFuncs) == 0 {
		return nil, fmt.Errorf("cms: RequestFuncs needs TemplateFuncs to declare the function names")
	}
	if err := admin.ValidateSections(cfg.AdminSections); err != nil {
		return nil, err
	}
	permissions, err := collectPermissions(cfg.Permissions, cfg.AdminSections)
	if err != nil {
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
	if cfg.Redis != nil {
		if cfg.Redis.Addr == "" {
			return nil, errors.New("cms: Config.Redis.Addr is required when Redis is set")
		}
		sessions.Store = redisstore.New(redis.NewClient(&redis.Options{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		}))
	} else {
		sessions.Store = sessionstore.New(db)
	}
	sessions.Lifetime = cfg.SessionLifetime
	sessions.Cookie.Name = "cms_session"
	// Session cookies by default; ticking "Remember me" at login makes
	// that session's cookie persistent (scs's RememberMe mechanism).
	sessions.Cookie.Persist = false
	sessions.Cookie.HttpOnly = true
	sessions.Cookie.SameSite = http.SameSiteLaxMode
	sessions.Cookie.Secure = cfg.SecureCookies

	users := auth.NewStore(db)
	users.SetLogger(cfg.Logger)
	contentStore := content.NewStore(db, cfg.Locales[0])
	codeStore := snippets.NewCodeStore(db)

	var renderer *render.Renderer
	if cfg.TemplateFS != nil {
		var hidden []render.PageTemplate
		if cfg.PostTemplate.File != "" {
			hidden = append(hidden, cfg.PostTemplate)
		}
		var err error
		renderer, err = render.NewWithFuncs(cfg.TemplateFS, cfg.SharedTemplates,
			cfg.PageTemplates, cfg.SectionStyles, cfg.TemplateFuncs, hidden...)
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
		mediaManager = media.NewManager(db, objects, cfg.Logger)
		if cfg.MediaWebPQuality != 0 {
			mediaManager.SetWebPQuality(cfg.MediaWebPQuality)
		}
		if cfg.MediaMaxVideoMB != 0 {
			mediaManager.SetMaxVideoBytes(int64(cfg.MediaMaxVideoMB) << 20)
		}
	}

	c := &CMS{
		cfg:      cfg,
		db:       db,
		sessions: sessions,
		users:    users,
		content:  contentStore,
		renderer: renderer,
		code:     codeStore,
		media:    mediaManager,
		objects:  objects,
	}
	if cfg.Tailwind != nil {
		c.cssBuilder = &cssRebuilder{c: c}
	}
	c.admin = admin.New(admin.Deps{
		// Nil-safe: schedule is a no-op when Tailwind isn't configured.
		ContentChanged: c.cssBuilder.schedule,
		// The same briefly-cached reading the public side stamps its
		// responses from, so the sidebar and the site cannot disagree
		// about which mode the site is in.
		SiteDevelopment: c.developmentMode,
		Sessions:        sessions,
		Users:           users,
		Content:         contentStore,
		Renderer:        renderer,
		RequestFuncs:    cfg.RequestFuncs,
		Media:           mediaManager,
		Snippets:        snippets.NewStore(db),
		CodeSnippets:    codeStore,
		Captcha:         capClient,
		Mailer:          cfg.Mailer,
		ConfigSnippets:  cfg.Snippets,
		SectionStyles:   cfg.SectionStyles,
		PostTemplate:    cfg.PostTemplate,
		Sections:        cfg.AdminSections,
		Permissions:     permissions,
		Logger:          cfg.Logger,
		AdminPath:       cfg.AdminPath,
		SiteBaseURL:     c.siteBaseURL,
		DefaultLocale:   cfg.Locales[0],
		Locales:         cfg.Locales,
		RememberFor:     cfg.RememberFor,
		PerPage:         cfg.AdminPerPage,
		MaxRequestBytes: cfg.AdminMaxRequestBytes,
		Version:         Version(),
	})
	return c, nil
}

// collectPermissions merges the host's declared permissions with those
// implied by its admin sections, validating keys as it goes. An explicit
// Config.Permissions entry wins over a section-derived one, so a host
// can relabel a section's permission; the built-ins cannot be
// redeclared — their behavior is the CMS's, not the host's.
func collectPermissions(declared []PermissionDef, sections []AdminSection) ([]PermissionDef, error) {
	builtin := make(map[auth.Permission]bool)
	for _, p := range auth.BuiltinPermissions() {
		builtin[p] = true
	}

	var out []PermissionDef
	seen := make(map[auth.Permission]bool)
	for _, d := range declared {
		if !auth.ValidPermissionKey(string(d.Key)) {
			return nil, fmt.Errorf("cms: permission key %q must be a lowercase letter followed by lowercase letters, digits, hyphens, or underscores", d.Key)
		}
		if builtin[d.Key] {
			return nil, fmt.Errorf("cms: permission %q is built in and cannot be redeclared", d.Key)
		}
		if seen[d.Key] {
			return nil, fmt.Errorf("cms: permission %q declared twice", d.Key)
		}
		seen[d.Key] = true
		if d.Label == "" {
			d.Label = string(d.Key)
		}
		out = append(out, d)
	}

	// Section keys were already format-checked by ValidateSections. A
	// section may reuse a built-in permission; only new keys are declared.
	// Whether the grant binds admins (AdminsNeedGrant) must agree across
	// every declaration of one key: the flag decides who a checkbox
	// applies to, and two answers would resolve by declaration order —
	// which is to say unpredictably — so a mismatch is refused instead.
	gated := make(map[auth.Permission]bool)
	for _, d := range out {
		gated[d.Key] = d.AdminsNeedGrant
	}
	for _, sec := range sections {
		key := auth.Permission(sec.Permission)
		if sec.Permission == "" || builtin[key] {
			continue
		}
		if was, ok := gated[key]; ok && was != sec.AdminsNeedGrant {
			return nil, fmt.Errorf("cms: permission %q is declared both with and without AdminsNeedGrant", key)
		}
		gated[key] = sec.AdminsNeedGrant
		if seen[key] {
			continue
		}
		seen[key] = true
		label := sec.NavLabel
		if label == "" {
			label = sec.Path
		}
		out = append(out, PermissionDef{Key: key, Label: label, AdminsNeedGrant: sec.AdminsNeedGrant})
	}
	return out, nil
}

// Migrate creates or upgrades the CMS's database schema, performs any
// configured one-time object-store setup (S3Config.ApplyPublicReadPolicy),
// and rebuilds the media library from the bucket when Config.MediaAdopt
// calls for it.
//
// It is safe to call on every startup and safe to call from multiple
// instances concurrently: advisory locks serialize the schema work and the
// adoption, the bucket policy is idempotent, and adoption skips media the
// database already has.
func (c *CMS) Migrate(ctx context.Context) error {
	if err := migrations.Run(ctx, c.db, c.cfg.Logger); err != nil {
		return err
	}
	// Traffic counters past their retention. Startup is the one moment
	// every deployment reliably has, and a miss just waits for the next
	// one — so pruning needs no scheduler of its own.
	if err := c.content.PrunePageViews(ctx, time.Now().UTC().AddDate(0, 0, -pageViewRetentionDays)); err != nil {
		return err
	}
	// Only for the store New built from Config.S3 — a host-supplied
	// ObjectStore manages its own bucket setup.
	if c.cfg.ObjectStore == nil && c.cfg.S3 != nil && c.cfg.S3.ApplyPublicReadPolicy {
		if s, ok := c.objects.(*media.S3Store); ok {
			if err := s.ApplyPublicReadPolicy(ctx); err != nil {
				return err
			}
			c.cfg.Logger.Info("cms: applied public-read bucket policy", "bucket", c.cfg.S3.Bucket)
		}
	}
	if c.media != nil {
		res, err := c.media.Restore(ctx, c.cfg.MediaAdopt)
		if err != nil {
			return err
		}
		if res.DidWork() {
			c.cfg.Logger.Info("cms: adopted media from the object store",
				"items", res.Adopted, "folders", res.Folders, "already_present", res.Skipped,
				"missing_objects", res.Orphaned, "failed", res.Failed)
		}
	}
	return nil
}

// SeedAdmin creates an initial administrator account if and only if no users
// exist yet. It returns true if the account was created. Call it after
// Migrate; it is a no-op on every startup after the first. The account gets
// the superadmin role — it belongs to whoever set the site up, and further
// users can be created with lesser roles from the admin area.
//
// A site being set up for the first time is also put into development mode
// (see content.SiteSettings.Mode), so it is not indexed while it is being
// built, and has the generated sitemap turned on. The superadmin switches
// the mode to production when the site is ready to be found. Existing
// sites are left alone on both counts: they are already live, and having
// an upgrade quietly pull them out of search results — or claim a URL
// their own app already answers — would be a far worse surprise than
// having to flip a switch once.
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
	if err := c.content.SetSiteMode(ctx, content.ModeDevelopment); err != nil {
		return false, err
	}
	// A new site also gets the generated sitemap, for the mirror-image
	// reason: nothing of the host's can be shadowed on a site that did
	// not exist a moment ago, and a site whose every URL the CMS knows
	// may as well publish them. Existing sites stay off — see the
	// comment above SiteSettings.Sitemap.
	if err := c.content.SetSitemap(ctx, true); err != nil {
		return false, err
	}
	c.cfg.Logger.Info("cms: created initial admin user", "email", email)
	c.cfg.Logger.Info("cms: new site starts in development mode — search engines are asked to skip it until a superadmin switches it to production")
	return true, nil
}

// SeedHomePage creates and publishes a home page — the empty slug, served at
// "/" — if and only if the site has no pages or posts yet. It returns true if
// the page was created. Call it after Migrate, alongside SeedAdmin; it is a
// no-op on every startup after the first, so a home page that is later
// deleted or renamed stays that way.
//
// templateName must be one of Config.PageTemplates; empty selects the first.
// This is an argument rather than something the CMS decides because page
// templates belong to the host — the module ships none, and a page naming a
// template the host has not configured fails to render.
//
// The page is published rather than left as a draft, so that a fresh install
// serves something at "/" instead of a 404. It has a title and no content:
// the point is to give an editor somewhere to click.
func (c *CMS) SeedHomePage(ctx context.Context, templateName, title string) (bool, error) {
	if len(c.cfg.PageTemplates) == 0 {
		return false, errors.New("cms: SeedHomePage needs at least one Config.PageTemplates entry")
	}
	if templateName == "" {
		templateName = c.cfg.PageTemplates[0].File
	}
	if !slices.ContainsFunc(c.cfg.PageTemplates, func(t PageTemplate) bool {
		return t.File == templateName
	}) {
		// Better to refuse than to write a row whose page cannot render.
		return false, fmt.Errorf("cms: SeedHomePage: %q is not one of Config.PageTemplates", templateName)
	}

	pages, posts, err := c.content.Counts(ctx)
	if err != nil {
		return false, err
	}
	if pages > 0 || posts > 0 {
		return false, nil
	}

	locale := c.cfg.Locales[0]
	page := &content.Page{Slug: "", TemplateName: templateName, Title: title}
	id, err := c.content.Insert(ctx, page, locale)
	if err != nil {
		return false, err
	}
	if err := c.content.Publish(ctx, id); err != nil {
		return false, err
	}
	c.cfg.Logger.Info("cms: created initial home page", "template", templateName, "title", title)
	return true, nil
}

// Admin returns the handler for the admin area (login, dashboard, user
// management, and — in later phases — content, media, and settings). Mount
// it under Config.AdminPath with the prefix stripped:
//
//	mux.Handle("/admin/", http.StripPrefix("/admin", c.Admin()))
//
// Most hosts should use Handler instead, which does this wiring itself.
func (c *CMS) Admin() http.Handler {
	return c.admin
}

// MediaManager returns the CMS's media library — the same one the admin's
// Media section manages — for host applications that want to reference
// library items from their own data: list with All, resolve with GetByID,
// and build servable URLs with URL. Nil when no object store is
// configured, so callers must check.
//
// The manager writes only under the library's own key root; host code
// keeping its own objects elsewhere in the bucket can use both without
// the two namespaces meeting.
func (c *CMS) MediaManager() *media.Manager {
	return c.media
}

// Handler returns a single handler for the whole site: requests under
// Config.AdminPath go to the admin area (with the prefix stripped and the
// bare admin path redirected to its trailing-slash form), and everything
// else goes to Pages. Because the routing comes from AdminPath, the mount
// point and the links the admin UI generates can never disagree:
//
//	mux.Handle("/", c.Handler())
//
// Hosts that need different wiring — the admin on its own hostname, extra
// middleware on one side only — can still compose Admin and Pages
// themselves.
func (c *CMS) Handler() http.Handler {
	adminPath := c.cfg.AdminPath // normalized by New: leading slash, no trailing slash
	admin := http.StripPrefix(adminPath, c.admin)
	pages := c.Pages()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == adminPath {
			url := adminPath + "/"
			if r.URL.RawQuery != "" {
				url += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, url, http.StatusMovedPermanently)
			return
		}
		if strings.HasPrefix(r.URL.Path, adminPath+"/") {
			admin.ServeHTTP(w, r)
			return
		}
		pages.ServeHTTP(w, r)
	})
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
		// A site in development says so on everything it serves, before
		// any of the branches below decide what that is. The header is
		// what covers the responses no <meta> tag can reach: the RSS
		// feeds, and the media proxy serving images and PDFs that search
		// engines index in their own right.
		site := c.siteFlags(r.Context())
		if site.dev {
			w.Header().Set("X-Robots-Tag", robotsDirective)
		}
		if r.URL.Path == robotsPath {
			if body := c.robotsBody(r, site); body != "" {
				c.serveRobotsTxt(w, body)
				return
			}
		}
		// A site in development publishes no sitemap: it is asking not to
		// be crawled, and a list of every URL it has is the opposite of
		// that.
		if r.URL.Path == sitemapPath && site.sitemap && !site.dev {
			c.serveSitemap(w, r)
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

// robotsPath is the address crawlers look for their instructions at, and
// robotsDirective is what a site in development answers every request
// with. In production the CMS claims the path only when a superadmin has
// stored a robots.txt in the site settings: a host that serves its own —
// with a sitemap line, or rules for one crawler — keeps doing so once
// the site goes live, and an install that stores nothing is left exactly
// as it was.
const (
	robotsPath      = "/robots.txt"
	robotsDirective = "noindex, nofollow"
)

// robotsTxt is what a development site serves at /robots.txt. Disallow
// is the blunt instrument and the right one here: a site under
// construction has nothing in anyone's index yet, so keeping crawlers
// out entirely beats letting them in to read a noindex.
//
// (The two do work against each other, and it matters in one direction:
// a crawler that may not fetch a page never sees the noindex on it. On a
// site that had been live and is being pulled back out of the index, the
// disallow is what would keep the old URLs there — search for "noindex
// and disallow" before reaching for this as a takedown.)
const robotsTxt = "User-agent: *\nDisallow: /\n"

// robotsBody is what /robots.txt should answer with, or "" to leave the
// path unclaimed so the host app's own handler sees it.
//
// Development serves its own Disallow over anything stored: the stored
// file is written for the live site, and a site that is being hidden must
// not hand crawlers a file that invites them in.
func (c *CMS) robotsBody(r *http.Request, site siteFacts) string {
	if site.dev {
		return robotsTxt
	}
	if site.robots == "" {
		return ""
	}
	if !site.sitemap {
		return site.robots
	}
	return withSitemapLine(site.robots, c.siteBaseURL(r)+sitemapPath)
}

// withSitemapLine adds a Sitemap: line for the generated sitemap to a
// stored robots.txt, so turning the sitemap on is enough to advertise it.
// A file that already names a sitemap is left exactly as written — the
// author pointing at their own is the likelier intent, and robots.txt
// takes any number of Sitemap lines, so a second one would only muddy
// which is meant.
func withSitemapLine(body, url string) string {
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "sitemap:") {
			return body
		}
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body + "\nSitemap: " + url + "\n"
}

// serveRobotsTxt answers /robots.txt with body: either the development
// Disallow above or the robots.txt a superadmin stored.
func (c *CMS) serveRobotsTxt(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// No caching: the switch to production — and an edit to the stored
	// file — has to take effect when it is made, not whenever an
	// intermediary decides it is done with this. Crawlers cache
	// robots.txt on their own schedule regardless.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, body)
}

// siteModeCacheTTL is how long an instance trusts its in-memory copy of
// the site mode. It bounds two things: how long a mode switch takes to
// reach the instances that did not serve the request that made it, and
// how stale the answer can be on the routes that read no other settings.
// Short, because the whole point of the setting is the moment it changes.
const siteModeCacheTTL = 5 * time.Second

// siteFacts is the public face of the site settings: what the CMS needs
// to answer a request from anyone who is not logged in.
type siteFacts struct {
	dev     bool   // the site is in development; keep it out of indexes
	robots  string // the stored /robots.txt, "" for none
	sitemap bool   // serve a generated /sitemap.xml
}

// siteFlags reports the site facts from a briefly-cached copy of the
// settings.
//
// The mode is consulted on every public response, including media and
// asset routes that touch the database for nothing else, so it cannot be
// a query per request; the other two ride along on the same read. A read
// that fails falls back to the last known answer; with no answer yet —
// the database is unreachable at the first request — it says
// development, because a site nobody can serve pages for has nothing to
// gain from being crawled, and an unfinished site landing in an index is
// the mistake that is hard to take back.
func (c *CMS) siteFlags(ctx context.Context) siteFacts {
	if c.content == nil {
		return siteFacts{} // no store to ask; only a hand-built CMS in a test
	}
	c.siteMu.Lock()
	got, at := c.siteVal, c.siteAt
	c.siteMu.Unlock()
	if !at.IsZero() && time.Since(at) < siteModeCacheTTL {
		return got
	}
	site, err := c.content.SiteSettings(ctx)
	if err != nil {
		c.cfg.Logger.Error("cms: reading the site mode", "err", err)
		if at.IsZero() {
			got.dev = true
		}
		return got
	}
	got = siteFacts{dev: site.Development(), robots: site.RobotsTxt, sitemap: site.Sitemap}
	c.siteMu.Lock()
	c.siteVal, c.siteAt = got, time.Now()
	c.siteMu.Unlock()
	return got
}

// developmentMode reports whether the site is in development; it is what
// the renderer is handed to decide the robots <meta> tag.
func (c *CMS) developmentMode(ctx context.Context) bool {
	return c.siteFlags(ctx).dev
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
	// Edit mode is per-page, decided by the slug's permission: blog/…
	// and news/… answer to their feed, everything else to pages. A
	// logged-in user without the page's permission gets the published
	// public render, exactly like a visitor (admin roles hold every
	// permission; Can is nil-safe for the anonymous case).
	editing := user.Can(auth.PermissionForSlug(slug))

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
	// Private pages are served only to logged-in users. A 404 rather than
	// a 403, so anonymous visitors can't tell the page exists.
	if page.Visibility == content.VisibilityPrivate && user == nil {
		c.notFound(w)
		return
	}

	blockStatus := content.StatusPublished
	if editing {
		blockStatus = content.StatusDraft
	}
	// The page's own blocks and the site's shared ones ({{cmsShared}})
	// come back from one query: every page renders both.
	blocks, shared, err := c.content.EffectiveBlocksWithShared(r.Context(), page.ID, locale, blockStatus)
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
		p, err := c.content.PostByPageID(r.Context(), page.ID, locale, editing)
		switch {
		case err == nil:
			post = render.PostInfoFor(p, render.LocalePrefix(locale, c.cfg.Locales[0]), c.postImages())
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
			// Shared regions are on this page too, so saved-but-unlive
			// footer edits are unpublished changes here as much as the
			// page's own are — and Publish makes both live.
			if !hasUnpublished {
				sharedChanged, err := c.content.HasSharedUnpublishedChanges(r.Context())
				if err != nil {
					c.cfg.Logger.Error("cms: checking shared unpublished changes", "err", err)
				}
				hasUnpublished = sharedChanged
			}
		}
		edit = &render.EditInfo{
			PageID:         page.ID,
			Slug:           page.Slug,
			AdminPath:      c.cfg.AdminPath,
			CSRFToken:      csrf,
			Locale:         locale,
			Status:         string(page.Status),
			Visibility:     string(page.Visibility),
			HasUnpublished: hasUnpublished,
			MediaEnabled:   c.media != nil,
			IsAdmin:        user.Role.IsAdmin(),
			IsSuperadmin:   user.Role.IsSuperadmin(),
			CanPages:       user.Can(auth.PermPages),
			CanBlogs:       user.Can(auth.PermBlogs),
			CanNews:        user.Can(auth.PermNews),
			PostsEnabled:   c.postsEnabled(),
			Post:           post,
			Locales:        c.cfg.Locales,
			Styles:         c.cfg.EditorStyles,
			Sections:       c.cfg.SectionStyles,
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := c.renderer.Render(w, render.Input{
		Page:      page,
		Blocks:    blocks,
		Shared:    shared,
		Locale:    locale,
		Menus:     menus,
		Edit:      edit,
		Post:      post,
		Posts:     c.postLister(r.Context(), locale, editing),
		Locales:   c.cfg.Locales,
		BaseURL:   c.siteBaseURL(r),
		Site:      site,
		AdminPath: c.cfg.AdminPath,
		// Custom-code blocks: stored content holds an inert placeholder
		// naming a library entry, and a public render swaps in what the
		// entry holds. Nothing is fetched until a placeholder is found,
		// so pages without one cost no query — and an edit render never
		// expands them at all.
		CodeSnippets: c.codeLookup(r.Context()),
		// Pagination for {{cmsFeed}} listings. Every page render carries
		// it: which template paginates is the template's business, and
		// none of this costs a query until one calls the func.
		PostsPerPage: c.cfg.PostsPerPage,
		PageNumber:   listingPage(r),
		PageURL:      listingPageURL(r),
		// Host template funcs bound to this request, so a query they run
		// carries the request's context. Nil when the host registered
		// none, or none that need the request.
		Funcs: c.requestFuncs(r),
	}); err != nil {
		c.cfg.Logger.Error("cms: rendering page", "slug", slug, "err", err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}

	// One page served to an ordinary visitor is one impression, counted
	// after the response so the write never delays it. Logged-in CMS
	// users are the site's staff, not its traffic, and crawlers that
	// announce themselves aren't either. A failed write loses one count,
	// which is not worth more than a log line.
	if user == nil && r.Method == http.MethodGet && !likelyBot(r.UserAgent()) {
		if err := c.content.RecordPageView(r.Context(), time.Now().UTC(), r.URL.Path); err != nil {
			c.cfg.Logger.Error("cms: recording page view", "slug", slug, "err", err)
		}
	}
}

// likelyBot spots self-identifying crawlers by the words nearly all of
// them carry in their user agent. It is a courtesy filter for the
// dashboard's traffic chart, not bot defense: a crawler that pretends to
// be a browser is counted, and that is fine at this stakes level.
func likelyBot(ua string) bool {
	if ua == "" {
		return true
	}
	ua = strings.ToLower(ua)
	for _, marker := range []string{"bot", "crawl", "spider", "slurp"} {
		if strings.Contains(ua, marker) {
			return true
		}
	}
	return false
}

// codeLookup resolves custom-code keys for one render. It is lazy — the
// renderer calls it only for placeholders it actually finds — and
// memoized, so a page using the same block twice reads it once. A key
// that fails to load renders as nothing rather than taking the page
// down; the log says which one.
func (c *CMS) codeLookup(ctx context.Context) render.CodeLookup {
	return c.code.Lookup(ctx, func(key string, err error) {
		c.cfg.Logger.Error("cms: loading custom code block", "key", key, "err", err)
	})
}

// requestFuncs asks the host to bind its template functions to this
// request. Nil when Config.RequestFuncs is unset, which leaves the
// renderer using the implementations Config.TemplateFuncs declared.
func (c *CMS) requestFuncs(r *http.Request) template.FuncMap {
	if c.cfg.RequestFuncs == nil {
		return nil
	}
	return c.cfg.RequestFuncs(r)
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
		if errors.Is(err, media.ErrObjectNotFound) {
			// A rendition this image predates — the ladder gained a rung
			// after it was uploaded — or one lost from the bucket. Build it
			// now and serve it; every later request is an ordinary hit.
			switch rebuildErr := c.media.EnsureRendition(r.Context(), rest); {
			case rebuildErr == nil:
				body, contentType, err = c.objects.Get(r.Context(), key)
			case errors.Is(rebuildErr, media.ErrNoRendition):
				// Nothing to build: the 404 below is the right answer.
			default:
				c.cfg.Logger.Error("cms: rebuilding media rendition", "key", key, "err", rebuildErr)
			}
		}
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
	if strings.HasPrefix(contentType, "image/svg") {
		// Viewed directly, an SVG is a document on this origin; block any
		// scripting the upload-time scan might have missed. Inline styles
		// stay allowed so the graphic still renders fully.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
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
