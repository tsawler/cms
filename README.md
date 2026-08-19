# cms

An embeddable content management system for Go web applications. Import it
as a module, hand it a database pool, and mount its handlers — no external
files, no separate install. See [DESIGN.md](DESIGN.md) for the full
architecture and build plan.

Runs on **PostgreSQL**, **MySQL 8.0.31+**, and **MariaDB 10.6+** — see
[Databases](#databases).

**Status: phase 6 (blog & news).** Auth, user management, page CRUD with
draft/publish, public rendering through the host's own templates, the
media library (any S3-compatible bucket, automatic image resizing,
SVG with script-stripping validation, MP4/WebM video, folders and
search, and a self-describing bucket an empty database can rebuild
itself from), in-place editing (TinyMCE 6 — the last MIT release, vendored and
self-hosted), the curated Styles menu, the snippet palette, editor-composable
sections, blog & news posts (page-backed, edited in place, with RSS), and
multilingual content (per-locale metadata and blocks, with field-level
fallback to the default language) are all working: log in, browse the site,
click Edit, change text and images directly on the page, drop in ready-made
blocks, save drafts, publish.

The storage layer runs on PostgreSQL, MySQL, and MariaDB, verified by a
conformance suite that runs the same store tests against all three.

To make an image editable in place, add `data-cms-image` to the tag:

```html
<img data-cms-image="hero" src="{{cmsImage "hero"}}" alt="...">
```

Clicking one while editing raises a small toolbar on the image: a pencil,
which opens the media library, and — only once the slot holds a chosen
picture — a trash can, which puts it back to whatever the template draws
when the slot is empty. There is no gear, because a slot's size, link and
alt text belong to the template rather than to the person editing.

A chosen picture arrives at the full-width rendition, which is what a
banner across the page wants. A slot smaller than that — a card, a tile
three across a grid — asks for a smaller rung by name:

```html
<img data-cms-image="tile-atvs" data-cms-rendition="card"
     src="{{cmsImage "tile-atvs"}}" alt="...">
```

so the page stores and serves the size it actually displays rather than
one the browser has to shrink. The value is a rung of the rendition
ladder — `web` (the default, bounded at 1600px), `card` (800px) or
`thumb` — and anything the chosen item has no such rendition for, a
vector included, falls back to `web`.

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

It creates the directory and its `go.mod` if they do not exist, and pins
the `cms` requirement to the version of the generator you ran, so the
generated `main.go` and the library it compiles against stay in step.

The generated project keeps its theme — fonts to begin with — in
`assets/theme.css`, which both Tailwind builds import. That is the file to
edit first, and the generated `README.md` explains why it is separate;
[keeping the two builds on one theme](#keep-the-two-builds-on-one-theme)
below is the short version.

Useful flags — see `cms init -h` for the rest:

| Flag | Default | Effect |
| --- | --- | --- |
| `-db` | `postgres` | `postgres`, `mysql`, or `mariadb`: picks the driver, the DSN, and the compose service |
| `-name` | directory name | site name in the page title and header |
| `-blog=false` | on | leave out the blog, news, and post templates |
| `-tailwind=false` | on | leave out the Tailwind build; supply `static/site.css` yourself |
| `-captcha` | off | add the Cap and Valkey services for the login CAPTCHA |
| `-replace` | — | point `go.mod` at a local checkout of this module |
| `-force` | off | overwrite files that already exist |
| `-n` | off | show what would be written, write nothing |

Existing files are never overwritten without `-force`, so re-running
`init` in a project that has moved on only fills in what is missing.

To install it once instead of fetching it each time:

```sh
go install github.com/tsawler/cms/cmd/cms@latest
```

The same generation is available as a library — see
[`scaffold.Write`](scaffold/scaffold.go) — for hosts that want to wrap it
in their own tooling. For the same thing done by hand, one step at a time,
read [QUICKSTART.md](QUICKSTART.md).

## Quick start

```go
//go:embed templates
var templateFS embed.FS

// Postgres: sql.Open("pgx", dsn), with _ "github.com/jackc/pgx/v5/stdlib".
// MySQL/MariaDB: see Databases below — the DSN needs specific settings.
c, err := cms.New(cms.Config{
    DB:              db, // *sql.DB
    // Dialect:      "mysql", // default "postgres"; "mysql" covers MariaDB
    S3: &cms.S3Config{ // omit to disable the media library
        Endpoint:  "us-ord-10.linodeobjects.com", // any S3-compatible store
        Bucket:    "my-site",
        AccessKey: os.Getenv("S3_ACCESS_KEY"),
        Secret:    os.Getenv("S3_SECRET"),
        // Default: media is proxied through the CMS (/cms/media/…), so a
        // private bucket just works. Set PublicRead or PublicBaseURL to
        // embed direct bucket/CDN URLs instead.
        // KeyPrefix: "my-site", // share one bucket across deployments:
        // each stores its objects under <KeyPrefix>/media/…. Pick a
        // stable slug per deployment and never change it once media
        // exists.
    },
    TemplateFS:      templateFS,
    SharedTemplates: []string{"templates/base.gohtml"},
    PageTemplates: []cms.PageTemplate{
        {File: "templates/pages/home.gohtml", Label: "Home page"},
        {File: "templates/pages/standard.gohtml", Label: "Standard page"},
    },
})
if err != nil { ... }
if err := c.Migrate(ctx); err != nil { ... }          // embedded migrations
if _, err := c.SeedAdmin(ctx, "you@example.com", "You", "a strong password"); err != nil { ... }

// Optional: give a brand-new site a published page at "/" instead of a 404.
// The template must be one of PageTemplates above — page templates belong to
// the host, so the CMS can't pick one, and it refuses a name you haven't
// configured. Pass "" to take the first entry. Both seeds are no-ops once
// the site has any content.
if _, err := c.SeedHomePage(ctx, "templates/pages/home.gohtml", "Welcome"); err != nil { ... }

mux.Handle("/", c.Handler())   // admin under Config.AdminPath, pages everywhere else
```

(Hosts configured by environment can start from `cms.ConfigFromEnv()`,
which fills S3, CAPTCHA, Tailwind, and the media knobs from the
[documented variables](#environment-variables), and then set `DB`,
`TemplateFS`, and the rest on the returned `Config`.)

Templates declare editable areas with the CMS template funcs, and the admin
UI discovers them automatically:

```html
<h1>{{cmsText "hero-title"}}</h1>       <!-- short plain text -->
<h1>{{cmsTitle}}</h1>                   <!-- the page's own title, edited in place -->
<div>{{cmsRegion "main"}}</div>         <!-- rich HTML content -->
<div>{{cmsShared "footer"}}</div>       <!-- rich HTML shared by every page -->
<img src="{{cmsImage "hero"}}">         <!-- image from the media library -->
{{cmsSections "body"}}                  <!-- editor-composed full-width sections -->
<head> ... {{cmsHead}} ... </head>      <!-- meta description, favicon, robots, per-page CSS -->
... {{cmsScripts}} </body>              <!-- per-page JS -->
```

Templates can also call the host's *own* functions, for the parts of a
page that come from your tables rather than from an editor — see
[host data in CMS pages](#host-data-in-cms-pages).

## Databases

| Engine | Minimum | Driver | `Config.Dialect` |
| --- | --- | --- | --- |
| PostgreSQL | 12 | `github.com/jackc/pgx/v5/stdlib` (`"pgx"`) | `"postgres"` (default) |
| MySQL | 8.0.31 | `github.com/go-sql-driver/mysql` (`"mysql"`) | `"mysql"` |
| MariaDB | 10.6 | `github.com/go-sql-driver/mysql` (`"mysql"`) | `"mysql"` |

`Config.DB` is a `*sql.DB` and `Config.Dialect` must match the driver it was
opened with — `database/sql` does not expose the driver name, so the CMS
cannot detect it.

The MySQL floor is 8.0.31 rather than 8.0 because change detection uses
`EXCEPT`, which MySQL only gained in that release. MariaDB has had it since
10.3.

### Choosing one

All three are first-class: the same store tests run against all of them, so
no feature works on one engine and not another. Pick on operational grounds,
not on what the CMS supports.

**Match whatever the host application already uses.** The CMS prefixes every
table `cms_`, so it is designed to share a database with your own schema.
One database means one backup, one connection pool, and transactions that
can span both.

Only if you have a free choice:

- **Postgres** is the reference implementation — it is what the SQL is
  written in, and the dialect layer translates *away* from it. It also
  keeps DDL inside transactions, so a failed migration rolls back cleanly.
  On MySQL and MariaDB, DDL commits as it goes and a failed migration can
  leave a half-applied schema needing manual repair.
- **MySQL / MariaDB** if that is what you operate, know, or your host
  provides. The cost is the four DSN settings below, which are easy to get
  wrong and fail subtly rather than loudly.

Setup differs in exactly three places — the driver import, the DSN, and
`Config.Dialect`. Nothing else in your application changes.

```go
// Postgres
import _ "github.com/jackc/pgx/v5/stdlib"
db, err := sql.Open("pgx", "postgres://user:pass@localhost:5432/mydb?sslmode=disable")
cfg := cms.Config{DB: db} // Dialect defaults to "postgres"

// MySQL or MariaDB
import _ "github.com/go-sql-driver/mysql"
db, err := sql.Open("mysql", "user:pass@tcp(localhost:3306)/mydb"+
    "?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true")
cfg := cms.Config{DB: db, Dialect: "mysql"}
```

Switching later means migrating the data yourself: the schemas are
equivalent but the CMS has no export/import, so treat it as a real
migration rather than a config change.

### MySQL and MariaDB DSN settings

Four settings are **required**; the CMS misbehaves subtly without them:

```go
dsn := "cms:cms@tcp(localhost:3307)/cms" +
    "?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true"
db, err := sql.Open("mysql", dsn)
```

| Setting | Why |
| --- | --- |
| `parseTime=true` | Timestamps scan into `time.Time` rather than `[]byte`. |
| `loc=UTC` | The driver reads and writes timestamps as UTC. |
| `time_zone='+00:00'` | Pins the *server session* to UTC too, so SQL `now()` and Go-written times agree. Without it, session expiry and post dates drift by the server's offset. |
| `clientFoundRows=true` | Makes `UPDATE` report rows **matched** instead of rows **changed**. Without it, re-saving a record with unchanged values reports zero affected rows and the CMS reads that as "no such row" — saves fail with a not-found error. |

### Schema

Migrations are embedded per engine under `migrations/sql/postgres/` and
`migrations/sql/mysql/`, sharing one version sequence: `0007` is the same
change in both. `c.Migrate(ctx)` picks the right set and is safe to call on
every startup.

Adding a migration means writing **both** files. A unit test fails if a
version exists in one directory and not the other.

One operational difference is worth knowing: **MySQL and MariaDB commit DDL
implicitly.** Postgres runs each migration in a transaction and rolls back
cleanly on failure; on MySQL a failed migration can leave a partially applied
schema that needs manual repair. The version is only recorded on success, so
a re-run retries the whole file.

### Behaviour parity

The store tests run as a conformance suite against all three engines in
throwaway containers (`make test`, needs Docker; `make test-unit` skips
them). Where the engines would otherwise differ, the CMS pins the behaviour
rather than letting it vary:

- Keyed and enumerated columns are `VARCHAR(n)` on both, with identical
  lengths — InnoDB cannot index `TEXT` without a prefix length, and Postgres
  migration `0022` narrows the same columns so a value that saves on one
  engine saves on the other.
- `cms_media_folders.name` uses a binary collation on MySQL. Folder names are
  free text an editor types, and MySQL's default collation would treat
  "Photos" and "photos" as the same folder where Postgres does not. Slugs and
  email addresses are normalized before they reach an index, so they need no
  such pinning.

## The Styles menu (Tailwind-first)

The in-place editor's toolbar starts with a **Styles** dropdown: a
built-in **Headings** submenu (Headings 1–4, plus Paragraph to go back
down), followed by a short list of named,
on-brand text styles editors can apply to a selection. There is
deliberately no free color picker and no font-family menu — every
style applies CSS **classes**, so your stylesheet stays the single
source of design truth, and a later redesign restyles existing content
by changing the CSS rather than hunting down baked-in inline styles.

Heading 1 is in the submenu for the case that needs it: a region that
carries the page's own headline, where the `<h1>` lives *inside* the
region so an editor can style parts of it. Content under a page title
should still start at Heading 2 — the entry exists so a heading that has
been flattened to a paragraph can be put back, not as an invitation to
put several `<h1>`s on a page.

### The defaults

With no configuration, the menu ships a Tailwind-flavored default set
(the five colors fold into a "Color" submenu):

| Label          | Classes                  | Applies to  | Group |
|----------------|--------------------------|-------------|-------|
| Muted          | `text-slate-500`         | selection   | Color |
| Red            | `text-red-600`           | selection   | Color |
| Green          | `text-emerald-600`       | selection   | Color |
| Blue           | `text-blue-600`          | selection   | Color |
| White          | `text-white`             | selection   | Color |
| Highlight      | `bg-yellow-200`          | selection   |       |
| Serif          | `font-serif`             | selection   |       |
| Monospace      | `font-mono`              | selection   |       |
| Lead paragraph | `text-lg text-slate-600` | whole `<p>` |       |
| Small print    | `text-sm text-slate-500` | selection   |       |

### Safelist the classes (important)

Editor content lives in the database, and production Tailwind only
generates CSS for classes it finds while scanning your **source files** —
so every class the menu can apply must be safelisted, or applied styles
will silently not render in production. The toolbar's alignment buttons
also apply utility classes — `text-left/center/right` on blocks, and
`float-left mr-6`, `block mx-auto`, or `float-right ml-6` on images — as
does the image gear's display-width and roundness settings (`w-full`,
`w-2/3`, `w-1/2`, `w-1/3`, `h-auto`, `rounded-lg`, `rounded-2xl`,
`rounded-full`), the video slot's generated players (`w-full`,
`rounded-lg`, and `aspect-video` on YouTube/Vimeo embeds), and the
toolbar's table button (`w-full` on the table, `border-b-2
border-slate-300 p-2 text-left font-semibold` on header cells,
`border-b border-slate-200 p-2 align-top` on body cells, and the table
gear's variants: `border`, `p-1`, `p-4`, `w-auto`, `odd:bg-slate-50`;
each table is also wrapped in a `<div class="cms-table-wrap
overflow-x-auto">` so wide tables scroll in place on phones instead of
widening the page) — so safelist those regardless of which Styles menu
you ship. (The gear's Shadow presets apply the CMS's own
`cms-shadow-subtle`/`cms-shadow-strong` classes, styled by CSS that
`{{cmsHead}}` ships — no safelisting needed, and overridable by your
stylesheet.) For the default menu plus alignment and the image gear:

```js
// tailwind.config.js (Tailwind v3)
safelist: [
    "text-slate-500", "text-red-600", "text-emerald-600",
    "text-blue-600", "text-white", "bg-yellow-200", "font-serif",
    "font-mono", "text-lg", "text-slate-600", "text-sm",
    "text-left", "text-center", "text-right",
    "float-left", "float-right", "mr-6", "ml-6", "block", "mx-auto",
    "w-full", "w-2/3", "w-1/2", "w-1/3", "h-auto",
    "rounded-lg", "rounded-2xl", "rounded-full", "aspect-video",
    "border", "border-b", "border-b-2", "border-slate-200", "border-slate-300",
    "p-1", "p-2", "p-4", "align-top", "font-semibold", "w-auto",
    "odd:bg-slate-50", "overflow-x-auto", "object-cover",
],
```

```css
/* Tailwind v4: in your main CSS file */
@source inline("text-slate-500 text-red-600 text-emerald-600 text-blue-600 text-white bg-yellow-200 font-serif font-mono text-lg text-slate-600 text-sm text-left text-center text-right float-left float-right mr-6 ml-6 block mx-auto w-full w-2/3 w-1/2 w-1/3 h-auto rounded-lg rounded-2xl rounded-full aspect-video object-cover");
```

(The example site shows the full production pattern with the Tailwind v4
standalone CLI: a compiled site stylesheet built from the templates plus
this safelist — `examples/basic/assets/input.css`, regenerated with
`go generate .` — and the CMS's generated content stylesheet for classes
that live only in the database, wired through
`examples/basic/tailwind-content.sh`; see the next section.)

### Generated CSS for content classes (optional)

The safelist covers the classes the CMS's own UI can emit — but users
with the superadmin role can type *any* class into content through the
HTML source views, and no static safelist can cover that. Setting
`Config.Tailwind` closes the gap: after every content change the CMS
collects the class tokens from stored content and, when the set actually
changed, runs your Tailwind CLI over a synthetic file of those classes
and serves the result as a supplemental stylesheet
(`/cms/content-<hash>.css`, linked by `{{cmsHead}}`, where the hash is of
the generated CSS itself and so doubles as the cache buster). The
stylesheet is stored in the database, so every instance of a
multi-instance deployment serves the same artifact.

```go
Tailwind: &cms.TailwindConfig{
    // {content} = the synthetic class file the CMS writes;
    // {output} = where the command must write CSS.
    Command: []string{"tailwindcss", "-i", "assets/input.css",
        "-o", "{output}", "--content", "{content}"},
    Dir: "/path/to/site", // where your Tailwind config lives
    // Files your build scans for itself, so editing one triggers a
    // rebuild. Defaults to Config.TemplateFS; see below.
    Sources: os.DirFS("/path/to/site/templates"),
},
```

The command runs your Tailwind — your version, your plugins, and your
theme *as far as the input CSS you point it at declares one*. Builds are
asynchronous, serialized, and skipped when nothing that feeds them has
changed; a failed build logs and keeps the previous stylesheet. Setups
whose CLI can't take an ad-hoc content file (e.g. Tailwind v4
auto-detection) can point `Command` at a wrapper script that copies
`{content}` where their build expects it.

