// Command basic is the reference host application for the CMS module. It
// wires the CMS into a plain net/http server. Run a Postgres instance (see
// docker-compose.yml in this directory), then:
//
//	go run .
//
// and visit http://localhost:4000/admin/. On first run an admin account is
// created as CMS_ADMIN_EMAIL, with CMS_ADMIN_PASSWORD or — when that is
// unset — a password generated and logged once.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"

	_ "github.com/go-sql-driver/mysql" // database/sql driver "mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
	"github.com/joho/godotenv"
	"github.com/tsawler/cms"
	"github.com/tsawler/cms/admin"
)

//go:embed templates
var templateFS embed.FS

// loadDotEnv loads the first .env file found; godotenv never overrides
// variables already set in the environment. A missing file is fine — real
// deployments should use their platform's config.
func loadDotEnv(paths ...string) {
	for _, path := range paths {
		if godotenv.Load(path) == nil {
			return
		}
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// The compiled site stylesheet is gitignored (a build artifact, like
	// a binary): fresh checkouts must generate it once or every page
	// renders unstyled.
	if _, err := os.Stat("static/site.css"); err != nil {
		logger.Warn("static/site.css not found — run `go generate .` in examples/basic (requires the tailwindcss CLI, e.g. brew install tailwindcss)")
	}
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()
	loadDotEnv(".env", "../../.env")

	// CMS_DIALECT picks the engine: "postgres" (the default) or "mysql",
	// which covers MariaDB too. Each needs its own driver and DSN.
	//
	// The MySQL default carries the four settings the CMS requires:
	// parseTime/loc so timestamps scan into time.Time as UTC, time_zone so
	// the server session agrees with them, and clientFoundRows so an UPDATE
	// reports rows matched rather than rows changed. A DATABASE_URL supplied
	// by hand has to include them as well — see the README.
	dialect := envOr("CMS_DIALECT", "postgres")
	driver, defaultDSN := "pgx", "postgres://cms:cms@localhost:5433/cms?sslmode=disable"
	if dialect == "mysql" {
		driver = "mysql"
		defaultDSN = "cms:cms@tcp(localhost:3307)/cms" +
			"?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true"
	}
	db, err := sql.Open(driver, envOr("DATABASE_URL", defaultDSN))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connecting to the %s database: %w", dialect, err)
	}

	// The optional features are configured entirely from the environment
	// — media library (S3_*, including a one-time public-read bucket
	// policy via S3_APPLY_PUBLIC_POLICY=1), login CAPTCHA against the
	// Cap container from docker-compose.yml (CAP_*; create a site key in
	// the Cap dashboard at http://localhost:3300), Tailwind rebuilds and
	// media tuning (CMS_*). See the README's variable table.
	cfg, err := cms.ConfigFromEnv()
	if err != nil {
		return err
	}
	if cfg.S3 == nil {
		logger.Warn("S3_ENDPOINT not set — media library disabled")
	}
	if cfg.Captcha == nil {
		logger.Warn("CAP_URL not set — login CAPTCHA disabled")
	}
	cfg.DB = db
	cfg.Dialect = dialect
	cfg.Locales = []string{"en", "fr"}
	cfg.Logger = logger
	cfg.TemplateFS = templateFS
	cfg.SharedTemplates = []string{"templates/base.gohtml"}
	cfg.PageTemplates = []cms.PageTemplate{
		{File: "templates/pages/home.gohtml", Label: "Home page"},
		{File: "templates/pages/standard.gohtml", Label: "Standard page"},
		{File: "templates/pages/canvas.gohtml", Label: "Blank canvas"},
		{File: "templates/pages/blog.gohtml", Label: "Blog listing"},
		{File: "templates/pages/news.gohtml", Label: "News listing"},
	}
	cfg.PostTemplate = cms.PageTemplate{File: "templates/pages/post.gohtml", Label: "Post"}
	// Site search. Setting the template is what turns it on: the CMS then
	// answers at /search, and the site-settings dialog offers the switch
	// that puts a magnifying glass in the menu bar.
	cfg.SearchTemplate = cms.PageTemplate{File: "templates/pages/search.gohtml", Label: "Search results"}
	cfg.AdminSections = []cms.AdminSection{
		{Path: "reports", NavLabel: "Reports", Handler: reportsSection(db)},
	}

	c, err := cms.New(cfg)
	if err != nil {
		return err
	}
	defer c.Close() // stops the CMS's background work; leaves db alone

	if err := c.Migrate(ctx); err != nil {
		return err
	}

	// The first superadmin, created on the first run only. There is no
	// fallback password written here: one would be the same on every
	// checkout of this example, which is a published credential rather
	// than a default. Set CMS_ADMIN_PASSWORD in .env to choose it, or let
	// the first run generate and print one.
	adminEmail := envOr("CMS_ADMIN_EMAIL", "admin@example.com")
	adminPassword := os.Getenv("CMS_ADMIN_PASSWORD")
	generated := adminPassword == ""
	if generated {
		var err error
		if adminPassword, err = devPassword(); err != nil {
			return err
		}
	}
	if created, err := c.SeedAdmin(ctx, adminEmail, "Site Admin", adminPassword); err != nil {
		return err
	} else if created && generated {
		// Printed exactly once, on the run that created the account. Set
		// CMS_ADMIN_PASSWORD in .env to choose it yourself, or reset the
		// database and start again if this scrolls past.
		logger.Warn("created initial admin with a generated password",
			"email", adminEmail, "password", adminPassword)
	} else if created {
		// Not the password: logs get shipped, tailed, and aggregated, so a
		// credential written here outlives the terminal it appeared in.
		// Whoever configured it already knows it.
		logger.Warn("created initial admin — sign in and change the password",
			"email", adminEmail)
	}

	// Give a fresh install something at "/" instead of a 404. No-op once
	// the site has any content, so it never fights the editor.
	if _, err := c.SeedHomePage(ctx, "templates/pages/canvas.gohtml", "Welcome"); err != nil {
		return err
	}

	// c.Handler() routes Config.AdminPath (default /admin) to the admin
	// area and everything else to the public site.
	mux := http.NewServeMux()
	// The compiled site stylesheet (see assets/input.css and gen.go).
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.Handle("/", c.Handler())

	// The site lock closes everything the CMS serves without any help;
	// Lockdown extends it to the routes above that the CMS does not serve,
	// and is where a health check or a partner feed would be exempted.
	handler := c.Lockdown(mux)

	addr := envOr("ADDR", ":4000")
	logger.Info("listening", "addr", addr, "admin", "http://localhost"+addr+"/admin/")
	return http.ListenAndServe(addr, handler)
}

