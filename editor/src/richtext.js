/* ------------------------------------------------------------------ *
 * TinyMCE (rich HTML regions)
 * ------------------------------------------------------------------ */

import { state, styleFormats, mediaEnabled, EDITOR_BASE } from "./state.js";
import { api } from "./util.js";
import { markDirty, markSectionsDirty, htmlRegions } from "./editing.js";
import { openPicker } from "./media.js";
import { openDialog } from "./dialogs.js";
import { runWithUndo } from "./undo.js";
import { hideChrome } from "./buttons.js";

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
        toolbar: "styles | bold italic underline strikethrough | alignleft aligncenter alignright | bullist numlist | blockquote hr table | link unlink" +
            (mediaEnabled ? " | cmsimage cmsvideo cmsdoc" : "") + " | removeformat",
        fixed_toolbar_container: "#cms-mce-toolbar",
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
        formats: {
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
        },
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
                // dark menu, down on the light one — over a faint pill
                // tinted from the same color. The shift can't be phrased
                // as `color: … from currentColor` — a later color
                // declaration makes currentColor resolve to the menu's
                // inherited color, not the entry's — so the entry color
                // is extracted and inlined. Formats with a background of
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
                                (light ? "calc(min(l, 0.55))" : "calc(max(l, 0.8))") + " c h);";
                        }
                        css += "background-color:color-mix(in srgb, currentColor 12%, transparent);";
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
