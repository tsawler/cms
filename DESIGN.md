# CMS — Design Document

An embeddable content management system for Go web applications, aimed at
non-technical end users and built to be extended per customer.

## Requirements

1. Site pages can use any CSS framework, or none at all (most will use Tailwind).
2. CMS-specific CSS must not depend on a framework and must be isolated enough
   to coexist with any page CSS, including bespoke.
3. Written in Go; pages use Go templates.
4. In-place editing — text and images are edited directly on the page.
5. News and blog support.
6. Image uploads stored on an S3-compatible bucket.
7. Additional JS and CSS on a per-page basis.
8. Drag-and-drop snippets (pre-written HTML the user can edit) onto pages.
9. Intended for non-technical users.
10. Data stored in Postgres.
11. Optional French/English content, English default.
12. **Distributed as a Go module** so per-customer functionality can be added
    without forking the core.

## Core concept

The CMS is a library, not an application. Each customer project is a small Go
app that imports the module; the module hands back `http.Handler`s plus
template helpers:

```go
c, err := cms.New(cms.Config{
    DB:        pool,                    // pgx pool
    S3:        s3cfg,                   // endpoint, bucket, keys (MinIO, Spaces, R2, AWS)
    Locales:   []string{"en", "fr"},    // "en" is always the default
    Templates: myTemplates,             // the customer's own Go templates
})

mux.Handle("/admin/", http.StripPrefix("/admin", c.Admin())) // admin UI + JSON API
mux.Handle("/", c.Pages())                                   // public page rendering
```

Customer-specific features are added through extension points (below), never
by forking the core. One deployment per customer — no multi-tenancy inside the
module, which keeps the data model and auth simple.

## Module layout

```
github.com/tsawler/cms
├── cms.go            // Config, New(), the public Handlers
├── auth/             // users, passwords, roles, login throttling
├── content/          // pages, regions, versions, publishing        (phase 2)
├── blog/             // posts (blog + news as two feeds of one type) (phase 6)
├── media/            // uploads, S3 client, image variants           (phase 3)
├── render/           // template integration, region injection       (phase 2)
├── snippets/         // snippet registry                             (phase 5)
├── i18n/             // locale resolution, translation store, en/fr admin strings (phase 7)
├── admin/            // admin handlers + embedded UI assets (embed.FS)
├── editor/           // in-place editing JS/CSS (embedded, framework-free) (phase 4)
├── internal/         // session store and other non-public plumbing
├── migrations/       // embedded SQL, run via c.Migrate(ctx)
└── examples/basic/   // runnable reference site — doubles as documentation
```

Everything static (admin UI, editor JS/CSS, migrations) ships inside the
module via `embed.FS`, so `go get` is the entire install. All database tables
are prefixed `cms_` so the module can share a database with the host app.

## Key design decisions

### 0. Tailwind-first, not Tailwind-only

The core is framework-agnostic (nothing in rendering, editing, or storage
depends on any CSS framework), but Tailwind is the blessed path: shipped
defaults — the editor's Styles menu, and (phase 5) the snippet library —
are written against Tailwind's class vocabulary, and the example site and
docs assume it. Sites on other CSS define their own styles/snippets and
lose only the batteries-included defaults.

**The Tailwind/CMS trap this creates:** production Tailwind generates CSS
by scanning source files, and editor content lives in the database where
the scanner never looks. Any class the editor can apply must therefore be
safelisted in the site's Tailwind build or it will silently not render.
The README carries the safelist for the default styles.

### 0.1 Editor color and font — a curated Styles menu, never pickers

The editor deliberately has no color picker and no font-family menu:
free-form pickers wreck design consistency in exactly the hands this CMS
targets, inline styles can't be restyled at the next redesign, and a font
menu can only truthfully offer fonts the template already loads. Instead,
the toolbar has a **Styles** dropdown of named, on-brand styles configured
by the developer (`Config.EditorStyles`) — each applies CSS classes, so
the host stylesheet stays the single source of design truth. Nil config
gets Tailwind-first defaults (colors, serif/mono, highlight, lead, small
print); empty config disables the menu.

### 1. CSS isolation — Shadow DOM for CMS chrome, prefixed classes for regions

