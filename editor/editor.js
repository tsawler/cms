/* CMS in-place editor.
 *
 * Injected into pages viewed by logged-in editors. The glue chrome
 * (toolbar, media picker) is rendered inside Shadow DOM so the host page's
 * CSS cannot restyle it. Rich HTML regions are edited with TinyMCE in
 * inline mode — content keeps the page's own styles while a floating
 * selection toolbar provides formatting. TinyMCE is self-hosted alongside
 * this script and loaded lazily on the first press of "Edit".
 */
(function () {
    "use strict";

    var cfg = document.currentScript.dataset;
    var pageId = cfg.pageId;
    var adminPath = cfg.adminPath || "/admin";
    var csrf = cfg.csrf;
    var mediaEnabled = cfg.media === "1";
    var pageStatus = cfg.status || "draft";
    // True when a published page's saved draft differs from what's live.
    var hasUnpublished = cfg.unpublished === "1";

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
    var sectionEditors = []; // {el, ed, region} per section content container
    var lastEditor = null; // most recently focused TinyMCE instance
    var lastEditorDirty = null; // dirty-marker matching lastEditor
    var sectionsDirty = {}; // sections region name -> true
    var pendingSection = null; // {region, after} while choosing a new section's start
    var snapshot = null; // page state captured when Edit was pressed

    // Curated section settings (backgrounds/widths) from the server.
    var sectionStyles = { backgrounds: [{ key: "default", label: "Default", class: "" }],
        widths: [{ key: "normal", label: "Normal", class: "" }] };
    try {
        var ss = JSON.parse(cfg.sectionStyles || "null");
        if (ss && ss.backgrounds && ss.backgrounds.length && ss.widths && ss.widths.length) {
            sectionStyles = ss;
        }
    } catch (e) { /* fall back to the minimal defaults above */ }

    // Page templates the site offers, for the "new page" dialog.
    var pageTemplates = [];
    try {
        pageTemplates = JSON.parse(cfg.pageTemplates || "[]") || [];
    } catch (e) { /* no new-page button */ }

    var DOC_ACCEPT = ".pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.odt,.ods,.odp,.txt,.csv,.zip";

    /* ------------------------------------------------------------------ *
     * Toolbar and panels (Shadow DOM)
     * ------------------------------------------------------------------ */

    // Small inline icons for the bar (filled with currentColor via CSS).
    var ICONS = {
        pencil: '<svg viewBox="0 0 24 24"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04c.39-.39.39-1.02 0-1.41l-2.34-2.34a.9959.9959 0 00-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z"/></svg>',
        check: '<svg viewBox="0 0 24 24"><path d="M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z"/></svg>',
        hide: '<svg viewBox="0 0 24 24"><path d="M7.4 8.6 12 13.2l4.6-4.6L18 10l-6 6-6-6z"/></svg>',
    };

    var host = document.createElement("div");
    host.id = "cms-editor-host";
    var shadow = host.attachShadow({ mode: "open" });
    shadow.innerHTML =
        "<style>" +
        ":host{all:initial}" +
        "*{box-sizing:border-box;font-family:system-ui,-apple-system,'Segoe UI',Roboto,sans-serif}" +
        /* Author display rules (inline-flex buttons etc.) would otherwise
           beat the UA's [hidden]{display:none}. */
        "[hidden]{display:none!important}" +
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
        ".chip{padding:3px 10px;border-radius:999px;background:rgba(255,255,255,.14);font-size:12px;text-transform:capitalize}" +
        ".chip.published{background:#1e7e4e}" +
        ".chip.changes{background:#b45309;text-transform:none}" +
        ".msg{opacity:.75;font-size:12px;max-width:14em;overflow:hidden;text-overflow:ellipsis}" +
        ".bar button{font:inherit;color:#fff;background:transparent;border:1px solid rgba(255,255,255,.28);" +
        "border-radius:999px;padding:5px 12px;cursor:pointer;display:inline-flex;align-items:center;gap:6px}" +
        ".bar button svg{width:13px;height:13px;fill:currentColor;flex-shrink:0}" +
        ".bar .ic{display:inline-flex}" +
        ".bar button:hover{background:rgba(255,255,255,.1)}" +
        ".bar button:disabled{opacity:.4;cursor:default}" +
        ".bar button.primary{background:#2f5fe0;border-color:#2f5fe0}" +
        ".bar button.primary:hover{background:#2149b8}" +
        ".bar button.quiet{border-color:transparent;opacity:.7;padding:5px 8px}" +
        /* Amber ring on Save while there are unsaved changes. */
        ".bar button.attn{border-color:#f0b429}" +
        /* ---- overflow (⋯) menu: rare and destructive actions ---- */
        ".more{position:relative;display:inline-flex}" +
        "#more{font-size:16px;padding:4px 10px;line-height:1}" +
        ".menu{position:absolute;bottom:calc(100% + 12px);right:0;display:none;flex-direction:column;" +
        "background:#242a33;border-radius:12px;padding:6px;min-width:190px;box-shadow:0 8px 24px rgba(0,0,0,.4)}" +
        ".menu.on{display:flex}" +
        ".menu button,.menu a{font:inherit;font-size:13px;color:#fff;background:none;border:none;border-radius:8px;" +
        "padding:8px 12px;text-align:left;cursor:pointer;white-space:nowrap;text-decoration:none;display:block}" +
        ".menu button:hover,.menu a:hover{background:rgba(255,255,255,.1)}" +
        ".menu button.dngr{color:#fca5a5}" +
        ".menu button.dngr:hover{background:rgba(252,165,165,.15)}" +
        ".menu hr{border:none;border-top:1px solid rgba(255,255,255,.12);margin:4px 6px}" +
        /* ---- tool rail (left edge, edit mode only) ---- */
        ".rail{position:fixed;top:0;left:0;bottom:0;width:56px;z-index:2147482999;" +
        "background:#1c2128;display:none;flex-direction:column;align-items:center;" +
        "padding-top:14px;gap:6px;box-shadow:2px 0 12px rgba(0,0,0,.25)}" +
        ".rail.on{display:flex}" +
        ".rail button{width:46px;height:46px;border-radius:10px;border:none;background:transparent;" +
        "color:#fff;cursor:pointer;display:flex;flex-direction:column;align-items:center;" +
        "justify-content:center;gap:3px;font-size:17px;line-height:1;padding:0}" +
        ".rail button span{font-size:9px;opacity:.75;letter-spacing:.02em}" +
        ".rail button:hover:not(:disabled){background:rgba(255,255,255,.14)}" +
        ".rail button.on{background:rgba(255,255,255,.2)}" +
        ".rail button:disabled{opacity:.35;cursor:default}" +
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
        /* ---- snippet drawer: slides out leftward from beside the tool
           rail (non-modal, so drag-and-drop can reach the page) ---- */
        ".drawer{position:fixed;top:0;left:56px;bottom:0;width:300px;z-index:2147482997;" +
        "background:#fff;color:#1c2128;box-shadow:8px 0 32px rgba(0,0,0,.18);" +
        "display:flex;flex-direction:column;transform:translateX(-130%);transition:transform .25s ease}" +
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
        "#snip-empty{border-style:dashed}" +
        ".sgroup{font-size:11px;font-weight:600;color:#98a2b3;text-transform:uppercase;" +
        "letter-spacing:.05em;margin:14px 0 8px}" +
        /* ---- menu panel rows ---- */
        ".mfoot{display:flex;gap:8px;align-items:center;padding:12px;border-top:1px solid #e3e6ea}" +
        ".mbtn{font:12px system-ui,sans-serif;color:#1c2128;background:#fff;border:1px solid #d9dce1;" +
        "border-radius:8px;padding:7px 12px;cursor:pointer}" +
        ".mbtn:hover{background:#f4f5f7}" +
        ".mbtn.primary{background:#2f5fe0;border-color:#2f5fe0;color:#fff}" +
        ".mbtn.primary:hover{background:#2149b8}" +
        ".mrow{border:1px solid #d9dce1;border-radius:10px;padding:10px;margin-bottom:10px}" +
        ".mrow input[type=text],.mrow select{width:100%;padding:6px 9px;border:1px solid #d9dce1;" +
        "border-radius:8px;font:inherit;font-size:12px;margin-bottom:6px;background:#fff}" +
        ".mrow .mchk{display:flex;gap:6px;align-items:center;font-size:12px;color:#475467;margin-bottom:6px}" +
        ".mrow .mtools{display:flex;gap:2px;justify-content:flex-end}" +
        ".mrow .mtools button{border:none;background:none;padding:3px 7px;border-radius:6px;" +
        "color:#667085;cursor:pointer;font-size:13px}" +
        ".mrow .mtools button:hover{background:#eceef1;color:#1c2128}" +
        ".merr{color:#c0392b;font-size:12px;padding:0 14px 10px}" +
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
        ".dlg .fld{margin:0 0 12px}" +
        ".dlg .fld label{display:block;font-size:12px;font-weight:600;margin-bottom:4px;color:#475467}" +
        ".dlg select{width:100%;padding:8px 10px;border:1px solid #d9dce1;border-radius:8px;" +
        "font:inherit;font-size:13px;background:#fff}" +
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
        '<span class="chip" id="chip"></span>' +
        '<span class="msg" id="msg" hidden></span>' +
        '<button id="edit" title="Edit this page in place">' +
        '<span class="ic" id="edit-ic">' + ICONS.pencil + '</span><span id="edit-label">Edit</span></button>' +
        '<button id="save" disabled hidden title="Save your changes as a draft">Save</button>' +
        '<button id="publish" class="primary" title="Make the current draft live">Publish</button>' +
        '<span class="more">' +
        '<button id="more" class="quiet" title="More actions" aria-haspopup="true" aria-expanded="false">⋯</button>' +
        '<div class="menu" id="more-menu">' +
        '<button id="cancel" hidden>Revert unsaved changes</button>' +
        '<button id="discard" class="dngr" hidden>Discard draft…</button>' +
        '<button id="del-page" class="dngr" hidden>Delete page…</button>' +
        '<hr id="menu-sep">' +
        '<a id="admin" href="#">Open admin</a>' +
        "</div></span>" +
        '<button id="close" class="quiet" title="Minimize editing tools">' + ICONS.hide + "</button>" +
        "</div>" +
        '<div class="rail" id="rail">' +
        '<button id="rail-add" title="Add a section">＋<span>Section</span></button>' +
        '<button id="rail-snips" title="Snippets">⧉<span>Snippets</span></button>' +
        '<button id="rail-page" title="New page">⊞<span>Page</span></button>' +
        '<button id="rail-menu" title="Edit the site menu">☰<span>Menu</span></button>' +
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
        '<div class="dhead"><h2 id="drawer-title">Snippets</h2>' +
        '<button id="drawer-close" title="Close" aria-label="Close">×</button></div>' +
        '<div class="dhint" id="drawer-hint">Drag a snippet onto the page, or click one to insert it at the cursor.</div>' +
        '<div class="dlist" id="snip-list"></div>' +
        "</div>" +
        '<div class="drawer" id="menu-drawer">' +
        '<div class="dhead"><h2>Site menu</h2>' +
        '<button id="menu-close" title="Close" aria-label="Close">×</button></div>' +
        '<div class="dhint">Items link to a page or a custom address. Saving applies to the whole site immediately.</div>' +
        '<div class="dlist" id="menu-list"></div>' +
        '<div class="merr" id="menu-err" hidden></div>' +
        '<div class="mfoot">' +
        '<button class="mbtn" id="menu-add">＋ Add item</button>' +
        '<span style="flex:1"></span>' +
        '<button class="mbtn primary" id="menu-save">Save menu</button>' +
        "</div></div>" +
        '<div class="dlg-overlay" id="dlg-overlay"></div>' +
        '<div class="dlg" id="dlg" role="dialog" aria-modal="true">' +
        '<p id="dlg-msg"></p>' +
        '<input type="text" id="dlg-input" hidden>' +
        '<div id="dlg-fields"></div>' +
        '<div class="acts">' +
        '<button id="dlg-cancel">Cancel</button>' +
        '<button id="dlg-ok" class="ok">OK</button>' +
        "</div></div>";
    document.documentElement.appendChild(host);

    var $ = function (id) { return shadow.getElementById(id); };
    $("admin").href = adminPath + "/";
    updateChip();

    /* ------------------------------------------------------------------ *
     * Dialogs — styled replacements for window.confirm / window.prompt
     * ------------------------------------------------------------------ */

    var dlgResolve = null;
    var dlgIsPrompt = false;
    var dlgHasSelects = false;

    function openDialog(opts) {
        return new Promise(function (resolve) {
            dlgResolve = resolve;
            dlgIsPrompt = !!opts.prompt;
            dlgHasSelects = !!(opts.selects && opts.selects.length);
            $("dlg-msg").textContent = opts.message;
            var input = $("dlg-input");
            input.hidden = !opts.prompt;
            input.value = opts.value || "";
            input.placeholder = opts.placeholder || "";
            var fields = $("dlg-fields");
            fields.innerHTML = "";
            if (dlgHasSelects) {
                opts.selects.forEach(function (f) {
                    var wrap = document.createElement("div");
                    wrap.className = "fld";
                    var label = document.createElement("label");
                    label.textContent = f.label;
                    var sel = document.createElement("select");
                    sel.dataset.field = f.id;
                    f.options.forEach(function (o) {
                        var opt = document.createElement("option");
                        opt.value = o.value;
                        opt.textContent = o.label;
                        if (o.value === f.value) opt.selected = true;
                        sel.appendChild(opt);
                    });
                    wrap.appendChild(label);
                    wrap.appendChild(sel);
                    fields.appendChild(wrap);
                });
            }
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
    function cmsPrompt(message, placeholder, okLabel, value) {
        return openDialog({ message: message, prompt: true, placeholder: placeholder, okLabel: okLabel, value: value });
    }

    function dialogOK() {
        if (dlgHasSelects) {
            var values = {};
            $("dlg-fields").querySelectorAll("select").forEach(function (sel) {
                values[sel.dataset.field] = sel.value;
            });
            // A dialog may combine a text input with selects; the input's
            // value rides along under "input".
            if (dlgIsPrompt) values.input = $("dlg-input").value.trim();
            settleDialog(values);
            return;
        }
        settleDialog(dlgIsPrompt ? $("dlg-input").value.trim() : true);
    }
    function dialogDismiss() { settleDialog(dlgIsPrompt || dlgHasSelects ? null : false); }

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
        "#cms-mce-toolbar > *{pointer-events:auto;box-shadow:0 4px 16px rgba(0,0,0,.18);border-radius:8px}" +
        /* Sections */
        ".cms-editing [data-cms-section]{position:relative;outline:1.5px dashed rgba(30,126,78,.55);outline-offset:-3px}" +
        ".cms-editing [data-cms-section]:hover{outline-style:solid}" +
        ".cms-editing [data-cms-section-content]{min-height:2em}" +
        ".cms-sec-ui{position:absolute;top:8px;right:8px;z-index:2147482996;display:flex;gap:2px;" +
        "background:#1c2128;border:1px solid rgba(255,255,255,.28);border-radius:999px;" +
        "padding:4px 6px;box-shadow:0 4px 12px rgba(0,0,0,.35)}" +
        ".cms-sec-ui button{font:13px/1 system-ui,sans-serif;color:#fff;background:transparent;border:none;" +
        "border-radius:999px;padding:5px 8px;cursor:pointer}" +
        ".cms-sec-ui button:hover{background:rgba(255,255,255,.18)}" +
        ".cms-sec-ui button[data-secact='del']{color:#fca5a5}" +
        ".cms-sec-ui button[data-secact='del']:hover{background:rgba(252,165,165,.2)}" +
        ".cms-add-section{padding:14px;text-align:center}" +
        ".cms-add-section button{font:13px system-ui,sans-serif;color:#2149b8;background:#e8edfb;" +
        "border:1.5px dashed #2f5fe0;border-radius:10px;padding:10px 18px;cursor:pointer}" +
        ".cms-add-section button:hover{background:#dbe4fa}" +
        /* Flexible-space snippet: invisible on the live site, visible and
         * click-to-adjust while editing. */
        ".cms-editing .cms-spacer{position:relative;cursor:pointer;min-height:14px;" +
        "outline:1.5px dashed rgba(217,119,6,.55);outline-offset:-2px;" +
        "background:repeating-linear-gradient(-45deg,rgba(217,119,6,.06),rgba(217,119,6,.06) 8px,transparent 8px,transparent 16px)}" +
        ".cms-editing .cms-spacer:hover{outline-style:solid}" +
        ".cms-editing .cms-spacer::after{content:'↕ Space · ' attr(data-height) ' — click to adjust';" +
        "position:absolute;top:50%;left:50%;transform:translate(-50%,-50%);" +
        "font:11px system-ui,sans-serif;color:#b45309;white-space:nowrap;pointer-events:none}";
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
            initInlineEditor(el, function () { markDirty(name); }, function (ed) { mceEditors[name] = ed; });
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
                var known = sectionEditors.some(function (s) { return s.el === el; });
                if (known) return;
                initInlineEditor(el, function () { markSectionsDirty(region); }, function (ed) {
                    sectionEditors.push({ el: el, ed: ed, region: region });
                });
            });
        });
    }

    function initInlineEditor(el, onDirty, register) {
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
                    register(ed);
                    ed.on("focus", function () { lastEditor = ed; lastEditorDirty = onDirty; });
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

    function removeRichEditors() {
        Object.keys(mceEditors).forEach(function (name) {
            mceEditors[name].remove();
        });
        mceEditors = {};
        sectionEditors.forEach(function (s) { s.ed.remove(); });
        sectionEditors = [];
        lastEditor = null;
        lastEditorDirty = null;
        // Remove injected section toolbars and add-buttons.
        document.querySelectorAll("[data-cms-ui]").forEach(function (el) { el.remove(); });
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
        var secs = [];
        document.querySelectorAll("[data-cms-sections]").forEach(function (el) {
            secs.push({ el: el, html: el.innerHTML });
        });
        return { regs: regs, imgs: imgs, secs: secs };
    }

    function restoreSnapshot(s) {
        s.regs.forEach(function (r) { r.el.innerHTML = r.html; });
        (s.secs || []).forEach(function (c) { c.el.innerHTML = c.html; });
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
        $("edit-ic").innerHTML = on ? ICONS.check : ICONS.pencil;
        $("edit-label").textContent = on ? "Done" : "Edit";
        $("edit").title = on ? "Finish editing" : "Edit this page in place";
        $("cancel").hidden = !on;
        // The home page (empty slug) is never deletable.
        $("del-page").hidden = !on || (cfg.slug || "") === "";
        $("rail").classList.toggle("on", on);
        if (!on) {
            closeDrawer();
            closeMenuPanel();
            pendingSection = null;
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
            setMsg("Loading editor…");
            loadTinyMCE().then(function () {
                initRichEditors();
                injectSectionUI();
                setMsg("");
            }).catch(function (err) { setMsg(err.message); });
        } else {
            removeRichEditors();
        }
    }

    function markDirty(name) {
        dirty[name] = true;
        $("save").disabled = false;
        updateBarButtons();
    }

    function markSectionsDirty(region) {
        sectionsDirty[region] = true;
        $("save").disabled = false;
        updateBarButtons();
    }

    function hasUnsaved() {
        return Object.keys(dirty).length > 0 || Object.keys(sectionsDirty).length > 0;
    }

    // updateBarButtons keeps the edit bar honest about the page's state:
    // while just viewing, Save and Publish are hidden — except that
    // Publish stays whenever there is something publishable: a draft page
    // (making it live is the primary action), saved-but-unpublished
    // changes, or unsaved work from a minimized session.
    function updateBarButtons() {
        var working = editing || hasUnsaved();
        $("save").hidden = !working;
        // The amber ring on Save is the unsaved-changes signal.
        $("save").classList.toggle("attn", hasUnsaved());
        $("publish").hidden = !working && pageStatus === "published" && !hasUnpublished;
        // Discard is offered whenever there's a saved draft that differs from
        // what's live — the same condition as the "Unpublished edits" chip.
        $("discard").hidden = !(pageStatus === "published" && hasUnpublished);
        // The separator only earns its place when the destructive group
        // above it has at least one visible item.
        $("menu-sep").hidden = $("cancel").hidden && $("discard").hidden && $("del-page").hidden;
    }

    document.addEventListener("input", function (e) {
        if (!editing) return;
        var el = e.target.closest ? e.target.closest('[data-cms-region][data-cms-kind="text"]') : null;
        if (el) markDirty(el.dataset.cmsRegion);
    });

    // Flexible-space snippets: click one while editing to set its height.
    document.addEventListener("click", function (e) {
        if (!editing) return;
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
        if (hasUnsaved()) {
            e.preventDefault();
            e.returnValue = "";
        }
    });

    /* ------------------------------------------------------------------ *
     * Saving and publishing
     * ------------------------------------------------------------------ */

    var msgTimer = null;

    function setMsg(text) {
        clearTimeout(msgTimer);
        $("msg").textContent = text || "";
        $("msg").hidden = !text;
    }

    // flash shows a short confirmation that clears itself, so the bar
    // doesn't hold on to stale status text (and stale width).
    function flash(text) {
        setMsg(text);
        msgTimer = setTimeout(function () { setMsg(""); }, 4000);
    }

    // The chip has three states: draft (never/no longer live), Live
    // (published and in sync), and "Unpublished edits" (live, but the saved
    // draft differs — the state that makes drafts trustworthy).
    function updateChip() {
        var chip = $("chip");
        chip.classList.remove("published", "changes");
        if (pageStatus === "published" && hasUnpublished) {
            chip.textContent = "Unpublished edits";
            chip.title = "This page has saved edits that aren't live yet — Publish makes them live";
            chip.classList.add("changes");
        } else if (pageStatus === "published") {
            chip.textContent = "Live";
            chip.title = "This page is published and up to date";
            chip.classList.add("published");
        } else {
            chip.textContent = pageStatus;
            chip.title = "Only editors can see this page until it's published";
        }
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

    // collectSections reads a sections region's current DOM order plus
    // each section's settings and (editor-cleaned) content.
    function collectSections(region) {
        var container = document.querySelector('[data-cms-sections="' + region + '"]');
        var out = [];
        if (!container) return out;
        container.querySelectorAll("[data-cms-section]").forEach(function (wrapper) {
            var contentEl = wrapper.querySelector("[data-cms-section-content]");
            if (!contentEl) return;
            var html = contentEl.innerHTML;
            sectionEditors.some(function (s) {
                if (s.el === contentEl) { html = s.ed.getContent(); return true; }
                return false;
            });
            out.push({ bg: wrapper.dataset.cmsBg || "", width: wrapper.dataset.cmsWidth || "", html: html });
        });
        return out;
    }

    function save() {
        var values = collect();
        var secRegions = Object.keys(sectionsDirty);
        if (Object.keys(values).length === 0 && secRegions.length === 0) return Promise.resolve();
        setMsg("Saving…");
        var chain = Promise.resolve();
        if (Object.keys(values).length > 0) {
            chain = chain.then(function () {
                return api("/pages/" + pageId + "/regions", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ locale: cfg.locale, regions: values }),
                });
            });
        }
        secRegions.forEach(function (region) {
            chain = chain.then(function () {
                return api("/pages/" + pageId + "/sections", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ locale: cfg.locale, region: region, sections: collectSections(region) }),
                });
            });
        });
        return chain.then(function () {
            dirty = {};
            sectionsDirty = {};
            Object.keys(mceEditors).forEach(function (name) {
                mceEditors[name].setDirty(false);
            });
            sectionEditors.forEach(function (s) { s.ed.setDirty(false); });
            $("save").disabled = true;
            if (pageStatus === "published") hasUnpublished = true;
            flash("Draft saved");
            updateChip();
            updateBarButtons();
        });
    }

    function publish() {
        save().then(function () {
            setMsg("Publishing…");
            return api("/pages/" + pageId + "/publish", { method: "POST" });
        }).then(function () {
            pageStatus = "published";
            hasUnpublished = false;
            updateChip();
            flash("Published ✓");
            updateBarButtons();
        }).catch(function (err) { setMsg(err.message); });
    }

    // discardDraft throws away the saved-but-unpublished draft, reverting it
    // to what's live. The page is reloaded afterwards so the visible content
    // matches the restored (published) draft.
    function discardDraft() {
        cmsConfirm("Discard your unpublished changes and go back to the published version of this page? This can't be undone.",
            "Discard draft", true).then(function (yes) {
            if (!yes) return;
            setMsg("Discarding…");
            api("/pages/" + pageId + "/discard", { method: "POST" }).then(function () {
                // Clear unsaved state so beforeunload doesn't second-guess the
                // reload, then reload to show the reverted content.
                dirty = {};
                sectionsDirty = {};
                window.location.reload();
            }).catch(function (err) { setMsg(err.message); });
        });
    }

    $("edit").addEventListener("click", function () { setEditing(!editing); });
    $("cancel").addEventListener("click", function () {
        var discard = function () {
            var snap = snapshot;
            setEditing(false); // tears down TinyMCE before the DOM is restored
            if (snap) restoreSnapshot(snap);
            dirty = {};
            sectionsDirty = {};
            imageValues = {};
            $("save").disabled = true;
            setMsg("");
            updateBarButtons();
        };
        if (hasUnsaved()) {
            cmsConfirm("Discard your unsaved changes? The page will go back to how it was before you started editing.",
                "Discard changes", true).then(function (yes) {
                if (yes) discard();
            });
        } else {
            discard();
        }
    });
    $("del-page").addEventListener("click", function () {
        cmsConfirm("Delete this page and everything on it? This cannot be undone.",
            "Delete page", true).then(function (yes) {
            if (!yes) return;
            setMsg("Deleting…");
            api("/pages/" + pageId, { method: "DELETE" }).then(function (body) {
                // Clear unsaved state so beforeunload doesn't second-guess
                // the navigation away from the now-deleted page.
                dirty = {};
                sectionsDirty = {};
                window.location.href = body.url || "/";
            }).catch(function (err) { setMsg(err.message); });
        });
    });
    $("save").addEventListener("click", function () {
        save().catch(function (err) { setMsg(err.message); });
    });
    $("publish").addEventListener("click", publish);
    $("discard").addEventListener("click", discardDraft);

    // The ⋯ menu holds rare and destructive actions. Any click elsewhere —
    // including on a menu item, whose own handler runs first — closes it.
    function closeMore() {
        $("more-menu").classList.remove("on");
        $("more").setAttribute("aria-expanded", "false");
    }
    $("more").addEventListener("click", function (e) {
        e.stopPropagation();
        var open = !$("more-menu").classList.contains("on");
        $("more-menu").classList.toggle("on", open);
        $("more").setAttribute("aria-expanded", open ? "true" : "false");
    });
    document.addEventListener("click", closeMore);

    // The × doesn't remove the toolbar — it minimizes it (animated) to a
    // pencil button in the same spot; clicking that brings the bar back.
    // Minimizing exits edit mode for a clean view of the page, but any
    // unsaved changes stay in the page and Save stays available. The
    // choice is remembered across pages.
    var BAR_MIN_KEY = "cms-bar-min";

    function setBarMinimized(min) {
        $("bar").classList.toggle("min", min);
        $("fab").classList.toggle("on", min);
        try {
            window.localStorage.setItem(BAR_MIN_KEY, min ? "1" : "0");
        } catch (e) { /* private mode */ }
    }

    $("close").addEventListener("click", function () {
        closePicker();
        setEditing(false);
        setBarMinimized(true);
    });
    $("fab").addEventListener("click", function () {
        setBarMinimized(false);
        if (hasUnsaved()) {
            $("save").disabled = false;
            flash("You have unsaved changes");
        }
    });

    // Restore the remembered state on page load. This runs before first
    // paint (the script is deferred), so no minimize animation plays.
    try {
        if (window.localStorage.getItem(BAR_MIN_KEY) === "1") {
            setBarMinimized(true);
        }
    } catch (e) { /* private mode */ }

    updateBarButtons();

    /* ------------------------------------------------------------------ *
     * Snippet drawer
     * ------------------------------------------------------------------ */

    var snippetsLoaded = false;

    function openDrawer() {
        closeMenuPanel();
        $("drawer").classList.add("on");
        $("drawer-title").textContent = pendingSection ? "Add a section" : "Snippets";
        $("drawer-hint").textContent = pendingSection
            ? "Start with an empty section, or use a snippet as its starting content."
            : "Drag a snippet onto the page, or click one to insert it at the cursor.";
        var emptyCard = $("snip-empty");
        if (emptyCard) emptyCard.hidden = !pendingSection;
        var groupLabel = $("snip-group-label");
        if (groupLabel) groupLabel.hidden = !pendingSection;
        if (!snippetsLoaded) loadSnippets();
        updateRail();
    }
    function closeDrawer() {
        $("drawer").classList.remove("on");
        pendingSection = null;
        updateRail();
    }

    // updateRail highlights whichever rail button the open panel belongs
    // to (Section when choosing a new section's start, Snippets otherwise,
    // Menu for the menu panel).
    function updateRail() {
        var open = $("drawer").classList.contains("on");
        $("rail-add").classList.toggle("on", open && !!pendingSection);
        $("rail-snips").classList.toggle("on", open && !pendingSection);
        $("rail-menu").classList.toggle("on", $("menu-drawer").classList.contains("on"));
    }

    // Pages whose template declares no sections region get a visibly
    // disabled Section button rather than a dead-feeling click.
    if (!document.querySelector("[data-cms-sections]")) {
        $("rail-add").disabled = true;
        $("rail-add").title = "This page type has no sections area";
    }

    // Rail buttons toggle: the button matching the drawer's current mode
    // closes it; the other button switches the drawer to its mode.
    $("rail-snips").addEventListener("click", function () {
        if ($("drawer").classList.contains("on") && !pendingSection) {
            closeDrawer();
            return;
        }
        pendingSection = null;
        openDrawer();
    });
    $("rail-add").addEventListener("click", function () {
        if ($("drawer").classList.contains("on") && pendingSection) {
            closeDrawer();
            return;
        }
        var container = document.querySelector("[data-cms-sections]");
        if (!container) {
            setMsg("This page has no sections area.");
            return;
        }
        pendingSection = { region: container.getAttribute("data-cms-sections"), after: null };
        openDrawer();
    });
    $("rail-page").hidden = pageTemplates.length === 0;
    $("rail-page").addEventListener("click", function () {
        if (!pageTemplates.length) return;
        openDialog({
            message: "Create a new page",
            prompt: true,
            placeholder: "Page name",
            okLabel: "Create page",
            selects: [{
                id: "template",
                label: "Page type",
                value: pageTemplates[0].file,
                options: pageTemplates.map(function (t) { return { value: t.file, label: t.label }; }),
            }],
        }).then(function (values) {
            if (!values || !values.input) return;
            setMsg("Creating page…");
            api("/pages", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ title: values.input, template: values.template }),
            }).then(function (body) {
                // The new page is a draft, so only editors see it —
                // navigate straight into it to start writing.
                window.location.href = body.url;
            }).catch(function (err) { setMsg(err.message); });
        });
    });

    $("drawer-close").addEventListener("click", closeDrawer);

    function loadSnippets() {
        var list = $("snip-list");
        list.innerHTML = '<span class="empty">Loading…</span>';
        api("/snippets", { method: "GET" }).then(function (body) {
            snippetsLoaded = true;
            list.innerHTML = "";
            // "Empty section" appears only when the drawer is choosing a
            // new section's starting point.
            var emptyCard = document.createElement("div");
            emptyCard.className = "snip";
            emptyCard.id = "snip-empty";
            emptyCard.hidden = !pendingSection;
            var enm = document.createElement("p");
            enm.className = "sname";
            enm.textContent = "Empty section";
            var eds = document.createElement("p");
            eds.className = "sdesc";
            eds.textContent = "Start from a blank section.";
            emptyCard.appendChild(enm);
            emptyCard.appendChild(eds);
            emptyCard.addEventListener("click", function () {
                chooseSnippet({ name: "Empty", html: "<p>Start writing…</p>" });
            });
            list.appendChild(emptyCard);
            var groupLabel = document.createElement("p");
            groupLabel.className = "sgroup";
            groupLabel.id = "snip-group-label";
            groupLabel.hidden = !pendingSection;
            groupLabel.textContent = "Or start from a snippet";
            list.appendChild(groupLabel);
            if (!body.snippets || body.snippets.length === 0) {
                list.appendChild(Object.assign(document.createElement("span"),
                    { className: "empty", textContent: "No snippets available." }));
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
                // Click: new-section starting point when one is pending,
                // otherwise insert at the cursor.
                card.addEventListener("click", function () { chooseSnippet(sn); });
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

    function chooseSnippet(sn) {
        if (pendingSection) {
            var target = pendingSection;
            closeDrawer();
            createSection(target, sn.html);
            return;
        }
        insertSnippet(sn);
    }

    function insertSnippet(sn) {
        var ed = lastEditor && !lastEditor.removed ? lastEditor : null;
        var onDirty = lastEditorDirty;
        if (!ed) {
            var first = Object.keys(mceEditors)[0];
            if (first) {
                ed = mceEditors[first];
                onDirty = function () { markDirty(first); };
            } else if (sectionEditors.length) {
                ed = sectionEditors[0].ed;
                var rg = sectionEditors[0].region;
                onDirty = function () { markSectionsDirty(rg); };
            }
        }
        if (!ed) {
            setMsg("Click into a content area first, then insert the snippet.");
            return;
        }
        ed.focus();
        ed.insertContent(sn.html);
        if (onDirty) onDirty();
        flash("Snippet inserted — click it to edit the text");
    }

    /* ------------------------------------------------------------------ *
     * Sections: per-section controls, add, reorder, settings
     * ------------------------------------------------------------------ */

    function sbOpt(list, key) {
        for (var i = 0; i < list.length; i++) {
            if (list[i].key === key) return list[i];
        }
        return list[0];
    }

    function injectSectionUI() {
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
        [["up", "↑", "Move up"], ["down", "↓", "Move down"], ["add", "＋", "Add section below"],
            ["set", "⚙", "Section settings"], ["del", "", "Delete section"]].forEach(function (b) {
            var btn = document.createElement("button");
            btn.type = "button";
            btn.setAttribute("data-secact", b[0]);
            if (b[0] === "del") {
                // SVG rather than 🗑: emoji presentation ignores CSS
                // color, and this button must read as red/destructive.
                btn.innerHTML = '<svg width="12" height="13" viewBox="0 0 24 24" fill="currentColor" style="display:block">' +
                    '<path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>';
            } else {
                btn.textContent = b[1];
            }
            btn.title = b[2];
            tb.appendChild(btn);
        });
        wrapper.appendChild(tb);
    }

    document.addEventListener("click", function (e) {
        if (!editing) return;
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
            pendingSection = { region: region, after: null };
            openDrawer();
            return;
        }
        if (!wrapper) return;

        if (act === "add") {
            pendingSection = { region: region, after: wrapper };
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
                sectionEditors = sectionEditors.filter(function (s) {
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
            openDialog({
                message: "Section settings",
                okLabel: "Apply",
                selects: [
                    { id: "bg", label: "Background", value: wrapper.dataset.cmsBg || "",
                        options: sectionStyles.backgrounds.map(function (o) { return { value: o.key, label: o.label }; }) },
                    { id: "width", label: "Content width", value: wrapper.dataset.cmsWidth || "",
                        options: sectionStyles.widths.map(function (o) { return { value: o.key, label: o.label }; }) },
                ],
            }).then(function (values) {
                if (!values) return;
                applySectionSettings(wrapper, values.bg, values.width);
                markSectionsDirty(region);
            });
        }
    });

    function applySectionSettings(wrapper, bgKey, widthKey) {
        var bg = sbOpt(sectionStyles.backgrounds, bgKey);
        var w = sbOpt(sectionStyles.widths, widthKey);
        wrapper.dataset.cmsBg = bg.key;
        wrapper.dataset.cmsWidth = w.key;
        wrapper.className = bg.class || "";
        var contentEl = wrapper.querySelector("[data-cms-section-content]");
        if (contentEl) contentEl.className = ((w.class || "") + " " + (bg.contentClass || "")).trim();
    }

    function createSection(target, html) {
        var container = document.querySelector('[data-cms-sections="' + target.region + '"]');
        if (!container) return;
        var bg = sectionStyles.backgrounds[0];
        var w = sectionStyles.widths[0];
        var wrapper = document.createElement("section");
        wrapper.setAttribute("data-cms-section", "");
        wrapper.dataset.cmsBg = bg.key;
        wrapper.dataset.cmsWidth = w.key;
        if (bg.class) wrapper.className = bg.class;
        var inner = document.createElement("div");
        inner.setAttribute("data-cms-section-content", "");
        var innerClass = ((w.class || "") + " " + (bg.contentClass || "")).trim();
        if (innerClass) inner.className = innerClass;
        inner.innerHTML = html;
        wrapper.appendChild(inner);
        if (target.after) {
            target.after.insertAdjacentElement("afterend", wrapper);
        } else {
            var addWrap = container.querySelector(".cms-add-section");
            if (addWrap) container.insertBefore(wrapper, addWrap);
            else container.appendChild(wrapper);
        }
        injectSectionToolbar(wrapper);
        initInlineEditor(inner, function () { markSectionsDirty(target.region); }, function (ed) {
            sectionEditors.push({ el: inner, ed: ed, region: target.region });
        });
        markSectionsDirty(target.region);
        wrapper.scrollIntoView({ behavior: "smooth", block: "center" });
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
            flash("File uploaded");
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
            dialogDismiss(); // an open dialog captures Escape first
            return;
        }
        closeMore();
        closePicker();
        closeDrawer();
        closeMenuPanel();
    });

    /* ------------------------------------------------------------------ *
     * Menu panel — edits navigation items (data only; the template owns
     * the nav markup, so saving reloads the page to re-render it)
     * ------------------------------------------------------------------ */

    var menuData = null; // [{label, pageId, url, newTab}]
    var menuPages = null; // [{id, title, slug, status}]

    function openMenuPanel() {
        closeDrawer();
        $("menu-drawer").classList.add("on");
        if (!menuData) loadMenu();
        updateRail();
    }
    function closeMenuPanel() {
        $("menu-drawer").classList.remove("on");
        updateRail();
    }

    $("rail-menu").addEventListener("click", function () {
        if ($("menu-drawer").classList.contains("on")) closeMenuPanel();
        else openMenuPanel();
    });
    $("menu-close").addEventListener("click", closeMenuPanel);

    function loadMenu() {
        $("menu-list").innerHTML = '<span class="empty">Loading…</span>';
        Promise.all([
            api("/menu?menu=main", { method: "GET" }),
            api("/pages", { method: "GET" }),
        ]).then(function (results) {
            menuData = results[0].items || [];
            menuPages = results[1].pages || [];
            renderMenuRows();
        }).catch(function (err) {
            $("menu-list").innerHTML = "";
            var span = document.createElement("span");
            span.className = "empty";
            span.textContent = err.message;
            $("menu-list").appendChild(span);
        });
    }

    function menuError(msg) {
        var el = $("menu-err");
        el.textContent = msg || "";
        el.hidden = !msg;
    }

    function renderMenuRows() {
        var list = $("menu-list");
        list.innerHTML = "";
        menuError("");
        if (menuData.length === 0) {
            list.innerHTML = '<span class="empty">No menu items yet — add your first one below.</span>';
        }
        menuData.forEach(function (item, i) {
            var row = document.createElement("div");
            row.className = "mrow";

            var tools = document.createElement("div");
            tools.className = "mtools";
            [["↑", "Move up"], ["↓", "Move down"], ["✕", "Remove"]].forEach(function (b, ti) {
                var btn = document.createElement("button");
                btn.type = "button";
                btn.textContent = b[0];
                btn.title = b[1];
                btn.addEventListener("click", function () {
                    if (ti === 0 && i > 0) {
                        menuData.splice(i - 1, 0, menuData.splice(i, 1)[0]);
                    } else if (ti === 1 && i < menuData.length - 1) {
                        menuData.splice(i + 1, 0, menuData.splice(i, 1)[0]);
                    } else if (ti === 2) {
                        menuData.splice(i, 1);
                    } else {
                        return;
                    }
                    renderMenuRows();
                });
                tools.appendChild(btn);
            });
            row.appendChild(tools);

            var label = document.createElement("input");
            label.type = "text";
            label.placeholder = "Label";
            label.value = item.label;
            label.addEventListener("input", function () { item.label = label.value; });
            row.appendChild(label);

            var sel = document.createElement("select");
            menuPages.forEach(function (p) {
                var opt = document.createElement("option");
                opt.value = String(p.id);
                opt.textContent = (p.title || "(untitled)") + (p.status === "published" ? "" : " (draft)");
                if (item.pageId === p.id) opt.selected = true;
                sel.appendChild(opt);
            });
            var custom = document.createElement("option");
            custom.value = "custom";
            custom.textContent = "Custom address…";
            if (!item.pageId) custom.selected = true;
            sel.appendChild(custom);
            sel.addEventListener("change", function () {
                if (sel.value === "custom") {
                    item.pageId = 0;
                } else {
                    item.pageId = parseInt(sel.value, 10);
                    item.url = "";
                }
                renderMenuRows();
            });
            row.appendChild(sel);

            if (!item.pageId) {
                var url = document.createElement("input");
                url.type = "text";
                url.placeholder = "https://example.com or /contact";
                url.value = item.url || "";
                url.addEventListener("input", function () { item.url = url.value; });
                row.appendChild(url);

                var chk = document.createElement("label");
                chk.className = "mchk";
                var cb = document.createElement("input");
                cb.type = "checkbox";
                cb.checked = !!item.newTab;
                cb.addEventListener("change", function () { item.newTab = cb.checked; });
                chk.appendChild(cb);
                chk.appendChild(document.createTextNode("Open in a new tab"));
                row.appendChild(chk);
            }

            list.appendChild(row);
        });
    }

    $("menu-add").addEventListener("click", function () {
        if (!menuData) return;
        var first = menuPages && menuPages.length ? menuPages[0].id : 0;
        menuData.push({ label: "", pageId: first, url: "", newTab: false });
        renderMenuRows();
        var inputs = $("menu-list").querySelectorAll(".mrow input[type=text]");
        if (inputs.length) inputs[inputs.length - (first ? 1 : 2)].focus();
    });

    $("menu-save").addEventListener("click", function () {
        if (!menuData) return;
        for (var i = 0; i < menuData.length; i++) {
            if (!menuData[i].label.trim()) {
                menuError("Every menu item needs a label.");
                return;
            }
            if (!menuData[i].pageId && !(menuData[i].url || "").trim()) {
                menuError("Custom links need a web address.");
                return;
            }
        }
        menuError("");
        setMsg("Saving menu…");
        api("/menu", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ menu: "main", items: menuData }),
        }).then(function () {
            // The template renders the nav, so reload to show the result.
            dirty = {};
            sectionsDirty = {};
            window.location.reload();
        }).catch(function (err) { menuError(err.message); setMsg(""); });
    });
})();
