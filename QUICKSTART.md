# Quickstart: build a site with the cms module

This guide walks through building a working, editable website with the
`github.com/tsawler/cms` module from an empty directory: project setup,
database, templates, the Go program, styling, first login, and creating
content. Optional features (media library, blog & news, multiple
languages, CAPTCHA, custom admin pages) follow at the end.

The mental model up front: **the CMS is a library, not a platform.** You
own `main()`, the HTTP server, the templates, and the stylesheet. The
module supplies an admin area, authentication, content storage,
migrations, and in-place editing on your public pages. You hand it a
Postgres pool and your templates; it hands you two `http.Handler`s.

A complete reference implementation lives in [`examples/basic`](examples/basic)
— when in doubt, compare against it.

## 1. Prerequisites

- **Go 1.25+**
- **PostgreSQL** (any recent version; the guide uses Docker)
- **Docker** (optional, for Postgres and the CAPTCHA server)
- **Tailwind CSS standalone CLI** (optional, for the recommended styling
  setup: `brew install tailwindcss` — no Node required)

## 2. Create the project

```sh
mkdir mysite && cd mysite
go mod init example.com/mysite
go get github.com/tsawler/cms
go get github.com/jackc/pgx/v5
```

That only creates `go.mod` and `go.sum` — the rest of the files are
written by hand over the next few steps. By the end of the guide the
project will look like this:

```
mysite/
├── go.mod
├── main.go                # step 5
├── docker-compose.yml     # step 3
├── assets/
│   └── input.css          # step 6 — Tailwind input (optional)
├── static/
│   └── site.css           # step 6 — your compiled/authored stylesheet
└── templates/             # step 4
    ├── base.gohtml        # shared layout
    └── pages/
        ├── home.gohtml    # page types editors can choose
        ├── standard.gohtml
        └── canvas.gohtml
```

## 3. Start Postgres

`docker-compose.yml`:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: cms
      POSTGRES_PASSWORD: cms
      POSTGRES_DB: cms
    ports:
      - "5433:5432"   # 5433 on the host to stay clear of a local Postgres
    volumes:
      - pgdata:/var/lib/postgresql/data

volumes:
  pgdata:
```

```sh
docker compose up -d
```

Any Postgres works — the CMS only needs a `*pgxpool.Pool`. All its tables
are prefixed `cms_`, so it can safely share a database with the rest of
your application. You never write schema: `Migrate` (step 5) creates and
upgrades everything, and is safe to run on every startup, even from
several instances at once.

## 4. Write the templates

Templates are ordinary `html/template` files. The CMS parses each **page
template** together with your **shared templates** (layouts/partials) and
exposes a set of `cms*` template functions. Editable areas are declared
right in the markup — the admin UI and the in-place editor discover them
automatically. Nothing else is required: no front-matter, no registration
step.

The functions available in every template:

| Function | Renders |
|---|---|
| `{{cmsText "key"}}` | short plain text (headlines, labels) |
| `{{cmsRegion "key"}}` | rich HTML content (the main editable body) |
| `{{cmsImage "key"}}` | an image URL from the media library |
| `{{cmsSections "key"}}` | editor-composed full-width sections |
| `{{cmsNav "key"}}` | a complete menu (markup, dropdowns, mobile toggle) |
| `{{cmsMenu "key"}}` | raw menu entries, if you want to own the markup |
| `{{cmsPosts "blog" 10}}` | blog/news entries for listing pages |
| `{{cmsLocales}}` | language-switcher links (multi-locale sites) |
| `{{cmsHead}}` | meta description, per-page CSS, hreflang — put in `<head>` |
| `{{cmsScripts}}` | per-page JS — put before `</body>` |

The dot (`.`) each page template receives carries `.Title`,
`.Description`, `.Slug`, `.Locale`, and `.Post` (nil except on blog/news
posts).

### The shared layout

`templates/base.gohtml`:

```html
{{define "base"}}<!doctype html>
<html lang="{{.Locale}}">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{.Title}} — My Site</title>
    <link rel="stylesheet" href="/static/site.css">
    {{cmsHead}}
