/* ------------------------------------------------------------------ *
 * Column count — the "Columns" control in the block gear.
 *
 * Two different things are called columns, and the control covers both
 * because an editor means one word by them:
 *
 *   - a GRID block ("Two columns", "Feature grid") is a row of separate
 *     cells, each holding its own content, and the count is the number
 *     of tracks (sm:grid-cols-3);
 *   - a FLOW block ("Article text") is one continuous body of prose
 *     split into newspaper columns, read down one and up into the next,
 *     and the count is column-count (columns-2).
 *
 * Which one a block is, it says itself: a grid carries grid-cols-N, a
 * flow carries columns-N. A block carrying neither is not a column
 * layout and gets no control. Changing either by hand means opening the
 * block's HTML, which is exactly what an editor should not have to do;
 * this offers the same edit as a number.
 *
 * Two things are deliberately kept apart here, because in real snippet
 * markup they come apart:
 *
 *   - the number of grid *tracks*, which is the class, and
 *   - the number of *children*, which is the content.
 *
 * They usually match. They don't in "Quote with portrait" — three tracks
 * holding two children, the second spanning two columns — so this only
 * adds or removes a child when the block currently has one child per
 * track. Anywhere else the class alone changes, which is the honest
 * reading of "make it three columns" for a grid whose cells were never
 * one-per-column.
 *
 * A flow block has no such split: its content is one stream and the
 * count is purely presentational, so changing it is a class edit and
 * nothing else. Nothing can be lost, which is why only the grid path
 * ever asks before applying.
 * ------------------------------------------------------------------ */

import { cmsConfirm } from "./dialogs.js";

// The two class vocabularies, each as a matcher and the stem a rewrite
// puts back. Both capture the responsive prefix, if any — "grid-cols-2",
// "sm:grid-cols-3", "lg:columns-2" — so the new count lands at the same
// breakpoint the old one was written for.
//
// GRID.offered stops at four because every class the control can write
// is then one a default snippet already uses, which is what keeps the
// Tailwind safelists (and the generated content stylesheet) covering
// them. FLOW starts at one because a single column is a real answer for
// running text — it is the shape "Article text" ships in.
var GRID = {
    re: /(?:^|\s)((?:[a-z0-9]+:)*)grid-cols-(\d+)(?=\s|$)/g,
    stem: "grid-cols-",
    marker: "grid-cols-",
    offered: [2, 3, 4],
};
var FLOW = {
    re: /(?:^|\s)((?:[a-z0-9]+:)*)columns-(\d+)(?=\s|$)/g,
    stem: "columns-",
    marker: "columns-",
    offered: [1, 2, 3],
};

// countsOn reads every column class of one kind off an element.
function countsOn(el, kind) {
    var out = [];
    var cls = el.getAttribute ? el.getAttribute("class") || "" : "";
    var m;
    kind.re.lastIndex = 0;
    while ((m = kind.re.exec(cls)) !== null) {
        out.push({ prefix: m[1], count: parseInt(m[2], 10) });
        // The lookahead for the trailing space is zero-width, so a class
        // list of back-to-back matches would skip every other one
        // without this.
        kind.re.lastIndex = m.index + m[0].length - 1;
    }
    return out;
}

// widest returns the class that decides how many columns the block
// *has*: the largest count. Tailwind's common idiom stacks a mobile
// default under a breakpoint — "grid-cols-1 sm:grid-cols-2" is one
// column on a phone and two everywhere else — and it is the two that an
// editor means. Rewriting the largest leaves the mobile stack alone.
function widest(el, kind) {
    var all = countsOn(el, kind);
    if (!all.length) return null;
    return all.reduce(function (a, b) { return b.count > a.count ? b : a; });
}

// carrierOf finds the element holding a column count of the given kind:
// the block root when the root carries it ("Two columns", "Article
// text"), otherwise the one descendant that does — most of the imported
// library puts a heading first and the grid under it. A block holding
// several such elements has no single answer, so it gets no control
// rather than a guess.
function carrierOf(block, kind) {
    if (!block) return null;
    if (widest(block, kind)) return block;
    var inner = block.querySelectorAll('[class*="' + kind.marker + '"]');
    return inner.length === 1 && widest(inner[0], kind) ? inner[0] : null;
}

// FLOW_HOSTILE lists what stops a block being ordinary running text.
// Media and buttons are placed things, not prose: splitting them over
// columns is never what "two columns" meant, and a nested block is
// another block's business. Links inside a sentence are fine, which is
// why only a.cms-btn is named rather than every anchor.
var FLOW_HOSTILE = "img, video, iframe, a.cms-btn, .cms-snippet," +
    "[data-cms-video-slot], [data-cms-photo-slot], [data-cms-map-slot], [data-cms-image]";

// isProse reports whether a block is plain running text that could be
// flowed into columns although it carries no column class yet — the
// stock "Text" block being the one everybody reaches for first.
//
// It has to hold words, so an empty block does not sprout a control over
// nothing, and it has to hold nothing that is placed rather than
// written.
function isProse(block) {
    if (!block || !(block.textContent || "").trim()) return false;
    return !block.querySelector(FLOW_HOSTILE);
}

// layoutOf reports which kind of column layout a block is, and the
// element carrying the count. Grid is tested first: "columns-" is a
// substring of nothing in the grid vocabulary, but testing in a fixed
// order keeps a block that somehow carried both from being ambiguous.
//
// A block with neither class can still be a flow *candidate*: prose that
// would become one the moment a count above one is chosen. It reports a
// count of one, which is what it renders as, so the control opens saying
// the truth about the block rather than proposing a change.
function layoutOf(block) {
    var grid = carrierOf(block, GRID);
    if (grid) return { kind: GRID, el: grid, grid: true };
    var flow = carrierOf(block, FLOW);
    if (flow) return { kind: FLOW, el: flow, grid: false };
    if (isProse(block)) return { kind: FLOW, el: block, grid: false, candidate: true };
    return null;
}

