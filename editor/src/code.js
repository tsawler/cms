/* ------------------------------------------------------------------ *
 * Custom-code blocks (admin-only): markup with its own <script>, kept
 * in a library and referenced from the page.
 *
 * What a page stores is an inert placeholder —
 * <div class="cms-snippet cms-code" data-cms-code="key"></div> — and
 * nothing else. The code itself lives behind the admin-only /api/code
 * endpoints, so an editor's save carries the reference through the
 * server's HTML sanitizer untouched without carrying anything
 * executable past it. Every render swaps each placeholder for the
 * markup its key names; an edit render parks the <script> tags in that
 * markup under a type no browser runs (render.InertScriptType), which
 * leaves this module three jobs on a page a logged-in editor has open:
 *
 *   captureCode  — remember each key's markup as the server sent it,
 *                  before anything has run. That is the block's declared
 *                  source, as against whatever its script later makes of
 *                  the DOM.
 *   fillCode     — put that source back and let its scripts run, so
 *                  someone merely looking at the page sees the page a
 *                  visitor sees.
 *   collapseCode — empty every block again when edit mode starts. The
 *                  widget's markup goes, its runtime DOM goes with it,
 *                  and what is left is the placeholder a save is meant
 *                  to store.
 *
 * Emptying a block is not a sandbox: a script that registered a timer or
 * a document-level listener on its way past is still registered, and
 * only a reload is rid of it. What collapsing does guarantee is that
 * nothing the widget built ever reaches the snapshot Cancel restores
 * from, the HTML TinyMCE takes over, or the markup a save serializes.
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

/* ---- block lifecycle: capture, fill, collapse ---- */

// INERT is the type an edit render parks a block's scripts under; it has
// to match render.InertScriptType.
var INERT = "text/cms-code";

// sources remembers each library entry's markup, keyed by code key
// rather than by element. The markup belongs to the entry, so two
// instances of one block share it — and, more to the point, the map
// outlives the elements: entering and leaving edit mode replaces
// regions wholesale (TinyMCE's teardown, Cancel's snapshot restore), so
// anything keyed on the element itself would be gone by the time it was
// needed.
var sources = {};

function codeBlocks() {
    return Array.prototype.slice.call(
        document.querySelectorAll(".cms-code[data-cms-code]"));
}

// activate replaces the parked <script> tags inside one filled block
// with live ones. A script node runs when the parser inserts it or when
// a fresh one is inserted by script — copying the attributes and the
// text onto a new element is what makes it go. Insertion happens after
// the node is in the tree, so the block's own
// document.currentScript.closest(".cms-code") still finds itself.
function activate(el) {
    el.querySelectorAll("script").forEach(function (old) {
        var live = document.createElement("script");
        Array.prototype.forEach.call(old.attributes, function (a) {
            if (a.name !== "type" && a.name !== "data-cms-type") {
                live.setAttribute(a.name, a.value);
            }
        });
        // A script element created by script carries the "force async"
        // flag, so external scripts run in whatever order they finish
        // downloading — not the order they are written in. A block that
        // loads a library and then something that uses it therefore
        // works for a visitor, whose scripts the parser ran in order,
        // and fails intermittently for the editor looking at the same
        // page. Clearing the flag restores document order. Only for
        // src'd scripts that did not ask for async themselves: an author
        // who wrote async meant it.
        if (live.src && !old.hasAttribute("async")) {
            live.async = false;
        }
        // The type the author wrote, if any: from data-cms-type on
        // markup the server parked, or from the tag itself on markup
        // that came straight from the library over /api/code.
        var type = old.getAttribute("data-cms-type");
        if (type === null && old.getAttribute("type") !== INERT) {
            type = old.getAttribute("type");
        }
        if (type !== null) live.setAttribute("type", type);
        live.text = old.text;
        old.parentNode.replaceChild(live, old);
    });
}

// captureCode remembers every block's markup exactly as the server sent
// it. Called once, before anything has had a chance to run, so what it
// stores is the block's declared source.
export function captureCode() {
    codeBlocks().forEach(function (el) {
        var key = el.getAttribute("data-cms-code");
        if (key && !(key in sources)) sources[key] = el.innerHTML;
    });
}

// fillCode puts every block back to its source and runs it: the state a
// page is in whenever a logged-in editor is only looking at it. Filling
// from the source rather than from what is on screen makes this
// idempotent — a second call replaces the first call's output whole
// rather than compounding it.
function fillCode() {
    codeBlocks().forEach(function (el) {
        var src = sources[el.getAttribute("data-cms-code")];
        if (!src) return;
        el.innerHTML = src; // innerHTML never runs a script; activate does
        activate(el);
    });
}

// reviveCode is fillCode deferred by a microtask. Leaving edit mode and
// putting the DOM back are two steps and Cancel does both in one turn
// (setEditing(false), then restoreSnapshot), so running on the spot
// would fill the DOM that is about to be thrown away and then fill the
// replacement — two runs of every widget. Waiting for the turn to end
// fills whichever DOM won, once.
var pending = false;
export function reviveCode() {
    if (pending) return;
    pending = true;
    Promise.resolve().then(function () {
        pending = false;
        fillCode();
    });
}

// collapseCode empties every block. Edit mode runs this first, before
// the snapshot is taken and before TinyMCE attaches, so neither ever
// sees the widget — only the placeholder that stored content holds.
export function collapseCode() {
    codeBlocks().forEach(function (el) { el.innerHTML = ""; });
}

// cacheCode makes sure a key's markup is known, fetching it when the
// page carried no block with that key to capture it from — which is the
// case for one the editor has just inserted. Failure is silent: the
// block stays empty until the next page load, exactly as it would have
// before.
function cacheCode(key) {
    if (key in sources) return;
    api("/code/" + encodeURIComponent(key)).then(function (c) {
        sources[c.key] = c.html;
    }).catch(function () {});
}

// initCode fills the blocks on a page the editor has just loaded. It
// runs from a deferred script, so a widget waiting on DOMContentLoaded
// still gets its event.
export function initCode() {
    captureCode();
    fillCode();
}

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
            // The chooser lists names only, so fetch the markup the new
            // block will want when the page goes back to being viewed.
            cacheCode(v.key);
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
// nothing to do with it.
//
// Both the opened markup and the saved markup go into sources, so a
// block created or rewritten during this editing session is the one that
// runs when edit mode ends — no reload in between, and a block inserted
// just now appears the moment the page goes back to being viewed.
function editCode(c) {
    sources[c.key] = c.html;
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
            sources[c.key] = html;
            flash("Code saved — it runs when you leave edit mode");
        }).catch(function (err) { setMsg(err.message); });
    });
}
