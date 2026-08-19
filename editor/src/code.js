/* ------------------------------------------------------------------ *
 * Custom-code blocks (admin-only): markup with its own <script>, kept
 * in a library and referenced from the page.
 *
 * What a page stores is an inert placeholder —
 * <div class="cms-snippet cms-code" data-cms-code="key"></div> — and
 * nothing else. The code itself lives behind the admin-only /api/code
 * endpoints, so an editor's save carries the reference through the
 * server's HTML sanitizer untouched without carrying anything
 * executable past it, and nothing runs inside the page being edited.
 * A public render swaps each placeholder for the markup its key names.
 *
 * The placeholder is a snippet block like any other: click it for the
 * usual chrome (drag to move, gear, trash) and the ⟨/⟩ button, which
 * for these blocks opens the library entry's code rather than the
 * placeholder's own markup (see buttons.js).
 * ------------------------------------------------------------------ */

import { api, setMsg, flash } from "./util.js";
import { openDialog, cmsPrompt, cmsConfirm } from "./dialogs.js";
import { openSource } from "./source.js";

// NEW is the sentinel value of the chooser's "create one" entry. Not a
// valid key (keys are lowercase letters, digits, and hyphens), so it can
// never collide with a real one.
var NEW = "+new";

// STARTER is what a new library entry begins as: the two halves an
// author needs, and the one line worth knowing — how a block finds
// itself on a page that may be using it more than once.
var STARTER = '<div class="cms-code-body">\n' +
    "  <!-- Your markup goes here. -->\n" +
    "</div>\n" +
    "<" + "script>\n" +
    "(function () {\n" +
    '    var root = document.currentScript.closest(".cms-code");\n' +
    "    // Your JavaScript goes here. `root` is this block on the page,\n" +
    "    // so a block used twice still finds its own markup.\n" +
    "}());\n" +
    "<" + "/script>\n";

// codeBlockHTML is the placeholder a page stores. The cms-snippet class
// buys the standard block chrome; cms-code is what the editor's styles
// and the server's expansion look for.
export function codeBlockHTML(key) {
    return '<div class="cms-snippet cms-code" data-cms-code="' + key + '"></div>';
}

// createCodeBlock asks for a name and adds an empty library entry,
// resolving with it — or with null if the prompt was dismissed or the
// server refused.
function createCodeBlock() {
    return cmsPrompt("Name for the new code block", "e.g. Booking widget", "Create")
        .then(function (name) {
            if (name === null || name.trim() === "") return null;
            return api("/code", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ name: name.trim(), html: STARTER }),
            });
        })
        .catch(function (err) {
            setMsg(err.message);
            return null;
        });
}

// chooseCodeBlock is the drawer's "Custom code" card: pick a library
// entry (or make one), then hand its placeholder markup to insert. With
// an empty library it skips the chooser and goes straight to creating.
export function chooseCodeBlock(insert) {
    api("/code").then(function (body) {
        var list = body.code || [];
        if (list.length === 0) {
            createCodeBlock().then(function (c) {
                if (c) insertAndOffer(c, insert);
            });
            return;
        }
        var options = list.map(function (c) {
            return { value: c.key, label: c.name };
        });
        options.push({ value: NEW, label: "New code block…" });
        openDialog({
            message: "Which code block?",
            okLabel: "Insert",
            selects: [{ id: "key", label: "Code block", value: options[0].value, options: options }],
        }).then(function (v) {
            if (!v) return;
            if (v.key === NEW) {
                createCodeBlock().then(function (c) {
                    if (c) insertAndOffer(c, insert);
                });
                return;
            }
            var picked = list.filter(function (c) { return c.key === v.key; })[0];
            insert(codeBlockHTML(v.key));
            flash("Added " + (picked ? picked.name : v.key) +
                " — click the block, then ⟨/⟩, to edit its code");
        });
    }).catch(function (err) { setMsg(err.message); });
}

// insertAndOffer places a freshly created block and opens its code right
// away: someone who just named a block wants to write it.
function insertAndOffer(c, insert) {
    insert(codeBlockHTML(c.key));
    editCode(c);
}

// openCodeEditor opens the library entry a placeholder names. A key with
// nothing behind it — the entry was deleted, or the block was pasted
// from a page on another site — offers to create it rather than dead-end.
export function openCodeEditor(el) {
    var key = el.getAttribute("data-cms-code");
    if (!key) return;
    api("/code/" + encodeURIComponent(key)).then(editCode).catch(function (err) {
        cmsConfirm("This block's code is missing (" + key + "). Create it?", "Create")
            .then(function (yes) {
                if (!yes) {
                    setMsg(err.message);
                    return;
                }
                api("/code", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ key: key, name: key, html: STARTER }),
                }).then(editCode).catch(function (e) { setMsg(e.message); });
            });
    });
}

// editCode shows one entry's markup in the source modal and saves what
// comes back. The save is immediate and page-independent: the code
// belongs to the library, not to this page, so the edit bar's Save has
// nothing to do with it — hence the reload prompt rather than a quiet
// success.
function editCode(c) {
    openSource({
        title: "Custom code — " + c.name,
        hint: "Markup and <script> for this block, shared by every page that uses it. " +
            "Applying saves it straight away.",
        html: c.html,
    }).then(function (html) {
        if (html === null) return;
        api("/code/" + encodeURIComponent(c.key), {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ name: c.name, html: html }),
        }).then(function () {
            flash("Code saved — it runs on the next page load");
        }).catch(function (err) { setMsg(err.message); });
    });
}
