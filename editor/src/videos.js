/* ------------------------------------------------------------------ *
 * Video slots — the "Click to add a video" placeholders that the video
 * snippet and section presets ship with. While editing, a click offers
 * the media library or a YouTube/Vimeo link and swaps the slot for a
 * <video> player or an <iframe> embed. Both output shapes must stay in
 * step with the editor sanitizer's video/iframe allowances.
 * ------------------------------------------------------------------ */

import { state } from "./state.js";
import { setMsg } from "./util.js";
import { openDialog, cmsPrompt } from "./dialogs.js";
import { openPicker } from "./media.js";
import { markDirty, markSectionsDirty } from "./editing.js";

function escapeAttr(s) {
    return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
}

// embedURL turns the link shapes people actually paste (watch pages,
// share links, shorts, existing embeds) into the canonical embed URL, or
// null when the link isn't recognizably YouTube/Vimeo.
export function embedURL(raw) {
    var u;
    try { u = new URL(raw.trim()); } catch (e) { return null; }
    if (u.protocol !== "https:" && u.protocol !== "http:") return null;
    var host = u.hostname.replace(/^www\./, "").replace(/^m\./, "");
    var id = null;
    if (host === "youtu.be") {
        id = u.pathname.slice(1).split("/")[0];
    } else if (host === "youtube.com" || host === "youtube-nocookie.com") {
        if (u.pathname === "/watch") id = u.searchParams.get("v");
        else {
            var m = u.pathname.match(/^\/(?:embed|shorts|live)\/([\w-]+)/);
            if (m) id = m[1];
        }
    }
    if (id && /^[\w-]{5,20}$/.test(id)) {
        return "https://www.youtube-nocookie.com/embed/" + id;
    }
    if (host === "vimeo.com" || host === "player.vimeo.com") {
        var vm = u.pathname.match(/\/(?:video\/)?([0-9]{4,15})(?:\/|$)/);
        if (vm) return "https://player.vimeo.com/video/" + vm[1];
    }
    return null;
}

// replaceEl swaps an element (a slot placeholder, or an existing player
// being changed) for new markup and marks whichever container holds it
// dirty (same resolution the spacer handler uses).
function replaceEl(el, html) {
    var region = el.closest("[data-cms-region]");
    var container = el.closest("[data-cms-sections]");
    el.outerHTML = html;
    if (region) markDirty(region.getAttribute("data-cms-region"));
    else if (container) markSectionsDirty(container.getAttribute("data-cms-sections"));
}

function fillFromLibrary(el) {
    openPicker("video", function (item) {
        replaceEl(el, '<video controls preload="metadata" class="w-full rounded-lg"' +
            ' src="' + escapeAttr(item.original) + '"' +
            (item.poster ? ' poster="' + escapeAttr(item.poster) + '"' : "") +
            "></video>");
    });
}

function fillFromURL(el) {
    cmsPrompt("Paste the video's link", "https://www.youtube.com/watch?v=…", "Embed video")
        .then(function (v) {
            if (v === null || v === "") return;
            var src = embedURL(v);
            if (!src) {
                setMsg("That doesn't look like a YouTube or Vimeo link.");
                return;
            }
            replaceEl(el, '<iframe class="w-full aspect-video rounded-lg"' +
                ' src="' + escapeAttr(src) + '" title="Video" loading="lazy"' +
                ' allow="fullscreen; picture-in-picture" allowfullscreen=""></iframe>');
        });
}

// chooseVideoInto runs the source chooser (library upload or external
// link) and replaces el with the chosen player. Used by slot clicks and
// by the video gear when changing an existing video.
export function chooseVideoInto(el, message) {
    openDialog({
        message: message,
        okLabel: "Next",
        selects: [{
            id: "source",
            label: "Where is the video?",
            value: "library",
            options: [
                { value: "library", label: "Media library (uploaded file)" },
                { value: "url", label: "YouTube or Vimeo link" },
            ],
        }],
    }).then(function (values) {
        if (!values) return;
        if (values.source === "url") fillFromURL(el);
        else fillFromLibrary(el);
    });
}

export function initVideoSlots() {
    // Capture phase, registered before the snippet-chrome handler, so a
    // slot click opens the chooser instead of the drag/trash chrome.
    document.addEventListener("click", function (e) {
        if (!state.editing) return;
        var slot = e.target.closest ? e.target.closest("[data-cms-video-slot]") : null;
        if (!slot) return;
        e.preventDefault();
        e.stopPropagation();
        chooseVideoInto(slot, "Add a video");
    }, true);
}
