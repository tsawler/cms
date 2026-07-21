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
will silently not render in production. The toolbar's alignment buttons
also apply utility classes — `text-left/center/right` on blocks, and
`float-left mr-6`, `block mx-auto`, or `float-right ml-6` on images — as
does the image gear's display-width and roundness settings (`w-full`,
`w-2/3`, `w-1/2`, `w-1/3`, `h-auto`, `rounded-lg`, `rounded-2xl`,
`rounded-full`) — so safelist those regardless of which Styles menu you
ship. (The gear's Shadow presets apply the CMS's own
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
    "rounded-lg", "rounded-2xl", "rounded-full",
],
```

```css
/* Tailwind v4: in your main CSS file */
@source inline("text-slate-500 text-red-600 text-emerald-600 text-blue-600 text-white bg-yellow-200 font-serif font-mono text-lg text-slate-600 text-sm text-left text-center text-right float-left float-right mr-6 ml-6 block mx-auto w-full w-2/3 w-1/2 w-1/3 h-auto rounded-lg rounded-2xl rounded-full");
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
  code. Nil gets a Tailwind-first default library: inline blocks
  (callout, call-to-action, two columns, quote, button link, flexible
  space) plus the section presets described under Sections below (hero,
  feature grid, stats, testimonials, FAQ, call-to-action banner); an
  empty slice ships none. The **flexible space** is invisible on the live
  site but shows as a striped, labelled band while editing — click it to
  set its height in pixels.
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
snippets** — the same database-content rule as the Styles menu. For the
default library add:

```js
safelist: [
    "not-prose", "rounded-lg", "rounded-xl", "border", "border-blue-200",
    "border-slate-200", "bg-blue-50", "bg-blue-600", "bg-slate-50",
    "bg-slate-900", "bg-white", "p-4", "p-6", "px-5", "px-6", "py-2.5",
    "py-3", "text-blue-700", "text-blue-900", "text-white",
    "text-slate-500", "text-slate-600", "text-slate-700", "text-center",
    "text-sm", "text-lg", "text-xl", "text-3xl", "text-4xl",
    "sm:text-5xl", "tracking-tight", "font-semibold", "font-bold",
    "mb-1", "mb-2", "mb-3", "mt-1", "mt-3", "my-4", "my-6", "my-8",
    "grid", "gap-6", "gap-8", "sm:grid-cols-2", "sm:grid-cols-3",
    "inline-block",
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
an "Add a section" chooser: start empty, seed the section from any
snippet, or pick a **section preset** (below).

### Section presets

A config snippet that carries `Settings` is a section preset: a
one-click starting point for a whole section, not an inline block. The
editor lists presets first in the "Add a section" chooser (tagged
"Section") and hides them from the inline-insert drawer; choosing one
creates the section with the settings already applied along with the
starting HTML. The default library ships six — **Hero** (dark, 75%
screen height, centered), **Feature grid**, **Stats**, **Testimonials**,
**FAQ**, and **Call-to-action banner** — so a blank-canvas page can be
composed into a landing page without touching the settings dialog.

Settings use the section-settings vocabulary: `bg` and `width` name
`SectionStyles` option keys, `height` is `"50"`/`"75"`/`"100"` (percent
of the screen), `valign` is `"center"`/`"bottom"`, and the free-form
`bgcolor` (`#rrggbb`) and `bgimage` (URL) work too. Unknown keys and
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
(background, width, height, vertical alignment); the free-form `bgcolor`
and `bgimage` settings are config-only. Everything after creation is
ordinary: the preset's settings land in the section's ⚙ dialog, its HTML
is normal editable content, and neither remembers where it came from.

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

Drop `{{cmsNav "main"}}` into a template and the CMS renders the whole
nav — including one level of dropdown submenus, with the toggle behavior
built in. Use any menu key you like ("main", "footer", …). The markup
carries stable classes your stylesheet targets:

```html
<nav class="cms-nav">
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

The CMS injects only the functional minimum (layout of the list, hiding
and positioning of dropdown panels, a neutral panel look) — colors,
spacing, and typography come from your CSS, and every injected rule can
be overridden by class.

**Editing happens right on the nav.** While in edit mode, editors
right-click any menu item (long-press on touch) to set its text, link it
to a page (searchable picker; the URL follows slug renames, and the item
disappears if the page is deleted) or a web address, open it in a new
tab, or turn a top-level item into a dropdown — a label-only item that
holds other items, one level deep. "＋" chips add items, and dragging
rearranges them, including into and out of a dropdown. Items linking to
draft pages show only for logged-in editors until the page is published.
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
- **`AdminOnly`** — editors get 403 and no nav link.
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
    http.Redirect(w, r, "/admin/x/reports/", http.StatusSeeOther)
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
  use the browser-facing URL (`/admin/x/reports/`).
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

## Bot protection

The public site is read-only (GET/HEAD only), so the bot-facing surface
is the admin login form. Three layers protect it:

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

```go
c, err := cms.New(cms.Config{
    // ...
    Captcha: &cms.CaptchaConfig{
        URL:     "https://cap.example.com", // browser-facing Cap server
        SiteKey: "your-site-key",
        Secret:  "your-secret",
        // InternalURL: "http://cap:3000", // optional: server-to-server
        //                                 // address, e.g. inside Docker
    },
})
```

With `Captcha` set, the login page renders the Cap widget (the admin CSP
is extended to admit exactly that origin), and the login handler
verifies the submitted token server-side before checking credentials. If
the Cap server rejects the token, the login fails; if the Cap server is
*unreachable*, the login proceeds with a logged warning — an outage of
the CAPTCHA backend shouldn't lock admins out, and the throttle still
applies. Host applications can reuse the verification client
(`captcha.New`, `Client.Verify`) for their own forms.

## Running the example

```sh
cd examples/basic
docker compose up -d      # Postgres on localhost:5433, Cap on localhost:3300
go run .
```

To try the login CAPTCHA: open <http://localhost:3300>, log in with the
`ADMIN_KEY` from `docker-compose.yml`, create a site key, and set
`CAP_URL=http://localhost:3300`, `CAP_SITE_KEY`, and `CAP_SECRET` in the
environment or `.env`. Without them the example runs without CAPTCHA.

The example reads S3 credentials from a `.env` file at the repo root
(`S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET`, optional
`S3_REGION` and `S3_KEY_PREFIX`); without one, the media library is
simply disabled. `CMS_MEDIA_WEBP_QUALITY` (a value in (0, 1], e.g.
`0.5`) overrides the WebP quality used for image variants; unset uses
the default 0.3.

Then open <http://localhost:4000/admin/> and log in with
`admin@example.com` / `password123` (development defaults; override with
`CMS_ADMIN_EMAIL` and `CMS_ADMIN_PASSWORD`).

Login sessions end when the browser closes unless "Remember me" is
ticked, which keeps the login for `cms.Config.RememberFor` (default 24h;
the example maps `CMS_REMEMBER_HOURS` onto it).

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
- `Migrate` is safe to run on every startup and from multiple instances
  concurrently.
- Set `Config.SecureCookies = true` in production (HTTPS).
- `Config.AdminPath` must match wherever you mount `Admin()`; it defaults
  to `/admin`.
