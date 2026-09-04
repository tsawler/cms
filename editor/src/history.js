/* ------------------------------------------------------------------ *
 * Page history: go back to an earlier published version.
 *
 * Every publish keeps a copy of what went live, and this is the way
 * back to one from the page itself. Restoring writes the draft and
 * nothing else, which is what makes a preview unnecessary here: the
 * editor already shows draft content, so the page reloads onto the old
 * version and you are looking at it. From there Publish makes it live
 * and Discard draft puts you back to what the site is serving — the two
 * buttons the edit bar already has, meaning the whole round trip is
 * reversible without this module owning any of it.
 *
 * The newest version is deliberately not offered: it is what the site is
 * already serving, and "go back to now" is Discard draft.
 * ------------------------------------------------------------------ */

import { pageId } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg } from "./util.js";
import { openDialog } from "./dialogs.js";
import { state } from "./state.js";

// tell shows a dialog with nothing to decide: the two cases where there is
// no earlier version to go back to, and the note a restore leaves about
// the page's custom-code blocks.
function tell(message) {
    return openDialog({ message: message, okLabel: "OK" });
}

function openHistory() {
    setMsg("Loading history…");
    api("/pages/" + pageId + "/versions").then(function (body) {
        setMsg("");
        var versions = body.versions || [];
        if (!versions.length) {
            tell("This page has no history yet. It starts the first time the page is published.");
            return;
        }
        var older = versions.slice(1);
        if (!older.length) {
            tell("This page has only ever been published once, so there is no earlier version to go back to.");
            return;
        }

        var options = older.map(function (v) {
            return { value: String(v.id), label: v.by ? v.label + " — " + v.by : v.label };
        });
        var message = "Go back to an earlier published version of this page. " +
            "It is put into the draft, so nothing changes on the site until you publish.";
        if (body.has_unpublished) {
            message += " Your saved but unpublished changes will be replaced.";
        }
        openDialog({
            message: message,
            okLabel: "Restore",
            // Unpublished work is about to be overwritten: the red button
            // and the extra beat of attention that comes with it.
            danger: !!body.has_unpublished,
            fields: [{
                id: "version", label: "Version", type: "select",
                options: options, value: options[0].value, span: true,
            }],
        }).then(function (values) {
            if (!values || !values.version) return;
            restore(values.version);
        });
    }).catch(function (err) { setMsg(err.message); });
}

// restore stages the chosen version and reloads onto it. Unsaved edits
// are cleared first for the same reason discardDraft clears them: the
// draft on the server is now the authority, and beforeunload must not
// offer to keep a copy of what was just replaced.
function restore(id) {
    setMsg("Restoring…");
    api("/pages/" + pageId + "/versions/" + id + "/restore", { method: "POST" }).then(function (body) {
        state.dirty = {};
        state.sectionsDirty = {};
        state.titleDirty = false;
        // A note about the page's custom-code blocks has to be read before
        // the reload, not after it: a toast would not survive the trip.
        if (body && body.note) {
            tell(body.note).then(function () { window.location.reload(); });
            return;
        }
        window.location.reload();
    }).catch(function (err) { setMsg(err.message); });
}

export function initHistory() {
    $("history").addEventListener("click", openHistory);
}
