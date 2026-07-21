/* ------------------------------------------------------------------ *
 * TinyMCE (rich HTML regions)
 * ------------------------------------------------------------------ */

import { state, styleFormats, mediaEnabled, EDITOR_BASE } from "./state.js";
import { api } from "./util.js";
import { markDirty, markSectionsDirty, htmlRegions } from "./editing.js";
import { openPicker } from "./media.js";

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
    var opts = {
        target: el,
        inline: true,
        menubar: false,
        // In inline mode the toolbar floats docked to the region
        // as soon as it gains focus — a click is enough, no text
        // selection needed.
        toolbar: (styleFormats.length ? "styles | " : "") +
            "bold italic | h2 h3 | alignleft aligncenter alignright | bullist numlist | blockquote | link unlink" +
            (mediaEnabled ? " | cmsimage cmsdoc" : "") + " | removeformat",
        fixed_toolbar_container: "#cms-mce-toolbar",
        plugins: "lists link autolink",
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
                ed.serializer.addAttributeFilter("contenteditable", function (nodes) {
                    for (var i = 0; i < nodes.length; i++) {
                        var n = nodes[i];
                        if ((n.attr("class") || "").indexOf("cms-btn") !== -1) {
                            n.attr("contenteditable", null);
                        }
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
            ed.on("input change undo redo SetContent", function () {
                if (ed.isDirty()) onDirty();
            });
        },
    };
    if (styleFormats.length) {
        opts.style_formats = styleFormats; // replaces TinyMCE's default menu
    }
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
