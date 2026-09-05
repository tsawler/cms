# Navigation & site chrome

The parts of a page that live outside its content: menus, shared regions, the notice bar, and site-wide settings.

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

## The notice bar

A holiday closure, a delivery delay, a service interruption: the one
thing the whole site has to say at once, above everything else, on every
page. **Site settings → "Show a notice bar at the top of every page"**
switches one on.

It needs no template change. A layout that never mentions the bar gets
it injected immediately after its `<body>` tag, which puts it above the
header — and therefore above the menu — on every page of a site that
predates the feature:

```html
<body>
  <div class="cms-notice cms-notice-warning">…</div>   <!-- the CMS puts this here -->
  <header>…{{cmsNav "main"}}…</header>
```

To place it yourself instead — under a fixed header rather than above
it, or inside a wrapper your CSS grid owns — call `{{cmsNotice}}` and
the bar renders there and nowhere else.

**The words are not a setting.** They are a shared region — content,
with everything that implies: per-language, sanitized, staged as a draft
and live on the next Publish. That is deliberate; a settings value has
no locale, and a notice that could only be written once would be wrong
on half a bilingual site. `{{cmsShared "notice"}}` reaches the same
content if you ever want it somewhere else as well.

There are two ways to write them, and they store the same thing:

- **In the dialog.** "What it says" is an editable box right under the
  switch, with **bold**, *italic* and links — everything a notice bar
  has ever wanted — so a closure notice is one visit: tick, write,
  Save. The wording saves as a draft (the switch itself is live at
  once, like every other setting) and goes live with the next Publish,
  in the language being edited.
- **In the bar.** Click into it on the page and type, exactly as you
  edit the footer, with the site's full editor behind it.

The two do not fight: the dialog's box shows whatever the bar currently
holds, formatting and all, including typing you have not saved yet — and
a save that leaves the wording untouched writes nothing at all, so
visiting the dialog to change a colour cannot disturb the notice.

The dialog's box keeps bold, italic, links and line breaks, and nothing
else; ⌘B and ⌘I work, pasted text arrives as words, and a link is
checked before it is accepted (`javascript:` and friends are refused).
Anything richer than that — a picture in the bar, say — is written in
the bar itself.

Three switches go with it:

- **Colour** — one of five curated schemes (dark, accent, warning,
  alert, light). The CSS ships with the CMS rather than coming from your
  Tailwind build, so the bar looks right on a site whose stylesheet has
  never heard of it, and nothing here needs safelisting.
- **"Let visitors close the notice"** — adds a close button. The
  dismissal is remembered in the visitor's browser against the notice's
  current wording, so **rewriting the notice shows it again** to
  everyone who closed the last one. Without it the bar stays until it is
  switched off. Logged in, the button closes the bar for that pageview
  only and nothing is remembered — so you can see what visitors see
  without losing the bar you may need to edit. While you are actually
  editing the page it does nothing at all, since the bar is a region
  you might be typing in.
- **The switch itself** — off is off everywhere, whatever is written in
  the bar.

A bar switched on with nothing written in it shows to nobody; an editor
sees it with a placeholder to type over. Emptying the notice therefore
hides the bar as surely as switching it off does — and the placeholder
itself is never stored, so it cannot reach the live site.

### Making it scroll away under a fixed menu

The bar sits in the normal flow at the top of the page, so if your
header is sticky you get the behaviour for free — the notice scrolls out
of sight and the menu stays pinned. This is all it takes, in your own
stylesheet:

```css
header { position: sticky; top: 0; }
```

A `position: fixed` header is the case that needs work, because it is
out of the flow and will sit on top of the bar; sticky is the simpler
answer. Style the bar itself by overriding `.cms-notice` and friends —
`.cms-notice-inner` is the centred container, `.cms-notice-text` wraps
the editable words, `.cms-notice-close` is the close button.

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

## Light or dark editing tools

The editor's own chrome — the bottom edit bar, the left tool rail, the
floating block/section/column toolbars, the wrench menu, and TinyMCE's
formatting toolbar — is dark by default. On a site whose design is dark
that is chrome you have to hunt for, so **Site settings → Editor →
"Colour of the editing tools"** switches the lot to a pale scheme.

It is a property of the site, stored with the rest of the settings and
shared by everyone who edits it, and it changes nothing a visitor ever
sees. Saving applies it straight away: the chrome repaints from a set of
CSS custom properties, and any TinyMCE instances currently attached are
rebuilt, since a TinyMCE skin (`oxide` / `oxide-dark`, both vendored) is
fixed when an instance is created. Sites that predate the setting stay
dark.
