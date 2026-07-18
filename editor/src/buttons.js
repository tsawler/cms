/* ------------------------------------------------------------------ *
 * Button editor — clicking an a.cms-btn while editing shows floating
 * gear/trash chrome; the gear opens a settings dialog whose choices
 * are stored as inline styles on the link (sanitizer-approved).
 * Snippet blocks get similar chrome: a drag handle and a trash can.
 * ------------------------------------------------------------------ */

import { state, mediaEnabled } from "./state.js";
import { $, host } from "./shell.js";
import { cmsConfirm, openDialog } from "./dialogs.js";
import { markDirty, markSectionsDirty, hasUnsaved } from "./editing.js";
import { openPicker } from "./media.js";
import { unnestSnippets } from "./snippets.js";

var BTN_SIZES = {
    s: { padding: "6px 14px", fontSize: "13px" },
    m: { padding: "10px 20px", fontSize: "15px" },
    l: { padding: "14px 28px", fontSize: "18px" },
};
var activeBtn = null; // the a.cms-btn the chrome is attached to

function rgbToHex(v) {
    var m = /^rgb\((\d+),\s*(\d+),\s*(\d+)\)$/.exec(v || "");
    if (!m) return /^#[0-9a-fA-F]{6}$/.test(v || "") ? v : "";
    var h = function (n) { return ("0" + (+n).toString(16)).slice(-2); };
    return "#" + h(m[1]) + h(m[2]) + h(m[3]);
}

// lockButtons makes every button atomic inside the editors: no caret
// inside, no backspacing the label into plain text. Button text is
// edited through the gear dialog instead. The attribute is stripped
// again at serialization (see the contenteditable filter in richtext).
export function lockButtons() {
    document.querySelectorAll(
        "[data-cms-region] a.cms-btn, [data-cms-sections] a.cms-btn").forEach(function (b) {
        b.setAttribute("contenteditable", "false");
    });
}

function showButtonUI(btn) {
    activeBtn = btn;
    var ui = $("btn-ui");
    ui.classList.add("on");
    var r = btn.getBoundingClientRect();
    // Above the button; below it when that would collide with the
    // TinyMCE toolbar pinned to the top of the viewport.
    var top = r.top - 44;
    if (top < 64) top = r.bottom + 6;
    ui.style.top = top + "px";
    ui.style.left = Math.max(8, r.left) + "px";
}

export function hideButtonUI() {
    activeBtn = null;
    $("btn-ui").classList.remove("on");
}

/* Snippet blocks get their own floating chrome: a drag handle to
 * move the block and a trash can to delete it. */
var activeSnip = null;
var dragSnip = null; // the block being moved while its handle is dragged

function showSnipUI(el) {
    activeSnip = el;
    var ui = $("snip-ui");
    ui.classList.add("on");
    var r = el.getBoundingClientRect();
    var top = r.top - 44;
    if (top < 64) top = r.top + 8;
    ui.style.top = top + "px";
    ui.style.left = Math.max(8, r.left) + "px";
}

export function hideSnipUI() {
    activeSnip = null;
    $("snip-ui").classList.remove("on");
}

// findOwningEditor returns the TinyMCE instance managing the content
// that contains el, so button changes join that editor's undo stack.
export function findOwningEditor(el) {
    var all = [];
    Object.keys(state.mceEditors).forEach(function (k) { all.push(state.mceEditors[k]); });
    state.sectionEditors.forEach(function (s) { all.push(s.ed); });
    for (var i = 0; i < all.length; i++) {
        var target = all[i].getElement && all[i].getElement();
        if (target && target.contains(el)) return all[i];
    }
    return null;
}

function applyButtonSettings(btn, v) {
    var size = BTN_SIZES[v.size] ? v.size : "m";
    // An emptied text field keeps the current label rather than
    // producing an invisible button.
    var label = (v.label || "").trim();
    if (label && label !== (btn.textContent || "").trim()) btn.textContent = label;
    var url = (v.href || "").trim() || "#";
    btn.setAttribute("href", url);
    // TinyMCE shadows URI attributes like it shadows style; keep it
    // in sync or serialization restores the old address.
    btn.setAttribute("data-mce-href", url);
    if (v.newtab) {
        btn.setAttribute("target", "_blank");
        btn.setAttribute("rel", "noopener");
    } else {
        btn.removeAttribute("target");
        btn.removeAttribute("rel");
    }
    btn.style.backgroundColor = v.bgcolor || "";
    btn.style.color = v.textcolor || "";
    btn.style.border = v.outline ? "2px solid " + v.outline : "";
    btn.style.borderRadius = v.radius + "px";
    btn.style.padding = BTN_SIZES[size].padding;
    btn.style.fontSize = BTN_SIZES[size].fontSize;
    btn.setAttribute("data-cms-btn-size", size);
    // Keep TinyMCE's style shadow in sync or serialization reverts
    // this (same dance as the flexible-space snippet).
    btn.setAttribute("data-mce-style", btn.style.cssText);
}

