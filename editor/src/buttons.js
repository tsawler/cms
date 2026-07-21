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
import { cmsConfirm, openDialog } from "./dialogs.js";
import { markDirty, markSectionsDirty, hasUnsaved } from "./editing.js";
import { openPicker } from "./media.js";
import { unnestSnippets } from "./snippets.js";
import { chooseVideoInto } from "./videos.js";

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

// renderImgPreview draws the dialog's live preview: the image at its
// chosen width on a white stand-in "page", with roundness, shadow,
// rendition, and caption applied. Scaled down as needed so a tall
// image can't blow up the dialog.
function renderImgPreview(img, v, el) {
    el.innerHTML = "";
    var ar = img.naturalWidth && img.naturalHeight
        ? img.naturalWidth / img.naturalHeight : 4 / 3;
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
    pimg.src = v.orig === "1"
        ? (img.getAttribute("data-cms-orig") || img.getAttribute("src"))
        : (img.getAttribute("data-cms-web") || img.getAttribute("src"));
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
        // Direct clicks on a rich-text image get the image gear; images
        // in template slots ([data-cms-image]) keep their picker click.
        var img = null;
        if (!btn && t.tagName === "IMG" && !t.closest("[data-cms-image]") &&
            t.closest("[data-cms-region],[data-cms-sections]")) {
            img = t;
        }
        // Videos and embeds are pointer-events:none while editing, so
        // they're found by position, not as the target.
        var vid = null;
        if (!btn && !img) vid = mediaAtPoint(e.clientX, e.clientY);
        var snip = null;
        if (!btn && !img && !vid && t.closest) {
            snip = t.closest(".cms-snippet");
            if (snip && !snip.closest("[data-cms-region],[data-cms-sections]")) snip = null;
        }
        if (btn) {
            e.preventDefault(); // never navigate while editing
            btn.setAttribute("contenteditable", "false"); // covers drag-dropped buttons
            hideSnipUI();
            hideImgUI();
            hideVidUI();
            showButtonUI(btn);
        } else if (img) {
            // No preventDefault: the click still gives TinyMCE the
            // selection (and its resize handles).
            hideButtonUI();
            hideSnipUI();
            hideVidUI();
            showImgUI(img);
        } else if (vid) {
            hideButtonUI();
            hideSnipUI();
            hideImgUI();
            showVidUI(vid);
        } else if (snip) {
            // No preventDefault: the click still places the caret for
            // editing the snippet's text.
            hideButtonUI();
            hideImgUI();
            hideVidUI();
            showSnipUI(snip);
        } else {
            hideButtonUI();
            hideSnipUI();
            hideImgUI();
            hideVidUI();
        }
    }, true);

    // Keep the chrome glued to its element through scrolls and resizes.
    window.addEventListener("scroll", function () {
        if (activeBtn) showButtonUI(activeBtn);
        if (activeSnip) showSnipUI(activeSnip);
        if (activeImg) showImgUI(activeImg);
        if (activeVid) showVidUI(activeVid);
    }, true);
    window.addEventListener("resize", function () {
        if (activeBtn) showButtonUI(activeBtn);
        if (activeSnip) showSnipUI(activeSnip);
        if (activeImg) showImgUI(activeImg);
        if (activeVid) showVidUI(activeVid);
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

    $("img-set").addEventListener("click", function () {
        if (!activeImg) return;
        var img = activeImg;
        var link = imageLink(img);
        var fig = imageFigure(img);
        var fc = fig ? fig.querySelector("figcaption") : null;
        var fields = [
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
        ];
        // The rendition choice only exists for images that recorded
        // both URLs at insert time.
        if (img.getAttribute("data-cms-orig") && img.getAttribute("data-cms-web")) {
            fields.splice(4, 0, { id: "orig", label: "Use full-quality original", type: "check", tab: "Style",
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
            if (ed) ed.undoManager.transact(run); else run();
            markContainerDirty(img);
            if (activeImg === img) showImgUI(img); // re-anchor around the new size
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
            if (ed) ed.undoManager.transact(run); else run();
            if (regionEl) markDirty(regionEl.getAttribute("data-cms-region"));
            else if (sectionsEl) markSectionsDirty(sectionsEl.getAttribute("data-cms-sections"));
        });
    });

    $("vid-set").addEventListener("click", function () {
        if (!activeVid) return;
        var vid = activeVid;
        hideVidUI(); // the element is replaced; the chrome can't follow it
        chooseVideoInto(vid, "Change the video");
    });

    $("vid-del").addEventListener("click", function () {
        if (!activeVid) return;
        var vid = activeVid;
        cmsConfirm("Delete this video?", "Delete video", true).then(function (yes) {
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
            if (ed) ed.undoManager.transact(run); else run();
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
