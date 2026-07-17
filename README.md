# cms

An embeddable content management system for Go web applications. Import it
as a module, hand it a Postgres pool, and mount its handlers — no external
files, no separate install. See [DESIGN.md](DESIGN.md) for the full
architecture and build plan.

**Status: phase 5 (snippets).** Auth, user management, page CRUD with
draft/publish, public rendering through the host's own templates, the
media library (any S3-compatible bucket, automatic resizing, folders and
search), in-place editing (TinyMCE 6 — the last MIT release, vendored and
self-hosted), the curated Styles menu, and the snippet palette are all
working: log in, browse the site, click Edit, change text and images
directly on the page, drop in ready-made blocks, save drafts, publish.
Blog/news and FR/EN localization are next.

To make an image editable in place, add `data-cms-image` to the tag:

```html
<img data-cms-image="hero" src="{{cmsImage "hero"}}" alt="...">
```

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
{{cmsSections "body"}}                  <!-- editor-composed full-width sections -->
<head> ... {{cmsHead}} ... </head>      <!-- meta description + per-page CSS -->
... {{cmsScripts}} </body>              <!-- per-page JS -->
```

## The Styles menu (Tailwind-first)

The in-place editor's toolbar starts with a **Styles** dropdown: a short
list of named, on-brand text styles editors can apply to a selection.
There is deliberately no free color picker and no font-family menu — every
style applies CSS **classes**, so your stylesheet stays the single source
of design truth, and a later redesign restyles existing content by
changing the CSS rather than hunting down baked-in inline styles.

### The defaults

With no configuration, the menu ships a Tailwind-flavored default set:

| Label          | Classes                  | Applies to |
|----------------|--------------------------|------------|
| Muted          | `text-slate-500`         | selection  |
| Red            | `text-red-600`           | selection  |
| Green          | `text-emerald-600`       | selection  |
| Blue           | `text-blue-600`          | selection  |
| White          | `text-white`             | selection  |
| Highlight      | `bg-yellow-200`          | selection  |
| Serif          | `font-serif`             | selection  |
| Monospace      | `font-mono`              | selection  |
| Lead paragraph | `text-lg text-slate-600` | whole `<p>` |
| Small print    | `text-sm text-slate-500` | selection  |

### Safelist the classes (important)

Editor content lives in the database, and production Tailwind only
generates CSS for classes it finds while scanning your **source files** —
so every class the menu can apply must be safelisted, or applied styles
will silently not render in production. For the default menu:

```js
// tailwind.config.js (Tailwind v3)
safelist: [
    "text-slate-500", "text-red-600", "text-emerald-600",
    "text-blue-600", "text-white", "bg-yellow-200", "font-serif",
    "font-mono", "text-lg", "text-slate-600", "text-sm",
],
```

```css
/* Tailwind v4: in your main CSS file */
@source inline("text-slate-500 text-red-600 text-emerald-600 text-blue-600 text-white bg-yellow-200 font-serif font-mono text-lg text-slate-600 text-sm");
```

(The example site uses the Tailwind Play CDN, which generates CSS in the
browser and needs no safelist — fine for development, not for
production.)

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
        // template must already load any webfont the class uses.
        {Label: "Display type", Class: "font-display"},
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
- `EditorStyles: []cms.EditorStyle{}` (empty, non-nil) removes the Styles
  dropdown altogether.

## Snippets

While editing, the **tool rail** (the dark strip on the left edge of the
screen, visible only in edit mode) holds the creation tools: **＋
Section**, **Snippets**, and **Page** — the last opens a "Create a new
page" dialog (page name + page type from your configured
`PageTemplates`), creates the draft with a slug derived from the name,
and takes the editor straight to it.

While editing, the edit bar also offers **Delete** for the current page
(with a confirmation; the home page can't be deleted — the server refuses
and the button doesn't appear on it).

The rail's **Snippets** button opens a drawer of ready-made blocks: drag
one onto a rich region (or click to insert at the cursor), then edit its
text and images in place like any other content. Snippets come from two
places:

- **`Config.Snippets`** — per-customer components, versioned with your
  code. Nil gets a Tailwind-first default library (callout,
  call-to-action, two columns, quote, button link, flexible space); an
  empty slice ships none. The **flexible space** is invisible on the live
  site but shows as a striped, labelled band while editing — click it to
  set its height in pixels.
- **The admin UI** (`/admin/snippets`, admins only) — for blocks created
  after deployment.

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
snippets** — the same database-content rule as the Styles menu. For the
default library add:

```js
safelist: [
    "not-prose", "rounded-lg", "rounded-xl", "border", "border-blue-200",
    "border-slate-200", "bg-blue-50", "bg-blue-600", "bg-slate-50",
    "bg-slate-900", "p-4", "p-6", "px-5", "py-2.5", "text-blue-900",
    "text-white", "text-slate-600", "text-slate-700", "text-center",
    "font-semibold", "font-bold", "mb-1", "mb-3", "mt-3", "my-4", "my-6",
    "grid", "gap-6", "sm:grid-cols-2", "inline-block",
],
```

Deleting a snippet never changes pages that already inserted it — inserted
snippets are ordinary page content.

## Sections

Snippets live *inside* a region's column; **sections** let editors compose
the page itself. Place `{{cmsSections "body"}}` in a template **outside
any max-width container** (it renders full-bleed), and editors can add,
reorder, restyle, and delete full-width sections directly on the page:
each section has a control pill (↑ ↓ ＋ ⚙ ✕) in edit mode, new sections
start from a snippet or empty, and the ⚙ settings offer curated choices
only — background (Default / Light gray / Dark / Accent) and content
width (Normal / Wide / Full width) by default. The ＋ buttons — on each
section, at the bottom of the sections area, and on the tool rail — open
an "Add a section" chooser: start empty, or seed the section from any
snippet.

Each section renders as:

```html
<section class="BG-CLASSES">
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
},
```

Safelist the default section classes along with the rest:

```js
safelist: [
    "bg-slate-50", "bg-slate-900", "bg-blue-700", "prose", "prose-slate",
    "prose-invert", "mx-auto", "max-w-3xl", "max-w-5xl", "max-w-none",
    "px-6", "py-12",
],
```

## Navigation menus

Templates render menus from CMS data — the CMS never emits nav markup, so
any design works:

```html
<nav class="flex gap-6">
    {{range cmsMenu "main"}}
    <a href="{{.URL}}"{{if .NewTab}} target="_blank" rel="noopener"{{end}}
       class="hover:text-slate-900{{if .Active}} font-semibold{{end}}">{{.Label}}</a>
    {{end}}
