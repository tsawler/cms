# Snippets, code blocks & sections

The building blocks an editor drops into a page: the snippet palette, custom code blocks, and the section system that arranges them.

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

The ⋯ menu's **Page history** goes back to an earlier published version.
Every publish keeps a copy of what went live — attributed to whoever
published it — and picking one from the list puts it back into the
*draft*: nothing changes on the site until you Publish, and until then
**Discard draft** returns you to what visitors are seeing. Since the
editor already shows draft content, restoring is its own preview — the
page reloads onto the old version and you are looking at it.

The same history is on the page's admin form (and a post's, under Blog &
News) as a **History** link, which adds a Preview of any version through
the real site templates and a **Restore & publish** for when something is
wrong on the live site right now. How many versions each page keeps is
`Config.PageVersionsKept` (default 50, oldest dropped first).

A version carries the custom-code blocks the page used, so a widget that
was deleted from the library since comes back with the page that showed
it. One that still exists but has been rewritten is left alone and
mentioned instead — the library is shared, and restoring one page should
not rewrite a widget other pages are showing.

Media and shared regions like the footer are not part of a version: they
are stored outside the page and stay as they are now. Shared regions keep
a history of their own, since they belong to the site rather than to any
one page.

The rail's **Snippets** button opens a drawer of ready-made blocks: drag
one onto a rich region (or click to insert at the cursor), then edit its
text and images in place like any other content. Snippets come from two
places:

