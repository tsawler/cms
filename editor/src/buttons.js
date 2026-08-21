/* ------------------------------------------------------------------ *
 * Button editor — clicking an a.cms-btn while editing shows floating
 * gear/trash chrome; the gear opens a settings dialog whose choices
 * are stored as inline styles on the link (sanitizer-approved).
 * Snippet blocks get similar chrome: a drag handle and a trash can.
 * Images embedded in rich-text content get a gear too: alt text,
 * an optional link, and a display width, stored as attributes and
 * classes (sanitizer-approved).
 * ------------------------------------------------------------------ */

import { state, mediaEnabled } from "./state.js";
import { $, host } from "./shell.js";
import { cmsConfirm, openDialog, refreshDialog } from "./dialogs.js";
import { markDirty, markSectionsDirty, hasUnsaved } from "./editing.js";
import { openPicker } from "./media.js";
import { unnestSnippets } from "./snippets.js";
import { chooseVideoInto } from "./videos.js";
import { chooseMapInto, isMapEmbed } from "./maps.js";
import { openSource, elementSource } from "./source.js";
import { openCodeEditor } from "./code.js";
import {
    columnTarget, addColumn, duplicateColumn, confirmRemove, removeColumn,
    resizeColumn, moveColumn, splitIntoColumns, duplicateBeside,
} from "./columns.js";
import { copyOf } from "./clone.js";
import { findOwningEditor, runWithUndo } from "./undo.js";
import { flash } from "./util.js";

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
 * move the block, a gear for block settings, and a trash can. */
var activeSnip = null;
var dragSnip = null; // the block being moved while its handle is dragged

// The block gear's spacing presets: curated combinations rather than
// free-form numbers, so restyled blocks stay consistent across a site.
// Padding shapes match cssPaddingRe in the server's sanitize policy.
var SNIP_SPACING = {
    compact: { padding: "10px 14px", margin: "8px" },
    normal: { padding: "24px", margin: "24px" },
    roomy: { padding: "40px", margin: "40px" },
};

