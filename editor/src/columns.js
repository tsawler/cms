/* ------------------------------------------------------------------ *
 * Columns — a row of cells, and the edits that reshape it.
 *
 * A column here is a real box with its own content, never a slice of
 * one continuous stream. That is the whole design, and it is worth
 * saying plainly because CSS offers the other thing (`column-count`,
 * which reflows text down one column and up into the next) and it is
 * almost never what someone means by "put this in two columns". It also
 * misbehaves in ways nobody asks for: the first column carries the
 * leading paragraph's top margin while the continuation in the second
 * does not, so the two start at visibly different heights.
 *
 * So: a row is a CSS grid, a cell is one of its children, and the edits
 * are the ones an editor would name — add a column here, drop this one,
 * make it wider, move it along. The model is borrowed from InnovaStudio
 * ContentBuilder (licensed with source), whose column tool is the same
 * five verbs against a flex row of cells; what is different is the width
 * vocabulary, which is Tailwind track spans rather than inline widths,
 * so a resized column stays responsive and needs no inline style the
 * server's sanitizer would have to allow.
 *
 * Two forms of row markup exist, and both are load-bearing:
 *
 *   - the EVEN form, `sm:grid-cols-K` with K cells carrying no span of
 *     their own. This is what every stock snippet ships and what a row
 *     goes back to whenever the cells are equal, because it is the form
 *     a person reading the HTML would have written;
 *   - the SPANNED form, `sm:grid-cols-12` with a `sm:col-span-N` on each
 *     cell summing to twelve. A row takes this on the moment a column is
 *     resized, which is the only edit that makes cells differ.
 *
 * Normalizing between them is exact, not approximate: twelve divides by
 * every track count the even form uses, so `sm:grid-cols-3` becomes
 * twelve tracks of span 4 with nothing rounded and nothing moved. A row
 * whose track count does not divide twelve (five, seven) can still gain
 * and lose columns; it just cannot be resized, and says so by disabling
 * the control rather than by rounding.
 *
 * The responsive prefix travels with the count. Stock markup writes its
 * tracks at `sm:` — one column on a phone, K above it — and every rewrite
 * here puts the new value back at whatever prefix it read the old one
 * from, so the mobile stack underneath is never touched. A host whose
 * snippets use a different breakpoint gets the same treatment and needs
 * that breakpoint's classes in its own safelist; see
 * render.EditorAppliedClasses for the ones this module ships.
 * ------------------------------------------------------------------ */

import { cmsConfirm } from "./dialogs.js";

// The grid Tailwind's span vocabulary is built on. Not configurable:
// col-span-N only means anything against a twelve-track row.
var TRACKS = 12;

// Above four the even form would need track counts no stock snippet
// carries, so five and six columns are written in the spanned form
// instead — which needs no new classes beyond the twelve spans already
// safelisted. Six is the ceiling because a seventh column cannot be
// given a span of two without the row overflowing.
var EVEN_MAX = 4;
var MAX_COLS = 6;

// Both class vocabularies as matchers. The capture keeps the responsive
// prefix — "grid-cols-2", "sm:grid-cols-3", "lg:col-span-4" — so a
// rewrite lands at the breakpoint the original was written for.
var GRID_RE = /(?:^|\s)((?:[a-z0-9]+:)*)grid-cols-(\d+)(?=\s|$)/g;
var SPAN_RE = /(?:^|\s)((?:[a-z0-9]+:)*)col-span-(\d+)(?=\s|$)/g;
var SPAN_ONE = /^(?:[a-z0-9]+:)*col-span-\d+$/;

// readAll pulls every class of one kind off an element, as
// {prefix, count} pairs in the order they appear.
function readAll(el, re) {
    var out = [];
    var cls = (el && el.getAttribute) ? el.getAttribute("class") || "" : "";
    var m;
    re.lastIndex = 0;
    while ((m = re.exec(cls)) !== null) {
        out.push({ prefix: m[1], count: parseInt(m[2], 10) });
        // The lookahead for the trailing space is zero-width, so
        // back-to-back matches would skip every other one without this.
        re.lastIndex = m.index + m[0].length - 1;
    }
    return out;
}

// widest returns the class that decides the value in play: the largest.
// Tailwind's common idiom stacks a mobile default under a breakpoint —
// "grid-cols-1 sm:grid-cols-2" is one column on a phone and two
// everywhere else — and it is the two that an editor means.
function widest(el, re) {
    var all = readAll(el, re);
    if (!all.length) return null;
    return all.reduce(function (a, b) { return b.count > a.count ? b : a; });
}