- **`Config.Snippets`** — per-customer components, versioned with your
  code. Nil gets a Tailwind-first default library: inline blocks
  (callout, call-to-action, two columns, article text, quote, button
  link, video,
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

#### Keeping the palette current

**Config snippets are code, not data.** They are registered on every
startup and merged with the admin-created ones when the palette is
served — nothing about them is stored in the database. So a module
upgrade that adds blocks delivers them on the next `go get -u` and
rebuild: no seed to re-run, no migration, and an editor's own snippets
and existing pages are untouched.

That only holds if your config asks for the whole library, and the
reliable way to do that is `snippets.All()` — everything the module
ships, in palette order:

```go
cfg.Snippets = snippets.All()   // identical to leaving it nil
```

Customize by composing *on top of* it, so the customization is expressed
against whatever the module ships rather than against a list copied out
of it:

```go
// Yours as well as the module's.
cfg.Snippets = append(snippets.All(), mySnippets()...)

// Yours instead of one group of the module's.
lib := slices.DeleteFunc(snippets.All(),
    func(s cms.Snippet) bool { return s.Group == "Buttons" })
cfg.Snippets = append(myButtons(), lib...)

// Inline blocks only — no section presets, from any source.
cfg.Snippets = append(
    slices.DeleteFunc(snippets.All(),
        func(s cms.Snippet) bool { return len(s.Settings) != 0 }),
    myPresets()...)
```

The four underlying constructors — `DefaultSnippets`, `LibrarySnippets`,
`DefaultSectionPresets`, `LibrarySectionPresets` — are still exported and
still the way to take a deliberate subset. Be aware of what that costs:
such a list is a snapshot. Blocks added to a constructor it names arrive
on upgrade; blocks added to one it does not name, or to a list that did
not exist when the config was written, never arrive at all, on any site,
with nothing to say so. A palette that is quietly missing a third of the
library is the failure this is warning about, and `snippets.All()` is
how not to have it. (A test in the module fails the build if a new
constructor is not folded into `All`, so "everything the module ships"
stays true rather than aspirational.)

Clicking an inserted block raises its chrome:

| | |
|---|---|
| `⠿` | drag the block anywhere on the page |
| `⌃` `⌄` | move it up or down one place |
| ▤ ▤ | duplicate it above or below |
| `⟨/⟩` | edit the block's HTML |
| ⚙ | block settings — background and text colour, spacing, corner roundness |
| 🗑 | delete the block |

The two move arrows trade the block with the one directly above or
below it, and each hides when there is nothing on that side. They are
the drag handle done precisely: a drag can go anywhere in the page but
travels through the rich-text editor's drop caret, which will sometimes
land a block *inside* the one it was aimed past; stepping over one
sibling cannot.

The duplicate pair copies the block whole and lands the copy on the
side chosen, with the chrome following the copy — it is the thing just
made, so it is the thing about to be typed into. A duplicated
custom-code block keeps its key, so the two placeholders are two
instances of one library entry, which is what the block's own starter
script is written to expect.

Everything on this toolbar is undoable with the usual keystroke.

#### Columns

A block that is a row of columns raises a **second, smaller toolbar**
alongside the block chrome. It is drawn on the row's top border, centred
over the column that was clicked — so it covers no content, and in a row
of identical cells it is obvious which one it has hold of (that column is
tinted too). Everything on it acts on that one column:

| | |
|---|---|
| `‹` `›` | move this column along the row |
| `⇥⇤` `⇤⇥` | make it narrower or wider, a track at a time |
| ▐▌ ▌▐ | duplicate it to the left or the right |
| `＋` | add a column after it |
| 🗑 | remove it |

Duplicating keeps the column's contents; adding blanks them. Adding a
column is making room for something not written yet, while duplicating
one is wanting a second of what is already there — the third card in a
row of cards, the fourth price tier.

**A row also gets drag handles**, one on each boundary between two
columns, drawn in the gutter. Dragging one snaps to the twelve tracks
as you go, so what you see mid-gesture is what you get — a column's
width *is* a track span, and a free-form drag would only have to round
itself on release and make the layout jump. The whole drag is one undo
level, and a drag that ends where it began leaves the markup untouched,
including the conversion to the twelve-track form it would otherwise
have made.

The handles do not replace `⇥⇤` and `⇤⇥`. Those two are the keyboard
and touch path, and they still say in words what the handle says by
being where it is. What the handle adds is that it names its own pair:
the buttons have to pick a neighbour by rule (the next column, or the
previous one for the last in the row), while a boundary you grab is
unambiguous.

Handles appear only where those buttons do — a row whose track count
does not divide twelve has neither. They also hide when the row is
drawn stacked, which it is on a phone: there is no gutter to put one
in, and a drag there would be editing the `sm:` width while showing the
mobile layout.

A column here is a real box with its own content, never a slice of one
continuous stream. That distinction is the whole design. CSS offers the
other thing — `column-count`, which reflows text down one column and up
into the next — and it is almost never what someone means by "put this
in two columns"; it also puts the leading paragraph's top margin at the
top of the first column and not the second, so the two start at visibly
different heights.

**A block that is not a row yet** gets the same toolbar with the two
edits that can make it one:

- `＋`, captioned *Split into two columns* — what was there goes in the
  first column, a placeholder in the second. Blocks built around a
  placed thing rather than a written one (a button, a video or photo
  slot, an image, a nested block) are not offered this, and neither is
  an empty block: cutting a block designed around a picture in half is
  a judgement about its design, not a column edit.
- ▐▌ ▌▐, *Duplicate this block to the left / right* — the block is
  wrapped in a fresh two-column row with a copy of itself beside it.
  Copying a block whole is not that judgement, so the button and photo
  blocks the split refuses are welcome here; all it needs is something
  in the block to copy. This is the only way left and right mean
  anything for a block, since blocks are a stack and only columns sit
  side by side.

  The block goes into its column intact — background, padding and
  rounding travelling with it — so a duplicated callout is two tinted
  boxes side by side rather than one wide tint with the words twice
  inside it. Custom-code blocks stay out: the wrapping row takes over
  as the block, and a code block that is no longer the block can no
  longer open its library entry from `⟨/⟩`. Duplicating one above or
  below from the block chrome keeps it whole.

Once a block *is* a row, the full toolbar applies to it whatever it
holds.

A new column is a copy of the one you added it after, so it keeps that
row's classes and any photo or video slot it carried, with its words
replaced by placeholders. Removing a column that holds real content asks
first. Taking the last column out of a row removes the row, and where
the row *is* the block, the block goes with it.

Two forms of row markup exist, and the tool moves between them exactly:

- the **even** form, `sm:grid-cols-K` with K cells carrying no span of
  their own — what every stock snippet ships, and what a row goes back
  to whenever its columns are equal and there are four or fewer;
- the **spanned** form, `sm:grid-cols-12` with a `sm:col-span-N` on each
  cell summing to twelve — taken on the moment a column is resized, or
  when a row grows past four columns.

Twelve divides by every track count the even form uses, so
`sm:grid-cols-3` becomes twelve tracks of span 4 with nothing rounded
and nothing moved. A row whose track count does not divide twelve (five
tracks, seven) can still gain and lose columns; it just can't be
resized, and says so by hiding those two buttons rather than by
rounding. Resizing always steps a *pair* of neighbours — one track out
of the column next door, or back into it — so the row stays exactly full
and no sequence of clicks can leave a gap or an overflow. Six columns is
the ceiling.

Adding or removing a column re-evens the widths. That is deliberate:
after a fourth column joins a row someone had set to 8-4, no
redistribution of the old widths is the obvious one, and even columns
are both predictable and one click from being reshaped again.

Two details worth knowing. Counts are read from the *largest*
`grid-cols-*` class on the element, so the common
`grid-cols-1 sm:grid-cols-2` idiom edits the `sm:` rule and leaves the
mobile stack alone — and every rewrite goes back at the prefix it was
read from. And a block holding several separate grids gets no tool
rather than a guess about which one was meant.

Every class the tool can write is declared in Go, by
`render.EditorAppliedClasses` — these are classes the editor puts into
content on its own, which no snippet carries and no scan of stored
content can find until after the first save that uses one. They are
folded into the generated content stylesheet's corpus so a fresh install
is covered, and the safelists below are checked against them by a test,
so the two cannot drift. Those are the `sm:` forms, because that is what
every stock snippet uses. If your own snippets set their tracks at
another breakpoint — or at none — the tool writes back at whatever prefix
it read, and you have to safelist that prefix's `grid-cols-*` and
`col-span-*` yourself.

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
    "mb-1", "mb-2", "mb-3", "mb-10", "mt-1", "mt-3", "my-4", "my-6", "my-8",
    "grid", "gap-6", "gap-8", "grid-cols-1", "sm:grid-cols-1",
    "sm:grid-cols-2", "sm:grid-cols-3", "sm:grid-cols-12",
    // The team grid's wrapping ladder (see "Team pages" below). Change
    // lg:grid-cols-3 in the block and safelist whatever you change it to.
    "lg:grid-cols-3",
    "columns-1", "columns-2", "columns-3",
    "inline-block", "flex", "items-center", "justify-center",
    "w-full", "aspect-video", "border-2", "border-dashed",
    "border-slate-300",
    // Used by the imported block library (buttons, profiles, gallery,
    // numbered features, pricing, achievements, client quotes):
    "aspect-square", "bg-slate-200", "border-blue-600",
    "border-slate-900", "h-0.5", "hover:bg-blue-600",
    "hover:bg-slate-300", "hover:bg-slate-900", "hover:text-white",
    "mr-2", "mt-2", "mt-4", "mt-6", "mt-10", "px-8", "size-24",
    "sm:col-span-1", "sm:col-span-2", "sm:col-span-3", "sm:col-span-4",
    "sm:col-span-5", "sm:col-span-6", "sm:col-span-7", "sm:col-span-8",
    "sm:col-span-9", "sm:col-span-10", "sm:col-span-11", "sm:col-span-12",
    "sm:grid-cols-4", "text-5xl", "text-6xl",
    "sm:text-7xl", "text-slate-200", "text-slate-400", "text-slate-900",
    "tracking-widest", "uppercase", "w-10", "object-contain",
],
```

Deleting a snippet never changes pages that already inserted it — inserted
snippets are ordinary page content.

### Questions and answers

Two of the stock snippets build collapsible questions: the **FAQ**
section preset, which starts with a heading and three of them, and
**Question & answer**, which inserts one more. They are the same markup,
so an accordion grows by putting the caret after the last answer and
inserting another — and a hand-added question is indistinguishable from
the three the section came with.

Each one is a `<details>`:

```html
<details class="cms-faq">
  <summary>A question people ask?</summary>
  <div class="cms-faq-body"><p>A short, direct answer.</p></div>
