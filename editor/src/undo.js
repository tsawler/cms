/* ------------------------------------------------------------------ *
 * Undo for the edits the chrome makes.
 *
 * Everything the editor's own UI changes — a button's colors, an
 * image's caption, a block's spacing or column count, a deleted
 * snippet — happens as a DOM change made from outside TinyMCE. Two
 * things have to be true for such a change to be undoable, and missing
 * either one looks identical from the outside:
 *
 *   1. It has to run inside the owning editor's undo transaction, or no
 *      level is recorded and there is nothing to go back to.
 *   2. That editor has to hold focus afterwards, or the keystroke never
 *      reaches it.
 *
 * The second is the one that is easy to miss. Every one of these edits
 * is made from a dialog or a floating chrome button, and when that
 * closes the browser leaves focus on <body> — so the level is recorded
 * perfectly and Ctrl/Cmd+Z still does nothing until the region is
 * clicked back into. runWithUndo does both halves, which is why call
 * sites should use it rather than calling transact directly.
 * ------------------------------------------------------------------ */

import { state } from "./state.js";

// findOwningEditor returns the TinyMCE instance managing the content
// that contains el, so a change to el can join that editor's undo stack.
// Null when el is not inside any editable region.
export function findOwningEditor(el) {
    var all = [];
    Object.keys(state.mceEditors).forEach(function (k) { all.push(state.mceEditors[k]); });
    state.sectionEditors.forEach(function (s) { all.push(s.ed); });
    for (var i = 0; i < all.length; i++) {
        var target = all[i].getElement && all[i].getElement();
        if (target && target.contains(el)) return all[i];
    }
    return null;
}

// beginUndo and endUndo bracket a change that happens over time rather
// than all at once — a column drag, where the spans are rewritten on
// every pointer move and one undo level should cover the whole gesture.
// transact cannot express that: it records a level around a function
// that returns, so a drag would either flood the stack with a level per
// move or record nothing at all.
//
// beforeChange stamps the state as it is now; add() records a level
// against whatever the DOM has become since. Between the two the change
// is free to happen as many times as it likes.
//
// A gesture that ends where it started must simply not call endUndo:
// beforeChange on its own records nothing, so an abandoned drag leaves
// the stack exactly as it found it.
export function beginUndo(ed) {
    if (ed) ed.undoManager.beforeChange();
}

export function endUndo(ed) {
    if (!ed) return;
    ed.undoManager.add();
    // Focus last, for the reason runWithUndo does it: the level is
    // useless until the keystroke that reaches it has somewhere to land.
    ed.focus();
}

// runWithUndo performs a content change inside ed's undo transaction and
// hands focus back to ed, so the change is both recorded and reachable
// from the keyboard. A null ed — the element is not inside an editable
// region, or editors are not attached yet — still runs the change; it
// simply is not undoable, which is the best available answer.
export function runWithUndo(ed, run) {
    if (!ed) {
        run();
        return;
    }
    ed.undoManager.transact(run);
    // Focus last: the change is already in the DOM, so TinyMCE restores
    // the selection against the content the editor will actually have.
    ed.focus();
}