// gridOf is layoutOf narrowed to grids, for the cell bookkeeping that
// only ever applies to them.
export function gridOf(block) {
    var it = layoutOf(block);
    return it && it.grid ? it.el : null;
}

// elementChildren is grid.children as a real array — the cells, ignoring
// the whitespace text nodes between them.
function elementChildren(grid) {
    return Array.prototype.slice.call(grid.children);
}

// tracksAndCells reports the current column count and whether the block
// is the simple one-child-per-column shape that may gain and lose cells.
function tracksAndCells(grid) {
    var cur = widest(grid, GRID);
    var kids = elementChildren(grid);
    return { count: cur.count, kids: kids, inStep: kids.length === cur.count };
}

// columnsField is the gear's "Columns" entry, or null for a block that
// is not a column layout at all. The current count is always among the
// options even when it is outside the offered range, so the select never
// misreports what the block is.
export function columnsField(block) {
    var it = layoutOf(block);
    if (!it) return null;
    var count = it.candidate ? 1 : widest(it.el, it.kind).count;
    var counts = it.kind.offered.slice();
    if (counts.indexOf(count) === -1) {
        counts.push(count);
        counts.sort(function (a, b) { return a - b; });
    }
    return {
        id: "columns",
        label: "Columns",
        type: "select",
        value: String(count),
        options: counts.map(function (n) {
            return { value: String(n), label: n === 1 ? "1 (one flowing column)" : String(n) };
        }),
    };
}

// droppedCells lists the cells a change to n columns would delete —
// empty when nothing is lost, which is every case except shrinking a
// grid whose cells are one per column.
function droppedCells(block, n) {
    var grid = gridOf(block);
    if (!grid) return [];
    var t = tracksAndCells(grid);
    if (!t.inStep || !(n < t.count)) return [];
    return t.kids.slice(n);
}

// confirmColumns asks before a change that would delete a cell holding
// something — written text, or a picture. Resolves true to go ahead.
// Losing an untouched placeholder cell is not worth a question, and
// neither is adding columns.
export function confirmColumns(block, value) {
    var n = parseInt(value, 10);
    if (!n) return Promise.resolve(true);
    var lost = droppedCells(block, n).filter(function (cell) {
        return (cell.textContent || "").trim() !== "" ||
            cell.querySelector("img, video, iframe");
    });
    if (!lost.length) return Promise.resolve(true);
    return cmsConfirm(lost.length === 1
        ? "Removing a column deletes its content. Continue?"
        : "Removing " + lost.length + " columns deletes their content. Continue?",
    "Remove", true);
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
    blankText(clone);
    return clone;
}

// setCount rewrites the one class the count was read from, keeping its
// breakpoint prefix; any narrower one is a mobile stack and stays.
function setCount(el, kind, n) {
    var cur = widest(el, kind);
    var target = cur.prefix + kind.stem + cur.count;
    el.className = el.className.split(/\s+/).map(function (c) {
        return c === target ? cur.prefix + kind.stem + n : c;
    }).join(" ");
}

// makeFlow turns a prose block that carries no column class into one
// that does, and returns the element that is now the block.
//
// A block that can hold paragraphs only needs the classes. A bare <p>
// cannot — paragraphs do not nest — so it is moved inside a new <div>
// that takes over as the block. That is worth doing for its own sake:
// pressing Enter in a bare paragraph splits it into two sibling blocks,
// while inside the wrapper the paragraphs stay together and go on
// reading as one piece of text, which is the whole point of flowing
// them.
function makeFlow(block, n) {
    var classes = FLOW.stem + n + " gap-8";
    if (block.tagName !== "P") {
        block.className = (block.className + " " + classes).trim();
        return block;
    }
    var wrap = document.createElement("div");
    wrap.className = (block.className + " " + classes).trim();
    block.parentNode.insertBefore(wrap, block);
    // The paragraph keeps its own identity inside the wrapper, but not
    // the marker that made it a block: there is one block here, and it
    // is now the wrapper.
    block.classList.remove("cms-snippet");
    if (!block.getAttribute("class")) block.removeAttribute("class");
    wrap.appendChild(block);
    return wrap;
}

// applyColumns sets the block to n columns and returns the element that
// is the block afterwards — the same one in every case but a bare
// paragraph becoming a flow, which grows a wrapper (see makeFlow).
//
// For a flow block the count is the whole change: the content is one
// stream, and how many columns it runs in is presentation. For a grid it
// is the class always, and the cells too when the block is
// one-child-per-column. Call it inside the owning editor's undo
// transaction, like the rest of the gear.
export function applyColumns(block, value) {
    var n = parseInt(value, 10);
    var it = layoutOf(block);
    if (!it || !n) return block;

    // Prose with no column class of its own renders as one column, so
    // choosing one asks for nothing; anything more converts it.
    if (it.candidate) {
        return n > 1 ? makeFlow(block, n) : block;
    }

    var cur = widest(it.el, it.kind).count;
    if (cur === n) return block;

    if (!it.grid) {
        setCount(it.el, it.kind, n);
        return block;
    }

    var grid = it.el;
    var t = tracksAndCells(grid);
    setCount(grid, GRID, n);

    if (!t.inStep) return block;
    if (n > t.count) {
        // Clone the last cell rather than the first: in a grid that
        // leads with a heading-ish cell, the last one is the ordinary
        // repeating unit.
        for (var i = t.count; i < n; i++) {
            grid.appendChild(cleanClone(grid.lastElementChild));
        }
        return block;
    }
    t.kids.slice(n).forEach(function (cell) { cell.remove(); });
    return block;
}
