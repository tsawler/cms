# cms

An embeddable content management system for Go web applications. Import it
as a module, hand it a Postgres pool, and mount its handlers — no external
files, no separate install. See [DESIGN.md](DESIGN.md) for the full
architecture and build plan.

**Status: phase 3 (media).** Auth, user management, page CRUD with
draft/publish, public rendering through the host's own templates, draft
preview, per-page CSS/JS, editor-content sanitization, and the media
library (uploads to any S3-compatible bucket, automatic resizing, image
regions) are working. In-place editing, snippets, blog/news, and FR/EN
localization are next.

## Quick start

```go
//go:embed templates
var templateFS embed.FS

c, err := cms.New(cms.Config{
    DB:              pool, // *pgxpool.Pool
    S3: &cms.S3Config{ // omit to disable the media library
        Endpoint:  "us-ord-10.linodeobjects.com", // any S3-compatible store
        Bucket:    "my-site",
        AccessKey: os.Getenv("S3_ACCESS_KEY"),
        Secret:    os.Getenv("S3_SECRET"),
        // Default: media is proxied through the CMS (/cms/media/…), so a
        // private bucket just works. Set PublicRead or PublicBaseURL to
        // embed direct bucket/CDN URLs instead.
    },
    TemplateFS:      templateFS,
    SharedTemplates: []string{"templates/base.tmpl"},
    PageTemplates: []cms.PageTemplate{
        {File: "templates/pages/home.tmpl", Label: "Home page"},
        {File: "templates/pages/standard.tmpl", Label: "Standard page"},
    },
})
if err != nil { ... }
if err := c.Migrate(ctx); err != nil { ... }          // embedded migrations
if _, err := c.SeedAdmin(ctx, "you@example.com", "You", "a strong password"); err != nil { ... }

mux.Handle("/admin/", http.StripPrefix("/admin", c.Admin()))
mux.Handle("/", c.Pages())
```

Templates declare editable areas with the CMS template funcs, and the admin
UI discovers them automatically:

```html
<h1>{{cmsText "hero-title"}}</h1>       <!-- short plain text -->
<div>{{cmsRegion "main"}}</div>         <!-- rich HTML content -->
<img src="{{cmsImage "hero"}}">         <!-- image from the media library -->
<head> ... {{cmsHead}} ... </head>      <!-- meta description + per-page CSS -->
... {{cmsScripts}} </body>              <!-- per-page JS -->
```

## Running the example

```sh
cd examples/basic
docker compose up -d      # Postgres on localhost:5433
go run .
```

The example reads S3 credentials from a `.env` file at the repo root
(`S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET`, optional
`S3_REGION`); without one, the media library is simply disabled.

Then open <http://localhost:4000/admin/> and log in with
`admin@example.com` / `password123` (development defaults; override with
`CMS_ADMIN_EMAIL` and `CMS_ADMIN_PASSWORD`).

## Notes for host applications

- All tables are prefixed `cms_`, so the CMS can share a database with the
  host app.
- `Migrate` is safe to run on every startup and from multiple instances
  concurrently.
- Set `Config.SecureCookies = true` in production (HTTPS).
- `Config.AdminPath` must match wherever you mount `Admin()`; it defaults
  to `/admin`.
