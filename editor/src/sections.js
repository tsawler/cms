/* ------------------------------------------------------------------ *
 * Sections: per-section controls, add, reorder, settings
 * ------------------------------------------------------------------ */

import { state, mediaEnabled, sectionStyles } from "./state.js";
import { ICONS } from "./shell.js";
import { cmsConfirm, openDialog, refreshDialog } from "./dialogs.js";
import { markSectionsDirty } from "./editing.js";
import { initInlineEditor } from "./richtext.js";
import { lockButtons } from "./buttons.js";
import { openDrawer } from "./snippets.js";
import { openSource } from "./source.js";
import { flash } from "./util.js";

function sbOpt(list, key) {
    for (var i = 0; i < list.length; i++) {
        if (list[i].key === key) return list[i];
    }
    return list[0];
}

// classBackground resolves what background a curated class actually
// paints, by probing it against the host page's stylesheets (shadow
// DOM styles can't see site CSS, so the preview can't just use the
// class). The probe element persists off-screen: JIT CSS setups
// (Tailwind Play CDN) generate rules asynchronously for classes they
// observe in the DOM, so the class has to stay attached for the
// delayed re-probe to find its rule.
var bgProbe = null;
function probeStyle(cls) {
    if (!bgProbe) {
        bgProbe = document.createElement("div");
        bgProbe.style.cssText = "position:absolute;left:-9999px;top:-9999px";
        bgProbe.setAttribute("aria-hidden", "true");
        document.body.appendChild(bgProbe);
    }
    bgProbe.className = cls;
    return getComputedStyle(bgProbe);
}

function classBackground(cls) {
    if (!cls) return "";
    var bg = probeStyle(cls).backgroundColor;
    return bg === "rgba(0, 0, 0, 0)" || bg === "transparent" ? "" : bg;
}

// classRadius probes a curated corner class's border radius the same way
// classBackground probes color, so the preview mirrors whatever the host
// CSS actually rounds.
function classRadius(cls) {
    if (!cls) return "";
    var r = probeStyle(cls).borderTopLeftRadius;
    return !r || r === "0px" ? "" : r;
}

// cornerOption resolves the corners setting, or a no-op option when the
// host ships no corner choices.
function cornerOption(key) {
    var list = sectionStyles.corners || [];
    return list.length ? sbOpt(list, key) : { key: "", class: "" };
}

function isDarkColor(c) {
    var r, g, b;
    var m = /^rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(c || "");
    if (m) {
        r = +m[1]; g = +m[2]; b = +m[3];
    } else if (/^#[0-9a-fA-F]{6}$/.test(c || "")) {
        r = parseInt(c.slice(1, 3), 16);
        g = parseInt(c.slice(3, 5), 16);
        b = parseInt(c.slice(5, 7), 16);
    } else {
        return false;
    }
    return 0.299 * r + 0.587 * g + 0.114 * b < 140;
}

// sectionPreview renders a miniature section from the dialog's
// current values: real background color/image, proportional content
// width, live vertical alignment, and a taller box for taller
// sections. The "content" is three placeholder text lines tinted
// for contrast against the background.
function sectionPreview(v, el) {
    var bgOpt = sbOpt(sectionStyles.backgrounds, v.bg);
    var cornerOpt = cornerOption(v.corners);
    var probed = v.bgcolor || classBackground(bgOpt.class);
    var radius = classRadius(cornerOpt.class);
    var box = buildSectionPreview(v, el, probed || "#ffffff", radius);
    if ((!probed && bgOpt.class) || (!radius && cornerOpt.class)) {
        // Dev setups on the Tailwind Play CDN generate class CSS
        // asynchronously — the probe itself triggers generation, so
        // one delayed re-probe finds the color and radius.
        setTimeout(function () {
            if (el.firstElementChild !== box) return; // stale render
            var lateBg = v.bgcolor || classBackground(bgOpt.class);
            buildSectionPreview(v, el, lateBg || "#ffffff", classRadius(cornerOpt.class));
        }, 250);
    }
}

