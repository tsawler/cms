/* CMS in-place editor.
 *
 * Injected into pages viewed by logged-in editors. The glue chrome
 * (toolbar, media picker) is rendered inside Shadow DOM so the host page's
 * CSS cannot restyle it. Rich HTML regions are edited with TinyMCE in
 * inline mode — content keeps the page's own styles while a floating
 * selection toolbar provides formatting. TinyMCE is self-hosted alongside
 * this script and loaded lazily on the first press of "Edit page".
 */
(function () {
    "use strict";

    var cfg = document.currentScript.dataset;
    var pageId = cfg.pageId;
    var adminPath = cfg.adminPath || "/admin";
    var csrf = cfg.csrf;
    var mediaEnabled = cfg.media === "1";
    var pageStatus = cfg.status || "draft";

    // The Styles menu: named, on-brand styles configured on the server.
    // Each applies CSS classes, so the site's stylesheet stays in charge.
    var styleFormats = [];
    try {
        (JSON.parse(cfg.styles || "[]") || []).forEach(function (s) {
            if (!s || !s.label || !s.class) return;
            var f = { title: s.label, classes: s.class.split(/\s+/) };
            if (s.block) f.block = s.block;
            else f.inline = "span";
            styleFormats.push(f);
        });
    } catch (e) { /* malformed config: no Styles menu */ }

    var EDITOR_BASE = "/cms/editor/";

    var editing = false;
    var dirty = {}; // region name -> true
    var imageValues = {}; // image region name -> chosen URL
    var pickerHandler = null; // function(mediaItem) while the picker is open
    var pickerKind = "image"; // "image" or "file" while the picker is open
    var mceEditors = {}; // region name -> TinyMCE editor instance
    var lastEditorName = null; // region whose editor most recently had focus
    var snapshot = null; // page state captured when Edit was pressed

    var DOC_ACCEPT = ".pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.odt,.ods,.odp,.txt,.csv,.zip";

    /* ------------------------------------------------------------------ *
     * Toolbar and panels (Shadow DOM)
     * ------------------------------------------------------------------ */

    var host = document.createElement("div");
    host.id = "cms-editor-host";
    var shadow = host.attachShadow({ mode: "open" });
    shadow.innerHTML =
        "<style>" +
        ":host{all:initial}" +
        "*{box-sizing:border-box;font-family:system-ui,-apple-system,'Segoe UI',Roboto,sans-serif}" +
        /* ---- bottom pill bar (dark) ---- */
        ".bar{position:fixed;bottom:20px;left:50%;transform:translateX(-50%);z-index:2147483000;" +
        "display:flex;align-items:center;gap:8px;background:#1c2128;color:#fff;border-radius:999px;" +
        "padding:8px 14px;font-size:13px;line-height:1;box-shadow:0 8px 24px rgba(0,0,0,.35);white-space:nowrap;" +
        "transition:transform .25s ease,opacity .25s ease}" +
        ".bar.min{transform:translateX(-50%) scale(.25);opacity:0;pointer-events:none}" +
        ".fab{position:fixed;bottom:20px;left:50%;z-index:2147483000;width:48px;height:48px;" +
        "border-radius:50%;background:#1c2128;color:#fff;border:none;cursor:pointer;" +
        "box-shadow:0 8px 24px rgba(0,0,0,.35);display:flex;align-items:center;justify-content:center;padding:0;" +
        "transform:translateX(-50%) scale(.25);opacity:0;pointer-events:none;" +
        "transition:transform .25s ease,opacity .25s ease}" +
        ".fab.on{transform:translateX(-50%) scale(1);opacity:1;pointer-events:auto}" +
        ".fab:hover{background:#2a3140}" +
        ".fab svg{width:20px;height:20px;fill:currentColor}" +
        ".brand{font-weight:700;letter-spacing:.04em}" +
        ".chip{padding:3px 10px;border-radius:999px;background:rgba(255,255,255,.14);font-size:12px;text-transform:capitalize}" +
        ".chip.published{background:#1e7e4e}" +
        ".msg{opacity:.75;font-size:12px;max-width:16em;overflow:hidden;text-overflow:ellipsis}" +
        ".bar button{font:inherit;color:#fff;background:transparent;border:1px solid rgba(255,255,255,.28);" +
        "border-radius:999px;padding:5px 12px;cursor:pointer}" +
        ".bar button:hover{background:rgba(255,255,255,.1)}" +
        ".bar button:disabled{opacity:.4;cursor:default}" +
        ".bar button.primary{background:#2f5fe0;border-color:#2f5fe0}" +
        ".bar button.primary:hover{background:#2149b8}" +
        ".bar button.quiet{border-color:transparent;opacity:.7;padding:5px 8px}" +
        ".bar a{color:#fff;opacity:.7;text-decoration:none;font-size:12px}.bar a:hover{opacity:1}" +
        /* ---- media modal (light, centered) ---- */
        ".overlay{position:fixed;inset:0;background:rgba(15,18,25,.5);z-index:2147482998;display:none}" +
        ".overlay.on{display:block}" +
        ".panel{position:fixed;top:50%;left:50%;transform:translate(-50%,-50%);z-index:2147483000;" +
        "width:min(940px,94vw);height:min(640px,88vh);background:#fff;color:#1c2128;" +
        "border-radius:12px;box-shadow:0 16px 48px rgba(0,0,0,.4);display:none;flex-direction:column;overflow:hidden}" +
        ".panel.on{display:flex}" +
        ".panel button{font:inherit;color:#1c2128;background:#fff;border:1px solid #d9dce1;" +
        "border-radius:8px;padding:6px 12px;cursor:pointer;font-size:13px}" +
        ".panel button:hover{background:#f4f5f7}" +
        ".head{display:flex;align-items:center;gap:10px;padding:14px 16px;border-bottom:1px solid #e3e6ea}" +
        ".head h2{margin:0;font-size:15px;white-space:nowrap}" +
        "#search{flex:1;min-width:0;padding:7px 12px;border:1px solid #d9dce1;border-radius:8px;font:inherit;font-size:13px}" +
        ".views{display:flex;gap:2px}" +
        ".views button{padding:6px 10px}" +
        ".views button.on{background:#e8edfb;border-color:#2f5fe0;color:#2149b8}" +
        "#picker-close{border:none;font-size:20px;line-height:1;padding:4px 9px;color:#667085}" +
        "#picker-close:hover{background:#eceef1;color:#1c2128}" +
        ".pbody{flex:1;display:flex;min-height:0}" +
        ".side{width:200px;flex-shrink:0;border-right:1px solid #e3e6ea;overflow-y:auto;padding:10px}" +
        ".side button{display:block;width:100%;text-align:left;border:none;background:none;" +
        "padding:8px 10px;border-radius:8px;font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}" +
        ".side button.on{background:#e8edfb;color:#2149b8;font-weight:600}" +
        ".side button .n{color:#98a2b3;font-size:11px;margin-left:4px}" +
        ".side .newf{color:#667085;margin-top:8px;border-top:1px solid #eceef1;border-radius:0;padding-top:12px}" +
        ".side .newf:hover{color:#2149b8;background:none}" +
        ".main{flex:1;display:flex;flex-direction:column;min-width:0}" +
        ".up{display:flex;gap:8px;align-items:center;padding:10px 14px;border-bottom:1px solid #e3e6ea}" +
        ".up input[type=file]{font-size:12px;min-width:0;flex:1}" +
        ".items{flex:1;overflow-y:auto;padding:14px}" +
        ".items.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(130px,1fr));gap:10px;align-content:start}" +
        ".items.grid figure{margin:0;cursor:pointer;border:2px solid transparent;border-radius:8px;overflow:hidden}" +
        ".items.grid figure:hover{border-color:#2f5fe0}" +
        ".items.grid img{display:block;width:100%;height:92px;object-fit:cover}" +
        ".items.grid .doc{display:flex;align-items:center;justify-content:center;height:92px;background:#f4f5f7;font-size:30px}" +
        ".items.grid .doc span{font-size:11px;font-weight:700;color:#667085;margin-left:4px;text-transform:uppercase}" +
        ".items.grid figcaption{font-size:11px;padding:4px 6px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}" +
        ".items.list .row{display:flex;align-items:center;gap:12px;padding:7px 10px;border-radius:8px;cursor:pointer}" +
        ".items.list .row:hover{background:#f4f5f7}" +
        ".items.list img{width:44px;height:44px;object-fit:cover;border-radius:6px;flex-shrink:0}" +
        ".items.list .doc{width:44px;height:44px;display:flex;align-items:center;justify-content:center;" +
        "background:#f4f5f7;border-radius:6px;font-size:20px;flex-shrink:0}" +
        ".items.list .nm{flex:1;font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}" +
        ".items.list .sz{color:#667085;font-size:12px;white-space:nowrap}" +
        ".empty{color:#667085;font-size:13px}" +
        /* ---- snippet drawer (non-modal, so drag-and-drop can reach the page) ---- */
        ".drawer{position:fixed;top:0;right:0;bottom:0;width:300px;z-index:2147482997;" +
        "background:#fff;color:#1c2128;box-shadow:-8px 0 32px rgba(0,0,0,.18);" +
        "display:flex;flex-direction:column;transform:translateX(105%);transition:transform .25s ease}" +
        ".drawer.on{transform:translateX(0)}" +
        ".drawer .dhead{display:flex;align-items:center;justify-content:space-between;" +
        "padding:14px 16px;border-bottom:1px solid #e3e6ea}" +
        ".drawer .dhead h2{margin:0;font-size:15px}" +
        ".drawer .dhead button{border:none;background:none;font-size:20px;line-height:1;" +
        "padding:4px 9px;color:#667085;cursor:pointer;border-radius:6px}" +
        ".drawer .dhead button:hover{background:#eceef1;color:#1c2128}" +
        ".drawer .dhint{padding:10px 16px;font-size:12px;color:#667085;border-bottom:1px solid #eceef1}" +
        ".drawer .dlist{flex:1;overflow-y:auto;padding:12px}" +
        ".snip{border:1px solid #d9dce1;border-radius:10px;padding:12px;margin-bottom:10px;" +
        "cursor:grab;background:#fff}" +
        ".snip:hover{border-color:#2f5fe0;box-shadow:0 2px 8px rgba(47,95,224,.12)}" +
        ".snip .sname{font-size:13px;font-weight:600;margin:0 0 3px}" +
        ".snip .sdesc{font-size:11px;color:#667085;margin:0;overflow:hidden;display:-webkit-box;" +
        "-webkit-line-clamp:2;-webkit-box-orient:vertical}" +
        /* ---- small dialog (replaces window.confirm / window.prompt) ---- */
        ".dlg-overlay{position:fixed;inset:0;background:rgba(15,18,25,.5);z-index:2147483001;display:none}" +
        ".dlg-overlay.on{display:block}" +
        ".dlg{position:fixed;top:50%;left:50%;transform:translate(-50%,-50%);z-index:2147483002;" +
        "width:min(400px,92vw);background:#fff;color:#1c2128;border-radius:12px;" +
        "box-shadow:0 16px 48px rgba(0,0,0,.4);padding:20px;display:none}" +
        ".dlg.on{display:block}" +
        ".dlg p{margin:0 0 14px;font-size:14px;line-height:1.45}" +
        ".dlg input{width:100%;padding:8px 12px;border:1px solid #d9dce1;border-radius:8px;" +
        "font:inherit;font-size:13px;margin-bottom:14px}" +
        ".dlg input:focus{outline:2px solid #2f5fe0;border-color:#2f5fe0}" +
        ".dlg .acts{display:flex;justify-content:flex-end;gap:8px}" +
        ".dlg button{font:inherit;color:#1c2128;background:#fff;border:1px solid #d9dce1;" +
        "border-radius:8px;padding:7px 16px;cursor:pointer;font-size:13px}" +
        ".dlg button:hover{background:#f4f5f7}" +
        ".dlg button.ok{background:#2f5fe0;border-color:#2f5fe0;color:#fff}" +
        ".dlg button.ok:hover{background:#2149b8}" +
        ".dlg button.ok.danger{background:#c0392b;border-color:#c0392b}" +
        ".dlg button.ok.danger:hover{background:#a03024}" +
        "</style>" +
        '<div class="bar" id="bar">' +
        '<span class="brand">CMS</span>' +
        '<span class="chip" id="chip"></span>' +
        '<span class="msg" id="msg"></span>' +
        '<button id="edit">Edit page</button>' +
        '<button id="snippets" hidden>Snippets</button>' +
        '<button id="cancel" hidden>Cancel</button>' +
        '<button id="save" disabled>Save draft</button>' +
        '<button id="publish" class="primary">Publish</button>' +
        '<a id="admin" href="#">Admin</a>' +
        '<button id="close" class="quiet" title="Minimize editing tools">×</button>' +
        "</div>" +
        '<button class="fab" id="fab" title="Show editing tools" aria-label="Show editing tools">' +
        '<svg viewBox="0 0 24 24"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04c.39-.39.39-1.02 0-1.41l-2.34-2.34a.9959.9959 0 00-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z"/></svg>' +
        "</button>" +
        '<div class="overlay" id="overlay"></div>' +
        '<div class="panel" id="picker">' +
        '<div class="head">' +
        '<h2 id="picker-title">Choose an image</h2>' +
        '<input type="search" id="search" placeholder="Search by name…">' +
        '<div class="views">' +
        '<button id="view-grid" title="Grid view" aria-label="Grid view">▦</button>' +
        '<button id="view-list" title="List view" aria-label="List view">≡</button>' +
        "</div>" +
        '<button id="picker-close" title="Close" aria-label="Close">×</button>' +
        "</div>" +
        '<div class="pbody">' +
        '<div class="side" id="folders"></div>' +
        '<div class="main">' +
        '<div class="up"><input type="file" id="file" accept="image/*"><button id="upload">Upload to this folder</button></div>' +
        '<div class="items grid" id="grid"></div>' +
        "</div></div></div>" +
        '<div class="drawer" id="drawer">' +
        '<div class="dhead"><h2>Snippets</h2>' +
        '<button id="drawer-close" title="Close" aria-label="Close">×</button></div>' +
        '<div class="dhint">Drag a snippet onto the page, or click one to insert it at the cursor.</div>' +
        '<div class="dlist" id="snip-list"></div>' +
        "</div>" +
        '<div class="dlg-overlay" id="dlg-overlay"></div>' +
        '<div class="dlg" id="dlg" role="dialog" aria-modal="true">' +
        '<p id="dlg-msg"></p>' +
        '<input type="text" id="dlg-input" hidden>' +
        '<div class="acts">' +
        '<button id="dlg-cancel">Cancel</button>' +
        '<button id="dlg-ok" class="ok">OK</button>' +
        "</div></div>";
    document.documentElement.appendChild(host);

    var $ = function (id) { return shadow.getElementById(id); };
    $("admin").href = adminPath + "/";
    setChip(pageStatus);

    /* ------------------------------------------------------------------ *
     * Dialogs — styled replacements for window.confirm / window.prompt
     * ------------------------------------------------------------------ */

    var dlgResolve = null;
    var dlgIsPrompt = false;

    function openDialog(opts) {
        return new Promise(function (resolve) {
            dlgResolve = resolve;
            dlgIsPrompt = !!opts.prompt;
            $("dlg-msg").textContent = opts.message;
            var input = $("dlg-input");
            input.hidden = !opts.prompt;
            input.value = "";
            input.placeholder = opts.placeholder || "";
            var ok = $("dlg-ok");
            ok.textContent = opts.okLabel || "OK";
            ok.classList.toggle("danger", !!opts.danger);
            $("dlg-overlay").classList.add("on");
            $("dlg").classList.add("on");
            (opts.prompt ? input : ok).focus();
        });
    }

    function settleDialog(value) {
        if (!dlgResolve) return;
        $("dlg-overlay").classList.remove("on");
        $("dlg").classList.remove("on");
        var resolve = dlgResolve;
        dlgResolve = null;
        resolve(value);
    }

    // cmsConfirm resolves true/false; cmsPrompt resolves the entered text
    // or null when dismissed.
    function cmsConfirm(message, okLabel, danger) {
        return openDialog({ message: message, okLabel: okLabel, danger: danger });
    }
    function cmsPrompt(message, placeholder, okLabel) {
        return openDialog({ message: message, prompt: true, placeholder: placeholder, okLabel: okLabel });
    }

    function dialogOK() { settleDialog(dlgIsPrompt ? $("dlg-input").value.trim() : true); }
    function dialogDismiss() { settleDialog(dlgIsPrompt ? null : false); }

    $("dlg-ok").addEventListener("click", dialogOK);
    $("dlg-cancel").addEventListener("click", dialogDismiss);
    $("dlg-overlay").addEventListener("click", dialogDismiss);
    $("dlg").addEventListener("keydown", function (e) {
        if (e.key === "Enter") {
            e.preventDefault();
            dialogOK();
        }
    });

    /* Fixed strip TinyMCE renders its toolbar into (light DOM — TinyMCE
     * can't reach into our shadow root). Pinned to the top of the
     * viewport so the toolbar never covers the region being edited;
     * TinyMCE still shows/hides it as regions gain and lose focus. */
    var mceBar = document.createElement("div");
    mceBar.id = "cms-mce-toolbar";
    // Full-width flex strip (not translateX centering — TinyMCE measures
    // the space available to the container, and a half-offset container
    // makes it overflow buttons into a "…" menu). pointer-events pass
    // through the empty strip; the toolbar itself re-enables them via
    // lightCss below.
    mceBar.style.cssText =
        "position:fixed;top:12px;left:0;right:0;display:flex;justify-content:center;" +
        "z-index:2147483000;pointer-events:none;";
    // Must live inside <body>: TinyMCE resolves fixed_toolbar_container
    // by searching the body only.
    document.body.appendChild(mceBar);

    /* Light-DOM styles for region outlines while editing. */
    var lightCss = document.createElement("style");
    lightCss.textContent =
        ".cms-editing [data-cms-region]{outline:1.5px dashed rgba(47,95,224,.6);outline-offset:3px;min-height:1em}" +
        ".cms-editing [data-cms-region]:hover,.cms-editing [data-cms-region]:focus{outline-style:solid}" +
        ".cms-editing [data-cms-region]:empty::before{content:'Click to edit…';opacity:.4}" +
        ".cms-editing [data-cms-image]{outline:1.5px dashed rgba(224,122,47,.75);outline-offset:3px;cursor:pointer}" +
        ".cms-editing [data-cms-image]:hover{outline-style:solid}" +
        /* TinyMCE inline adds its own focus outline; ours is enough. */
        ".cms-editing [data-cms-region].mce-edit-focus{outline:1.5px solid rgba(47,95,224,.6)}" +
        "#cms-mce-toolbar > *{pointer-events:auto;box-shadow:0 4px 16px rgba(0,0,0,.18);border-radius:8px}";
    document.head.appendChild(lightCss);

    /* ------------------------------------------------------------------ *
     * TinyMCE (rich HTML regions)
     * ------------------------------------------------------------------ */

    var tinyLoading = null;

    function loadTinyMCE() {
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

    function escapeAttr(s) {
        return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
    }
    function escapeText(s) {
        return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    }

    function initRichEditors() {
        htmlRegions().forEach(function (el) {
            var name = el.dataset.cmsRegion;
            if (mceEditors[name]) return;
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
                // Never rewrite URLs relative to the current page — media
                // lives at absolute paths like /cms/media/… and relative
                // forms would break on nested pages.
                convert_urls: false,
                link_default_protocol: "https",
                browser_spellcheck: true,
                contextmenu: false,
                setup: function (ed) {
                    mceEditors[name] = ed;
                    ed.on("focus", function () { lastEditorName = name; });
                    // Both media buttons skip TinyMCE's URL dialogs and go
                    // straight to the CMS media picker (library + upload).
                    if (mediaEnabled) {
                        // Insert image; with an image selected, replaces it.
                        ed.ui.registry.addButton("cmsimage", {
                            icon: "image",
                            tooltip: "Insert image",
                            onAction: function () {
                                openPicker("image", function (item) {
                                    ed.insertContent('<img src="' + escapeAttr(item.web) +
                                        '" alt="' + escapeAttr(item.alt || "") + '">');
                                    markDirty(name);
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
                                    markDirty(name);
                                });
                            },
                        });
                    }
                    ed.on("input change undo redo SetContent", function () {
                        if (ed.isDirty()) markDirty(name);
                    });
                },
            };
            if (styleFormats.length) {
                opts.style_formats = styleFormats; // replaces TinyMCE's default menu
            }
            window.tinymce.init(opts);
        });
    }

    function removeRichEditors() {
        Object.keys(mceEditors).forEach(function (name) {
            mceEditors[name].remove();
        });
        mceEditors = {};
    }

    /* ------------------------------------------------------------------ *
     * Editing state
     * ------------------------------------------------------------------ */

    function textRegions() {
        return Array.prototype.slice.call(
            document.querySelectorAll('[data-cms-region][data-cms-kind="text"]'));
    }
    function htmlRegions() {
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
        return { regs: regs, imgs: imgs };
    }

    function restoreSnapshot(s) {
        s.regs.forEach(function (r) { r.el.innerHTML = r.html; });
        s.imgs.forEach(function (i) {
            if (i.src === null) i.el.removeAttribute("src");
            else i.el.setAttribute("src", i.src);
            if (i.alt === null) i.el.removeAttribute("alt");
            else i.el.setAttribute("alt", i.alt);
        });
    }

    function setEditing(on) {
        editing = on;
        if (on) snapshot = takeSnapshot();
        document.body.classList.toggle("cms-editing", on);
        $("edit").textContent = on ? "Done editing" : "Edit page";
        $("cancel").hidden = !on;
        $("snippets").hidden = !on;
        if (!on) closeDrawer();
        textRegions().forEach(function (el) {
            if (on) {
                el.setAttribute("contenteditable", "plaintext-only");
                if (!el.isContentEditable) el.setAttribute("contenteditable", "true"); // Firefox
            } else {
                el.removeAttribute("contenteditable");
            }
        });
        if (on) {
            setMsg("Loading editor…");
            loadTinyMCE().then(function () {
                initRichEditors();
                setMsg("");
            }).catch(function (err) { setMsg(err.message); });
        } else {
            removeRichEditors();
        }
    }

    function markDirty(name) {
        dirty[name] = true;
        $("save").disabled = false;
        setMsg("Unsaved changes");
    }

    document.addEventListener("input", function (e) {
        if (!editing) return;
        var el = e.target.closest ? e.target.closest('[data-cms-region][data-cms-kind="text"]') : null;
        if (el) markDirty(el.dataset.cmsRegion);
    });

    document.addEventListener("click", function (e) {
        if (!editing || !mediaEnabled) return;
        var img = e.target.closest ? e.target.closest("[data-cms-image]") : null;
        if (!img) return;
        e.preventDefault();
        e.stopPropagation();
        var name = img.getAttribute("data-cms-image");
        openPicker("image", function (item) {
            img.src = item.web;
            if (item.alt) img.alt = item.alt;
            imageValues[name] = item.web;
            markDirty(name);
        });
    }, true);

    window.addEventListener("beforeunload", function (e) {
        if (Object.keys(dirty).length > 0) {
            e.preventDefault();
            e.returnValue = "";
        }
    });

    /* ------------------------------------------------------------------ *
     * Saving and publishing
     * ------------------------------------------------------------------ */

    function setMsg(text) { $("msg").textContent = text || ""; }
    function setChip(status) {
        var chip = $("chip");
        chip.textContent = status;
        chip.classList.toggle("published", status === "published");
    }

    function collect() {
        var values = {};
        Object.keys(dirty).forEach(function (name) {
            if (mceEditors[name]) {
                values[name] = mceEditors[name].getContent();
                return;
            }
            var el = document.querySelector('[data-cms-region="' + name + '"]');
            if (el) {
                values[name] = el.dataset.cmsKind === "text" ? el.textContent : el.innerHTML;
                return;
            }
            if (imageValues[name] !== undefined) values[name] = imageValues[name];
        });
        return values;
    }

    function api(path, options) {
        options = options || {};
        options.headers = options.headers || {};
        options.headers["X-CSRF-Token"] = csrf;
        options.credentials = "same-origin";
        return fetch(adminPath + "/api" + path, options).then(function (res) {
            var type = res.headers.get("Content-Type") || "";
            if (type.indexOf("application/json") === -1) {
                throw new Error("Your session may have expired — please log in to the admin again.");
            }
            return res.json().then(function (body) {
                if (!res.ok) throw new Error(body.error || "Something went wrong.");
                return body;
            });
        });
    }

    function save() {
        var values = collect();
        if (Object.keys(values).length === 0) return Promise.resolve();
        setMsg("Saving…");
        return api("/pages/" + pageId + "/regions", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ locale: cfg.locale, regions: values }),
        }).then(function () {
            dirty = {};
            Object.keys(mceEditors).forEach(function (name) {
                mceEditors[name].setDirty(false);
            });
            $("save").disabled = true;
            setMsg("Draft saved");
        });
    }

    function publish() {
        save().then(function () {
            setMsg("Publishing…");
            return api("/pages/" + pageId + "/publish", { method: "POST" });
        }).then(function () {
            setChip("published");
            setMsg("Published ✓");
        }).catch(function (err) { setMsg(err.message); });
    }

    $("edit").addEventListener("click", function () { setEditing(!editing); });
    $("cancel").addEventListener("click", function () {
        var discard = function () {
            var snap = snapshot;
            setEditing(false); // tears down TinyMCE before the DOM is restored
            if (snap) restoreSnapshot(snap);
            dirty = {};
            imageValues = {};
            $("save").disabled = true;
            setMsg("");
        };
        if (Object.keys(dirty).length > 0) {
            cmsConfirm("Discard your unsaved changes? The page will go back to how it was before you started editing.",
                "Discard changes", true).then(function (yes) {
                if (yes) discard();
            });
        } else {
            discard();
        }
    });
    $("save").addEventListener("click", function () {
        save().catch(function (err) { setMsg(err.message); });
    });
    $("publish").addEventListener("click", publish);
    // The × doesn't remove the toolbar — it minimizes it (animated) to a
    // pencil button in the same spot; clicking that brings the bar back.
    // Minimizing exits edit mode for a clean view of the page, but any
    // unsaved changes stay in the page and Save stays available.
    $("close").addEventListener("click", function () {
        closePicker();
        setEditing(false);
        $("bar").classList.add("min");
        $("fab").classList.add("on");
    });
    $("fab").addEventListener("click", function () {
        $("fab").classList.remove("on");
        $("bar").classList.remove("min");
        if (Object.keys(dirty).length > 0) {
            $("save").disabled = false;
            setMsg("Unsaved changes");
        }
    });

    /* ------------------------------------------------------------------ *
     * Snippet drawer
     * ------------------------------------------------------------------ */

    var snippetsLoaded = false;

    function openDrawer() {
        $("drawer").classList.add("on");
        if (!snippetsLoaded) loadSnippets();
    }
    function closeDrawer() {
        $("drawer").classList.remove("on");
    }

    $("snippets").addEventListener("click", function () {
        if ($("drawer").classList.contains("on")) closeDrawer();
        else openDrawer();
    });
    $("drawer-close").addEventListener("click", closeDrawer);

    function loadSnippets() {
        var list = $("snip-list");
        list.innerHTML = '<span class="empty">Loading…</span>';
        api("/snippets", { method: "GET" }).then(function (body) {
            snippetsLoaded = true;
            list.innerHTML = "";
            if (!body.snippets || body.snippets.length === 0) {
                list.innerHTML = '<span class="empty">No snippets available.</span>';
                return;
            }
            body.snippets.forEach(function (sn) {
                var card = document.createElement("div");
                card.className = "snip";
                card.draggable = true;
                var nm = document.createElement("p");
                nm.className = "sname";
                nm.textContent = sn.name;
                var desc = document.createElement("p");
                desc.className = "sdesc";
                var probe = document.createElement("div");
                probe.innerHTML = sn.html;
                desc.textContent = (probe.textContent || "").trim().replace(/\s+/g, " ").slice(0, 90);
                card.appendChild(nm);
                card.appendChild(desc);
                // Drag: TinyMCE accepts text/html drops and inserts the
                // markup at the drop caret inside any rich region.
                card.addEventListener("dragstart", function (e) {
                    e.dataTransfer.setData("text/html", sn.html);
                    e.dataTransfer.setData("text/plain", sn.name);
                    e.dataTransfer.effectAllowed = "copy";
                });
                // Click: insert at the cursor in the most recently focused
                // rich region (or the first one).
                card.addEventListener("click", function () { insertSnippet(sn); });
                list.appendChild(card);
            });
        }).catch(function (err) {
            list.innerHTML = "";
            var span = document.createElement("span");
            span.className = "empty";
            span.textContent = err.message;
            list.appendChild(span);
        });
    }

    function insertSnippet(sn) {
        var name = lastEditorName;
        if (!name || !mceEditors[name]) {
            name = Object.keys(mceEditors)[0];
        }
        var ed = name && mceEditors[name];
        if (!ed) {
            setMsg("Click into a content area first, then insert the snippet.");
            return;
        }
        ed.focus();
        ed.insertContent(sn.html);
        markDirty(name);
        setMsg("Snippet inserted — click it to edit the text");
    }

    /* ------------------------------------------------------------------ *
     * Media picker
     * ------------------------------------------------------------------ */

    var pickerFolder = ""; // "" = all files, "root" = unfiled, or a folder id
    var pickerQuery = "";
    var pickerView = "grid";
    try {
        pickerView = window.localStorage.getItem("cms-media-view") === "list" ? "list" : "grid";
    } catch (e) { /* private mode */ }

    function openPicker(kind, handler) {
        pickerKind = kind;
        pickerHandler = handler;
        pickerFolder = "";
        pickerQuery = "";
        $("search").value = "";
        $("picker-title").textContent = kind === "file" ? "Choose a document" : "Choose an image";
        $("file").setAttribute("accept", kind === "file" ? DOC_ACCEPT : "image/*");
        $("overlay").classList.add("on");
        $("picker").classList.add("on");
        applyView();
        loadFolders();
        loadMedia();
    }
    function closePicker() {
        $("picker").classList.remove("on");
        $("overlay").classList.remove("on");
        pickerHandler = null;
    }

    function applyView() {
        $("grid").className = "items " + pickerView;
        $("view-grid").classList.toggle("on", pickerView === "grid");
        $("view-list").classList.toggle("on", pickerView === "list");
    }
    function setView(v) {
        pickerView = v;
        try { window.localStorage.setItem("cms-media-view", v); } catch (e) { /* ignore */ }
        applyView();
        renderItems(lastItems);
    }
    $("view-grid").addEventListener("click", function () { setView("grid"); });
    $("view-list").addEventListener("click", function () { setView("list"); });

    var searchTimer = null;
    $("search").addEventListener("input", function () {
        clearTimeout(searchTimer);
        searchTimer = setTimeout(function () {
            pickerQuery = $("search").value.trim();
            loadMedia();
        }, 250);
    });

    function loadFolders() {
        api("/media/folders", { method: "GET" }).then(function (body) {
            renderFolders(body.folders || []);
        }).catch(function () {
            renderFolders([]);
        });
    }

    function renderFolders(folders) {
        var side = $("folders");
        side.innerHTML = "";
        var add = function (label, value, count) {
            var btn = document.createElement("button");
            btn.textContent = label;
            if (count !== undefined) {
                var n = document.createElement("span");
                n.className = "n";
                n.textContent = count;
                btn.appendChild(n);
            }
            btn.classList.toggle("on", pickerFolder === value);
            btn.addEventListener("click", function () {
                pickerFolder = value;
                renderFolders(folders);
                loadMedia();
            });
            side.appendChild(btn);
            return btn;
        };
        add("All files", "");
        add("Unfiled", "root");
        folders.forEach(function (f) { add(f.name, String(f.id), f.count); });

        var newf = document.createElement("button");
        newf.className = "newf";
        newf.textContent = "＋ New folder";
        newf.addEventListener("click", function () {
            cmsPrompt("Name the new folder", "e.g. Hero images", "Create folder").then(function (name) {
                if (!name) return;
                api("/media/folders", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ name: name }),
                }).then(function (body) {
                    pickerFolder = String(body.folder.id);
                    loadFolders();
                    loadMedia();
                }).catch(function (err) { setMsg(err.message); });
            });
        });
        side.appendChild(newf);
    }

    var lastItems = [];

    function loadMedia() {
        var grid = $("grid");
        grid.innerHTML = '<span class="empty">Loading…</span>';
        var params = "?kind=" + pickerKind +
            (pickerQuery ? "&q=" + encodeURIComponent(pickerQuery) : "") +
            (pickerFolder ? "&folder=" + encodeURIComponent(pickerFolder) : "");
        api("/media" + params, { method: "GET" }).then(function (body) {
            lastItems = body.media || [];
            renderItems(lastItems);
        }).catch(function (err) {
            grid.innerHTML = "";
            var span = document.createElement("span");
            span.className = "empty";
            span.textContent = err.message;
            grid.appendChild(span);
        });
    }

    function docBadge(item, forList) {
        var doc = document.createElement("div");
        doc.className = "doc";
        doc.textContent = "📄";
        if (!forList) {
            var ext = document.createElement("span");
            ext.textContent = item.filename.split(".").pop();
            doc.appendChild(ext);
        }
        return doc;
    }

    function renderItems(items) {
        var grid = $("grid");
        grid.innerHTML = "";
        if (!items || items.length === 0) {
            grid.innerHTML = '<span class="empty">Nothing here — upload a file above, or pick another folder.</span>';
            return;
        }
        items.forEach(function (item) {
            var el;
            if (pickerView === "list") {
                el = document.createElement("div");
                el.className = "row";
                if (item.kind === "file") {
                    el.appendChild(docBadge(item, true));
                } else {
                    var img = document.createElement("img");
                    img.src = item.thumb;
                    img.alt = item.alt || "";
                    el.appendChild(img);
                }
                var nm = document.createElement("span");
                nm.className = "nm";
                nm.textContent = item.filename;
                var sz = document.createElement("span");
                sz.className = "sz";
                sz.textContent = item.kind === "file" ? item.size : item.width + "×" + item.height;
                el.appendChild(nm);
                el.appendChild(sz);
            } else {
                el = document.createElement("figure");
                if (item.kind === "file") {
                    el.appendChild(docBadge(item, false));
                } else {
                    var gimg = document.createElement("img");
                    gimg.src = item.thumb;
                    gimg.alt = item.alt || "";
                    el.appendChild(gimg);
                }
                var cap = document.createElement("figcaption");
                cap.textContent = item.filename + (item.kind === "file" ? " · " + item.size : "");
                el.appendChild(cap);
            }
            el.addEventListener("click", function () { pick(item); });
            grid.appendChild(el);
        });
    }

    function pick(item) {
        if (pickerHandler) pickerHandler(item);
        closePicker();
    }

    $("upload").addEventListener("click", function () {
        var input = $("file");
        if (!input.files || input.files.length === 0) return;
        var fd = new FormData();
        fd.append("file", input.files[0]);
        // Upload into the folder being viewed (root/all -> unfiled).
        if (pickerFolder && pickerFolder !== "root") fd.append("folder", pickerFolder);
        setMsg("Uploading…");
        api("/media", { method: "POST", body: fd }).then(function (body) {
            setMsg("File uploaded");
            input.value = "";
            if (body.media) pick(body.media);
            else loadMedia();
        }).catch(function (err) { setMsg(err.message); });
    });

    $("picker-close").addEventListener("click", closePicker);
    $("overlay").addEventListener("click", closePicker);

    document.addEventListener("keydown", function (e) {
        if (e.key !== "Escape") return;
        if (dlgResolve) {
            dialogDismiss(); // an open dialog captures Escape before the picker
            return;
        }
        closePicker();
    });
})();
