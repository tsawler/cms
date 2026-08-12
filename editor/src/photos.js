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