// setTracks rewrites the one grid-cols class the count was read from,
// keeping its breakpoint prefix; any narrower one is a mobile stack and
// stays exactly as it was.
function setTracks(el, n) {
    var cur = widest(el, GRID_RE);
    if (!cur) return;
    var target = cur.prefix + "grid-cols-" + cur.count;
    var next = cur.prefix + "grid-cols-" + n;
    el.className = el.className.split(/\s+/).map(function (c) {
        return c === target ? next : c;
    }).join(" ");
}

// setSpan gives a cell exactly one span class, at the row's prefix.
// Passing 0 leaves it with none, which is the even form.
function setSpan(cell, prefix, n) {
    var kept = (cell.getAttribute("class") || "").split(/\s+/).filter(function (c) {
        return c !== "" && !SPAN_ONE.test(c);
    });
    if (n) kept.push(prefix + "col-span-" + n);
    var cls = kept.join(" ");
    if (cls) cell.setAttribute("class", cls);
    else cell.removeAttribute("class");
}

// evenSpans splits twelve tracks over k cells as evenly as twelve
// allows: five columns come out 3-3-2-2-2 rather than refusing to exist.
function evenSpans(k) {
    var base = Math.floor(TRACKS / k);
    var rem = TRACKS % k;
    var out = [];
    for (var i = 0; i < k; i++) out.push(base + (i < rem ? 1 : 0));
    return out;
}

// cellsOf lists a row's real cells. TinyMCE parks bogus <br>s inside
// containers it is about to put a caret in, and one of those is not a
// column.
function cellsOf(row) {
    return Array.prototype.slice.call(row.children).filter(function (el) {
        return el.tagName !== "BR" && !el.hasAttribute("data-mce-bogus");
    });
}

/* ---- finding the row -------------------------------------------- */

// rowIn finds the element holding a track count: the block root when the
// root carries it ("Two columns", "Quote with portrait"), otherwise the
// one descendant that does — much of the imported library puts a heading
// first and the grid under it. A block holding several such elements has
// no single answer, so it gets no control rather than a guess.
function rowIn(block) {
    if (!block) return null;
    if (widest(block, GRID_RE)) return block;
    var inner = block.querySelectorAll('[class*="grid-cols-"]');
    return inner.length === 1 && widest(inner[0], GRID_RE) ? inner[0] : null;
}

// SPLIT_HOSTILE lists what stops a block being ordinary running text
// that could be split into columns. Media and buttons are placed things:
// dividing a block built around one is a judgement about its design, not
// a column edit, and a nested block is another block's business. Links
// inside a sentence are fine, which is why only a.cms-btn is named
// rather than every anchor.
var SPLIT_HOSTILE = "img, video, iframe, a.cms-btn, .cms-snippet," +
    "[data-cms-video-slot], [data-cms-photo-slot], [data-cms-map-slot], [data-cms-image]";

// canSplit reports whether a block is plain running text that could
// become a two-column row — the stock "Text" block being the one
// everybody reaches for first. It has to hold words, so an empty block
// does not sprout a control over nothing, and nothing that is placed
// rather than written.
function canSplit(block) {
    if (!block || !(block.textContent || "").trim()) return false;
    return !block.querySelector(SPLIT_HOSTILE);
}

// columnTarget reports what the column tool should offer for a click at
// `target` inside `block`, or null for a block that is neither a row nor
// splittable.
//
//   { mode: "cell",  … }  the click landed in one cell of a row
//   { mode: "split", … }  the block is prose and could become a row
//
// In cell mode the flags say which edits are available, so the tool can
// hide the buttons that would do nothing rather than offer them and
// refuse: a lone cell cannot move or be narrowed, a full row cannot
// grow, and a row whose track count does not divide twelve cannot be
// resized at all.
export function columnTarget(block, target) {
    var row = rowIn(block);
    if (!row) {
        return canSplit(block) ? { mode: "split", block: block } : null;
    }
    var cells = cellsOf(row);
    if (!cells.length) return null;
    // The click may be deep inside a cell, or on the row's own padding
    // between cells; the latter has no column to act on.
    var cell = null;
    for (var i = 0; i < cells.length; i++) {
        if (cells[i] === target || cells[i].contains(target)) { cell = cells[i]; break; }
    }
    if (!cell) return null;
    var at = cells.indexOf(cell);
    var tracks = widest(row, GRID_RE).count;
    return {
        mode: "cell",
        row: row,
        cell: cell,
        index: at,
        count: cells.length,
        canAdd: cells.length < MAX_COLS,
        // Resizing needs the spanned form, and reaching it exactly needs
        // a track count twelve divides by.
        canResize: cells.length > 1 && TRACKS % tracks === 0,
        canMoveBack: at > 0,
        canMoveOn: at < cells.length - 1,
    };
}