// presetSectionHTML wraps a preset's starting markup in the same
// section wrapper createSection would build for it — classes, inline
// styles and content-width div included — so the drawer thumbnail
// shows the section exactly as inserting it would.
export function presetSectionHTML(html, settings) {
    var wrapper = document.createElement("section");
    var inner = document.createElement("div");
    inner.setAttribute("data-cms-section-content", "");
    inner.innerHTML = html;
    wrapper.appendChild(inner);
    applySectionSettings(wrapper, settings || {});
    return wrapper.outerHTML;
}

function buildSectionPreview(v, el, resolved, radius) {
    el.innerHTML = "";
    var box = document.createElement("div");
    var hMap = { auto: 96, 50: 112, 75: 128, 100: 144 };
    box.style.cssText = "width:100%;position:relative;display:flex;flex-direction:column;" +
        "overflow:hidden;border:1px solid #e3e6ea;transition:height .15s ease,border-radius .15s ease";
    // Sites without corner options keep the preview's soft chrome; with
    // them, the box shows the real radius (square for "none").
    box.style.borderRadius = (sectionStyles.corners || []).length ? (radius || "0px") : "8px";
    box.style.height = (hMap[v.height] || 96) + "px";
    box.style.backgroundColor = resolved;
    if (v.bgimage) {
        box.style.backgroundImage = "url('" + v.bgimage.replace(/'/g, "%27") + "')";
        box.style.backgroundSize = "cover";
        box.style.backgroundPosition = bgPosition(v);
    }
    box.style.justifyContent = v.valign === "center" ? "center"
        : (v.valign === "bottom" ? "flex-end" : "flex-start");
    var widths = sectionStyles.widths;
    var idx = 0;
    for (var i = 0; i < widths.length; i++) {
        if (widths[i].key === v.width) idx = i;
    }
    var pct = widths.length > 1 ? 50 + (idx / (widths.length - 1)) * 45 : 70;
    var content = document.createElement("div");
    content.style.cssText = "margin:12px auto;flex:0 0 auto;display:flex;flex-direction:column;gap:6px";
    content.style.width = pct + "%";
    var dark = !!v.bgimage || isDarkColor(resolved);
    for (var j = 0; j < 3; j++) {
        var line = document.createElement("div");
        line.style.cssText = "height:8px;border-radius:4px";
        line.style.background = dark ? "rgba(255,255,255,.8)" : "rgba(28,33,40,.3)";
        if (j === 0) {
            line.style.height = "12px";
            line.style.width = "40%";
        } else {
            line.style.width = j === 2 ? "85%" : "100%";
        }
        content.appendChild(line);
    }
    box.appendChild(content);
    el.appendChild(box);
    return box;
}

export function injectSectionUI() {
    var containers = document.querySelectorAll("[data-cms-sections]");
    containers.forEach(function (container) {
        container.querySelectorAll("[data-cms-section]").forEach(injectSectionToolbar);
        if (!container.querySelector(".cms-add-section")) {
            var addWrap = document.createElement("div");
            addWrap.setAttribute("data-cms-ui", "");
            addWrap.className = "cms-add-section";
            addWrap.contentEditable = "false";
            var btn = document.createElement("button");
            btn.type = "button";
            btn.setAttribute("data-secact", "addend");
            btn.textContent = "＋ Add section";
            // One sections area on the page needs no name; several — a
            // post's banner above its body, say — are indistinguishable
            // without one, and both buttons otherwise read as "the place
            // to add content". The name is the template's own.
            if (containers.length > 1) {
                var tag = document.createElement("span");
                tag.className = "cms-add-region";
                tag.textContent = container.getAttribute("data-cms-sections");
                btn.appendChild(tag);
            }
            addWrap.appendChild(btn);
            container.appendChild(addWrap);
        }
    });
    updateAddButtons();
}

// bannerRegion is the sections area a template puts above its main
// content — the post template's header. The convention is the server's
// (sectionRegions in the admin): with more than one sections area, the
// first is the banner and the last is the main content. One area alone
// is the main content and has no banner.
function bannerRegion() {
    var all = document.querySelectorAll("[data-cms-sections]");
    return all.length > 1 ? all[0] : null;
}

