// Command basic is the reference host application for the CMS module. It
// wires the CMS into a plain net/http server. Run a Postgres instance (see
// docker-compose.yml in this directory), then:
//
//	go run .
//
// and visit http://localhost:4000/admin/. On first run an admin account is
// created from CMS_ADMIN_EMAIL / CMS_ADMIN_PASSWORD (with development-only
// defaults, printed at startup).
package main

import (
	"bufio"
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tsawler/cms"
	"github.com/tsawler/cms/admin"
	"github.com/tsawler/cms/media"
)

//go:embed templates
var templateFS embed.FS

// loadDotEnv reads simple KEY=VALUE lines from the first .env file found and
// sets any variables not already present in the environment. Good enough for
// an example app; real deployments should use their platform's config.
func loadDotEnv(paths ...string) {
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if ok && os.Getenv(key) == "" {
				os.Setenv(strings.TrimSpace(key), strings.TrimSpace(value))
			}
		}
		err = scanner.Err()
		f.Close()
		if err == nil {
			return
		}
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()
	loadDotEnv(".env", "../../.env")

	dsn := envOr("DATABASE_URL", "postgres://cms:cms@localhost:5433/cms?sslmode=disable")
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	var objects media.ObjectStore
	if os.Getenv("S3_ENDPOINT") != "" {
		store, err := media.NewS3Store(media.S3Config{
			Endpoint:  os.Getenv("S3_ENDPOINT"),
			Region:    os.Getenv("S3_REGION"), // optional; derived from endpoint if empty
			Bucket:    os.Getenv("S3_BUCKET"),
			AccessKey: os.Getenv("S3_ACCESS_KEY"),
			Secret:    os.Getenv("S3_SECRET"),
			KeyPrefix: os.Getenv("S3_KEY_PREFIX"), // optional; namespaces keys in a shared bucket
		})
		if err != nil {
			return err
		}
		// One-time setup: make uploads publicly readable via bucket
		// policy (object ACLs are rejected by many modern stores).
		if os.Getenv("S3_APPLY_PUBLIC_POLICY") == "1" {
			if err := store.ApplyPublicReadPolicy(ctx); err != nil {
				return err
			}
			logger.Info("applied public-read bucket policy", "bucket", os.Getenv("S3_BUCKET"))
		}
		objects = store
	} else {
		logger.Warn("S3_ENDPOINT not set — media library disabled")
	}

	// Login CAPTCHA against the Cap container from docker-compose.yml.
	// Create a site key in the Cap dashboard (http://localhost:3000) and
	// set CAP_URL, CAP_SITE_KEY, and CAP_SECRET; unset means no CAPTCHA.
	var capCfg *cms.CaptchaConfig
	if os.Getenv("CAP_URL") != "" {
		capCfg = &cms.CaptchaConfig{
			URL:         os.Getenv("CAP_URL"),
			InternalURL: os.Getenv("CAP_INTERNAL_URL"), // optional
			SiteKey:     os.Getenv("CAP_SITE_KEY"),
			Secret:      os.Getenv("CAP_SECRET"),
		}
	} else {
		logger.Warn("CAP_URL not set — login CAPTCHA disabled")
	}

	// "Remember me" duration in hours; unset or invalid falls back to
	// the CMS default (24h).
	var rememberFor time.Duration
	if h, err := strconv.Atoi(os.Getenv("CMS_REMEMBER_HOURS")); err == nil && h > 0 {
		rememberFor = time.Duration(h) * time.Hour
	}

	c, err := cms.New(cms.Config{
		DB:              db,
		Locales:         []string{"en", "fr"},
		Logger:          logger,
		ObjectStore:     objects,
		Captcha:         capCfg,
		RememberFor:     rememberFor,
		TemplateFS:      templateFS,
		SharedTemplates: []string{"templates/base.gohtml"},
		PageTemplates: []cms.PageTemplate{
			{File: "templates/pages/home.gohtml", Label: "Home page"},
			{File: "templates/pages/standard.gohtml", Label: "Standard page"},
			{File: "templates/pages/canvas.gohtml", Label: "Blank canvas"},
		},
		AdminSections: []cms.AdminSection{
			{Path: "reports", NavLabel: "Reports", Handler: reportsSection(db)},
		},
	})
	if err != nil {
		return err
	}

	if err := c.Migrate(ctx); err != nil {
		return err
	}

	adminEmail := envOr("CMS_ADMIN_EMAIL", "admin@example.com")
	adminPassword := envOr("CMS_ADMIN_PASSWORD", "password123")
	created, err := c.SeedAdmin(ctx, adminEmail, "Site Admin", adminPassword)
	if err != nil {
		return err
	}
	if created {
		logger.Warn("created initial admin — change this password",
			"email", adminEmail, "password", adminPassword)
	}

	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", c.Admin()))
	mux.Handle("/admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
	mux.Handle("/", c.Pages())

	addr := envOr("ADDR", ":4000")
	logger.Info("listening", "addr", addr, "admin", "http://localhost"+addr+"/admin/")
	return http.ListenAndServe(addr, mux)
}

// reportsSection is a deployment-specific admin page, registered through
// Config.AdminSections. It serves {AdminPath}/x/reports/ behind the CMS's
// login, session, and CSRF middleware, and uses the admin package helpers
// to render inside the standard admin chrome.
func reportsSection(db *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		var pages, users int
		if err := db.QueryRow(r.Context(), "select count(*) from cms_pages").Scan(&pages); err != nil {
			http.Error(w, "Something went wrong.", http.StatusInternalServerError)
			return
		}
		if err := db.QueryRow(r.Context(), "select count(*) from cms_users").Scan(&users); err != nil {
			http.Error(w, "Something went wrong.", http.StatusInternalServerError)
			return
		}

		body := fmt.Sprintf(`<h1>Reports</h1>
<p class="cms-muted">A custom admin page registered by the host application.</p>
<p>Hello %s — this site has %d pages and %d CMS users.</p>
<form method="post" action="ping">
    <input type="hidden" name="csrf_token" value="%s">
    <button type="submit" class="cms-btn">Ping</button>
</form>`,
			template.HTMLEscapeString(admin.UserFrom(r).Name), pages, users,
			template.HTMLEscapeString(admin.CSRFToken(r)))

		admin.RenderPage(w, r, "Reports", template.HTML(body))
	})

	// The relative form action resolves to {AdminPath}/x/reports/ping.
	// CSRF has already been validated by the time this runs. Redirects
	// need the full browser-facing URL: the handler sees stripped paths,
	// so a relative redirect would resolve against the wrong base.
	mux.HandleFunc("POST /ping", func(w http.ResponseWriter, r *http.Request) {
		admin.SetFlash(r, "Pong — handled by the host application.")
		http.Redirect(w, r, "/admin/x/reports/", http.StatusSeeOther)
	})

	return mux
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
