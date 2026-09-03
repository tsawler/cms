// Package snippets manages the pre-written HTML blocks editors can insert
// into rich regions from the in-place editor's palette. Snippets come from
// two places: the host application's config (per-customer components,
// versioned with the code) and the database (created by admins in the
// admin UI). Once inserted, a snippet is ordinary region content — edited
// in place, sanitized on save, and styled entirely by the host site's CSS.
package snippets

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/tsawler/cms/internal/sqldb"
)

// Snippet is one insertable block. ID is set only for database-stored
// snippets; config-registered ones have ID zero.
//
// A snippet with a non-nil Settings map is a *section preset*: instead of
// a block dropped into existing content, it is a starting point for a
// whole section — the editor offers it only in the "Add a section"
// chooser, and applies the settings (background, width, height, vertical
// alignment) to the new section along with the HTML. Keys and values are
// the section-settings vocabulary: "bg" and "width" name curated
// SectionStyles option keys, "height" is "50"/"75"/"100", "valign" is
// "center"/"bottom", "bgcolor" is #rrggbb, "bgimage" is a URL, and
// "bgposition" anchors that image as a pair of percentages across and
// down, e.g. "50% 20%" ("50% 50%" is centered, and is the default).
// Unknown keys or invalid values fall back to the defaults,
// same as the section settings dialog. Presets come from config or from
// the admin snippets UI (which offers the curated settings; the
// free-form bgcolor/bgimage/bgposition are config-only).
// Group names the category the editor's drawer files the snippet under:
// the drawer offers a category dropdown when any loaded snippet carries
// one. Grouping is config-only (like render.EditorStyle.Group) — admin-
// created snippets and ungrouped config snippets appear under "Custom".
type Snippet struct {
	ID        int64
	Name      string
	Group     string
	HTML      string
	Settings  map[string]string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrNotFound is returned when no snippet matches the query.
var ErrNotFound = errors.New("snippets: not found")

// videoSlotHTML is the shared "Click to add a video" placeholder used by
// the video snippet and section presets. While editing, the editor script
// turns a click on it into a chooser (media library or YouTube/Vimeo
// link) and replaces the slot with a <video> player or a bounded <iframe>
// embed — both shapes the editor sanitizer keeps.
// faqItemHTML is one question and its answer.
//
// <details> rather than a scripted accordion, and the reasons are all
// things a script would have to reimplement: it opens and closes with no
// JavaScript at all, it is keyboard-operable and announced as a
// disclosure by screen readers without any ARIA, it is findable by the
// browser's own in-page search even while closed, and it prints open.
//
// The cms-faq class carries no appearance of its own beyond the
// functional minimum in faqCSS — hosts style it, the way they style the
// nav. Deliberately not `open`: a page of questions that all start
// expanded is a page of answers, which is what the accordion was there
// to avoid.
const faqItemHTML = `<details class="cms-faq">
<summary>A question people ask?</summary>
<div class="cms-faq-body"><p>A short, direct answer.</p></div>
</details>`

// teamCardHTML is one person on a team page: a portrait, their name, the
// job title under it, and a line or two about them.
//
// The card is a bare <div>, not a nested .cms-snippet, for the same
// reason a column cell is bare — it is one of a run of siblings the card
// tool reshapes (editor/src/team.js), not a block sitting in one. Its
// marker class carries no appearance; everything the card looks like is
// in the Tailwind classes below, so a host that wants square-cornered
// portraits or a bigger name edits the markup rather than fighting CSS
// the module injected.
//
// The box is the Testimonials preset's, down to the class list: a
// bordered, rounded, padded panel. Two card designs a screen apart in
// the same library should not be two different opinions about what a
// card is, and a host restyling one has the other for free.
//
// The placeholders are read back by the card tool: deleting a card that
// still says exactly this goes without a prompt, and one somebody has
// written in asks first. Change the words here and they must change in
// team.js too — there is a test that says so.
const teamCardHTML = `<div class="cms-team-card rounded-xl border border-slate-200 bg-white p-6">
` + photoSlotSquare + `
<h3 class="mt-4 text-lg font-semibold">Full Name</h3>
<p class="text-sm text-slate-500">Job title</p>
<p class="mt-2 text-slate-600">A sentence or two about this person &mdash; what they do, and anything a visitor would want to know before getting in touch.</p>
</div>`

// teamGridHTML is the row of people itself, with no heading over it.
//
// The grid classes are the whole answer to "what happens when a fourth
// person joins". They set a *maximum* per row, not a fixed row: one
// column on a phone so a portrait is never squeezed to a thumbnail, two
// from the small breakpoint up, three from large — and a fourth card
// wraps onto a second row on its own, with no markup change and nothing
// for the editor to rebalance. Someone whose staff page wants four
// across changes lg:grid-cols-3 to lg:grid-cols-4 in the block's HTML
// (the ⟨/⟩ button) and safelists that one class.
//
// The cms-team class on the grid is what keeps the column tool off it.
// Both tools answer the same click, and they answer it differently: the
// column tool grows the track count so a new cell joins the same row,
// which is right for a two-up text layout and wrong for a staff list,
// where the point is that people wrap. A marked grid is invisible to
// columns.js (SELF_MANAGED there) and the card tool has it alone.
//
// No top margin here: the spacing between a heading and the people
// under it belongs to the heading, which is the only one of the two
// blocks below that has one.
const teamGridHTML = `<div class="cms-team grid grid-cols-1 gap-8 sm:grid-cols-2 lg:grid-cols-3">
` + teamCardHTML + `
` + teamCardHTML + `
` + teamCardHTML + `
</div>`

// teamBlockHTML is a whole staff page section: a heading over the grid.
// One block, one palette entry — and that is worth a note, because this
// shipped as two and both attempts were wrong.
//
// The first cut offered an inline "Team cards" alongside this preset
// with byte-identical markup: the same block under two names. The
// second gave the inline one its own shape (this, minus the <h2>), so
// the two at least looked different. They were still the same design
// twice, in a chooser that lists inline blocks and presets together —
// any block can start a section — and a reader hunting for the team
// layout had to work out which of two near-identical cards was meant.
//
// What made a second entry unnecessary rather than merely confusing is
// that the drawer inserts a preset inline anyway: clicking one from
// Snippets drops its HTML at the caret and ignores the settings, which
// only mean anything on a section (chooseSnippet in
// editor/src/snippets.js). So a staff grid could always go into an
// ordinary region — the second entry bought no capability, only the
// convenience of arriving without a heading, which is one delete.
const teamBlockHTML = `<div class="cms-snippet not-prose my-8">
<h2 class="mb-10 text-center text-3xl font-bold">Meet the team</h2>
` + teamGridHTML + `
</div>`

const videoSlotHTML = `<div class="cms-video-slot not-prose flex aspect-video w-full items-center justify-center rounded-lg border-2 border-dashed border-slate-300 bg-slate-50" data-cms-video-slot=""><p class="font-semibold text-slate-500">&#127916; Click to add a video</p></div>`

// DefaultSnippets is the Tailwind-first default library, used when the
// host does not configure its own. The markup avoids elements the editor
// sanitizer strips (no SVG, no scripts) and uses `not-prose` so Tailwind
// Typography doesn't restyle component internals. Like the editor styles,
// every class here must be safelisted in the site's Tailwind build (see
// the README).
// The cms-snippet marker class on each snippet's root makes the block
// manageable in the in-place editor: a dotted outline while editing, and
// a floating drag-handle/trash chrome when clicked. Like cms-btn (which
// marks a link as an editable button), it carries no CSS of its own.
func DefaultSnippets() []Snippet {
	return []Snippet{
		// A plain paragraph: the workhorse block. Deliberately unstyled,
		// so it picks up the section's own prose styles.
		{Name: "Text", Group: "Basic", HTML: `<p class="cms-snippet">Write your text here.</p>`},
		// Several paragraphs as one block. Deliberately not the "Text"
		// block: pressing Enter in a bare paragraph splits it into two
		// sibling blocks, which is right for a standalone paragraph and
		// useless for an article — here the paragraphs stay inside this
		// wrapper and read as one piece of running text.
		//
		// No not-prose: this *is* prose, and should take the section's
		// typography. No column classes either — a block is one column
		// until someone says otherwise, and the column tool splits this
		// one as readily as it splits "Text".
		{Name: "Article text", Group: "Basic", HTML: `<div class="cms-snippet">
<p>Write the opening paragraph here. Press Enter for another — they stay in this block, so the text runs on as one piece.</p>
<p>To put the article in columns, click it and use ＋ on the column tool.</p>
</div>`},
		{Name: "Callout", Group: "Basic", HTML: `<div class="cms-snippet not-prose my-6 rounded-lg border border-blue-200 bg-blue-50 p-4 text-blue-900">
<p class="font-semibold mb-1">Heads up</p>
<p>Something worth knowing goes here.</p>
</div>`},
		{Name: "Call to action", Group: "Basic", HTML: `<div class="cms-snippet not-prose my-6 rounded-xl bg-slate-900 p-6 text-center">
<p class="text-lg font-semibold text-white mb-3">Ready to get started?</p>
<a href="/" class="cms-btn inline-block rounded-lg bg-blue-600 px-5 py-2.5 font-semibold text-white">Get in touch</a>
</div>`},
		// A column holds blocks, so what ships in one is marked as a
		// block: each heading and paragraph here is its own, with its
		// own outline, move, duplicate, restyle and delete, all acting
		// inside its column. Nesting the marker is allowed in a cell
		// and nowhere else (editor/src/snippets.js, unnestSnippets) —
		// which is also why the cells themselves stay bare: a cell is a
		// column, not a block in one.
		{Name: "Two columns", Group: "Basic", HTML: `<div class="cms-snippet not-prose my-6 grid gap-6 sm:grid-cols-2">
<div><h3 class="cms-snippet font-semibold mb-1">First column</h3><p class="cms-snippet text-slate-600">Write something here.</p></div>
<div><h3 class="cms-snippet font-semibold mb-1">Second column</h3><p class="cms-snippet text-slate-600">And something here.</p></div>
</div>`},
		{Name: "Quote", Group: "Quotes", HTML: `<figure class="cms-snippet not-prose my-6 rounded-xl border border-slate-200 bg-slate-50 p-6">
<blockquote class="text-lg text-slate-700">&ldquo;A quote worth repeating.&rdquo;</blockquote>
<figcaption class="mt-3 text-sm font-semibold text-slate-500">&mdash; Name, Title</figcaption>
</figure>`},
		{Name: "Button link", Group: "Buttons", HTML: `<p class="cms-snippet not-prose my-4">
<a href="/" class="cms-btn inline-block rounded-lg bg-blue-600 px-5 py-2.5 font-semibold text-white">Button text</a>
</p>`},
		// A movie, dropped inline: the slot offers the media library or a
		// YouTube/Vimeo link when clicked while editing.
		{Name: "Video", Group: "Media", HTML: `<div class="cms-snippet not-prose my-6">
` + videoSlotHTML + `
</div>`},
		// Invisible on the live site; in edit mode the editor script makes
		// it visible and click-to-adjust (see editor.js and the height
		// allowance in the sanitizer policy).
		// One question, insertable on its own — which is what makes an
		// accordion growable: an editor puts the caret at the end of the
		// last answer and inserts another. The section preset below is
		// the same markup with a heading and three of these.
		{Name: "Question & answer", Group: "Basic", HTML: `<div class="cms-snippet">` + faqItemHTML + `</div>`},
		{Name: "Flexible space", Group: "Basic", HTML: `<div class="cms-spacer" data-height="48px" style="height: 48px"></div>`},
	}
}

// DefaultSectionPresets is the Tailwind-first default library of section
// presets — snippets with Settings, offered as one-click starting points
// in the "Add a section" chooser. Two markup strategies, chosen per
// preset: markup that should adapt when the editor later changes the
// section background skips not-prose and leans on prose/prose-invert for
// its colors (Hero, CTA, FAQ); grid layouts that Typography would restyle
// use not-prose with explicit slate colors and suit light backgrounds
// (Feature grid, Stats, Testimonials). Like DefaultSnippets, every class
// must be safelisted in the site's Tailwind build (see the README).
func DefaultSectionPresets() []Snippet {
	return []Snippet{
		// A tall dark band, content centered both ways. The heading and
		// paragraph take their color from prose-invert, so the hero stays
		// readable if the background is later switched.
		{Name: "Hero", Group: "Headlines", Settings: map[string]string{"bg": "dark", "width": "wide", "height": "75", "valign": "center"},
			HTML: `<div class="cms-snippet text-center">
<h1 class="text-4xl sm:text-5xl font-bold tracking-tight">A headline that lands</h1>
<p class="text-xl">One sentence on why visitors should care.</p>
<p><a href="/" class="cms-btn inline-block rounded-lg bg-blue-600 px-6 py-3 font-semibold text-white">Get started</a></p>
</div>`},
		{Name: "Feature grid", Group: "Features", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 text-center sm:grid-cols-3">
<div><h3 class="text-lg font-semibold mb-2">Fast</h3><p class="text-slate-600">Explain the first thing you do well.</p></div>
<div><h3 class="text-lg font-semibold mb-2">Simple</h3><p class="text-slate-600">Explain the second thing you do well.</p></div>
<div><h3 class="text-lg font-semibold mb-2">Reliable</h3><p class="text-slate-600">Explain the third thing you do well.</p></div>
</div>`},
		{Name: "Stats", Group: "Stats", Settings: map[string]string{"bg": "light", "width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 text-center sm:grid-cols-3">
<div><p class="text-4xl font-bold">10k+</p><p class="text-slate-600 mt-1">Happy customers</p></div>
<div><p class="text-4xl font-bold">99.9%</p><p class="text-slate-600 mt-1">Uptime</p></div>
<div><p class="text-4xl font-bold">24/7</p><p class="text-slate-600 mt-1">Support</p></div>
</div>`},
		{Name: "Testimonials", Group: "Quotes", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-6 sm:grid-cols-2">
<figure class="rounded-xl border border-slate-200 bg-slate-50 p-6">
<blockquote class="text-slate-700">&ldquo;Exactly what we needed &mdash; it just works.&rdquo;</blockquote>
<figcaption class="mt-3 text-sm font-semibold text-slate-500">&mdash; A happy customer</figcaption>
</figure>
<figure class="rounded-xl border border-slate-200 bg-slate-50 p-6">
<blockquote class="text-slate-700">&ldquo;The best decision we made this year.&rdquo;</blockquote>
<figcaption class="mt-3 text-sm font-semibold text-slate-500">&mdash; Another happy customer</figcaption>
</figure>
</div>`},
		// Collapsible questions rather than a run of headings and
		// paragraphs. A page of twenty answers is a wall of text; a page
		// of twenty questions is a list someone can scan. Add more with
		// the "Question & answer" snippet above — the markup is the same,
		// so a hand-added item and one from this preset look alike.
		//
		// The width key is the default anyway, but a preset needs at
		// least one setting or the API's omitempty would demote it to a
		// plain snippet.
		{Name: "FAQ", Group: "Basic", Settings: map[string]string{"width": "normal"},
			HTML: `<div class="cms-snippet">
<h2>Frequently asked questions</h2>
` + faqItemHTML + `
` + faqItemHTML + `
` + faqItemHTML + `
</div>`},
		// A staff page in one section. Wide, because three portraits
		// side by side want the room; everything else about how it
		// grows is in teamBlockHTML's comment.
		{Name: "Team", Group: "Team", Settings: map[string]string{"width": "wide"},
			HTML: teamBlockHTML},
		// Movie sections: a full-width player, and both split layouts.
		// The slot picks up a library video or a YouTube/Vimeo embed when
		// clicked while editing (see videoSlotHTML).
		{Name: "Full-width video", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
` + videoSlotHTML + `
</div>`},
		{Name: "Text + video", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 items-center sm:grid-cols-2">
<div><h2 class="text-2xl font-bold mb-2">A headline for the video</h2><p class="text-slate-600">Set up what viewers will see and why it&rsquo;s worth watching.</p></div>
` + videoSlotHTML + `
</div>`},
		{Name: "Video + text", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 items-center sm:grid-cols-2">
` + videoSlotHTML + `
<div><h2 class="text-2xl font-bold mb-2">A headline for the video</h2><p class="text-slate-600">Set up what viewers will see and why it&rsquo;s worth watching.</p></div>
</div>`},
		{Name: "Call-to-action banner", Group: "Basic", Settings: map[string]string{"bg": "accent", "valign": "center"},
			HTML: `<div class="cms-snippet text-center">
<h2 class="text-3xl font-bold">Ready when you are</h2>
<p>Tell visitors the one thing to do next.</p>
<p><a href="/" class="cms-btn inline-block rounded-lg bg-white px-6 py-3 font-semibold text-blue-700">Get in touch</a></p>
</div>`},
	}
}

// Store reads and writes admin-created snippets in Postgres.
type Store struct {
	db *sqldb.DB
}

// NewStore returns a Store backed by db.
func NewStore(db *sqldb.DB) *Store {
	return &Store{db: db}
}

const snippetColumns = "id, name, html, settings, created_at, updated_at"

func scanSnippet(row sqldb.Scanner) (*Snippet, error) {
	var s Snippet
	err := row.Scan(&s.ID, &s.Name, &s.HTML, sqldb.JSONInto(&s.Settings), &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// All returns every stored snippet, ordered by name.
func (s *Store) All(ctx context.Context) ([]Snippet, error) {
	rows, err := s.db.Query(ctx, "SELECT "+snippetColumns+" FROM cms_snippets ORDER BY name")
	if err != nil {
		return nil, err
	}
	return sqldb.CollectRows(rows, func(row sqldb.Scanner) (Snippet, error) {
		sn, err := scanSnippet(row)
		if err != nil {
			return Snippet{}, err
		}
		return *sn, nil
	})
}

// Count returns how many stored snippets exist — the admin adds the
// host-registered ones and shows the total beside its Snippets nav entry.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, "SELECT count(*) FROM cms_snippets").Scan(&n)
	return n, err
}

// GetByID returns one stored snippet, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id int64) (*Snippet, error) {
	return scanSnippet(s.db.QueryRow(ctx,
		"SELECT "+snippetColumns+" FROM cms_snippets WHERE id = $1", id))
}

// Insert stores a new snippet and returns its id. A nil Settings map is
// stored as NULL (plain block); a non-nil one makes it a section preset.
func (s *Store) Insert(ctx context.Context, sn *Snippet) (int64, error) {
	id, err := s.db.InsertID(ctx,
		"INSERT INTO cms_snippets (name, html, settings) VALUES ($1, $2, $3)",
		sn.Name, sn.HTML, sqldb.JSON(sn.Settings))
	if err != nil {
		return 0, err
	}
	sn.ID = id
	return sn.ID, nil
}

// Update saves a stored snippet's name, HTML, and settings.
func (s *Store) Update(ctx context.Context, sn *Snippet) error {
	tag, err := s.db.Exec(ctx,
		"UPDATE cms_snippets SET name = $1, html = $2, settings = $3, updated_at = now() WHERE id = $4",
		sn.Name, sn.HTML, sqldb.JSON(sn.Settings), sn.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a stored snippet. Content already inserted into pages is
// unaffected — inserted snippets are plain region HTML.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, "DELETE FROM cms_snippets WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