// updateAddButtons takes away the ways of adding to an area that is
// already full. A banner is one section by definition — a second one
// underneath is not a banner, it is the top of the page — so once it
// holds a section, both its own "Add section" button and the ＋ on that
// section's toolbar go away. Deleting the section brings them back.
export function updateAddButtons() {
    var banner = bannerRegion();
    document.querySelectorAll("[data-cms-sections]").forEach(function (container) {
        var full = container === banner && !!container.querySelector("[data-cms-section]");
        var add = container.querySelector(".cms-add-section");
        if (add) add.hidden = full;
        container.querySelectorAll('[data-secact="add"]').forEach(function (btn) {
            btn.hidden = full;
        });
    });
}

function injectSectionToolbar(wrapper) {
    if (wrapper.querySelector(".cms-sec-ui")) return;
    var tb = document.createElement("div");
    tb.setAttribute("data-cms-ui", "");
    tb.className = "cms-sec-ui";
    tb.contentEditable = "false";
    // Gear and trash are SVGs (sized by the .cms-sec-ui CSS) so they
    // hold their own optically against the text glyphs; the trash
    // emoji is also avoided because emoji presentation ignores CSS
    // color and that button must read as red/destructive.
    [["up", "↑", "Move up"], ["down", "↓", "Move down"], ["add", "＋", "Add section below"],
        ["src", ICONS.code, "Edit the HTML of this section"],
        ["set", ICONS.gear, "Section settings"], ["del", ICONS.trash, "Delete section"]].forEach(function (b) {
        var btn = document.createElement("button");
        btn.type = "button";
        btn.setAttribute("data-secact", b[0]);
        if (b[1].indexOf("<svg") === 0) btn.innerHTML = b[1];
        else btn.textContent = b[1];
        btn.title = b[2];
        tb.appendChild(btn);
    });
    wrapper.appendChild(tb);
}

