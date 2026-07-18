/* ------------------------------------------------------------------ *
 * Toolbar and panels (Shadow DOM)
 *
 * The editor's chrome — bar, rail, drawers, panels, dialogs — lives in
 * a shadow root so host-page CSS can't restyle it. This module builds
 * that DOM, exposes $ for looking chrome elements up by id, and owns
 * the light-DOM pieces the chrome needs (TinyMCE's toolbar strip, the
 * editing-outline styles) plus the bar's minimize/restore behavior.
 * ------------------------------------------------------------------ */

import { adminPath } from "./state.js";
import { updateChip } from "./saving.js";
import { setEditing, hasUnsaved, updateBarButtons } from "./editing.js";
import { closePicker } from "./media.js";
import { flash } from "./util.js";
// Real stylesheets, bundled in as text by esbuild's `text` loader.
import shadowCss from "./styles.css";
import lightCssText from "./light.css";

// Small inline icons for the bar (filled with currentColor via CSS).
export var ICONS = {
    pencil: '<svg viewBox="0 0 24 24"><path d="M3 17.25V21h3.75L17.81 9.94l-3.75-3.75L3 17.25zM20.71 7.04c.39-.39.39-1.02 0-1.41l-2.34-2.34a.9959.9959 0 00-1.41 0l-1.83 1.83 3.75 3.75 1.83-1.83z"/></svg>',
    check: '<svg viewBox="0 0 24 24"><path d="M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z"/></svg>',
    hide: '<svg viewBox="0 0 24 24"><path d="M7.4 8.6 12 13.2l4.6-4.6L18 10l-6 6-6-6z"/></svg>',
    gear: '<svg viewBox="0 0 24 24"><path d="M19.14 12.94c.04-.3.06-.61.06-.94 0-.32-.02-.64-.07-.94l2.03-1.58c.18-.14.23-.41.12-.61l-1.92-3.32c-.12-.22-.37-.29-.59-.22l-2.39.96c-.5-.38-1.03-.7-1.62-.94l-.36-2.54c-.04-.24-.24-.41-.48-.41h-3.84c-.24 0-.43.17-.47.41l-.36 2.54c-.59.24-1.13.57-1.62.94l-2.39-.96c-.22-.08-.47 0-.59.22L2.74 8.87c-.12.21-.08.47.12.61l2.03 1.58c-.05.3-.09.63-.09.94s.02.64.07.94l-2.03 1.58c-.18.14-.23.41-.12.61l1.92 3.32c.12.22.37.29.59.22l2.39-.96c.5.38 1.03.7 1.62.94l.36 2.54c.05.24.24.41.48.41h3.84c.24 0 .44-.17.47-.41l.36-2.54c.59-.24 1.13-.56 1.62-.94l2.39.96c.22.08.47 0 .59-.22l1.92-3.32c.12-.22.07-.47-.12-.61l-2.01-1.58zM12 15.6c-1.98 0-3.6-1.62-3.6-3.6s1.62-3.6 3.6-3.6 3.6 1.62 3.6 3.6-1.62 3.6-3.6 3.6z"/></svg>',
    trash: '<svg viewBox="0 0 24 24"><path d="M6 19c0 1.1.9 2 2 2h8c1.1 0 2-.9 2-2V7H6v12zM19 4h-3.5l-1-1h-5l-1 1H5v2h14V4z"/></svg>',
};

export var host = null;
var shadow = null;

export function $(id) { return shadow.getElementById(id); }

export function initShell() {
    host = document.createElement("div");
    host.id = "cms-editor-host";
    shadow = host.attachShadow({ mode: "open" });
    shadow.innerHTML =
        "<style>" + shadowCss + "</style>" +
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
        '<button id="code-btn" hidden>Page CSS &amp; JS…</button>' +
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
        '<div class="code-overlay" id="code-overlay"></div>' +
        '<div class="codepanel" id="code-panel">' +
        '<div class="chead"><h2>Page CSS &amp; JS</h2>' +
        '<div class="ctabs">' +
        '<button id="code-tab-css" class="on">CSS</button>' +
        '<button id="code-tab-js">JavaScript</button>' +
        "</div>" +
        '<button id="code-close" title="Close" aria-label="Close">×</button></div>' +
        '<div class="clinks">' +
        '<label for="code-links" id="code-links-label">External stylesheets — one URL per line</label>' +
        '<textarea id="code-links" rows="1" spellcheck="false" autocapitalize="off"' +
        ' placeholder="https://cdn.example.com/library.css"></textarea>' +
        "</div>" +
        '<div class="cbody">' +
        '<pre id="code-hl" aria-hidden="true"></pre>' +
        '<textarea id="code-ta" spellcheck="false" autocapitalize="off" autocomplete="off" wrap="off"></textarea>' +
        "</div>" +
        '<div class="cfoot">' +
        '<span class="chint">This page only. Enter plain code — no &lt;style&gt; or &lt;script&gt; tags; ' +
        "CSS goes into &lt;head&gt;, JavaScript runs before &lt;/body&gt;.</span>" +
        '<button class="mbtn" id="code-cancel">Cancel</button>' +
        '<button class="mbtn primary" id="code-save">Save</button>' +
        "</div></div>" +
        '<div class="btnui" id="btn-ui">' +
        '<button id="btn-set" title="Button settings">' + ICONS.gear + "</button>" +
        '<button id="btn-del" title="Delete button">' + ICONS.trash + "</button>" +
        "</div>" +
        '<div class="btnui" id="snip-ui">' +
        '<button id="snip-move" title="Drag to move this block" draggable="true">⠿</button>' +
        '<button id="snip-del" title="Delete this block">' + ICONS.trash + "</button>" +
        "</div>" +
        '<div class="dlg-overlay" id="dlg-overlay"></div>' +
        '<div class="dlg" id="dlg" role="dialog" aria-modal="true">' +
        '<p id="dlg-msg"></p>' +
        '<div class="tabs" id="dlg-tabs" hidden></div>' +
        '<input type="text" id="dlg-input" hidden>' +
        '<p class="derr" id="dlg-err" hidden></p>' +
        '<div id="dlg-fields"></div>' +
        '<div id="dlg-preview" hidden></div>' +
        '<div class="acts">' +
        '<button id="dlg-cancel">Cancel</button>' +
        '<button id="dlg-ok" class="ok">OK</button>' +
        "</div></div>";
    document.documentElement.appendChild(host);

    $("admin").href = adminPath + "/";
    updateChip();
}

/* Fixed strip TinyMCE renders its toolbar into (light DOM — TinyMCE
 * can't reach into our shadow root). Pinned to the top of the
 * viewport so the toolbar never covers the region being edited;
 * TinyMCE still shows/hides it as regions gain and lose focus. */
export function initLightDom() {
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
    lightCss.textContent = lightCssText;
    document.head.appendChild(lightCss);
}

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

export function initBarMin() {
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
}