</nav>
```

Entries carry `Label`, `URL`, `Active` (links to the page being viewed),
`NewTab`, and `External`; use any menu key you like ("main", "footer", …).
Editors manage the main menu from the tool rail's **Menu** panel: items
link to a page (picked from a list; the URL follows slug renames, and the
item disappears if the page is deleted) or to a custom address. Items
linking to draft pages show only for logged-in editors until the page is
published. Menu changes have no draft state — saving applies to the whole
site immediately.

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

- **Style your rich regions.** The CMS never injects styles into your
  pages, so headings, lists, and blockquotes created by editors look
  however *your* CSS says. With Tailwind this matters: Preflight resets
  h1–h6/ul/blockquote to plain text, so give rich regions typography
  styles (e.g. the `@tailwindcss/typography` plugin's `prose` class, as
  `examples/basic` does) or editors' formatting will be invisible.
- **Safelist the editor's style classes** — see
  [The Styles menu](#the-styles-menu-tailwind-first) above; skipping this
  makes applied styles silently invisible in production Tailwind builds.
- All tables are prefixed `cms_`, so the CMS can share a database with the
  host app.
- `Migrate` is safe to run on every startup and from multiple instances
  concurrently.
- Set `Config.SecureCookies = true` in production (HTTPS).
- `Config.AdminPath` must match wherever you mount `Admin()`; it defaults
  to `/admin`.
