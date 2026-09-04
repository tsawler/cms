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

mux.Handle("/", c.Handler()) // admin UI under Config.AdminPath, public pages elsewhere
```

Customer-specific features are added through extension points (below), never
by forking the core. One deployment per customer — no multi-tenancy inside the
module, which keeps the data model and auth simple.

## Module layout

```
github.com/tsawler/cms
├── cms.go            // Config, New(), the public Handlers
├── auth/             // users, passwords, roles, login throttling, reset tokens
├── content/          // pages, regions, publishing; posts (blog + news) (phases 2, 6)
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
tag (an attribute can't be wrapped from a template func). Such a slot
gets floating pencil/trash chrome like every other editable element, and
the script tag carries `data-filled-images` — which slots hold a stored
picture — because the markup alone can't distinguish a chosen picture
from the template's own fallback. An optional `data-cms-rendition` names
the rung of the ladder a pick is stored at, defaulting to `web`: only the
template knows how wide the slot is on the page, so only the template can
say that a full-width image is more bytes than it will ever show.

The glue script (vanilla JS, chrome in Shadow DOM) provides two pieces of
persistent chrome — the **edit bar** (floating bottom pill: status chip,
Edit toggle, Cancel, Save draft, Publish, admin link, minimize-to-pencil)
and the **tool rail** (fixed full-height strip on the left, edit mode
only: Add section, Snippets drawer toggle, New page, New post when blog
& news is configured). Document-level
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
curated, configurable options (`Config.SectionStyles`: backgrounds,
widths, and corner rounding, Tailwind-first defaults, same no-pickers
philosophy as the Styles menu; a dark background carries `prose-invert`
via ContentClass, corner classes join the background on the wrapper).

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

### 4.2.1 Shared regions — content the site owns, not a page

A footer is the same on every page, so requiring an editor to build one
per page is asking them to maintain the same paragraph in twenty places.
But regions are keyed `(page, region, locale)`, and the whole editing
stack — save, sanitize, draft/publish, translate — is keyed to a page id.

So `{{cmsShared "footer"}}` is an ordinary rich region whose content
belongs to **one reserved system page** (slug `__site`, `is_system` on
`cms_pages`). Nothing about blocks, sanitization, snapshots, or locale
fallback changes; only the id the rows hang off does. It is the same
trade as posts being pages (4.3): reuse the machinery rather than grow a
parallel one. The alternative — a nullable `page_id` plus an owner
column — would give every block query a variant and leave the UNIQUE key
meaning less than it does now.

Consequences, in order of how much thought they took:

- **The read costs nothing extra.** A page render already loads its
  blocks; the shared ones come back in the same query, the site page
  reached by an uncorrelated subquery on its slug rather than by an id
  resolved in a round trip of its own. Locale fallback is decided per
  (page, region) rather than per region, so a translated shared footer
  cannot stop an untranslated page region of the same name from falling
  back.
- **Markers are namespaced.** An edit render emits
  `data-cms-region="site:footer"`; the editor collects and saves it like
  any other region, and the save endpoint splits the submitted map on the
  prefix. So the editor needed no new code path, and a page region named
  "footer" stays a different region from the shared one.
- **Region names are validated against the union of all templates.**
  Shared content has no template of its own — it is reached from
  whichever page the editor is standing on — so `SharedRegions()` unions
  what every template set declares, and `Regions()` (the page's own list,
  which drives the admin form) filters shared regions out.
- **Publish rides along; discard does not.** There is no page to publish
  shared content "on", so publishing any page publishes it, and the
  status chip counts unpublished shared edits so the chip is not lying
  about the page in front of you. Discard deliberately does not ride
  along: reverting page A's draft should not silently delete footer work
  done on page B. The cost is that undoing a shared edit before
  publishing means editing it back, which is the cheaper mistake.
- **An empty region renders the template's own markup.** `{{cmsShared
  "footer" "<p>&copy; Acme</p>"}}` shows the fallback until someone
  edits, which is what makes "every site has a footer by default" true
  with no seeding step — and in edit mode the fallback is the starting
  point for the first edit, the same trick untranslated regions use.
- **The system page is not a page.** It is filtered out of listings,
  counts, and slug lookup, and refuses to be deleted. It is also
  recreated on demand if it goes missing, so an emptied database (a test
  harness truncating between cases) heals rather than losing the ability
  to save shared content.

Shared regions are rich HTML only. `cmsText`, `cmsImage`, and
`cmsSections` all exist to be placed by a page's own layout, and a
site-wide sections stack is a page in a layout slot, not a region.

### 4.2.2 The notice bar — a region and a switch, not a section

"A thin bar at the top of every page for a holiday closure" looks like a
section, and cannot be one: sections render inside `{{cmsSections}}`,
which every layout places in `<main>`, below the header that carries the
menu. Nothing an editor adds to a page's content can climb above the
nav. It is also site-wide, and a section belongs to one page.

So the bar is split between the two stores each half belongs in:

- **Its words are a shared region** (`render.NoticeRegion`), edited in
  place in the bar itself. That is what makes it translate, sanitize,
  draft, and publish with no new machinery — and it is forced, not
  merely convenient: `SiteSettings` is a flat key/value map with no
  locale dimension, so a notice stored as a setting would be a single
  string on a bilingual site.
- **Its switch and look are settings** (`NoticeBar`, `NoticeStyle`,
  `NoticeDismissible`). "Is there a notice today" is a state, not
  content, and it has to be able to hide words that are still stored.

Consequences worth writing down:

- **Placement is a post-render injection, not a required template
  call.** `{{cmsNotice}}` exists for layouts that want the bar
  somewhere specific, but a template that never calls it gets the bar
  inserted after its `<body>` tag once the render is done — the same
  seam `injectEditorScript` uses. That is what lets an existing site
  switch a bar on without touching its templates, which for a feature
  whose whole point is "we need to say something today" is the
  difference between a setting and a deployment.
- **`SharedRegions()` contributes the region itself.** Saves are
  validated against the union of regions the host's templates declare,
  and the notice's may appear in none of them, so the renderer adds it
  unconditionally. An unused region has no rows, so this costs nothing.
- **The bar ships its own CSS**, like `cmsNav` and unlike sections:
  the look must not depend on the host having safelisted anything.
  It is emitted only on pages that carry a bar, plus every edit render
  — where the settings dialog can conjure one client-side.
- **Dismissal is keyed to the wording.** A closed notice is remembered
  in `localStorage` against a digest of its own words, so rewriting the
  notice re-shows it to everyone who dismissed the last one; a bar that
  stayed closed for the *next* emergency would be worse than no bar.
  The hiding happens from a `<head>` script that appends a style rule,
  not from a handler at the end of the body, because the alternative is
  a notice that flashes up and shoves the page down on every visit.
  An edit render draws the same button and wires it to the same
  handler, but hands it no key: a logged-in editor reading their own
  site is a visitor, and a button that visibly does nothing reads as a
  broken site — while a *remembered* dismissal would lock that editor
  out of the words they were writing. The handler's other half is a
  guard on `.cms-editing`: mid-edit the bar is a region with an editor
  instance attached, and closing it would strand both.
- **Edit mode reserves the toolbar's lane.** The shared TinyMCE toolbar
  is pinned to the top of the viewport, which assumes a region under it
  can be scrolled out from beneath it — an assumption the bar breaks by
  being the first thing in the document, where scroll 0 leaves it
  permanently underneath. Edit mode therefore pushes a top-of-page bar
  down by the toolbar's height, the same deal `.cms-editing` already
  strikes with the left rail. The space is claimed on entering edit mode
  rather than on focusing the bar, so what is being clicked never moves
  out from under the pointer.
- **Two ways in, one store.** The settings dialog carries a plain-text
  box for the wording as well, because "switch it on, then go find the
  bar in the page" is two steps for what is one thought — and it left a
  placeholder standing in the page, one stray Save away from being the
  notice the site published. The box writes the same shared region
  through the same regions endpoint, so drafting, publishing and
  locales are untouched; it is seeded from the bar's live content
  (unsaved typing included), and it writes **only when the words
  actually changed**, so a visit to the dialog to change a colour
  cannot disturb the notice it merely displayed.
- **The dialog's box is a small rich field, not a textarea.** A notice
  that cannot link to the page explaining it is half a notice, and a
  plain-text box would have flattened one that could. It is ~200 lines
  in `dialogs.js` (`type: "rich"`) rather than a second TinyMCE:
  TinyMCE is lazy-loaded only in edit mode, renders its toolbar into
  the light DOM at the top of the viewport, and would be a strange
  thing to summon into a modal that opens while merely reading a page.
  The field allows `strong`, `em`, `a[href]` and `br` and nothing else,
  enforcing that with one sanitizer used on the way in *and* out — so
  what it stores is only ever that list, which matters because the
  server trusts an admin's markup. Formatting is applied by Range
  surgery rather than `execCommand`, and the selection is read from the
  field's own root (`ShadowRoot.getSelection()` where it exists, the
  document's otherwise) with a containment check, so a selection the
  buttons cannot see makes them do nothing rather than something
  surprising.
- **An empty bar is no bar.** TinyMCE leaves `<p><br></p>` behind when a
  region is emptied, so "nothing written" is a content test rather than
  a string comparison; an editor still sees a placeholder to type over,
  and never a coloured strip with nothing in it.

### 4.3 Blog & news — posts are pages

Requirement 5 could have been a parallel content pipeline (`cms_posts` +
`cms_post_content` with its own body storage, as the original data-model
sketch had it). It isn't, because the body of a post wants everything a
page already has: sections, snippets, in-place TinyMCE editing,
draft/publish snapshots, sanitization, per-page CSS/JS. All of that
machinery is keyed to `cms_pages`/`cms_blocks` — so **a post is a page**
(slug prefixed `blog/` or `news/`, rendered by `Config.PostTemplate`, a
template parsed like a PageTemplate but hidden from the page-template
choosers) **plus one `cms_posts` row**: feed, display date, author, and
an optional thumbnail. The page's per-locale title and description double
as the post's title and summary, so phase 7 gets post localization for
free.

The same reasoning later took the header image out of that row. A post
once stored a banner image the template drew above the article, which
meant a second, poorer vocabulary for something sections already did
properly: the field could name a picture and nothing else — no width, no
corners, no say in which part of it survived being cropped to a banner —
and it went live the moment it was set, outside the draft/publish flow.
So the banner became an ordinary section in a region of its own
(`{{cmsSections "header"}}` above the article), and the columns went
away. The gain is the same one as making a post a page: the section gear,
its settings, the preview, drafts, and translation all already existed.

Consequences fall out rather than being built: the in-place editor works
on posts unchanged (it only ever sees a page id); publish/discard/preview
reuse the page endpoints; deleting the backing page cascades away the
post; menu items may link to posts. The backing pages are hidden from
the admin Pages list — posts are managed under Blog & News (list with
feed filter tabs, form with feed/date/summary/thumbnail, same
draft-publish-discard-delete verbs as pages). Posts are created there or
from the editor tool rail's "Post" button — a dialog taking title, feed,
summary, date, and one image via the media picker, which becomes both the
listing thumbnail and the background of a banner section seeded into the
template's header region; `POST /api/posts` then navigates straight into
the new draft, the same shape as New page. Seeding keeps the old
one-picture-one-click convenience without keeping the old coupling: what
lands is an ordinary section, and everything about it is editable
afterwards. On a post's page in edit mode, a
"⚙ Post settings" pill pinned top-right (discoverable next to the
title, unlike a bar icon) edits date, summary, and thumbnail
(`PUT /api/posts/{id}`); like menus these have no draft state — they
describe the post in listings, so saves are live at once. The banner is
not among them: it is a section, saved with the rest of the page. Post
creation stamps the creating user as the author, fixed thereafter.

Templates see posts two ways: on a post's own page the dot carries
`.Post` (feed, date, author, thumbnail — nil on ordinary pages), and
any template can call `{{cmsPosts "blog" 12}}` for render-ready listing
entries (public renders get published posts; editors also get drafts,
flagged so templates can badge them). Listing pages are just pages using
a listing template, created wherever the host wants them.

Listings paginate server-side. `{{cmsFeed "blog"}}` reads `?page=` off
the request and returns one page — a `LIMIT`/`OFFSET` window plus a
`COUNT(*)`, so the query cost does not grow with the feed — along with
the prev/next and numbered links to reach the rest; `{{cmsPagination}}`
renders those as markup, the same data-or-markup pair as `cmsMenu` and
`cmsNav`. Page size is `Config.PostsPerPage` (`CMS_POSTS_PER_PAGE`,
default 10), overridable per listing from the template. The ordering is
total — `published_at` then `id` — so no post can straddle two pages or
be skipped between them, and the count runs the same published-only
filter as the window, so an editor paging through drafts sees a page
count that matches what they are being shown. A page number past the end
clamps to the last real page rather than 404ing or showing an empty
listing: a listing URL with a junk page number is still a valid page.
`{{cmsPosts}}` stays as it was, the unpaginated newest-N func, since
plenty of listings (a homepage's "latest three") want exactly that and
should not pay for a count.

The admin's Blog & News and Pages lists page the same way, off the same
`render.Pager` and the same `render.PagerCSS`, so there is one pagination
bar in the codebase rather than an admin one and a site one that drift.
The shared pieces — `perPage`, `listPage`, `listPageURL`, and the
stylesheet route — live in `admin/pagination.go`; a list handler counts,
builds a pager, then fetches its window, in that order. Two things differ
from the public site. Its page size is `Config.AdminPerPage`
(`CMS_ADMIN_PER_PAGE`, default 25), separate from `PostsPerPage` because
an editor's table wants far more rows than a blog page and tuning one
must not disturb the other; it sizes every paginated admin list, since a
per-list knob would be configuration nobody asked for. And its links carry `AdminPath` — the admin
router runs with its mount prefix stripped, so a link built from
`r.URL.Path` alone would point at the public site. The bar's stylesheet
is *served*, at `{AdminPath}/static/pager.css`, not inlined: the admin's
CSP has no `style-src 'unsafe-inline'`, so an inline `<style>` reaches
the browser and is silently dropped. The feed tab (`?feed=blog`) rides
along in the page links, so paging the Blog tab does not drop you back
into All, and the Pages list's count excludes posts' backing pages
exactly as its window does, so the page count never promises rows the
table will not show. RSS is served
at fixed paths `/blog/rss.xml` and `/news/rss.xml` (dots can't appear in
page slugs, so the paths are free), taking channel metadata from the
published listing page at the feed's slug when one exists. The
`published_at` date orders listings and displays on posts; it is not a
publishing schedule — visibility is the page's draft/published status.
Categories/tags are deferred.

### 5. i18n — prefix routing, region-level fallback, in-place translation

`Config.Locales` lists the site's locales; the first is the default and
with a single entry none of this surfaces. URLs: `/` serves the default
locale, `/fr/...` serves French — the prefix is the locale code, resolved
before slug lookup, and slug validation rejects pages whose first segment
matches a configured locale code. `{{cmsLocales}}` gives templates the
current page's URL in every locale for a language switcher, and cmsHead
emits absolute hreflang alternates on multi-locale sites.

**Fallback is per region, field-level for metadata.** Rendering `fr`
loads fr blocks; a region with no fr rows uses its en rows wholesale
(region granularity because a sections region is one ordered document —
interleaving locales would be nonsense). Page title/description fall
back field-by-field (`COALESCE(NULLIF(fr,''), en)`), which also covers
posts (their title/summary are page metadata). Publish and discard
snapshot every locale at once, so "unpublished changes" is locale-blind.

**Translating happens in place.** The edit bar grows an EN|FR switcher
(navigate to the same page under the other prefix, confirm if there are
unsaved edits). On a non-default locale, untranslated regions render the
default-language fallback with a dashed amber outline and tooltip; the
badge clears the moment the region is edited, and saving writes that
locale's rows — the fallback content is the natural starting point for a
translation. The ⋯ menu gains "Remove this translation" (deletes the
locale's draft blocks and metadata via `POST
/api/pages/{id}/revert-locale`, applied live on next publish). All the
save APIs carry a validated `locale`. The admin page/post forms get
locale tabs (`?locale=fr`): title/summary and region source are
per-locale; address, template, feed, date, images, and code stay on the
default tab.

**Menu labels** ride on the row as a `labels` JSONB map of per-locale
overrides ({"fr": "À propos"}) — a child table would be wiped by
ReplaceMenu's wipe-and-reinsert, while the editor round-tripping the
whole map and editing only its active locale survives it. Nav page links
and Active detection are locale-prefixed; RSS feeds localize under the
prefix (`/fr/news/rss.xml`).

**Admin chrome ships in en + fr** (Canadian French): a string table
keyed by the English text, a per-user cookie toggle in the topbar
(default sniffed from Accept-Language), shown only when fr is
configured. The in-place editor's own chrome strings (Edit/Save/dialogs)
remain English for now — a deliberate deferral, tracked for phase 8.

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
uploaded asset files), injected by the renderer; site-wide equivalents
live in settings. Each field takes plain code or raw markup: a value
containing `<style>`, `<link>`, or `<script>` tags is written into the
page verbatim, anything else is wrapped in the appropriate tag. Editable
in admin under an "Advanced" tab — present but out of the way for
non-technical users.

## Data model (Postgres)

```
cms_users         id, email, name, password_hash (argon2id; bcrypt accepted on
                  import and rehashed at the next login), role (admin|editor), active