</details>
```

No JavaScript is involved, which is the reason for the element rather
than a scripted panel: it opens and closes on its own, it is
keyboard-operable and announced as a disclosure without any ARIA, the
browser's in-page search finds text inside a closed one, and it prints
open.

**Editing them is a small toolbar.** Click a question while editing and a
pill appears on its top edge with the four verbs a list has: move it up,
move it down, add one below it, delete it. A new question is a copy of
the one it was added after with its words replaced by placeholders, so it
inherits whatever classes that markup carries and a site that has
restyled its accordions gets restyled new questions for free.

The tool works on any `<details class="cms-faq">`, whether it came from a
snippet or from content that already had one — it does not require the
`cms-snippet` wrapper the block chrome hangs from.

"Up" and "down" move a question within its own run: consecutive
`.cms-faq` siblings, so a page with two accordions under two headings has
two independent lists, and the buttons disappear at each end rather than
carrying a question across the gap. Deleting asks first only when the
question has been written in; one still showing its placeholder goes
without a prompt.

**Answers are open while editing.** A `<summary>` inside an editable
region does not toggle when clicked — the click places the caret, which
is what editing the question needs — so a closed answer would otherwise
have no way of being opened and no way of being written in. Edit mode
shows every answer instead, dimmed and with the caret still pointing
right so a question goes on reading as closed. Both the question and the
answer are ordinary rich text: click and type.

Nothing is written to the document to achieve that. There is no `open`
attribute to strip at save time, and what is stored is exactly what was
there — an accordion that ships expanded is not an accordion.

`{{cmsHead}}` ships the functional minimum — a pointer cursor, a rotating
caret in place of the browser's three different default markers, a
keyboard focus ring, and a reset for the top margin Typography would
otherwise put between a question and its answer. Everything else is
yours, addressed through the two classes:

```css
.cms-faq          { border-bottom: 1px solid #e2e8f0; }
.cms-faq > summary{ padding: .75rem 0; font-weight: 600; }
.cms-faq-body     { padding-bottom: 1rem; }
```

Every injected rule is wrapped in `:where()`, so it carries no
specificity and a plain class selector of yours wins — which matters
because `{{cmsHead}}` is emitted *after* your stylesheet, and an
unwrapped rule would otherwise beat an equally specific one of yours on
source order alone.

Nothing here needs safelisting: the classes are the CMS's own and its
stylesheet defines them.

### Team pages

Two stock section presets build a staff page, both under the Team
category: **Team**, a "Meet the team" heading over a grid of three
cards, and **Team profiles**, the imported design — the same grid with
circular portraits and no card panels.

Either can also go into an ordinary rich region: open the **Snippets**
drawer instead of the section chooser and click it, and the markup is
inserted at the caret (the section settings only mean something on a
section, so they are ignored). Delete the heading if the page already
has one.

Each card is a photo slot, a name, the job title under it, and a line or
two about the person:

```html
<div class="cms-team mt-10 grid grid-cols-1 gap-8 sm:grid-cols-2 lg:grid-cols-3">
  <div class="cms-team-card">
    <div class="cms-photo-slot …" data-cms-photo-slot=""> … </div>
    <h3 class="mt-4 text-lg font-semibold">Full Name</h3>
    <p class="text-sm text-slate-500">Job title</p>
    <p class="mt-2 text-slate-600">A sentence or two about this person …</p>
  </div>
  …
</div>
```

**The grid classes are a maximum per row, not a fixed row.** One column
on a phone, two from `sm:`, three from `lg:` — and a fourth person wraps
onto a second row on their own. Nobody gets narrower because a colleague
was hired, and a portrait is never squeezed to a thumbnail on a small
screen. A staff page that wants four across changes `lg:grid-cols-3` in
the block's HTML (the ⟨/⟩ button) and safelists the class it changed to.

**Editing is a small toolbar.** Click a card while editing and a pill
appears centred on its top edge, with the column tool's verbs minus the
two resize buttons: move this person left, move them right, duplicate
them to the left, duplicate them to the right, add a blank card after
them, delete them. Duplicating copies the card whole — words, photo and
all — because "another one like this" is usually about the card's shape;
the ＋ is for a genuinely blank one, and it puts the photo slot back so
the newest hire never silently wears a colleague's portrait.

"Left" and "right" move a person within their own run: consecutive
`.cms-team-card` siblings, so a page with a Leadership grid and a Sales
grid has two independent lists and the arrows disappear at each end
rather than carrying somebody across the gap. Deleting asks first only
when the card has been written in or has a photo; one still showing its
placeholders goes without a prompt. Like the question tool, this works
on any `.cms-team-card` — including markup converted from an old site —
and does not need the `cms-snippet` wrapper the block chrome hangs from.

**`cms-team` on the grid is what keeps the column tool off it.** Both
tools answer a click inside a grid, and they answer it differently: the
column tool grows the track count so a new cell joins the *same* row,
which is right for a two-up text layout and exactly wrong for a staff
list. A grid carrying that class is invisible to the column tool, so the
two can never write the same `grid-cols-*` class. Put it on your own
wrapping card layouts to get the same treatment — and the card toolbar
along with it, for any child you mark `cms-team-card`.

Neither class carries any appearance: everything these cards look like
is in the Tailwind classes above, which is why changing the design is
editing the markup rather than overriding CSS the module injected.

### Sliders

**Slider**, under Media in the section chooser, is a run of slides that a
visitor moves through. Each slide is a picture from the media library
with a box of content on top of it — and the content is ordinary content:
click into it and type, drop a snippet in from the drawer, put a button
in it. A slide is not a headline field and a subtitle field.

```html
<div class="cms-snippet cms-slider cms-slider-bleed not-prose" data-cms-slider="fade">
  <div class="cms-slide cms-slide-scrim">
    <div class="cms-photo-slot …" data-cms-photo-slot=""> … </div>
    <div class="cms-slide-body text-center"> … heading, text, button … </div>
  </div>
  …
</div>
```

**That is the whole of what is stored.** No arrows, no dots, no "which
slide is showing" — `{{cmsScripts}}` ships a small script that builds the
controls from the slide count at runtime, so adding a slide cannot leave
a stale dot behind, and nothing a `<button>` in content could break has
to be allowed through the sanitizer. `{{cmsHead}}` ships the layout. Both
ride along the way the nav's already do; **a template missing
`{{cmsScripts}}` gets a slider that never moves.**

**Choosing pictures** works the two ways everything else in the library
does: click a slide's "Click to add a photo" slot, or use the gear. Both
produce the same `<img>`.

**The gear** — click a slider while editing — holds everything that is
about the run rather than about one slide:

| | |
|---|---|
| Transition | **Fade** or **Slide** |
| Move on its own | off, or every 4 / 6 / 9 seconds |
| Slides | one row per slide, in playing order: move up, move down, change the picture, delete — and **Add a slide…**, which picks from the media library |

Reordering a row moves the whole slide, words and all. A slide added from
the gear is a copy of the first one with its words replaced by
placeholders, so it inherits whatever that site's slides look like —
including the button, if they carry one. The last slide has no delete
button: an empty slider is invisible and unclickable, and removing the
whole thing is the block chrome's trash can.

**While editing, the stack comes apart.** A slider shows one slide at a
time, which would leave the other three unreachable and unwritable, so
edit mode lays them out one under another, numbered to match the gear's
list, and stops the full-bleed one bleeding (it would otherwise run under
the tool rail). Autoplay stops too. None of that is written to the
document: what is saved is exactly the slides.

**Height** belongs to the slider, not to the section — a section's height
setting is a min-height on a wrapper the slider cannot fill, so a slider
in a 75vh section would be a short slider in a tall empty band. The
full-bleed one is `min(70vh, 40rem)`; override `.cms-slider-bleed` to
change it.

**Styling.** Everything the module injects is layout and legibility — the
crop, the stack, the scrim behind the words, and controls that are
visible and hittable and nothing more. Colour and shape are yours,
through `.cms-slider`, `.cms-slide`, `.cms-slide-body`,
`.cms-slider-nav` (`.cms-slider-prev` / `.cms-slider-next`),
`.cms-slider-dots` and `.cms-slider-dot`. Only the slide's own text
classes need safelisting; the rest are the CMS's own.

Sliders respect `prefers-reduced-motion` (the slide changes, it just does
not travel), pause autoplay while the pointer is over them or focus is
inside them, stop in a background tab, take the arrow keys when focused,
and keep hidden slides out of the reading order so a screen reader
announces one headline rather than four.

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
untouched. Every save also empties the placeholders on the way in — the
editor before it sends, the server as it stores — so a widget's output
never becomes page content, however the page was serialized.

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
  bar's **Page CSS & JS** and the wrench menu's **Site code** panels are
  still the right tool — they inject into `<head>` and before `</body>`.

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
    Sizes: []cms.SectionOption{
        {Key: "normal", Label: "Normal", Class: ""},
        {Key: "large", Label: "Large", Class: "prose-lg"},
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
its own axis. It is configured by default — Normal / Roomy / Snug /
Tight / None → `py-12` / `py-20` / `py-6` / `py-3` / `py-0` — and the
default `Widths` carry no `py-*` of their own, so the two compose:

```go
Paddings: []cms.SectionOption{
    {Key: "normal", Label: "Normal", Class: "py-12"},
    {Key: "tight",  Label: "Tight",  Class: "py-3"},
    {Key: "none",   Label: "None",   Class: "py-0"},
},
```

Separating it from `Widths` is worth it for two reasons:

- Bundled into `Widths`, "the same measure but tighter" has no
  expression except a second width option, and the list multiplies by
  every spacing anyone wants.
- It is not the same thing as the section **height** setting, which is a
  `min-height` and can only ever make a section *taller*. An editor who
  wants less space reaches for height first, finds "Auto" already
  selected, and concludes the CMS cannot do it.

The class is emitted after the width class, so moving a `py-*` out of a
width preset and into `Paddings` needs no thought about ordering. The
first option is the default and is what content saved before the axis
existed resolves to — make it match whatever padding your width presets
used to carry and nothing already published will move.

Replacing `SectionStyles` wholesale replaces this too: a host that keeps
its `py-*` inside the width presets simply leaves `Paddings` nil and the
axis does not appear. It is not backfilled the way `Corners` is, because
a default `py-12` appended to width classes that already carry their own
spacing would fight it.

### Text size

`Sizes` is how editors change font size — one setting per section that
scales that section's whole type scale, headings and body and their
leading together. It is configured by default — Normal / Large / Extra
large / Small → nothing / `prose-lg` / `prose-xl` / `prose-sm`:

```go
Sizes: []cms.SectionOption{
    {Key: "normal", Label: "Normal", Class: ""},
    {Key: "large",  Label: "Large",  Class: "prose-lg"},
},
```

This is deliberately the *only* font-size control the editor offers, and
the scope is the reason. Font size is the one setting where per-element
freedom reliably produces worse pages than no control at all: a
paragraph bumped two steps beside headings your theme sized does not read
as emphasis, it reads as broken. Scaling the container keeps the ratios
your theme chose, so the worst an editor can do is a section that is
bigger or smaller — never one that is internally inconsistent. (A
per-element step is still available where it belongs: as a named entry
in the Styles menu, like the default "Lead paragraph" and "Small print".)

Two things to know if you replace the list:

- The default classes are [Tailwind
  Typography](https://github.com/tailwindlabs/tailwindcss-typography)
  size modifiers, and they go on the **content container** — the element
  the width presets put `prose` on. A `prose-lg` with no `prose` beside
  it styles nothing, which is why `Sizes` is not backfilled the way
  `Corners` is: a host whose own `Widths` don't carry `prose` would get a
  dropdown where every choice looks applied and no text moves. Declaring
  `Sizes` is how you say your container is one the classes can reach. If
  it isn't a prose container, use container-level `text-*` classes here
  instead.
- **The first option must contribute no class.** Content saved before the
  axis existed resolves to it, and unlike spacing there is no size that
  old markup was implicitly carrying — anything else moves every
  published section the moment the axis appears.

Safelist the default section classes along with the rest:

```js
safelist: [
    "bg-slate-50", "bg-slate-900", "bg-blue-700", "prose", "prose-slate",
    "prose-invert", "mx-auto", "max-w-3xl", "max-w-5xl", "max-w-none",
    "px-6", "py-12", "py-20", "py-6", "py-3", "py-0",
    "prose-sm", "prose-lg", "prose-xl",
    "rounded-lg", "rounded-2xl", "rounded-3xl",
],
```
