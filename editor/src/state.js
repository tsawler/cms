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

// Configured site locales ([0] = default). More than one shows the edit
// bar's language switcher; the current render's locale is cfg.locale.
export var locales = [];
try {
    locales = JSON.parse(cfg.locales || "[]") || [];
} catch (e) { /* no switcher */ }
export var defaultLocale = locales[0] || cfg.locale || "en";

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

export var state = {
    pageStatus: cfg.status || "draft",
    visibility: cfg.visibility || "public",
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
