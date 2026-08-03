/* ------------------------------------------------------------------ *
 * Saving and publishing
 * ------------------------------------------------------------------ */

import { state, cfg, pageId } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { cmsConfirm, openDialog } from "./dialogs.js";
import { setEditing, restoreSnapshot, hasUnsaved, updateBarButtons } from "./editing.js";
import { hideButtonUI, hideSnipUI, hideImgUI } from "./buttons.js";
import { saveTitle } from "./title.js";

/* A save may make the server rebuild its generated Tailwind stylesheet
 * (classes typed into content get compiled CSS). This page's <link> was
 * rendered before the save, so poll briefly for a new stylesheet URL and
 * swap the link in place — otherwise freshly typed classes look
 * unstyled until the next reload. The rebuild is asynchronous, hence
 * the widening retry delays; an unchanged URL after all attempts just
 * means the save introduced no new classes. */
var cssPollTimer = null;
function refreshContentCSS() {
    var delays = [600, 1500, 3000, 6000];
    var attempt = 0;
    clearTimeout(cssPollTimer);
    var tick = function () {
        fetch("/cms/content-css", { cache: "no-store" }).then(function (res) {
            return res.ok ? res.json() : null;
        }).then(function (body) {
            if (!body || !body.href) return; // feature off, or no artifact yet
            var link = document.querySelector('link[href^="/cms/content-"]');
            if (link && link.getAttribute("href") === body.href) {
                if (attempt < delays.length) {
                    cssPollTimer = setTimeout(tick, delays[attempt++]);
                }
                return;
            }
            var fresh = document.createElement("link");
            fresh.rel = "stylesheet";
            // The old sheet leaves only once the new one has loaded, so
            // styling never flashes off.
            fresh.onload = function () { if (link) link.remove(); };
            fresh.href = body.href;
            document.head.appendChild(fresh);
        }).catch(function () { /* transient; the next save polls again */ });
    };
    tick();
}

// The chip has three states: draft (never/no longer live), Live
// (published and in sync), and "Unpublished edits" (live, but the saved
// draft differs — the state that makes drafts trustworthy). A private
// page carries a marker on top of whichever state it's in.
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
    if (state.visibility === "private") {
        chip.textContent += " · private";
        chip.title += ". Private: only logged-in users can see this page on the site.";
    }
}

// The ⋯ menu's visibility item always names the action, not the state.
function updateVisBtn() {
    $("vis-btn").textContent =
        state.visibility === "private" ? "Make page public…" : "Make page private…";
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
            corners: wrapper.dataset.cmsCorners || "",
            padding: wrapper.dataset.cmsPadding || "",
            height: wrapper.dataset.cmsHeight || "", valign: wrapper.dataset.cmsValign || "",
            bgcolor: wrapper.dataset.cmsBgcolor || "", bgimage: wrapper.dataset.cmsBgimage || "",
            bgposition: wrapper.dataset.cmsBgposition || "",
            html: html });
    });
    return out;
}

export function save() {
    var values = collect();
    var secRegions = Object.keys(state.sectionsDirty);
    var titleDirty = state.titleDirty;
    if (Object.keys(values).length === 0 && secRegions.length === 0 && !titleDirty) {
        return Promise.resolve();
    }
    setMsg("Saving…");
    var chain = Promise.resolve();
    // The title is page metadata, not a region, so it saves on its own
    // endpoint — first, so a failure there stops before content moves.
    if (titleDirty) chain = chain.then(saveTitle);
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
        // Saving reads as "done with that element" — clear any floating
        // gear/trash chrome (clicks on the bar deliberately keep it, so
        // it would otherwise linger).
        hideButtonUI();
        hideSnipUI();
        hideImgUI();
        refreshContentCSS();
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

// unpublish takes the page off the public site. Nothing is thrown away —
// the draft and published content both survive — so the page stays fully
// editable and Publish puts it back. No reload: only the page's status
// changes, not anything on screen.
function unpublish() {
    cmsConfirm("Take this page off the public site? Visitors will get a not-found page. Nothing is deleted — you can publish it again later.",
        "Unpublish", true).then(function (yes) {
        if (!yes) return;
        setMsg("Unpublishing…");
        api("/pages/" + pageId + "/unpublish", { method: "POST" }).then(function () {
            state.pageStatus = "draft";
            state.hasUnpublished = false;
            updateChip();
            flash("Unpublished — no longer on the site");
            updateBarButtons();
        }).catch(function (err) { setMsg(err.message); });
    });
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
            state.titleDirty = false;
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
    updateVisBtn();
    $("edit").addEventListener("click", function () { setEditing(!state.editing); });
    $("cancel").addEventListener("click", function () {
        var discard = function () {
            var snap = state.snapshot;
            setEditing(false); // tears down TinyMCE before the DOM is restored
            if (snap) restoreSnapshot(snap);
            state.dirty = {};
            state.sectionsDirty = {};
            state.imageValues = {};
            state.titleDirty = false;
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
                state.titleDirty = false;
                window.location.href = body.url || "/";
            }).catch(function (err) { setMsg(err.message); });
        });
    });
    $("dup-page").addEventListener("click", function () {
        openDialog({
            message: "Duplicate this page",
            prompt: true,
            placeholder: "Name for the new page",
            required: "Give the new page a name.",
            okLabel: "Duplicate",
        }).then(function (name) {
            // A field-less prompt resolves with the entered text itself,
            // or null when dismissed.
            if (!name) return;
            setMsg("Duplicating…");
            // Save first so the copy matches what's on screen, not the
            // last saved draft.
            var ready = hasUnsaved() ? save() : Promise.resolve();
            ready.then(function () {
                return api("/pages/" + pageId + "/duplicate", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ title: name, locale: cfg.locale }),
                });
            }).then(function (body) {
                // The duplicate is a draft, so only editors see it —
                // navigate straight into it to start writing.
                window.location.href = body.url;
            }).catch(function (err) { setMsg(err.message); });
        });
    });
    $("vis-btn").addEventListener("click", function () {
        var toPrivate = state.visibility !== "private";
        var msg = toPrivate
            ? "Make this page private? Only logged-in users will be able to see it on the site, even after publishing."
            : "Make this page public? Anyone will be able to see it once it's published.";
        cmsConfirm(msg, toPrivate ? "Make private" : "Make public").then(function (yes) {
            if (!yes) return;
            api("/pages/" + pageId + "/visibility", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ visibility: toPrivate ? "private" : "public" }),
            }).then(function (body) {
                state.visibility = body.visibility;
                updateVisBtn();
                updateChip();
                flash(toPrivate ? "Page is now private" : "Page is now public");
            }).catch(function (err) { setMsg(err.message); });
        });
    });
    $("save").addEventListener("click", function () {
        save().catch(function (err) { setMsg(err.message); });
    });
    $("publish").addEventListener("click", publish);
    $("unpublish").addEventListener("click", unpublish);
    $("discard").addEventListener("click", discardDraft);

    $("more").addEventListener("click", function (e) {
        e.stopPropagation();
        var open = !$("more-menu").classList.contains("on");
        $("more-menu").classList.toggle("on", open);
        $("more").setAttribute("aria-expanded", open ? "true" : "false");
    });
    document.addEventListener("click", closeMore);
}
