/* ------------------------------------------------------------------ *
 * TinyMCE (rich HTML regions)
 * ------------------------------------------------------------------ */

import { state, styleFormats, colorStyles, mediaEnabled, EDITOR_BASE } from "./state.js";
import { api, cssColorToHex } from "./util.js";
import { markDirty, markSectionsDirty, htmlRegions } from "./editing.js";
import { openPicker } from "./media.js";
import { openDialog } from "./dialogs.js";
import { runWithUndo } from "./undo.js";
import { hideChrome } from "./buttons.js";
import { cellFor } from "./columns.js";

var tinyLoading = null;

export function loadTinyMCE() {
    if (window.tinymce) return Promise.resolve();
    if (tinyLoading) return tinyLoading;
    tinyLoading = new Promise(function (resolve, reject) {
        var s = document.createElement("script");
        s.src = EDITOR_BASE + "tinymce/tinymce.min.js";
        s.onload = function () { resolve(); };
        s.onerror = function () {
            tinyLoading = null;
            reject(new Error("Could not load the editor — check your connection."));
        };
        document.head.appendChild(s);
    });
    return tinyLoading;
}

// uploadImage backs TinyMCE's image workflows (insert button, browse,
// paste, drag-drop): the file goes through the CMS media API — stored
// on the bucket, resized, recorded in the library — and the returned
// web-variant URL lands in the page.
function uploadImage(blobInfo) {
    var fd = new FormData();
    fd.append("file", blobInfo.blob(), blobInfo.filename());
    return api("/media", { method: "POST", body: fd }).then(function (body) {
        return body.media.web;
    });
}

// alignBlocks are the elements the alignment buttons act on when the
// selection is not an image (mirrors TinyMCE's default list, minus img).
var alignBlocks = "p,h1,h2,h3,h4,h5,h6,td,th,div,ul,ol,li,blockquote,figure";

/* ---- text size ---------------------------------------------------
 * The toolbar's size control. It writes utility classes onto the block
 * the cursor is in, and three decisions in this list are the whole
 * reason it is safe to hand an editor:
 *
 * It is a short ladder of named steps, not Tailwind's full text-xs…
 * text-9xl scale. Thirteen absolute steps per element is how a page
 * stops looking designed; a handful of relative ones keep the choice
 * inside the range the theme can absorb. Nothing above text-5xl is
 * offered, because a 128px headline is a decision for a comp, not a
 * menu.
 *
 * The steps are spaced by ratio rather than by Tailwind's index, so no
 * two neighbours are far enough apart for the menu to feel like it
 * skipped one. From sm up: 14, 16, 18, 24, 30, 36, 48 — no jump wider
 * than a third. Picking Tailwind's own consecutive names instead would
 * put an unusable crowd at the bottom (14, 16, 18, 20) and a 2x chasm
 * at the top.
 *
 * The labels are the clothing ladder rather than comparatives, because
 * English runs out: Large/Larger/Largest cannot take a fourth step, and
 * a reader should never have to work out which of two adjectives is
 * bigger. They deliberately do NOT line up with the Tailwind names in
 * the values beside them — "XL" is text-xl only below sm — since a
 * label naming its own implementation would be a promise this ladder
 * cannot keep at two breakpoints. Labels are not stored either way;
 * content carries the classes, so renaming a step is free.
 *
 * Ordered small to large, with Normal in its true place rather than
 * first: the menu ticks whichever step is current, so the way back to
 * the base size is no harder to find, and a ladder that runs in one
 * direction is easier to aim at than one that restarts.
 *
 * Every step above the base is a responsive *pair*. A bare text-5xl is
 * 60px on a phone too, so the unit an editor picks is already
 * "text-3xl on mobile, text-5xl from sm up" — the overflow they would
 * otherwise only discover on someone else's screen cannot be expressed.
 *
 * Two of the upper steps share a size below sm (2XL and 3XL are both
 * 24px on a phone). That is the pairing working, not a mistake: the
 * range a phone can carry is shorter than the range this ladder covers,
 * so the top of it converges. Display stays alone at the top on both.
 *
 * And it applies to the block, never to an inline run. Changing size
 * mid-paragraph re-leads only the line boxes the span touches, which
 * gives one paragraph two different line heights; the Styles menu's
 * "Lead paragraph" and "Small print" remain the way to size a phrase.
 *
 * Every class here must be compiled: they live in the database, so they
 * are declared in render.EditorAppliedClasses (Go) and safelisted in the
 * docs, and a test keeps the two in step. */

var TEXT_SIZES = [
    { value: "text-sm", label: "Small" },
    { value: "", label: "Normal" },
    { value: "text-lg", label: "Large" },
    { value: "text-xl sm:text-2xl", label: "XL" },
    { value: "text-2xl sm:text-3xl", label: "2XL" },
    { value: "text-2xl sm:text-4xl", label: "3XL" },
    { value: "text-3xl sm:text-5xl", label: "Display" },
];