// reportsSection is a deployment-specific admin page, registered through
// Config.AdminSections. It serves {AdminPath}/x/reports/ behind the CMS's
// login, session, and CSRF middleware, and uses the admin package helpers
// to render inside the standard admin chrome.
func reportsSection(db *sql.DB) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		var pages, users int
		if err := db.QueryRowContext(r.Context(), "select count(*) from cms_pages").Scan(&pages); err != nil {
			http.Error(w, "Something went wrong.", http.StatusInternalServerError)
			return
		}
		if err := db.QueryRowContext(r.Context(), "select count(*) from cms_users").Scan(&users); err != nil {
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
	// need the full browser-facing URL — the handler sees stripped paths —
	// which admin.SectionPath provides.
	mux.HandleFunc("POST /ping", func(w http.ResponseWriter, r *http.Request) {
		admin.SetFlash(r, "Pong — handled by the host application.")
		http.Redirect(w, r, admin.SectionPath(r), http.StatusSeeOther)
	})

	return mux
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// devPassword returns a password for the example's first-run superadmin
// when none was configured.
//
// The generated sites this module writes put a per-project password in
// their .env, so their main.go can simply refuse when the variable is
// missing. An example checked out of a repository has nowhere for that to
// have been written — .env here is the developer's own, gitignored file —
// so it makes one instead, and prints it the once. A password written
// into this file would be in the repository, which is the thing being
// avoided.
func devPassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
