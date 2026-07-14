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

When a logged-in editor visits a page, middleware injects one `<script>` tag.
That script:

- outlines the editable regions and makes them `contenteditable`
- shows a floating formatting toolbar (bold, italic, headings, links, lists) — Shadow DOM
- click an image → media dialog → upload straight to S3
- saves via `fetch` to the JSON API; the server sanitizes all HTML with
  **bluemonday** (critical — contenteditable output is untrusted)
- draft/publish toggle so edits aren't live until published

### 4. Snippets (drag & drop)

A snippet is a named, pre-written HTML fragment with editable slots,
registered two ways: by the developer in Go (`c.RegisterSnippet(...)` — this
is how per-customer custom components ship) or created in the admin UI. The
editor shows a palette panel; dragging a snippet into a `cmsRegion` inserts an
instance whose text/images are then edited in place like everything else.
Regions are internally a list of blocks (snippet instances + free-form rich
text), which also provides reordering.

### 5. i18n — locale is a column, not a fork

- Content tables key on `(page_id, region, locale)` with fallback to `en`
  when a `fr` row doesn't exist.
- URL strategy: `/` = default locale, `/fr/...` = French (configurable).
- The edit toolbar gets a locale switcher: flip to FR, edit the same page in
  place, save.
- Admin UI strings themselves ship in en + fr.
- Sites that don't want French configure `Locales: []string{"en"}` and none
  of it surfaces.

### 6. Per-page CSS/JS

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
| Editor JS       | vanilla ES modules, no build step | nothing to break, nothing to depend on |

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
2. **Pages & rendering** — page CRUD, template funcs, region storage,
   draft/publish, per-page CSS/JS
3. **Media** — S3 uploads, variants, media library UI
4. **In-place editor** — injected script, contenteditable + toolbar in
   Shadow DOM, save API with sanitization
5. **Snippets** — registry, palette, drag & drop, block reordering
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