</head>
<body class="bg-white text-slate-800 antialiased">
<header class="border-b border-slate-200">
    <div class="mx-auto max-w-4xl px-6 py-4 flex items-center justify-between gap-6">
        <a href="/" class="text-lg font-bold tracking-tight">My&nbsp;Site</a>
        {{cmsNav "main"}}
    </div>
</header>

<main>{{block "content" .}}{{end}}</main>

<footer class="border-t border-slate-200 mt-16">
    <div class="mx-auto max-w-4xl px-6 py-8 text-sm text-slate-500">
        © My Site
    </div>
</footer>
{{cmsScripts}}
</body>
</html>{{end}}
```

Two lines matter more than the rest: `{{cmsHead}}` in the `<head>` and
`{{cmsScripts}}` before `</body>`. They carry each page's metadata,
stylesheets, and the in-place editor for logged-in users. `{{cmsNav
"main"}}` renders the whole navigation — you'll build the actual menu by
right-clicking it in edit mode later (see step 8). Its markup carries
stable `cms-nav-*` classes your stylesheet can restyle; the CMS injects
only the functional minimum (layout, dropdown behavior, mobile collapse).

### Page templates

Each file in `PageTemplates` (step 5) is one **page type** editors can
choose when creating a page. Ship at least a home page and a generic
content page.

`templates/pages/home.gohtml`:

```html
{{template "base" .}}

{{define "content"}}
<section class="bg-slate-50 border-b border-slate-200">
    <div class="mx-auto max-w-4xl px-6 py-20 text-center">
        <h1 class="text-4xl font-extrabold tracking-tight text-slate-900">{{cmsText "hero-title"}}</h1>
        <p class="mt-4 text-lg text-slate-600">{{cmsText "hero-subtitle"}}</p>
    </div>
</section>

{{with cmsImage "feature-image"}}
<section class="mx-auto max-w-4xl px-6">
    <img src="{{.}}" alt="" data-cms-image="feature-image" class="rounded-xl shadow-md w-full">
</section>
{{end}}

<section class="mx-auto max-w-3xl px-6 py-12 prose prose-slate">
    {{cmsRegion "main"}}
</section>

{{cmsSections "sections"}}
{{end}}
```

`templates/pages/standard.gohtml`:

```html
{{template "base" .}}

{{define "content"}}
<section class="mx-auto max-w-3xl px-6 py-12">
    <h1 class="text-3xl font-bold tracking-tight text-slate-900 mb-6">{{cmsText "heading"}}</h1>
    <div class="prose prose-slate">
        {{cmsRegion "main"}}
    </div>
</section>

{{cmsSections "sections"}}
{{end}}
```

`templates/pages/canvas.gohtml` — the "sections-only" pattern, a page
editors compose entirely themselves from full-width sections:

```html
{{template "base" .}}

