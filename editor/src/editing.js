/* ------------------------------------------------------------------ *
 * Editing state — entering/leaving edit mode, dirty tracking, and the
 * bar-button visibility rules that reflect it all.
 * ------------------------------------------------------------------ */

import { state, cfg, postInfo, locales, defaultLocale } from "./state.js";
import { $, ICONS } from "./shell.js";
import { setMsg } from "./util.js";
import { cmsPrompt } from "./dialogs.js";
import { loadTinyMCE, initRichEditors, removeRichEditors } from "./richtext.js";
import { lockButtons, hideButtonUI, hideSnipUI, hideImgUI, hideVidUI } from "./buttons.js";
import { closeDrawer } from "./snippets.js";
import { setMenuEditing } from "./menu.js";
import { injectSectionUI, reapplySectionClasses } from "./sections.js";

export function textRegions() {
    return Array.prototype.slice.call(
        document.querySelectorAll('[data-cms-region][data-cms-kind="text"]'));
}
export function htmlRegions() {
    return Array.prototype.slice.call(
        document.querySelectorAll('[data-cms-region][data-cms-kind="html"]'));
}

// takeSnapshot / restoreSnapshot let Cancel put the page back exactly
// as it was before editing started. Captured before TinyMCE attaches,
// so its DOM normalizations are rolled back too.
function takeSnapshot() {
    var regs = [];
    document.querySelectorAll("[data-cms-region]").forEach(function (el) {
        regs.push({ el: el, html: el.innerHTML });
    });
    var imgs = [];
    document.querySelectorAll("[data-cms-image]").forEach(function (el) {
        imgs.push({ el: el, src: el.getAttribute("src"), alt: el.getAttribute("alt") });
    });
    var secs = [];
    document.querySelectorAll("[data-cms-sections]").forEach(function (el) {
        secs.push({ el: el, html: el.innerHTML });
    });
    return { regs: regs, imgs: imgs, secs: secs };
}

export function restoreSnapshot(s) {
    s.regs.forEach(function (r) { r.el.innerHTML = r.html; });
    (s.secs || []).forEach(function (c) { c.el.innerHTML = c.html; });
    s.imgs.forEach(function (i) {
        if (i.src === null) i.el.removeAttribute("src");
        else i.el.setAttribute("src", i.src);
        if (i.alt === null) i.el.removeAttribute("alt");
        else i.el.setAttribute("alt", i.alt);
    });
}

export function setEditing(on) {
    state.editing = on;
    if (on) state.snapshot = takeSnapshot();
    document.body.classList.toggle("cms-editing", on);
    $("edit-ic").innerHTML = on ? ICONS.check : ICONS.pencil;
    $("edit-label").textContent = on ? "Done" : "Edit";
    $("edit").title = on ? "Finish editing" : "Edit this page in place";
    $("cancel").hidden = !on;
    // The post-settings gear shows only when editing a post's page.
    $("post-settings").hidden = !on || !postInfo;
    // Removing a translation only makes sense off the default locale.
    $("revert-locale").hidden = !on || locales.length < 2 || cfg.locale === defaultLocale;
    // The language chips matter while editing (the nav is the menu
    // editor then, so its own language links don't navigate); outside
    // edit mode the site's navigation handles language switching.
    $("locs").hidden = !on || locales.length < 2;
    // The home page (empty slug) is never deletable.
    $("del-page").hidden = !on || (cfg.slug || "") === "";
    // Title and meta description. A post keeps its own in the gear,
    // next to the date and images they belong with.
    $("meta-btn").hidden = !on || !!postInfo;
    // Posts are more than their backing page, so only plain pages
    // offer Duplicate.
    $("dup-page").hidden = !on || !!postInfo;
    // Visibility follows the same rule: posts belong to their feed's
    // listings, so they stay managed as a whole from the admin.
    $("vis-btn").hidden = !on || !!postInfo;
    // Untranslated regions (showing default-language fallback) get a
    // tooltip to go with their dashed amber outline.
    if (on) {
        document.querySelectorAll("[data-cms-fallback]").forEach(function (el) {
            el.title = "Not translated yet — showing the default language. Edit to translate.";
        });
    }
    $("rail").classList.toggle("on", on);
    setMenuEditing(on);
    if (!on) {
        closeDrawer();
        state.pendingSection = null;
    }
    updateBarButtons();
    textRegions().forEach(function (el) {
        if (on) {
            el.setAttribute("contenteditable", "plaintext-only");
            if (!el.isContentEditable) el.setAttribute("contenteditable", "true"); // Firefox
        } else {
            el.removeAttribute("contenteditable");
        }
    });
    if (on) {
        lockButtons(); // after the snapshot, so Cancel restores clean HTML
        setMsg("Loading editor…");
        loadTinyMCE().then(function () {
            initRichEditors();
            injectSectionUI();
            setMsg("");
        }).catch(function (err) { setMsg(err.message); });
    } else {
        hideButtonUI();
        hideSnipUI();
        hideImgUI();
        hideVidUI();
        removeRichEditors();
        reapplySectionClasses();
    }
}

