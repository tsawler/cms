/* ------------------------------------------------------------------ *
 * Column resize handles — dragging a boundary between two columns.
 *
 * This is the same edit the column tool's narrower/wider buttons make,
 * reached the way people expect to reach it. The buttons stay: they are
 * the keyboard and touch path, and they say in words what the handle
 * says by being where it is. What the handle adds is directness, and
 * one thing the buttons cannot express at all — *which* boundary moves.
 * resizeColumn has to pick a neighbour by rule (the next column, or the
 * previous one for the last in the row), which is a rule you have to
 * learn; grabbing a boundary names it.
 *
 * Snapping is not a nicety here, it is the whole design. A column's
 * width is a Tailwind track span, so the only widths that exist are the
 * twelve, and a drag that produced anything else would have to be
 * rounded on release — which is the interaction where you let go and
 * watch the layout jump. Snapping while dragging means what you see
 * mid-gesture is what you get, and the row is exactly full at every
 * frame because the two spans either side of the boundary always sum to
 * what they summed to before.
 *
 * The handles are fixed-position overlays in the editor's shadow root,
 * like every other piece of chrome. Nothing is injected into the page,
 * so there is nothing here for the serializer to strip, and a pointer
 * landing on a handle never lands in the contenteditable underneath.
 * ------------------------------------------------------------------ */

import { $ } from "./shell.js";
import { toSpanned, setPair } from "./columns.js";
import { findOwningEditor, beginUndo, endUndo } from "./undo.js";

// TinyMCE's toolbar is pinned to the top of the viewport; a handle must
// not run under it, for the same reason placeColUI keeps the column
// pill clear of it.
var TOP_LIMIT = 64;

var hooks = {};

// active describes the row the handles are currently drawn for. Rebuilt
// whenever the column tool re-resolves, which is every click and every
// column edit.
var active = null;

// drag is non-null only between pointerdown and pointerup. Its presence
// freezes the handles: a rebuild mid-gesture would destroy the element
// holding the pointer capture.
var drag = null;

/* ---- placing ----------------------------------------------------- */

// stacked reports whether the row is currently drawn as one column —
// which it is on a phone, since stock rows are "grid-cols-1
// sm:grid-cols-N". There is no gutter to put a handle in then, and a
// drag would be editing the sm: value while showing the mobile layout.
// The buttons have the same blind spot and hide nothing; the handle
// cannot pretend, which makes a real constraint visible for once.
//
// Measured rather than read off the classes: two cells side by side are
// separated by the gap, two cells stacked share a left edge.
function stacked(cells) {
    var a = cells[0].getBoundingClientRect();
    var b = cells[1].getBoundingClientRect();
    return b.left < a.right;
}

// layout puts every handle over its gutter, spanning the row's height.
// Split from placeHandles because a drag calls it on every move, when
// the guard placeHandles carries is exactly wrong.
function layout() {
    if (!active) return;
    var cells = active.cells;
    if (stacked(cells)) {
        active.handles.forEach(function (h) { h.hidden = true; });
        return;
    }
    var row = active.row.getBoundingClientRect();
    var top = Math.max(row.top, TOP_LIMIT);
    var height = row.bottom - top;
    active.handles.forEach(function (h, i) {
        // A row scrolled under the toolbar has no handle worth drawing,
        // and a zero-height one would be an invisible trap.
        if (height <= 0) { h.hidden = true; return; }
        var a = cells[i].getBoundingClientRect();
        var b = cells[i + 1].getBoundingClientRect();
        h.hidden = false;
        h.style.left = (a.right + b.left) / 2 + "px";
        h.style.top = top + "px";
        h.style.height = height + "px";
    });
}

export function placeHandles() {
    if (drag) return;
    layout();
}

export function hideHandles() {
    if (drag) return;
    active = null;
    var box = $("col-handles");
    while (box.firstChild) box.removeChild(box.firstChild);
}

// showHandles draws one handle per boundary in `row`. Callers pass only
// rows the column tool reports as resizable, so a row whose track count
// does not divide twelve never grows handles it would have to refuse.
export function showHandles(row, cells) {
    if (drag) return;
    hideHandles();
    if (!row || !cells || cells.length < 2) return;
    var box = $("col-handles");
    var handles = [];
    for (var i = 0; i < cells.length - 1; i++) handles.push(makeHandle(box, i));
    active = { row: row, cells: cells, handles: handles };
    layout();
}

function makeHandle(box, index) {
    var h = document.createElement("div");
    h.className = "colhandle";
    // Out of the accessibility tree on purpose. The narrower and wider
    // buttons are the keyboard path and they are still there; exposing
    // a control that announces itself and then cannot be operated by
    // keyboard would be worse than exposing nothing. Giving the handle
    // its own arrow-key handling — the ARIA window-splitter pattern —
    // is what it would take to earn a role here.
    h.setAttribute("aria-hidden", "true");
    h.addEventListener("pointerdown", function (e) { start(e, h, index); });
    box.appendChild(h);
    return h;
}

