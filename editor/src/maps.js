/* ------------------------------------------------------------------ *
 * Map slots — the "Click to add a map" placeholders the map snippet
 * and section presets ship with. While editing, a click prompts for a
 * Google Maps link (any shape people actually have: the Share > Embed
 * code, a maps URL, or just a typed address) and swaps the slot for a
 * bounded maps iframe. The output must stay in step with the editor
 * sanitizer's embedURLRe maps forms.
 * ------------------------------------------------------------------ */

import { state } from "./state.js";
import { setMsg } from "./util.js";
import { cmsPrompt } from "./dialogs.js";
import { markDirty, markSectionsDirty } from "./editing.js";

function escapeAttr(s) {
    return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
}

// mapEmbedURL turns what people actually paste into an embeddable src,
// or null when it's a link somewhere that isn't Google Maps.
export function mapEmbedURL(raw) {
    raw = String(raw || "").trim();
    if (!raw) return null;
    // The whole <iframe> from Share > "Embed a map": lift out the src.
    var m = raw.match(/<iframe[^>]*\ssrc=["']([^"']+)["']/i);
    if (m) raw = m[1];
    // The official embed URL passes through untouched.
    if (/^https:\/\/(?:www\.)?google\.com\/maps\/embed\?/.test(raw)) return raw;
    var u = null;
    try { u = new URL(raw); } catch (e) { /* not a URL: treat as an address */ }
    if (u) {
        var host = u.hostname.replace(/^www\./, "");
        if (host !== "google.com" && host !== "maps.google.com") return null;
        // A share/browse link: prefer its q=, else the @lat,lng from
        // the path, else the /maps/place/<name> segment.
        var q = u.searchParams.get("q");
        if (!q) {
            var at = (u.pathname + u.hash).match(/@(-?[0-9.]+),(-?[0-9.]+)/);
            if (at) q = at[1] + "," + at[2];
        }
        if (!q) {
            var place = u.pathname.match(/\/maps\/place\/([^/]+)/);
            if (place) q = decodeURIComponent(place[1].replace(/\+/g, " "));
        }
        if (!q) return null;
        return "https://www.google.com/maps?q=" + encodeURIComponent(q) + "&output=embed";
    }
    // Plain text: the keyless query embed takes any address or place.
    return "https://www.google.com/maps?q=" + encodeURIComponent(raw) + "&output=embed";
}

// isMapEmbed reports whether el is a maps iframe rather than a
// YouTube/Vimeo embed — the discriminator the shared embed chrome
// (buttons.js) uses to route its gear and word its delete confirm.
export function isMapEmbed(el) {
    return !!(el && el.tagName === "IFRAME" &&
        /^https:\/\/(?:www\.)?(?:google\.com|maps\.google\.com)\/maps/.test(el.src || ""));
}

// chooseMapInto prompts for a location and replaces el — an unfilled
// slot, or an existing map being changed from the gear — with the
// resulting maps iframe.
export function chooseMapInto(el, message) {
    cmsPrompt(message, "e.g. 123 Main Street, Halifax", "Embed map")
        .then(function (v) {
            if (v === null || v === "") return;
            var src = mapEmbedURL(v);
            if (!src) {
                setMsg("That doesn't look like a Google Maps link or an address.");
                return;
            }
            var region = el.closest("[data-cms-region]");
            var container = el.closest("[data-cms-sections]");
            el.outerHTML = '<iframe class="w-full aspect-video rounded-lg"' +
                ' src="' + escapeAttr(src) + '" title="Map" loading="lazy"></iframe>';
            if (region) markDirty(region.getAttribute("data-cms-region"));
            else if (container) markSectionsDirty(container.getAttribute("data-cms-sections"));
        });
}

export function initMapSlots() {
    // Capture phase, registered before the snippet-chrome handler, so a
    // slot click opens the prompt instead of the drag/trash chrome.
    document.addEventListener("click", function (e) {
        if (!state.editing) return;
        var slot = e.target.closest ? e.target.closest("[data-cms-map-slot]") : null;
        if (!slot) return;
        e.preventDefault();
        e.stopPropagation();
        chooseMapInto(slot, "Paste a Google Maps link, its embed code, or type an address");
    }, true);
}
