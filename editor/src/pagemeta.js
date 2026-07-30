/* ------------------------------------------------------------------ *
 * Page settings — the edit bar's ⋯ menu entry for the two things a
 * page carries that aren't on the page: its <title> and its meta
 * description.
 *
 * Both are per-locale, and the dialog edits whichever locale the bar is
 * currently in — so translating a title is: switch to FR, open page
 * settings, type. What a locale has none of is left empty and shown as
 * a placeholder instead, because the page falls back to the default
 * language field by field; prefilling would turn "not translated yet"
 * into a copy of the English on the next save.
 *
 * The post-settings gear edits the same two fields (a post's title and
 * its summary are its backing page's metadata), so it shares the field
 * builders here.
 * ------------------------------------------------------------------ */

import { cfg, state, pageId, defaultLocale, locales } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { openDialog } from "./dialogs.js";
import { updateChip } from "./saving.js";
import { updateBarButtons } from "./editing.js";

// isTranslation is true when the bar is on a locale that falls back to
// another one — the case where an empty field means "inherit".
export function isTranslation() {
    return locales.length > 1 && cfg.locale !== defaultLocale;
}

// localeSuffix names the language being edited, for dialog titles. It
// stays out of the way on a single-language site.
export function localeSuffix() {
    return locales.length > 1 ? " — " + cfg.locale.toUpperCase() : "";
}

// fetchMeta reads this page's stored title and description for the
// current locale, unresolved: empty fields are genuinely empty, and the
// inherited* values are what the page falls back to.
export function fetchMeta() {
    return api("/pages/" + pageId + "/meta?locale=" + encodeURIComponent(cfg.locale));
}

// inheritPlaceholder is what an empty translated field shows: the words
// the page falls back to, marked as inherited so they don't read as a
// value that is already saved.
function inheritPlaceholder(inherited, fallback) {
    if (isTranslation() && inherited) {
        return defaultLocale.toUpperCase() + ": " + inherited;
    }
    return fallback;
}

// charCount is the live length note under whichever field ends up being
// the one search engines read. The useful length of a description is a
// fact about search results rather than about the field, so it is worth
// saying while it is being typed.
function charCount(id) {
    return { type: "note", span: true, text: function (v) {
        var n = (v[id] || "").length;
        if (!n) return "";
        return n + " characters" + (n > 160
            ? " — search results usually cut off around 160."
            : "");
    } };
}

// metaFields builds the title and description fields both dialogs use.
// opts.descLabel and opts.descHint caption the description: a page's
// meta description, or a post's summary.
//
// opts.meta adds the separate meta description underneath, which is what
// a post has and a page does not — a page's description already is its
// meta description, while a post's is the blurb its listing card shows.
// The count then belongs under that field instead, since it is the one
// search results are cut from.
export function metaFields(meta, opts) {
    var fields = [
        { id: "title", label: "Title", type: "text", value: meta.title, span: true,
            placeholder: inheritPlaceholder(meta.inheritedTitle, "Shown in the browser tab and in search results") },
        { id: "description", label: opts.descLabel, type: "textarea", rows: 3, span: true,
            value: meta.description,
            placeholder: inheritPlaceholder(meta.inheritedDescription, opts.descHint) },
    ];
    if (!opts.meta) {
        fields.push(charCount("description"));
        return fields;
    }
    fields.push({ id: "metaDescription", label: "Meta description", type: "textarea", rows: 3,
        span: true, value: meta.metaDescription,
        placeholder: inheritPlaceholder(meta.inheritedMetaDescription,
            "The summary search engines show — leave empty to use the summary above") });
    fields.push(charCount("metaDescription"));
    return fields;
}

// metaNote is the dialog's closing line. On a translation it explains
// what an empty field does, which is the thing most worth saying there;
// otherwise it says when the save reaches the site. staged names what
// waits on a Publish. Returned as a field so it sits in the grid.
export function metaNote(staged) {
    if (isTranslation()) {
        return { type: "note", span: true, text: function () {
            return "Editing the " + cfg.locale.toUpperCase() + " version. Leave a field empty to " +
                "keep showing the " + defaultLocale.toUpperCase() + " one.";
        } };
    }
    return { type: "note", span: true, text: function () {
        return "Saved as a draft — Publish to make " + staged + " live.";
    } };
}

// afterMetaSave records that the page now has a draft ahead of what is
// live, so the bar's chip and buttons say so without a reload. staged
// names what is waiting on a Publish — everything, for a page; only the
// text, for a post whose date and thumbnail went live with the save.
export function afterMetaSave(what, staged) {
    if (state.pageStatus === "published") {
        state.hasUnpublished = true;
        updateChip();
        updateBarButtons();
        flash(what + " saved — Publish to make " + staged + " live.");
        return;
    }
    flash(what + " saved.");
}

export function initPageSettings() {
    $("meta-btn").addEventListener("click", function () {
        setMsg("Loading page settings…");
        fetchMeta().then(function (meta) {
            setMsg("");
            return openDialog({
                message: "Page settings" + localeSuffix(),
                okLabel: "Save",
                wide: true,
                fields: metaFields(meta, {
                    descLabel: "Meta description",
                    descHint: "The summary search engines show under the page's title",
                }).concat([metaNote("them")]),
            });
        }).then(function (values) {
            if (!values) return;
            setMsg("Saving…");
            return api("/pages/" + pageId + "/meta", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ locale: cfg.locale, title: values.title,
                    description: values.description }),
            }).then(function () {
                afterMetaSave("Page settings", "the change");
            });
        }).catch(function (err) { setMsg(err.message); });
    });
}