/* ---- dragging ---------------------------------------------------- */

// snapshot remembers the classes a gesture is about to rewrite, so one
// that ends where it started can put them back exactly. That matters
// more than it sounds: the first drag on an even row converts it to the
// twelve-track form, and a stray click on a handle must not leave
// "sm:grid-cols-2" rewritten as twelve tracks and two spans for nothing.
function snapshot(row, cells) {
    return {
        row: row.getAttribute("class"),
        cells: cells.map(function (c) { return c.getAttribute("class"); }),
    };
}

function restore(row, cells, snap) {
    setClass(row, snap.row);
    cells.forEach(function (c, i) { setClass(c, snap.cells[i]); });
}

function setClass(el, v) {
    if (v === null) el.removeAttribute("class");
    else el.setAttribute("class", v);
}

function start(e, handle, index) {
    if (!active || drag || e.button !== 0) return;
    // Without this the pointerdown would also place a caret in the
    // content under the handle and drag a text selection across the
    // gesture.
    e.preventDefault();

    var row = active.row;
    var cells = active.cells;
    var snap = snapshot(row, cells);
    var ed = findOwningEditor(row);
    // Stamp the undo state before anything is rewritten — including the
    // conversion to the twelve-track form, which is part of the edit.
    beginUndo(ed);

    var info = toSpanned(row);
    if (!info || info.cells.length !== cells.length) {
        restore(row, cells, snap);
        return;
    }

    // Everything the snap needs, measured once. The pair's outer edges
    // do not move while the boundary between them does, so taking them
    // now costs nothing in accuracy — and it saves measuring the row's
    // content box, which would have to account for the row's own
    // padding and would be wrong the moment a snippet has any.
    var a = info.cells[index];
    var b = info.cells[index + 1];
    var left = a.getBoundingClientRect().left;
    var right = b.getBoundingClientRect().right;
    var tracks = info.spans[index] + info.spans[index + 1];
    var gap = parseFloat(window.getComputedStyle(row).columnGap) || 0;
    var track = (right - left - (tracks - 1) * gap) / tracks;
    if (!(track > 0)) {
        restore(row, cells, snap);
        return;
    }

    drag = {
        row: row, cells: cells, snap: snap, ed: ed, prefix: info.prefix,
        a: a, b: b, left: left, gap: gap, track: track, tracks: tracks,
        start: info.spans[index], span: info.spans[index],
    };
    handle.classList.add("on");
    handle.setPointerCapture(e.pointerId);
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", stop);
    handle.addEventListener("pointercancel", cancel);
    // A capture lost to something outside this module — a window blur, a
    // browser gesture — would otherwise leave drag set forever, and with
    // it every handle frozen.
    handle.addEventListener("lostpointercapture", cancel);
    if (hooks.onStart) hooks.onStart();
}

function move(e) {
    if (!drag) return;
    // Which track boundary the pointer is nearest. The half-gap shifts
    // the rounding so the snap lands on the gutter's middle, which is
    // where the handle is drawn, rather than on a track's leading edge.
    var n = Math.round((e.clientX - drag.left + drag.gap / 2) / (drag.track + drag.gap));
    // A column can never be squeezed out of existence by dragging; use
    // Remove for that. Clamping here is what upholds it, and it is the
    // same floor resizeColumn refuses to step past.
    if (n < 1) n = 1;
    if (n > drag.tracks - 1) n = drag.tracks - 1;
    if (n === drag.span) return;
    drag.span = n;
    setPair(drag.prefix, drag.a, n, drag.b, drag.tracks - n);
    // The handle follows the snapped boundary rather than the pointer,
    // which is what makes the snapping legible: it lags visibly and then
    // catches up as the layout does.
    layout();
}

function finish(e, keep) {
    if (!drag) return;
    var d = drag;
    drag = null;
    var handle = e.currentTarget;
    handle.classList.remove("on");
    handle.removeEventListener("pointermove", move);
    handle.removeEventListener("pointerup", stop);
    handle.removeEventListener("pointercancel", cancel);
    handle.removeEventListener("lostpointercapture", cancel);
    if (handle.hasPointerCapture && handle.hasPointerCapture(e.pointerId)) {
        handle.releasePointerCapture(e.pointerId);
    }

    var changed = keep && d.span !== d.start;
    // A gesture that ends where it began — an abandoned drag, or a click
    // that never moved — puts every class back as it was and records no
    // undo level, so it is as if it never happened.
    if (!changed) restore(d.row, d.cells, d.snap);
    else endUndo(d.ed);
    layout();
    if (hooks.onEnd) hooks.onEnd(d.row, changed);
}

function stop(e) { finish(e, true); }
function cancel(e) { finish(e, false); }

// initColResize takes the two things this module should not know for
// itself: what else on the chrome has to get out of the way while a drag
// is running, and who to tell when one lands.
export function initColResize(opts) {
    hooks = opts || {};
}