// Every token the control owns — the tokens of the presets above plus
// any it has ever written. Changing the ladder must not strand a class
// on saved content that nothing can then clear.
var TEXT_SIZE_TOKENS = ["text-sm", "text-base", "text-lg", "text-xl",
    "text-2xl", "text-3xl", "text-4xl", "text-5xl",
    "sm:text-2xl", "sm:text-3xl", "sm:text-4xl", "sm:text-5xl"];

// textSizeBlocks are the blocks the control acts on: whatever the
// selection covers, minus the ones where a size class would be a
// surprise (a table cell takes its size from the table's own settings,
// and a figure is a wrapper around media).
var TEXT_SIZE_BLOCKS = "p,h1,h2,h3,h4,h5,h6,li,blockquote,div";

function textSizeTargets(ed) {
    var blocks = ed.selection.getSelectedBlocks() || [];
    return blocks.filter(function (b) { return b.matches && b.matches(TEXT_SIZE_BLOCKS); });
}

// currentTextSize reports the preset the selection is already at, or ""
// for the base size. A mixed selection reports the first block's value —
// the menu shows a tick, and picking anything makes the whole selection
// agree.
function currentTextSize(ed) {
    var el = textSizeTargets(ed)[0];
    if (!el) return "";
    var have = (el.getAttribute("class") || "").split(/\s+/);
    for (var i = TEXT_SIZES.length - 1; i >= 0; i--) {
        var v = TEXT_SIZES[i].value;
        if (!v) continue;
        var parts = v.split(" ");
        var all = parts.every(function (c) { return have.indexOf(c) !== -1; });
        if (all) return v;
    }
    return "";
}

function applyTextSize(ed, value) {
    textSizeTargets(ed).forEach(function (el) {
        TEXT_SIZE_TOKENS.forEach(function (c) { el.classList.remove(c); });
        if (value) value.split(" ").forEach(function (c) { el.classList.add(c); });
        // classList.remove leaves class="" behind on a block whose only
        // classes were ours, and an empty attribute is noise in the
        // saved HTML.
        if (!el.getAttribute("class")) el.removeAttribute("class");
    });
}

/* ---- text colour -------------------------------------------------
 * The toolbar's colour control: the host's curated colours, applied as
 * classes exactly as the Styles menu used to apply them, and under them
 * a picker for any colour at all.
 *
 * The picker is the one place in this editor that writes a colour into
 * content as an inline style, and it does so knowingly. A class is what
 * lets a redesign reach text that was coloured a year ago; a hex is a
 * decision frozen into the words. Everything curated therefore stays a
 * class and stays first in the menu, so the cheap click is the one that
 * ages well and the picker is the deliberate extra step.
 *
 * Only #rrggbb and rgb(r, g, b) survive the server's sanitizer, which is
 * what the colour input produces — an alpha channel would be dropped on
 * save, so nothing here offers one. */

// COLOR_FMT names a curated colour's registered format. Registration
// happens through the init options (see colorFormats), so the names have
// to be derivable from the index alone, here and at the call sites.
function colorFmt(i) { return "cmscolor" + i; }

// CUSTOM_FMT is the picker's format: a span carrying the chosen colour.
// Same shape TinyMCE's own forecolor uses, registered under our own name
// so nothing depends on the theme's internals.
var CUSTOM_FMT = "cmstextcolorcustom";

// The three flags forecolor carries, and none of them is decoration.
// remove_similar is what lets the format be removed by name without
// naming the colour — without it "Remove color" matches nothing, since
// the span it is trying to strip holds a real value and the removal
// asks for a null one, and the colour silently stays put. links carries
// the colour onto text inside an <a>, which is where "why is this bit
// still blue" comes from otherwise. clear_child_styles drops colours on
// nested spans, so recolouring a run that already had a coloured word
// inside it changes all of it rather than all but that word.
var CUSTOM_FMT_DEF = {
    inline: "span",
    styles: { color: "%value" },
    links: true,
    remove_similar: true,
    clear_child_styles: true,
};

// colorFormats is the init `formats` contribution: one entry per curated
// colour, plus the picker's.
function colorFormats() {
    var out = {};
    colorStyles.forEach(function (c, i) { out[colorFmt(i)] = c.format; });
    out[CUSTOM_FMT] = CUSTOM_FMT_DEF;
    return out;
}

// clearTextColor takes every colour this control can apply back off the
// selection — curated and custom both. Picking a colour clears first, so
// two colours can never end up layered on the same words, each winning
// somewhere different.
function clearTextColor(ed) {
    colorStyles.forEach(function (c, i) {
        ed.formatter.remove(colorFmt(i), null, null, true);
    });
    ed.formatter.remove(CUSTOM_FMT, { value: null }, null, true);
}

// currentTextColor is the colour the selection actually renders in —
// computed, so a colour coming from a class or from the section's own
// styling is what the picker opens on rather than white.
//
// getStart, not getNode: a selection that spans a coloured run reports
// the run's common ancestor as its "node", which is the paragraph
// around it — so the picker would open on the body colour of the very
// text it is being asked to recolour. The start element is inside the
// run, and is what the words at the caret actually inherit from.
function currentTextColor(ed) {
    return cssColorToHex(ed.dom.getStyle(ed.selection.getStart(), "color", true));
}

