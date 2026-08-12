# Quickstart: build a site with the cms module

This guide walks through building a working, editable website with the
`github.com/tsawler/cms` module from an empty directory: project setup,
database, templates, the Go program, styling, first login, and creating
content. Optional features (media library, blog & news, multiple
languages, CAPTCHA, forgot-password, custom admin pages) follow at the
end.

The mental model up front: **the CMS is a library, not a platform.** You
own `main()`, the HTTP server, the templates, and the stylesheet. The
module supplies an admin area, authentication, content storage,
migrations, and in-place editing on your public pages. You hand it a
database pool and your templates; it hands you two `http.Handler`s.

A complete reference implementation lives in [`examples/basic`](examples/basic)
— when in doubt, compare against it.

> **In a hurry?** `go run github.com/tsawler/cms/cmd/cms@latest init mysite`
> writes everything this guide builds by hand, in one step — see
> [Starting a new site](README.md#starting-a-new-site). Come back here when
> you want to know what each piece is doing and why.

## 1. Prerequisites

- **Go 1.25+**
- **A database** — see the next section
- **Docker** (optional, for the database and the CAPTCHA server)
- **Tailwind CSS standalone CLI** (optional, for the recommended styling
  setup: `brew install tailwindcss` — no Node required)

### Choosing a database

| Engine | Minimum | Driver | `Config.Dialect` |
| --- | --- | --- | --- |
| PostgreSQL | 12 | `github.com/jackc/pgx/v5/stdlib` (`"pgx"`) | `"postgres"` (default) |
| MySQL | 8.0.31 | `github.com/go-sql-driver/mysql` (`"mysql"`) | `"mysql"` |
| MariaDB | 10.6 | `github.com/go-sql-driver/mysql` (`"mysql"`) | `"mysql"` |

All three are first-class — the same store tests run against all of them, so
no feature works on one and not another. Pick on operational grounds:

**Match whatever your application already uses.** Every CMS table is prefixed
`cms_`, so it is built to share a database with your own schema. One database
means one backup, one pool, and transactions that can span both.

Only if you have a free choice: **Postgres** is the reference implementation
— the SQL is written in it and the dialect layer translates *away* from it —
and it keeps DDL inside transactions, so a failed migration rolls back
cleanly. On MySQL and MariaDB, DDL commits as it goes, so a failed migration
can leave a half-applied schema needing manual repair. Choose
**MySQL/MariaDB** if that is what you operate or your host provides; the cost
is four DSN settings that fail subtly if you miss them (step 3).

The MySQL floor is 8.0.31 rather than 8.0 because change detection uses
`EXCEPT`, which MySQL only gained in that release. MariaDB has had it since
10.3.

This guide uses Postgres. Where MySQL differs — three places, all in step 3
and step 5 — it says so inline.

## 2. Create the project

```sh
mkdir mysite && cd mysite
go mod init example.com/mysite
go get github.com/tsawler/cms
```

Then the driver for your engine — one or the other, not both:

```sh
go get github.com/jackc/pgx/v5           # PostgreSQL
go get github.com/go-sql-driver/mysql    # MySQL or MariaDB
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
│   ├── input.css          # step 6 — Tailwind input (optional)
│   └── theme.css          # step 6 — shared theme tokens, e.g. fonts (optional)
├── static/
│   └── site.css           # step 6 — your compiled/authored stylesheet
└── templates/             # step 4
    ├── base.gohtml        # shared layout
    └── pages/
        ├── home.gohtml    # page types editors can choose
        ├── standard.gohtml
        └── canvas.gohtml
```

## 3. Start the database

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

Any Postgres works — the CMS only needs a `*sql.DB`. All its tables
are prefixed `cms_`, so it can safely share a database with the rest of
your application. You never write schema: `Migrate` (step 5) creates and
upgrades everything, and is safe to run on every startup, even from
several instances at once.

### If you chose MySQL or MariaDB

Use this service instead. `--default-time-zone=+00:00` matters: the CMS
stores UTC, and anything else reading the database should see the same clock.

```yaml
services:
  mysql:                        # or: image: mariadb:lts-ubi10
    image: mysql:8.4
    environment:
      MYSQL_DATABASE: cms
      MYSQL_USER: cms
      MYSQL_PASSWORD: cms
      MYSQL_ROOT_PASSWORD: cms
    command: --default-time-zone=+00:00
    ports:
      - "3307:3306"   # 3307 to stay clear of a local MySQL
    volumes:
      - mysqldata:/var/lib/mysql

volumes:
  mysqldata:
```

**The DSN needs four settings.** They are not optional — without them the CMS
misbehaves in ways that look like bugs elsewhere:

| Setting | What breaks without it |
| --- | --- |
| `parseTime=true` | Timestamps scan as `[]byte`, not `time.Time` — scans fail. |
| `loc=UTC` | The driver reads and writes timestamps in local time. |
| `time_zone='+00:00'` | The *server session* disagrees with the driver, so SQL `now()` drifts from Go-written times: session expiry and post dates go wrong by your server's offset. |
| `clientFoundRows=true` | `UPDATE` reports rows *changed* instead of rows *matched*. Re-saving a record with unchanged values then looks like "no such row", and **saves fail with a not-found error**. |

Written out (note `'+00:00'` is URL-encoded):

```
cms:cms@tcp(localhost:3307)/cms?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true
```

**One operational difference to know now:** on MySQL and MariaDB, DDL commits
as it goes. Postgres runs each migration in a transaction and rolls back on
failure; these engines can leave a migration half-applied, needing manual
repair. The version is only recorded on success, so a re-run retries the whole
file.

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
| `{{cmsTitle}}` | the page's own title, typed over in place while editing |
| `{{cmsRegion "key"}}` | rich HTML content (the main editable body) |
| `{{cmsShared "key"}}` | rich HTML stored once for the whole site (a footer) |
| `{{cmsImage "key"}}` | an image URL from the media library |
| `{{cmsSections "key"}}` | editor-composed full-width sections |
| `{{cmsNav "key"}}` | a complete menu (markup, dropdowns, mobile toggle) |
| `{{cmsMenu "key"}}` | raw menu entries, if you want to own the markup |
| `{{cmsPosts "blog" 10}}` | the newest N blog/news entries |
| `{{cmsFeed "blog"}}` | one `?page=`-worth of a feed, for paginated listings |
| `{{cmsPagination $feed}}` | a ready-made Previous / 1 2 3 / Next bar |
| `{{cmsDate .PublishedAt}}` | a date in the page's language ("30 juillet 2026") |
| `{{cmsLocales}}` | language-switcher links (multi-locale sites) |
| `{{cmsHead}}` | meta description, favicon, per-page CSS, hreflang, robots — put in `<head>` |
| `{{cmsScripts}}` | per-page JS — put before `</body>` |

The dot (`.`) each page template receives carries `.Title`,
`.Description`, `.Slug`, `.Locale`, and `.Post` (nil except on blog/news
posts).

That list is the CMS's half. Templates can also call functions **you**
register, for the parts of a page that come from your own tables rather
than from an editor — see [host data in a CMS
page](#host-data-in-a-cms-page) in step 9.

`{{cmsTitle}}` and `{{.Title}}` print the same words; the difference is
what an editor can do with them. `{{.Title}}` is plain text, so a heading
built from it can only be changed through the title field in the edit
bar's **Page settings** (or a post's gear). `{{cmsTitle}}` is the same
title made editable where it is shown: click the heading, type, Save —
and the browser tab and search results follow, because it is one value
and not a copy. Use it for the heading in the page body, and keep
`{{.Title}}` in `<title>`, where an editing wrapper would show up as
markup.

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
        {{cmsShared "footer" "<p>&copy; My Site</p>"}}
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

An entry with `Unlisted: true` is offered only to superadmins when
creating a page — use it for one-off templates that back exactly one
page (the home page is a natural candidate), so the everyday "new page"
list stays short and no one accidentally creates a second home page.
Existing pages using an unlisted template are unaffected.

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

`templates/pages/standard.gohtml` — a heading the template owns, then
editor-composed sections, so every block of content carries its own
width, background, and spacing controls:

```html
{{template "base" .}}

{{define "content"}}
<section class="mx-auto max-w-3xl px-6 py-12">
    <h1 class="text-3xl font-bold tracking-tight text-slate-900 mb-6">{{cmsText "heading"}}</h1>
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
- **`{{cmsShared}}` is the exception**, and is what a footer wants: its
  content is stored once for the site, so putting one in your layout
  gives every page an editable footer without anyone creating it page by
  page. It takes optional fallback markup —
  `{{cmsShared "footer" "<p>&copy; My Site</p>"}}` — shown until an
  editor fills it in. Publishing any page publishes shared edits with it.
- Templates are parsed at startup — restart the server after editing
  them. The file extension is your choice (`.gohtml` plays best with
  editor tooling), but pages store their template's *path*, so renaming
  files under an existing database needs a one-time SQL fixup.

## 5. Write main.go

```go
package main

import (
	"context"
	"database/sql"
	"embed"
	"log/slog"
	"net/http"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
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
	db, err := sql.Open("pgx", dsn)
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

	// Publishes a page at "/" so a brand-new site serves something instead
	// of a 404. The template must be one of PageTemplates above ("" takes
	// the first). Also a no-op once the site has any content.
	if _, err := c.SeedHomePage(ctx, "templates/pages/home.gohtml", "Welcome"); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	mux.Handle("/", c.Handler())

	logger.Info("listening", "addr", ":4000", "admin", "http://localhost:4000/admin/")
	return http.ListenAndServe(":4000", mux)
}
```

### For MySQL or MariaDB

Three lines change, and nothing else in the program does:

```go
import _ "github.com/go-sql-driver/mysql"   // instead of the pgx stdlib driver

dsn = "cms:cms@tcp(localhost:3307)/cms" +
    "?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true"
db, err := sql.Open("mysql", dsn)           // instead of "pgx"

c, err := cms.New(cms.Config{
    DB:      db,
    Dialect: "mysql",                       // "mysql" covers MariaDB too
    // ...everything else identical
})
```

`Config.Dialect` must match the driver the pool was opened with.
`database/sql` does not expose the driver name, so the CMS cannot detect it,
and the default is `"postgres"` — a mismatch fails immediately with SQL
syntax errors rather than corrupting anything.

### What the handler serves

`c.Handler()` serves two areas from one handler:

- **The admin area** — login, dashboard, pages, users, media, snippets,
  blog & news — under `Config.AdminPath` (default `/admin`).
- **The public site** everywhere else. It resolves the request path to a
  page and renders it with *your* templates. Anonymous visitors get
  published content; logged-in CMS users get drafts with the in-place
  editor injected. It also serves the editor's assets and proxied media
  under `/cms/`, and is strictly read-only (GET/HEAD).

Hosts that need different wiring — the admin on its own hostname, extra
middleware on one side only — can mount the two areas separately with
`c.Admin()` (under `Config.AdminPath`, prefix stripped) and `c.Pages()`.

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
@source inline("text-slate-500 text-red-600 text-emerald-600 text-blue-600 text-white bg-yellow-200 font-serif font-mono text-lg text-slate-600 text-sm text-left text-center text-right float-left float-right mr-6 ml-6 block mx-auto w-full w-2/3 w-1/2 w-1/3 h-auto rounded-lg rounded-2xl rounded-full aspect-video object-cover");
```

Compile it (rerun after template changes, or use `--watch`):

```sh
tailwindcss --input assets/input.css --output static/site.css --minify
```

### The safelist

Editor content lives in the database, where Tailwind's source scanner can't
see it. Every class the CMS's own UI can apply must therefore be listed
explicitly, or those styles silently vanish in a production build. This
covers the default Styles menu, the alignment and image controls, the
snippet library, and the section presets:

```css
/* Tailwind v4 — in assets/input.css, alongside @import "tailwindcss"; */
@source inline("align-top aspect-square aspect-video bg-blue-50 bg-blue-600 bg-blue-700 bg-slate-200 bg-slate-50 bg-slate-900 bg-white bg-yellow-200 block border border-2 border-b border-b-2 border-blue-200 border-blue-600 border-dashed border-slate-200 border-slate-300 border-slate-900 flex float-left float-right font-bold font-mono font-semibold font-serif gap-6 gap-8 grid h-0.5 h-auto hover:bg-blue-600 hover:bg-slate-300 hover:bg-slate-900 hover:text-white inline-block items-center justify-center max-w-3xl max-w-5xl max-w-none mb-1 mb-2 mb-3 ml-6 mr-2 mr-6 mt-1 mt-10 mt-2 mt-3 mt-4 mt-6 mx-auto my-4 my-6 my-8 not-prose object-contain object-cover odd:bg-slate-50 overflow-x-auto p-1 p-2 p-4 p-6 prose prose-invert prose-slate px-5 px-6 px-8 py-12 py-2.5 py-3 rounded-2xl rounded-3xl rounded-full rounded-lg rounded-xl size-24 sm:col-span-2 sm:grid-cols-2 sm:grid-cols-3 sm:grid-cols-4 sm:text-5xl sm:text-7xl text-2xl text-3xl text-4xl text-5xl text-6xl text-blue-600 text-blue-700 text-blue-900 text-center text-emerald-600 text-left text-lg text-red-600 text-right text-slate-200 text-slate-400 text-slate-500 text-slate-600 text-slate-700 text-slate-900 text-sm text-white text-xl tracking-tight tracking-widest uppercase w-1/2 w-1/3 w-10 w-2/3 w-auto w-full");
```

```js
// Tailwind v3 — tailwind.config.js
safelist: [
    "align-top", "aspect-square", "aspect-video", "bg-blue-50",
    "bg-blue-600", "bg-blue-700", "bg-slate-200", "bg-slate-50",
    "bg-slate-900", "bg-white", "bg-yellow-200", "block", "border",
    "border-2", "border-b", "border-b-2", "border-blue-200",
    "border-blue-600", "border-dashed", "border-slate-200",
    "border-slate-300", "border-slate-900", "flex", "float-left",
    "float-right", "font-bold", "font-mono", "font-semibold",
    "font-serif", "gap-6", "gap-8", "grid", "h-0.5", "h-auto",
    "hover:bg-blue-600", "hover:bg-slate-300", "hover:bg-slate-900",
    "hover:text-white", "inline-block", "items-center", "justify-center",
    "max-w-3xl", "max-w-5xl", "max-w-none", "mb-1", "mb-2", "mb-3",
    "ml-6", "mr-2", "mr-6", "mt-1", "mt-10", "mt-2", "mt-3", "mt-4",
    "mt-6", "mx-auto", "my-4", "my-6", "my-8", "not-prose",
    "object-contain", "object-cover", "odd:bg-slate-50", "overflow-x-auto", "p-1", "p-2", "p-4", "p-6",
    "prose", "prose-invert", "prose-slate", "px-5", "px-6", "px-8",
    "py-12", "py-2.5", "py-3", "rounded-2xl", "rounded-3xl",
    "rounded-full", "rounded-lg", "rounded-xl", "size-24",
    "sm:col-span-2", "sm:grid-cols-2", "sm:grid-cols-3",
    "sm:grid-cols-4", "sm:text-5xl",
    "sm:text-7xl", "text-2xl", "text-3xl", "text-4xl", "text-5xl",
    "text-6xl", "text-blue-600", "text-blue-700", "text-blue-900",
    "text-center", "text-emerald-600", "text-left", "text-lg",
    "text-red-600", "text-right", "text-slate-200", "text-slate-400",
    "text-slate-500", "text-slate-600", "text-slate-700",
    "text-slate-900", "text-sm", "text-white", "text-xl",
    "tracking-tight", "tracking-widest", "uppercase", "w-1/2", "w-1/3",
    "w-10", "w-2/3", "w-auto", "w-full",
],
```

If you replace `EditorStyles`, `Snippets`, or `SectionStyles` with your own,
safelist your classes instead of these. A test in the module
(`TestDocsListDefaultClasses`) keeps this list in step with the defaults, so
it won't quietly fall behind.

`examples/basic/assets/input.css` is a working copy of the above. For classes
typed into content *after* deployment — superadmins can enter arbitrary
markup through the HTML source view — see
[content-driven rebuilds](#content-driven-tailwind-rebuilds) below.

### Custom fonts

> Sites generated by `cms init` already have this wiring —
> `assets/theme.css`, imported by both builds — and only need their tokens
> filled in. This section is what that arrangement is, and why.

Typography needs one extra piece of care, because a site that sets
`Config.Tailwind` compiles **two** Tailwind stylesheets rather than one:
`static/site.css`, built here from the templates, and the CMS's generated
`/cms/content-<hash>.css`, built from the classes found in stored content
(see [content-driven rebuilds](#content-driven-tailwind-rebuilds)). The
second one is a full Tailwind build too, and `{{cmsHead}}` links it
*after* `site.css` — so wherever the two disagree, the content build
wins.

That catches typography twice:

- **A customized `@theme` gets reset.** The content build emits its own
  `@layer theme { :root { --font-sans: … } }` holding Tailwind's stock
  stack. Arriving second, it quietly overwrites a `--font-sans` this
  project redefined, and the site falls back to the default font.
- **Fonts editors apply never compile.** A class that lives only in the
  database — `font-display` from an `EditorStyles` entry, say — is the
  content build's job, and that build knows nothing about
  `--font-display`, so it emits no rule at all.

Both go away once the two builds share one theme file. Put the tokens in
`assets/theme.css`:

```css
@theme {
  --font-display: "Playfair Display", ui-serif, Georgia, serif;
  --font-body: "Inter", ui-sans-serif, system-ui, sans-serif;
}
```

Prefer *new* variables (`--font-display`, `--font-body`) over redefining
`--font-sans`: they name the role rather than the slot, and nothing in
Tailwind's defaults is competing for them.

Import that from `assets/input.css`, and give the headings editors create
somewhere to get their font from:

```css
@import "tailwindcss" source(none);
@plugin "@tailwindcss/typography";
@import "./theme.css";

@source "../templates";
@source inline("font-display");   /* only what editors can apply */
/* …plus the safelist from above… */

/* Typography as CSS rules rather than utility classes: headings written
 * in a rich region arrive as bare <h2>/<h3> with no class for a utility
 * to hook onto. Unlayered on purpose — layered CSS always loses to
 * unlayered CSS, so these hold regardless of which stylesheet loads
 * last. */
body { font-family: var(--font-body); }
h1, h2, h3, h4 { font-family: var(--font-display); }
```

Load the webfont itself in `templates/base.gohtml`, above the stylesheet:

```html
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Playfair+Display:wght@700&family=Inter:wght@400;600&display=swap">
<link rel="stylesheet" href="/static/site.css">
{{cmsHead}}
```

Then hand the same theme to the content build. `tailwind-content.sh`
stages its input in a scratch directory, so the theme has to be copied in
beside it — a relative `@import` won't resolve from `/tmp`:

```sh
cp "$1" "$dir/content.html"
cp assets/theme.css "$dir/theme.css"        # add this
cat > "$dir/input.css" <<'EOF'
@import "tailwindcss" source(none);
@plugin "@tailwindcss/typography";
@import "./theme.css";                      # and this
@source "./content.html";
EOF
```

Now both stylesheets agree on `--font-body`, and `font-display` compiles
wherever it is applied — in a template or by an editor. Nothing further
is needed for the editing experience: the in-place editor runs TinyMCE
inline, editing the real page, so editors see the real fonts as they
type. There is no separate editor stylesheet to configure.

Fonts are the clearest case, but the rule is general: **any `@theme`
customization — colors, spacing, breakpoints — belongs in the shared
file**, or the content build will disagree with the site build about it.

## 7. Run it, log in, create pages

```sh
go run .
```

1. Open <http://localhost:4000/admin/> and log in with the credentials
   you passed to `SeedAdmin`. The sidebar is stamped **Development**: a
   new site is kept out of search engines until a superadmin says
   otherwise (step 10).
2. Visit <http://localhost:4000/>. `SeedHomePage` already created and
   published it, so you get your layout with an empty content area rather
   than a 404.
3. To add more pages: **Pages → New page** in the admin. The **address** is
   the URL path — `about`, or `about/team` for nesting. **Leave it empty to
   make a page the homepage**; the empty slug *is* `/`, and only one page can
   hold it. New pages start as drafts and 404 for the public until published.
4. Back on <http://localhost:4000/>. Because you're logged in, an **Edit**
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

Each of these is one config field away. The snippets below set the fields by
hand; [Configuration from the environment](#configuration-from-the-environment)
at the end of this section does the same thing with one call.

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
direct bucket/CDN URLs instead (for a bucket that wasn't created public,
also set `ApplyPublicReadPolicy` and `Migrate` will apply a public-read
bucket policy — one-time, idempotent setup). Images get an automatic ladder
of WebP renditions — full width, card, thumbnail — that post templates emit
as a srcset (`MediaWebPQuality` tunes compression); videos up to
`MediaMaxVideoMB` (default 512) are stored as uploaded. Without `S3` the
media library is simply disabled — everything else works. For a custom
backend (local disk, tests), implement `media.ObjectStore` and set
`Config.ObjectStore`.

### Adopting a bucket that already has media

The bucket is self-describing. Alongside the binaries under
`<prefix>/media/`, every upload writes a small JSON manifest under
`<prefix>/manifests/` recording what the object keys cannot: the original
filename, alt text per locale, which folder the item is in, its
dimensions, and the uploader's email.

So a database that knows nothing about a bucket can rebuild from it. On
`Migrate`, when `cms_media` is empty and the bucket has manifests, the CMS
adopts them — the media library comes back with its folders, alt text, and
attribution intact. That covers restoring a lost database, and pointing a
staging environment at a copy of production's bucket.

```go
MediaAdopt: cms.MediaAdoptWhenEmpty,  // the default (zero value)
// cms.MediaAdoptReconcile — check on every startup, not only when empty
// cms.MediaAdoptOff       — never read the bucket
```

Adoption is **additive**: it inserts rows for manifests the database is
missing and never deletes anything, because a truncated listing, a
transient error, and misscoped credentials all look exactly like "the
objects are gone". Items whose binaries are actually missing are logged and
skipped rather than adopted as dead entries. Runs are idempotent and
resumable — an interrupted one picks up where it left off — and an advisory
lock keeps two instances starting at once from racing.

Two things to know:

- **Set `S3_KEY_PREFIX` when the bucket is shared.** Adoption is scoped to
  this deployment's prefix; without one, a fresh deployment on a shared
  bucket would pull in another site's library. A bucket this site owns
  outright needs no prefix — the CMS only warns if it actually adopts media
  from an unprefixed bucket.
- **Manifests are private.** They carry uploader emails and original
  filenames, so they live outside the media root and `ApplyPublicReadPolicy`
  grants public GET on `<prefix>/media/*` only.

Manifest writes during normal operation are best-effort: an object store
that is briefly unreachable leaves a sidecar stale rather than failing an
editor's edit. `Manager.SyncManifests` rewrites every manifest from the
database and repairs that drift.

### Blog & news

Add a post template to enable both feeds:

```go
PostTemplate: cms.PageTemplate{File: "templates/pages/post.gohtml", Label: "Post"},
```

`templates/pages/post.gohtml` is a page template whose dot additionally
carries `.Post` (date, author, listing image, …):

```html
{{template "base" .}}

{{define "content"}}
{{cmsSections "header"}}
<article>
    <header class="mx-auto max-w-3xl px-6 {{if cmsHasSections "header"}}pt-8{{else}}pt-12{{end}}">
        {{if not (cmsHasSections "header")}}
        <h1 class="text-4xl font-extrabold tracking-tight text-slate-900">{{cmsTitle}}</h1>
        {{end}}
        {{with .Post}}<p class="mt-3 text-sm text-slate-500">{{cmsDate .PublishedAt}}{{with .Author}} · {{.}}{{end}}</p>{{end}}
    </header>
    {{cmsSections "sections"}}
</article>
{{end}}
```

The banner at the top is a sections region of its own, so editors restyle
it on the page with the section gear — background image and where to
anchor it, width, corners, height, text over the top — instead of being
stuck with one fixed image from a form. A seeded banner opens with the
post's title as the page's `<h1>`, centered over the picture in white or
near-black depending on how dark the image is; `{{cmsHasSections
"header"}}` is how the template knows to leave the title to the banner,
and to print its own `<h1>` on a post with no banner. Printing it in both
places would show the same words twice. Either heading is edited where it
sits: the banner's is a line inside a section, and the no-banner one is
`{{cmsTitle}}`, the post's title itself. Region order is the convention:
the **last** sections region is the main content (where a new page's
starter section goes) and the **first** is the banner (where a new post's
image goes). A post created without an image simply has an empty banner
region, which offers its own "Add section" button.

Posts are created from the tool rail's **Post** button (title, feed,
summary, date, image up front) or under **Blog & News** in the
admin, and then **edited in place exactly like pages** — a post is an
ordinary page whose slug lives under `blog/` or `news/`.

The gear beside the edit bar's **Done** button holds the rest of a post's
settings, the same fields the admin's post form has:

- **Title** and **Summary** — the summary is the blurb listing cards and
  the RSS feed show.
- **Meta description** — what search engines are given, when that should
  read differently from the summary. Empty means "use the summary", so a
  post that never sets one behaves as it always did. (An ordinary page
  has one description, edited under **Page settings**: for a page the
  description *is* the meta description.)
- **Date** — shown on the post and used to order listings.
- **Show the author's name** — off publishes the post under the site's
  name, keeping the date but printing no byline. The author stays
  recorded, so switching it back on restores the same name; templates
  need nothing beyond the `{{with .Author}}` they already write.
- **Thumbnail** — the listing image.

Title, summary, and meta description are per-locale and staged like the
rest of the page, so they reach the site on the next Publish; the date,
the byline, and the thumbnail apply at once.

For the listing page, create a page at slug `blog` (or `news`) whose
template ranges over `{{cmsPosts "blog" 12}}` — each entry has `.Title`,
`.Summary`, `.URL`, `.PublishedAt`, `.Author`, `.Thumbnail`,
and `.Draft` (true only for editors, so you can badge drafts). RSS is
automatic at `/blog/rss.xml` and `/news/rss.xml`.

`cmsPosts` shows the newest N and stops. To page through the whole feed,
use `cmsFeed` — same entries, one page at a time, paginated server-side
from `?page=`:

```html
{{$feed := cmsFeed "blog"}}
{{range $feed.Posts}}<a href="{{.URL}}"><h2>{{.Title}}</h2></a>{{end}}
{{cmsPagination $feed}}
```

Page size is `CMS_POSTS_PER_PAGE` (default 10), or per listing with
`{{cmsFeed "blog" 6}}`. `{{cmsPagination}}` emits the bar for you under
`cms-pager*` classes; to own the markup, range over `$feed.Links`
(`.Number`, `.URL`, `.Current`, `.Ellipsis`) and use `$feed.PrevURL`,
`$feed.NextURL`, `$feed.Page`, `$feed.TotalPages` and `$feed.HasPages`.
`examples/basic` shows both — `blog.gohtml` the ready-made bar,
`news.gohtml` a hand-built one.

`.Thumbnail` is the post's listing image with its renditions resolved —
`.URL` is sized for a card, `.Srcset` lists the rest, `.Width`/`.Height`
are the intrinsic size of `.URL`, and `.Alt` comes from the media
library. It is nil when the post has no image, so `{{with}}` is the
natural way to use it:

```html
{{with .Thumbnail}}
<img src="{{.URL}}" srcset="{{.Srcset}}" sizes="(min-width: 640px) 21rem, 100vw"
     width="{{.Width}}" height="{{.Height}}" alt="{{.Alt}}" loading="lazy">
{{end}}
```

Write `sizes` yourself — only your template knows how wide the image ends
up. `.ThumbnailURL` still works if you only want a string.

### Multiple languages

```go
Locales: []string{"en", "fr"}, // first entry is the default
```

The default language stays at `/about`; others live under their code
(`/fr/about`, `/fr`). Add `{{cmsLocales}}` to the layout for a language
switcher; `{{cmsHead}}` emits `hreflang` alternates automatically.
Translating is in-place editing too: flip the edit bar's language
switcher, and untranslated regions render the default language with an
amber outline — edit and save to translate, region by region. The page's
title and meta description translate in the ⋯ menu's **Page settings**
(a post's, in its ⚙ pill), where an empty field means "keep showing the
default language". Publish applies to all languages at once.

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

Two things worth knowing before you rely on it:

- **Pin the widget version.** Set `WIDGET_VERSION` and `WASM_VERSION` on the
  Cap container rather than leaving them at `latest`. The widget is a browser
  dependency of your login page: on `latest`, an upstream release can change
  its API or its CSP requirements under a deployment that hasn't changed at
  all. `examples/basic/docker-compose.yml` pins known-good versions.
- **The login page's CSP carries `'unsafe-eval'`**, because Cap's
  instrumentation challenge calls `eval()`. It is scoped to that one page —
  every other admin page gets a strict `default-src 'self'` policy. Turning
  instrumentation off for the site key in the Cap dashboard removes the need
  for it, at the cost of the anti-automation layer.

If the Cap server is unreachable the login proceeds with a logged warning —
an outage of the CAPTCHA backend shouldn't lock you out — and the throttle
still applies.

### Forgot password

Give `cms.Config` a `Mailer` — one method, `Send(ctx, to, subject, text,
html)` — and the login page grows a "Forgot your password?" link backed
by the full reset flow: single-use emailed links that expire after an
hour, throttled and honeypotted like the login form, with the email's
wording authored by the CMS so it never confirms whether an address has
an account.

```go
Mailer: cmsMailer{appMailer}, // adapt whatever your app sends mail through
```

Without a `Mailer` the feature is off: no link, and the reset routes
answer 404. See "Password resets" in the README for the full design.

### Custom admin pages

Reports, imports, settings — register plain handlers and they're mounted
*inside* the admin's login/session/CSRF middleware at `/admin/x/{path}/`:

```go
AdminSections: []cms.AdminSection{
    {Path: "reports", NavLabel: "Reports", Handler: reportsHandler},
},
```

`NavAfter: "dashboard"` (or any built-in entry name) moves the sidebar
link directly under that entry instead of the default spot after the
built-ins.

Inside the handler, `admin.UserFrom(r)`, `admin.CSRFToken(r)`,
`admin.SetFlash(r, …)`, and `admin.RenderPage(w, r, title, body)` (from
`github.com/tsawler/cms/admin`) integrate with the admin chrome.

The `/x/` namespace guarantees your paths never collide with built-in admin
routes, now or after upgrades. Handlers run behind the same login, session,
and CSRF middleware as the rest of the admin, so a POST needs the CSRF token
in a `csrf_token` field or the `X-CSRF-Token` header. Redirects need
`admin.SectionPath(r)` — your handler sees stripped paths, so a bare relative
redirect lands in the wrong place. A working `reportsSection` is in
`examples/basic/main.go`.

### Host data in a CMS page

Not every part of a page is content. A dealership's "fresh on the lot"
strip, a shop's best sellers, a clinic's next available slots — those live
in *your* tables, change when the data changes rather than when someone
rewrites a sentence, and have fields (price, trim, photo) that no
arrangement of editable text slots models honestly.

Modelling them as `cmsText` slots looks fine for a week. Then a car sells,
and someone has to remember to edit the home page.

`Config.TemplateFuncs` publishes your own functions to page templates, so
one page can mix both. Start with the type the template wants — return
display-ready strings and `{{.Price}}` stays a field rather than a
pipeline of formatting funcs:

```go
type Vehicle struct {
    Name, Detail, Price, PhotoURL, URL string
}

type VehicleStore struct{ db *sql.DB }

func (s *VehicleStore) Featured(ctx context.Context, n int) []Vehicle {
    // SELECT … FROM vehicles WHERE sold_at IS NULL AND featured
    // ORDER BY listed_at DESC LIMIT $1  — run with ctx.
}

func (s *VehicleStore) Count(ctx context.Context) int { ... }
```

Register it. This is a `FuncMap`, so add as many functions as the page
needs:

```go
vehicles := &VehicleStore{db: db}

cfg.TemplateFuncs = template.FuncMap{
    "featuredVehicles": func(n int) []Vehicle { return vehicles.Featured(context.Background(), n) },
    "vehicleCount":     func() int { return vehicles.Count(context.Background()) },
}

// Optional but usually right: rebind the same names per request, so a
// query carries that request's context and dies with it.
cfg.RequestFuncs = func(r *http.Request) template.FuncMap {
    return template.FuncMap{
        "featuredVehicles": func(n int) []Vehicle { return vehicles.Featured(r.Context(), n) },
        "vehicleCount":     func() int { return vehicles.Count(r.Context()) },
    }
}
```

Then call them in a page template like any other func:

```html
<h2>{{cmsText "inventory-title"}}</h2>   <!-- content: an editor owns this -->
<p>{{cmsText "inventory-lede"}}</p>

<div class="grid gap-6 sm:grid-cols-3">
  {{range featuredVehicles 3}}                <!-- data: your table owns this -->
    <a href="{{.URL}}">
      <img src="{{.PhotoURL}}" alt="{{.Name}}" loading="lazy">
      <h3>{{.Name}}</h3><p>{{.Detail}}</p><span>{{.Price}}</span>
    </a>
  {{else}}
    <p>Nothing on the lot right now — check back shortly.</p>
  {{end}}
</div>
```

Four things to know:

- **`TemplateFuncs` declares the names.** Templates are parsed against it
  at startup, so a name missing from it fails with `function
  "featuredVehicles" not defined`. `RequestFuncs` only swaps
  implementations for one render; a name appearing only there is
  unreachable, and `New` rejects `RequestFuncs` without `TemplateFuncs`.
- **`cms*` is reserved**, so a future release can add template funcs
  without colliding with yours. `New` refuses a host func named `cmsFoo`.
- **A func in `TemplateFuncs` alone is shared by every render** and must
  be concurrency-safe. Per-request state belongs in `RequestFuncs`.
- **They run with your full trust.** A func returning `template.HTML`
  skips the editor's content sanitizer, so never interpolate untrusted
  input into one.

Always give `{{range}}` an `{{else}}`: an empty result is a normal state,
and without one the section renders as a heading over nothing.

Two conveniences fall out of this. The markup lives in your template
files, which your Tailwind build already scans — so data-driven markup
needs no safelisting and never touches the generated content stylesheet.
And detail pages (`/inventory/{slug}`) are plain host routes, registered
on your mux ahead of `c.Handler()`; only the pages the CMS renders need
the funcs.

If you validate templates at build time with `render.CheckTemplate`,
switch to `render.CheckTemplateFuncs` and pass the same map, or every
template calling a host func reports a spurious parse failure.

`examples/wheels` is a worked version: `vehicles.go` holds the store and
both maps, and `templates/pages/home.gohtml` ranges over the result
between two `cmsText` slots.

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

This is a second, independent Tailwind build, and it only knows what its
own input CSS tells it. A site that customizes `@theme` must therefore
give the wrapper script the same tokens the site build uses, or the two
stylesheets will disagree — see [custom fonts](#custom-fonts) in step 6
for the shared-theme-file pattern and what it prevents.

### Other knobs

- `EditorStyles` — replace the editor's Styles menu with your own named,
  on-brand styles (classes only, no color pickers).
- `Snippets` / `SectionStyles` — your own block library and section
  background/width options; nil gets Tailwind-first defaults. **Both
  defaults are Tailwind class names**, so a site not using Tailwind should
  override them with classes its own stylesheet defines —
  `examples/mariadb` shows exactly that.
- `ObjectStore` — replace S3 entirely. Implement `media.ObjectStore`
  (`Put`/`Get`/`Delete`/`PublicURL`) for local disk in development or any
  storage you already run. When set, `S3` is ignored.
- `SessionLifetime` (default 24h), `RememberFor` (default 30 days) — login
  session durations.
- `AdminPath` — serve the admin somewhere other than `/admin`.
- `Logger` — a `*slog.Logger`; defaults to `slog.Default()`.
- `SecureCookies` — see [step 10](#10-going-to-production).

### Configuration from the environment

`cms.ConfigFromEnv()` returns a `Config` with the media library, CAPTCHA,
Tailwind, and session knobs already filled in from environment variables. You
then set the fields it can't know — the database pool, templates — on the
result:

```go
cfg, err := cms.ConfigFromEnv()
if err != nil {
    return err   // an invalid value is a startup error, not a silent default
}
cfg.DB = db
cfg.TemplateFS = templateFS
cfg.SharedTemplates = []string{"templates/base.gohtml"}
cfg.PageTemplates = []cms.PageTemplate{ /* ... */ }

c, err := cms.New(cfg)
```

Every variable it reads:

| Variable | Default | Purpose |
| --- | --- | --- |
| `CMS_SITE_URL` | unset (each request's own host) | The site's canonical public address, e.g. `https://example.com`. Used wherever a link has to work away from the page it was made on: the media library's **Copy link**, RSS item links, and hreflang alternates. Set it when the request's `Host` would be wrong — behind a proxy that rewrites it, or when the admin is reached by a different name than the public site. A value with no scheme is taken as `https`. |
| `CMS_REMEMBER_DAYS` | `30` | How long a "Remember me" login lasts, in days. Invalid or non-positive is a startup error. |
| `CMS_POSTS_PER_PAGE` | `10` | Posts per page in a paginated `{{cmsFeed}}` listing. Invalid or non-positive is a startup error. |
| `CMS_ADMIN_PER_PAGE` | `25` | Rows per page in the admin's Blog & News and Pages lists. Invalid or non-positive is a startup error. |
| `S3_ENDPOINT` | unset (media library disabled) | S3-compatible endpoint. Setting it enables the media library and makes the other `S3_*` variables relevant. |
| `S3_BUCKET` | — | Bucket for uploaded media. |
| `S3_ACCESS_KEY` | — | Object-store access key. |
| `S3_SECRET` | — | Object-store secret key. |
| `S3_REGION` | derived from the endpoint | Region, if your provider needs it spelled out. |
| `S3_KEY_PREFIX` | unset | Prefix namespacing this site's keys inside a shared bucket. Pick a stable slug; it also scopes media adoption, so set it whenever the bucket is shared. |
| `S3_APPLY_PUBLIC_POLICY` | unset | `1` applies a public-read bucket policy during `Migrate` (one-time, idempotent). |
| `CMS_MEDIA_WEBP_QUALITY` | `0.3` | Lossy WebP quality for image variants, in (0, 1]. Non-numeric is a startup error. |
| `CMS_MEDIA_MAX_VIDEO_MB` | `512` | Video upload cap in MB. Non-numeric is a startup error. |
| `CMS_MEDIA_ADOPT` | `when-empty` | Rebuild the media library from the bucket: `when-empty` on a database with no media, `reconcile` on every startup, `off` never. See [Adopting a bucket that already has media](#adopting-a-bucket-that-already-has-media). |
| `CAP_URL` | unset (CAPTCHA disabled) | Browser-facing URL of the Cap server. Setting it enables the login CAPTCHA. |
| `CAP_SITE_KEY` | — | Site key created in the Cap dashboard. |
| `CAP_SECRET` | — | Secret paired with that site key. |
| `CAP_INTERNAL_URL` | uses `CAP_URL` | Server-to-server Cap address, e.g. a container name inside Docker. |
| `CAP_WIDGET` | invisible | `visible` shows Cap's checkbox widget instead of solving in the background. |
| `CMS_TAILWIND_COMMAND` | unset (rebuilds disabled) | Content-driven Tailwind rebuild command: space-separated argv with `{content}` and `{output}` placeholders. |
| `CMS_TAILWIND_DIR` | unset | Working directory for that command. |

These are read by the module. Anything else — where your DSN or listen
address comes from — is your program's business; the variables
`examples/basic` uses for that (`DATABASE_URL`, `ADDR`, `CMS_DIALECT`,
`CMS_ADMIN_EMAIL`, `CMS_ADMIN_PASSWORD`) are its own, not the module's.

## 10. Going to production

- **Switch the site to production.** A freshly seeded site starts in
  **development**, where it asks search engines to skip it — the admin
  sidebar stamps every page while it does. A superadmin flips it in the
  editor's wrench menu → **Site settings** → **Site mode**. Nothing else
  changes; it decides whether the site may be indexed. If you cache
  `Pages()` at the edge, purge after the switch — cached responses can
  still carry the `X-Robots-Tag: noindex` header.
- **Set `SecureCookies: true`** — you're serving over HTTPS, and the
  session cookie should say so.
- **Seed a strong password**, or change the seeded one immediately; the
  first account is a superadmin.
- **Keep `Migrate` in startup.** It's idempotent, concurrency-safe, and
  how schema upgrades ship with new module versions. On MySQL and MariaDB
  it is not transactional (step 3), so take a backup before upgrading the
  module — on those engines a failed migration needs manual repair.
- **Leave the seeds in.** `SeedAdmin` and `SeedHomePage` both no-op once the
  site has content, so they cost one cheap query per boot and can't
  overwrite anything. Deleting the home page keeps it deleted.
- **Compile the stylesheet as a build step** (the example wires it as
  `go generate`), and keep the safelist in sync with any custom
  `EditorStyles`/`Snippets`/`SectionStyles` you configure.
- Run behind your usual TLS-terminating proxy; the CMS is just handlers
  on your mux. `Pages()` is GET/HEAD-only and safe to cache at the edge
  for anonymous traffic.
- Users, roles (editor / admin / superadmin), and further accounts are
  managed in the admin's Users area.

## Where next

You now have a complete site. These go deeper:

- [`examples/basic`](examples/basic) — the reference host application, with
  everything in step 9 wired up: Docker, Tailwind, S3 media, CAPTCHA, blog &
  news, and a custom admin section. Postgres by default; MySQL and MariaDB
  behind compose profiles.
- [`examples/wheels`](examples/wheels) — a design mockup turned into a
  working site: a custom Tailwind v4 theme shared by both builds, an
  editor vocabulary in the site's own palette, all copy seeded as content
  rather than defaulted in templates, and a data-driven vehicle strip
  through `TemplateFuncs`.
- [`examples/mariadb`](examples/mariadb) — the opposite end: the smallest
  host that does something real. MariaDB, hand-written CSS, no Tailwind, no
  build step. The one to read if you're not using Tailwind, since it shows
  how to override `SectionStyles` and `EditorStyles` with your own classes.
- [README.md](README.md) — the feature reference, in more depth than this
  guide: the Styles menu, the snippet palette, section composition,
  localization, menus, and bot protection.
- [DESIGN.md](DESIGN.md) — architecture and design rationale.

Running the CMS's own tests needs Docker: `make test` runs the store suite
against Postgres, MySQL, and MariaDB in throwaway containers; `make
test-unit` skips anything needing a database.

## Releasing a new version (maintainers)

Not part of building a site — this is for publishing the `cms` module
itself. Tag, push the tag, then check that the module proxy sees it:

```sh
git tag v0.2.0
git push origin v0.2.0
curl -s https://proxy.golang.org/github.com/tsawler/cms/@v/list
```

That list is the thing that matters. `go get`, `go install`, and
`go run …@latest` resolve `@latest` from it, so a tag missing from the
list does not exist as far as anyone's Go toolchain is concerned —
regardless of what GitHub shows.

### If the new tag isn't listed

Ask the proxy for that exact version. Requesting one by name makes it
fetch from GitHub and rebuild the list:

```sh
curl -s https://proxy.golang.org/github.com/tsawler/cms/@v/v0.2.0.info
```

Re-check `@v/list` and the new version appears within seconds.

This is worth remembering because the symptom points at the wrong thing —
it reads as a broken tag or a missing package, not a stale cache:

```
go: github.com/tsawler/cms/cmd/cms@latest: module github.com/tsawler/cms@latest
    found (v0.1.0), but does not contain package github.com/tsawler/cms/cmd/cms
```

`found (v0.1.0)` is the tell: the toolchain is resolving to an older
version than the one you tagged.

To confirm a tag is fine while the cache catches up, skip the proxy
entirely:

```sh
GOPROXY=direct go run github.com/tsawler/cms/cmd/cms@latest version
```

### Why it happens

The proxy caches negative results as well as positive ones. This module's
list sat at `v0.1.0` for a while after the repository went from private to
public, because the proxy had cached "cannot read this repository" from
the private era; pushing tags did nothing to dislodge it. Once a module is
public and warm, new tags normally show up within minutes.

The flip side: once the proxy has served a version, it is cached
permanently. Deleting a tag does not unpublish it, and neither does making
the repository private again. Tag deliberately.
