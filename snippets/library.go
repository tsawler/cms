package snippets

// This file is the imported block library: designs converted by hand to
// Tailwind from the InnovaStudio ContentBuilder "minimalist-blocks" pack
// (licensed with source; conversions harmonize the originals to the
// module's slate/blue palette). The conversions follow the same rules as
// snippets.go: no scripts, no SVG, no inline styles (the editor
// sanitizer strips them), not-prose on component markup, and every class
// safelisted in the README and QUICKSTART.
//
// Where the originals used icon fonts (Ionicons) or bundled stock
// photos, the conversions substitute photo slots (below); icons become
// text characters or are dropped.

// Photo slots: the "Click to add a photo" placeholders the imported
// photo-bearing blocks ship with. They are the package's, not this
// file's — teamCardHTML in snippets.go takes the square one, because a
// staff portrait wants exactly the shape a gallery tile does and a
// second copy of these class lists is a second thing to keep in step
// with photos.js. Like videoSlotHTML, the marker
// attribute makes the slot clickable while editing: the editor opens
// the media library and swaps the slot for an <img> that keeps the
// slot's shape classes (aspect ratio, size, rounding, centering) plus
// object-cover — so the sanitizer allowance, the editor script
// (editor/src/photos.js), and these class lists must stay in step.
const (
	photoSlotWide   = `<div class="cms-photo-slot not-prose flex aspect-video items-center justify-center rounded-lg border-2 border-dashed border-slate-300 bg-slate-50" data-cms-photo-slot=""><p class="font-semibold text-slate-500">&#128247; Click to add a photo</p></div>`
	photoSlotSquare = `<div class="cms-photo-slot not-prose flex aspect-square items-center justify-center rounded-lg border-2 border-dashed border-slate-300 bg-slate-50" data-cms-photo-slot=""><p class="font-semibold text-slate-500">&#128247; Click to add a photo</p></div>`
	// Too small for a label: the camera alone, with the same click
	// affordance (cursor + hover outline) the other slots get.
	photoSlotCircle = `<div class="cms-photo-slot not-prose mx-auto flex size-24 items-center justify-center rounded-full border-2 border-dashed border-slate-300 bg-slate-50" data-cms-photo-slot=""><p class="text-2xl">&#128247;</p></div>`
	// Logos letterbox instead of crop: object-contain is inert on the
	// slot div itself, but imgClassFor carries it onto the inserted
	// image in place of the usual object-cover.
	photoSlotLogo = `<div class="cms-photo-slot not-prose flex aspect-video items-center justify-center rounded-lg border-2 border-dashed border-slate-300 bg-slate-50 object-contain" data-cms-photo-slot=""><p class="font-semibold text-slate-500">&#128247; Click to add a logo</p></div>`
	// The map slot: clicking it while editing prompts for a Google
	// Maps link (or the Share > Embed code, or just a typed address)
	// and swaps in a bounded maps iframe — see editor/src/maps.js and
	// the embedURLRe maps forms in the sanitizer.
	mapSlotHTML = `<div class="cms-map-slot not-prose flex aspect-video w-full items-center justify-center rounded-lg border-2 border-dashed border-slate-300 bg-slate-50" data-cms-map-slot=""><p class="font-semibold text-slate-500">&#128506; Click to add a map</p></div>`
)