export function initSections() {
    document.addEventListener("click", function (e) {
        if (!state.editing) return;
        var btn = e.target.closest ? e.target.closest("[data-secact]") : null;
        if (!btn) return;
        e.preventDefault();
        e.stopPropagation();
        var container = btn.closest("[data-cms-sections]");
        if (!container) return;
        var region = container.getAttribute("data-cms-sections");
        var wrapper = btn.closest("[data-cms-section]");
        var act = btn.getAttribute("data-secact");

        if (act === "addend") {
            state.pendingSection = { region: region, after: null };
            openDrawer();
            return;
        }
        if (!wrapper) return;

        if (act === "add") {
            state.pendingSection = { region: region, after: wrapper };
            openDrawer();
        } else if (act === "up") {
            var prev = wrapper.previousElementSibling;
            if (prev && prev.hasAttribute("data-cms-section")) {
                container.insertBefore(wrapper, prev);
                markSectionsDirty(region);
            }
        } else if (act === "down") {
            var next = wrapper.nextElementSibling;
            if (next && next.hasAttribute("data-cms-section")) {
                container.insertBefore(next, wrapper);
                markSectionsDirty(region);
            }
        } else if (act === "del") {
            cmsConfirm("Delete this section and its content?", "Delete section", true).then(function (yes) {
                if (!yes) return;
                var contentEl = wrapper.querySelector("[data-cms-section-content]");
                state.sectionEditors = state.sectionEditors.filter(function (s) {
                    if (s.el === contentEl) {
                        s.ed.remove();
                        return false;
                    }
                    return true;
                });
                wrapper.remove();
                updateAddButtons(); // an emptied banner can take one again
                markSectionsDirty(region);
            });
        } else if (act === "src") {
            var srcContent = wrapper.querySelector("[data-cms-section-content]");
            if (!srcContent) return;
            // Through the section's own TinyMCE instance when there is
            // one: getContent serializes without editor artifacts, and
            // setContent joins the undo stack.
            var entry = null;
            state.sectionEditors.some(function (s) {
                if (s.el === srcContent) { entry = s; return true; }
                return false;
            });
            openSource({
                title: "Section HTML",
                hint: "The markup of this section's content — background and layout live in the section settings (gear). Applied changes still need Save.",
                html: entry ? entry.ed.getContent() : srcContent.innerHTML,
            }).then(function (html) {
                if (html === null) return;
                if (entry) {
                    entry.ed.undoManager.transact(function () { entry.ed.setContent(html); });
                } else {
                    srcContent.innerHTML = html;
                }
                lockButtons(); // the new markup may contain a button
                markSectionsDirty(region);
                flash("Section updated");
            });
        } else if (act === "set") {
            // Two tabs: how the section is laid out, and what it is
            // painted with. Each holds four controls, which fits a
            // dialog without scrolling.
            var setFields = [
                { id: "width", label: "Content width", type: "select", tab: "Layout",
                    value: wrapper.dataset.cmsWidth || "",
                    options: sectionStyles.widths.map(function (o) { return { value: o.key, label: o.label }; }) },
                { id: "height", label: "Section height", type: "select", tab: "Layout",
                    value: wrapper.dataset.cmsHeight || "auto",
                    options: [
                        { value: "auto", label: "Auto (fits the content)" },
                        { value: "50", label: "50% of the screen" },
                        { value: "75", label: "75% of the screen" },
                        { value: "100", label: "Full screen" },
                    ] },
                { id: "valign", label: "Vertical alignment", type: "select", tab: "Layout",
                    value: wrapper.dataset.cmsValign || "top",
                    options: [
                        { value: "top", label: "Top" },
                        { value: "center", label: "Center" },
                        { value: "bottom", label: "Bottom" },
                    ] },
            ];
            if ((sectionStyles.corners || []).length) {
                setFields.push({ id: "corners", label: "Rounded corners", type: "select", tab: "Layout",
                    value: wrapper.dataset.cmsCorners || "",
                    options: sectionStyles.corners.map(function (o) { return { value: o.key, label: o.label }; }) });
            }
            setFields.push(
                { id: "bg", label: "Background style", type: "select", tab: "Background",
                    value: wrapper.dataset.cmsBg || "",
                    options: sectionStyles.backgrounds.map(function (o) { return { value: o.key, label: o.label }; }) },
                { id: "bgcolor", label: "Background color", type: "color", tab: "Background",
                    value: wrapper.dataset.cmsBgcolor || "" });
            if (mediaEnabled) {
                // A background image is cropped to cover the section, and
                // these two decide which part of it survives the crop.
                // Both sliders run left to right; their labels say which
                // edge each end is.
                var pos = parseBgPosition(wrapper.dataset.cmsBgposition);
                setFields.push(
                    { id: "bgimage", label: "Background image", type: "image", tab: "Background",
                        span: true, value: wrapper.dataset.cmsBgimage || "" },
                    { id: "bgx", label: "Horizontal position (0 = left, 100 = right)",
                        type: "range", tab: "Background", min: 0, max: 100, value: String(pos.x) },
                    { id: "bgy", label: "Vertical position (0 = top, 100 = bottom)",
                        type: "range", tab: "Background", min: 0, max: 100, value: String(pos.y) },
                    { type: "note", tab: "Background", span: true, text: bgFitNote(wrapper) });
            }
            openDialog({
                message: "Section settings",
                okLabel: "Apply",
                wide: true,
                tabs: ["Layout", "Background"],
                fields: setFields,
                preview: sectionPreview,
            }).then(function (values) {
                if (!values) return;
                applySectionSettings(wrapper, values);
                markSectionsDirty(region);
            });
        }
    });
}

// A background image's anchor is stored as the CSS itself — a pair of
// percentages, matching sectionBGPositionRe in render.go, since the
// server writes the same style on a public render. CENTERED is the
// default and is never stored.
var BG_POS_RE = /^([0-9]{1,3})% ([0-9]{1,3})%$/;
var CENTERED = "50% 50%";

