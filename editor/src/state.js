/* Configuration read from the injected script tag, and the mutable
 * editing-session state every module shares.
 *
 * Config values are plain exports (read-only after parse). Mutable state
 * lives on the single `state` object so writes from one module are seen
 * by all — ES-module live bindings only propagate reads, so exported
 * `let` variables can't be assigned across modules. */

export var cfg = document.currentScript.dataset;

export var pageId = cfg.pageId;
export var adminPath = cfg.adminPath || "/admin";
export var csrf = cfg.csrf;
export var mediaEnabled = cfg.media === "1";
export var isAdmin = cfg.isAdmin === "1";
// Superadmin unlocks the site settings dialog's development/production
// switch. The server enforces it regardless.
export var isSuperadmin = cfg.isSuperadmin === "1";
export var postsEnabled = cfg.posts === "1";
// Per-user permissions: cross-page chrome (new page, menus, site
// settings) needs canPages; creating a post needs its feed's flag. The
// server enforces all of this regardless.
export var canPages = cfg.canPages === "1";
export var canBlogs = cfg.canBlogs === "1";
export var canNews = cfg.canNews === "1";

// Configured site locales ([0] = default). More than one shows the edit
// bar's language switcher; the current render's locale is cfg.locale.
export var locales = [];
try {
    locales = JSON.parse(cfg.locales || "[]") || [];
} catch (e) { /* no switcher */ }
export var defaultLocale = locales[0] || cfg.locale || "en";

// When the current page backs a blog/news post: {id, feed, summary,
// publishedAt, hideAuthor, authorName, thumbnailUrl, thumbnailMediaId}.
// Null on ordinary pages. Mutable:
// the post-settings dialog updates it after a save so reopening shows
// the saved values.
export var postInfo = null;
try {
    postInfo = JSON.parse(cfg.post || "null");
} catch (e) { /* no post-settings gear */ }

// The notice bar's stored words, sent whether or not the bar is
// currently showing: switching it on in the settings dialog inserts the
// bar client-side, and it should come back carrying what the site
// already says rather than an empty placeholder. Mutable — the dialog
// stashes the bar's current content here when it removes one, so a
// switch off and on again does not throw away unsaved typing.
export var notice = { html: cfg.notice || "" };

// Where the editor's own assets live, derived from this script's URL
// rather than written down.
//
// The server serves the bundle under a digest of its contents —
// /cms/editor/<version>/editor.js — so that shipping a new editor changes
// the address and no browser keeps running a cached copy of the old one.
// A hard-coded "/cms/editor/" would send TinyMCE to fetch its skins and
// plugins from the unversioned path, which is the one place that trick
// does not protect: the script would be current and its skin a year old.
//
// document.currentScript is the tag being executed, and reading it at
// module scope is what makes that work — it is null once any callback
// runs. The literal is a fallback for the case where this file is loaded
// some other way; it resolves to files that are still served, just
// without the cache guarantee.
export var EDITOR_BASE = (function () {
    var src = document.currentScript && document.currentScript.src;
    if (!src) return "/cms/editor/";
    return src.slice(0, src.lastIndexOf("/") + 1);
})();

// The Styles menu: named, on-brand styles configured on the server.
// Each applies CSS classes, so the site's stylesheet stays in charge.
// Entries sharing a group fold into a submenu, placed where the
// group's first member appears among the ungrouped entries.
export var styleFormats = [];
try {
    var styleGroups = {}; // group title -> its {title, items} submenu
    (JSON.parse(cfg.styles || "[]") || []).forEach(function (s) {
        if (!s || !s.label || !s.class) return;
        var f = { title: s.label, classes: s.class.split(/\s+/) };
        if (s.block) f.block = s.block;
        else f.inline = "span";
        if (s.group) {
            if (!styleGroups[s.group]) {
                styleGroups[s.group] = { title: s.group, items: [] };
                styleFormats.push(styleGroups[s.group]);
            }
            styleGroups[s.group].items.push(f);
        } else {
            styleFormats.push(f);
        }
    });
} catch (e) { /* malformed config: no Styles menu */ }

// Curated section settings (backgrounds/widths/corners) from the server.
// corners may be empty: a host that ships no corner options hides the
// gear dialog's rounding field.
export var sectionStyles = { backgrounds: [{ key: "default", label: "Default", class: "" }],
    widths: [{ key: "normal", label: "Normal", class: "" }], corners: [] };
try {
    var ss = JSON.parse(cfg.sectionStyles || "null");
    if (ss && ss.backgrounds && ss.backgrounds.length && ss.widths && ss.widths.length) {
        if (!ss.corners) ss.corners = [];
        sectionStyles = ss;
    }
} catch (e) { /* fall back to the minimal defaults above */ }

// Page templates the site offers, for the "new page" dialog.
export var pageTemplates = [];
try {
    pageTemplates = JSON.parse(cfg.pageTemplates || "[]") || [];
} catch (e) { /* no new-page button */ }

// filledSlots is which image slots ({{cmsImage}}) hold a stored picture,
// as {region name: true}. The page cannot be read for this — a slot's
// <img> looks the same whether its src is a chosen picture or the
// template's own fallback — so the server sends the list, and it is what
// decides whether an image slot offers to remove anything. Mutable:
// choosing a picture fills a slot, clearing one empties it.
var filledImages = [];
try {
    filledImages = JSON.parse(cfg.filledImages || "[]") || [];
} catch (e) { /* nothing offers to be cleared */ }

function filledSlotMap() {
    var out = {};
    filledImages.forEach(function (name) { out[name] = true; });
    return out;
}

// resetFilledSlots puts the map back to what the server sent, for Cancel
// — which restores the page's images, and has to restore what the editor
// believes about them along with it.
export function resetFilledSlots() {
    state.filledSlots = filledSlotMap();
}

export var state = {
    // The editor's own chrome: "dark" (the default) or "light". A site
    // whose design is dark needs the light one — dark chrome over a
    // dark page is chrome you have to hunt for. Mutable: the site
    // settings dialog switches it without a reload.
    editorTheme: cfg.editorTheme === "light" ? "light" : "dark",

    pageStatus: cfg.status || "draft",
    visibility: cfg.visibility || "public",
    // True when a published page's saved draft differs from what's live.
    hasUnpublished: cfg.unpublished === "1",

    editing: false,
    dirty: {}, // region name -> true
    sectionsDirty: {}, // sections region name -> true
    titleDirty: false, // the page title ({{cmsTitle}}) was typed in
    // The last title the server is known to hold, restored when the
    // heading is emptied. Null until edit mode first reads the page.
    titleSaved: null,
    imageValues: {}, // image region name -> chosen URL
    filledSlots: filledSlotMap(), // image region name -> true, when it holds a picture
    mceEditors: {}, // region name -> TinyMCE editor instance
    sectionEditors: [], // {el, ed, region} per section content container
    lastEditor: null, // most recently focused TinyMCE instance
    lastEditorDirty: null, // dirty-marker matching lastEditor
    pendingSection: null, // {region, after} while choosing a new section's start
    snapshot: null, // page state captured when Edit was pressed
};