{{define "content"}}
{{cmsSections "sections"}}
{{end}}
```

Conventions worth knowing:

- **Editable images** need both the URL and a marker attribute:
  `<img data-cms-image="hero" src="{{cmsImage "hero"}}">`. Wrapping in
  `{{with cmsImage "hero"}}` hides the element until an image is chosen.
- **`{{cmsSections}}` goes outside any max-width container** — sections
  render full-bleed and bring their own width/background wrappers.
- **Keys are per-page.** `{{cmsRegion "main"}}` on two different pages is
  two independent pieces of content. Pages that switch template later
  keep content in regions both templates share.
- Templates are parsed at startup — restart the server after editing
  them. The file extension is your choice (`.gohtml` plays best with
  editor tooling), but pages store their template's *path*, so renaming
  files under an existing database needs a one-time SQL fixup.

## 5. Write main.go

```go
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

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://cms:cms@localhost:5433/cms?sslmode=disable"
	}
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	c, err := cms.New(cms.Config{
		DB:              db,
		Logger:          logger,
		TemplateFS:      templateFS,
		SharedTemplates: []string{"templates/base.gohtml"},
		PageTemplates: []cms.PageTemplate{
			{File: "templates/pages/home.gohtml", Label: "Home page"},
			{File: "templates/pages/standard.gohtml", Label: "Standard page"},
			{File: "templates/pages/canvas.gohtml", Label: "Blank canvas"},
		},
	})
	if err != nil {
		return err
	}

	// Creates/upgrades the cms_* tables. Safe on every startup.
	if err := c.Migrate(ctx); err != nil {
		return err
	}

	// Creates the first (superadmin) account — only if no users exist
	// yet, so it is a no-op on every startup after the first.
	created, err := c.SeedAdmin(ctx, "you@example.com", "Your Name", "change-this-password")
	if err != nil {
		return err
	}
	if created {
		logger.Warn("created initial admin — change this password")
	}

	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", c.Admin()))
	mux.Handle("/admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.Handle("/", c.Pages())

	logger.Info("listening", "addr", ":4000", "admin", "http://localhost:4000/admin/")
	return http.ListenAndServe(":4000", mux)
}
```

What the two handlers do:

- **`c.Admin()`** — the whole admin area: login, dashboard, pages, users,
  media, snippets, blog & news. Mount it wherever you like, but the mount
  point must match `Config.AdminPath` (default `/admin`), and the prefix
  must be stripped.
- **`c.Pages()`** — the public site. It resolves the request path to a
  page and renders it with *your* templates. Anonymous visitors get
  published content; logged-in CMS users get drafts with the in-place
  editor injected. It also serves the editor's assets and proxied media
  under `/cms/`, and is strictly read-only (GET/HEAD).

Everything else — other routes, middleware, your API — is your mux, your
rules. The CMS doesn't own the server.

## 6. Style the site

The CMS deliberately injects no styles into your pages: headings, lists,
and blockquotes that editors create look however **your** stylesheet says.
Any CSS works — hand-written, a framework, whatever `static/site.css`
contains.

The templates above use Tailwind, and that's the recommended setup. Two
Tailwind-specific rules matter:

1. **Give rich regions typography styles.** Tailwind's Preflight resets
   `h1`–`h6`, lists, and blockquotes to plain text, so wrap
   `{{cmsRegion}}` output in the `@tailwindcss/typography` plugin's
   `prose` class (as the templates above do) or editors' formatting will
   be invisible.
2. **Safelist the classes the editor applies.** Editor content lives in
   the database, and Tailwind only generates CSS for classes it can see
   in source files. The Styles menu, alignment buttons, image options,
   and default snippets all apply utility classes that may appear
   nowhere in your templates.

With the Tailwind v4 standalone CLI, `assets/input.css`:

```css
@import "tailwindcss" source(none);
@plugin "@tailwindcss/typography";

/* Your own markup. */
@source "../templates";

/* Classes the CMS editor UI can apply (Styles menu defaults, alignment,
 * image gear, video embeds). */
