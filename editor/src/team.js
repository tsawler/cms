/* ------------------------------------------------------------------ *
 * Team cards — a wrapping grid of people, and the edits that reshape it.
 *
 * A staff page is the one layout where "how many across" and "how many
 * there are" have to be different numbers. Ten people is ten cards; how
 * many of them sit on a line is the reader's screen's business, not the
 * editor's. So a team grid sets a *maximum* per row and lets the rest
 * wrap, and adding somebody is inserting one more sibling — no track
 * count to grow, no widths to rebalance, nothing that gets narrower
 * because a colleague was hired.
 *
 * That is exactly what the column tool does NOT do, which is why this
 * file exists rather than reusing it. Every button on the column tool
 * changes the row: a seventh column is refused because a row of seven
 * cannot be spanned, and the six it allows each get thinner as they
 * arrive. Both behaviours are right for a two-up text layout and wrong
 * for a list of people. A grid marked .cms-team is invisible to
 * columns.js (SELF_MANAGED there) so the two tools never write the same
 * grid-cols class.
 *
 * The verbs are the ones the column tool offers minus resizing, in the
 * same order and with the same icons: move it left, move it right, put a
 * copy either side of it, add a blank one, throw it away. Someone who
 * has reshaped a row has already used this.
 *
 * What this file does not do is decide what a card looks like. A new
 * card is a copy of the one it was added beside, so it inherits whatever
 * classes that site's markup carries and a host that has restyled its
 * team page gets restyled new cards for free.
 * ------------------------------------------------------------------ */

import { cmsConfirm } from "./dialogs.js";
import { copyOf } from "./clone.js";
import { emptySlot } from "./photos.js";

// The classes the CMS's own team markup carries: the grid, and one card
// in it. A bare <div> in a grid is somebody's own layout and none of
// this tool's business — matching on a class rather than on "child of a
// grid" is what lets an editor hand-write a three-up section the tool
// leaves alone.
var CARD = "cms-team-card";

// cardOf walks up from a click to the card it landed in, stopping at the
// root so a click outside never reaches for one above.
function cardOf(root, target) {
    var n = target;
    while (n && n !== root) {
        if (n.classList && n.classList.contains(CARD)) return n;
        n = n.parentElement;
    }
    return null;
}

// runOf collects the cards either side of one: its immediately adjacent
// .cms-team-card siblings, in document order.
//
// Adjacency rather than "every card under the block", for the same
// reason the question tool works that way: a page can hold two team
// grids — Leadership and Sales, under two headings — and "move right"
// should walk a person along their own list rather than into the other
// one. Anything between two runs ends one and starts the next, which is
// what a reader sees too.
function runOf(card) {
    var run = [card];
    var n = card.previousElementSibling;
    while (n && n.classList && n.classList.contains(CARD)) {
        run.unshift(n);
        n = n.previousElementSibling;
    }
    n = card.nextElementSibling;
    while (n && n.classList && n.classList.contains(CARD)) {
        run.push(n);
        n = n.nextElementSibling;
    }
    return run;
}

// teamTarget describes the card a click landed in, or null when it did
// not land in one.
//
// "back" and "on" rather than "left" and "right" is columns.js's
// vocabulary, and it is deliberate there: the first card is at the start
// of the run in a right-to-left locale too, where the arrow that moves
// it towards the start points the other way.
export function teamTarget(root, target) {
    var card = cardOf(root, target);
    if (!card) return null;
    var run = runOf(card);
    var at = run.indexOf(card);
    return {
        card: card,
        run: run,
        index: at,
        count: run.length,
        canMoveBack: at > 0,
        canMoveOn: at < run.length - 1,
    };
}

// The words a card says until somebody writes it, matching teamCardHTML
// in snippets/snippets.go. Deliberately a name, a job title and a
// sentence rather than "Lorem ipsum": an editor filling in nine people
// can see at a glance which ones are still waiting — and confirmRemove
// below reads them back to tell a card nobody has touched from one
// somebody wrote.
var PLACEHOLDER_NAME = "Full Name";
var PLACEHOLDER_TITLE = "Job title";
var PLACEHOLDER_BIO = "A sentence or two about this person — what they " +
    "do, and anything a visitor would want to know before getting in touch.";