// parseBgPosition splits a stored anchor into the two axes the sliders
// edit, falling back to centered for an unset or unreadable one.
function parseBgPosition(stored) {
    var m = BG_POS_RE.exec(stored || "");
    if (!m) return { x: 50, y: 50 };
    return { x: pct(m[1]), y: pct(m[2]) };
}

function pct(v) {
    var n = parseInt(v, 10);
    return isNaN(n) ? 50 : Math.max(0, Math.min(100, n));
}

// bgAspects caches a background image's width/height ratio by address.
// Only the browser can measure it, and it arrives asynchronously; null
// marks one still loading, 0 one that failed.
var bgAspects = {};

function bgAspect(url) {
    if (url in bgAspects) return bgAspects[url];
    bgAspects[url] = null;
    var probe = new Image();
    probe.addEventListener("load", function () {
        bgAspects[url] = probe.naturalHeight ? probe.naturalWidth / probe.naturalHeight : 0;
        refreshDialog(); // the note asked before the answer existed
    });
    probe.addEventListener("error", function () {
        bgAspects[url] = 0;
        refreshDialog();
    });
    probe.src = url;
    return null;
}

// bgFitNote explains which of the two position sliders can actually do
// anything right now. A background image is sized to cover the section,
// so it is scaled until the tighter of the two axes fits exactly — and
// that axis then has no slack left to slide along. Which one that is
// depends on the section's proportions against the image's, so it
// changes with the section's height and with the width of the screen
// the page is being read on.
function bgFitNote(wrapper) {
    return function (v) {
        if (!v.bgimage) return "";
        var aspect = bgAspect(v.bgimage);
        if (aspect === null) return "Measuring the image…";
        if (!aspect) return "";
        var boxW = wrapper.clientWidth || window.innerWidth;
        var contentEl = wrapper.querySelector("[data-cms-section-content]");
        var boxH = contentEl ? contentEl.offsetHeight : wrapper.clientHeight;
        // A chosen height is a minimum: content can still outgrow it.
        if (v.height !== "auto") {
            boxH = Math.max(boxH, window.innerHeight * (parseInt(v.height, 10) / 100));
        }
        if (!boxW || !boxH) return "";
        var box = boxW / boxH;
        if (Math.abs(box - aspect) < 0.02) {
            return "The image matches this section's shape almost exactly, so neither slider has much to move.";
        }
        if (box > aspect) {
            return "At this size the image already fills the width, so only the vertical slider moves it. " +
                "The horizontal one takes over on narrower screens.";
        }
        return "At this size the image already fills the height, so only the horizontal slider moves it. " +
            "The vertical one takes over on wider screens.";
    };
}

// bgPosition reads the anchor out of a settings object. The dialog
// carries the two axes apart (one per slider) and stored settings carry
// them composed, so both shapes are accepted.
function bgPosition(s) {
    if (s.bgx !== undefined || s.bgy !== undefined) {
        return pct(s.bgx) + "% " + pct(s.bgy) + "%";
    }
    return BG_POS_RE.test(s.bgposition || "") ? s.bgposition : CENTERED;
}