@source inline("text-slate-500 text-red-600 text-emerald-600 text-blue-600 text-white bg-yellow-200 font-serif font-mono text-lg text-slate-600 text-sm text-left text-center text-right float-left float-right mr-6 ml-6 block mx-auto w-full w-2/3 w-1/2 w-1/3 h-auto rounded-lg rounded-2xl rounded-full aspect-video");
```

Compile it (rerun after template changes, or use `--watch`):

```sh
tailwindcss --input assets/input.css --output static/site.css --minify
```

If you keep the CMS's default snippet library and section styles, their
classes need covering too — the full lists are in the README under
[Snippets](README.md#snippets) and [Sections](README.md#sections). For
classes typed into content *after* deployment, see
[content-driven rebuilds](#content-driven-tailwind-rebuilds) below.

## 7. Run it, log in, create pages

```sh
go run .
```

1. Open <http://localhost:4000/admin/> and log in with the credentials
   you passed to `SeedAdmin`.
2. Visiting <http://localhost:4000/> right now shows a 404 — a fresh
   database has no pages. Go to **Pages → New page** in the admin.
3. Create the home page: title "Home", **leave the address empty** (the
   empty slug *is* the homepage), pick the "Home page" template, save.
4. Visit <http://localhost:4000/>. Because you're logged in, an **Edit**
   bar appears on the page. Click Edit and the page becomes editable in
   place: click the headline and type; write rich text in the main
   region; click the image slot to pick or upload a picture (media
   library required — see below).
5. While editing, the **tool rail** on the left edge offers **＋ Section**
   (compose full-width sections — hero, feature grid, stats… — with
   per-section background/width settings), **Snippets** (drag ready-made
   blocks into rich regions), and **Page** (create a new page without
   leaving the site).
6. **Save** stores a draft only you and other editors see. **Publish**
   makes it live for the public. Draft and published versions coexist;
   you can keep editing after publishing.

Create a few more pages ("About", "Contact", …) with the Standard or
Blank-canvas types, either from the admin or the tool rail.

## 8. Build the menu

Menus are edited on the site itself, not in the admin. While in edit
mode, **right-click** (long-press on touch) the `{{cmsNav "main"}}` area:

- "＋" chips add items; dragging rearranges them, including into and out
  of dropdowns.
- Each item can link to a page (searchable picker — the URL follows slug
  renames) or an external address, open in a new tab, or become a
  label-only dropdown parent (one level deep).
- Items pointing at draft pages are visible only to editors until the
  page is published.
- Menu changes apply immediately, site-wide — there is no draft state
  for menus.

Use any menu key you like — `{{cmsNav "footer"}}` in the footer gives
you a second, independently edited menu.

## 9. Optional features

Each of these is one config field away. All of them are shown wired up
in `examples/basic/main.go`.

### Media library (image/video uploads)

Point the CMS at any S3-compatible bucket (AWS, Linode, DigitalOcean,
MinIO, …):

```go
S3: &cms.S3Config{
    Endpoint:  "us-ord-10.linodeobjects.com",
    Bucket:    "my-site",
    AccessKey: os.Getenv("S3_ACCESS_KEY"),
    Secret:    os.Getenv("S3_SECRET"),
    // KeyPrefix: "prod",  // share one bucket across deployments
},
```

By default media is **proxied** through the CMS (`/cms/media/…`), so a
private bucket just works; set `PublicRead` or `PublicBaseURL` to embed
direct bucket/CDN URLs instead. Uploads get automatic WebP variants and
thumbnails (`MediaWebPQuality` tunes compression); videos up to
`MediaMaxVideoMB` (default 512) are stored as uploaded. Without `S3` the
media library is simply disabled — everything else works. For a custom
backend (local disk, tests), implement `media.ObjectStore` and set
`Config.ObjectStore`.

### Blog & news

Add a post template to enable both feeds:

```go
PostTemplate: cms.PageTemplate{File: "templates/pages/post.gohtml", Label: "Post"},
```

`templates/pages/post.gohtml` is a page template whose dot additionally
carries `.Post` (date, author, header image, …):

```html
{{template "base" .}}

{{define "content"}}
{{with .Post}}{{if .HeaderURL}}<img src="{{.HeaderURL}}" alt="" class="h-64 w-full object-cover">{{end}}{{end}}
<article>
    <header class="mx-auto max-w-3xl px-6 pt-12">
        <h1 class="text-4xl font-extrabold tracking-tight text-slate-900">{{.Title}}</h1>
        {{with .Post}}<p class="mt-3 text-sm text-slate-500">{{.PublishedAt.Format "January 2, 2006"}}{{with .Author}} · {{.}}{{end}}</p>{{end}}
    </header>
    {{cmsSections "sections"}}