cms_sessions      token, data, expiry
cms_password_resets  token_hash (sha256; the email holds the only usable copy),
                  user_id, expires_at  -- single-use, one live row per user
cms_pages         id, slug, template_name, status (draft|published), sort, parent_id,
                  is_system  -- the reserved __site row owning shared regions (4.2.1)
cms_page_meta     page_id, locale, title, description            -- SEO per locale
cms_blocks        id, page_id, region, sort, snippet_key (nullable), html, locale, status
cms_page_assets   page_id, kind (css|js), inline_content, media_id
cms_posts         id, page_id (unique FK), feed (blog|news), published_at, author_id,
                  thumbnail_media_id, thumbnail_url
                  -- the thumbnail is a library id, so the renderer picks the rendition
                  --   a listing needs; thumbnail_url holds an image from outside it
                  -- the banner above a post is a section, not a column (4.3)
                  -- slug/status/title/summary live on the backing page (4.3)
cms_media         id, store_key, filename, mime, width, height, size, uploaded_by
                  -- store_key is relative to the media root: no row embeds S3_KEY_PREFIX
cms_media_meta    media_id, locale, alt_text
cms_snippets      key, name, html, editable_slots, source (code|db)
cms_page_versions page_id, payload, payload_hash, kind (publish|manual), note,
                  saved_by, saved_at
                  -- one edition of a page: its whole published content, all
                  --   locales, frozen as a JSON document (see below)
