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
	"context"
	"embed"
	"log/slog"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tsawler/cms"
)

//go:embed templates
var templateFS embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	dsn := envOr("DATABASE_URL", "postgres://cms:cms@localhost:5433/cms?sslmode=disable")
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	c, err := cms.New(cms.Config{
		DB:              db,
		Locales:         []string{"en", "fr"},
		Logger:          logger,
		TemplateFS:      templateFS,
		SharedTemplates: []string{"templates/base.tmpl"},
		PageTemplates: []cms.PageTemplate{
			{File: "templates/pages/home.tmpl", Label: "Home page"},
			{File: "templates/pages/standard.tmpl", Label: "Standard page"},
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