</article>
{{end}}
```

Posts are created from the tool rail's **Post** button (title, feed,
summary, date, images up front) or under **Blog & News** in the admin,
and then **edited in place exactly like pages** — a post is an ordinary
page whose slug lives under `blog/` or `news/`.

For the listing page, create a page at slug `blog` (or `news`) whose
template ranges over `{{cmsPosts "blog" 12}}` — each entry has `.Title`,
`.Summary`, `.URL`, `.PublishedAt`, `.Author`, `.ThumbnailURL`,
`.HeaderURL`, and `.Draft` (true only for editors, so you can badge
drafts). RSS is automatic at `/blog/rss.xml` and `/news/rss.xml`.

### Multiple languages

```go
Locales: []string{"en", "fr"}, // first entry is the default
```

The default language stays at `/about`; others live under their code
(`/fr/about`, `/fr`). Add `{{cmsLocales}}` to the layout for a language
switcher; `{{cmsHead}}` emits `hreflang` alternates automatically.
Translating is in-place editing too: flip the edit bar's language
switcher, and untranslated regions render the default language with an
amber outline — edit and save to translate, region by region. Publish
applies to all languages at once.

### Login CAPTCHA

The public site is read-only, so the bot-facing surface is the login
form. Throttling and a honeypot are always on; for a proof-of-work
CAPTCHA, run a [Cap](https://capjs.js.org) server (docker image
`tiago2/cap` — see `examples/basic/docker-compose.yml`), create a site
key in its dashboard, and set:

```go
Captcha: &cms.CaptchaConfig{
    URL:     "https://cap.example.com",
    SiteKey: os.Getenv("CAP_SITE_KEY"),
    Secret:  os.Getenv("CAP_SECRET"),
},
```

By default the challenge is solved invisibly in the background — users
never see a CAPTCHA. Add `Visible: true` to show Cap's checkbox widget
on the form instead.

### Custom admin pages

Reports, imports, settings — register plain handlers and they're mounted
*inside* the admin's login/session/CSRF middleware at `/admin/x/{path}/`:

```go
AdminSections: []cms.AdminSection{
    {Path: "reports", NavLabel: "Reports", Handler: reportsHandler},
},
```

Inside the handler, `admin.UserFrom(r)`, `admin.CSRFToken(r)`,
`admin.SetFlash(r, …)`, and `admin.RenderPage(w, r, title, body)` (from
`github.com/tsawler/cms/admin`) integrate with the admin chrome. See
[Custom admin pages](README.md#custom-admin-pages) and the working
`reportsSection` in the example.

### Content-driven Tailwind rebuilds

Superadmins can type any class into content through the HTML source
views, and no static safelist can cover that. Set `Config.Tailwind` and
the CMS reruns *your* Tailwind CLI over the classes found in stored
content whenever they change, serving the result as a supplemental
stylesheet linked by `{{cmsHead}}`:

```go
Tailwind: &cms.TailwindConfig{
    Command: []string{"./tailwind-content.sh", "{content}", "{output}"},
},
```

(Tailwind v4's CLI reads sources from the input CSS rather than a
`--content` flag, so point `Command` at a small wrapper script —
`examples/basic/tailwind-content.sh` is a ready-made one.)

### Other knobs

- `EditorStyles` — replace the editor's Styles menu with your own named,
  on-brand styles (classes only, no color pickers).
- `Snippets` / `SectionStyles` — your own block library and section
  background/width options; nil gets Tailwind-first defaults.
- `SessionLifetime`, `RememberFor` — login session durations.
- `AdminPath` — mount the admin somewhere other than `/admin`.

## 10. Going to production

- **Set `SecureCookies: true`** — you're serving over HTTPS, and the
  session cookie should say so.
- **Seed a strong password**, or change the seeded one immediately; the
  first account is a superadmin.
- **Keep `Migrate` in startup.** It's idempotent, concurrency-safe, and
  how schema upgrades ship with new module versions.
- **Compile the stylesheet as a build step** (the example wires it as
  `go generate`), and keep the safelist in sync with any custom
  `EditorStyles`/`Snippets`/`SectionStyles` you configure.
- Run behind your usual TLS-terminating proxy; the CMS is just handlers
  on your mux. `Pages()` is GET/HEAD-only and safe to cache at the edge
  for anonymous traffic.
- Users, roles (editor / admin / superadmin), and further accounts are
  managed in the admin's Users area.

## Where next

- [README.md](README.md) — the full feature reference: Styles menu,
  snippets, sections, blog & news, localization, menus, custom admin
  pages, bot protection.
- [`examples/basic`](examples/basic) — the reference host application,
  including Docker setup, Tailwind wiring, S3 media, CAPTCHA, and a
  custom admin section.
- [DESIGN.md](DESIGN.md) — architecture and design rationale.
