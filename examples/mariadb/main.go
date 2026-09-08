// Command mariadb is a second reference host application for the CMS,
// running on MariaDB instead of Postgres.
//
// It is deliberately the plainest possible host: hand-written CSS in the
// layout, no Tailwind, no build step, no object store. Start the database
// and run it —
//
//	docker compose up -d
//	go run .
//
// — then open http://localhost:4200/admin/ and log in with
// admin@example.com, with a password generated and logged on the first run.
//
// See examples/basic for the fuller setup: Tailwind, the media library, a
// login CAPTCHA, and a custom admin section.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	_ "github.com/go-sql-driver/mysql" // registers the "mysql" driver
	"github.com/tsawler/cms"
)

//go:embed templates
var templateFS embed.FS

// defaultDSN points at the container in docker-compose.yml.
//
// The four query parameters are not optional — the CMS misbehaves subtly
// without them, so any DATABASE_URL you supply instead has to carry them
// too:
//
//	parseTime=true     timestamps scan into time.Time rather than []byte
//	loc=UTC            the driver reads and writes them as UTC
//	time_zone='+00:00' pins the server session to UTC, so SQL now() agrees
//	clientFoundRows    UPDATE reports rows matched, not rows changed —
//	                   without it, re-saving an unchanged record looks like
//	                   "no such row" and the save fails
const defaultDSN = "cms:cms@tcp(localhost:3309)/cms" +
	"?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	db, err := sql.Open("mysql", envOr("DATABASE_URL", defaultDSN))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connecting to MariaDB (is `docker compose up -d` running?): %w", err)
	}

	c, err := cms.New(cms.Config{
		DB: db,
		// One dialect covers MySQL and MariaDB. It has to be set explicitly:
		// database/sql does not expose which driver a pool was opened with,
		// so the CMS cannot detect it, and the default is "postgres".
		Dialect:         "mysql",
		Logger:          logger,
		TemplateFS:      templateFS,
		SharedTemplates: []string{"templates/base.gohtml"},
		PageTemplates: []cms.PageTemplate{
			{File: "templates/pages/standard.gohtml", Label: "Standard page"},
			{File: "templates/pages/wide.gohtml", Label: "Full width"},
		},
		// The built-in defaults for these two are Tailwind class names, which
		// would render as nothing in a host with no Tailwind. Both are just
		// lists of classes the host's own CSS defines, so overriding them is
		// how a plain-CSS site takes ownership of the appearance. Every class
		// below exists in the <style> block in templates/base.gohtml.
		SectionStyles: &cms.SectionStyles{
			Backgrounds: []cms.SectionOption{
				{Key: "default", Label: "None", Class: ""},
				{Key: "paper", Label: "Paper", Class: "cms-section-paper"},
				{Key: "accent", Label: "Accent", Class: "cms-section-accent"},
			},
			// A width's Class lands on the inner <div>; its ContentClass is
			// not used. A background's Class lands on the <section> and its
			// ContentClass joins the inner <div> — which is how the built-in
			// dark and accent backgrounds add prose-invert.
			Widths: []cms.SectionOption{
				{Key: "normal", Label: "Normal", Class: "prose cms-section-inner"},
				{Key: "wide", Label: "Wide", Class: "prose cms-section-inner cms-section-wide"},
				{Key: "full", Label: "Full bleed", Class: "prose cms-section-inner cms-section-full"},
			},
			Corners: []cms.SectionOption{
				{Key: "square", Label: "Square", Class: ""},
				{Key: "round", Label: "Rounded", Class: "cms-section-round"},
			},
		},
		EditorStyles: []cms.EditorStyle{
			{Label: "Lead paragraph", Class: "cms-lead", Block: "p"},
			{Label: "Muted", Class: "cms-muted"},
			{Label: "Button", Class: "cms-btn"},
		},
	})
	if err != nil {
		return err
	}
	defer c.Close() // stops the CMS's background work; leaves db alone

	// Creates the schema on first run and upgrades it afterwards; safe to
	// call on every startup.
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

	// Give a fresh install something at "/" instead of a 404. An empty
	// template name takes the first entry in PageTemplates. No-op once the
	// site has any content.
	if _, err := c.SeedHomePage(ctx, "", "Welcome"); err != nil {
		return err
	}

	// One handler: the admin area under Config.AdminPath (/admin), the
	// public site everywhere else.
	mux := http.NewServeMux()
	mux.Handle("/", c.Handler())

	// Extends the site lock to anything the host serves itself; the CMS's
	// own pages are closed by the switch either way.
	handler := c.Lockdown(mux)

	addr := envOr("ADDR", ":4200")
	logger.Info("listening", "addr", addr, "admin", "http://localhost"+addr+"/admin/")
	return http.ListenAndServe(addr, handler)
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