/* ---- rewriting the row ------------------------------------------ */

// spanned puts a row into the twelve-track form, preserving the relative
// widths it already had: three tracks of span 1 and 2 become twelve of
// span 4 and 8, which is the same layout written in the vocabulary a
// resize can step through. Returns the cells' spans, or null when the
// row's track count does not divide twelve.
function spanned(row) {
    var cur = widest(row, GRID_RE);
    if (!cur) return null;
    var factor = TRACKS / cur.count;
    if (factor !== Math.floor(factor)) return null;
    var cells = cellsOf(row);
    var spans = cells.map(function (c) {
        var s = widest(c, SPAN_RE);
        return (s ? s.count : 1) * factor;
    });
    var total = spans.reduce(function (a, b) { return a + b; }, 0);
    // A row whose spans do not add up to its tracks is one the markup
    // never meant as a simple row of columns — a deliberately short row,
    // or a wrapping one. Making the widths even is the only honest thing
    // left, and it is what the next add or remove would do anyway.
    if (total !== TRACKS) spans = evenSpans(cells.length);
    setTracks(row, TRACKS);
    cells.forEach(function (c, i) { setSpan(c, cur.prefix, spans[i]); });
    return spans;
}

// fixLayout re-stamps the widths after the cell count changes, so they
// always add up to a full row — the one piece of ContentBuilder's column
// tool that is pure bookkeeping, and the reason its rows can never drift
// out of alignment.
//
// Widths go back to even, which is deliberate: after adding a fourth
// column to a row someone had set to 8-4, no redistribution of the old
// widths is the obvious one, and even columns are both predictable and
// one click from being reshaped again.
//
// Four or fewer even columns are written in the even form, spans
// stripped, because that is the markup a person would have written and
// the markup every stock snippet already carries.
function fixLayout(row) {
    var cur = widest(row, GRID_RE);
    if (!cur) return;
    var cells = cellsOf(row);
    if (!cells.length) return;
    if (cells.length <= EVEN_MAX) {
        setTracks(row, cells.length);
        cells.forEach(function (c) { setSpan(c, cur.prefix, 0); });
        return;
    }
    setTracks(row, TRACKS);
    var spans = evenSpans(cells.length);
    cells.forEach(function (c, i) { setSpan(c, cur.prefix, spans[i]); });
}

// blankText replaces the words in a cloned cell with a placeholder, so a
// new column reads as something to fill in rather than as a duplicate of
// the column it was copied from. The cell keeps its structure and
// classes — which is the point of cloning rather than inserting a
// generic div: a copied photo slot is still a working photo slot, and a
// copied heading is still styled like its neighbours.
function blankText(cell) {
    var walker = document.createTreeWalker(cell, NodeFilter.SHOW_TEXT, null);
    var nodes = [];
    var n;
    while ((n = walker.nextNode())) {
        if ((n.nodeValue || "").trim() !== "") nodes.push(n);
    }
    nodes.forEach(function (node) {
        var host = node.parentElement;
        var heading = host && /^h[1-6]$/i.test(host.tagName);
        node.nodeValue = heading ? "Heading" : "Write something here.";
    });
}

// cleanClone copies a cell for use as a new column: TinyMCE's shadow
// attributes go (they describe the original's state, and a stale
// data-mce-style would be written back over the copy's own on
// serialization), and the words are replaced.
function cleanClone(cell) {
    var clone = cell.cloneNode(true);
    var all = [clone];
    clone.querySelectorAll("*").forEach(function (el) { all.push(el); });
    all.forEach(function (el) {
        for (var i = el.attributes.length - 1; i >= 0; i--) {
            if (el.attributes[i].name.indexOf("data-mce-") === 0) {
                el.removeAttribute(el.attributes[i].name);
            }
        }
    });
    // The mark naming the column the tool has hold of belongs to the
    // original, not to a copy of it.
    clone.classList.remove("cms-col-active");
    blankText(clone);
    return clone;
}