// COLOR_PREVIEW_PROPS is what a colour entry shows of itself: the
// colour, and the pill the badge treatment draws it on. The rest of
// what getCssText reports — the font the page happens to be set in, its
// size, weight, borders — is the Styles menu's business, where an entry
// really can be a serif or a bold; carrying it here would only make the
// colour names a different size than the two plain items under them.
var COLOR_PREVIEW_PROPS = ["color", "background-color", "padding", "border-radius"];

// colorPreviewStyle turns a colour format's preview into the shape the
// theme renders around a menu item's label: `meta.style`, a {tag,
// styles} pair it wraps the text in. It is the mechanism the Styles
// menu previews with, and the source is the same too — getCssText,
// carrying the badge treatment PreInit puts on it: the lightness shift
// that keeps a colour legible on the skin it is drawn against, over an
// opaque pill in the same hue. So a colour reads the same in both
// menus, and neither has a rule about light and dark that the other
// doesn't.
//
// The declarations are split here rather than through dom.parseStyle,
// which would put them through TinyMCE's style compressor on the way:
// these values are colour functions — oklch(from …), color-mix(…) —
// and a normalizer written for margins and urls is the wrong thing to
// hand them to. Splitting on ";" and the first ":" is enough while no
// value can contain either, which none of these can.
//
// A format with no colour to show for itself leaves the item plain
// rather than half-styled.
function colorPreviewStyle(ed, fmt) {
    var css;
    try {
        css = ed.formatter.getCssText(fmt);
    } catch (err) {
        return null;
    }
    if (!css) return null;
    var styles = {};
    var any = false;
    css.split(";").forEach(function (decl) {
        var at = decl.indexOf(":");
        if (at < 1) return;
        var prop = decl.slice(0, at).trim();
        var val = decl.slice(at + 1).trim();
        if (!val || COLOR_PREVIEW_PROPS.indexOf(prop) === -1) return;
        styles[prop] = val;
        if (prop === "color") any = true;
    });
    return any ? { tag: "span", styles: styles } : null;
}

/* ---- tables ------------------------------------------------------
 * Editor-inserted tables are styled with utility classes, like the
 * alignment buttons: content carries classes, the host's CSS stays in
 * charge, and the sanitizer (which strips inline styles) is happy.
 * The classes are also the state — the gear dialog reads its current
 * values off the table and rewrites them, and cells created later by
 * row/column operations are stamped from the same reading. Nothing is
 * stored anywhere else. Every class here must be safelisted in the
 * site's Tailwind build (see README). */

var TBL_LINES = {
    rows: { th: "border-b-2 border-slate-300", td: "border-b border-slate-200" },
    grid: { th: "border border-slate-300", td: "border border-slate-200" },
    none: { th: "", td: "" },
};
var TBL_PAD = { compact: "p-1", normal: "p-2", spacious: "p-4" };
var TBL_STRIPE = "odd:bg-slate-50";
// The tokens the stamps own. Anything else on a cell (an alignment
// class, a hand-written class from the source view) is left alone.
var TBL_CELL_TOKENS = ["border", "border-b", "border-b-2", "border-slate-200",
    "border-slate-300", "p-1", "p-2", "p-4", "align-top", "font-semibold"];

// tableStyleOptions reads the curated settings off a table's classes.
// A cell without even a padding token has never been stamped (the
// table is mid-insert) and reads as the defaults.
function tableStyleOptions(t) {
    var cell = t.querySelector("td") || t.querySelector("th");
    var cls = cell ? " " + cell.className + " " : "";
    var has = function (tok) { return cls.indexOf(" " + tok + " ") !== -1; };
    var virgin = !has("p-1") && !has("p-2") && !has("p-4");
    var striped = false;
    Array.prototype.forEach.call(t.rows, function (row) {
        if (row.parentNode.nodeName !== "THEAD" &&
            (" " + row.className + " ").indexOf(" " + TBL_STRIPE + " ") !== -1) {
            striped = true;
        }
    });
    return {
        lines: has("border") ? "grid"
            : has("border-b") || has("border-b-2") || virgin ? "rows" : "none",
        density: has("p-1") ? "compact" : has("p-4") ? "spacious" : "normal",
        striped: striped,
        width: (" " + t.className + " ").indexOf(" w-auto ") !== -1 ? "fit" : "full",
    };
}

// swapTokens rewrites the owned part of an element's class list: the
// `remove` tokens go, `add` (space-separated) comes first, everything
// else stays.
function swapTokens(el, remove, add) {
    var addToks = add ? add.split(" ") : [];
    var keep = (el.className || "").split(/\s+/).filter(function (tok) {
        return tok && remove.indexOf(tok) === -1 && addToks.indexOf(tok) === -1;
    });
    var cls = addToks.concat(keep).join(" ");
    if (cls) el.className = cls;
    else el.removeAttribute("class");
}

