/* ------------------------------------------------------------------ *
 * Photo slots — the "Click to add a photo" placeholders the imported
 * snippet library ships with. While editing, a click opens the media
 * library and swaps the slot for an <img> that keeps the slot's shape
 * classes, so a gallery tile stays 1:1, a product photo stays 16:9,
 * and a portrait stays a circle. The inserted markup must stay in step
 * with the editor sanitizer's img allowances (same shape the rich-text
 * image button inserts, plus the class attribute).
 * ------------------------------------------------------------------ */

import { state } from "./state.js";
import { openPicker } from "./media.js";
import { markDirty, markSectionsDirty } from "./editing.js";

function escapeAttr(s) {
    return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
}

// imgClassFor derives the replacement image's classes from the slot:
// the shape utilities (aspect ratio, fixed size, rounding, centering,
// object fit) carry over, the placeholder chrome (dashed border,
// background, flex centering) does not. Photos crop to the shape with
// object-cover unless the slot carries its own object-* (the logo
// slots use object-contain so logos letterbox instead), and w-full
// fills the column unless the slot is fixed-size (size-*).
function imgClassFor(slot) {
    var keep = [];
    var fixedSize = false, hasFit = false;
    (slot.getAttribute("class") || "").split(/\s+/).forEach(function (c) {
        if (/^(aspect-|size-|rounded($|-)|mx-auto$|object-)/.test(c)) {
            keep.push(c);
            if (c.indexOf("size-") === 0) fixedSize = true;
            if (c.indexOf("object-") === 0) hasFit = true;
        }
    });
    if (!fixedSize) keep.push("w-full");
    if (!hasFit) keep.push("object-cover");
    return keep.join(" ");
}

// SLOT_CHROME is the placeholder's own look — the dashed box and the
// centring that make it read as somewhere a picture goes. Kept apart
// from the shape classes below because those two halves are exactly what
// imgClassFor splits an existing slot into, in the other direction.
//
// It has to match the slot markup in snippets/library.go. A mismatch is
// not a crash but a slow drift: a card emptied here would grow a
// slightly different placeholder from the one it shipped with.
var SLOT_CHROME = "cms-photo-slot not-prose flex items-center justify-center " +
    "rounded-lg border-2 border-dashed border-slate-300 bg-slate-50";
var SLOT_LABEL = '<p class="font-semibold text-slate-500">&#128247; Click to add a photo</p>';

// emptySlot is imgClassFor run backwards: it rebuilds the placeholder an
// image was dropped into, keeping the shape the slot had (a circle stays
// a circle, a 1:1 tile stays 1:1) and putting the chrome back.
//
// It exists for the card tool's "add a blank one" (team.js). A blank
// card is a copy of its neighbour with the words replaced, and a copy
// that kept the neighbour's face would be the one placeholder nobody
// could mistake for a placeholder.
export function emptySlot(img) {
    var keep = [];
    (img.getAttribute("class") || "").split(/\s+/).forEach(function (c) {
        // w-full and object-cover are imgClassFor's additions, not the
        // slot's; everything else it kept came off the slot and goes
        // back on it. rounded-* is in SLOT_CHROME already, so a slot
        // whose shape is a circle replaces it rather than adding to it.
        if (/^(aspect-|size-|rounded($|-)|mx-auto$)/.test(c)) keep.push(c);
    });
    var chrome = SLOT_CHROME;
    if (keep.some(function (c) { return /^rounded($|-)/.test(c); })) {
        chrome = chrome.replace(" rounded-lg", "");
    }
    var div = document.createElement("div");
    div.setAttribute("class", chrome + (keep.length ? " " + keep.join(" ") : ""));
    div.setAttribute("data-cms-photo-slot", "");
    // A slot too small for a label gets the camera alone, the way
    // photoSlotCircle ships. size-* is the only fixed-size form the
    // slots use, and it is the small one.
    div.innerHTML = keep.some(function (c) { return c.indexOf("size-") === 0; })
        ? '<p class="text-2xl">&#128247;</p>'
        : SLOT_LABEL;
    return div;
}

export function initPhotoSlots() {
    // Capture phase, registered before the snippet-chrome handler, so a
    // slot click opens the picker instead of the drag/trash chrome.
    document.addEventListener("click", function (e) {
        if (!state.editing) return;
        var slot = e.target.closest ? e.target.closest("[data-cms-photo-slot]") : null;
        if (!slot) return;
        e.preventDefault();
        e.stopPropagation();
        openPicker("image", function (item) {
            // Resolve the dirty target before the slot leaves the DOM.
            var region = slot.closest("[data-cms-region]");
            var container = slot.closest("[data-cms-sections]");
            slot.outerHTML = '<img src="' + escapeAttr(item.web) +
                '" alt="' + escapeAttr(item.alt || "") + '" loading="lazy"' +
                ' class="' + imgClassFor(slot) + '"' +
                ' data-cms-web="' + escapeAttr(item.web) +
                '" data-cms-orig="' + escapeAttr(item.original) + '">';
            if (region) markDirty(region.getAttribute("data-cms-region"));
            else if (container) markSectionsDirty(container.getAttribute("data-cms-sections"));
        });
    }, true);
}