// LibrarySnippets returns the imported inline blocks. They ship with the
// defaults when Config.Snippets is nil.
func LibrarySnippets() []Snippet {
	return []Snippet{
		// From buttons-01: a filled/outline pair on one line.
		{Name: "Button pair", Group: "Buttons", HTML: `<p class="cms-snippet not-prose my-4">
<a href="/" class="cms-btn mr-2 inline-block rounded-lg bg-slate-200 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-700 hover:bg-slate-300">Read more</a>
<a href="/" class="cms-btn inline-block rounded-lg border-2 border-blue-600 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-blue-600 hover:bg-blue-600 hover:text-white">Buy now</a>
</p>`},
		// From buttons-10: the same pair, pill-shaped.
		{Name: "Pill buttons", Group: "Buttons", HTML: `<p class="cms-snippet not-prose my-4">
<a href="/" class="cms-btn mr-2 inline-block rounded-full bg-slate-200 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-700 hover:bg-slate-300">Read more</a>
<a href="/" class="cms-btn inline-block rounded-full border-2 border-blue-600 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-blue-600 hover:bg-blue-600 hover:text-white">Buy now</a>
</p>`},
		// From buttons-02/-03/-08/-09: the pairs' two looks as single
		// buttons, rectangular and pill. The originals' small-size
		// variants (buttons-04..06, -10..12) aren't converted — the
		// button gear's size control covers that axis.
		{Name: "Filled button", Group: "Buttons", HTML: `<p class="cms-snippet not-prose my-4">
<a href="/" class="cms-btn inline-block rounded-lg bg-slate-200 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-700 hover:bg-slate-300">Read more</a>
</p>`},
		{Name: "Outline button", Group: "Buttons", HTML: `<p class="cms-snippet not-prose my-4">
<a href="/" class="cms-btn inline-block rounded-lg border-2 border-blue-600 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-blue-600 hover:bg-blue-600 hover:text-white">Buy now</a>
</p>`},
		{Name: "Filled pill button", Group: "Buttons", HTML: `<p class="cms-snippet not-prose my-4">
<a href="/" class="cms-btn inline-block rounded-full bg-slate-200 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-700 hover:bg-slate-300">Read more</a>
</p>`},
		{Name: "Outline pill button", Group: "Buttons", HTML: `<p class="cms-snippet not-prose my-4">
<a href="/" class="cms-btn inline-block rounded-full border-2 border-blue-600 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-blue-600 hover:bg-blue-600 hover:text-white">Buy now</a>
</p>`},
		// From article-02: centered title over an accent rule, body in
		// two columns. The original justified its text; these stay
		// left-aligned (no text-justify in the safelist, and ragged-
		// right reads better on the web).
		//
		// The body paragraphs are blocks, as everything in a column is
		// (see "Two columns" in snippets.go); the title and the rule
		// above them are not, because they are parts of this block's own
		// design and a marker outside a cell would have unnestSnippets
		// lift them out of it.
		{Name: "Two-column article", Group: "Article", HTML: `<div class="cms-snippet not-prose my-6">
<h2 class="text-center text-3xl font-bold">Flying high</h2>
<div class="mx-auto mt-3 h-0.5 w-10 bg-blue-600"></div>
<div class="mt-6 grid gap-6 sm:grid-cols-2">
<div>
<p class="cms-snippet text-slate-600">Open the article here: set the scene and tell readers why it matters.</p>
<p class="cms-snippet mt-3 text-slate-600">Carry the thought into a second paragraph.</p>
</div>
<div>
<p class="cms-snippet text-slate-600">The second column continues the story.</p>
<p class="cms-snippet mt-3 text-slate-600">And wraps up the introduction.</p>
</div>
</div>
</div>`},
		// From article-04: kicker, display title, byline, two columns.
		//
		// Each column's paragraph sits in a <div> of its own rather than
		// being the grid's child directly. The wrapper is what makes it
		// a column with a paragraph in it instead of a paragraph that is
		// a column: Enter in the latter adds a cell, so writing a second
		// line in a two-column row would silently make it a three-column
		// one. With the cell there, Enter does what it says, and the
		// paragraph can carry the block marker every column's contents
		// carry (see "Two columns" in snippets.go).
		{Name: "Bylined article", Group: "Article", HTML: `<div class="cms-snippet not-prose my-6">
<p class="text-sm uppercase tracking-widest text-slate-500">A beautiful day in October</p>
<h2 class="mt-2 text-4xl font-bold tracking-tight">Time to think, time to create.</h2>
<p class="mt-3 text-slate-500">&mdash; By David Anderson</p>
<div class="mt-6 grid gap-6 sm:grid-cols-2">
<div><p class="cms-snippet text-slate-600">Open the article here: set the scene and tell readers why it matters.</p></div>
<div><p class="cms-snippet text-slate-600">The second column continues the story.</p></div>
</div>
</div>`},
		// From article-07: an oversized ghost-gray headline, two text
		// columns, and a centered italic byline.
		{Name: "Ghost headline article", Group: "Article", HTML: `<div class="cms-snippet not-prose my-6">
<h2 class="text-center text-5xl font-bold tracking-tight text-slate-200">Sunday, lovely Sunday.</h2>
<div class="mt-6 grid gap-6 sm:grid-cols-2">
<div><p class="cms-snippet text-slate-600">Open the article here: set the scene and tell readers why it matters.</p></div>
<div><p class="cms-snippet text-slate-600">The second column continues the story.</p></div>
</div>
<p class="mt-6 text-center text-slate-400"><em>By Jennifer Anderson</em></p>
</div>`},
		// From article-09: an italic serif title with the author under
		// it. Italics ride on <em> — an italic utility class would need
		// safelisting, the tag doesn't.
		{Name: "Serif title article", Group: "Article", HTML: `<div class="cms-snippet not-prose my-6">
<h2 class="text-center font-serif text-3xl"><em>Simplify things</em></h2>
<p class="mt-1 text-center text-slate-500">Natasha Williams</p>
<p class="mt-6 text-slate-600">Open the article here: set the scene and tell readers why it matters. Keep going &mdash; this block is a single flowing column.</p>
</div>`},
		// A map, dropped inline: the slot prompts for a Google Maps
		// link or an address when clicked while editing (after the
		// pack's element-map snippet, rebuilt on the slot mechanism).
		{Name: "Map", Group: "Media", HTML: `<div class="cms-snippet not-prose my-6">
` + mapSlotHTML + `
</div>`},
		// From quotes-27: portrait beside the quotation, the portrait a
		// circular photo slot.
		{Name: "Quote with portrait", Group: "Quotes", HTML: `<figure class="cms-snippet not-prose my-6 grid items-center gap-6 sm:grid-cols-3">
` + photoSlotCircle + `
<div class="sm:col-span-2">
<blockquote class="text-lg text-slate-700">&ldquo;A few sentences of praise from someone whose opinion matters to your visitors.&rdquo;</blockquote>
<p class="mt-3 text-sm font-semibold text-slate-500">&mdash; Lucas Fulmer</p>
</div>
</figure>`},
	}
}

