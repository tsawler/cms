# Content Management System

An embeddable content management system for Go web applications.

## Starting a new site

`cms init` writes a runnable site — `main.go`, a base layout, page
templates, `.env`, and a `docker-compose.yml` — into an empty directory:

```sh
go run github.com/tsawler/cms/cmd/cms@latest init mysite
cd mysite
docker compose up -d      # start the database
go mod tidy
go generate .             # compile static/site.css (needs the tailwindcss CLI)
go run .                  # http://localhost:4000, admin at /admin/
```

Sign in at `/admin/` with the credentials written into `.env`.

It creates the directory and its `go.mod` if they do not exist, and pins
the `cms` requirement to the version of the generator you ran, so the
generated `main.go` and the library it compiles against stay in step.

To install it once instead of fetching it each time:

```sh
go install github.com/tsawler/cms/cmd/cms@latest
cms init mysite
```

Useful flags — see `cms init -h` for the rest:

| Flag | Default | Effect |
| --- | --- | --- |
| `-db` | `postgres` | `postgres`, `mysql`, or `mariadb`: picks the driver, the DSN, and the compose service |
| `-name` | directory name | site name in the page title and header |
| `-module` | directory name | module path when `init` creates the `go.mod` |
| `-blog=false` | on | leave out the blog, news, and post templates |
| `-tailwind=false` | on | leave out the Tailwind build; supply `static/site.css` yourself |
| `-captcha` | off | add the Cap and Valkey services for the login CAPTCHA |
| `-replace` | — | point `go.mod` at a local checkout of this module |
| `-tidy` | off | run `go mod tidy` when finished |
| `-force` | off | overwrite files that already exist |
| `-n` | off | show what would be written, write nothing |

Existing files are never overwritten without `-force`, so re-running
`init` in a project that has moved on only fills in what is missing.

The same generation is available as a library — see
[`scaffold.Write`](scaffold/scaffold.go) — for hosts that want to wrap it
in their own tooling. For the same site built by hand, one step at a time,
read [QUICKSTART.md](QUICKSTART.md).

## Documentation

- [Architecture](docs/ARCHITECTURE.md) - Overview of the system architecture
- [Quick Start](QUICKSTART.md) - Getting started guide
- [Design Principles](DESIGN.md) - Core design concepts  

## Packages

- [admin](admin/) - Admin interface components
- [auth](auth/) - Authentication and authorization  
- [media](media/) - Media library handling
- [render](render/) - Template rendering system
- [snippets](snippets/) - Content blocks and section presets
- [content](content/) - Page and post content management

## Getting Started

```go
// Import the CMS package
import "github.com/tsawler/cms"

// Configure CMS with your database
cfg := cms.Config{
    DB: dbPool,
    TemplateFS: templates,
}

c, err := cms.New(cfg)
if err != nil { 
    // handle error
}

// Run migrations
if err := c.Migrate(ctx); err != nil {
    // handle error
}
```

See [QUICKSTART.md](QUICKSTART.md) for full details.