function markContainerDirty(el) {
    var regionEl = el.closest("[data-cms-region]");
    if (regionEl) {
        markDirty(regionEl.getAttribute("data-cms-region"));
        return;
    }
    var container = el.closest("[data-cms-sections]");
    if (container) markSectionsDirty(container.getAttribute("data-cms-sections"));
}

// snipTwins counts blocks matching el's shape, so a move only removes
// the original when the dropped copy verifiably exists somewhere.
function snipTwins(el) {
    var sig = el.className + "|" + (el.textContent || "").replace(/\s+/g, " ").trim();
    var count = 0;
    document.querySelectorAll(".cms-snippet").forEach(function (s) {
        if (el.className === s.className &&
            sig === s.className + "|" + (s.textContent || "").replace(/\s+/g, " ").trim()) {
            count++;
        }
    });
    return count;
}

export function initButtons() {
    document.addEventListener("click", function (e) {
        if (!state.editing) return;
        if (e.target === host) return; // clicks on editor chrome keep the state
        var t = e.target;
        var btn = t.closest ? t.closest("a.cms-btn") : null;
        if (btn && !btn.closest("[data-cms-region],[data-cms-sections]")) btn = null;
        var snip = null;
        if (!btn && t.closest) {
            snip = t.closest(".cms-snippet");
            if (snip && !snip.closest("[data-cms-region],[data-cms-sections]")) snip = null;
        }
        if (btn) {
            e.preventDefault(); // never navigate while editing
            btn.setAttribute("contenteditable", "false"); // covers drag-dropped buttons
            hideSnipUI();
            showButtonUI(btn);
        } else if (snip) {
            // No preventDefault: the click still places the caret for
            // editing the snippet's text.
            hideButtonUI();
            showSnipUI(snip);
        } else {
            hideButtonUI();
            hideSnipUI();
        }
    }, true);

    // Keep the chrome glued to its element through scrolls and resizes.
    window.addEventListener("scroll", function () {
        if (activeBtn) showButtonUI(activeBtn);
        if (activeSnip) showSnipUI(activeSnip);
    }, true);
    window.addEventListener("resize", function () {
        if (activeBtn) showButtonUI(activeBtn);
        if (activeSnip) showSnipUI(activeSnip);
    });

    $("btn-set").addEventListener("click", function () {
        if (!activeBtn) return;
        var btn = activeBtn;
        var cs = window.getComputedStyle(btn);
        // Class-derived looks the preview falls back to when a color
        // field reads "None" (cleared).
        var baseBg = rgbToHex(cs.backgroundColor) || "#2563eb";
        var baseText = rgbToHex(cs.color) || "#ffffff";
        openDialog({
            message: "Button settings",
            okLabel: "Apply",
            tabs: ["Link", "Style"],
            previewTab: "Style",
            fields: [
                { id: "label", label: "Button text", type: "text", tab: "Link",
                    placeholder: "Button text", value: (btn.textContent || "").trim() },
                { id: "href", label: "Link address", type: "text", tab: "Link",
                    placeholder: "https://example.com or /contact",
                    value: btn.getAttribute("href") === "#" ? "" : (btn.getAttribute("href") || "") },
                { id: "newtab", label: "Open in a new tab", type: "check", tab: "Link",
                    value: btn.getAttribute("target") === "_blank" },
                { id: "bgcolor", label: "Background color", type: "color", tab: "Style",
                    value: rgbToHex(btn.style.backgroundColor) || baseBg },
                { id: "textcolor", label: "Text color", type: "color", tab: "Style",
                    value: rgbToHex(btn.style.color) || baseText },
                { id: "outline", label: "Outline color", type: "color", tab: "Style",
                    value: rgbToHex(btn.style.borderColor) },
                { id: "radius", label: "Corner roundness", type: "range", min: 0, max: 40, tab: "Style",
                    value: String(Math.min(40, parseInt(cs.borderRadius, 10) || 0)) },
                { id: "size", label: "Size", type: "select", tab: "Style",
                    value: btn.getAttribute("data-cms-btn-size") || "m",
                    options: [
                        { value: "s", label: "Small" },
                        { value: "m", label: "Medium" },
                        { value: "l", label: "Large" },
                    ] },
            ],
            // Live sample rendered with the current field values.
            preview: function (v, el) {
                el.innerHTML = "";
                var sample = document.createElement("span");
                sample.textContent = (v.label || "").trim() || "Button text";
                var sz = BTN_SIZES[v.size] || BTN_SIZES.m;
                sample.style.display = "inline-block";
                sample.style.fontWeight = "600";
                sample.style.lineHeight = "1.4";
                sample.style.padding = sz.padding;
                sample.style.fontSize = sz.fontSize;
                sample.style.borderRadius = (parseInt(v.radius, 10) || 0) + "px";
                sample.style.backgroundColor = v.bgcolor || baseBg;
                sample.style.color = v.textcolor || baseText;
                sample.style.border = v.outline ? "2px solid " + v.outline : "none";
                el.appendChild(sample);
            },
        }).then(function (v) {
            if (!v) return;
            var ed = findOwningEditor(btn);
            var run = function () { applyButtonSettings(btn, v); };
            if (ed) ed.undoManager.transact(run); else run();
            markContainerDirty(btn);
            if (activeBtn === btn) showButtonUI(btn); // re-anchor around the new size
        });
    });

    $("btn-del").addEventListener("click", function () {
        if (!activeBtn) return;
        var btn = activeBtn;
        cmsConfirm("Delete this button?", "Delete button", true).then(function (yes) {
            if (!yes) return;
            hideButtonUI();
            // Resolve the dirty target before the button leaves the DOM.
            var regionEl = btn.closest("[data-cms-region]");
            var sectionsEl = btn.closest("[data-cms-sections]");
            var ed = findOwningEditor(btn);
            // Run through the owning editor's undo stack so Cmd+Z can
            // still bring the button back after the confirm.
            var run = function () {
                var parent = btn.parentElement;
                btn.remove();
                // A button snippet's wrapper paragraph is scaffolding;
                // drop it once it holds nothing else.
                if (parent && parent.textContent.trim() === "" && !parent.querySelector("img,a")) {
                    parent.remove();
                }
            };
            if (ed) ed.undoManager.transact(run); else run();
            if (regionEl) markDirty(regionEl.getAttribute("data-cms-region"));
            else if (sectionsEl) markSectionsDirty(sectionsEl.getAttribute("data-cms-sections"));
        });
    });

    /* ---- snippet chrome actions: delete and drag-to-move ---- */

    $("snip-del").addEventListener("click", function () {
        if (!activeSnip) return;
        var el = activeSnip;
        cmsConfirm("Delete this block and its content?", "Delete block", true).then(function (yes) {
            if (!yes) return;
            hideSnipUI();
            var regionEl = el.closest("[data-cms-region]");
            var sectionsEl = el.closest("[data-cms-sections]");
            var ed = findOwningEditor(el);
            var run = function () { el.remove(); };
            if (ed) ed.undoManager.transact(run); else run();
            if (regionEl) markDirty(regionEl.getAttribute("data-cms-region"));
            else if (sectionsEl) markSectionsDirty(sectionsEl.getAttribute("data-cms-sections"));
        });
    });

    $("snip-move").addEventListener("dragstart", function (e) {
        if (!activeSnip) return;
        var el = activeSnip;
        dragSnip = el;
        e.dataTransfer.setData("text/html", el.outerHTML);
        e.dataTransfer.effectAllowed = "copyMove";
        try {
            e.dataTransfer.setDragImage(el, 20, 20);
        } catch (err) { /* not available outside a real drag session */ }
        // Hide the original once the drag image is captured, so the drop
        // caret can't land inside the block being moved. Guarded: if the
        // drag already ended, the element must not end up hidden forever.
        setTimeout(function () {
            if (dragSnip === el) el.style.display = "none";
        }, 0);
    });

    $("snip-move").addEventListener("dragend", function (e) {
        var el = dragSnip;
        dragSnip = null;
        if (!el) return;
        el.style.display = "";
        hideSnipUI();
        if (e.dataTransfer.dropEffect === "none") return; // cancelled drag
        // TinyMCE inserted a copy at the drop caret; remove the original
        // only if that copy is really there.
        if (snipTwins(el) < 2) return;
        var regionEl = el.closest("[data-cms-region]");
        var sectionsEl = el.closest("[data-cms-sections]");
        var ed = findOwningEditor(el);
        var run = function () { el.remove(); };
        if (ed) ed.undoManager.transact(run); else run();
        if (regionEl) markDirty(regionEl.getAttribute("data-cms-region"));
        else if (sectionsEl) markSectionsDirty(sectionsEl.getAttribute("data-cms-sections"));
        unnestSnippets(); // a drop inside another snippet lifts back out
        lockButtons(); // the moved copy may contain a button
    });

    document.addEventListener("click", function (e) {
        if (!state.editing || !mediaEnabled) return;
        var img = e.target.closest ? e.target.closest("[data-cms-image]") : null;
        if (!img) return;
        e.preventDefault();
        e.stopPropagation();
        var name = img.getAttribute("data-cms-image");
        openPicker("image", function (item) {
            img.src = item.web;
            if (item.alt) img.alt = item.alt;
            state.imageValues[name] = item.web;
            markDirty(name);
        });
    }, true);

    window.addEventListener("beforeunload", function (e) {
        if (hasUnsaved()) {
            e.preventDefault();
            e.returnValue = "";
        }
    });
}