function stampCell(c, opt) {
    var th = c.nodeName === "TH";
    var add = (TBL_LINES[opt.lines][th ? "th" : "td"] + " " + TBL_PAD[opt.density] +
        (th ? " font-semibold" : " align-top")).trim();
    // Header cells center by browser default; text-left restores flow.
    // Never override an alignment the alignment buttons applied.
    if (th && !/(^|\s)text-(center|right)(\s|$)/.test(c.className || "")) {
        add += " text-left";
    }
    swapTokens(c, TBL_CELL_TOKENS, add);
}

// Tables scroll inside their own box on narrow screens instead of
// dragging the whole page wider: every top-level table lives in a
// <div class="cms-table-wrap overflow-x-auto">. The marker class keeps
// the sweep from touching host markup that happens to use the same
// utility; a wrapper whose table is gone (deleted) is dropped. A div
// wrapper (not display:block on the table) preserves the table's
// semantics for screen readers.
var TBL_WRAP = "cms-table-wrap";

function wrapTables(root) {
    Array.prototype.forEach.call(root.querySelectorAll("table"), function (t) {
        if (t.closest("." + TBL_WRAP)) return;
        if (t.parentElement && t.parentElement.closest("table")) return; // nested
        var w = root.ownerDocument.createElement("div");
        w.className = TBL_WRAP + " overflow-x-auto";
        t.parentNode.insertBefore(w, t);
        w.appendChild(t);
    });
    Array.prototype.forEach.call(root.querySelectorAll("div." + TBL_WRAP), function (w) {
        if (w.querySelector("table")) return;
        while (w.firstChild) w.parentNode.insertBefore(w.firstChild, w);
        w.parentNode.removeChild(w);
    });
}

// stampTable rewrites a whole table to the given settings (or freshens
// its current ones); structural operations funnel through here via the
// TableModified event.
function stampTable(t, opt) {
    opt = opt || tableStyleOptions(t);
    swapTokens(t, ["w-full", "w-auto"], opt.width === "fit" ? "w-auto" : "w-full");
    Array.prototype.forEach.call(t.rows, function (row) {
        var body = row.parentNode.nodeName !== "THEAD";
        swapTokens(row, [TBL_STRIPE], body && opt.striped ? TBL_STRIPE : "");
        Array.prototype.forEach.call(row.cells, function (c) { stampCell(c, opt); });
    });
}