// textParas lists a card's own paragraphs. The photo slot ships with one
// inside it ("Click to add a photo") and that is chrome, not prose — a
// blank card that had its slot label overwritten with a job title would
// lose the thing telling an editor the picture is missing.
function textParas(card) {
    return Array.prototype.filter.call(card.querySelectorAll("p"), function (p) {
        return !p.closest("[data-cms-photo-slot]");
    });
}

// blankCopy clones a card and empties it of everything personal, keeping
// every class and wrapper so the new card is styled like its neighbours.
//
// "Everything personal" includes the face. A duplicate is meant to carry
// the original's picture — that is what duplicating is — but a card added
// as blank must not, or the newest member of staff silently wears a
// colleague's portrait until somebody notices.
function blankCopy(card) {
    var copy = copyOf(card);
    copy.querySelectorAll("img").forEach(function (img) {
        img.parentNode.replaceChild(emptySlot(img), img);
    });
    var name = copy.querySelector("h1,h2,h3,h4,h5,h6");
    if (name) name.textContent = PLACEHOLDER_NAME;
    // The first paragraph is the job title and the second the biography,
    // which is the shape every stock card has. A card with only one
    // paragraph gets the title and keeps its shape; extra paragraphs go,
    // because one placeholder sentence is the whole of a blank card.
    var ps = textParas(copy);
    if (ps.length) ps[0].textContent = PLACEHOLDER_TITLE;
    if (ps.length > 1) ps[1].textContent = PLACEHOLDER_BIO;
    for (var i = 2; i < ps.length; i++) ps[i].remove();
    return copy;
}

// addCard inserts a fresh, empty card after the one clicked, and returns
// it so the caller can re-anchor the chrome on it.
export function addCard(info) {
    var copy = blankCopy(info.card);
    info.card.parentNode.insertBefore(copy, info.card.nextSibling);
    return copy;
}

// duplicateCard puts a full copy of a card beside it — dir -1 for the
// side the run starts at, +1 for the side it ends at — and returns the
// copy, so the chrome lands on the new card and the next click is
// already on the thing that needs editing.
//
// A whole copy, words and picture and all. Someone duplicating a card is
// usually saying "another one like this" about its *shape* — the same
// heading, the same badge, whatever that site's card carries — and
// replacing three fields is less work than rebuilding a layout. The
// blank card is one button along for the other case.
export function duplicateCard(info, dir) {
    var copy = copyOf(info.card);
    if (dir < 0) info.card.parentNode.insertBefore(copy, info.card);
    else info.card.parentNode.insertBefore(copy, info.card.nextSibling);
    return copy;
}

// moveCard swaps a card with its neighbour. dir is -1 towards the start
// of the run and +1 towards the end; a move that would leave the run
// does nothing, which is why the buttons for it are hidden at the ends
// rather than disabled.
export function moveCard(info, dir) {
    var to = info.index + dir;
    if (to < 0 || to >= info.count) return;
    var other = info.run[to];
    if (dir < 0) other.parentNode.insertBefore(info.card, other);
    else other.parentNode.insertBefore(info.card, other.nextSibling);
}

// confirmRemove asks before throwing away somebody an editor wrote in,
// and does not ask about a card that still says what it said when it
// arrived.
//
// The comparison is against the placeholders rather than against
// emptiness: an untouched card has words and a photo slot in it, so "is
// it empty" would prompt every time and teach an editor to click through
// the prompt without reading it. A picture counts as written in on its
// own — choosing one is work, and losing it to an unprompted click is
// the same annoyance as losing a paragraph.
export function confirmRemove(info) {
    var name = info.card.querySelector("h1,h2,h3,h4,h5,h6");
    var written = name && (name.textContent || "").trim() !== PLACEHOLDER_NAME;
    if (!written) {
        var ps = textParas(info.card);
        written = ps.some(function (p, i) {
            var t = (p.textContent || "").trim();
            if (!t) return false;
            return i === 0 ? t !== PLACEHOLDER_TITLE : t !== PLACEHOLDER_BIO;
        });
    }
    if (!written) written = !!info.card.querySelector("img");
    if (!written) return Promise.resolve(true);
    return cmsConfirm("Delete this person from the team?", "Delete", true);
}

// removeCard drops a card and reports the one to put the chrome on next
// — the neighbour to its right where there is one, to its left
// otherwise, and null when that was the last card in the run.
export function removeCard(info) {
    var next = info.run[info.index + 1] || info.run[info.index - 1] || null;
    info.card.remove();
    return next;
}