// snipDark mirrors the section preview's contrast check, for tinting
// the block preview's placeholder lines against the chosen background.
function snipDark(hex) {
    if (!/^#[0-9a-fA-F]{6}$/.test(hex || "")) return false;
    var r = parseInt(hex.slice(1, 3), 16), g = parseInt(hex.slice(3, 5), 16),
        b = parseInt(hex.slice(5, 7), 16);
    return 0.299 * r + 0.587 * g + 0.114 * b < 140;
}

// syncStyleShadow keeps TinyMCE's style shadow in step with an element's
// real style attribute, or serialization reverts the change (same dance
// as the button gear).
function syncStyleShadow(el) {
    if (el.style.cssText) {
        el.setAttribute("data-mce-style", el.style.cssText);
    } else {
        el.removeAttribute("style");
        el.removeAttribute("data-mce-style");
    }
}

// syncDescendantColors makes a chosen text color actually take: children
// whose own classes set a color would ignore the color inherited from
// the block root, so they're pinned to color:inherit while an override
// is active — and released when it's cleared. Buttons keep their own
// colors (they have their own gear), as does any hand-set inline color.
// Top-down document order matters: once an element inherits, everything
// under it recomputes before it's compared.
function syncDescendantColors(el, forced) {
    el.querySelectorAll("*").forEach(function (n) {
        if (n.closest(".cms-btn")) return;
        if (forced) {
            if (n.style.color && n.style.color !== "inherit") return;
            if (getComputedStyle(n).color === getComputedStyle(n.parentElement).color) return;
            n.style.color = "inherit";
        } else if (n.style.color === "inherit") {
            n.style.color = "";
        } else {
            return; // untouched by the gear — leave its shadow alone
        }
        syncStyleShadow(n);
    });
}

// classRadius reports the corner rounding el would have from its classes
// alone, in pixels — the value to treat as "as designed".
//
// It has to be measured rather than read, because the gear's own inline
// style is what getComputedStyle would otherwise report. So the override
// is lifted for the length of one measurement and put straight back;
// nothing is painted in between, since layout is read synchronously.
//
// A block with different corners (rounded-t-lg, say) computes to a list
// like "8px 8px 0px 0px" and parseInt keeps the first — which is the
// same number the dialog's slider shows, so baseline and choice are
// compared on equal terms and an untouched slider still writes nothing.
function classRadius(el) {
    var override = el.style.borderRadius;
    if (override) el.style.borderRadius = "";
    var base = parseInt(getComputedStyle(el).borderRadius, 10) || 0;
    if (override) el.style.borderRadius = override;
    return base;
}

// applySnippetSettings writes the gear's choices as inline styles on the
// block root (they beat whatever classes the snippet came with, and are
// saved as part of the content — no schema anywhere), plus a data
// attribute so the dialog can re-select the spacing preset later.
//
// baseRadius is the block's class-derived rounding (see classRadius),
// measured before the dialog opened.
function applySnippetSettings(el, v, baseRadius) {
    el.style.backgroundColor = v.bgcolor || "";
    el.style.color = v.textcolor || "";
    syncDescendantColors(el, !!v.textcolor);
    var sp = SNIP_SPACING[v.spacing];
    el.style.padding = sp ? sp.padding : "";
    el.style.marginTop = sp ? sp.margin : "";
    el.style.marginBottom = sp ? sp.margin : "";
    if (sp) el.setAttribute("data-cms-snip-spacing", v.spacing);
    else el.removeAttribute("data-cms-snip-spacing");
    // Only a radius that differs from the block's own classes is worth
    // storing. Writing one that matches would pin a class-derived value
    // as a pixel override — so a later restyle of that class, or an edit
    // to the snippet's markup, would stop reaching this block — and
    // would leave "border-radius: 0px" on every unrounded block that
    // ever had its colour changed.
    var radius = parseInt(v.radius, 10) || 0;
    el.style.borderRadius = radius === baseRadius ? "" : radius + "px";
    syncStyleShadow(el);
}

// blockSibling returns what sits directly above or below `el` in the
// stack it belongs to — the thing the move arrows swap it with. Any
// element counts, not only another block: a region may hold loose
// paragraphs someone typed between two snippets, and stepping over one
// of those in a single press is what "move this block up" plainly
// means. What does not count is furniture: TinyMCE parks bogus <br>s
// and placeholders wherever it is about to put a caret, and the editor
// injects its own chrome into the page marked data-cms-ui. Neither is
// something to trade places with — they are the same two things
// elementSource strips before showing anyone a block's HTML.
function blockSibling(el, dir) {
    var n = dir < 0 ? el.previousElementSibling : el.nextElementSibling;
    while (n && (n.tagName === "BR" || n.hasAttribute("data-mce-bogus") ||
                 n.hasAttribute("data-cms-ui"))) {
        n = dir < 0 ? n.previousElementSibling : n.nextElementSibling;
    }
    return n;
}

function showSnipUI(el) {
    activeSnip = el;
    var ui = $("snip-ui");
    ui.classList.add("on");
    // An arrow with nothing on that side to swap with would be a button
    // that does nothing, so it stays away — the same rule the column
    // tool's moves follow. Recomputed on every anchor, which is also
    // every scroll; two sibling walks are nothing next to the reflow
    // getBoundingClientRect already costs.
    $("snip-up").hidden = !blockSibling(el, -1);
    $("snip-down").hidden = !blockSibling(el, 1);
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

// moveBlock trades a block with its neighbour above or below. Sections
// have had this since they existed (the up/down buttons in sections.js);
// blocks have had only the drag handle, which is the same verb done
// vaguely: a drag travels through TinyMCE's drop caret and can land a
// block *inside* the one it was aimed past, which is why the handle's
// dragend has to unnest and count twins afterwards. Swapping two
// siblings has none of that to go wrong.
function moveBlock(dir) {
    if (!activeSnip) return;
    var el = activeSnip;
    var other = blockSibling(el, dir);
    if (!other) return;
    var ed = findOwningEditor(el);
    runWithUndo(ed, function () {
        if (dir < 0) el.parentNode.insertBefore(el, other);
        else el.parentNode.insertBefore(other, el);
    });
    markContainerDirty(el);
    // Both halves of re-anchoring matter. The block is at a new height,
    // and it may have gone off-screen entirely if the neighbour it
    // passed is a tall one — so bring it back into view first, then
    // measure, or the chrome is placed against where the block was
    // rather than where it now is. scrollIntoView with "nearest" moves
    // nothing when the block is already visible, which is the usual case.
    el.scrollIntoView({ block: "nearest" });
    showSnipUI(el);
    placeColUI(); // the column tool rides on the block and moved with it
}

// duplicateBlock puts a copy of a block directly above or below it.
//
// Both directions are offered rather than one plus the move arrows,
// which is what ContentBuilder does and what this toolbar could have
// done for one button less. Duplicating is the commonest edit there is
// — three more of this card, another row like that one — and it almost
// always has a side in mind; making half of those a duplicate followed
// by a move is a worse trade than a button.
//
// The chrome follows the copy, not the original. The copy is what was
// just made, so it is what is about to be typed into or moved, and
// leaving the chrome behind on the original would make the page look
// unchanged at the one moment it is not. addColumn already works this
// way with the column it returns.
function duplicateBlock(dir) {
    if (!activeSnip) return;
    var el = activeSnip;
    var copy = copyOf(el);
    var ed = findOwningEditor(el);
    runWithUndo(ed, function () {
        el.parentNode.insertBefore(copy, dir < 0 ? el : el.nextSibling);
    });
    markContainerDirty(copy);
    lockButtons(); // the copy may carry a button
    copy.scrollIntoView({ block: "nearest" });
    showSnipUI(copy);
    // No column is selected in a block nobody has clicked into yet, so
    // this resolves to nothing and puts the column tool away; the next
    // click inside the copy raises it against the cell that was meant.
    showColUI(copy, copy);
    flash("Block duplicated");
}

/* Column chrome. A block that is a row of columns gets a second, smaller
 * toolbar on top of the block chrome, anchored to the column that was
 * clicked: everything on it acts on that one column. A block that is
 * not a row yet gets the same toolbar showing only the edits that could
 * make it one — ＋ to split it in half, and the duplicate pair to stand
 * it beside a copy of itself — those being the only ways a plain block
 * ever becomes a row.
 *
 * The pair is deliberate. The block chrome moves, restyles, and deletes
 * the whole thing; this reshapes what is inside it. Which one someone
 * wants is answered by which one they reach for, not by a mode. */
var activeCol = null; // the columnTarget the chrome is attached to

// placeColUI straddles the row's top border — half above, half below —
// and centres the pill over the column it acts on.
//
// Both halves of that are load-bearing. Sitting *on* the boundary means
// it never covers the first line of the column, which anchoring inside
// the cell did; it is the same idiom the section toolbar already uses
// (.cms-sec-ui in light.css), so a toolbar on an edge consistently
// reads as belonging to the container it is drawn on. Centring on the
// column rather than its left edge is what makes "this one" obvious in
// a row of identical cells, and it keeps the pill clear of the block
// chrome, which is left-aligned above the block.
//
// The top is the *row's*, not the cell's: cells rarely start at the same
// height (a short one, a photo above text), and a toolbar that stepped
// up and down as columns were clicked would read as belonging to the
// content rather than to the row.
function placeColUI() {
    if (!activeCol) return;
    var ui = $("col-ui");
    var cell = activeCol.mode === "cell" ? activeCol.cell : activeCol.block;
    var row = activeCol.mode === "cell" ? activeCol.row : activeCol.block;
    // Measured, not assumed: the pill's width changes with how many of
    // its buttons this column can use.
    var box = ui.getBoundingClientRect();
    var r = cell.getBoundingClientRect();
    var top = row.getBoundingClientRect().top - box.height / 2;
    // TinyMCE's toolbar is pinned to the top of the viewport; never
    // slide underneath it.
    if (top < 64) top = 64;
    var left = r.left + (r.width - box.width) / 2;
    ui.style.top = top + "px";
    ui.style.left = Math.max(8, Math.min(left, window.innerWidth - box.width - 8)) + "px";
}

// markActiveCell tints the one column every button on the toolbar acts
// on. The class is stripped again at serialization time along with the
// rest of the editor's marks (see clearColMarks) — it describes what is
// selected right now, and nothing about it belongs in saved content.
function markActiveCell(cell) {
    document.querySelectorAll(".cms-col-active").forEach(function (el) {
        if (el === cell) return;
        el.classList.remove("cms-col-active");
        // classList.remove leaves class="" behind on a cell whose only
        // class was the mark, and an empty attribute would go on to be
        // saved as content.
        if (!el.getAttribute("class")) el.removeAttribute("class");
    });
    if (cell) cell.classList.add("cms-col-active");
}

function showColUI(block, target) {
    var info = columnTarget(block, target);
    if (!info) {
        hideColUI();
        return;
    }
    activeCol = info;
    markActiveCell(info.mode === "cell" ? info.cell : null);
    var cell = info.mode === "cell";
    $("col-back").hidden = !cell || !info.canMoveBack;
    $("col-on").hidden = !cell || !info.canMoveOn;
    $("col-narrow").hidden = !cell || !info.canResize;
    $("col-wide").hidden = !cell || !info.canResize;
    $("col-del").hidden = !cell;
    // The duplicate pair is the one thing on this toolbar that means
    // something in both modes, and it is the same sentence either way —
    // "put another of this beside it". In a row that is a copied column
    // and it goes when the row is full, the same gate ＋ uses; on a
    // block that is not a row yet it is the block itself, copied into a
    // fresh two-column row, and the only requirement is that there be
    // something in the block to copy.
    var canDup = cell ? info.canAdd : info.canPair;
    $("col-dup-back").hidden = !canDup;
    $("col-dup-on").hidden = !canDup;
    $("col-dup-back").title = cell
        ? "Duplicate this column to the left"
        : "Duplicate this block to the left";
    $("col-dup-on").title = cell
        ? "Duplicate this column to the right"
        : "Duplicate this block to the right";
    $("col-add").hidden = cell ? !info.canAdd : !info.canSplit;
    $("col-add").title = cell ? "Add a column" : "Split into two columns";
    // Everything can be unavailable at once: a full row of one column
    // offers only Remove, and a row already at the maximum offers only
    // the moves. An empty pill would be a puzzle, so it stays away.
    var any = ["col-back", "col-on", "col-narrow", "col-wide",
        "col-dup-back", "col-dup-on", "col-add", "col-del"]
        .some(function (id) { return !$(id).hidden; });
    if (!any) {
        hideColUI();
        return;
    }
    $("col-ui").classList.add("on");
    placeColUI();
}

export function hideColUI() {
    activeCol = null;
    markActiveCell(null);
    $("col-ui").classList.remove("on");
}

// afterColumnEdit puts everything back in step once a column edit has
// landed: the block may have changed identity (a split paragraph grows a
// row that becomes the block), the chrome has to re-measure around new
// geometry, and the container needs marking dirty so the change can be
// saved. `now` is the element the block is afterwards, or null when the
// last column went and took the block with it — which is exactly why the
// container is passed in rather than looked up from the block: a removed
// element has no ancestors left to find it by.
function afterColumnEdit(container, now, target) {
    lockButtons(); // a cloned or moved column may carry a button
    if (container) markContainerDirty(container);
    if (!now) {
        hideChrome();
        return;
    }
    activeSnip = now;
    showSnipUI(now);
    showColUI(now, target || now);
}

// runColumnEdit is the shape every column button shares: make the change
// inside the owning editor's undo transaction, then re-anchor. The
// callback may return the block afterwards and the element the chrome
// should re-resolve its column from; anything it leaves out is unchanged.
function runColumnEdit(edit) {
    if (!activeCol) return;
    var info = activeCol;
    var block = activeSnip;
    var anchor = info.mode === "cell" ? info.cell : info.block;
    var container = anchor.closest("[data-cms-region],[data-cms-sections]");
    var ed = findOwningEditor(anchor);
    var now = block;
    var target = info.mode === "cell" ? info.cell : null;
    runWithUndo(ed, function () {
        var out = edit(info) || {};
        if ("block" in out) now = out.block;
        if ("target" in out) target = out.target;
    });
    afterColumnEdit(container, now, target);
}

/* Embedded images get a gear (alt text, caption, link, rendition, and
 * style presets) and a trash can, anchored to the image's top-right
 * corner like the section toolbar. */
var activeImg = null;

// IMG_SIZES are the gear's display-width presets. Width classes need
// h-auto alongside them: TinyMCE stamps width/height attributes on
// resize, and a CSS width with an attribute height would distort.
var IMG_SIZES = ["w-full h-auto", "w-2/3 h-auto", "w-1/2 h-auto", "w-1/3 h-auto"];
var IMG_ROUND = ["rounded-lg", "rounded-2xl", "rounded-full"];
// CMS-owned shadow classes (styled by imgShadowCSS in render.go, shipped
// via cmsHead). Tailwind's own shadow scale is 10–25% black — too faint
// to read as a shadow next to a photo — and its classes would need
// safelisting in every host build.
var IMG_SHADOW = ["cms-shadow-subtle", "cms-shadow-strong"];
// Every shadow class the gear has ever applied, so changing the preset
// list above can't strand an old class on saved content.
var IMG_SHADOW_ALL = ["shadow-md", "shadow-lg", "shadow-xl", "shadow-2xl",
    "cms-shadow-subtle", "cms-shadow-strong"];

// Inline-style equivalents of the presets, for the settings dialog's
// live preview: the dialog lives in the editor's shadow DOM, where the
// host page's stylesheet (and Tailwind) can't reach, so the preview
// can't just apply the classes. Shadow values mirror imgShadowCSS in
// render.go; rounded-full is 9999px to match Tailwind (pill, not
// ellipse, on non-square images).
var IMG_PREVIEW_SHADOW = {
    "cms-shadow-subtle": "0 4px 12px rgba(0,0,0,.2),0 2px 4px rgba(0,0,0,.14)",
    "cms-shadow-strong": "0 16px 40px rgba(0,0,0,.34),0 4px 12px rgba(0,0,0,.22)",
};
var IMG_PREVIEW_ROUND = { "rounded-lg": "8px", "rounded-2xl": "16px", "rounded-full": "9999px" };
var IMG_PREVIEW_FRACTION = {};
IMG_PREVIEW_FRACTION[IMG_SIZES[0]] = 1;
IMG_PREVIEW_FRACTION[IMG_SIZES[1]] = 2 / 3;
IMG_PREVIEW_FRACTION[IMG_SIZES[2]] = 1 / 2;
IMG_PREVIEW_FRACTION[IMG_SIZES[3]] = 1 / 3;

// imgPreviewSrc is the address the dialog's preview should draw: a file
// chosen in the dialog wins over the one on the page, since the preview
// shows what applying would produce.
function imgPreviewSrc(img, v) {
    if (v.file_web || v.file_orig) {
        return v.orig === "1" ? (v.file_orig || v.file_web) : (v.file_web || v.file_orig);
    }
    return v.orig === "1"
        ? (img.getAttribute("data-cms-orig") || img.getAttribute("src"))
        : (img.getAttribute("data-cms-web") || img.getAttribute("src"));
}

// Measured proportions, keyed by address: a file chosen in the dialog
// has none until the browser has loaded it, so the first draw guesses
// and the load refreshes the dialog with the real shape.
var imgRatios = {};

// renderImgPreview draws the dialog's live preview: the image at its
// chosen width on a white stand-in "page", with roundness, shadow,
// rendition, and caption applied. Scaled down as needed so a tall
// image can't blow up the dialog.
function renderImgPreview(img, v, el) {
    el.innerHTML = "";
    var src = imgPreviewSrc(img, v);
    var ar = imgRatios[src] ||
        (!v.file_web && !v.file_orig && img.naturalWidth && img.naturalHeight
            ? img.naturalWidth / img.naturalHeight : 4 / 3);
    var pagePad = 16;
    var avail = (el.clientWidth || 320) - 2 * pagePad;
    var f = IMG_PREVIEW_FRACTION[v.size] || 0;
    var w = f ? f * avail : Math.min(img.naturalWidth || avail, avail);
    // Cap the preview's height by shrinking the whole stand-in page,
    // so the chosen width fraction stays visually truthful.
    var scale = Math.min(1, 170 / (w / ar));
    var page = document.createElement("div");
    page.style.cssText = "background:#fff;border:1px solid #e3e6ea;border-radius:4px;" +
        "display:inline-block;width:" + Math.round(avail * scale + 2 * pagePad) + "px;" +
        "padding:" + pagePad + "px;text-align:center";
    var pimg = document.createElement("img");
    pimg.src = src;
    pimg.addEventListener("load", function () {
        if (!pimg.naturalWidth || !pimg.naturalHeight) return;
        var r = pimg.naturalWidth / pimg.naturalHeight;
        if (imgRatios[src] === r) return; // already drawn at this shape
        imgRatios[src] = r;
        refreshDialog();
    });
    pimg.style.cssText = "width:" + Math.round(w * scale) + "px;height:auto;" +
        "display:inline-block;vertical-align:top;" +
        "border-radius:" + (IMG_PREVIEW_ROUND[v.round] || "0") + ";" +
        (IMG_PREVIEW_SHADOW[v.shadow] ? "box-shadow:" + IMG_PREVIEW_SHADOW[v.shadow] + ";" : "");
    page.appendChild(pimg);
    var caption = (v.caption || "").trim();
    if (caption) {
        var cap = document.createElement("div");
        cap.textContent = caption;
        cap.style.cssText = "font:italic 11px system-ui,sans-serif;color:#667085;margin-top:8px";
        page.appendChild(cap);
    }
    el.appendChild(page);
}

function showImgUI(img) {
    activeImg = img;
    var ui = $("img-ui");
    ui.classList.add("on");
    var r = img.getBoundingClientRect();
    // Top-right corner of the image, mirroring the section toolbar;
    // above it when the image is too small to hold the chrome.
    var top = r.top + 8;
    if (r.height < 56 || r.width < ui.offsetWidth + 16) top = r.top - 44;
    if (top < 64) top = r.bottom + 6;
    ui.style.top = top + "px";
    ui.style.left = Math.max(8, r.right - ui.offsetWidth - 8) + "px";
}

export function hideImgUI() {
    activeImg = null;
    $("img-ui").classList.remove("on");
}

/* Videos and external embeds in rich content get a gear (swap the
 * source: another upload or a different YouTube/Vimeo link) and a trash
 * can, anchored like the image chrome. */
var activeVid = null;

// mediaAtPoint finds the video or embed under a click. While editing
// these are pointer-events:none (embeds can't swallow clicks, players
// don't start mid-edit) — which also means they can never be the event
// target, so hit-test their rectangles instead.
function mediaAtPoint(x, y) {
    var els = document.querySelectorAll(
        "[data-cms-region] video, [data-cms-region] iframe," +
        "[data-cms-sections] video, [data-cms-sections] iframe");
    for (var i = 0; i < els.length; i++) {
        var r = els[i].getBoundingClientRect();
        if (x >= r.left && x <= r.right && y >= r.top && y <= r.bottom) return els[i];
    }
    return null;
}

function showVidUI(vid) {
    activeVid = vid;
    var ui = $("vid-ui");
    // The same chrome serves video embeds and maps; say the right word.
    var map = isMapEmbed(vid);
    $("vid-set").title = map ? "Change this map" : "Change this video";
    $("vid-del").title = map ? "Delete map" : "Delete video";
    ui.classList.add("on");
    var r = vid.getBoundingClientRect();
    var top = r.top + 8;
    if (r.height < 56 || r.width < ui.offsetWidth + 16) top = r.top - 44;
    if (top < 64) top = r.bottom + 6;
    ui.style.top = top + "px";
    ui.style.left = Math.max(8, r.right - ui.offsetWidth - 8) + "px";
}

export function hideVidUI() {
    activeVid = null;
    $("vid-ui").classList.remove("on");
}

/* Template image slots — an <img data-cms-image="name"> the host's
 * template put there — get a pencil (choose a picture from the media
 * library) and, when the slot holds one, a trash can.
 *
 * They used to open the picker on the click itself, and that was the one
 * editable thing on a page that answered a click with a modal. A slot is
 * often the whole of something large — a card, a banner, a tile — so any
 * stray click landed in it, and a slot inside a link (they usually are)
 * had no reading of a click other than "open the library". Chrome first,
 * dialog second, is what every other element here does. */
var activeSlot = null;

/* slotRendition is which size of the chosen picture a slot gets.
 *
 * The default is "web", the full-width rung, because a slot is most often
 * the banner across the top of a page and that is the size it wants. A
 * template whose slot is smaller than that — a card, a tile three across
 * a grid — says so on the element:
 *
 *     <img data-cms-image="lot-image-atvs" data-cms-rendition="card">
 *
 * and the picker stores that rung's URL instead, so the page holds the
 * size it will actually display rather than one the browser has to
 * shrink. The value is a rung of the ladder ("web", "card", "thumb"); an
 * unknown name, or one the item has no URL for — a vector, a document, a
 * video, none of which have a card — falls back to "web", which every
 * item has.
 *
 * It is read at pick time rather than stored anywhere: the attribute
 * belongs to the template, so a template that changes its mind about the
 * size applies to the next picture chosen, and the ones already there
 * keep working. */
function slotRendition(img, item) {
    var rung = img.getAttribute("data-cms-rendition");
    return (rung && item[rung]) || item.web;
}

function showSlotUI(img) {
    activeSlot = img;
    var ui = $("slot-ui");
    // Removing is offered only when there is something to remove: an
    // untouched slot showing the template's own picture would otherwise
    // have a trash can that does nothing visible.
    var name = img.getAttribute("data-cms-image");
    $("slot-clear").hidden = !state.filledSlots[name] && !state.imageValues[name];
    ui.classList.add("on");
    var r = img.getBoundingClientRect();
    // Top-right corner, like the image and video chrome; above the slot
    // when it is too small to hold the toolbar inside it.
    var top = r.top + 8;
    if (r.height < 56 || r.width < ui.offsetWidth + 16) top = r.top - 44;
    if (top < 64) top = r.bottom + 6;
    ui.style.top = top + "px";
    ui.style.left = Math.max(8, r.right - ui.offsetWidth - 8) + "px";
}

export function hideSlotUI() {
    activeSlot = null;
    $("slot-ui").classList.remove("on");
}

// hideChrome puts away every floating toolbar except the one named, so a
// click that raises one is also the click that dismisses the others.
// Callers outside this module pass nothing, meaning "all of it".
export function hideChrome(except) {
    if (except !== "btn") hideButtonUI();
    // Column chrome belongs to the block chrome: it is raised by the same
    // click and never outlives it.
    if (except !== "snip") { hideSnipUI(); hideColUI(); }
    if (except !== "img") hideImgUI();
    if (except !== "vid") hideVidUI();
    if (except !== "slot") hideSlotUI();
}

// imageLink returns the <a> wrapping img when that anchor exists purely
// for the image (no text of its own), else null.
function imageLink(img) {
    var a = img.parentElement;
    if (!a || a.tagName !== "A" || a.classList.contains("cms-btn")) return null;
    return (a.textContent || "").trim() === "" ? a : null;
}

// imageFigure returns the <figure> wrapping img when that figure exists
// purely for the image — nothing but the (possibly linked) image and an
// optional figcaption. Snippet figures with their own text don't count.
function imageFigure(img) {
    var fig = img.closest ? img.closest("figure") : null;
    if (!fig || fig.querySelector("img") !== img) return null;
    var fc = fig.querySelector("figcaption");
    var text = fig.textContent || "";
    if (fc) text = text.replace(fc.textContent || "", "");
    return text.trim() === "" ? fig : null;
}

function imgClassValue(img, presets) {
    for (var i = 0; i < presets.length; i++) {
        if (img.classList.contains(presets[i].split(" ")[0])) return presets[i];
    }
    return "";
}

// imgShadowValue maps legacy Tailwind shadow classes to the nearest
// current preset, so re-opening older content shows Subtle/Strong, not
// None (and Apply upgrades the class).
function imgShadowValue(img) {
    var v = imgClassValue(img, IMG_SHADOW_ALL);
    if (v === "shadow-md" || v === "shadow-lg") return IMG_SHADOW[0];
    if (v === "shadow-xl" || v === "shadow-2xl") return IMG_SHADOW[1];
    return v;
}

// swapClasses clears every class the preset list can apply, then adds
// the chosen preset's classes.
function swapClasses(el, presets, chosen) {
    presets.forEach(function (s) {
        s.split(" ").forEach(function (c) { el.classList.remove(c); });
    });
    if (chosen) {
        chosen.split(" ").forEach(function (c) { el.classList.add(c); });
    }
}

function applyImageSettings(img, v) {
    // Empty alt is valid (it marks the image decorative), so the
    // attribute is always written rather than removed.
    img.setAttribute("alt", (v.alt || "").trim());
    // Everything below the fold loads lazily; applying settings also
    // upgrades images inserted before this attribute existed.
    img.setAttribute("loading", "lazy");

    // A different file chosen in the dialog. Both renditions are
    // replaced together, so the rendition switch below still has both
    // addresses to pick from, and TinyMCE's resize attributes go with
    // the old file — the new one has its own proportions, and a stale
    // width/height pair would distort it.
    if (v.file_web || v.file_orig) {
        img.setAttribute("data-cms-web", v.file_web || v.file_orig);
        img.setAttribute("data-cms-orig", v.file_orig || v.file_web);
        img.removeAttribute("width");
        img.removeAttribute("height");
    }

    swapClasses(img, IMG_SIZES, v.size);
    swapClasses(img, IMG_ROUND, v.round);
    swapClasses(img, IMG_SHADOW_ALL, v.shadow);
    if (!img.getAttribute("class")) img.removeAttribute("class");

    // Rendition: swap between the compressed web variant and the
    // full-quality original when both URLs are known (stored on the
    // image at insert time).
    var origURL = img.getAttribute("data-cms-orig");
    var webURL = img.getAttribute("data-cms-web");
    if (origURL && webURL) {
        var src = v.orig === "1" ? origURL : webURL;
        img.setAttribute("src", src);
        // TinyMCE shadows URI attributes; keep the shadow in sync or
        // serialization restores the old address.
        img.setAttribute("data-mce-src", src);
    }

    var url = (v.href || "").trim();
    var link = imageLink(img);
    if (url) {
        if (!link) {
            link = document.createElement("a");
            img.parentNode.insertBefore(link, img);
            link.appendChild(img);
        }
        link.setAttribute("href", url);
        link.setAttribute("data-mce-href", url);
        if (v.newtab) {
            link.setAttribute("target", "_blank");
            link.setAttribute("rel", "noopener");
        } else {
            link.removeAttribute("target");
            link.removeAttribute("rel");
        }
    } else if (link) {
        link.parentNode.insertBefore(img, link);
        link.remove();
    }

    // Caption: a <figure> around the (possibly linked) image with a
    // <figcaption>. Recomputed after the link work so the figure wraps
    // whatever the outermost image node now is.
    var node = imageLink(img) || img;
    var fig = imageFigure(img);
    var caption = (v.caption || "").trim();
    if (caption) {
        if (!fig) {
            fig = document.createElement("figure");
            var p = node.parentElement;
            if (p && p.tagName === "P") {
                // A figure may not live inside a paragraph — the
                // browser's parser would split it on the next load.
                p.parentNode.insertBefore(fig, p.nextSibling);
                fig.appendChild(node);
                if ((p.textContent || "").trim() === "" && !p.querySelector("img,a,br")) p.remove();
            } else {
                node.parentNode.insertBefore(fig, node);
                fig.appendChild(node);
            }
        }
        var fc = fig.querySelector("figcaption");
        if (!fc) {
            fc = document.createElement("figcaption");
            fig.appendChild(fc);
        }
        fc.textContent = caption;
        syncFigureAlignment(fig, img);
    } else if (fig) {
        // Back into a paragraph of its own where the figure stood.
        // Float classes the figure took over go back onto the image.
        FLOAT_CLASSES.forEach(function (c) {
            if (fig.classList.contains(c)) img.classList.add(c);
        });
        if (!img.getAttribute("class")) img.removeAttribute("class");
        var host = document.createElement("p");
        fig.parentNode.insertBefore(host, fig);
        host.appendChild(node);
        fig.remove();
    }
}

// The toolbar's image-alignment classes (richtext.js formats).
var FLOAT_CLASSES = ["float-left", "mr-6", "float-right", "ml-6"];

// syncFigureAlignment makes a caption figure follow its image's
// alignment: float classes move from the image to the figure (so the
// caption travels with a floated image), and a centered image
// (block mx-auto) mirrors as text-center so the caption centers too.
// Alignment is normalized back onto the image first, so re-applying
// after an alignment change stays correct.
function syncFigureAlignment(fig, img) {
    FLOAT_CLASSES.forEach(function (c) {
        if (fig.classList.contains(c)) {
            fig.classList.remove(c);
            img.classList.add(c);
        }
    });
    fig.classList.remove("text-center");
    FLOAT_CLASSES.forEach(function (c) {
        if (img.classList.contains(c)) {
            img.classList.remove(c);
            fig.classList.add(c);
        }
    });
    if (img.classList.contains("mx-auto")) fig.classList.add("text-center");
    if (!fig.getAttribute("class")) fig.removeAttribute("class");
    if (!img.getAttribute("class")) img.removeAttribute("class");
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
        // A template image slot gets the pencil chrome; only with the
        // media library configured, since choosing a picture is the only
        // thing it offers.
        var slot = null;
        if (!btn && mediaEnabled && t.closest) slot = t.closest("[data-cms-image]");
        // Direct clicks on a rich-text image get the image gear instead.
        // A slot is never one, even with no media library to offer it
        // anything: its size, link and alt text are the template's.
        var img = null;
        if (!btn && !slot && t.tagName === "IMG" && !t.closest("[data-cms-image]") &&
            t.closest("[data-cms-region],[data-cms-sections]")) {
            img = t;
        }
        // Videos and embeds are pointer-events:none while editing, so
        // they're found by position, not as the target.
        var vid = null;
        if (!btn && !slot && !img) vid = mediaAtPoint(e.clientX, e.clientY);
        var snip = null;
        if (!btn && !slot && !img && !vid && t.closest) {
            snip = t.closest(".cms-snippet");
            if (snip && !snip.closest("[data-cms-region],[data-cms-sections]")) snip = null;
        }
        if (btn) {
            e.preventDefault(); // never navigate while editing
            btn.setAttribute("contenteditable", "false"); // covers drag-dropped buttons
            hideChrome("btn");
            showButtonUI(btn);
        } else if (slot) {
            // Slots are usually inside a link, and a click while editing
            // must raise the toolbar rather than leave the page.
            e.preventDefault();
            e.stopPropagation();
            hideChrome("slot");
            showSlotUI(slot);
        } else if (img) {
            // No preventDefault: the click still gives TinyMCE the
            // selection (and its resize handles).
            hideChrome("img");
            showImgUI(img);
        } else if (vid) {
            hideChrome("vid");
            showVidUI(vid);
        } else if (snip) {
            // No preventDefault: the click still places the caret for
            // editing the snippet's text.
            hideChrome("snip");
            showSnipUI(snip);
            // The same click decides which column was meant, so the
            // column tool follows the caret without a second one.
            showColUI(snip, t);
        } else {
            hideChrome();
        }
    }, true);

    // Keep the chrome glued to its element through scrolls and resizes.
    window.addEventListener("scroll", function () {
        if (activeBtn) showButtonUI(activeBtn);
        if (activeSnip) showSnipUI(activeSnip);
        if (activeCol) placeColUI();
        if (activeImg) showImgUI(activeImg);
        if (activeVid) showVidUI(activeVid);
        if (activeSlot) showSlotUI(activeSlot);
    }, true);
    window.addEventListener("resize", function () {
        if (activeBtn) showButtonUI(activeBtn);
        if (activeSnip) showSnipUI(activeSnip);
        if (activeCol) placeColUI();
        if (activeImg) showImgUI(activeImg);
        if (activeVid) showVidUI(activeVid);
        if (activeSlot) showSlotUI(activeSlot);
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
            runWithUndo(ed, run);
            markContainerDirty(btn);
            if (activeBtn === btn) showButtonUI(btn); // re-anchor around the new size
        });
    });

    $("img-set").addEventListener("click", function () {
        if (!activeImg) return;
        var img = activeImg;
        var link = imageLink(img);
        var fig = imageFigure(img);
        var fc = fig ? fig.querySelector("figcaption") : null;
        var fields = [];
        // Swapping the file in place, so the alt text, caption, link and
        // styling already chosen for this image survive the change. It
        // leads the Content tab because "wrong picture" is the reason to
        // open this dialog that can't be fixed anywhere else.
        if (mediaEnabled) {
            fields.push({ id: "file", label: "Image file", type: "image", tab: "Content",
                noClear: true, chooseLabel: "Replace…",
                value: img.getAttribute("data-cms-web") || img.getAttribute("src") });
        }
        fields.push(
            { id: "alt", label: "Alternative text (screen readers, SEO)", type: "text", tab: "Content",
                placeholder: "Describe the image", value: img.getAttribute("alt") || "" },
            { id: "caption", label: "Caption (optional)", type: "text", tab: "Content",
                placeholder: "Shown under the image", value: fc ? fc.textContent : "" },
            { id: "href", label: "Link address (optional)", type: "text", tab: "Content",
                placeholder: "https://example.com or /contact",
                value: link ? (link.getAttribute("href") || "") : "" },
            { id: "newtab", label: "Open in a new tab", type: "check", tab: "Content",
                value: !!link && link.getAttribute("target") === "_blank" },
            { id: "size", label: "Display width", type: "select", tab: "Style",
                value: imgClassValue(img, IMG_SIZES),
                options: [
                    { value: "", label: "Natural" },
                    { value: IMG_SIZES[0], label: "Full width" },
                    { value: IMG_SIZES[1], label: "Two thirds" },
                    { value: IMG_SIZES[2], label: "Half" },
                    { value: IMG_SIZES[3], label: "One third" },
                ] },
            { id: "round", label: "Corner roundness", type: "select", tab: "Style",
                value: imgClassValue(img, IMG_ROUND),
                options: [
                    { value: "", label: "Square" },
                    { value: IMG_ROUND[0], label: "Rounded" },
                    { value: IMG_ROUND[1], label: "Extra rounded" },
                    { value: IMG_ROUND[2], label: "Circle" },
                ] },
            { id: "shadow", label: "Shadow", type: "select", tab: "Style",
                value: imgShadowValue(img),
                options: [
                    { value: "", label: "None" },
                    { value: IMG_SHADOW[0], label: "Subtle" },
                    { value: IMG_SHADOW[1], label: "Strong" },
                ] },
        );
        // The rendition choice only exists for images that recorded
        // both URLs at insert time. It heads the Style tab, so it goes in
        // ahead of the display width rather than at a counted position —
        // the fields before it depend on whether media is enabled.
        if (img.getAttribute("data-cms-orig") && img.getAttribute("data-cms-web")) {
            var sizeAt = fields.findIndex(function (f) { return f.id === "size"; });
            fields.splice(sizeAt, 0, { id: "orig", label: "Use full-quality original", type: "check", tab: "Style",
                value: img.getAttribute("src") === img.getAttribute("data-cms-orig") });
        }
        openDialog({
            message: "Image settings",
            okLabel: "Apply",
            tabs: ["Content", "Style"],
            fields: fields,
            preview: function (v, el) { renderImgPreview(img, v, el); },
        }).then(function (v) {
            if (!v) return;
            var ed = findOwningEditor(img);
            var run = function () { applyImageSettings(img, v); };
            runWithUndo(ed, run);
            markContainerDirty(img);
            if (activeImg !== img) return;
            showImgUI(img); // re-anchor around the new size
            // A newly chosen file has no size on the page until the
            // browser has it, so anchor again once it does.
            if (v.file_web || v.file_orig) {
                img.addEventListener("load", function () {
                    if (activeImg === img) showImgUI(img);
                }, { once: true });
            }
        });
    });

    $("img-del").addEventListener("click", function () {
        if (!activeImg) return;
        var img = activeImg;
        cmsConfirm("Delete this image?", "Delete image", true).then(function (yes) {
            if (!yes) return;
            hideImgUI();
            // Resolve the dirty target before the image leaves the DOM.
            var regionEl = img.closest("[data-cms-region]");
            var sectionsEl = img.closest("[data-cms-sections]");
            var ed = findOwningEditor(img);
            var run = function () {
                // Take the image's scaffolding with it: caption figure,
                // link wrapper, and a paragraph left holding nothing.
                var outer = imageFigure(img) || imageLink(img) || img;
                var parent = outer.parentElement;
                outer.remove();
                if (parent && parent.tagName === "P" && (parent.textContent || "").trim() === "" &&
                    !parent.querySelector("img,a,br")) {
                    parent.remove();
                }
            };
            runWithUndo(ed, run);
            if (regionEl) markDirty(regionEl.getAttribute("data-cms-region"));
            else if (sectionsEl) markSectionsDirty(sectionsEl.getAttribute("data-cms-sections"));
        });
    });

    $("vid-set").addEventListener("click", function () {
        if (!activeVid) return;
        var vid = activeVid;
        hideVidUI(); // the element is replaced; the chrome can't follow it
        if (isMapEmbed(vid)) {
            chooseMapInto(vid, "Paste a new Google Maps link, its embed code, or type an address");
        } else {
            chooseVideoInto(vid, "Change the video");
        }
    });

    $("vid-del").addEventListener("click", function () {
        if (!activeVid) return;
        var vid = activeVid;
        var map = isMapEmbed(vid);
        cmsConfirm(map ? "Delete this map?" : "Delete this video?",
            map ? "Delete map" : "Delete video", true).then(function (yes) {
            if (!yes) return;
            hideVidUI();
            // Resolve the dirty target before the video leaves the DOM.
            var regionEl = vid.closest("[data-cms-region]");
            var sectionsEl = vid.closest("[data-cms-sections]");
            var ed = findOwningEditor(vid);
            var run = function () {
                var parent = vid.parentElement;
                vid.remove();
                // A paragraph left holding nothing was scaffolding.
                if (parent && parent.tagName === "P" && (parent.textContent || "").trim() === "" &&
                    !parent.querySelector("img,video,iframe,a,br")) {
                    parent.remove();
                }
            };
            runWithUndo(ed, run);
            if (regionEl) markDirty(regionEl.getAttribute("data-cms-region"));
            else if (sectionsEl) markSectionsDirty(sectionsEl.getAttribute("data-cms-sections"));
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
            runWithUndo(ed, run);
            if (regionEl) markDirty(regionEl.getAttribute("data-cms-region"));
            else if (sectionsEl) markSectionsDirty(sectionsEl.getAttribute("data-cms-sections"));
        });
    });

    /* ---- snippet chrome actions: edit HTML, delete, drag-to-move ---- */

    $("snip-src").addEventListener("click", function () {
        if (!activeSnip) return;
        var el = activeSnip;
        hideSnipUI(); // the chrome floats above the modal's overlay
        // A custom-code block's own markup is an empty placeholder —
        // there is nothing to edit there. The same button opens the
        // library entry it names, which is the code that actually runs.
        if (el.classList.contains("cms-code")) {
            openCodeEditor(el);
            return;
        }
        openSource({
            title: "Block HTML",
            hint: "The markup of this block. Applied changes still need Save.",
            html: elementSource(el),
        }).then(function (html) {
            if (html === null) return;
            if (html.trim() === "") {
                flash("Nothing to apply — use the trash can to delete the block.");
                return;
            }
            var regionEl = el.closest("[data-cms-region]");
            var sectionsEl = el.closest("[data-cms-sections]");
            var ed = findOwningEditor(el);
            var run = function () {
                var tpl = document.createElement("template");
                tpl.innerHTML = html;
                el.parentNode.replaceChild(tpl.content, el);
            };
            runWithUndo(ed, run);
            unnestSnippets();
            lockButtons(); // the new markup may contain a button
            if (regionEl) markDirty(regionEl.getAttribute("data-cms-region"));
            else if (sectionsEl) markSectionsDirty(sectionsEl.getAttribute("data-cms-sections"));
            flash("Block updated");
        });
    });

    $("col-back").addEventListener("click", function () {
        runColumnEdit(function (info) { moveColumn(info, -1); });
    });
    $("col-on").addEventListener("click", function () {
        runColumnEdit(function (info) { moveColumn(info, 1); });
    });
    $("col-narrow").addEventListener("click", function () {
        runColumnEdit(function (info) { resizeColumn(info, -1); });
    });
    $("col-wide").addEventListener("click", function () {
        runColumnEdit(function (info) { resizeColumn(info, 1); });
    });
    // One handler shape for both modes: inside a row it copies the
    // column, and on a block that is not a row yet it copies the block
    // into a new two-column row. Either way the chrome ends up anchored
    // on the copy, which is the half about to be edited.
    var dupBeside = function (dir) {
        return function () {
            runColumnEdit(function (info) {
                if (info.mode === "cell") {
                    return { target: duplicateColumn(info, dir) };
                }
                var made = duplicateBeside(info.block, dir);
                return { block: made.row, target: made.copy };
            });
        };
    };
    $("col-dup-back").addEventListener("click", dupBeside(-1));
    $("col-dup-on").addEventListener("click", dupBeside(1));
    $("col-add").addEventListener("click", function () {
        runColumnEdit(function (info) {
            if (info.mode === "split") {
                var row = splitIntoColumns(info.block);
                // Anchor on the new second column: it is the one holding
                // a placeholder, so it is the one about to be written in.
                return { block: row, target: row.lastElementChild };
            }
            return { target: addColumn(info) };
        });
    });
    $("col-del").addEventListener("click", function () {
        if (!activeCol || activeCol.mode !== "cell") return;
        var info = activeCol;
        var block = activeSnip;
        // The removed column cannot anchor the chrome afterwards, so the
        // neighbour that closes the gap is noted while it is still
        // findable.
        var near = info.cell.nextElementSibling || info.cell.previousElementSibling;
        var container = info.cell.closest("[data-cms-region],[data-cms-sections]");
        confirmRemove(info).then(function (yes) {
            if (!yes) return;
            var ed = findOwningEditor(info.row);
            var now = block;
            runWithUndo(ed, function () { now = removeColumn(info, block); });
            afterColumnEdit(container, now, near);
        });
    });

    $("snip-set").addEventListener("click", function () {
        if (!activeSnip) return;
        var el = activeSnip;
        var cs = window.getComputedStyle(el);
        // Class-derived looks, for the preview when no override is set.
        var baseBg = rgbToHex(cs.backgroundColor);
        var basePad = cs.padding;
        var baseMargin = { top: cs.marginTop, bottom: cs.marginBottom };
        // The rounding the block's classes give it, so Apply can tell a
        // deliberate choice from the value the slider was merely showing.
        var baseRadius = classRadius(el);
        var setFields = [
            { id: "bgcolor", label: "Background color", type: "color",
                value: rgbToHex(el.style.backgroundColor) },
            { id: "textcolor", label: "Text color", type: "color",
                value: rgbToHex(el.style.color) },
            { id: "spacing", label: "Spacing", type: "select",
                value: el.getAttribute("data-cms-snip-spacing") || "",
                options: [
                    { value: "", label: "As designed" },
                    { value: "compact", label: "Compact" },
                    { value: "normal", label: "Comfortable" },
                    { value: "roomy", label: "Roomy" },
                ] },
            // An override wins even when it is zero — a block squared off
            // on purpose has to read back as 0, not as the rounding its
            // classes would have given it. (`|| baseRadius` would not do:
            // 0 is falsy, and that is exactly the deliberate case.)
            { id: "radius", label: "Corner roundness", type: "range", min: 0, max: 40,
                value: String(Math.min(40, el.style.borderRadius
                    ? parseInt(el.style.borderRadius, 10) || 0
                    : baseRadius)) },
        ];
        openDialog({
            message: "Block settings",
            okLabel: "Apply",
            fields: setFields,
            // A stand-in page: gray context lines above and below the
            // block, so spacing and background read as they will inline.
            preview: function (v, out) {
                out.innerHTML = "";
                var page = document.createElement("div");
                // The preview pane centers flex children; an explicit
                // width keeps the stand-in page from shrink-wrapping.
                page.style.cssText = "width:100%;box-sizing:border-box;background:#fff;" +
                    "border:1px solid #e3e6ea;border-radius:4px;padding:8px 14px";
                var ctxLine = function () {
                    var l = document.createElement("div");
                    l.style.cssText = "height:6px;border-radius:3px;background:#e3e6ea";
                    return l;
                };
                var sp = SNIP_SPACING[v.spacing];
                var bg = v.bgcolor || baseBg;
                var box = document.createElement("div");
                box.style.padding = sp ? sp.padding : basePad;
                box.style.marginTop = sp ? sp.margin : baseMargin.top;
                box.style.marginBottom = sp ? sp.margin : baseMargin.bottom;
                box.style.borderRadius = (parseInt(v.radius, 10) || 0) + "px";
                if (bg) box.style.background = bg;
                else box.style.border = "1px dashed #d9dce1";
                // Placeholder lines take the chosen text color, so a bad
                // contrast choice is visible before it's applied.
                var lineColor = v.textcolor ||
                    (snipDark(bg) ? "rgba(255,255,255,.8)" : "rgba(28,33,40,.3)");
                var lines = function (into, shapes) {
                    shapes.forEach(function (d, i) {
                        var line = document.createElement("div");
                        line.style.cssText = "border-radius:3px";
                        line.style.height = d[0];
                        line.style.width = d[1];
                        line.style.marginTop = i ? "6px" : "0";
                        line.style.background = lineColor;
                        into.appendChild(line);
                    });
                };
                lines(box, [["12px", "40%"], ["7px", "100%"], ["7px", "80%"]]);
                page.appendChild(ctxLine());
                page.appendChild(box);
                page.appendChild(ctxLine());
                out.appendChild(page);
            },
        }).then(function (v) {
            if (!v) return;
            var ed = findOwningEditor(el);
            runWithUndo(ed, function () { applySnippetSettings(el, v, baseRadius); });
            markContainerDirty(el);
            if (activeSnip === el) {
                showSnipUI(el); // re-anchor around the new size
                placeColUI(); // spacing moved the row the column tool sits in
            }
        });
    });

    $("snip-up").addEventListener("click", function () { moveBlock(-1); });
    $("snip-down").addEventListener("click", function () { moveBlock(1); });
    $("snip-dup-up").addEventListener("click", function () { duplicateBlock(-1); });
    $("snip-dup-down").addEventListener("click", function () { duplicateBlock(1); });

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
            runWithUndo(ed, run);
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
        runWithUndo(ed, run);
        if (regionEl) markDirty(regionEl.getAttribute("data-cms-region"));
        else if (sectionsEl) markSectionsDirty(sectionsEl.getAttribute("data-cms-sections"));
        unnestSnippets(); // a drop inside another snippet lifts back out
        lockButtons(); // the moved copy may contain a button
    });

    $("slot-pick").addEventListener("click", function () {
        if (!activeSlot) return;
        var img = activeSlot;
        var name = img.getAttribute("data-cms-image");
        hideSlotUI(); // the chrome floats above the picker's overlay
        openPicker("image", function (item) {
            var url = slotRendition(img, item);
            img.src = url;
            if (item.alt) img.alt = item.alt;
            state.imageValues[name] = url;
            state.filledSlots[name] = true;
            markDirty(name);
        });
    });

    // Removing a picture is not deleting anything: the slot goes back to
    // whatever the page's template draws when nobody has chosen one,
    // which on many templates is a picture of its own. The editor has no
    // way to show that here — it only exists on the server — so this
    // says what will happen rather than pretending to preview it.
    $("slot-clear").addEventListener("click", function () {
        if (!activeSlot) return;
        var img = activeSlot;
        var name = img.getAttribute("data-cms-image");
        hideSlotUI(); // the chrome floats above the dialog's overlay
        cmsConfirm("Remove this picture? The slot goes back to the page's own, " +
            "which you'll see the next time the page loads.",
        "Remove picture", true).then(function (yes) {
            if (!yes) return;
            img.removeAttribute("src");
            state.imageValues[name] = "";
            delete state.filledSlots[name];
            markDirty(name);
        });
    });

    window.addEventListener("beforeunload", function (e) {
        if (hasUnsaved()) {
            e.preventDefault();
            e.returnValue = "";
        }
    });
}
