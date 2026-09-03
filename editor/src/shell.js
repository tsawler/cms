/* ------------------------------------------------------------------ *
 * Toolbar and panels (Shadow DOM)
 *
 * The editor's chrome — bar, rail, drawers, panels, dialogs — lives in
 * a shadow root so host-page CSS can't restyle it. This module builds
 * that DOM, exposes $ for looking chrome elements up by id, and owns
 * the light-DOM pieces the chrome needs (TinyMCE's toolbar strip, the
 * editing-outline styles) plus the bar's minimize/restore behavior.
 * ------------------------------------------------------------------ */

import { state, adminPath } from "./state.js";
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
    code: '<svg viewBox="0 0 24 24"><path d="M9.4 16.6 4.8 12l4.6-4.6L8 6l-6 6 6 6zm5.2 0 4.6-4.6-4.6-4.6L16 6l6 6-6 6z"/></svg>',
    search: '<svg viewBox="0 0 24 24"><path d="M15.5 14h-.79l-.28-.27a6.5 6.5 0 10-.7.7l.27.28v.79l5 4.99L20.49 19l-4.99-5zm-6 0A4.5 4.5 0 1114 9.5 4.5 4.5 0 019.5 14z"/></svg>',
    plus: '<svg viewBox="0 0 24 24"><path d="M11 5h2v14h-2zM5 11h14v2H5z"/></svg>',
    // The column tool's width pair: a column edge with arrows pulling in
    // (narrower) or pushing out (wider). Deliberately not chevrons —
    // those are the move buttons sitting next to them.
    narrower: '<svg viewBox="0 0 24 24"><path d="M11 4h2v16h-2z"/><path d="M9 12 3 8v8zM15 12l6-4v8z"/></svg>',
    wider: '<svg viewBox="0 0 24 24"><path d="M11 4h2v16h-2z"/><path d="M3 12l6-4v8zM21 12l-6-4v8z"/></svg>',
    chevL: '<svg viewBox="0 0 24 24"><path d="M15.4 7.4 14 6l-6 6 6 6 1.4-1.4-4.6-4.6z"/></svg>',
    chevR: '<svg viewBox="0 0 24 24"><path d="M8.6 7.4 10 6l6 6-6 6-1.4-1.4 4.6-4.6z"/></svg>',
    // The block tool's move pair. Same chevrons turned a quarter, so a
    // stack of blocks and a row of columns are moved by the same shape
    // pointing the way the thing will go.
    chevU: '<svg viewBox="0 0 24 24"><path d="M7.4 15.4 6 14l6-6 6 6-1.4 1.4L12 10.8z"/></svg>',
    chevD: '<svg viewBox="0 0 24 24"><path d="M7.4 8.6 6 10l6 6 6-6-1.4-1.4L12 13.2z"/></svg>',
    // The duplicate family: two boxes in the arrangement the edit
    // leaves behind, with the *copy* filled in. Boxes rather than
    // arrows on purpose — an arrow already means "move" on the two
    // toolbars these sit on, and the thing worth showing about a
    // duplicate is not a direction of travel but the pair that results.
    // Which of the two is solid is what says where the new one lands.
    dupUp: '<svg viewBox="0 0 24 24"><rect x="4" y="3" width="16" height="7" rx="1.5"/>' +
        '<rect x="5" y="15" width="14" height="5" rx="1" fill="none" stroke="currentColor" stroke-width="2"/></svg>',
    dupDown: '<svg viewBox="0 0 24 24"><rect x="5" y="4" width="14" height="5" rx="1" fill="none" stroke="currentColor" stroke-width="2"/>' +
        '<rect x="4" y="14" width="16" height="7" rx="1.5"/></svg>',
    dupLeft: '<svg viewBox="0 0 24 24"><rect x="3" y="4" width="7" height="16" rx="1.5"/>' +
        '<rect x="15" y="5" width="5" height="14" rx="1" fill="none" stroke="currentColor" stroke-width="2"/></svg>',
    dupRight: '<svg viewBox="0 0 24 24"><rect x="4" y="5" width="5" height="14" rx="1" fill="none" stroke="currentColor" stroke-width="2"/>' +
        '<rect x="14" y="4" width="7" height="16" rx="1.5"/></svg>',
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
        '<span class="locs" id="locs"></span>' +
        '<button id="edit" title="Edit this page in place">' +
        '<span class="ic" id="edit-ic">' + ICONS.pencil + '</span><span id="edit-label">Edit</span></button>' +
        '<button id="save" disabled hidden title="Save your changes as a draft">Save</button>' +
        '<button id="publish" class="primary" title="Make the current draft live">Publish</button>' +
        '<span class="more">' +
        '<button id="more" class="quiet" title="More actions" aria-haspopup="true" aria-expanded="false">⋯</button>' +
        '<div class="menu" id="more-menu">' +
        '<button id="cancel" hidden>Revert unsaved changes</button>' +
        '<button id="revert-locale" class="dngr" hidden>Remove this translation…</button>' +
        '<button id="discard" class="dngr" hidden>Discard draft…</button>' +
        '<button id="unpublish" class="dngr" hidden>Unpublish page…</button>' +
        '<button id="del-page" class="dngr" hidden>Delete page…</button>' +
        '<hr id="menu-sep">' +
        '<button id="meta-btn" hidden>Page settings…</button>' +
        '<button id="dup-page" hidden>Duplicate page…</button>' +
        '<button id="vis-btn" hidden>Make page private…</button>' +
        '<button id="code-btn" hidden>Page CSS &amp; JS…</button>' +
        '<a id="admin" href="#">Open admin</a>' +
        "</div></span>" +
        '<button id="close" class="quiet" title="Minimize editing tools">' + ICONS.hide + "</button>" +
        "</div>" +
        '<div class="rail" id="rail">' +
        '<button id="rail-add" title="Add a section">＋<span>Section</span></button>' +
        '<button id="rail-snips" title="Snippets">⧉<span>Snippets</span></button>' +
        '<button id="rail-page" title="New page">⊞<span>Page</span></button>' +
        '<button id="rail-post" title="New blog or news post">✎<span>Post</span></button>' +
        "</div>" +
        // Post pages only, edit mode only: a pinned top-right pill, far
        // more discoverable than a bar icon for post date/summary/images.
        '<button class="post-pill" id="post-settings" hidden ' +
        'title="Edit this post&#39;s date, summary, thumbnail, and header image">' +
        '⚙<span>Post settings</span></button>' +
        '<div class="toast" id="toast" role="status" aria-live="polite"></div>' +
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
        '<div class="dactions">' +
        '<button id="drawer-search-btn" title="Search by name" aria-label="Search by name">' + ICONS.search + "</button>" +
        '<button id="drawer-close" title="Close" aria-label="Close">×</button>' +
        "</div></div>" +
        '<div class="dhint" id="drawer-hint">Drag a snippet onto the page, or click one to insert it at the cursor.</div>' +
        '<div class="dsearch" id="drawer-search" hidden><input type="search" id="snip-q" placeholder="Search by name&hellip;" aria-label="Search snippets by name"></div>' +
        '<div class="dcat" id="drawer-cat" hidden><select id="snip-cat" aria-label="Snippet category"></select></div>' +
        '<div class="dlist" id="snip-list"></div>' +
        "</div>" +
        '<div class="dlg-overlay" id="mm-overlay"></div>' +
        '<div class="dlg" id="mm" role="dialog" aria-modal="true">' +
        '<p id="mm-title">Menu item</p>' +
        '<div class="fld"><label>Menu text</label>' +
        '<input type="text" id="mm-label" class="tinput" placeholder="e.g. About us"></div>' +
        '<div class="fld"><label>Links to</label>' +
        '<label class="chk"><input type="radio" name="mmkind" id="mm-kind-page" value="page">A page on this site</label>' +
        '<label class="chk"><input type="radio" name="mmkind" id="mm-kind-url" value="url">A web address</label>' +
        '<label class="chk" id="mm-kind-drop-row"><input type="radio" name="mmkind" id="mm-kind-drop" value="dropdown">' +
        "Nothing — it opens a dropdown menu</label></div>" +
        '<div class="fld" id="mm-page-fld"><label>Page</label>' +
        '<div class="combo"><input type="text" id="mm-page" class="tinput" placeholder="Type to search pages…"' +
        ' autocomplete="off"><div class="combo-list" id="mm-page-list" hidden></div></div></div>' +
        '<div class="fld" id="mm-url-fld"><label>Web address</label>' +
        '<input type="text" id="mm-url" class="tinput" placeholder="https://example.com or /contact"></div>' +
        '<div class="fld" id="mm-tab-fld"><label class="chk"><input type="checkbox" id="mm-newtab">Open in a new tab</label></div>' +
        '<p class="derr" id="mm-err" hidden></p>' +
        '<div class="acts">' +
        '<button id="mm-remove" class="rm" hidden>Remove</button>' +
        '<button id="mm-cancel">Cancel</button>' +
        '<button id="mm-ok" class="ok">OK</button>' +
        "</div></div>" +
        '<div class="code-overlay" id="code-overlay"></div>' +
        '<div class="codepanel" id="code-panel">' +
        '<div class="chead"><h2 id="code-title">Page CSS &amp; JS</h2>' +
        '<div class="ctabs">' +
        '<button id="code-tab-css" class="on">CSS</button>' +
        '<button id="code-tab-js">JavaScript</button>' +
        // Site scope only — a page has no head markup of its own, so
        // pagecode.js hides this one for the page panel.
        '<button id="code-tab-meta" hidden>Meta tags</button>' +
        "</div>" +
        '<button id="code-close" title="Close" aria-label="Close">×</button></div>' +
        '<div class="cbody">' +
        '<pre id="code-hl" aria-hidden="true"></pre>' +
        '<textarea id="code-ta" spellcheck="false" autocapitalize="off" autocomplete="off" wrap="off"></textarea>' +
        "</div>" +
        '<div class="cfoot">' +
        // Filled in by pagecode.js per scope (page vs. site-wide).
        '<span class="chint" id="code-hint"></span>' +
        '<button class="mbtn" id="code-cancel">Cancel</button>' +
        '<button class="mbtn primary" id="code-save">Save</button>' +
        "</div></div>" +
        '<div class="btnui" id="btn-ui">' +
        '<button id="btn-set" title="Button settings">' + ICONS.gear + "</button>" +
        '<button id="btn-del" title="Delete button">' + ICONS.trash + "</button>" +
        "</div>" +
        '<div class="btnui" id="snip-ui">' +
        '<button id="snip-move" title="Drag to move this block" draggable="true">⠿</button>' +
        // The move pair sits next to the drag handle because it is the
        // same verb done precisely: dragging goes anywhere but travels
        // through TinyMCE's drop caret, which can land a block inside
        // its neighbour; stepping over one sibling cannot. Each arrow
        // hides when there is nothing on that side to swap with.
        '<button id="snip-up" title="Move this block up">' + ICONS.chevU + "</button>" +
        '<button id="snip-down" title="Move this block down">' + ICONS.chevD + "</button>" +
        // Duplicate lands the copy above or below rather than always
        // in one place and leaving the arrows to finish the job, which
        // is the older ContentBuilder ergonomic. Two presses to put a
        // copy where it was wanted is one too many for the commonest
        // edit there is.
        '<button id="snip-dup-up" title="Duplicate this block above">' + ICONS.dupUp + "</button>" +
        '<button id="snip-dup-down" title="Duplicate this block below">' + ICONS.dupDown + "</button>" +
        '<button id="snip-src" title="Edit the HTML of this block">' + ICONS.code + "</button>" +
        '<button id="snip-set" title="Block settings">' + ICONS.gear + "</button>" +
        '<button id="snip-del" title="Delete this block">' + ICONS.trash + "</button>" +
        "</div>" +
        // The column tool, anchored to the column that was clicked
        // rather than to the block — every button here acts on that one
        // column. It rides alongside the block chrome above instead of
        // replacing it, because a row and a column in it are two things
        // to edit and an editor may well want either.
        // The column resize handles, one per gutter in the active row.
        // They are built and placed by colresize.js; this is only
        // somewhere in the shadow root for them to hang from.
        '<div id="col-handles"></div>' +
        '<div class="btnui" id="col-ui">' +
        '<button id="col-back" title="Move this column left">' + ICONS.chevL + "</button>" +
        '<button id="col-on" title="Move this column right">' + ICONS.chevR + "</button>" +
        '<button id="col-narrow" title="Make this column narrower">' + ICONS.narrower + "</button>" +
        '<button id="col-wide" title="Make this column wider">' + ICONS.wider + "</button>" +
        '<button id="col-dup-back" title="Duplicate this column to the left">' + ICONS.dupLeft + "</button>" +
        '<button id="col-dup-on" title="Duplicate this column to the right">' + ICONS.dupRight + "</button>" +
        '<button id="col-add" title="Add a column">' + ICONS.plus + "</button>" +
        // The same gear the block chrome carries, pointed at one cell:
        // a column is a box like any other, and giving this one a
        // background should not mean learning a second control.
        '<button id="col-set" title="Column settings">' + ICONS.gear + "</button>" +
        '<button id="col-del" title="Remove this column">' + ICONS.trash + "</button>" +
        "</div>" +
        // The question tool, anchored to the disclosure that was
        // clicked. Same four verbs as the column tool minus resizing,
        // which a stacked list has no equivalent of — someone who has
        // reshaped a row already knows how to reshape an accordion.
        '<div class="btnui" id="faq-ui">' +
        '<button id="faq-up" title="Move this question up">' + ICONS.chevU + "</button>" +
        '<button id="faq-down" title="Move this question down">' + ICONS.chevD + "</button>" +
        '<button id="faq-add" title="Add a question below this one">' + ICONS.plus + "</button>" +
        '<button id="faq-del" title="Delete this question">' + ICONS.trash + "</button>" +
        "</div>" +
        // The team-card tool, anchored to the card that was clicked.
        // The column tool's vocabulary minus the two resize buttons: a
        // card in a wrapping grid is what a column is, except that its
        // width belongs to the breakpoint rather than to the editor, so
        // "narrower" has nothing to act on. Same icons in the same
        // order, so reshaping a staff page is a thing somebody already
        // knows how to do by the time they get here.
        '<div class="btnui" id="team-ui">' +
        '<button id="team-back" title="Move this person left">' + ICONS.chevL + "</button>" +
        '<button id="team-on" title="Move this person right">' + ICONS.chevR + "</button>" +
        '<button id="team-dup-back" title="Duplicate this person to the left">' + ICONS.dupLeft + "</button>" +
        '<button id="team-dup-on" title="Duplicate this person to the right">' + ICONS.dupRight + "</button>" +
        '<button id="team-add" title="Add a blank person after this one">' + ICONS.plus + "</button>" +
        '<button id="team-del" title="Delete this person">' + ICONS.trash + "</button>" +
        "</div>" +
        '<div class="btnui" id="img-ui">' +
        '<button id="img-set" title="Image settings">' + ICONS.gear + "</button>" +
        '<button id="img-del" title="Delete image">' + ICONS.trash + "</button>" +
        "</div>" +
        // Template image slots ({{cmsImage}}). A gear, like every other
        // "settings for the thing you just clicked" control here — the
        // image gear above, the video one below, the section one in
        // buttons.js.
        //
        // It used to be a pencil, on the reasoning that a slot offers
        // less than the image gear does: the size, the link and the
        // caption all belong to the template, so the only choice left is
        // which picture. True, and the wrong thing to spend the icon on.
        // What an editor has to work out first is that the toolbar
        // belongs to the picture under it at all, and every other
        // toolbar teaches them a gear means exactly that. Worse, the
        // pencil is already the Edit button's own glyph, so the one
        // control that opens the media library wore the icon for
        // entering edit mode. How much sits behind the gear is what the
        // panel is for; the tooltip still says "Choose a picture".
        //
        // The trash can shows only for a slot that actually holds one.
        '<div class="btnui" id="slot-ui">' +
        '<button id="slot-pick" title="Choose a picture">' + ICONS.gear + "</button>" +
        '<button id="slot-clear" title="Remove this picture">' + ICONS.trash + "</button>" +
        "</div>" +
        '<div class="btnui" id="vid-ui">' +
        '<button id="vid-set" title="Change this video">' + ICONS.gear + "</button>" +
        '<button id="vid-del" title="Delete video">' + ICONS.trash + "</button>" +
        "</div>" +
        '<div class="code-overlay" id="src-overlay"></div>' +
        '<div class="codepanel" id="src-panel">' +
        '<div class="chead"><h2 id="src-title">HTML source</h2>' +
        '<button id="src-close" title="Close" aria-label="Close">×</button></div>' +
        '<div class="cbody">' +
        '<pre id="src-hl" aria-hidden="true"></pre>' +
        '<textarea id="src-ta" spellcheck="false" autocapitalize="off" autocomplete="off" wrap="off"></textarea>' +
        "</div>" +
        '<div class="cfoot">' +
        '<span class="chint" id="src-hint"></span>' +
        '<button class="mbtn" id="src-cancel">Cancel</button>' +
        '<button class="mbtn primary" id="src-apply">Apply</button>' +
        "</div></div>" +
        '<div class="dlg-overlay" id="dlg-overlay"></div>' +
        '<div class="dlg" id="dlg" role="dialog" aria-modal="true">' +
        '<p id="dlg-msg"></p>' +
        '<div class="tabs" id="dlg-tabs" hidden></div>' +
        '<input type="text" id="dlg-input" hidden>' +
        '<p class="derr" id="dlg-err" hidden></p>' +
        // The scrolling middle. The message, the tab bar, and the
        // actions stay put; only the controls scroll, so a long panel
        // never pushes Save off the bottom of the dialog.
        '<div class="dbody">' +
        '<div id="dlg-fields"></div>' +
        '<div id="dlg-preview" hidden></div>' +
        "</div>" +
        '<div class="acts">' +
        '<button id="dlg-cancel">Cancel</button>' +
        '<button id="dlg-ok" class="ok">OK</button>' +
        "</div></div>";
    document.documentElement.appendChild(host);
    host.classList.toggle("light", state.editorTheme === "light");

    $("admin").href = adminPath + "/";
    updateChip();
}

/* setChromeTheme repaints the editor's own chrome light or dark.
 *
 * Two classes, because the chrome lives in two places: the shadow root
 * holds the bar, rail, panels and floating pills, and the light DOM
 * holds the section pills and TinyMCE's toolbar, which cannot be
 * reached from inside a shadow root. Each carries a palette of custom
 * properties that the theme class swaps wholesale.
 *
 * TinyMCE's own skin is not repainted from here — it is chosen when an
 * editor is created — so a live switch has to rebuild the instances;
 * see settings.js.
 */
export function setChromeTheme(theme) {
    state.editorTheme = theme === "light" ? "light" : "dark";
    var light = state.editorTheme === "light";
    if (host) host.classList.toggle("light", light);
    document.body.classList.toggle("cms-chrome-light", light);
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

    // The light-DOM half of the chrome palette (section pills, the
    // toolbar strip). The shadow half is set in initShell.
    document.body.classList.toggle("cms-chrome-light", state.editorTheme === "light");

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

// expandBar brings a minimized bar back, for actions started outside it
// (the wrench menu's Edit entry) whose controls and status messages live
// on the bar. Expand-only: minimizing stays the bar's own business.
export function expandBar() {
    if ($("bar").classList.contains("min")) setBarMinimized(false);
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
