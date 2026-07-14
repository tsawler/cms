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
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tsawler/cms"
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

	c, err := cms.New(cms.Config{
		DB:              db,
		Locales:         []string{"en", "fr"},
		Logger:          logger,
		ObjectStore:     objects,
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
