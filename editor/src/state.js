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
export var isSuperadmin = cfg.isSuperadmin === "1";
export var postsEnabled = cfg.posts === "1";

// When the current page backs a blog/news post: {id, feed, summary,
// publishedAt, thumbnailUrl, headerUrl}. Null on ordinary pages. Mutable:
// the post-settings dialog updates it after a save so reopening shows
// the saved values.
export var postInfo = null;
try {
    postInfo = JSON.parse(cfg.post || "null");
} catch (e) { /* no post-settings gear */ }

export var EDITOR_BASE = "/cms/editor/";

// The Styles menu: named, on-brand styles configured on the server.
// Each applies CSS classes, so the site's stylesheet stays in charge.
export var styleFormats = [];
try {
    (JSON.parse(cfg.styles || "[]") || []).forEach(function (s) {
        if (!s || !s.label || !s.class) return;
        var f = { title: s.label, classes: s.class.split(/\s+/) };
        if (s.block) f.block = s.block;
        else f.inline = "span";
        styleFormats.push(f);
    });
} catch (e) { /* malformed config: no Styles menu */ }

// Curated section settings (backgrounds/widths) from the server.
export var sectionStyles = { backgrounds: [{ key: "default", label: "Default", class: "" }],
    widths: [{ key: "normal", label: "Normal", class: "" }] };
try {
    var ss = JSON.parse(cfg.sectionStyles || "null");
    if (ss && ss.backgrounds && ss.backgrounds.length && ss.widths && ss.widths.length) {
        sectionStyles = ss;
    }
} catch (e) { /* fall back to the minimal defaults above */ }

// Page templates the site offers, for the "new page" dialog.
export var pageTemplates = [];
try {
    pageTemplates = JSON.parse(cfg.pageTemplates || "[]") || [];
} catch (e) { /* no new-page button */ }

export var state = {
    pageStatus: cfg.status || "draft",
    // True when a published page's saved draft differs from what's live.
    hasUnpublished: cfg.unpublished === "1",

    editing: false,
    dirty: {}, // region name -> true
    sectionsDirty: {}, // sections region name -> true
    imageValues: {}, // image region name -> chosen URL
    mceEditors: {}, // region name -> TinyMCE editor instance
    sectionEditors: [], // {el, ed, region} per section content container
    lastEditor: null, // most recently focused TinyMCE instance
    lastEditorDirty: null, // dirty-marker matching lastEditor
    pendingSection: null, // {region, after} while choosing a new section's start
    snapshot: null, // page state captured when Edit was pressed
};
