/* ------------------------------------------------------------------ *
 * Sections: per-section controls, add, reorder, settings
 * ------------------------------------------------------------------ */

import { state, mediaEnabled, sectionStyles } from "./state.js";
import { ICONS } from "./shell.js";
import { cmsConfirm, openDialog } from "./dialogs.js";
import { markSectionsDirty } from "./editing.js";
import { initInlineEditor } from "./richtext.js";
import { lockButtons } from "./buttons.js";
import { openDrawer } from "./snippets.js";

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
function classBackground(cls) {
    if (!cls) return "";
    if (!bgProbe) {
        bgProbe = document.createElement("div");
        bgProbe.style.cssText = "position:absolute;left:-9999px;top:-9999px";
        bgProbe.setAttribute("aria-hidden", "true");
        document.body.appendChild(bgProbe);
    }
    bgProbe.className = cls;
    var bg = getComputedStyle(bgProbe).backgroundColor;
    return bg === "rgba(0, 0, 0, 0)" || bg === "transparent" ? "" : bg;
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
    var probed = v.bgcolor || classBackground(bgOpt.class);
    var box = buildSectionPreview(v, el, probed || "#ffffff");
    if (!probed && bgOpt.class) {
        // Dev setups on the Tailwind Play CDN generate class CSS
        // asynchronously — the probe itself triggers generation, so
        // one delayed re-probe finds the color.
        setTimeout(function () {
            if (el.firstElementChild !== box) return; // stale render
            var late = classBackground(bgOpt.class);
            if (late) buildSectionPreview(v, el, late);
        }, 250);
    }
}

function buildSectionPreview(v, el, resolved) {
    el.innerHTML = "";
    var box = document.createElement("div");
    var hMap = { auto: 96, 50: 112, 75: 128, 100: 144 };
    box.style.cssText = "width:100%;position:relative;display:flex;flex-direction:column;" +
        "overflow:hidden;border-radius:8px;border:1px solid #e3e6ea;transition:height .15s ease";
    box.style.height = (hMap[v.height] || 96) + "px";
    box.style.backgroundColor = resolved;
    if (v.bgimage) {
        box.style.backgroundImage = "url('" + v.bgimage.replace(/'/g, "%27") + "')";
        box.style.backgroundSize = "cover";
        box.style.backgroundPosition = "center";
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
    document.querySelectorAll("[data-cms-sections]").forEach(function (container) {
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
            addWrap.appendChild(btn);
            container.appendChild(addWrap);
        }
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
                markSectionsDirty(region);
            });
        } else if (act === "set") {
            var setFields = [
                { id: "bg", label: "Background style", type: "select", value: wrapper.dataset.cmsBg || "",
                    options: sectionStyles.backgrounds.map(function (o) { return { value: o.key, label: o.label }; }) },
                { id: "width", label: "Content width", type: "select", value: wrapper.dataset.cmsWidth || "",
                    options: sectionStyles.widths.map(function (o) { return { value: o.key, label: o.label }; }) },
                { id: "height", label: "Section height", type: "select", value: wrapper.dataset.cmsHeight || "auto",
                    options: [
                        { value: "auto", label: "Auto (fits the content)" },
                        { value: "50", label: "50% of the screen" },
                        { value: "75", label: "75% of the screen" },
                        { value: "100", label: "Full screen" },
                    ] },
                { id: "valign", label: "Vertical alignment", type: "select", value: wrapper.dataset.cmsValign || "top",
                    options: [
                        { value: "top", label: "Top" },
                        { value: "center", label: "Center" },
                        { value: "bottom", label: "Bottom" },
                    ] },
                { id: "bgcolor", label: "Background color", type: "color", value: wrapper.dataset.cmsBgcolor || "" },
            ];
            if (mediaEnabled) {
                setFields.push({ id: "bgimage", label: "Background image", type: "image",
                    value: wrapper.dataset.cmsBgimage || "" });
            }
            openDialog({
                message: "Section settings",
                okLabel: "Apply",
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

// applySectionSettings takes a settings object {bg, width, bgcolor,
// bgimage} and makes the wrapper reflect it: curated options become
// classes, the free-form background color/image become inline styles.
function applySectionSettings(wrapper, s) {
    var bg = sbOpt(sectionStyles.backgrounds, s.bg);
    var w = sbOpt(sectionStyles.widths, s.width);
    var color = /^#[0-9a-fA-F]{6}$/.test(s.bgcolor || "") ? s.bgcolor : "";
    var image = s.bgimage || "";
    // Height is a minimum, in viewport units — content can still grow
    // a section taller than its chosen height.
    var height = { 50: 1, 75: 1, 100: 1 }[s.height] ? s.height : "auto";
    // Where the content sits vertically, which matters once a section
    // is taller than its content.
    var valign = { center: 1, bottom: 1 }[s.valign] ? s.valign : "top";
    wrapper.dataset.cmsBg = bg.key;
    wrapper.dataset.cmsWidth = w.key;
    wrapper.dataset.cmsHeight = height;
    wrapper.dataset.cmsValign = valign;
    wrapper.dataset.cmsBgcolor = color;
    wrapper.dataset.cmsBgimage = image;
    wrapper.className = bg.class || "";
    wrapper.style.minHeight = height === "auto" ? "" : height + "vh";
    wrapper.style.display = valign === "top" ? "" : "flex";
    wrapper.style.flexDirection = valign === "top" ? "" : "column";
    wrapper.style.justifyContent = valign === "top" ? "" :
        (valign === "center" ? "center" : "flex-end");
    wrapper.style.backgroundColor = color;
    wrapper.style.backgroundImage = image ? "url('" + image.replace(/'/g, "%27") + "')" : "";
    wrapper.style.backgroundSize = image ? "cover" : "";
    wrapper.style.backgroundPosition = image ? "center" : "";
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
            height: wrapper.dataset.cmsHeight,
            valign: wrapper.dataset.cmsValign,
            bgcolor: wrapper.dataset.cmsBgcolor,
            bgimage: wrapper.dataset.cmsBgimage,
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
    markSectionsDirty(target.region);
    wrapper.scrollIntoView({ behavior: "smooth", block: "center" });
}