"Nothing that feeds them" includes the files your build scans on its own
account. If your input CSS has `@source "../templates"` — which the
section below explains why you want — then a template is a build input
the CMS cannot see, and an edit to one must invalidate the artifact.
`Sources` is how it does: its contents are fingerprinted into the build
key. It defaults to `Config.TemplateFS`, so the usual arrangement needs
no configuration; set it explicitly when your build reads more than the
templates (an `input.css` you edit, a theme file), or to an empty FS to
opt out.

Getting this wrong is unpleasant to debug, which is why it is not
optional. Edit a template to add `lg:grid-cols-6`, and with the class set
unchanged there is no rebuild — the stored stylesheet still holds
`sm:grid-cols-2` and not the `lg:` rule, and because it is linked *after*
your site stylesheet it beats the `lg:` rule that build did emit. The
element silently stays two columns at every width, and neither stylesheet
looks wrong on its own.

#### Keep the two builds on one theme

This is a second, independent Tailwind build, and `{{cmsHead}}` links its
output *after* the site stylesheet — so where the two disagree, this one
wins. A v4 wrapper script whose input is a bare `@import "tailwindcss"`
therefore re-emits `@layer theme { :root { … } }` with Tailwind's stock
values, overwriting anything the site build customized: redefine
`--font-sans` (or any other theme token) in the site's `input.css` and
the generated stylesheet silently resets it site-wide. The same gap runs
the other way for classes: a `font-display` an editor applies exists only
in the database, so it is this build's job to compile — and a build that
never saw `--font-display` emits nothing for it.

The fix is to keep theme tokens in a file both builds `@import`.
`cms init` already generates that arrangement — `assets/theme.css`, wired
into `assets/input.css` and `tailwind-content.sh` — so a scaffolded site
only has to edit the tokens. For the same thing built by hand, see
[custom fonts](QUICKSTART.md#custom-fonts) in the quickstart, including
why a wrapper script that stages its input in `mktemp` has to copy that
file in rather than reference it.

### Customizing the menu

Define your own entries with `Config.EditorStyles`; whatever you set
replaces the defaults entirely:

```go
c, err := cms.New(cms.Config{
    // ...
    EditorStyles: []cms.EditorStyle{
        // Inline styles wrap the selected text in a <span>.
        {Label: "Brand", Class: "text-brand-600"},
        {Label: "Subtle", Class: "text-slate-400"},
        {Label: "Highlight", Class: "bg-amber-100 px-1 rounded"}, // several classes are fine
        // A Block entry converts and styles the whole surrounding
        // block instead ("p", "h2", ...).
        {Label: "Lead paragraph", Class: "text-lg text-slate-600", Block: "p"},
        // Fonts are styles too: name the *role*, not the typeface. The
        // template must already load any webfont the class uses, and the
        // theme that defines --font-display has to reach both Tailwind
        // builds — see QUICKSTART.md#custom-fonts.
        {Label: "Display type", Class: "font-display"},
        // Entries sharing a Group fold into a submenu with that title,
        // placed where the group's first member appears. Handy once a
        // palette grows past a handful of entries.
        {Label: "Warning", Class: "text-amber-600", Group: "Callouts"},
        {Label: "Success", Class: "text-emerald-600", Group: "Callouts"},
    },
})
```

Rules of thumb:

- Keep the list short and named for meaning ("Brand", "Warning", "Display
  type"), not appearance ("Dark red #8b0000") — that's what makes it safe
  to hand to non-technical editors.
- Every class must exist in the site's CSS *and* be safelisted (when
  using Tailwind). Custom classes like `text-brand-600` come from your
  Tailwind theme; bespoke-CSS sites can point entries at their own
  classes — the mechanism doesn't require Tailwind.
- `EditorStyles: []cms.EditorStyle{}` (empty, non-nil) removes the custom
  entries; the dropdown itself stays, since it also carries the built-in
  Headings submenu.

## Snippets

While editing, the **tool rail** (the dark strip on the left edge of the
screen, visible only in edit mode) holds the creation tools: **＋
Section**, **Snippets**, and **Page** — the last opens a "Create a new
page" dialog (page name + page type from your configured
`PageTemplates`), creates the draft with a slug derived from the name,
and takes the editor straight to it.

A `PageTemplate` marked `Unlisted: true` is left out of that dialog for
everyone but superadmins (and the server refuses it on create for anyone
else) — for one-off templates that only ever back a single page, like a
home page or a staff directory, where offering them on every "new page"
would invite a second. Pages already using an unlisted template render
and edit exactly as before.

