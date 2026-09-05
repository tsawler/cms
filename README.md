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

Start here:

- [Quick Start](QUICKSTART.md) — build a working site from an empty
  directory, step by step
- [Architecture](docs/ARCHITECTURE.md) — how the packages fit together
- [Design Principles](DESIGN.md) — the rationale behind the design

Feature guides, in more depth than the quickstart:

- [Snippets, code blocks & sections](docs/snippets-and-sections.md) — the
  building blocks an editor drops into a page, and how sections arrange them
- [Navigation & site chrome](docs/navigation-and-layout.md) — menus, shared
  regions, the notice bar, and site-wide settings
- [Site search](docs/site-search.md) — full-text search over the published
  public site
- [Users, roles & permissions](docs/users-and-permissions.md) — the role
  model, the dashboard, bot protection, password resets, two-factor login
- [Host application integration](docs/host-integration.md) — mounting your
  own pages inside the admin area, and the embedding contract
- [Going to production](docs/production.md) — search-engine visibility,
  robots.txt, sitemaps, and closing the site behind the lock
- [Working on the CMS](docs/contributing.md) — running the bundled examples
  and rebuilding the in-place editor

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

## Tailwind safelist

Editor content lives in the database, where Tailwind's source scanner can't
see it. Every class the CMS's own UI can apply must therefore be listed
explicitly, or those styles silently vanish in a production build. This
covers the default Styles menu, the alignment and image controls, the
snippet library, and the section presets:

```css
/* Tailwind v4 — in assets/input.css, alongside @import "tailwindcss"; */
@source inline("aspect-square aspect-video bg-blue-50 bg-blue-600 bg-blue-700 bg-slate-200 bg-slate-50 bg-slate-900 bg-white bg-yellow-200 border border-2 border-blue-200 border-blue-600 border-dashed border-slate-200 border-slate-300 border-slate-900 columns-1 columns-2 columns-3 flex font-bold font-mono font-semibold font-serif gap-6 gap-8 grid grid-cols-1 h-0.5 hover:bg-blue-600 hover:bg-slate-300 hover:bg-slate-900 hover:text-white inline-block items-center justify-center lg:grid-cols-3 max-w-3xl max-w-5xl max-w-none mb-1 mb-10 mb-2 mb-3 mr-2 mt-1 mt-10 mt-2 mt-3 mt-4 mt-6 mx-auto my-4 my-6 my-8 not-prose object-contain p-4 p-6 prose prose-invert prose-lg prose-slate prose-sm prose-xl px-5 px-6 px-8 py-0 py-12 py-2.5 py-20 py-3 py-6 rounded-2xl rounded-3xl rounded-full rounded-lg rounded-xl size-24 sm:col-span-1 sm:col-span-10 sm:col-span-11 sm:col-span-12 sm:col-span-2 sm:col-span-3 sm:col-span-4 sm:col-span-5 sm:col-span-6 sm:col-span-7 sm:col-span-8 sm:col-span-9 sm:grid-cols-1 sm:grid-cols-12 sm:grid-cols-2 sm:grid-cols-3 sm:grid-cols-4 sm:text-2xl sm:text-3xl sm:text-4xl sm:text-5xl sm:text-7xl text-2xl text-3xl text-4xl text-5xl text-6xl text-blue-600 text-blue-700 text-blue-900 text-center text-emerald-600 text-lg text-red-600 text-right text-slate-200 text-slate-400 text-slate-500 text-slate-600 text-slate-700 text-slate-900 text-sm text-white text-xl tracking-tight tracking-widest uppercase w-10 w-full");
```

Replacing `EditorStyles`, `Snippets`, or `SectionStyles` with your own?
Safelist those classes instead. The Tailwind v3 form of this list, and what
to do about classes typed into content *after* deployment, are in
[The safelist](QUICKSTART.md#the-safelist).

A test in the module (`TestDocsListDefaultClasses`) checks this list against
the defaults, so it cannot quietly fall behind.