// applySectionSettings takes a settings object {bg, width, corners,
// bgcolor, bgimage, and the anchor as either bgposition or bgx/bgy} and
// makes the wrapper reflect it: curated options become classes, the
// free-form background color/image/position become inline styles.
function applySectionSettings(wrapper, s) {
    var bg = sbOpt(sectionStyles.backgrounds, s.bg);
    var w = sbOpt(sectionStyles.widths, s.width);
    var corner = cornerOption(s.corners);
    var color = /^#[0-9a-fA-F]{6}$/.test(s.bgcolor || "") ? s.bgcolor : "";
    var image = s.bgimage || "";
    // An anchor without an image has nothing to anchor, and centered is
    // the default — neither is worth storing.
    var position = image ? bgPosition(s) : CENTERED;
    // Height is a minimum, in viewport units — content can still grow
    // a section taller than its chosen height.
    var height = { 50: 1, 75: 1, 100: 1 }[s.height] ? s.height : "auto";
    // Where the content sits vertically, which matters once a section
    // is taller than its content.
    var valign = { center: 1, bottom: 1 }[s.valign] ? s.valign : "top";
    wrapper.dataset.cmsBg = bg.key;
    wrapper.dataset.cmsWidth = w.key;
    wrapper.dataset.cmsCorners = corner.key;
    wrapper.dataset.cmsHeight = height;
    wrapper.dataset.cmsValign = valign;
    wrapper.dataset.cmsBgcolor = color;
    wrapper.dataset.cmsBgimage = image;
    wrapper.dataset.cmsBgposition = position === CENTERED ? "" : position;
    wrapper.className = [bg.class, corner.class].filter(Boolean).join(" ");
    wrapper.style.minHeight = height === "auto" ? "" : height + "vh";
    wrapper.style.display = valign === "top" ? "" : "flex";
    wrapper.style.flexDirection = valign === "top" ? "" : "column";
    wrapper.style.justifyContent = valign === "top" ? "" :
        (valign === "center" ? "center" : "flex-end");
    wrapper.style.backgroundColor = color;
    wrapper.style.backgroundImage = image ? "url('" + image.replace(/'/g, "%27") + "')" : "";
    wrapper.style.backgroundSize = image ? "cover" : "";
    wrapper.style.backgroundPosition = image ? position : "";
    var contentEl = wrapper.querySelector("[data-cms-section-content]");
    if (!contentEl) return;
    // Preserve TinyMCE's own classes (mce-content-body etc.) when an
    // inline editor is attached to the content element.
    var mce = (contentEl.className || "").split(/\s+/).filter(function (c) {
        return c.indexOf("mce-") === 0;
    });
    contentEl.className = [w.class, bg.contentClass, mce.join(" ")]
        .filter(Boolean).join(" ");
}

// TinyMCE snapshots its target element's attributes at init and puts
// them back on remove(), so section settings applied during the session
// would visually revert when editing ends (the saved data is fine —
// only the DOM went stale). Re-derive the classes from the settings
// keys, which live on the wrapper and survive the teardown.
export function reapplySectionClasses() {
    document.querySelectorAll("[data-cms-section]").forEach(function (wrapper) {
        applySectionSettings(wrapper, {
            bg: wrapper.dataset.cmsBg,
            width: wrapper.dataset.cmsWidth,
            corners: wrapper.dataset.cmsCorners,
            height: wrapper.dataset.cmsHeight,
            valign: wrapper.dataset.cmsValign,
            bgcolor: wrapper.dataset.cmsBgcolor,
            bgimage: wrapper.dataset.cmsBgimage,
            bgposition: wrapper.dataset.cmsBgposition,
        });
    });
}

export function createSection(target, html, settings) {
    var container = document.querySelector('[data-cms-sections="' + target.region + '"]');
    if (!container) return;
    var wrapper = document.createElement("section");
    wrapper.setAttribute("data-cms-section", "");
    var inner = document.createElement("div");
    inner.setAttribute("data-cms-section-content", "");
    inner.innerHTML = html;
    wrapper.appendChild(inner);
    // Section presets carry starting settings; a plain snippet start
    // gets the defaults (applySectionSettings falls back per key).
    applySectionSettings(wrapper, settings || {});
    if (target.after) {
        target.after.insertAdjacentElement("afterend", wrapper);
    } else {
        var addWrap = container.querySelector(".cms-add-section");
        if (addWrap) container.insertBefore(wrapper, addWrap);
        else container.appendChild(wrapper);
    }
    injectSectionToolbar(wrapper);
    initInlineEditor(inner, function () { markSectionsDirty(target.region); }, function (ed) {
        state.sectionEditors.push({ el: inner, ed: ed, region: target.region });
    });
    lockButtons(); // the starting snippet may contain a button
    updateAddButtons(); // a banner that just filled up takes no more
    markSectionsDirty(target.region);
    wrapper.scrollIntoView({ behavior: "smooth", block: "center" });
}
