/* ------------------------------------------------------------------ *
 * Questions and answers — a run of disclosures, and the edits that
 * reshape it.
 *
 * An accordion here is not one widget. It is a sequence of sibling
 * <details> elements, each standing on its own, and that is the whole
 * design: no wrapper to keep in step, no index to renumber, no state to
 * synchronise. Adding a question is inserting an element next to its
 * neighbours; reordering is swapping two of them; deleting is removing
 * one. The browser supplies the opening and closing, the keyboard
 * handling, the screen-reader semantics and the find-in-page — see the
 * comment on faqItemHTML in snippets/snippets.go for why <details> is
 * the element rather than a scripted panel.
 *
 * The verbs are the ones an editor would name — add one after this,
 * move it up, move it down, delete it — and they are deliberately the
 * same four the column tool offers, minus resizing, which a stacked
 * list has no equivalent of. Someone who has used one tool has used
 * this one.
 *
 * What this file does NOT do is style anything or decide what a
 * question looks like. A new question is a copy of the one it was added
 * after, with its words replaced by placeholders, so it inherits
 * whatever classes that site's markup carries and a host that has
 * restyled its accordions gets restyled new questions for free.
 * ------------------------------------------------------------------ */

import { cmsConfirm } from "./dialogs.js";
import { copyOf } from "./clone.js";

// The class the CMS's own FAQ snippets carry, and what a host's CSS
// hangs its styling on. A <details> without it is somebody's own markup
// and none of this tool's business — the point of matching on a class
// rather than on the tag is that an editor can still hand-write a
// disclosure the tool leaves alone.
var FAQ = "cms-faq";

// itemOf walks up from a click to the disclosure it landed in, stopping
// at the block so a click outside never reaches for one above.
function itemOf(block, target) {
    var n = target;
    while (n && n !== block) {
        if (n.classList && n.classList.contains(FAQ)) return n;
        n = n.parentElement;
    }
    return null;
}

// runOf collects the questions either side of one: its immediately
// adjacent .cms-faq siblings, in document order.
//
// Adjacency rather than "every .cms-faq under the block" is what lets a
// page hold two separate accordions — Key Program Info and Frequently
// Asked Questions, on the same page of the site this was built for —
// and move a question within its own group rather than into the other.
// Anything between two runs (a heading, a paragraph) ends one and starts
// the next, which is what a reader sees too.
function runOf(item) {
    var run = [item];
    var n = item.previousElementSibling;
    while (n && n.classList && n.classList.contains(FAQ)) {
        run.unshift(n);
        n = n.previousElementSibling;
    }
    n = item.nextElementSibling;
    while (n && n.classList && n.classList.contains(FAQ)) {
        run.push(n);
        n = n.nextElementSibling;
    }
    return run;
}

// faqTarget describes the question a click landed in, or null when it
// did not land in one.
export function faqTarget(block, target) {
    var item = itemOf(block, target);
    if (!item) return null;
    var run = runOf(item);
    var at = run.indexOf(item);
    return {
        item: item,
        run: run,
        index: at,
        count: run.length,
        canMoveUp: at > 0,
        canMoveDown: at < run.length - 1,
    };
}

// PLACEHOLDER_Q and PLACEHOLDER_A are what a new question says until
// someone writes it. Deliberately a question and an answer rather than
// "Lorem ipsum": an editor who adds three and fills in two can see at a
// glance which one is still waiting.
var PLACEHOLDER_Q = "A question people ask?";
var PLACEHOLDER_A = "A short, direct answer.";

// blankCopy clones a question and empties it of words, keeping every
// class and wrapper so the new one is styled like its neighbours.
function blankCopy(item) {
    var copy = copyOf(item);
    var summary = copy.querySelector("summary");
    if (summary) summary.textContent = PLACEHOLDER_Q;
    // The answer is whatever is not the summary. Replacing its text
    // rather than rebuilding it keeps a host's wrapper markup — the
    // cms-faq-body div, or whatever a site's own snippet uses.
    var body = null;
    for (var n = copy.firstElementChild; n; n = n.nextElementSibling) {
        if (n !== summary) { body = n; break; }
    }
    if (body) {
        var p = body.querySelector("p");
        if (p) {
            p.textContent = PLACEHOLDER_A;
            // A copied answer may hold several paragraphs, a list, an
            // image. One placeholder paragraph is the whole of a blank
            // answer, so the rest goes.
            while (p.nextElementSibling) p.nextElementSibling.remove();
            while (p.previousElementSibling) p.previousElementSibling.remove();
        } else {
            body.textContent = PLACEHOLDER_A;
        }
    }
    // A copy of an expanded question should not itself be expanded: it
    // has nothing in it worth reading yet.
    copy.removeAttribute("open");
    return copy;
}

// addQuestion inserts a fresh question after the one clicked, and
// returns it so the caller can put the caret in it.
export function addQuestion(info) {
    var copy = blankCopy(info.item);
    info.item.parentNode.insertBefore(copy, info.item.nextSibling);
    return copy;
}

// moveQuestion swaps a question with its neighbour. dir is -1 for up and
// +1 for down; a move that would leave the run does nothing, which is
// why the buttons for it are hidden at the ends rather than disabled.
export function moveQuestion(info, dir) {
    var to = info.index + dir;
    if (to < 0 || to >= info.count) return;
    var other = info.run[to];
    if (dir < 0) other.parentNode.insertBefore(info.item, other);
    else other.parentNode.insertBefore(info.item, other.nextSibling);
}

// confirmRemove asks before throwing away a question somebody wrote, and
// does not ask about one that still says what it said when it arrived.
//
// The comparison is against the placeholders rather than against
// emptiness: a question nobody has touched has words in it, so "is it
// empty" would prompt every time and teach an editor to click through
// the prompt without reading it.
export function confirmRemove(info) {
    var summary = info.item.querySelector("summary");
    var q = summary ? (summary.textContent || "").trim() : "";
    var text = (info.item.textContent || "").trim();
    var untouched = q === PLACEHOLDER_Q &&
        text.replace(q, "").trim() === PLACEHOLDER_A;
    if (untouched || !text) return Promise.resolve(true);
    return cmsConfirm("Deleting this question deletes its answer. Continue?",
        "Delete", true);
}

// removeQuestion drops a question and reports the one to put the chrome
// on next — the neighbour below where there is one, above otherwise, and
// null when that was the last question in the run.
export function removeQuestion(info) {
    var next = info.run[info.index + 1] || info.run[info.index - 1] || null;
    info.item.remove();
    return next;
}