/* ---- the edits --------------------------------------------------- */

// addColumn puts a fresh column immediately after the one acted on — a
// blanked copy of it, so it arrives with that row's own styling — and
// re-evens the widths. Returns the new cell.
export function addColumn(info) {
    var clone = cleanClone(info.cell);
    info.row.insertBefore(clone, info.cell.nextSibling);
    fixLayout(info.row);
    return clone;
}

// confirmRemove asks before deleting a column holding something someone
// wrote or placed. An untouched placeholder column is not worth a
// question. Resolves true to go ahead.
export function confirmRemove(info) {
    var cell = info.cell;
    var used = (cell.textContent || "").trim() !== "" ||
        cell.querySelector("img, video, iframe");
    if (!used) return Promise.resolve(true);
    return cmsConfirm(info.count === 1
        ? "Deleting the last column deletes this block. Continue?"
        : "Removing this column deletes its content. Continue?",
    "Remove", true);
}

// removeColumn drops a column. Taking the last one out of a row leaves
// nothing to hold, so the row goes too — and where the row *is* the
// block, so does the block. Returns the element still standing, or null
// when the block itself was removed, so the caller knows what to
// re-anchor its chrome around.
export function removeColumn(info, block) {
    info.cell.remove();
    if (cellsOf(info.row).length) {
        fixLayout(info.row);
        return block;
    }
    if (info.row === block) {
        block.remove();
        return null;
    }
    info.row.remove();
    return block;
}

// resizeColumn widens or narrows a column by one track, taking the width
// from (or giving it to) the neighbour on the side there is one — the
// next column normally, the previous for the last column in the row.
// Both stay at a track or more, so a column can never be squeezed out of
// existence by resizing; use Remove for that.
//
// Stepping a *pair* rather than one column is what keeps the row full:
// the total is invariant, so no combination of resizes can leave a gap
// or an overflow.
export function resizeColumn(info, delta) {
    var spans = spanned(info.row);
    if (!spans) return;
    var cells = cellsOf(info.row);
    var at = cells.indexOf(info.cell);
    var other = at < cells.length - 1 ? at + 1 : at - 1;
    if (other < 0) return;
    var mine = spans[at] + delta;
    var theirs = spans[other] - delta;
    if (mine < 1 || theirs < 1) return;
    var prefix = widest(info.row, GRID_RE).prefix;
    setSpan(cells[at], prefix, mine);
    setSpan(cells[other], prefix, theirs);
}

// moveColumn swaps a column with its neighbour. Widths travel with the
// cells rather than staying with the positions, which is what "move this
// column" means: a narrow sidebar moved to the other end is still narrow.
export function moveColumn(info, dir) {
    var cells = cellsOf(info.row);
    var at = cells.indexOf(info.cell);
    var to = at + dir;
    if (to < 0 || to >= cells.length) return;
    if (dir < 0) info.row.insertBefore(info.cell, cells[to]);
    else info.row.insertBefore(cells[to], info.cell);
}

// splitIntoColumns turns a prose block into a two-column row: what was
// there goes in the first column, a placeholder in the second. Returns
// the element that is the block afterwards.
//
// A block that can hold divs keeps its identity and gains the grid
// classes. A bare <p> cannot — paragraphs do not nest — so it moves
// inside a new <div> that takes over as the block. That is worth doing
// for its own sake: pressing Enter in a bare paragraph splits it into
// two sibling blocks, while inside a column the paragraphs stay together
// and go on reading as one piece of text.
export function splitIntoColumns(block) {
    var row = block;
    if (block.tagName === "P") {
        row = document.createElement("div");
        row.className = block.className;
        block.parentNode.insertBefore(row, block);
        // The paragraph keeps its own identity inside the row, but not
        // the marker that made it a block: there is one block here, and
        // it is now the row.
        block.classList.remove("cms-snippet");
        if (!block.getAttribute("class")) block.removeAttribute("class");
    }
    var first = document.createElement("div");
    while (row.firstChild) first.appendChild(row.firstChild);
    if (row !== block) first.appendChild(block);
    row.appendChild(first);

    var second = document.createElement("div");
    var p = document.createElement("p");
    p.textContent = "Write something here.";
    second.appendChild(p);
    row.appendChild(second);

    row.className = (row.className + " grid gap-6 sm:grid-cols-2").trim();
    return row;
}
