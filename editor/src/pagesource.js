/* ------------------------------------------------------------------ *
 * Whole-page HTML source (superadmin only) — a rail button that opens
 * every editable HTML chunk on the page (rich regions and section
 * contents) in one source view, delimited by cms: marker comments.
 * Apply routes each chunk back through its own TinyMCE instance, so
 * the storage model is untouched: section wrappers stay derived from
 * their settings, and saves go through the normal per-region APIs.
 * Text regions, image slots, and menus have no HTML form — they are
 * not shown and stay editable on the page itself.
 * ------------------------------------------------------------------ */

import { state, isSuperadmin } from "./state.js";
import { $ } from "./shell.js";
import { setMsg, flash } from "./util.js";
import { openSource, formatHTML } from "./source.js";
import { markDirty, markSectionsDirty } from "./editing.js";
import { lockButtons } from "./buttons.js";

var INTRO = "<!-- Editable page content. Keep the cms: marker comments — they route " +
    "each chunk back to its place. Text areas, images, menus, and section " +
    "layout/settings are edited on the page itself. -->";

function openMarker(c) { return "<!-- cms:" + c.key + " -->"; }
function closeMarker(c) { return "<!-- /cms:" + c.key + " -->"; }

// chunkList builds the ordered manifest of the page's editable HTML:
// one chunk per rich region, one per section. Only chunks with a live
// TinyMCE instance qualify — everything routes back through an editor.
function chunkList() {
    var chunks = [];
    document.querySelectorAll('[data-cms-region][data-cms-kind="html"]').forEach(function (el) {
        var name = el.dataset.cmsRegion;
        if (state.mceEditors[name]) {
            chunks.push({ key: "region " + name, region: name });
        }
    });
    document.querySelectorAll("[data-cms-sections]").forEach(function (container) {
        var region = container.getAttribute("data-cms-sections");
        var i = 0;
        container.querySelectorAll("[data-cms-section-content]").forEach(function (el) {
            i++;
            state.sectionEditors.some(function (s) {
                if (s.el !== el) return false;
                chunks.push({ key: "section " + region + "/" + i, region: region, entry: s });
                return true;
            });
        });
    });
    return chunks;
}

function chunkContent(c) {
    return c.entry ? c.entry.ed.getContent() : state.mceEditors[c.region].getContent();
}

function compose(chunks) {
    var parts = [INTRO];
    chunks.forEach(function (c) {
        parts.push("", openMarker(c), c.orig, closeMarker(c));
    });
    return parts.join("\n");
}

// parse splits the edited text back into per-chunk HTML. Every marker
// must appear exactly once and in order, and no cms: markers beyond
// the expected ones may exist — the seams are the routing table, so a
// mangled seam refuses to apply rather than misfiling content.
function parse(text, chunks) {
    var parts = [];
    var pos = 0;
    for (var i = 0; i < chunks.length; i++) {
        var c = chunks[i];
        var o = openMarker(c);
        var cl = closeMarker(c);
        var oi = text.indexOf(o);
        var ci = text.indexOf(cl);
        if (oi === -1 || ci === -1) {
            return { error: 'The marker for "' + c.key + '" is missing or altered — put it back, or Cancel and reopen.' };
        }
        if (text.indexOf(o, oi + 1) !== -1 || text.indexOf(cl, ci + 1) !== -1) {
            return { error: 'The marker for "' + c.key + '" appears more than once.' };
        }
        if (oi < pos || ci < oi) {
            return { error: "The cms: markers are out of order — keep the chunks in their original order." };
        }
        parts.push({ chunk: c, html: text.slice(oi + o.length, ci).trim() });
        pos = ci + cl.length;
    }
    var total = (text.match(/<!--\s*\/?cms:/g) || []).length;
    if (total !== chunks.length * 2) {
        return { error: "Unexpected cms: markers found — regions and sections can't be added or removed here. Use the page tools instead, then reopen." };
    }
    return { parts: parts };
}

export function initPageSource() {
    var btn = $("rail-html");
    btn.hidden = !isSuperadmin;
    if (!isSuperadmin) return;

    btn.addEventListener("click", function () {
        var chunks = chunkList();
        if (!chunks.length) {
            // Distinguish "nothing here" from "TinyMCE still attaching".
            var hasAreas = document.querySelector(
                '[data-cms-region][data-cms-kind="html"],[data-cms-section-content]');
            setMsg(hasAreas ? "The editor is still loading — try again in a moment."
                : "This page has no HTML content areas.");
            return;
        }
        chunks.forEach(function (c) { c.orig = chunkContent(c); });
        openSource({
            title: "Page HTML",
            hint: "All editable HTML on this page, in order. Keep the cms: markers. Applied changes still need Save.",
            html: compose(chunks),
            validate: function (text) {
                return parse(text, chunks).error || null;
            },
        }).then(function (text) {
            if (text === null) return;
            var r = parse(text, chunks);
            if (r.error) return; // validate already refused this
            var changed = 0;
            r.parts.forEach(function (p) {
                // The view re-formats markup, so compare against the
                // original's formatted form to spot real edits.
                if (p.html === formatHTML(p.chunk.orig).trim()) return;
                changed++;
                var ed = p.chunk.entry ? p.chunk.entry.ed : state.mceEditors[p.chunk.region];
                ed.undoManager.transact(function () { ed.setContent(p.html); });
                if (p.chunk.entry) markSectionsDirty(p.chunk.region);
                else markDirty(p.chunk.region);
            });
            if (!changed) {
                flash("No changes to apply");
                return;
            }
            lockButtons(); // the new markup may contain buttons
            flash(changed === 1 ? "1 area updated" : changed + " areas updated");
        });
    });
}