// LibrarySectionPresets returns the imported section presets. Like
// DefaultSectionPresets, each carries Settings so the editor offers it
// in the "Add a section" chooser. All use not-prose with explicit slate
// colors, so they suit light backgrounds.
func LibrarySectionPresets() []Snippet {
	return []Snippet{
		// From header-07: an oversized statement, one supporting line,
		// and an outline button.
		{Name: "Big headline", Group: "Headlines", Settings: map[string]string{"width": "wide", "height": "50", "valign": "center"},
			HTML: `<div class="cms-snippet not-prose text-center">
<h1 class="text-5xl font-bold tracking-tight sm:text-7xl">Outstanding</h1>
<p class="mt-3 text-xl text-slate-600">One line that sets up the statement above.</p>
<p class="mt-6"><a href="/" class="cms-btn inline-block rounded-lg border-2 border-slate-900 px-8 py-3 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Contact us</a></p>
</div>`},
		// From header-02: one oversized, letter-spaced statement.
		{Name: "Statement headline", Group: "Headlines", Settings: map[string]string{"width": "wide", "height": "50", "valign": "center"},
			HTML: `<div class="cms-snippet not-prose text-center">
<h1 class="text-5xl font-bold uppercase tracking-widest sm:text-7xl">Stunning</h1>
</div>`},
		// From header-25: a small uppercase kicker over a big headline,
		// right-aligned, with a pill outline button.
		{Name: "Kicker headline", Group: "Headlines", Settings: map[string]string{"width": "wide", "height": "50", "valign": "center"},
			HTML: `<div class="cms-snippet not-prose text-right">
<p class="text-sm uppercase tracking-widest text-slate-500">Welcome to our coffee shop</p>
<h1 class="mt-2 text-5xl font-bold tracking-tight sm:text-7xl">Smell it, taste it.</h1>
<p class="mt-6"><a href="/" class="cms-btn inline-block rounded-full border-2 border-slate-900 px-8 py-3 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Browse menu</a></p>
</div>`},
		// From profile-01: heading plus three portrait cards, each
		// portrait a circular photo slot.
		//
		// The grid and the cards carry the card tool's markers, same as
		// the stock "Team" preset in snippets.go: this is the other way
		// to start a staff page, and the two should not disagree about
		// what happens when a fourth person joins. So it wraps rather
		// than squeezing a fourth portrait into the same row, and the
		// column tool leaves it alone — see teamBlockHTML for why.
		{Name: "Team profiles", Group: "Team", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<h2 class="text-center text-3xl font-bold uppercase tracking-widest">Meet our team</h2>
<div class="cms-team mt-10 grid grid-cols-1 gap-8 text-center sm:grid-cols-2 lg:grid-cols-3">
<div class="cms-team-card">
` + photoSlotCircle + `
<h3 class="mt-4 text-lg font-semibold uppercase tracking-widest">Vincent Nelson</h3>
<p class="mt-1 text-sm uppercase tracking-widest text-slate-400">Web designer</p>
</div>
<div class="cms-team-card">
` + photoSlotCircle + `
<h3 class="mt-4 text-lg font-semibold uppercase tracking-widest">Nathan Williams</h3>
<p class="mt-1 text-sm uppercase tracking-widest text-slate-400">Web developer</p>
</div>
<div class="cms-team-card">
` + photoSlotCircle + `
<h3 class="mt-4 text-lg font-semibold uppercase tracking-widest">Thomas Calvin</h3>
<p class="mt-1 text-sm uppercase tracking-widest text-slate-400">Account manager</p>
</div>
</div>
</div>`},
		// From photos-52: a three-up gallery of square photo slots.
		{Name: "Photo gallery", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-6 sm:grid-cols-3">
` + photoSlotSquare + `
` + photoSlotSquare + `
` + photoSlotSquare + `
</div>`},
		// From features-02: three features under oversized ghost numbers.
		{Name: "Numbered features", Group: "Features", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 sm:grid-cols-3">
<div><p class="text-6xl font-bold text-slate-200">01</p><h3 class="mt-3 text-lg font-semibold">Feature item</h3><p class="mt-1 text-slate-600">Explain the first thing you do well.</p></div>
<div><p class="text-6xl font-bold text-slate-200">02</p><h3 class="mt-3 text-lg font-semibold">Feature item</h3><p class="mt-1 text-slate-600">Explain the second thing you do well.</p></div>
<div><p class="text-6xl font-bold text-slate-200">03</p><h3 class="mt-3 text-lg font-semibold">Feature item</h3><p class="mt-1 text-slate-600">Explain the third thing you do well.</p></div>
</div>`},
		// From pricing-01: three plans with ghost numbers, a short rule
		// under each name, and an outline button.
		{Name: "Pricing plans", Group: "Pricing", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<h2 class="text-3xl font-bold uppercase tracking-widest">Choose your plan</h2>
<div class="mt-10 grid gap-8 sm:grid-cols-3">
<div>
<p class="text-6xl font-bold text-slate-200">01</p>
<h3 class="mt-2 text-2xl font-bold">Lite / $33</h3>
<div class="mt-2 h-0.5 w-10 bg-slate-900"></div>
<p class="mt-3 text-slate-600">One sentence on what this plan includes.</p>
<p class="mt-4"><a href="/" class="cms-btn inline-block rounded-lg border-2 border-slate-900 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Buy now</a></p>
</div>
<div>
<p class="text-6xl font-bold text-slate-200">02</p>
<h3 class="mt-2 text-2xl font-bold">Advanced / $59</h3>
<div class="mt-2 h-0.5 w-10 bg-slate-900"></div>
<p class="mt-3 text-slate-600">One sentence on what this plan includes.</p>
<p class="mt-4"><a href="/" class="cms-btn inline-block rounded-lg border-2 border-slate-900 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Buy now</a></p>
</div>
<div>
<p class="text-6xl font-bold text-slate-200">03</p>
<h3 class="mt-2 text-2xl font-bold">Ultimate / $77</h3>
<div class="mt-2 h-0.5 w-10 bg-slate-900"></div>
<p class="mt-3 text-slate-600">One sentence on what this plan includes.</p>
<p class="mt-4"><a href="/" class="cms-btn inline-block rounded-lg border-2 border-slate-900 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Buy now</a></p>
</div>
</div>
</div>`},
		// From achievements-01: three counters with a short rule under
		// each label.
		{Name: "Achievements", Group: "Stats", Settings: map[string]string{"bg": "light", "width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 text-center">
<h2 class="text-3xl font-bold uppercase tracking-widest">Achievements</h2>
<p class="mt-2 text-sm uppercase tracking-widest text-slate-500">Discover how good we are</p>
<div class="mt-10 grid gap-8 sm:grid-cols-3">
<div><p class="text-6xl font-bold">97</p><p class="mt-2 text-xl uppercase tracking-widest text-slate-600">Projects done</p><div class="mx-auto mt-3 h-0.5 w-10 bg-slate-900"></div></div>
<div><p class="text-6xl font-bold">200+</p><p class="mt-2 text-xl uppercase tracking-widest text-slate-600">Happy clients</p><div class="mx-auto mt-3 h-0.5 w-10 bg-slate-900"></div></div>
<div><p class="text-6xl font-bold">15</p><p class="mt-2 text-xl uppercase tracking-widest text-slate-600">Awards won</p><div class="mx-auto mt-3 h-0.5 w-10 bg-slate-900"></div></div>
</div>
</div>`},
		// From products-01: two products side by side — photo slot, name
		// with bold price, blurb, outline button.
		{Name: "Product pair", Group: "Products", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<h2 class="text-3xl font-bold uppercase tracking-widest">Our products</h2>
<div class="mt-10 grid gap-8 sm:grid-cols-2">
<div>
` + photoSlotWide + `
<h3 class="mt-4 text-lg font-semibold">Product one, <b>$109</b></h3>
<p class="mt-1 text-slate-600">One sentence on what makes it worth buying.</p>
<p class="mt-4"><a href="/" class="cms-btn inline-block rounded-lg border-2 border-slate-900 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Buy now</a></p>
</div>
<div>
` + photoSlotWide + `
<h3 class="mt-4 text-lg font-semibold">Product two, <b>$299</b></h3>
<p class="mt-1 text-slate-600">One sentence on what makes it worth buying.</p>
<p class="mt-4"><a href="/" class="cms-btn inline-block rounded-lg border-2 border-slate-900 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Buy now</a></p>
</div>
</div>
</div>`},
		// From products-02: three product cards. The originals' drop
		// shadows become the module's bordered-card look (Testimonials'
		// pattern), and the circular photos become circular photo slots.
		{Name: "Product cards", Group: "Products", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<h2 class="text-center text-3xl font-bold tracking-tight">Our products</h2>
<p class="mt-2 text-center text-slate-600">We make shopping way easier and more convenient for you.</p>
<div class="mt-10 grid gap-6 text-center sm:grid-cols-3">
<div class="rounded-xl border border-slate-200 bg-white p-6">
` + photoSlotCircle + `
<h3 class="mt-4 text-lg font-semibold">Product one</h3>
<p class="mt-1 text-slate-600">One sentence on what makes it worth buying.</p>
<p class="mt-4"><a href="/" class="cms-btn inline-block rounded-lg border-2 border-slate-900 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Buy now</a></p>
</div>
<div class="rounded-xl border border-slate-200 bg-white p-6">
` + photoSlotCircle + `
<h3 class="mt-4 text-lg font-semibold">Product two</h3>
<p class="mt-1 text-slate-600">One sentence on what makes it worth buying.</p>
<p class="mt-4"><a href="/" class="cms-btn inline-block rounded-lg border-2 border-slate-900 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Buy now</a></p>
</div>
<div class="rounded-xl border border-slate-200 bg-white p-6">
` + photoSlotCircle + `
<h3 class="mt-4 text-lg font-semibold">Product three</h3>
<p class="mt-1 text-slate-600">One sentence on what makes it worth buying.</p>
<p class="mt-4"><a href="/" class="cms-btn inline-block rounded-lg border-2 border-slate-900 px-6 py-2.5 text-sm font-semibold uppercase tracking-widest text-slate-900 hover:bg-slate-900 hover:text-white">Buy now</a></p>
</div>
</div>
</div>`},
		// From products-19: a two-by-two services grid. The Ionicons
		// checkmarks become plain text check characters, which the
		// sanitizer keeps and every font ships.
		{Name: "Services list", Group: "Products", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<h2 class="text-center text-3xl font-bold uppercase tracking-widest">Services we provide</h2>
<div class="mt-10 grid gap-8 sm:grid-cols-2">
<div class="flex gap-6"><p class="text-2xl font-bold text-blue-600">&#10003;</p><div><h3 class="text-lg font-semibold">Creative designs</h3><p class="mt-1 text-slate-600">A sentence about this service.</p></div></div>
<div class="flex gap-6"><p class="text-2xl font-bold text-blue-600">&#10003;</p><div><h3 class="text-lg font-semibold">Web development</h3><p class="mt-1 text-slate-600">A sentence about this service.</p></div></div>
<div class="flex gap-6"><p class="text-2xl font-bold text-blue-600">&#10003;</p><div><h3 class="text-lg font-semibold">Brand building</h3><p class="mt-1 text-slate-600">A sentence about this service.</p></div></div>
<div class="flex gap-6"><p class="text-2xl font-bold text-blue-600">&#10003;</p><div><h3 class="text-lg font-semibold">Friendly support</h3><p class="mt-1 text-slate-600">A sentence about this service.</p></div></div>
</div>
</div>`},
		// From steps-01: a serif kicker over the heading, then three
		// steps with ghost numbers and a short rule under each title.
		{Name: "Process steps", Group: "Process", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<p class="font-serif text-xl text-slate-400"><em>Discover</em></p>
<h2 class="mt-1 text-3xl font-bold uppercase tracking-widest">How it works</h2>
<div class="mt-10 grid gap-8 sm:grid-cols-3">
<div>
<p class="text-6xl font-bold text-slate-200">1.</p>
<h3 class="mt-2 text-2xl uppercase tracking-widest">Step 01</h3>
<div class="mt-2 h-0.5 w-10 bg-slate-900"></div>
<p class="mt-3 text-slate-600">Describe the first step of your process.</p>
</div>
<div>
<p class="text-6xl font-bold text-slate-200">2.</p>
<h3 class="mt-2 text-2xl uppercase tracking-widest">Step 02</h3>
<div class="mt-2 h-0.5 w-10 bg-slate-900"></div>
<p class="mt-3 text-slate-600">Describe the second step of your process.</p>
</div>
<div>
<p class="text-6xl font-bold text-slate-200">3.</p>
<h3 class="mt-2 text-2xl uppercase tracking-widest">Step 03</h3>
<div class="mt-2 h-0.5 w-10 bg-slate-900"></div>
<p class="mt-3 text-slate-600">Describe the third step of your process.</p>
</div>
</div>
</div>`},
		// From steps-03: steps alternating with photo slots, text left
		// of the first picture and right of the second.
		{Name: "Alternating steps", Group: "Process", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<h2 class="text-center text-3xl font-bold uppercase tracking-widest">The process</h2>
<div class="mt-10 grid items-center gap-8 sm:grid-cols-2">
<div>
<p class="text-5xl font-bold">01.</p>
<h3 class="mt-2 text-2xl uppercase tracking-widest">Step one</h3>
<p class="mt-3 text-slate-600">Describe the first step of your process.</p>
</div>
` + photoSlotWide + `
` + photoSlotWide + `
<div>
<p class="text-5xl font-bold">02.</p>
<h3 class="mt-2 text-2xl uppercase tracking-widest">Step two</h3>
<p class="mt-3 text-slate-600">Describe the second step of your process.</p>
</div>
</div>
</div>`},
		// From partners-03: heading, muted subtitle, and a row of logo
		// slots. The pack's other eleven partner blocks are the same
		// strip with different logo counts and alignments.
		{Name: "Partner logos", Group: "Partners", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 text-center">
<h2 class="text-3xl font-bold uppercase tracking-widest">Our clients</h2>
<p class="mt-2 text-slate-500">We are globally trusted by the world&rsquo;s best names.</p>
<div class="mt-10 grid gap-6 sm:grid-cols-4">
` + photoSlotLogo + `
` + photoSlotLogo + `
` + photoSlotLogo + `
` + photoSlotLogo + `
</div>
</div>`},
		// Map sections, mirroring the video trio: a full-width embed,
		// and both split layouts with an address block beside the map.
		{Name: "Full-width map", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
` + mapSlotHTML + `
</div>`},
		{Name: "Text + map", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 items-center sm:grid-cols-2">
<div><h2 class="text-2xl font-bold mb-2">Find us</h2><p class="text-slate-600">123 Main Street, Your Town. Drop by during business hours &mdash; we&rsquo;d love to see you.</p></div>
` + mapSlotHTML + `
</div>`},
		{Name: "Map + text", Group: "Media", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid gap-8 items-center sm:grid-cols-2">
` + mapSlotHTML + `
<div><h2 class="text-2xl font-bold mb-2">Find us</h2><p class="text-slate-600">123 Main Street, Your Town. Drop by during business hours &mdash; we&rsquo;d love to see you.</p></div>
</div>`},
		// From asfeaturedon-01: the label sits in the row as the first
		// cell, followed by press-logo slots. The pack's other featured
		// -on blocks are the Partner logos layout with different words.
		// The heading is a column like the logos beside it, so it sits
		// in a cell of its own rather than being one — otherwise Enter
		// while retitling it would add a fifth column to a four-track
		// row and wrap the logos. Being in a cell, it is also a block
		// there (see "Two columns" in snippets.go); the slots are not,
		// because a slot is the whole of its column and already has its
		// own chrome.
		{Name: "Featured on", Group: "Partners", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 grid items-center gap-6 sm:grid-cols-4">
<div><h2 class="cms-snippet text-2xl font-bold uppercase tracking-widest">As featured on</h2></div>
` + photoSlotLogo + `
` + photoSlotLogo + `
` + photoSlotLogo + `
</div>`},
		// From comingsoon-05/-01: the tall holding page — a huge tracked
		// headline over an uppercase line, social links as plain text
		// (the originals' Ionicons become words).
		{Name: "Coming soon", Group: "Coming soon", Settings: map[string]string{"width": "wide", "height": "75", "valign": "center"},
			HTML: `<div class="cms-snippet not-prose text-center">
<h1 class="text-5xl font-bold uppercase tracking-widest sm:text-7xl">Coming soon</h1>
<p class="mt-3 text-sm uppercase tracking-widest text-slate-500">Check back soon for the new and improved site</p>
<p class="mt-10 text-sm font-semibold uppercase tracking-widest"><a href="https://twitter.com/" class="text-blue-600">Twitter</a> &middot; <a href="https://www.facebook.com/" class="text-blue-600">Facebook</a> &middot; <a href="mailto:you@example.com" class="text-blue-600">Email</a></p>
</div>`},
		// From comingsoon-04: the maintenance notice with a ghost
		// progress percentage.
		{Name: "Maintenance mode", Group: "Coming soon", Settings: map[string]string{"width": "wide", "height": "75", "valign": "center"},
			HTML: `<div class="cms-snippet not-prose text-center">
<h2 class="text-3xl font-bold uppercase tracking-widest">Maintenance mode</h2>
<p class="mt-2 text-xl text-slate-600">Our website is under maintenance. Please come back later.</p>
<p class="mt-10 text-6xl font-bold text-slate-200">90%</p>
<p class="mt-1 text-sm uppercase tracking-widest text-slate-500">Completed</p>
</div>`},
		// From skills-01: three big percentages with skill names. The
		// category has no real progress bars — it's all percentage
		// displays, so nothing here needs width utilities.
		{Name: "Skill percentages", Group: "Skills", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<p class="text-sm uppercase tracking-widest text-slate-500">Discover how good we are</p>
<h2 class="mt-1 text-3xl font-bold uppercase tracking-widest">Team skills</h2>
<div class="mt-10 grid gap-8 sm:grid-cols-3">
<div><p class="text-6xl font-bold">85%</p><h3 class="mt-2 text-lg font-semibold uppercase tracking-widest">Web design</h3><p class="mt-1 text-slate-600">A sentence about this skill.</p></div>
<div><p class="text-6xl font-bold">98%</p><h3 class="mt-2 text-lg font-semibold uppercase tracking-widest">Web development</h3><p class="mt-1 text-slate-600">A sentence about this skill.</p></div>
<div><p class="text-6xl font-bold">77%</p><h3 class="mt-2 text-lg font-semibold uppercase tracking-widest">Photoshop</h3><p class="mt-1 text-slate-600">A sentence about this skill.</p></div>
</div>
</div>`},
		// From skills-02: percentages in filled circles. The originals'
		// green/pink/blue circles harmonize to the module's single
		// accent blue.
		{Name: "Skill circles", Group: "Skills", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8 text-center">
<h2 class="text-3xl font-bold uppercase tracking-widest">Professional skills</h2>
<div class="mt-10 grid gap-8 sm:grid-cols-3">
<div>
<div class="mx-auto flex size-24 items-center justify-center rounded-full bg-blue-600 text-2xl font-bold text-white">87%</div>
<h3 class="mt-4 text-lg font-semibold uppercase tracking-widest">Web design</h3>
<p class="mt-1 text-slate-600">A sentence about this skill.</p>
</div>
<div>
<div class="mx-auto flex size-24 items-center justify-center rounded-full bg-blue-600 text-2xl font-bold text-white">92%</div>
<h3 class="mt-4 text-lg font-semibold uppercase tracking-widest">Web development</h3>
<p class="mt-1 text-slate-600">A sentence about this skill.</p>
</div>
<div>
<div class="mx-auto flex size-24 items-center justify-center rounded-full bg-blue-600 text-2xl font-bold text-white">99%</div>
<h3 class="mt-4 text-lg font-semibold uppercase tracking-widest">Customer support</h3>
<p class="mt-1 text-slate-600">A sentence about this skill.</p>
</div>
</div>
</div>`},
		// From skills-03: percentages in outlined rings, two per row.
		{Name: "Skill rings", Group: "Skills", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<h2 class="text-3xl font-bold uppercase tracking-widest">Work skills</h2>
<div class="mt-10 grid gap-8 sm:grid-cols-2">
<div>
<div class="flex size-24 items-center justify-center rounded-full border-2 border-slate-300 text-2xl font-bold text-slate-500">93%</div>
<h3 class="mt-4 text-2xl font-bold">Design / Graphics</h3>
<p class="mt-1 text-slate-600">A sentence about this skill.</p>
</div>
<div>
<div class="flex size-24 items-center justify-center rounded-full border-2 border-slate-300 text-2xl font-bold text-slate-500">85%</div>
<h3 class="mt-4 text-2xl font-bold">HTML &amp; CSS</h3>
<p class="mt-1 text-slate-600">A sentence about this skill.</p>
</div>
<div>
<div class="flex size-24 items-center justify-center rounded-full border-2 border-slate-300 text-2xl font-bold text-slate-500">77%</div>
<h3 class="mt-4 text-2xl font-bold">WordPress</h3>
<p class="mt-1 text-slate-600">A sentence about this skill.</p>
</div>
<div>
<div class="flex size-24 items-center justify-center rounded-full border-2 border-slate-300 text-2xl font-bold text-slate-500">89%</div>
<h3 class="mt-4 text-2xl font-bold">Customer support</h3>
<p class="mt-1 text-slate-600">A sentence about this skill.</p>
</div>
</div>
</div>`},
		// From quotes-24: two client quotes under circular photo slots,
		// each with a short rule between name and text.
		{Name: "Client quotes", Group: "Quotes", Settings: map[string]string{"width": "wide"},
			HTML: `<div class="cms-snippet not-prose my-8">
<h2 class="text-center text-3xl font-bold uppercase tracking-widest">Happy clients</h2>
<div class="mt-10 grid gap-8 text-center sm:grid-cols-2">
<div>
` + photoSlotCircle + `
<p class="mt-4 text-xl uppercase tracking-widest">Mary Pals</p>
<div class="mx-auto mt-2 h-0.5 w-10 bg-slate-900"></div>
<p class="mt-3 text-slate-500">A sentence or two about working with you, in the client&rsquo;s own words.</p>
</div>
<div>
` + photoSlotCircle + `
<p class="mt-4 text-xl uppercase tracking-widest">Wilma Finn</p>
<div class="mx-auto mt-2 h-0.5 w-10 bg-slate-900"></div>
<p class="mt-3 text-slate-500">A sentence or two about working with you, in the client&rsquo;s own words.</p>
</div>
</div>
</div>`},
	}
}
