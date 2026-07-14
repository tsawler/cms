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
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tsawler/cms/admin"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/content"
	"github.com/tsawler/cms/internal/sessionstore"
	"github.com/tsawler/cms/migrations"
	"github.com/tsawler/cms/render"
)

// PageTemplate is one template the host application offers for pages; see
// render.PageTemplate.
type PageTemplate = render.PageTemplate

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

	// SecureCookies marks the session cookie Secure so it is only sent
	// over HTTPS. Enable in production; leave off for local development
	// over plain HTTP.
	SecureCookies bool

	// TemplateFS holds the host application's page templates (often an
	// embed.FS). If nil, the public Pages handler serves a placeholder.
	TemplateFS fs.FS

	// SharedTemplates are glob patterns within TemplateFS for layouts and
	// partials parsed into every page's template set, e.g.
	// []string{"templates/base.tmpl", "templates/partials/*.tmpl"}.
	SharedTemplates []string

	// PageTemplates lists the templates editors may choose for a page.
	// Each entry's File is parsed together with SharedTemplates into its
	// own set, so different pages may define the same block names.
	PageTemplates []PageTemplate

	// Logger receives operational log output. Defaults to slog.Default().
	Logger *slog.Logger
}

// CMS is the root object of the module. Create one with New.
type CMS struct {
	cfg      Config
	sessions *scs.SessionManager
	users    *auth.Store
	content  *content.Store
	renderer *render.Renderer
	admin    http.Handler
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
	if cfg.AdminPath == "" {
		cfg.AdminPath = "/admin"
	}
	cfg.AdminPath = "/" + strings.Trim(cfg.AdminPath, "/")
	if cfg.SessionLifetime <= 0 {
		cfg.SessionLifetime = 24 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	sessions := scs.New()
	sessions.Store = sessionstore.New(cfg.DB)
	sessions.Lifetime = cfg.SessionLifetime
	sessions.Cookie.Name = "cms_session"
	sessions.Cookie.HttpOnly = true
	sessions.Cookie.SameSite = http.SameSiteLaxMode
	sessions.Cookie.Secure = cfg.SecureCookies

	users := auth.NewStore(cfg.DB)
	contentStore := content.NewStore(cfg.DB)

	var renderer *render.Renderer
	if cfg.TemplateFS != nil {
		var err error
		renderer, err = render.New(cfg.TemplateFS, cfg.SharedTemplates, cfg.PageTemplates)
		if err != nil {
			return nil, err
		}
	}

	c := &CMS{
		cfg:      cfg,
		sessions: sessions,
		users:    users,
		content:  contentStore,
		renderer: renderer,
	}
	c.admin = admin.New(admin.Deps{
		Sessions:      sessions,
		Users:         users,
		Content:       contentStore,
		Renderer:      renderer,
		Logger:        cfg.Logger,
		AdminPath:     cfg.AdminPath,
		DefaultLocale: cfg.Locales[0],
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
// Migrate; it is a no-op on every startup after the first.
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
		Role:         auth.RoleAdmin,
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

// Pages returns the public site handler: it looks up the published page for
// the request path and renders it with the host's templates. When no
// TemplateFS is configured it serves a placeholder instead.
func (c *CMS) Pages() http.Handler {
	if c.renderer == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>CMS</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 40rem; margin: 4rem auto;">
<h1>It works</h1>
<p>Configure Config.TemplateFS and Config.PageTemplates to render pages here.
The admin area is available at <a href="` + c.cfg.AdminPath + `/">` + c.cfg.AdminPath + `/</a>.</p>
</body></html>`))
		})
	}

	locale := c.cfg.Locales[0]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		slug := strings.Trim(r.URL.Path, "/")

		page, err := c.content.GetBySlug(r.Context(), slug, locale, true)
		if errors.Is(err, content.ErrNotFound) {
			c.notFound(w)
			return
		}
		if err != nil {
			c.cfg.Logger.Error("cms: loading page", "slug", slug, "err", err)
			http.Error(w, "Something went wrong.", http.StatusInternalServerError)
			return
		}

		blocks, err := c.content.BlocksFor(r.Context(), page.ID, locale, content.StatusPublished)
		if err != nil {
			c.cfg.Logger.Error("cms: loading blocks", "slug", slug, "err", err)
			http.Error(w, "Something went wrong.", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := c.renderer.Render(w, page, blocks, locale); err != nil {
			c.cfg.Logger.Error("cms: rendering page", "slug", slug, "err", err)
			http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		}
	})
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