// clearFallbackBadge drops a region's untranslated marker the moment it
// is edited — the edit is becoming this locale's own content.
function clearFallbackBadge(selector) {
    document.querySelectorAll(selector).forEach(function (el) {
        if (el.hasAttribute("data-cms-fallback")) {
            el.removeAttribute("data-cms-fallback");
            el.removeAttribute("title");
        }
    });
}

export function markDirty(name) {
    state.dirty[name] = true;
    clearFallbackBadge('[data-cms-region="' + CSS.escape(name) + '"]');
    $("save").disabled = false;
    updateBarButtons();
}

export function markSectionsDirty(region) {
    state.sectionsDirty[region] = true;
    clearFallbackBadge('[data-cms-sections="' + CSS.escape(region) + '"]');
    $("save").disabled = false;
    updateBarButtons();
}

export function hasUnsaved() {
    return Object.keys(state.dirty).length > 0 || Object.keys(state.sectionsDirty).length > 0;
}

// updateBarButtons keeps the edit bar honest about the page's state:
// while just viewing, Save is hidden, and Publish shows only when
// there is actually something publishable: a draft page (making it
// live is the primary action), saved-but-unpublished changes, or
// unsaved edits (Publish saves them first).
export function updateBarButtons() {
    var working = state.editing || hasUnsaved();
    $("save").hidden = !working;
    // The amber ring on Save is the unsaved-changes signal.
    $("save").classList.toggle("attn", hasUnsaved());
    $("publish").hidden = state.pageStatus === "published" && !state.hasUnpublished && !hasUnsaved();
    // Discard is offered whenever there's a saved draft that differs from
    // what's live — the same condition as the "Unpublished edits" chip.
    $("discard").hidden = !(state.pageStatus === "published" && state.hasUnpublished);
    // Unpublish is the counterpart to Publish: offered only while the page
    // is actually live, since that is the only state it changes.
    $("unpublish").hidden = state.pageStatus !== "published";
    // The separator only earns its place when the destructive group
    // above it has at least one visible item.
    $("menu-sep").hidden = $("cancel").hidden && $("discard").hidden && $("del-page").hidden &&
        $("revert-locale").hidden && $("unpublish").hidden;
}

export function initEditing() {
    document.addEventListener("input", function (e) {
        if (!state.editing) return;
        var el = e.target.closest ? e.target.closest('[data-cms-region][data-cms-kind="text"]') : null;
        if (el) markDirty(el.dataset.cmsRegion);
    });

    // Flexible-space snippets: click one while editing to set its height.
    document.addEventListener("click", function (e) {
        if (!state.editing) return;
        var sp = e.target.closest ? e.target.closest(".cms-spacer") : null;
        if (!sp) return;
        e.preventDefault();
        e.stopPropagation();
        var current = parseInt(sp.style.height, 10) || 48;
        cmsPrompt("Height of the space, in pixels", "e.g. 60", "Set height", String(current)).then(function (v) {
            if (v === null || v === "") return;
            var n = parseInt(v, 10);
            if (isNaN(n) || n < 4 || n > 800) {
                setMsg("Enter a height between 4 and 800 pixels.");
                return;
            }
            sp.style.height = n + "px";
            sp.setAttribute("data-height", n + "px");
            // TinyMCE restores style attributes from its shadow
            // data-mce-style at serialization, which would undo a direct
            // DOM change — keep it in sync so the new height saves.
            sp.setAttribute("data-mce-style", "height: " + n + "px;");
            var regionEl = sp.closest("[data-cms-region]");
            if (regionEl) {
                markDirty(regionEl.getAttribute("data-cms-region"));
                return;
            }
            var container = sp.closest("[data-cms-sections]");
            if (container) markSectionsDirty(container.getAttribute("data-cms-sections"));
        });
    }, true);
}
