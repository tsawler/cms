/* ------------------------------------------------------------------ *
 * Saving and publishing
 * ------------------------------------------------------------------ */

import { state, cfg, pageId } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { cmsConfirm } from "./dialogs.js";
import { setEditing, restoreSnapshot, hasUnsaved, updateBarButtons } from "./editing.js";

// The chip has three states: draft (never/no longer live), Live
// (published and in sync), and "Unpublished edits" (live, but the saved
// draft differs — the state that makes drafts trustworthy).
export function updateChip() {
    var chip = $("chip");
    chip.classList.remove("published", "changes");
    if (state.pageStatus === "published" && state.hasUnpublished) {
        chip.textContent = "Unpublished edits";
        chip.title = "This page has saved edits that aren't live yet — Publish makes them live";
        chip.classList.add("changes");
    } else if (state.pageStatus === "published") {
        chip.textContent = "Live";
        chip.title = "This page is published and up to date";
        chip.classList.add("published");
    } else {
        chip.textContent = state.pageStatus;
        chip.title = "Only editors can see this page until it's published";
    }
}

function collect() {
    var values = {};
    Object.keys(state.dirty).forEach(function (name) {
        if (state.mceEditors[name]) {
            values[name] = state.mceEditors[name].getContent();
            return;
        }
        var el = document.querySelector('[data-cms-region="' + name + '"]');
        if (el) {
            values[name] = el.dataset.cmsKind === "text" ? el.textContent : el.innerHTML;
            return;
        }
        if (state.imageValues[name] !== undefined) values[name] = state.imageValues[name];
    });
    return values;
}

// collectSections reads a sections region's current DOM order plus
// each section's settings and (editor-cleaned) content.
function collectSections(region) {
    var container = document.querySelector('[data-cms-sections="' + region + '"]');
    var out = [];
    if (!container) return out;
    container.querySelectorAll("[data-cms-section]").forEach(function (wrapper) {
        var contentEl = wrapper.querySelector("[data-cms-section-content]");
        if (!contentEl) return;
        var html = contentEl.innerHTML;
        state.sectionEditors.some(function (s) {
            if (s.el === contentEl) { html = s.ed.getContent(); return true; }
            return false;
        });
        out.push({ bg: wrapper.dataset.cmsBg || "", width: wrapper.dataset.cmsWidth || "",
            height: wrapper.dataset.cmsHeight || "", valign: wrapper.dataset.cmsValign || "",
            bgcolor: wrapper.dataset.cmsBgcolor || "", bgimage: wrapper.dataset.cmsBgimage || "",
            html: html });
    });
    return out;
}

export function save() {
    var values = collect();
    var secRegions = Object.keys(state.sectionsDirty);
    if (Object.keys(values).length === 0 && secRegions.length === 0) return Promise.resolve();
    setMsg("Saving…");
    var chain = Promise.resolve();
    if (Object.keys(values).length > 0) {
        chain = chain.then(function () {
            return api("/pages/" + pageId + "/regions", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ locale: cfg.locale, regions: values }),
            });
        });
    }
    secRegions.forEach(function (region) {
        chain = chain.then(function () {
            return api("/pages/" + pageId + "/sections", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ locale: cfg.locale, region: region, sections: collectSections(region) }),
            });
        });
    });
    return chain.then(function () {
        state.dirty = {};
        state.sectionsDirty = {};
        Object.keys(state.mceEditors).forEach(function (name) {
            state.mceEditors[name].setDirty(false);
        });
        state.sectionEditors.forEach(function (s) { s.ed.setDirty(false); });
        $("save").disabled = true;
        if (state.pageStatus === "published") state.hasUnpublished = true;
        flash("Draft saved");
        updateChip();
        updateBarButtons();
    });
}

function publish() {
    save().then(function () {
        setMsg("Publishing…");
        return api("/pages/" + pageId + "/publish", { method: "POST" });
    }).then(function () {
        state.pageStatus = "published";
        state.hasUnpublished = false;
        updateChip();
        // Publishing ends the session: leave edit mode so the page
        // reads the way visitors now see it. On failure (the catch
        // below) editing stays active with the work intact.
        if (state.editing) setEditing(false);
        flash("Published ✓");
        updateBarButtons();
    }).catch(function (err) { setMsg(err.message); });
}

// discardDraft throws away the saved-but-unpublished draft, reverting it
// to what's live. The page is reloaded afterwards so the visible content
// matches the restored (published) draft.
function discardDraft() {
    cmsConfirm("Discard your unpublished changes and go back to the published version of this page? This can't be undone.",
        "Discard draft", true).then(function (yes) {
        if (!yes) return;
        setMsg("Discarding…");
        api("/pages/" + pageId + "/discard", { method: "POST" }).then(function () {
            // Clear unsaved state so beforeunload doesn't second-guess the
            // reload, then reload to show the reverted content.
            state.dirty = {};
            state.sectionsDirty = {};
            window.location.reload();
        }).catch(function (err) { setMsg(err.message); });
    });
}

// The ⋯ menu holds rare and destructive actions. Any click elsewhere —
// including on a menu item, whose own handler runs first — closes it.
export function closeMore() {
    $("more-menu").classList.remove("on");
    $("more").setAttribute("aria-expanded", "false");
}

export function initSaving() {
    $("edit").addEventListener("click", function () { setEditing(!state.editing); });
    $("cancel").addEventListener("click", function () {
        var discard = function () {
            var snap = state.snapshot;
            setEditing(false); // tears down TinyMCE before the DOM is restored
            if (snap) restoreSnapshot(snap);
            state.dirty = {};
            state.sectionsDirty = {};
            state.imageValues = {};
            $("save").disabled = true;
            setMsg("");
            updateBarButtons();
        };
        if (hasUnsaved()) {
            cmsConfirm("Discard your unsaved changes? The page will go back to how it was before you started editing.",
                "Discard changes", true).then(function (yes) {
                if (yes) discard();
            });
        } else {
            discard();
        }
    });
    $("del-page").addEventListener("click", function () {
        cmsConfirm("Delete this page and everything on it? This cannot be undone.",
            "Delete page", true).then(function (yes) {
            if (!yes) return;
            setMsg("Deleting…");
            api("/pages/" + pageId, { method: "DELETE" }).then(function (body) {
                // Clear unsaved state so beforeunload doesn't second-guess
                // the navigation away from the now-deleted page.
                state.dirty = {};
                state.sectionsDirty = {};
                window.location.href = body.url || "/";
            }).catch(function (err) { setMsg(err.message); });
        });
    });
    $("save").addEventListener("click", function () {
        save().catch(function (err) { setMsg(err.message); });
    });
    $("publish").addEventListener("click", publish);
    $("discard").addEventListener("click", discardDraft);

    $("more").addEventListener("click", function (e) {
        e.stopPropagation();
        var open = !$("more-menu").classList.contains("on");
        $("more-menu").classList.toggle("on", open);
        $("more").setAttribute("aria-expanded", open ? "true" : "false");
    });
    document.addEventListener("click", closeMore);
}