All CMS UI injected into *customer pages* (edit toolbar, dialogs, snippet
palette, media library) renders inside Shadow DOM, so the host page's CSS —
Tailwind, Bootstrap, bespoke, anything — cannot touch it and vice versa. The
only thing the CMS puts in the host page's DOM is `data-cms-region`
attributes and a thin outline on hover, using namespaced `cms-` custom
properties. This is the strongest isolation available without iframes and
needs zero framework.

The *admin area* (`/admin`) is a whole page owned by the CMS, so it uses its
own plain, framework-free stylesheet with `cms-` prefixed classes.

### 2. Editable regions via template funcs

Customer templates mark editable areas:

```html
<h1>{{cmsText "hero-title"}}</h1>
{{cmsRegion "main"}}          <!-- rich content: blocks/snippets live here -->
<img src="{{cmsImage "hero"}}" ...>
```

Content is stored per `(page, region, locale)` in Postgres. The template owns
layout and design; the CMS owns only the content that fills the holes. This is
what makes "any CSS framework" work — the CMS never generates page-level
markup structure except inside regions.

### 3. In-place editing

When a logged-in CMS user visits the public site, they see the **draft**
version of each page with the editor script injected before `</body>`. In
edit renders, `cmsText`/`cmsRegion` output is wrapped in marker elements
(`<span/div data-cms-region data-cms-kind>`); images become in-place
editable when the template adds `data-cms-image="region-name"` to the img
tag (an attribute can't be wrapped from a template func).

The glue script (vanilla JS, chrome in Shadow DOM) provides two pieces of
persistent chrome — the **edit bar** (floating bottom pill: status chip,
Edit toggle, Cancel, Save draft, Publish, admin link, minimize-to-pencil)
and the **tool rail** (fixed full-height strip on the left, edit mode
only: Add section, Snippets drawer toggle, New page). Document-level
actions live in the edit bar; creation tools live in the rail. "New page"
opens a dialog (name + page type from the host's PageTemplates), creates
a draft via `POST /api/pages` with a slug derived from the name
(`content.Slugify`, numeric suffix when taken), and navigates the editor
straight into the new draft. The edit bar's Delete button
(`DELETE /api/pages/{id}`) removes the current page after a danger
confirm; the home page (empty slug) is not deletable — enforced
server-side in both the API and the admin form, and the button is hidden
on it. Specifically:

- the edit bar: status chip, Edit toggle, Save draft, Publish, admin
  link
- Edit mode outlines regions; plain-text regions get `contenteditable`
  (plaintext-only); **rich HTML regions are edited with TinyMCE 6.8.6 in
  inline mode** — the last MIT-licensed TinyMCE release, vendored into the
  module (`editor/tinymce/`, ~1.1 MB, self-hosted, no CDN or build step)
  and lazy-loaded only when "Edit page" is pressed. Inline mode edits the
  real element with the page's own styles, keeps `class` attributes (so
  framework-styled markup survives), and shows a floating selection
  toolbar (bold, italic, headings, lists, blockquote, links) via the
  quickbars plugin.

  Why TinyMCE 6 and not the alternatives: CKEditor 5 and TinyMCE 7+ are
  GPL/commercial (unsafe for closed customer deployments); TipTap/
  ProseMirror need a build pipeline and normalize unknown markup away
  (they would mangle snippet HTML); Editor.js/Trix own the whole editing
  surface rather than editing in place. TinyMCE 6.x is EOL upstream —
  acceptable because the integration surface is thin (one `init` call)
  and all HTML is sanitized server-side regardless.

- click an image → Shadow DOM media picker (grid from `GET /api/media`)
  with direct upload (`POST /api/media`)
- images inside rich regions: the toolbar's image button opens the CMS
  media picker directly (browse the library or upload — no URL field, no
  TinyMCE dialog), and paste/drag of image data uploads through
  `images_upload_handler`; every path stores to the bucket and inserts
  the resized web-variant URL. `convert_urls` is disabled so TinyMCE
  never rewrites `/cms/media/…` paths relative to the current page.
- the media picker is a centered modal with a folder sidebar, name search,
  and grid/list views. Folders are metadata only — a Postgres table plus a
  `folder_id` on media — so the bucket's object keys never change and
  "moving" a file is one UPDATE; search is a SQL ILIKE on filename. The
  admin media page has the same search/folder filters plus per-item
  move-to-folder controls.
- documents: the media library also accepts non-image files behind a
  strict whitelist — PDF, Word/Excel/PowerPoint (legacy + OOXML),
  OpenDocument, text/CSV, ZIP — validated by extension *and* sniffed
  content (an HTML file renamed `.pdf` is rejected; HTML/SVG/JS are never
  accepted since proxied media is same-origin). Files are stored as-is
  under a sanitized filename so downloads are readable
  (`q3-report-final.pdf`). The toolbar's paperclip button opens the same
  picker filtered to documents and inserts a link: selected text becomes
  the link, otherwise the filename is inserted as link text.
- Save posts dirty regions to `POST /admin/api/pages/{id}/regions` with the
  session CSRF token in `X-CSRF-Token`; the server validates region names
  against the template, sanitizes HTML from non-admins with **bluemonday**,
  validates image URLs, and stores draft blocks
- Publish hits `POST /admin/api/pages/{id}/publish` — same snapshot
  mechanics as the admin form
- `beforeunload` warning when there are unsaved changes

Nothing an editor does is visible to the public until Publish. The admin
form remains as the fallback/"source view" for the same content.

### 4. Snippets (drag & drop)

A snippet is a named, pre-written HTML fragment, registered two ways: by
the developer via `Config.Snippets` (per-customer components, versioned
with the code) or created by admins in the admin UI. Nil config gets a
Tailwind-first default library — inline blocks (callout, call-to-action,
two columns, quote, button) plus the section presets described in 4.1;
snippet classes need safelisting like editor styles do.

While editing, a **Snippets** button in the bar opens a non-modal side
drawer. Dragging a card onto a rich region inserts the markup at the drop
caret (TinyMCE accepts `text/html` drops natively); clicking a card
inserts at the cursor of the most recently focused region. Once inserted,
a snippet is ordinary region HTML — edited in place, sanitized on save
(so default snippets avoid SVG/scripts), and styled by the host CSS.
Deleting a snippet never affects pages that already used it.

*Scope note:* inside an ordinary `cmsRegion`, snippets are fragments of
the region's single HTML block. Page-level composition — reorderable,
full-width building blocks — is what **sections** are for.

### 4.1 Sections (page-level layout for editors)

`{{cmsSections "name"}}` declares a full-bleed area the template places
outside any max-width container. It renders an ordered stack of sections;
each section is a full-width `<section>` wrapper (background classes)
around an inner container (width classes) around ordinary rich HTML. This
is the one place the CMS generates page-level markup — the trade-off for
letting editors compose layouts — and every class it emits comes from
curated, configurable options (`Config.SectionStyles`: backgrounds and
widths, Tailwind-first defaults, same no-pickers philosophy as the Styles
menu; a dark background carries `prose-invert` via ContentClass).

Data model: this activates the multi-block regions the `cms_blocks`
schema anticipated — one block per section, ordered by `sort`, with a
`settings` JSONB column holding the chosen background/width **keys**
(classes are resolved at render time, so restyling options later
restyles existing content). Saving replaces the region's draft blocks
atomically with the submitted ordered list
(`POST /api/pages/{id}/sections`); unknown setting keys fall back to the
first option; draft/publish snapshotting is unchanged.

In edit mode each section gets a floating control pill (move up/down,
add below, settings, delete — all through the styled dialogs) and its own
TinyMCE instance, so structure stays out of the editable surface. Adding
a section opens the snippet drawer as a "choose a starting point" picker
(any snippet, or an empty section). Section granularity is deliberate:
per-paragraph block models fight TinyMCE; one editor per section
composes cleanly with everything else.

**Section presets** extend snippets into that picker without a new
concept: a snippet carrying a `Settings` map (the same
background/width/height/valign keys the ⚙ dialog stores) is offered
only when adding a section, listed first, and creates the section with
both the starting HTML and the settings applied. This is what makes a
sections-only "blank canvas" page type genuinely composable — hero, CTA
band, and similar full-bleed patterns are one click instead of
markup-plus-settings assembly — while staying inside the existing
model: there is no section "type" stored anywhere; a preset-created
section is indistinguishable from one assembled by hand. Presets come
from config or from the admin snippets UI (a "Section preset" type on
the snippet form, curated settings only — the free-form bgcolor/bgimage
stay config-side); stored presets keep their settings in a nullable
JSONB column, NULL meaning plain block. The defaults ship
hero, feature grid, stats, testimonials, FAQ, and a call-to-action
banner. Presets whose look should survive background changes lean on
prose/prose-invert instead of `not-prose` (colors follow the section);
grid layouts Typography would mangle stay `not-prose` with explicit
colors.

### 4.2 Navigation menus — a CMS partial, edited in place

Menus bend the core principle, deliberately. Nav markup is
design-specific HTML, but hand-rolled markup is invisible to the
in-place editor — right-click editing needs markup the CMS understands.
So `{{cmsNav "main"}}` renders the complete nav (list, links, one level
of dropdown submenus, toggle behavior) with stable `cms-nav-*` classes
the host styles; the CMS injects only the functional CSS (layout,
dropdown hide/show/position) via `cmsHead` and the toggle script via
`cmsScripts`, all overridable by class. `{{cmsMenu "main"}}` remains the
data-only escape hatch (`Label`, `URL`, `Active`, `NewTab`, `External`,
`Children`) for bespoke navs, which simply aren't editable in place.
Any number of menus per site by key ("main", "footer", ...).

Items live in `cms_menu_items` and link **either to a page or to a
literal URL** — or, for a top-level item, to nothing: a label-only row
(no page, no URL) is a **dropdown parent**, and its children hang off
`parent_id`, one level deep by design (the API rejects deeper nesting).
Label-only parents avoid the classic tap ambiguity on touch — a parent
never both links and opens. Page links resolve their URL from the slug
at render time (rename-safe) and vanish when the page is deleted (FK
cascade); items pointing at draft pages render only for logged-in
editors, matching draft-page visibility. An empty dropdown renders for
editors (so it can be filled) but is dropped from public renders.
Literal URLs are validated to a small scheme whitelist (`/`, http(s),
mailto:, tel:).

Editing happens **on the nav itself** while in edit mode: right-click an
item (long-press on touch) for its settings modal — label, searchable
page picker or custom URL, new-tab, make-it-a-dropdown, remove; "＋"
chips append items; drag rearranges, including into/out of a dropdown.
Every change saves immediately via `PUT /api/menu` as an atomic
tree replacement, and the editor re-renders the nav client-side
(editor/src/menu.js mirrors navHTML's markup — keep them in sync).
Because ReplaceMenu reassigns row ids on every save, the editor
addresses items by tree position, not id. Menus deliberately have **no
draft state**: they are site-wide, so "publish on which page?" has no
good answer; changes are live at once. Labels get per-locale variants
when phase 7 lands.

- Content tables key on `(page_id, region, locale)` with fallback to `en`
  when a `fr` row doesn't exist.
- URL strategy: `/` = default locale, `/fr/...` = French (configurable).
- The edit toolbar gets a locale switcher: flip to FR, edit the same page in
  place, save.
- Admin UI strings themselves ship in en + fr.
- Sites that don't want French configure `Locales: []string{"en"}` and none
  of it surfaces.

### 6. Media serving — proxy by default, direct when possible

Uploads always live on the S3-compatible bucket, but the URL pages embed
depends on configuration:

- **Default (private bucket):** media is served by the CMS itself at
  `/cms/media/…` — the app streams from the bucket with immutable cache
  headers. Works everywhere with zero bucket configuration; modern stores
  (AWS with ACLs disabled, newer Linode clusters) reject per-object ACLs
  anyway.
- **`PublicRead: true`:** pages embed direct bucket URLs; the bucket must
  allow public `s3:GetObject` (bucket policy / provider access setting;
  `ApplyPublicReadPolicy` can set it where the key is allowed to).
- **`PublicBaseURL`:** pages embed CDN/custom-domain URLs.

The `ObjectStore` interface (Put/Get/Delete/PublicURL) is an extension
point — hosts can swap in local disk for development or anything else.

One S3-compatibility caveat baked in: the AWS SDK's default streaming
checksums (aws-chunked + CRC32 trailers) are rejected by Ceph-based stores
(Linode, DigitalOcean), so the client only computes checksums when an
operation requires them.

### 7. Per-page CSS/JS

Each page has optional `head_css` and `body_js` fields (plus attachable
uploaded asset files), injected by the renderer. Editable in admin under an
"Advanced" tab — present but out of the way for non-technical users.

## Data model (Postgres)

```
cms_users         id, email, name, password_hash (argon2id), role (admin|editor), active
cms_sessions      token, data, expiry
cms_pages         id, slug, template_name, status (draft|published), sort, parent_id
cms_page_meta     page_id, locale, title, description            -- SEO per locale
cms_blocks        id, page_id, region, sort, snippet_key (nullable), html, locale, status
cms_page_assets   page_id, kind (css|js), inline_content, media_id
cms_posts         id, type (blog|news), slug, status, published_at, author_id
cms_post_content  post_id, locale, title, summary, body_html
cms_media         id, s3_key, filename, mime, width, height, size, uploaded_by
cms_media_meta    media_id, locale, alt_text
cms_snippets      key, name, html, editable_slots, source (code|db)
cms_versions      entity_type, entity_id, locale, payload jsonb, saved_by, saved_at
```

`cms_versions` gives cheap undo/history — every publish snapshots the
previous state.

## Tech choices

| Concern         | Choice                        | Why |
|-----------------|-------------------------------|-----|
| Router          | chi                           | stdlib-compatible, mountable, zero magic |
| DB              | pgx v5                        | the standard for Postgres in Go |
| Sessions        | alexedwards/scs + own pgx store | mature; own store so the table is `cms_sessions` |
| Passwords       | argon2id                      | current best practice |
| HTML sanitizing | bluemonday                    | non-negotiable for contenteditable input |
| S3              | aws-sdk-go-v2                 | works with every S3-compatible store |
| Images          | disintegration/imaging        | thumbnails/resizes on upload |
| Migrations      | embedded SQL + tiny runner    | no external tool for customers to run |
| Editor JS       | vanilla glue + vendored TinyMCE 6 (MIT) | proven WYSIWYG UX, inline mode, no build step, no CDN |

Admin UI: server-rendered Go templates + a little vanilla JS. No SPA — keeps
the module self-contained and the surface area small.

## Extension points (the per-customer story)

- `RegisterSnippet` / `RegisterSnippetDir` — custom components per customer
- `RegisterContentType` — e.g. "Events" or "Staff Directory"; gets CRUD admin
  + template funcs
- Hooks: `OnSave`, `OnPublish`, `OnUpload`, `OnLogin` — for search indexing,
  cache purging, notifications
- `FuncMap()` merged into customer templates; customers can add their own funcs
- Auth: `UserStore` interface with the Postgres implementation as default, so
  a customer can plug in LDAP/OAuth later
- Everything mounted under paths the host app chooses

## Build order

1. **Foundation** ✅ — module skeleton, config, migrations runner, auth
   (login, sessions, CSRF, roles, throttling), admin shell, user management,
   example app
2. **Pages & rendering** ✅ — page CRUD, template funcs, region storage,
   draft/publish, per-page CSS/JS, region auto-detection from parse trees,
   draft preview, editor-content sanitization
3. **Media** ✅ — S3 uploads (any S3-compatible store), automatic web/thumb
   variants, media library UI, cmsImage template func with picker, private
   buckets supported by proxying media through the CMS (/cms/media/)
4. **In-place editor** ✅ — injected script, Shadow DOM toolbar, TinyMCE
   6 (MIT, vendored) inline editing for rich regions, click-to-replace
   images with media picker and upload, JSON save/publish API with
   sanitization
5. **Snippets** ✅ — config + admin-created registry, Tailwind-first
   default library, palette drawer with click-to-insert and drag & drop
   into regions
5.5. **Sections** ✅ — full-width, editor-composable page areas
   (`cmsSections`): add/reorder/delete/settings controls in place,
   curated backgrounds & widths, snippet-seeded new sections,
   multi-block storage with settings JSONB
6. **Blog & news** — posts, listing/detail helpers, RSS, categories/tags
7. **i18n** — locale routing, fallback, in-place locale switching, FR admin
   strings
8. **Polish** — versioning/undo, sitemap.xml, hooks API, docs

Each phase ends with `examples/basic` exercising the new feature, so there is
always a runnable proof.

## Assumptions

- One deployment per customer (no shared multi-tenant instance).
- Pages map to developer-authored templates; non-technical users edit content
  and assemble snippets but don't create new *layouts* — that's the
  developer's job via templates. This boundary is what keeps the product
  usable for non-technical users.
- News and blog are the same engine with two feeds, separately listable.