function escapeAttr(s) {
    return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
}
function escapeText(s) {
    return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

export function initRichEditors() {
    htmlRegions().forEach(function (el) {
        var name = el.dataset.cmsRegion;
        if (state.mceEditors[name]) return;
        initInlineEditor(el, function () { markDirty(name); }, function (ed) { state.mceEditors[name] = ed; });
    });
    initSectionEditors();
}

// initSectionEditors attaches an editor to every section's content
// container; each section is its own TinyMCE instance so structure
// (wrappers, settings) stays out of the editable surface.
function initSectionEditors() {
    document.querySelectorAll("[data-cms-sections]").forEach(function (container) {
        var region = container.getAttribute("data-cms-sections");
        container.querySelectorAll("[data-cms-section-content]").forEach(function (el) {
            var known = state.sectionEditors.some(function (s) { return s.el === el; });
            if (known) return;
            initInlineEditor(el, function () { markSectionsDirty(region); }, function (ed) {
                state.sectionEditors.push({ el: el, ed: ed, region: region });
            });
        });
    });
}

/* ---- pasting into a snippet -------------------------------------
 * The plainest snippets *are* their block: the Text snippet is a
 * single <p class="cms-snippet">. Paste more than one block's worth
 * into one and the browser's own rules take over — the paragraph is
 * replaced when its whole content was selected, split when the caret
 * sat inside it — and the blocks that come out of that carry only
 * what the clipboard held. The class does not survive, so the block
 * quietly stops being a snippet: no outline, no drag handle, no
 * trash, and nothing for the section tools to take hold of.
 *
 * Enter already carries the class onto the paragraph it makes, and
 * paste does the same here: every top-level block a paste brings in
 * inherits the snippet it landed in. Only a snippet that is itself
 * the block being pasted into qualifies — a container snippet (the
 * Article text <div>) keeps its own outline and its children must stay
 * plain, or unnestSnippets would tear the paste back out of it. */
function pasteHostSnippet(ed) {
    var block = ed.dom.getParent(ed.selection.getNode(), ed.dom.isBlock);
    if (!block || !ed.dom.hasClass(block, "cms-snippet")) return null;
    // Snippets are siblings at the top of a region by design, or of a
    // column, which is the other place they are allowed to stack; a
    // snippet anywhere deeper is a nesting accident and not something to
    // copy onto more blocks.
    if (block.parentNode === ed.getBody()) return block;
    return cellFor(block) ? block : null;
}

export function initInlineEditor(el, onDirty, register) {
    var light = state.editorTheme === "light";
    var opts = {
        target: el,
        inline: true,
        menubar: false,
        // Chrome to match the rest of the editor's, dark or light as
        // the site settings say. Content styles are unaffected either
        // way: oxide-dark's content.inline.css is identical to oxide's.
        // The skin is fixed when the instance is built, so switching
        // schemes without a reload means rebuilding them — see
        // settings.js.
        skin: light ? "oxide" : "oxide-dark",
        // In inline mode the toolbar floats docked to the region
        // as soon as it gains focus — a click is enough, no text
        // selection needed.
        toolbar: "styles cmstextsize cmstextcolor | bold italic underline strikethrough | alignleft aligncenter alignright | bullist numlist | blockquote hr table | link unlink" +
            (mediaEnabled ? " | cmsimage cmsvideo cmsdoc" : "") + " | removeformat",
        fixed_toolbar_container: "#cms-mce-toolbar",
        // Every paragraph at the top of a region is a block in the
        // editor's sense: an outline, a drag handle, a trash, something
        // the section tools can take hold of. Splitting one carries the
        // class already (that is how Enter makes another), but TinyMCE
        // also *creates* blocks from nothing — the first keystroke in a
        // region whose content was just selected and deleted, loose text
        // that needs wrapping, whatever it puts back when a delete takes
        // the last paragraph with it. Those arrive bare, and bare is the
        // one state where the words someone just typed are the only
        // thing on the page the block tools cannot take hold of.
        //
        // The name says root block and the option is documented for the
        // wrapping case, but TinyMCE merges these attributes into every
        // block it builds, at any depth — so this alone would put the
        // marker on a paragraph made inside a callout or a column too.
        // The NewBlock handler below takes it back off those; see there
        // for why that split is where it belongs.
        forced_root_block_attrs: { class: "cms-snippet" },
        plugins: "lists link autolink table",
        // Tables are structural only: no properties dialogs, no
        // drag-resizing, no colgroups — every path that would write
        // inline styles or width attributes is off. The look comes
        // from the utility classes stampTable applies; the floating
        // toolbar inside a table offers row/column/header operations.
        table_default_styles: {},
        table_default_attributes: {},
        table_use_colgroups: false,
        table_resize_bars: false,
        object_resizing: "img",
        table_header_type: "sectionCells",
        table_toolbar: "tablerowheader | tableinsertrowbefore tableinsertrowafter tabledeleterow | " +
            "tableinsertcolbefore tableinsertcolafter tabledeletecol | cmstablegear tabledelete",
        // Paste and drag-drop of image data still upload through
        // the media API (these are core editor features, not part
        // of the image-dialog plugin, which we don't use).
        images_upload_handler: uploadImage,
        automatic_uploads: true,
        paste_data_images: mediaEnabled,
        // Tailwind-first alignment: replace TinyMCE's default align
        // formats, which write inline styles (text-align on blocks;
        // display/margin on images), with utility classes. Classes
        // survive the server-side sanitizer for non-admin editors and
        // keep saved content styleable by the host's CSS. Images need
        // their own rules: under Tailwind's preflight an <img> is a
        // block element, so text-align on its parent can never move it.
        formats: Object.assign(colorFormats(), {
            // Semantic tags instead of TinyMCE's span-with-inline-style
            // defaults: the sanitizer strips every inline style except
            // text-align, so the default forms would vanish from
            // non-admin saves (including via the always-on Cmd+U/
            // Cmd+Shift+S shortcuts).
            underline: { inline: "u" },
            strikethrough: { inline: "s" },
            alignleft: [
                { selector: alignBlocks, classes: "text-left" },
                { selector: "img", classes: "float-left mr-6" },
            ],
            aligncenter: [
                { selector: alignBlocks, classes: "text-center" },
                { selector: "img", classes: "block mx-auto" },
            ],
            alignright: [
                { selector: alignBlocks, classes: "text-right" },
                { selector: "img", classes: "float-right ml-6" },
            ],
        }),
        // Never rewrite URLs relative to the current page — media
        // lives at absolute paths like /cms/media/… and relative
        // forms would break on nested pages.
        convert_urls: false,
        link_default_protocol: "https",
        browser_spellcheck: true,
        contextmenu: false,
        setup: function (ed) {
            register(ed);
            ed.on("focus", function () { state.lastEditor = ed; state.lastEditorDirty = onDirty; });
            // A block built inside another block is that block's
            // business, not a block of its own: a paragraph made in a
            // callout is part of the callout, and one made in a column
            // is a block there only if the paragraph it came out of was
            // (which the split itself carries). forced_root_block_attrs
            // cannot say that — it stamps every block TinyMCE makes —
            // so what it stamped too widely comes off here.
            //
            // The twin is the block the new one was split from, which is
            // the one whose answer it should copy: on either side,
            // because Enter at the start of a paragraph puts the new
            // block before it rather than after. A new block with no
            // twin at all is alone inside a container and can only be
            // part of it — and would be dragged out of it by the next
            // unnestSnippets if the marker were left on.
            ed.on("NewBlock", function (e) {
                var b = e.newBlock;
                if (!b || !b.parentNode || b.parentNode === ed.getBody()) return;
                var twin = b.previousElementSibling || b.nextElementSibling;
                if (twin && ed.dom.hasClass(twin, "cms-snippet")) return;
                ed.dom.removeClass(b, "cms-snippet");
                if (!b.getAttribute("class")) b.removeAttribute("class");
            });
            // Blocks arriving on the clipboard inherit the snippet
            // they are pasted into — see pasteHostSnippet.
            // PastePostProcess hands over the parsed fragment while
            // the selection is still where the paste will land.
            ed.on("PastePostProcess", function (e) {
                if (!pasteHostSnippet(ed)) return;
                for (var n = e.node.firstChild; n; n = n.nextSibling) {
                    if (n.nodeType === 1 && ed.dom.isBlock(n)) {
                        ed.dom.addClass(n, "cms-snippet");
                    }
                }
            });
            // Buttons are atomic while editing (contenteditable=
            // false, see lockButtons): their text is edited via
            // the gear dialog only. Strip the lock attribute at
            // serialization so it never reaches saved content.
            ed.on("PreInit", function () {
                // The Styles menu previews each entry with inline styles
                // computed against the page: the page's own text color
                // when a format sets none, plus an explicit transparent
                // background. That leaves half the entries invisible —
                // dark previews (Serif, Monospace, …) on the dark menu,
                // White on the light one. Rewrite each preview into a
                // badge: keep the entry's hue but drag its lightness to
                // the end of the range the menu can show — up on the
                // dark menu, down on the light one — over an opaque
                // pill in the same hue at the other end of that range.
                // Because both ends are absolute, the gap between them
                // is too: any colour a host curates is legible on its
                // own pill (measured over the stock palette: no worse
                // than 5.5:1 on the light skin, 6.3:1 on the dark).
                // The shift can't be phrased as `color: … from
                // currentColor` — a later color declaration makes
                // currentColor resolve to the menu's inherited color,
                // not the entry's — so the entry color is extracted
                // and inlined. Formats with a background of
                // their own (e.g. Highlight) keep their exact colors.
                // Engines without relative color keep the plain menu.
                var getCssText = ed.formatter.getCssText;
                ed.formatter.getCssText = function (format) {
                    var css = getCssText.call(ed.formatter, format).replace(
                        /background-color:\s*(?:transparent|rgba\([^)]*,\s*0\s*\))\s*;?/g, "");
                    // The theme runs this through dom.parseStyle, which
                    // reads ";;" as part of the next property name and
                    // then drops it — never produce empty declarations.
                    if (css && css.charAt(css.length - 1) !== ";") css += ";";
                    if (css.indexOf("background-color") === -1) {
                        var m = /(?:^|;)\s*color:\s*([^;]+)/.exec(css);
                        if (m) {
                            css += "color:oklch(from " + m[1] + " " +
                                (light ? "calc(min(l, 0.45))" : "calc(max(l, 0.8))") + " c h);";
                            // The pill is opaque, not a tint of whatever
                            // is behind it. The row under an entry turns
                            // the skin's selection blue the moment it is
                            // hovered or arrowed onto, and a translucent
                            // pill lets that through: a pale name lands
                            // on bright blue, which is where Blue became
                            // a blue word on a blue row. An opaque pill
                            // in the entry's own hue fixes the pair, so
                            // the name is read against the same backdrop
                            // selected or not. Lightness is absolute for
                            // the same reason the text's is clamped —
                            // one end of the range on each skin — and
                            // the chroma is pulled in so the pill stays
                            // a backdrop rather than competing with the
                            // word on it.
                            css += "background-color:oklch(from " + m[1] + " " +
                                (light ? "0.95 calc(c * 0.25)" : "0.22 calc(c * 0.35)") + " h);";
                        } else {
                            css += "background-color:color-mix(in srgb, currentColor 12%, transparent);";
                        }
                    }
                    css += "padding:2px 8px;border-radius:4px;";
                    return css;
                };
                ed.serializer.addAttributeFilter("contenteditable", function (nodes) {
                    for (var i = 0; i < nodes.length; i++) {
                        var n = nodes[i];
                        if ((n.attr("class") || "").indexOf("cms-btn") !== -1) {
                            n.attr("contenteditable", null);
                        }
                    }
                });
                // cms-col-active tints the column the column tool has
                // hold of. It is a selection, not content, and a save can
                // happen while it is on — the chrome only clears once the
                // request comes back — so it comes off here.
                ed.serializer.addAttributeFilter("class", function (nodes) {
                    for (var i = 0; i < nodes.length; i++) {
                        var n = nodes[i];
                        var cls = n.attr("class") || "";
                        if (cls.indexOf("cms-col-active") === -1) continue;
                        cls = cls.split(/\s+/).filter(function (c) {
                            return c !== "" && c !== "cms-col-active";
                        }).join(" ");
                        n.attr("class", cls || null);
                    }
                });
            });
            // Text size for the block under the cursor. A text label
            // rather than an icon: it sits beside the Styles dropdown,
            // which is also a labelled menu, and the icon pack has
            // nothing that reads as "size".
            ed.ui.registry.addMenuButton("cmstextsize", {
                text: "Size",
                tooltip: "Text size",
                fetch: function (callback) {
                    var cur = currentTextSize(ed);
                    callback(TEXT_SIZES.map(function (sz) {
                        return {
                            type: "togglemenuitem",
                            text: sz.label,
                            active: sz.value === cur,
                            onAction: function () {
                                runWithUndo(ed, function () { applyTextSize(ed, sz.value); });
                                onDirty();
                            },
                        };
                    }));
                },
            });
            // Text colour for the selection: the host's curated colours
            // (as classes) over a picker for anything else (inline).
            // An icon rather than a label, since the icon pack has one
            // that reads as "colour" and two labelled menus in a row
            // was already the widest thing on the toolbar.
            ed.ui.registry.addMenuButton("cmstextcolor", {
                icon: "text-color",
                tooltip: "Text color",
                fetch: function (callback) {
                    var items = colorStyles.map(function (c, i) {
                        var on = ed.formatter.match(colorFmt(i));
                        // The name in its own colour: a menu of words
                        // that only say "Blue" makes the reader hold the
                        // palette in their head, and the palette is the
                        // host's, not one they can be assumed to know.
                        var style = colorPreviewStyle(ed, colorFmt(i));
                        var item = {
                            type: "togglemenuitem",
                            text: c.label,
                            active: on,
                            onAction: function () {
                                runWithUndo(ed, function () {
                                    clearTextColor(ed);
                                    // Ticked already: the click means
                                    // "take it off", and clearing did.
                                    if (!on) ed.formatter.apply(colorFmt(i));
                                });
                                onDirty();
                            },
                        };
                        if (style) item.meta = { style: style };
                        return item;
                    });
                    if (items.length) items.push({ type: "separator" });
                    items.push({
                        type: "menuitem",
                        text: "Custom color\u2026",
                        onAction: function () {
                            // The dialog takes focus, and an inline
                            // editor's selection does not survive that:
                            // note where the words were before opening,
                            // and go back to them either way, so a
                            // cancel leaves the caret where it was.
                            var mark = ed.selection.getBookmark(2, true);
                            openDialog({
                                message: "Text color",
                                okLabel: "Apply",
                                fields: [{ id: "color", label: "Color", type: "color",
                                    value: currentTextColor(ed) }],
                            }).then(function (v) {
                                ed.focus();
                                ed.selection.moveToBookmark(mark);
                                if (!v) return;
                                runWithUndo(ed, function () {
                                    clearTextColor(ed);
                                    // Cleared in the dialog: the colour
                                    // is being taken off, not chosen.
                                    if (v.color) {
                                        ed.formatter.apply(CUSTOM_FMT, { value: v.color });
                                    }
                                });
                                onDirty();
                            });
                        },
                    });
                    items.push({
                        type: "menuitem",
                        text: "Remove color",
                        onAction: function () {
                            runWithUndo(ed, function () { clearTextColor(ed); });
                            onDirty();
                        },
                    });
                    callback(items);
                },
            });
            // Both media buttons skip TinyMCE's URL dialogs and go
            // straight to the CMS media picker (library + upload).
            if (mediaEnabled) {
                // Insert image; with an image selected, replaces it.
                // Both rendition URLs ride along as data attributes so
                // the image gear can swap between the compressed web
                // variant and the full-quality original later.
                ed.ui.registry.addButton("cmsimage", {
                    icon: "image",
                    tooltip: "Insert image",
                    onAction: function () {
                        openPicker("image", function (item) {
                            ed.insertContent('<img src="' + escapeAttr(item.web) +
                                '" alt="' + escapeAttr(item.alt || "") + '" loading="lazy"' +
                                ' data-cms-web="' + escapeAttr(item.web) +
                                '" data-cms-orig="' + escapeAttr(item.original) + '">');
                            onDirty();
                        });
                    },
                });
                // Insert video: a native player for a library MP4/WebM,
            // stored as uploaded. preload="metadata" keeps page loads
            // light; the poster (when the upload captured one) gives
            // the player a face before playback.
            ed.ui.registry.addButton("cmsvideo", {
                icon: "embed",
                tooltip: "Insert video",
                onAction: function () {
                    openPicker("video", function (item) {
                        ed.insertContent('<video controls preload="metadata" src="' +
                            escapeAttr(item.original) + '"' +
                            (item.poster ? ' poster="' + escapeAttr(item.poster) + '"' : "") +
                            "></video>");
                        onDirty();
                    });
                },
            });
            // Link to a document (PDF, office file, ...):
                // selected text becomes the link; with no
                // selection, the filename is inserted as the link.
                // Fill-based glyph: TinyMCE's CSS fills icon paths
                // with currentColor, so stroke-only icons render as
                // solid blobs.
                ed.ui.registry.addIcon("cms-paperclip",
                    '<svg width="24" height="24" viewBox="0 0 24 24">' +
                    '<path d="M16.5 6v11.5c0 2.21-1.79 4-4 4s-4-1.79-4-4V5c0-1.38 1.12-2.5 2.5-2.5s2.5 1.12 2.5 2.5v10.5c0 .55-.45 1-1 1s-1-.45-1-1V6H10v9.5c0 1.38 1.12 2.5 2.5 2.5s2.5-1.12 2.5-2.5V5c0-2.21-1.79-4-4-4S7 2.79 7 5v12.5c0 3.04 2.46 5.5 5.5 5.5s5.5-2.46 5.5-5.5V6h-1.5z"/></svg>');
                ed.ui.registry.addButton("cmsdoc", {
                    icon: "cms-paperclip",
                    tooltip: "Link to a document",
                    onAction: function () {
                        openPicker("file", function (item) {
                            var url = item.original;
                            var selected = ed.selection.getContent({ format: "text" });
                            if (selected && selected.trim() !== "") {
                                ed.execCommand("mceInsertLink", false, url);
                            } else {
                                ed.insertContent('<a href="' + escapeAttr(url) + '">' +
                                    escapeText(item.filename) + "</a>");
                            }
                            onDirty();
                        });
                    },
                });
            }
            // Stamp the table's current look onto every cell the table
            // model creates (initial grid, added rows/columns), and
            // re-walk the table after structural changes — the header
            // toggle renames td<->th without a NewCell event.
            ed.on("NewCell", function (e) {
                var t = e.node.closest ? e.node.closest("table") : null;
                if (t) stampCell(e.node, tableStyleOptions(t));
            });
            ed.on("TableModified", function (e) {
                if (e.table) stampTable(e.table);
                wrapTables(ed.getBody());
            });
            // Keep wrappers in step with edits the table events don't
            // cover: inserts (change fires right after), deletions by
            // command or keyboard, undo/redo, and pre-existing content
            // when the editor attaches.
            ed.on("init change SetContent undo redo", function () {
                wrapTables(ed.getBody());
            });
            // The gear on the in-table toolbar: curated Tailwind looks
            // for the table under the cursor.
            ed.ui.registry.addButton("cmstablegear", {
                icon: "table-classes",
                tooltip: "Table style",
                onAction: function () {
                    var t = ed.dom.getParent(ed.selection.getStart(), "table");
                    if (!t) return;
                    var cur = tableStyleOptions(t);
                    openDialog({
                        message: "Table style",
                        okLabel: "Apply",
                        selects: [
                            { id: "lines", label: "Lines", value: cur.lines, options: [
                                { value: "rows", label: "Row dividers" },
                                { value: "grid", label: "Full grid" },
                                { value: "none", label: "No lines" },
                            ] },
                            { id: "striped", label: "Striped rows", value: cur.striped ? "yes" : "no", options: [
                                { value: "no", label: "Off" },
                                { value: "yes", label: "On" },
                            ] },
                            { id: "density", label: "Density", value: cur.density, options: [
                                { value: "compact", label: "Compact" },
                                { value: "normal", label: "Normal" },
                                { value: "spacious", label: "Spacious" },
                            ] },
                            { id: "width", label: "Width", value: cur.width, options: [
                                { value: "full", label: "Full width" },
                                { value: "fit", label: "Fit content" },
                            ] },
                        ],
                    }).then(function (v) {
                        if (!v) return;
                        runWithUndo(ed, function () {
                            stampTable(t, { lines: v.lines, density: v.density,
                                striped: v.striped === "yes", width: v.width });
                        });
                        onDirty();
                    });
                },
            });
            ed.on("input change undo redo SetContent", function () {
                if (ed.isDirty()) onDirty();
            });
            // Undo, redo and any wholesale content replacement swap the
            // elements out for new ones, so every floating toolbar is
            // left anchored to a node that is no longer in the page —
            // and the column resize handles are left drawn over content
            // that has moved out from under them. Put the chrome away
            // and let the next click raise it against what is really
            // there. (Before the handles existed this was invisible: a
            // stale pill only misbehaved if you pressed something on
            // it. A handle sitting on a boundary that no longer exists
            // is an invitation.)
            ed.on("undo redo SetContent", function () { hideChrome(); });
        },
    };
    // The dropdown replaces TinyMCE's default menu: a built-in Headings
    // submenu (headings are structure, not host-configurable styles),
    // then the host's entries. Heading 1 is there for completeness —
    // a region that carries the page's own title needs it — though most
    // content should start at Heading 2 under the page title.
    // Paragraph is the way back down: the menu applies block formats
    // without a toggle, so reverting a heading needs its own entry.
    opts.style_formats = [{
        title: "Headings",
        items: [
            { title: "Heading 1", block: "h1" },
            { title: "Heading 2", block: "h2" },
            { title: "Heading 3", block: "h3" },
            { title: "Heading 4", block: "h4" },
            { title: "Paragraph", block: "p" },
        ],
    }].concat(styleFormats);
    window.tinymce.init(opts);
}

export function removeRichEditors() {
    Object.keys(state.mceEditors).forEach(function (name) {
        state.mceEditors[name].remove();
    });
    state.mceEditors = {};
    state.sectionEditors.forEach(function (s) { s.ed.remove(); });
    state.sectionEditors = [];
    state.lastEditor = null;
    state.lastEditorDirty = null;
    // Remove injected section toolbars and add-buttons.
    document.querySelectorAll("[data-cms-ui]").forEach(function (el) { el.remove(); });
}