While editing, the edit bar also offers **Delete** for the current page
(with a confirmation; the home page can't be deleted — the server refuses
and the button doesn't appear on it).

The ⋯ menu's **Page settings** holds the two things a page carries that
aren't on the page: its title — what fills `<title>` in your template and
what search results show — and its meta description, which `{{cmsHead}}`
emits as `<meta name="description">`. Both are per-language and both are
staged like content, so they go live with the next Publish. On a
translated page the fields start empty with the default language shown
as a placeholder: type to translate, leave empty to keep following the
original. Posts keep the same two fields in their **⚙ Post settings**
pill instead, next to the date and listing image.

The rail's **Snippets** button opens a drawer of ready-made blocks: drag
one onto a rich region (or click to insert at the cursor), then edit its
text and images in place like any other content. Snippets come from two
places:

- **`Config.Snippets`** — per-customer components, versioned with your
  code. Nil gets a Tailwind-first default library: inline blocks
  (callout, call-to-action, two columns, quote, button link, video,
  flexible space, plus imported blocks — button pair, pill buttons,
  filled and outline single buttons in both shapes, quote with
  portrait, and four article layouts) and the section presets described
  under Sections below (hero, feature grid, stats, testimonials, FAQ,
  the three video layouts, call-to-action banner, plus imported
  presets — big headline, statement headline, kicker headline, team
  profiles, photo gallery, numbered features, pricing plans,
  achievements, client quotes, process steps, alternating steps, three
  product layouts, three skills layouts, two logo strips, two holding
  pages, and three map layouts — plus an inline map block); an empty
  slice ships none. The
  **flexible space** is invisible on the live site but shows as a
  striped, labelled band while editing — click it to set its height in
  pixels. The **video** block (and the video section presets) ship a
  "Click to add a video" slot: clicking it while editing offers the
  media library or a YouTube/Vimeo link, and the slot becomes a native
  `<video>` player or a privacy-enhanced embed. The imported
  photo-bearing blocks (gallery, products, team, quotes) ship the same
  affordance for images — a dashed **"Click to add a photo"** slot that
  opens the media library and becomes an `<img>` keeping the slot's
  shape (tile, wide, or circular portrait), cropped with
  `object-cover`. The map block and map sections ship a
  **"Click to add a map"** slot: paste a Google Maps link or its embed
  code, or just type an address, and the slot becomes a bounded maps
  `<iframe>` — no API key needed.
- **The admin UI** (`/admin/snippets`, admins only) — for blocks and
  section presets created after deployment.

```go
Snippets: []cms.Snippet{
    {Name: "Pricing card", HTML: `<div class="not-prose rounded-xl border p-6">
        <p class="text-3xl font-bold">$99</p>
        <p class="text-slate-600">per month</p></div>`},
},
```

Guidelines: wrap components in `not-prose` so Tailwind Typography doesn't
restyle their internals; avoid `<script>` and SVG (stripped when
editor-role users save); and **safelist every class that appears only in
snippets** — the same database-content rule as the Styles menu.

Set `Group` on a snippet to file it under a category: whenever the
loaded snippets carry two or more categories, the drawer grows a
dropdown that filters the list (per mode — a category whose entries are
all section presets isn't offered while inserting inline). Grouping is
config-only, like `EditorStyles` groups; admin-created and ungrouped
snippets pool under **Custom**. The default library is grouped as Basic,
Buttons, Quotes, Media, Article, Headlines, Features, Stats, Team,
Pricing, Products, Process, Skills, Partners, and Coming soon. The
magnifier in the drawer header opens a search field that filters the
list by name, combining with the category dropdown.

For the default library add:

```js
safelist: [
    "not-prose", "rounded-lg", "rounded-xl", "border", "border-blue-200",
    "border-slate-200", "bg-blue-50", "bg-blue-600", "bg-slate-50",
    "bg-slate-900", "bg-white", "p-4", "p-6", "px-5", "px-6", "py-2.5",
    "py-3", "text-blue-700", "text-blue-900", "text-white",
    "text-slate-500", "text-slate-600", "text-slate-700", "text-center",
    "text-sm", "text-lg", "text-xl", "text-2xl", "text-3xl", "text-4xl",
    "sm:text-5xl", "tracking-tight", "font-semibold", "font-bold",
    "mb-1", "mb-2", "mb-3", "mt-1", "mt-3", "my-4", "my-6", "my-8",
    "grid", "gap-6", "gap-8", "sm:grid-cols-2", "sm:grid-cols-3",
    "inline-block", "flex", "items-center", "justify-center",
    "w-full", "aspect-video", "border-2", "border-dashed",
    "border-slate-300",
    // Used by the imported block library (buttons, profiles, gallery,
    // numbered features, pricing, achievements, client quotes):
    "aspect-square", "bg-slate-200", "border-blue-600",
    "border-slate-900", "h-0.5", "hover:bg-blue-600",
    "hover:bg-slate-300", "hover:bg-slate-900", "hover:text-white",
    "mr-2", "mt-2", "mt-4", "mt-6", "mt-10", "px-8", "size-24",
    "sm:col-span-2", "sm:grid-cols-4", "text-5xl", "text-6xl",
    "sm:text-7xl", "text-slate-200", "text-slate-400", "text-slate-900",
    "tracking-widest", "uppercase", "w-10", "object-contain",
],
```

Deleting a snippet never changes pages that already inserted it — inserted
snippets are ordinary page content.

## Custom code blocks

Some blocks are not content: an availability calendar, a pricing
calculator, a third-party embed that needs a line of setup. These are
**custom code blocks** — markup with its own `<script>` — and they are
admin-only.

They are not stored in the page. What a page holds is an inert
placeholder naming a library entry:

```html
<div class="cms-snippet cms-code" data-cms-code="booking-widget"></div>
```

The code itself lives in a library behind the admin-only `/api/code`
endpoints, and a public render swaps each placeholder for the markup its
key names, keeping the placeholder's own `<div>` as the wrapper. A block
finds itself with `document.currentScript.closest(".cms-code")`, so the
same block used twice on one page still scopes to its own markup.

The split is what makes the feature safe and durable at once. Region and
section HTML is sanitized on every non-admin save: executable markup left
inline would either be stripped — silently deleting the widget the first
time an editor fixed a typo in the same section — or have to be
safelisted, which would hand every editor a script-injection hole. A
placeholder carries nothing executable, so it rides through the sanitizer
untouched.

To use one, an admin opens the **Snippets** drawer and clicks **Custom
code**, then picks a library entry or creates one. On the page the block
shows as a labelled card while editing; click it and press the `⟨/⟩`
button in the block chrome to edit its code, which saves immediately and
applies everywhere the key is used. The usual chrome moves and deletes
the block like any other. Nothing executes while the page is being
edited — an edit render leaves the placeholders alone — so the code runs
on the public page and in the admin's preview, and nowhere else.

Two things worth knowing:

- Deleting a library entry leaves placeholders that name it rendering as
  nothing; recreating the key brings them back.
- For code that belongs to a *page* rather than to a spot in it, the
  wrench menu's **Page CSS & JS** and **Site CSS & JS** panels are still
  the right tool — they inject into `<head>` and before `</body>`.

## Sections

Snippets live *inside* a region's column; **sections** let editors compose
the page itself. Place `{{cmsSections "body"}}` in a template **outside
any max-width container** (it renders full-bleed), and editors can add,
reorder, restyle, and delete full-width sections directly on the page:
each section has a control pill (↑ ↓ ＋ ⚙ ✕) in edit mode, new sections
start from a snippet or empty, and the ⚙ settings offer curated choices
only — background (Default / Light gray / Dark / Accent), content
width (Normal / Wide / Full width), and rounded corners (None / Small /
Medium / Large) by default — alongside a free-form background colour and
background image. The dialog splits into **Layout** and **Background**
tabs, with a live preview under both. A background image is cropped to
cover the section, so it comes with two sliders — across and down — that
place which part of it survives the crop, the difference between a
portrait's face and its shoulder. The ＋ buttons — on each
section, at the bottom of the sections area, and on the tool rail — open
an "Add a section" chooser: start empty, seed the section from any
snippet, or pick a **section preset** (below).

### Section presets

A config snippet that carries `Settings` is a section preset: a
one-click starting point for a whole section, not an inline block. The
editor lists presets first in the "Add a section" chooser (tagged
"Section") and hides them from the inline-insert drawer; choosing one
creates the section with the settings already applied along with the
starting HTML. The default library ships thirty-three — **Hero** (dark,
75% screen height, centered), **Feature grid**, **Stats**,
**Testimonials**, **FAQ**, **Full-width video**, **Text + video**,
**Video + text**, and **Call-to-action banner**; twenty-one
converted from a commercial block library: **Big headline**,
**Statement headline**, **Kicker headline**, **Team profiles**,
**Photo gallery**, **Numbered features**, **Pricing plans**,
**Achievements**, **Client quotes**, **Process steps**,
**Alternating steps**, **Product pair**, **Product cards**,
**Services list**, **Skill percentages**, **Skill circles**,
**Skill rings**, **Partner logos**, **Featured on**, **Coming soon**,
and **Maintenance mode**; and three map sections (**Full-width map**,
**Text + map**, **Map + text**) built on the map slot — so a
blank-canvas page can be composed into a landing page without touching
the settings dialog.

Settings use the section-settings vocabulary: `bg`, `width`, and
`corners` name `SectionStyles` option keys, `height` is
`"50"`/`"75"`/`"100"` (percent of the screen), `valign` is
`"center"`/`"bottom"`, and the free-form `bgcolor` (`#rrggbb`),
`bgimage` (URL) and `bgposition` — a pair of percentages across and down,
e.g. `"50% 20%"`, centered by default — work too. Unknown keys and
invalid values fall back to the defaults. Register your own next to
ordinary snippets:

```go
Snippets: append(
    append(snippets.DefaultSnippets(), snippets.DefaultSectionPresets()...),
    cms.Snippet{
        Name:     "Brand hero",
        Settings: map[string]string{"bg": "brand", "height": "100", "valign": "center"},
        HTML:     `<div class="cms-snippet text-center"><h1>Big claim</h1></div>`,
    },
),
```

Admins can create presets after deployment too: the snippet form at
`/admin/snippets` has a "Section preset" type with the curated settings
(background, width, rounded corners, height, vertical alignment); the
free-form `bgcolor`, `bgimage`, and `bgposition` settings are
config-only. Everything after creation is
ordinary: the preset's settings land in the section's ⚙ dialog, its HTML
is normal editable content, and neither remembers where it came from.

Each section renders as:

```html
<section class="BG-CLASSES CORNER-CLASSES">
  <div class="WIDTH-CLASSES">…editor content…</div>
</section>
```

**The sections-only page pattern.** For pages editors should compose
entirely themselves, offer a page type that is nothing but chrome plus a
sections area — no fixed regions at all:

```html
{{template "base" .}}

{{define "content"}}
{{cmsSections "sections"}}
{{end}}
```

Add it to `PageTemplates` (the example ships one as "Blank canvas") and
the New-page dialog offers it alongside your structured layouts. This is
deliberately a page *type*, not a per-page "hide region" switch: the CMS
can only empty a region's hole, never remove the wrapper markup your
template put around it, so suppressing fixed regions is the template's
decision. Pages can switch between types later in the admin — content in
regions both templates share (like the sections area) carries over.

Customize the choices with `Config.SectionStyles` (nil gets the
Tailwind-first defaults). Only the option *keys* are stored with content;
classes resolve at render time, so changing an option's classes restyles
every existing section using it:

```go
SectionStyles: &cms.SectionStyles{
    Backgrounds: []cms.SectionOption{
        {Key: "default", Label: "Default", Class: ""},
        {Key: "brand", Label: "Brand", Class: "bg-brand-900", ContentClass: "prose-invert"},
    },
    Widths: []cms.SectionOption{
        {Key: "normal", Label: "Normal", Class: "prose mx-auto max-w-3xl px-6 py-12"},
        {Key: "full", Label: "Full width", Class: "prose max-w-none px-6 py-12"},
    },
    Corners: []cms.SectionOption{
        {Key: "none", Label: "None (square)", Class: ""},
        {Key: "soft", Label: "Soft", Class: "rounded-2xl"},
    },
},
```

`Corners` rounds the `<section>` wrapper itself (where the background
paints), so a tinted band reads as a card. Leaving `Corners` nil keeps
the default choices (None / Small / Medium / Large →
`rounded-lg` / `rounded-2xl` / `rounded-3xl`); an explicit empty slice
(`[]cms.SectionOption{}`) removes the setting from the ⚙ dialog
entirely. The first option is the default and should normally be a
no-class "none".

`Paddings` is the vertical breathing room around a section's content, as
its own axis:

```go
Paddings: []cms.SectionOption{
    {Key: "normal", Label: "Normal", Class: "py-12"},
    {Key: "tight",  Label: "Tight",  Class: "py-3"},
    {Key: "none",   Label: "None",   Class: "py-0"},
},
```

It is optional and nil by default, which renders exactly as it did
before the setting existed — so keeping your `py-*` inside the `Widths`
presets goes on working. Configure it and the spacing becomes
independently choosable, which is worth doing for two reasons:

- Bundled into `Widths`, "the same measure but tighter" has no
  expression except a second width option, and the list multiplies by
  every spacing anyone wants.
- It is not the same thing as the section **height** setting, which is a
  `min-height` and can only ever make a section *taller*. An editor who
  wants less space reaches for height first, finds "Auto" already
  selected, and concludes the CMS cannot do it.

The class is emitted after the width class, so moving a `py-*` out of a
width preset and into `Paddings` needs no thought about ordering. The
first option is the default and is what content saved before you added
the axis resolves to — make it match whatever padding your width presets
used to carry and nothing already published will move.

Safelist the default section classes along with the rest:

```js
safelist: [
    "bg-slate-50", "bg-slate-900", "bg-blue-700", "prose", "prose-slate",
    "prose-invert", "mx-auto", "max-w-3xl", "max-w-5xl", "max-w-none",
    "px-6", "py-12", "rounded-lg", "rounded-2xl", "rounded-3xl",
],
```

## Blog & news

Blog and news are two feeds of the same engine. Enable them by giving the
CMS a post template:

```go
PostTemplate: cms.PageTemplate{File: "templates/pages/post.gohtml", Label: "Post"},
```

A post is an ordinary page underneath: its slug lives under `blog/` or
`news/` (e.g. `/blog/launch-day`), its body is regular region/section
content, and draft/publish works exactly like pages. That means **posts
are edited the same way pages are** — open the post on the site, press
Edit, and write in place with sections, snippets, and the media picker.
The post template never appears in the page-template choosers; posts are
created from the editor tool rail's **Post** button or under
**Blog & News** in the admin. The rail dialog takes everything up front:
title, feed (Blog or News), summary, date/time, and an image chosen
through the media picker (browse the library or upload) — that one image
does two jobs, becoming the background of the post's banner section and
its card in the listings. You land in the new draft with the banner
already on it, ready to write. While editing a post in place, a **⚙ Post
settings** pill pinned to the top-right of the page reopens those
settings, and holds the rest of them: title, summary, meta description,
date, byline, and listing image — the same fields as the admin's post
form. The date, the byline, and the image go live the moment they are
saved, like menus, since they order, sign, and illustrate the post
rather than being page content; the title, summary, and meta description
are page metadata and reach the site with the next Publish. On a
translated site the pill edits the language the page is rendered in, so
a French post gets a French title and summary. The creating user is
stamped as the author; feed and address changes live on the admin form.

Two of those settings are particular to posts:

- **Meta description.** A page has one description, which is its meta
  description. A post's description is its *summary* — the blurb on
  listing cards and in RSS — which is not always the line worth showing
  under a search result, so a post carries a second, per-locale meta
  description beside it. Empty means "use the summary", which is what
  every post that has never set one says, so nothing changes until it is
  filled in. `{{cmsHead}}` publishes whichever applies.
- **Show the author's name.** Off publishes the post under the site's
  name: the date still prints, the byline does not. The author is not
  changed — it stays recorded and comes back when the setting does — and
  templates need nothing new, because a post with its byline off reports
  an empty `.Author`, which the `{{with .Author}}` a byline is already
  written with skips.

A post has no header field. The banner at the top of a post is a
**section** in a region of its own, so it is edited on the page with the
same gear as every other section — background image and where to anchor
it, content width, rounded corners, height, vertical alignment, and any
text laid over it — and it follows the draft/publish flow rather than
going live the moment it is chosen. Creating a post with an image seeds
that section with it; creating one without leaves the region empty, and
its own "Add section" button is how a banner gets added later. Give the
post template a sections region above the article and there is nothing
else to wire up:

```html
{{cmsSections "header"}}
<article>
  {{if not (cmsHasSections "header")}}<h1>{{cmsTitle}}</h1>{{end}}
  {{with .Post}}<p>{{cmsDate .PublishedAt}}{{with .Author}} · {{.}}{{end}}</p>{{end}}
  {{cmsSections "sections"}}
</article>
```

A seeded banner starts with the post's title as the page's `<h1>`, centered
over the picture and in white or near-black depending on how dark the image
measures. `{{cmsHasSections "header"}}` reports whether an area actually
holds anything — which `cmsSections` cannot be used for, since an edit
render always emits its wrapper — so the template can leave the title to
the banner when there is one and print its own `<h1>` when there is not.
Showing it in both places is the thing to avoid — the same words, twice,
on one page. Either heading is edited where it sits: the banner's is a
line of text inside a section, and the no-banner one is `{{cmsTitle}}`.

Order tells the CMS what the regions are for: the **last** sections
region a template declares is its main content, where a new page's
starter section goes, and the **first** — when there is more than one —
is the banner, where a new post's chosen image goes. While editing, each
"Add section" button is tagged with its region's name so the two areas
are told apart.

The post template is a page template that additionally receives the
post's metadata as `.Post` (nil on ordinary pages).

Any template can list posts with `cmsPosts` — feed name and a limit
(≤ 0 for all), newest first. It does not paginate; for a listing that
walks the whole feed see [Paginated listings](#paginated-listings) below:

```html
{{range cmsPosts "blog" 12}}
  <a href="{{.URL}}">
    {{with .Thumbnail}}
      <img src="{{.URL}}" srcset="{{.Srcset}}" sizes="(min-width: 640px) 21rem, 100vw"
           width="{{.Width}}" height="{{.Height}}" alt="{{.Alt}}" loading="lazy">
    {{end}}
    <h2>{{.Title}}</h2>
    <p>{{cmsDate .PublishedAt}}</p>
    <p>{{.Summary}}</p>
  </a>
{{end}}
```

Each entry carries `Feed`, `Title`, `Summary` (the page description),
`URL`, `PublishedAt`, `Author`, `Thumbnail`, and `Draft`. `Author` is
empty on a post whose byline is switched off in its settings, so
`{{with .Author}} · {{.}}{{end}}` is all a template needs to honour that
— the date stays either way.

Print dates with `{{cmsDate .PublishedAt}}` rather than Go's own
`.Format`: it writes the date in the language the page is being rendered
in ("July 30, 2026", "30 juillet 2026"), where `.Format` names months in
English on every page whatever language it is written in. Add `"short"`
— `{{cmsDate .PublishedAt "short"}}` — for the abbreviated month in a
tight listing. English and French are built in; any other locale formats
as English, and a template is free to format its own dates instead.

#### Paginated listings

`cmsPosts` shows the newest N posts and stops; older posts are reachable
only by their own URL. For a listing that walks the whole feed, use
`cmsFeed` instead. It returns one page of posts plus everything needed to
link to the others, and reads the page number from `?page=` on the
request — the query runs `LIMIT`/`OFFSET` server-side, so a long feed
never loads more than a page's worth of rows:

```html
{{$feed := cmsFeed "blog"}}
{{range $feed.Posts}}
  <a href="{{.URL}}"><h2>{{.Title}}</h2></a>
{{end}}
{{cmsPagination $feed}}
```

Page size comes from `Config.PostsPerPage` (`CMS_POSTS_PER_PAGE`, default
10). Pass a number to override it for one listing: `{{cmsFeed "blog" 6}}`.

`{{cmsPagination $feed}}` emits a ready-made bar — Previous, numbered
page links, Next — under `cms-pager*` classes your stylesheet can
restyle, the same arrangement as `cmsNav`. To own the markup instead,
build it from the `FeedPage` fields:

| Field | What it holds |
| --- | --- |
| `.Posts` | this page's posts, the same `PostInfo` entries `cmsPosts` returns |
| `.Page`, `.TotalPages` | where this page sits; `TotalPages` is at least 1 |
| `.Total`, `.PerPage` | posts in the whole feed, and per page |
| `.PrevURL`, `.NextURL` | adjacent pages; empty at either end |
| `.Links` | the numbered bar: `.Number`, `.URL`, `.Current`, `.Ellipsis` |
| `.HasPages` | true when the feed runs to more than one page |

`.Links` shows every page in a short feed; in a long one it shows the
first page, the last, the two either side of the current one, and an
`.Ellipsis` entry for each gap. `examples/basic` has both versions —
`blog.gohtml` calls `cmsPagination`, `news.gohtml` draws its own.

A `?page=` past the end of the feed lands on the last real page rather
than an empty listing, and one that is not a number is page 1; neither is
a 404. Page 1's links omit `page=` altogether, so the canonical listing
URL stays the bare one, and any other query parameters on the URL are
carried across pages.

`Thumbnail` is the post's listing image with every rendition resolved,
and is nil when the post has none. It has `URL` (a default `src` at card
size), `Srcset` (every rendition, so the browser can pick a better one),
`Width` and `Height` (the intrinsic size of `URL`, which stops the page
reflowing as images arrive), and `Alt` from the media library. Write
`sizes` yourself: only your template knows the layout.

`ThumbnailURL` is still there for templates that just want one string,
and holds the same address as `.Thumbnail.URL`.

The public sees published posts only; logged-in editors also see drafts
(`Draft` is true, so listing templates can badge them — see
`examples/basic/templates/pages/blog.gohtml`). Listing pages themselves
are ordinary pages: create a page at slug `blog` or `news` using a
listing template.

RSS is served automatically at `/blog/rss.xml` and `/news/rss.xml` — the
twenty newest published posts, with the channel title and description
taken from the published listing page at `/blog` or `/news` when one
exists.

## French & multilingual content

Configure the site's languages and everything else follows:

```go
Locales: []string{"en", "fr"}, // first entry is the default
```

With one locale none of this surfaces. With more, the default language
lives at `/about` and the others under their code — `/fr/about`, `/fr`
for the French homepage, `/fr/news/rss.xml` for a localized feed. Put
`{{cmsLocales}}` in a template for a language switcher (each entry has
`.Code`, `.URL`, `.Active` — see the example site's header), and
`{{cmsHead}}` emits `hreflang` alternates automatically.

**Translating is in-place editing.** The edit bar shows an EN | FR
switcher; flip to FR and the page renders with English fallback wherever
no French exists yet — those regions get a dashed amber outline in edit
mode. Edit them (the English text is your starting point), save, and the
French version now exists; anything you don't touch keeps following the
English original, region by region. Publish applies to all languages at
once. A page's title and meta description translate the same way, in the
⋯ menu's "Page settings" (a post's, in its ⚙ pill): the fields start
empty with the English shown as a placeholder, and an empty field keeps
following the English. The ⋯ menu's "Remove this translation" reverts
the current language back to fallback. Menu labels are translated the same way:
right-click a nav item while on the French site and the label you type
is the French override.

In the admin, the page and post forms grow EN/FR tabs — title, summary,
and region source are per-language; address, template, feed, date, and
images live on the default tab. The admin UI itself speaks English and
(Canadian) French: a toggle by the logout button switches per user.

## Navigation menus

Drop `{{cmsNav "main"}}` into a template and the CMS renders the whole
nav — including one level of dropdown submenus, with the toggle behavior
built in. Use any menu key you like ("main", "footer", …). The markup
carries stable classes your stylesheet targets:

```html
<nav class="cms-nav">
  <button class="cms-nav-burger">…</button> <!-- hamburger; hidden above 768px -->
  <ul class="cms-nav-list">                <!-- horizontal flex list -->
    <li class="cms-nav-item">
      <a class="cms-nav-link cms-active" aria-current="page" href="/">Home</a>
    </li>
    <li class="cms-nav-item cms-nav-drop"> <!-- dropdown parent; .cms-open while open -->
      <button class="cms-nav-link cms-nav-toggle">Services<span class="cms-nav-caret"></span></button>
      <ul class="cms-nav-sub">…</ul>       <!-- the dropdown panel -->
    </li>
  </ul>
</nav>
```

On screens 768px and narrower the list collapses behind the hamburger
button: tapping it sets `.cms-nav-open` on the nav and the items open
in a left-aligned panel anchored below the button — the header keeps
its height — with dropdown submenus expanding inline inside the panel.
Override the `.cms-nav-burger` / `@media` rules in your CSS to change
the breakpoint or the look.

The CMS injects only the functional minimum (layout of the list, hiding
and positioning of dropdown panels, a neutral panel look, the mobile
collapse) — colors, spacing, and typography come from your CSS, and
every injected rule can be overridden by class.

A dropdown's parent is a `<button>` rather than an `<a>`, so it can be
opened from the keyboard, but it carries `.cms-nav-link` exactly like a
plain item — style that one class and both look the same. The reset that
undoes the browser's button styling is wrapped in `:where()`, so it has
zero specificity and your rule wins even though it names an element.

**Editing happens right on the nav.** While in edit mode, editors
right-click any menu item (long-press on touch) to set its text, link it
to a page (searchable picker; the URL follows slug renames, and the item
disappears if the page is deleted) or a web address, open it in a new
tab, or turn a top-level item into a dropdown — a label-only item that
holds other items, one level deep.

A "web address" is a site-relative path (`/contact`), an `https://`,
`http://`, `mailto:` or `tel:` URL, or a same-page anchor (`#pricing`) —
a one-page site's nav is made of anchors. A bare `#` is refused, since it
links nowhere. Note that `#pricing` only resolves on a page that has that
anchor, so on a site with more than one page `/#pricing` is usually what
is meant: the nav renders on every page, and the bare form does nothing
on the others. "＋" chips add items, and dragging
rearranges them, including into and out of a dropdown. Items linking to
draft pages show only for logged-in editors until the page is published,
and items linking to private pages (visibility "Private" in the page
form) stay editor-only even after publishing.
Menu changes have no draft state — every change applies to the whole
site immediately.

Prefer to own the markup completely? `cmsMenu` returns the raw entries —
`Label`, `URL`, `Active`, `NewTab`, `External`, and `Children` for
dropdown parents (whose own `URL` is empty):

```html
<nav class="flex gap-6">
    {{range cmsMenu "main"}}
    <a href="{{.URL}}"{{if .NewTab}} target="_blank" rel="noopener"{{end}}
       class="hover:text-slate-900{{if .Active}} font-semibold{{end}}">{{.Label}}</a>
    {{end}}
</nav>
```

Hand-rolled navs render the same data but aren't right-click editable —
the in-place menu editor only attaches to `cmsNav` markup.

## Shared regions: one footer for the whole site

A footer is the same on every page, so nobody should have to build one
page by page. `{{cmsShared "key"}}` is a rich region like `cmsRegion`,
except that its content is stored once for the site and rendered on every
page that uses the template:

```html
<footer class="border-t mt-16">
  <div class="mx-auto max-w-4xl px-6 py-8 text-sm text-slate-500">
    {{cmsShared "footer" "<p>&copy; Example Site</p>"}}
  </div>
</footer>
```

Put it in your layout and every page has an editable footer from the
first request — no page to create, nothing to seed. The optional second
argument is markup to show while the region is empty, so a fresh site
says something sensible; it disappears the moment an editor saves
content, and comes back if they empty the region again.

Editing works exactly as it does for page content: an editor clicks into
the footer on whichever page they are on, TinyMCE opens inline, snippets
and images drop in, and **Save draft** stages the change. What differs is
scope, and it is worth being clear with editors about it:

- **Nothing goes live until a Publish, but any page's Publish makes it
  live.** Shared content has no page of its own to be published from, so
  it rides along with whichever page the editor publishes — the same page
  they were editing it on. The status chip counts unpublished shared
  edits, so a page showing "Unpublished edits" may be reporting a footer
  change rather than one of its own.
- **Discard is page-only.** Discarding a page's draft leaves shared edits
  alone rather than silently throwing away work done elsewhere on the
  site. To undo a footer change before publishing, edit it back.
- Translations work per region as everywhere else: a shared region with
  no content in the current locale renders the default language with the
  usual dashed amber "not translated" badge, and editing it writes that
  locale's copy.

Templates may declare as many shared regions as they like
(`{{cmsShared "footer"}}`, `{{cmsShared "contact-strip"}}`), and a shared
region and a page region may safely have the same name — they are
different regions. Shared regions are rich HTML only; there is no shared
equivalent of `cmsText`, `cmsImage`, or `cmsSections`.

Under the hood the content lives in `cms_blocks` like everything else,
against one reserved system page (slug `__site`) that never appears in
the admin's Pages list and is not reachable as a URL. That is what lets
shared content reuse drafts, publishing, sanitization, and locales
unchanged.

## Site settings: brand, favicon & menu alignment

The wrench menu's **Site settings** entry lets editors set the site
name, an optional logo, a favicon, and the nav's alignment without
touching templates. Put `{{cmsBrand "Fallback Name"}}` where your header
shows the brand (typically inside your logo link):

```html
<a href="/">{{cmsBrand "Example Site"}}</a>
```

It renders a `span.cms-brand` holding the stored logo
(`img.cms-brand-logo`, sized to `1.6em` unless your CSS says otherwise)
and/or the stored site name (`span.cms-brand-text`). Save a logo and
clear the name for a logo-only brand; the fallback argument shows until
either is set. Saves are live immediately — like menus, there is no
draft state.

Where markup does not fit — the `<title>`, an `og:site_name` meta tag, a
copyright line — `{{cmsSiteName "Fallback Name"}}` gives the same stored
name as plain text, escaped for wherever it lands:

```html
<title>{{.Title}} — {{cmsSiteName "Example Site"}}</title>
```

Sites scaffolded before this function existed have the name written
into `base.gohtml` as a literal; swap it for the call above and the
title follows the dialog from then on.

The favicon is picked from the media library — PNG, JPEG, GIF, WebP, or
SVG, whichever you uploaded, unmodified rather than re-encoded — and
`{{cmsHead}}` emits it as `<link rel="icon">` on every page. It is the
one image field that uses the original file instead of the library's
downscaled web rendition, since a browser tab paints it at 16px and a
lossy re-encode buys nothing there. Leave it unset and the CMS emits
nothing at all, so a `<link rel="icon">` in your own `base.gohtml` (or
the browser's `/favicon.ico` guess) keeps working.

Menu alignment (left / center / right) adds a `cms-nav-left` /
`cms-nav-center` / `cms-nav-right` class to `cmsNav` markup, which makes
the nav grow (`flex:1`) inside your header's flexbox and justifies the
items within it. "Theme default" adds no class and leaves your layout
alone.

## Development & production: keeping a site out of search

The same dialog carries a **Site mode** switch, which only superadmins
see. A site in **development** is built and browsable but asks search
engines to leave it alone; switching it to **production** is what makes
it findable. `SeedAdmin` starts a brand-new site in development, so a
site is never indexed while it is still being written. Sites that
predate the setting stay in production — an upgrade never quietly pulls
a live site out of search.

In development the CMS:

- sends `X-Robots-Tag: noindex, nofollow` on **every** public response —
  pages, RSS feeds, and media. The header is what covers the files no
  `<meta>` tag can reach: search engines index a PDF or an image on its
  own, and the media proxy serves both.
- emits `<meta name="robots" content="noindex, nofollow">` from
  `{{cmsHead}}`.
- serves `/robots.txt` with `Disallow: /`.

In production it does none of those things, and — unless you have written
a robots.txt in the site settings (below) — does not claim `/robots.txt`
at all. If your app serves its own (with a `Sitemap:` line, or rules for
a particular crawler), it keeps serving it the moment the site goes live;
the CMS's copy is only there while the site is hidden.

Two things worth knowing:

**This hides the site; it does not protect it.** Everything is still
served to anyone with the address, and a crawler that ignores the rules
is not stopped by them. If an unfinished site must not be *reachable*,
that is HTTP auth, an IP allowlist, or not pointing a public name at it —
none of which the CMS does for you.

**Development mode is for a site that was never indexed.** `Disallow`
keeps crawlers out, and a crawler that never fetches a page never sees
the `noindex` on it. That is the right trade before launch, when there is
nothing in anyone's index yet. It is the wrong tool for pulling a site
that *has* been live back out of search results: for that the pages have
to stay crawlable so the `noindex` can be read, which means removing the
URLs through the search engine's own tools rather than flipping this
switch.

Everywhere else the mode stays visible: while a site is in development
the admin sidebar carries a **Development** stamp under the brand, on
every page, because the failure this feature invites is a finished site
nobody remembered to switch over.

### A robots.txt for the live site

Under the mode switch — and, like it, superadmin-only — the site settings
dialog carries a **robots.txt** box. Whatever you write there is served
verbatim at `/robots.txt` once the site is in production:

```
User-agent: *
Disallow: /private

Sitemap: https://example.com/sitemap.xml
```

On a site that has never stored one, the box opens on a working starting
point rather than empty — crawl everything except the admin, at whatever
`Config.AdminPath` you mounted it on:

```
User-agent: *
Disallow: /admin/
```

Nothing is stored until you save, and the note under the box says so: the
save is what takes `/robots.txt` over from the host app. Clear the box and
save to hand it back.

Three rules govern it, and they are worth stating plainly:

- **Empty means the CMS serves nothing there.** That is the default and
  the behaviour every existing site keeps: the path stays the host app's,
  and an app already serving its own file is unaffected by this feature
  existing. The suggested text above is only ever a suggestion — it takes
  a save to become real.
- **Development ignores it.** A hidden site serves its own `Disallow: /`
  no matter what is stored, because a file written for the live site
  would otherwise invite crawlers into an unfinished one. The box says so
  while the site is in development.
- **Only superadmins may edit it.** Admins and editors see the dialog and
  save the rest of it normally; their save carries the stored file
  through untouched, the same way it carries the mode.

Responses are sent `Cache-Control: no-store`, so an edit is live at once
as far as any proxy is concerned. Crawlers cache `robots.txt` on their
own schedule regardless — Google for about a day — so a change takes
effect on their next fetch, not yours.

This is a text box, not a validator: the CMS caps the length and
normalizes line endings, and otherwise serves what you typed. A
`Disallow` that hides a page from search does not make it unreachable —
that is the same caveat as development mode, and worth re-reading above.

### A sitemap

Above the robots.txt box, and superadmin-only in the same way, is
**Publish a sitemap at /sitemap.xml**. With it on, the CMS generates a
sitemap of every page it serves:

```xml
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://example.com/</loc>
    <lastmod>2026-08-15T12:04:11Z</lastmod>
  </url>
  <url>
    <loc>https://example.com/about</loc>
    <lastmod>2026-07-02T09:30:00Z</lastmod>
  </url>
</urlset>
```

**What it lists** is every page that is published *and* publicly visible
— posts included, since a post is a page. Drafts and private pages are
left out, which means the sitemap says exactly what an anonymous visitor
could reach anyway.

**`lastmod` is the live page's date, not the editor's.** It moves when a
page is published, unpublished, renamed, or has its visibility changed,
and stays put while someone works on a draft — draft edits are stored
separately from the page row. So editing all afternoon without publishing
does not tell search engines the page changed, which is correct: it
hasn't.

**Multi-locale sites list every language**, and each URL carries the
`hreflang` alternates — including `x-default` — that `{{cmsHead}}` already
emits in the page head, so the two agree about which URLs exist:

```xml
<url>
  <loc>https://example.com/about</loc>
  <xhtml:link rel="alternate" hreflang="en" href="https://example.com/about"/>
  <xhtml:link rel="alternate" hreflang="fr" href="https://example.com/fr/about"/>
  <xhtml:link rel="alternate" hreflang="x-default" href="https://example.com/about"/>
</url>
```

**Turning it on advertises it.** If you have written a robots.txt, a
`Sitemap:` line pointing at it is added to what gets served — unless your
file already names a sitemap, in which case yours is left alone. An empty
robots.txt box stays empty: the sitemap does not make the CMS start
claiming `/robots.txt`.

Three more things worth knowing:

- **New sites get it; upgrades don't.** `SeedAdmin` turns it on for a
  brand-new site, exactly as it starts one in development mode. An
  existing site is left alone — if your app already serves its own
  `/sitemap.xml`, an upgrade must not quietly take the address over.
  Turn it on in the dialog when you want the CMS's.
- **A site in development publishes none.** It is asking not to be
  crawled; handing out a list of every URL it has is the opposite of
  that. The switch stays where you left it and takes effect at the
  production flip.
- **The document is cached for five minutes.** A page published a moment
  ago may not appear until then. That is a bound on cost, not a freshness
  promise — crawlers refetch on their own far slower schedule. The URLs
  come from `Config.SiteURL` when you set one, and otherwise from the
  requesting host, so an install reached by several names answers each
  with its own.

Past 50,000 URLs — pages × locales — the extra pages are left out and a
warning is logged. Splitting into a sitemap index is the fix, and does
not exist yet.

## Host data in CMS pages

Some of a page isn't content. A dealership's "fresh on the lot" strip, a
shop's best sellers, a clinic's next available appointments — those come
from the host's own tables, change when the data changes rather than when
someone rewrites a sentence, and have fields no arrangement of text slots
models honestly. `Config.TemplateFuncs` lets page templates call the
host's own functions alongside the `cms*` ones, so one page can mix both:

Start with the type the templates want. Returning display-ready strings
keeps `{{.Price}}` in the template instead of a pipeline of formatting
funcs:

```go
type Vehicle struct {
    Name, Detail, Price, Terms, PhotoURL, URL string
}

type VehicleStore struct{ db *sql.DB }

func (s *VehicleStore) Featured(ctx context.Context, n int) []Vehicle { ... }
func (s *VehicleStore) Count(ctx context.Context) int                 { ... }
```

Register as many functions as the site needs — it is a `FuncMap`, so a
page may call several:

```go
vehicles := &VehicleStore{db: db}

c, err := cms.New(cms.Config{
    // ...
    // Declares the names page templates may call. These implementations
    // are used as-is by any render that supplies no replacement.
    TemplateFuncs: template.FuncMap{
        "featuredVehicles": func(n int) []Vehicle { return vehicles.Featured(context.Background(), n) },
        "vehicleCount":     func() int { return vehicles.Count(context.Background()) },
    },
    // Optional: rebind the same names per request, so each query carries
    // that request's context and is cancelled when the visitor leaves.
    RequestFuncs: func(r *http.Request) template.FuncMap {
        return template.FuncMap{
            "featuredVehicles": func(n int) []Vehicle { return vehicles.Featured(r.Context(), n) },
            "vehicleCount":     func() int { return vehicles.Count(r.Context()) },
        }
    },
})
```

In the template they look like any other func, and sit happily beside the
`cms*` ones — the heading is content, the cards are data:

```html
<h2>{{cmsText "inventory-title"}}</h2>          <!-- marketing owns the words -->
<p>{{cmsText "inventory-lede"}}</p>
<p>{{vehicleCount}} on the lot right now</p>

<div class="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
  {{range featuredVehicles 3}}
    <a href="{{.URL}}">
      <img src="{{.PhotoURL}}" alt="{{.Name}}" loading="lazy">
      <h3>{{.Name}}</h3>
      <p>{{.Detail}}</p>
      <span>{{.Price}}</span> <span>{{.Terms}}</span>
    </a>
  {{else}}
    <p>Nothing on the lot right now — check back shortly.</p>
  {{end}}
</div>
```

An editor cannot type over a card, and nobody has to remember to edit the
home page when a car sells. Give `{{range}}` an `{{else}}` branch: an
empty result is a normal state, and without one the section renders as a
heading over nothing.

Rules of thumb:

- **`TemplateFuncs` declares the names.** Page templates are parsed
  against it, so every function a template calls must appear there — a
  name that doesn't fails at startup with `function "featuredVehicles"
  not defined`. `RequestFuncs` only *replaces* implementations for one
  render: names it omits keep their declared version, and a name
  appearing only there is unreachable, because no template could have
  compiled a call to it. `New` refuses `RequestFuncs` without
  `TemplateFuncs` for that reason.
- **The `cms*` prefix is reserved.** `New` rejects a host func whose name
  starts with `cms`, so a later release can add template funcs without
  silently losing to (or shadowing) a host's.
- **A func registered only in `TemplateFuncs` is shared by every render**
  and must be safe for concurrent use. Anything needing per-request state
  belongs in `RequestFuncs`.
- **They run with the host's full trust.** A func returning
  `template.HTML` bypasses the editor's content sanitizer entirely, so
  never interpolate untrusted input into one.
- **Their markup lives in your template files**, which your Tailwind build
  already scans — so data-driven markup needs no safelisting and never
  involves the generated content stylesheet.
- Host funcs are bound in the admin's page and post **previews** too, so a
  preview renders what a visitor gets.

Hosts that validate templates at build time with `render.CheckTemplate`
should switch to `render.CheckTemplateFuncs`, passing the same map, or
every template calling a host func reports a spurious "function not
defined".

`examples/wheels` shows the whole arrangement: `vehicles.go` is the store
and the two maps, and `templates/pages/home.gohtml` ranges over the result
between two `cmsText` slots.

## Roles & permissions

Accounts have one of three **roles**, which encode trust:

- **editor** — works on content, gated by per-user permissions (below).
- **admin** — everything: all permissions implicitly, plus site-wide and
  per-page CSS/JS (written into pages unsanitized), the custom-code block
  library, and user management without restriction.
- **superadmin** — admin plus snippet management (snippets are raw HTML
  injected into every editor), unlisted page templates in the new-page
  dialog, the development/production switch (whether the site may be
  indexed at all), and the admin panel's Pages section (everyone
  else works on pages in place on the public site, where every page
  feature is available; the admin list is the superadmin's index of
  pages that aren't linked anywhere).

What an *editor* may work on is a set of per-user **permissions**,
toggled on their page under Users:

| Permission | Grants |
|---|---|
| Blog posts | the blog feed: creating, editing, publishing blog posts |
| News | the news feed, the same way |
| Pages, menus & site settings | site pages, navigation menus, and the non-code site settings, all through the in-place editor (the site mode stays superadmin-only) |
| User management | managing *editor* accounts (see below) |

Everything follows from the grant: nav entries and dashboard cards the
user can't act on don't render, the routes 403 regardless, and on the
public site the in-place editor appears only on pages the user may edit
— a blogs-only editor gets the editor (and sees drafts) on `blog/…`
pages and the ordinary published render everywhere else. The media
library stays open to every logged-in user; content work needs it.

A migration grants existing editors the three content permissions, so
an upgrade changes nothing until you start unticking boxes.

**User management without the admin role is deliberately bounded.** An
editor with that grant manages editor accounts only: admin accounts are
out of reach entirely (including their passwords and two-factor
resets), the role select offers only "editor", and they can neither
grant nor revoke a permission they don't hold themselves. Escalation by
way of the users page is a dead end.

Deployments can declare **custom permissions** for functionality they
gate themselves — either implicitly through an admin section's
`Permission` field (see [Custom admin pages](#custom-admin-pages)), or
standalone:

```go
Permissions: []cms.PermissionDef{
    {Key: "vehicles", Label: "Manage vehicles"},
},
```

Each declared permission becomes a checkbox on the user form; grants
live in the `cms_user_permissions` table. Check them in your own
handlers with `auth.User.Can` — inside an admin section that's
`admin.UserFrom(r).Can("vehicles")` (admin roles always pass). Keys are
lowercase identifiers (`[a-z][a-z0-9_-]*`, max 64 chars); the built-in
keys `blogs`, `news`, `pages`, and `users` are reserved.

A permission can also be made to **bind admins**: declare it (or its
section) with `AdminsNeedGrant: true` and the admin role stops holding
it implicitly — the checkbox must be ticked for an admin exactly as for
an editor, and only superadmins hold it regardless. Check these in your
own handlers with `auth.User.HasGrant` rather than `Can`. Every
declaration of one key must agree on the flag; `cms.New` refuses a
mismatch.

## Custom admin pages

Deployments often need admin pages the CMS doesn't ship — reports,
imports, integration settings. Register them with `Config.AdminSections`:
each section is a plain `http.Handler` the CMS mounts at
`{AdminPath}/x/{Path}/` **inside** the admin's middleware chain, so
login enforcement, sessions, CSRF validation, and security headers are
guaranteed — a custom page can't accidentally ship without them.

```go
AdminSections: []cms.AdminSection{
    {Path: "reports", NavLabel: "Reports", Handler: reportsHandler},
    {Path: "billing", NavLabel: "Billing", AdminOnly: true, Handler: billingHandler},
},
```

- **`Path`** — one URL segment of letters, digits, or `- . _ ~`; the
  section root is served at `{AdminPath}/x/reports/` (the `/x/` namespace
  guarantees host sections never collide with built-in admin routes, now
  or after upgrades). The bare URL without the trailing slash redirects
  to the slashed form, so relative links inside the section resolve
  correctly.
- **`NavLabel`** — adds a link to the admin top bar; leave empty for
  routable-but-unlisted pages.
- **`NavAfter`** — places the nav link directly under a built-in sidebar
  entry: `"dashboard"`, `"pages"`, `"posts"`, `"media"`, `"snippets"`, or
  `"users"`. Leave empty for the default position, after the built-in
  entries. Sections naming the same anchor keep their registration order
  — the wheels example anchors all six of its sections to `"dashboard"`,
  so the dealership's own tools top the sidebar.
- **`NavCount`** — a `func(context.Context) (int, error)` supplying the
  number beside the nav link, with the same leader line as the built-in
  entries. Called on every admin page render, so keep it a cheap query;
  an error is logged and renders as zero. Leave nil for no count — right
  for links that trigger an action rather than open a list. The wheels
  example counts its vehicles, active sales people and staff, leads, and
  push-ready feeds this way (`navcounts.go`).
- **`Dashboard`** — puts a card for the section on the admin dashboard,
  ahead of the built-in cards, linking to the section root. A
  `cms.DashboardCard` carries a `Title` (defaults to `NavLabel`), a
  one-line `Description`, a `Count` func run once per dashboard render
  (defaults to `NavCount`; the same error-renders-as-zero contract),
  and an optional `Note` func supplying a short dynamic line under the
  description — freshness or urgency in the host's words ("Oldest
  undelivered: 2 days"; errors log and show nothing). Give the card the
  number that asks for attention — the wheels example's Vehicles card
  counts what still needs making ready, while its nav link counts the
  whole lot. Visibility follows the section's own rules (`AdminOnly`,
  `Permission`, `AdminsNeedGrant`).
- **`AdminOnly`** — editors get 403 and no nav link.
- **`Permission`** — restricts the section to users holding the named
  permission (see [Roles & permissions](#roles--permissions)). Naming a
  built-in permission reuses it; any other key *declares* a custom
  permission, which appears as a checkbox on the admin's user form,
  labelled with `NavLabel`. The wheels example gates its inventory
  manager this way: `{Path: "inventory", NavLabel: "Inventory",
  Permission: "vehicles", Handler: ...}`.
- **`AdminsNeedGrant`** — makes `Permission` bind the admin role too:
  admins see and open the section only with the grant ticked on their
  user page, exactly as editors do; superadmins always pass. Several
  sections may share one permission key to switch on and off together —
  the wheels example groups its Sales people and Staff sections under a
  single `"team"` grant this way.
- **`Handler`** — sees section-relative paths (`/` at the root), so it can
  serve sub-routes and its own static assets beneath it.

`cms.New` rejects a config with malformed, duplicate, or handler-less
sections; call `admin.ValidateSections` yourself to fail even earlier
(e.g. in a test).

Inside a handler, four helpers from `github.com/tsawler/cms/admin`
integrate with the admin UI:

| Helper | Purpose |
|---|---|
| `admin.UserFrom(r)` | the logged-in `*auth.User` (never nil in a section) |
| `admin.CSRFToken(r)` | token for the `csrf_token` field in POST forms |
| `admin.SetFlash(r, msg)` | one-time message on the next admin page load |
| `admin.RenderPage(w, r, title, body)` | wrap trusted HTML in the admin chrome |

```go
func reportsPage(w http.ResponseWriter, r *http.Request) {
    user := admin.UserFrom(r)   // logged-in *auth.User, never nil here
    body := fmt.Sprintf(`<h1>Reports</h1>
        <p>Hello %s.</p>
        <form method="post" action="refresh">
            <input type="hidden" name="csrf_token" value="%s">
            <button type="submit" class="cms-btn">Refresh</button>
        </form>`,
        template.HTMLEscapeString(user.Name),
        template.HTMLEscapeString(admin.CSRFToken(r)))

    // Wraps body (trusted host HTML) in the admin chrome: top bar,
    // nav, flash messages, stylesheet.
    admin.RenderPage(w, r, "Reports", template.HTML(body))
}

func refresh(w http.ResponseWriter, r *http.Request) {
    // CSRF was already validated before this ran.
    admin.SetFlash(r, "Refreshed.") // shown on the next admin page load
    http.Redirect(w, r, admin.SectionPath(r), http.StatusSeeOther)
}
```

Things to know:

- **POST forms need the CSRF token.** The admin middleware rejects unsafe
  methods without it; include `admin.CSRFToken(r)` as the `csrf_token`
  field (or an `X-CSRF-Token` header from JS).
- **No inline scripts or styles.** The admin serves a strict
  Content-Security-Policy without `unsafe-inline`; serve JS/CSS as files
  from the section's own handler. With an embedded FS that's one route:

  ```go
  //go:embed assets
  var assetsFS embed.FS

  mux := http.NewServeMux()
  mux.Handle("GET /assets/", http.FileServerFS(assetsFS))
  // referenced from the page as <script src="assets/app.js" defer></script>
  ```
- **Redirect with full paths.** The handler sees stripped paths, so
  `http.Redirect` with a relative URL resolves against the wrong base;
  use `admin.SectionPath(r)`, the section's browser-facing base URL
  (e.g. `/admin/x/reports/`), appending a segment for sub-routes.
- `admin.RenderPage` always writes 200 and inserts `body` unescaped —
  it's your code's HTML, escape any user data you interpolate into it.
  For other status codes or a fully custom look, write the response
  directly; the CSS classes in `admin.css` (`cms-btn`, `cms-card`,
  `cms-muted`, ...) are available either way.

The example app registers a working section — see `reportsSection` in
`examples/basic/main.go`.

For *modifying* built-in admin pages there is deliberately no template
override mechanism (it would couple deployments to internal template
data and break silently on upgrades). The supported paths are the
existing configuration knobs (`Snippets`, `EditorStyles`,
`SectionStyles`, ...) — and when those don't cover a need, a config
option added to the CMS itself.

## The dashboard

The admin's landing page is built for the people who use it daily:

- **Host section cards come first** — whatever the deployment registered
  with `AdminSection.Dashboard` (above), visible by the section's own
  permission rules. For most editors these cards *are* the dashboard.
- **The built-in cards — Pages, Snippets, Users, Media — are
  superadmin-only.** They are site plumbing; editors and admins do their
  content work in place on the public site. **Blog & News** renders for
  anyone holding a blogs or news grant.
- **A traffic chart** shows the public site's page views for each of the
  last seven days (UTC), for every logged-in user, with the week's five
  most-viewed pages listed beside it.

The traffic numbers come from the CMS itself: serving a page to an
anonymous visitor upserts a per-day, per-path counter in
`cms_page_views` — no cookies, no IPs, no user agents stored, so there
is nothing here a privacy policy needs a section for. Logged-in CMS
users aren't counted (staff aren't traffic), and neither are crawlers
that identify themselves. `Migrate` prunes counters older than ninety
days on every startup.

## Bot protection

The public site is read-only (GET/HEAD only), so the bot-facing surface
is the admin login form — and the forgot-password form, when a Mailer is
configured (see the next section). Three layers protect both:

- **Login throttling** (always on): five failed attempts per email+IP in
  fifteen minutes, then 429 responses until the window passes.
- **Honeypot** (always on): a visually hidden form field; anything that
  fills it gets the ordinary wrong-password error and a throttle strike.
- **CAPTCHA** (opt-in): a proof-of-work challenge verified against a
  self-hosted [Cap](https://capjs.js.org) server — no third-party
  service, no tracking, and the widget script is served by your own Cap
  instance rather than a CDN.

To enable the CAPTCHA, run Cap (docker image `tiago2/cap`, see
`examples/basic/docker-compose.yml`), open its dashboard, log in with
the container's `ADMIN_KEY`, create a site key, and configure:

> **Pin the widget version.** Set `WIDGET_VERSION` and `WASM_VERSION` on the
> Cap container rather than leaving them at `latest`. The widget is a browser
> dependency of the login page: on `latest`, an upstream release can change
> its API or its CSP requirements under a deployment that has not changed at
> all. The compose file pins known-good versions; treat a bump like any other
> dependency upgrade and log in against it before committing.
>
> The login page's CSP also carries `'unsafe-eval'`, because Cap's
> instrumentation challenge calls `eval()`. It is scoped to that one page —
> every other admin page gets a strict `default-src 'self'` policy. Turning
> instrumentation off for the site key in the Cap dashboard removes the need
> for it, at the cost of the anti-automation layer.

```go
c, err := cms.New(cms.Config{
    // ...
    Captcha: &cms.CaptchaConfig{
        URL:     "https://cap.example.com", // browser-facing Cap server
        SiteKey: "your-site-key",
        Secret:  "your-secret",
        // InternalURL: "http://cap:3000", // optional: server-to-server
        //                                 // address, e.g. inside Docker
        // Visible: true,                  // optional: show Cap's checkbox
        //                                 // widget instead of solving the
        //                                 // challenge invisibly (default)
    },
})
```

With `Captcha` set, the login page solves the challenge invisibly in the
background (Cap's programmatic mode) — users never see a CAPTCHA. Set
`Visible: true` to show Cap's interactive checkbox widget instead. Either
way the admin CSP is extended to admit exactly the Cap origin, and the
login handler verifies the submitted token server-side before checking
credentials. If
the Cap server rejects the token, the login fails; if the Cap server is
*unreachable*, the login proceeds with a logged warning — an outage of
the CAPTCHA backend shouldn't lock admins out, and the throttle still
applies. Host applications can reuse the verification client
(`captcha.New`, `Client.Verify`) for their own forms.

## Password resets ("forgot password")

The login page can offer a self-service reset: ask for a link, get an
email, follow it, set a new password. The CMS owns the whole flow — the
token table, the two pages, the throttling, and the wording of the email
— and the host supplies exactly one thing: delivery.

```go
// Satisfy the one-method interface with whatever your application
// already sends mail through:
type cmsMailer struct{ m *yourMailer }

func (a cmsMailer) Send(ctx context.Context, to, subject, text, html string) error {
    return a.m.Deliver(ctx, to, subject, text, html)
}

c, err := cms.New(cms.Config{
    // ...
    Mailer: cmsMailer{yourAppMailer},
})
```

That split is deliberate. Delivery policy — SMTP or an API, which From
address, a development mail sink — already lives in the host, and the
CMS should not duplicate it. The message content goes the other way:
the CMS authors the email so every deployment sends the same
carefully-worded thing, in particular the part that never confirms
whether an address has an account.

**Nil means off.** With no `Mailer` configured, the login page shows no
"Forgot your password?" link and the reset routes answer 404. A reset
form that could never send its email would *look* broken; absent, the
feature is honestly off. A host that wants the flow without real
delivery (development, tests) can pass a Mailer that logs.

What the flow does, so you don't have to re-derive it from the code:

- **Tokens are single-use and expire after an hour** (`auth.ResetTTL`).
  Asking again revokes the earlier link, so at most one works at a time.
  The database stores only a SHA-256 of the token — the email holds the
  only usable copy, so a leaked backup or a curious query replays
  nothing.
- **No account oracle.** Every address gets the same confirmation page,
  and the email (when there is one) is sent in the background so known
  addresses are not measurably slower than unknown ones. Deactivated
  accounts get the same page and no email.
- **The same defenses as the login form**: its own throttle (five
  requests per email+IP per fifteen minutes — this endpoint makes the
  server send email, which is worth as much to a spammer as a password
  guess is to a thief), the honeypot, and the CAPTCHA when one is
  configured.
- **A typo doesn't burn the link.** Password validation failures
  re-render the form with the token intact; the token is only consumed
  once a valid new password is installed.
- Both pages and the email are translated when the site has a French
  locale, like the rest of the admin.

The email link is built from `Config.SiteURL` when set, otherwise from
the request's own scheme and host — the same rule as every other
absolute link the CMS mints. If the admin is reached behind a proxy that
rewrites `Host`, set `SiteURL`.

`examples/wheels` wires this up for real (see `adminMailer` in its
`mail.go`): the adapter is five lines around the mailer the site already
had, and it only sets `Config.Mailer` when mail is actually configured —
so a fresh checkout without SMTP credentials gets the honest absent
state rather than emails that vanish into a log.

## Account settings and two-factor login

Every logged-in user has an account page at `/admin/settings` (their name
in the sidebar links to it) with three things on it: editing their own
name and email, changing their own password — behind the current one, so
a walked-away session can't quietly take over the account — and turning
**two-factor login** on or off.

Email edits get the same validation as the admin's user form (a
well-formed address that no other account holds) and additionally
require the current password, because the address is the login
identifier and where reset links go. A name change alone needs no
password. Role and active status are deliberately not on this page —
nobody adjusts their own powers outside the admin-only `/admin/users`.

Two-factor uses ordinary TOTP authenticator apps (Google Authenticator,
1Password, Authy, …): the settings page shows a QR code and a
manual-entry key, and enrollment only saves once a live code from the
app confirms it — nobody locks themselves out by enabling it with an app
that never scanned the code. From then on, the password step of login
parks the user at a 6-digit code challenge; the session only exists
after the code passes. Nothing to configure: the feature is per-user and
always offered.

What the flow does, so you don't have to re-derive it from the code:

- **Codes are single-use.** Each accepted code's 30-second time step is
  recorded (`totp_last_step`), and a login only succeeds by moving it
  forward — a shoulder-surfed or phished code replays nothing. One step
  of clock skew either side is accepted, like everything else that
  speaks TOTP.
- **The challenge is throttled** like the login form (five wrong codes
  per account+IP per fifteen minutes), and it expires: a correct
  password opens a five-minute window to produce the code, then the
  half-login goes stale.
- **Turning it off requires the password** — a borrowed session alone
  can't strip the second factor.
- **Lost phone?** An admin editing the user (`/admin/users/…`) gets a
  "Reset two-factor authentication" checkbox; the user logs in with
  just their password and can re-enroll.

## Running the examples

There are two, and they are deliberately opposites — start with whichever
matches how you intend to build.

| | [`examples/basic`](examples/basic) | [`examples/mariadb`](examples/mariadb) |
| --- | --- | --- |
| Database | Postgres (MySQL/MariaDB by profile) | MariaDB |
| Styling | Tailwind, compiled via `go generate` | hand-written CSS in the layout |
| Build step | needs the `tailwindcss` CLI | none |
| Also shows | media library, login CAPTCHA, blog & news, a custom admin page | overriding `SectionStyles`/`EditorStyles` for a non-Tailwind host |
| Ports | app 4000, db 5433 | app 4200, db 3309 |

They use different ports and separate compose projects, so both can run at
once.

### The basic example

```sh
cd examples/basic
docker compose up -d      # Postgres on localhost:5433, Cap on localhost:3300
go run .
```

The compose file also carries MySQL and MariaDB, started only when asked for
by profile. To run the example against one of them:

```sh
docker compose --profile mysql up -d      # MySQL on localhost:3307
CMS_DIALECT=mysql go run .

docker compose --profile mariadb up -d    # MariaDB on localhost:3308
CMS_DIALECT=mysql DATABASE_URL='cms:cms@tcp(localhost:3308)/cms?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&clientFoundRows=true' go run .
```

`CMS_DIALECT=mysql` covers MariaDB too — the two share one dialect. The
example supplies the required DSN settings when `DATABASE_URL` is unset; if
you set it yourself, include them all (see
[MySQL and MariaDB DSN settings](#mysql-and-mariadb-dsn-settings)).

To try the login CAPTCHA: open <http://localhost:3300>, log in with the
`ADMIN_KEY` from `docker-compose.yml`, create a site key, and set
`CAP_URL=http://localhost:3300`, `CAP_SITE_KEY`, and `CAP_SECRET` in the
environment or `.env`. Without them the example runs without CAPTCHA.
The challenge is solved invisibly by default; `CAP_WIDGET=visible` shows
the checkbox widget instead.

Then open <http://localhost:4000/admin/> and log in with
`admin@example.com` / `password123` (development defaults; override with
`CMS_ADMIN_EMAIL` and `CMS_ADMIN_PASSWORD`).

Login sessions end when the browser closes unless "Remember me" is
ticked, which keeps the login for `cms.Config.RememberFor` — 30 days by
default, or `CMS_REMEMBER_DAYS` (measured in days) when set.

Both examples call `SeedHomePage` after `SeedAdmin`, so a fresh database
serves a published page at `/` rather than a 404. It is a no-op once the
site has any content — see [Quick start](#quick-start).

### The MariaDB example

The smallest host that does something real: no Tailwind, no build step, no
object store, and a hand-written stylesheet in `templates/base.gohtml`.

```sh
cd examples/mariadb
docker compose up -d      # MariaDB on localhost:3309
go run .
```

Then <http://localhost:4200/admin/>, same development credentials. Its own
[README](examples/mariadb/README.md) covers the one thing a non-Tailwind
host has to know: the built-in `SectionStyles` and `EditorStyles` are
Tailwind class names, so a plain-CSS site overrides both with classes its
own stylesheet defines.

### Environment variables

The example loads variables from a `.env` file — `examples/basic/.env`
first, falling back to one at the repo root — without overriding
anything already set in the real environment. The `CMS_TAILWIND_*`,
`S3_*`, `CAP_*`, and `CMS_SESSION_REDIS_*` variables plus `CMS_SITE_URL`,
`CMS_REMEMBER_DAYS`,
`CMS_POSTS_PER_PAGE`, `CMS_ADMIN_PER_PAGE` and the two `CMS_MEDIA_*` knobs are read by
`cms.ConfigFromEnv`, which any host can
use to fill those `Config` fields; the rest are the example's own.
Everything read:

| Variable | Default | Purpose |
| --- | --- | --- |
| `CMS_DIALECT` | `postgres` | Database engine: `postgres`, or `mysql` for both MySQL and MariaDB. Selects the driver and the default `DATABASE_URL`. |
| `DATABASE_URL` | Postgres: `postgres://cms:cms@localhost:5433/cms?sslmode=disable`; MySQL: `cms:cms@tcp(localhost:3307)/cms?parseTime=true&loc=UTC&time_zone='+00:00'&clientFoundRows=true` | Connection string, matching `docker-compose.yml`. A MySQL DSN you supply yourself must carry all four settings in the default. |
| `ADDR` | `:4000` | HTTP listen address. |
| `CMS_ADMIN_EMAIL` | `admin@example.com` | Email for the admin account seeded on first run. |
| `CMS_ADMIN_PASSWORD` | `password123` | Password for that seeded admin account. |
| `CMS_REMEMBER_DAYS` | `30` | How long a "Remember me" login lasts, in days. An invalid or non-positive value is a startup error. |
| `CMS_SESSION_REDIS_ADDR` | unset (sessions stay in the database) | Redis server address, `host:port`. Setting it moves login-session storage from the `cms_sessions` table to Redis (keys prefixed `cms_session:`) and makes the other `CMS_SESSION_REDIS_*` variables relevant. |
| `CMS_SESSION_REDIS_PASSWORD` | unset (no auth) | Password for that Redis server. |
| `CMS_SESSION_REDIS_DB` | `0` | Redis logical database number. An invalid or negative value is a startup error. |
| `CMS_SITE_URL` | unset (each request's own host) | The site's canonical public address, e.g. `https://example.com`. Used wherever a link has to work away from the page it was made on: the media library's **Copy link**, RSS item links, and hreflang alternates. Set it when the request's `Host` would be wrong — behind a proxy that rewrites it, or when the admin is reached by a different name than the public site. A value with no scheme is taken as `https`. |
| `CMS_POSTS_PER_PAGE` | `10` | How many posts a paginated `{{cmsFeed}}` listing shows per page. An invalid or non-positive value is a startup error. A template can override it per listing with `{{cmsFeed "blog" 6}}`. |
| `CMS_ADMIN_PER_PAGE` | `25` | How many rows a paginated admin list shows per page (Blog & News, and Pages). Separate from `CMS_POSTS_PER_PAGE`: an editor's table wants more rows than a public listing. An invalid or non-positive value is a startup error. |
| `CMS_MEDIA_WEBP_QUALITY` | `0.3` | Lossy WebP quality for image variants, in (0, 1]. A non-numeric value is a startup error. |
| `CMS_MEDIA_MAX_VIDEO_MB` | `512` | Video upload size cap in MB. A non-numeric value is a startup error. |
| `CMS_MEDIA_ADOPT` | `when-empty` | Whether `Migrate` rebuilds the media library from the object store: `when-empty` (only when the database holds no media), `reconcile` (check every startup), or `off`. Any other value is a startup error. |
| `CMS_TAILWIND_COMMAND` | unset (rebuilds disabled) | Content-driven Tailwind rebuild command: a space-separated argv with `{content}` and `{output}` placeholders (see [Generated CSS for content classes](#generated-css-for-content-classes-optional) and `tailwind-content.sh`). |
| `CMS_TAILWIND_DIR` | unset | Working directory for `CMS_TAILWIND_COMMAND`. |
| `S3_ENDPOINT` | unset (media library disabled) | S3-compatible object-store endpoint. Setting it enables the media library and makes the other `S3_*` variables relevant. |
| `S3_BUCKET` | — | Bucket for uploaded media. |
| `S3_ACCESS_KEY` | — | Object-store access key. |
| `S3_SECRET` | — | Object-store secret key. |
| `S3_REGION` | derived from the endpoint | Region, if your provider needs it spelled out. |
| `S3_KEY_PREFIX` | unset | Prefix that namespaces this site's keys inside a shared bucket. It also scopes media adoption and the public-read policy, so set it whenever the bucket is shared. |
| `S3_APPLY_PUBLIC_POLICY` | unset | Set to `1` to apply a public-read bucket policy during `Migrate` (one-time setup; idempotent). |
| `CAP_URL` | unset (CAPTCHA disabled) | Browser-facing URL of the Cap server. Setting it enables the login CAPTCHA and makes the other `CAP_*` variables relevant. |
| `CAP_SITE_KEY` | — | Site key created in the Cap dashboard. |
| `CAP_SECRET` | — | Secret for that site key. |
| `CAP_INTERNAL_URL` | unset (uses `CAP_URL`) | Server-to-server Cap address, e.g. inside Docker. |
| `CAP_WIDGET` | unset (invisible challenge) | Set to `visible` to show Cap's checkbox widget on the login form. |

## Working on the in-place editor

The editor script served to logged-in editors is a generated bundle:
`editor/editor.js` is built from the ES modules in `editor/src/` (plus
`styles.css`/`light.css`) and committed, so consumers of the module never
need Node or a bundler — `go get` and `go:embed` keep working as before.

Think of `editor/src/` as source code and `editor/editor.js` as its
compiled output, like `.go` files and a binary. `go generate ./editor`
is the compile step — Go never runs it for you, so it's part of the
edit-build-run loop whenever the editor's source changes.

**When to run `go generate ./editor`:** after editing any file under
`editor/src/` — and only then. Changes to Go code, templates, or admin
files don't involve the bundle. Consumers of the module never run it at
all; the committed `editor.js` ships ready-made.

The full contributor loop, from the repo root:

```sh
# 1. change something in the editor's source
$EDITOR editor/src/dialogs.js

# 2. regenerate the committed bundle
go generate ./editor

# 3. restart the dev server — the bundle is embedded at compile
#    time, so a running server keeps serving the old one
cd examples/basic && go run .

# 4. commit the source change and the regenerated bundle together
git add editor/src/dialogs.js editor/editor.js
```

When iterating, skip the repeated step 2 and leave a watcher running in
a spare terminal instead (you still restart the server to embed the
result):

```sh
go run -C editor/build . -watch       # rebuild on every save
```

The trap to know about: forgetting to regenerate is silent. Everything
still compiles and runs — it just serves the stale bundle, and your
`src/` change doesn't appear in the browser. If the editor is ignoring
an edit you're sure you made, a missed `go generate ./editor` (or a
missed server restart) is the likely cause.

The build tool is a nested Go module (`editor/build`) wrapping
[esbuild's Go API](https://pkg.go.dev/github.com/evanw/esbuild/pkg/api),
so it needs only the Go toolchain and stays out of the cms module's
dependency graph. Don't edit `editor/editor.js` by hand; it is
overwritten by the next build (and marked `linguist-generated`).

## Notes for host applications

- **Style your rich regions.** The CMS never injects styles into your
  pages, so headings, lists, and blockquotes created by editors look
  however *your* CSS says. With Tailwind this matters: Preflight resets
  h1–h6/ul/blockquote to plain text, so give rich regions typography
  styles (e.g. the `@tailwindcss/typography` plugin's `prose` class, as
  `examples/basic` does) or editors' formatting will be invisible.
- **Safelist the editor's style classes** — see
  [The Styles menu](#the-styles-menu-tailwind-first) above; skipping this
  makes applied styles silently invisible in production Tailwind builds.
- Template file extensions are the host's choice (`.gohtml` plays best
  with editor tooling) — the CMS loads whatever paths you configure. But
  pages store their template's path, so renaming template files under an
  existing database needs a one-time fixup, e.g.
  `UPDATE cms_pages SET template_name = replace(template_name, '.tmpl', '.gohtml');`
- All tables are prefixed `cms_`, so the CMS can share a database with the
  host app.
- **Login sessions can live in Redis instead of the database.** Set
  `Config.Redis` (or `CMS_SESSION_REDIS_ADDR`) and sessions move from the
  `cms_sessions` table to Redis under `cms_session:` keys, with Redis's own
  key expiry replacing the hourly cleanup sweep. Everything else stays in
  the database; leave it unset and sessions do too.
- `Migrate` is safe to run on every startup and from multiple instances
  concurrently. On MySQL and MariaDB it is not transactional — see
  [Schema](#schema).
- **`Config.ObjectStore` replaces S3 entirely.** Implement
  `media.ObjectStore` (`Put`/`Get`/`Delete`/`PublicURL`) and the media
  library uses it instead of a bucket — local disk in development, or any
  storage you already run. When set, `Config.S3` is ignored.
- A few `Config` fields have no environment variable and are easy to miss:
  `SessionLifetime` (default 24h; `RememberFor` extends it for "Remember
  me"), `Logger` (defaults to `slog.Default()`), and `ObjectStore` above.
  The struct's godoc documents every field.
- SVG uploads are accepted as images (they act as their own renditions at
  every size — no rasterizing). Because an SVG viewed directly
  is a document that can run scripts, uploads are rejected unless they
  are free of active content (`<script>`, `<foreignObject>`, `on*`
  attributes, `javascript:`/non-image `data:` hrefs, DTD internal
  subsets), and the media proxy serves `image/svg+xml` with a
  script-blocking `Content-Security-Policy`. If you serve media straight
  from a public bucket/CDN instead of the proxy, that header is in your
  hands — configure it there if SVG uploads concern you.
- Set `Config.SecureCookies = true` in production (HTTPS).
- `Config.AdminPath` (default `/admin`) is where `Handler()` serves the
  admin area. If you wire `Admin()` yourself instead, the mount point
  must match it.

## License

This project is licensed under the **Mozilla Public License 2.0** — see
[LICENSE](LICENSE).

MPL-2.0 is file-level copyleft: you can import this module into an
application under any license, including a closed-source or commercial
one, and mounting the CMS in your app does not affect how you license
that app. What the license asks is that if you modify this project's own
source files and distribute the result, those modified files stay under
MPL-2.0 and their source is made available.

### Third-party components

Bundled and imported components keep their own licenses:

- **TinyMCE 6** (`editor/tinymce/`) — MIT, vendored and self-hosted; this
  is the last MIT-licensed release, and the version bundled here is
  deliberately pinned to it. See `editor/tinymce/license.txt`.
- **pako 2.1.0** (`admin/static/pako_inflate.min.js`) — MIT AND Zlib,
  vendored for the Cap proof-of-work captcha; the license banner is in
  the file itself.
- Go dependencies are MIT, BSD, Apache-2.0, or MPL-2.0 — see `go.mod` and
  each module's own license.