```

`cms_page_versions` is the page history. A version is the same tuple of
data `Publish` already copies — every region, every locale, plus the
page-level fields staged in `cms_page_drafts` — so history is that
existing snapshot given a third place to live rather than a new concept.
Some consequences worth knowing:

- **The payload is an opaque document, not shadow rows.** Nothing queries
  into a version; it is written once, listed by its metadata, and
  restored wholesale. Keeping it as text also means an old edition stays
  readable after the live tables' shape moves on. A third value in the
  `status` column was the alternative, and it would have put history
  inside the `EXCEPT` probes in `HasUnpublishedChanges` and reshaped the
  `UNIQUE` key on `cms_blocks`.
- **A version carries no slug or visibility.** 0021 kept those out of the
  draft/publish workflow because they are addressing rather than content,
  and a rollback that silently moved a page's URL would be a surprise.
- **Identical snapshots are not stored.** Publishing any page publishes
  the site's shared content with it (4.2.1), so without the
  `payload_hash` check the `__site` page would collect a near-identical
  edition every time anyone published anything.
- **Custom-code blocks are frozen with the page, and put back only when
  the library has lost them.** A page's markup holds an inert placeholder
  naming a `code_key`; the body lives in `cms_code_snippets`. An edition
  copies the bodies its blocks name, so a widget deleted since is
  recoverable. Restoring recreates a key the library no longer holds —
  safe precisely because nothing else can be using it — and *reports*,
  rather than overwrites, one it still holds under a different body: the
  library is shared, and a button labelled "restore this page" must not
  rewrite a widget other pages show. A consequence: rewriting a block
  changes what its pages publish, so their next publish records an
  edition even though the pages were not touched. That is honest, and it
  is not something `HasUnpublishedChanges` reports — code is not staged
  at all, so an edit to one is live everywhere the moment it is saved.
- **Everything else is still the world around the page.** Media lives in
  the library, registered snippets live in the host's templates, and
  shared regions version as `__site`'s own history. A version puts the
  page's own content back; those are wherever they are now.
- **The payload format is numbered, and the two directions differ.** An
  older payload reads straight into the current struct — every change so
  far has been an added field, and what an old edition does not carry
  reads as absent, which is what "this predates that feature" should
  mean. A *newer* payload is refused: a build rolled back over a database
  a later one has written to would otherwise read it short, which on a
  restore means losing content rather than failing. A change that is not
  additive needs a real branch in `decodeSnapshot` rather than that rule.
- **Restore writes the draft**, exactly as `DiscardDraft` does — nothing
  reaches the public site without a Publish, and the editor can preview
  an old edition before committing to it. "Restore & publish" does both
  in one click, for the case the feature really exists for: something is
  wrong on the live site *now*.
- **In the editor, restoring is its own preview.** The editor shows
  draft content, and a restore writes the draft — so the page reloads
  onto the old version and you are looking at it, with Publish and
  Discard draft already on the bar to decide its fate. That is why the
  ⋯ menu's Page history offers a version list and nothing else, while
  the admin screen, which is not standing on the page, needs a Preview.
  The newest version is never offered as a destination: it is what the
  site is already serving, and "go back to now" is Discard draft.
- **The history screen is one template for pages and posts.** A post is a
  page underneath, so its history is its backing page's; only the loader
  that enforces the permission and the URL the screen sits under differ,
  and both are arguments. A version preview renders the stored edition
  through the real site templates, with two things taken from now rather
  than from the edition: the shared regions (the site's current chrome,
  which this page's edition never held) and, when the host has since
  dropped the template an edition was written for, the page's current
  one — an old edition is still worth looking at when its layout is gone.

## Tech choices

| Concern         | Choice                        | Why |
|-----------------|-------------------------------|-----|
| Router          | chi                           | stdlib-compatible, mountable, zero magic |
| DB              | pgx v5                        | the standard for Postgres in Go |
| Sessions        | alexedwards/scs + own pgx store | mature; own store so the table is `cms_sessions` |
| Passwords       | argon2id, verifying bcrypt    | current best practice; bcrypt read-only so imported accounts migrate on login |
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
3. **Media** ✅ — S3 uploads (any S3-compatible store), an automatic
   web/card/thumb variant ladder behind srcsets (renditions an older upload
   lacks are rebuilt on first request), media library UI, cmsImage template
   func with picker, private buckets supported by proxying media through
   the CMS (/cms/media/)
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
6. **Blog & news** ✅ — page-backed posts (two feeds, one engine),
   thumbnail/header images, Blog & News admin, .Post/cmsPosts template
   helpers, RSS; categories/tags deferred
7. **i18n** ✅ — /fr/ prefix routing, region-level content fallback,
   in-place locale switcher with untranslated-region badges and
   per-locale saves, admin form locale tabs, per-locale menu labels,
   localized RSS, FR admin strings (editor chrome strings deferred)
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
