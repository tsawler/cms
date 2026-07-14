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
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tsawler/cms/admin"
	"github.com/tsawler/cms/auth"
	"github.com/tsawler/cms/internal/sessionstore"
	"github.com/tsawler/cms/migrations"
)

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

	// Logger receives operational log output. Defaults to slog.Default().
	Logger *slog.Logger
}

// CMS is the root object of the module. Create one with New.
type CMS struct {
	cfg      Config
	sessions *scs.SessionManager
	users    *auth.Store
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

	c := &CMS{
		cfg:      cfg,
		sessions: sessions,
		users:    users,
	}
	c.admin = admin.New(admin.Deps{
		Sessions:  sessions,
		Users:     users,
		Logger:    cfg.Logger,
		AdminPath: cfg.AdminPath,
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

// Pages returns the public site handler. Page rendering arrives in phase 2;
// until then this serves a small placeholder so the example application runs
// end to end.
func (c *CMS) Pages() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>CMS</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 40rem; margin: 4rem auto;">
<h1>It works</h1>
<p>Public page rendering arrives in phase 2. The admin area is available at
<a href="` + c.cfg.AdminPath + `/">` + c.cfg.AdminPath + `/</a>.</p>
</body></html>`))
	})
}
