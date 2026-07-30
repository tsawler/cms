/* ------------------------------------------------------------------ *
 * The page title, edited on the page.
 *
 * A template that prints {{cmsTitle}} gets a marker element instead of
 * bare text on an edit render, and this module makes it typeable: the
 * words a post or page shows as its heading are the same words the
 * page-settings dialog edits, so the heading is no longer a read-only
 * echo of a field hidden in a menu.
 *
 * It is not a region. There is one title per page per locale, stored on
 * the page rather than in a content block, so it saves to the metadata
 * endpoint rather than travelling with the region save — and, because a
 * title is one line, Enter and pasted line breaks are refused rather
 * than quietly becoming markup.
 * ------------------------------------------------------------------ */

import { state, cfg, pageId } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg } from "./util.js";
import { updateBarButtons } from "./editing.js";

// A title is one line of text: whatever route the words came in by
// (typing, paste, a drop), this is what is stored and shown.
function clean(text) {
    return (text || "").replace(/\s+/g, " ").trim();
}

export function titleEls() {
    return Array.prototype.slice.call(document.querySelectorAll("[data-cms-title]"));
}

// titleValue is what a save would send: the current text of the first
// marker on the page. A template printing the title twice keeps both in
// step (see the input handler), so any of them would do.
export function titleValue() {
    var els = titleEls();
    return els.length ? clean(els[0].textContent) : "";
}

// setTitleValue writes text into every marker, and remembers it as the
// value an emptied title falls back to.
function setTitleValue(text) {
    titleEls().forEach(function (el) { el.textContent = text; });
    state.titleSaved = text;
}

export function setTitleEditing(on) {
    titleEls().forEach(function (el) {
        if (on) {
            el.setAttribute("contenteditable", "plaintext-only");
            if (!el.isContentEditable) el.setAttribute("contenteditable", "true"); // Firefox
            el.title = "The page title — shown here, in the browser tab, and in search results";
        } else {
            el.removeAttribute("contenteditable");
            el.removeAttribute("title");
        }
    });
    if (on && state.titleSaved === null) state.titleSaved = titleValue();
}

export function markTitleDirty() {
    state.titleDirty = true;
    $("save").disabled = false;
    updateBarButtons();
}

// saveTitle is a step in the page save, run only when the title was
// touched. An empty title is not saved: the default locale requires one,
// and on a translation clearing the field is a "go back to the default
// language" that belongs in the page-settings dialog, where it says so.
export function saveTitle() {
    var value = titleValue();
    if (value === "") {
        setTitleValue(state.titleSaved || "");
        state.titleDirty = false;
        return Promise.resolve();
    }
    return api("/pages/" + pageId + "/meta", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        // No description: the endpoint leaves out what it isn't sent, so
        // saving a title here can't blank the page's meta description.
        body: JSON.stringify({ locale: cfg.locale, title: value }),
    }).then(function () {
        state.titleSaved = value;
        state.titleDirty = false;
    });
}

export function initTitle() {
    document.addEventListener("input", function (e) {
        if (!state.editing) return;
        var el = e.target.closest ? e.target.closest("[data-cms-title]") : null;
        if (!el) return;
        markTitleDirty();
        // Two headings showing one title stay in step while typing; the
        // one being typed in is left alone so the caret doesn't jump.
        titleEls().forEach(function (other) {
            if (other !== el) other.textContent = el.textContent;
        });
    });

    // Enter would put a line break in a field that is one line by
    // definition; it reads as "done here" instead.
    document.addEventListener("keydown", function (e) {
        if (!state.editing || e.key !== "Enter") return;
        var el = e.target.closest ? e.target.closest("[data-cms-title]") : null;
        if (!el) return;
        e.preventDefault();
        el.blur();
    });

    // Leaving an emptied title puts the last saved words back, rather
    // than letting the page sit headless until the save refuses it.
    document.addEventListener("focusout", function (e) {
        if (!state.editing) return;
        var el = e.target.closest ? e.target.closest("[data-cms-title]") : null;
        if (!el) return;
        if (titleValue() === "") {
            setTitleValue(state.titleSaved || "");
            setMsg("A page needs a title.");
            return;
        }
        // Whitespace and pasted line breaks are normalized on the way
        // out, so what stays on screen is what a save would store.
        var value = titleValue();
        if (el.textContent !== value) {
            titleEls().forEach(function (other) { other.textContent = value; });
        }
    });
}
