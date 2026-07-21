/* ------------------------------------------------------------------ *
 * Snippet drawer — the palette of insertable blocks, the tool-rail
 * buttons that open it, and the "new page" dialog on the rail.
 * ------------------------------------------------------------------ */

import { state, pageTemplates } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { openDialog } from "./dialogs.js";
import { markDirty, markSectionsDirty } from "./editing.js";
import { lockButtons } from "./buttons.js";
import { createSection } from "./sections.js";

var snippetsLoaded = false;

export function openDrawer() {
    $("drawer").classList.add("on");
    $("drawer-title").textContent = state.pendingSection ? "Add a section" : "Snippets";
    $("drawer-hint").textContent = state.pendingSection
        ? "Choose a snippet to be the section's starting content."
        : "Drag a snippet onto the page, or click one to insert it at the cursor.";
    if (!snippetsLoaded) loadSnippets();
    updateRail();
}
export function closeDrawer() {
    $("drawer").classList.remove("on");
    state.pendingSection = null;
    updateRail();
}

// updateRail highlights whichever rail button the open drawer belongs
// to (Section when choosing a new section's start, Snippets otherwise).
export function updateRail() {
    var open = $("drawer").classList.contains("on");
    $("rail-add").classList.toggle("on", open && !!state.pendingSection);
    $("rail-snips").classList.toggle("on", open && !state.pendingSection);
}

function loadSnippets() {
    var list = $("snip-list");
    list.innerHTML = '<span class="empty">Loading…</span>';
    api("/snippets", { method: "GET" }).then(function (body) {
        snippetsLoaded = true;
        list.innerHTML = "";
        if (!body.snippets || body.snippets.length === 0) {
            list.appendChild(Object.assign(document.createElement("span"),
                { className: "empty", textContent: "No snippets available." }));
            return;
        }
        body.snippets.forEach(function (sn) {
            var card = document.createElement("div");
            card.className = "snip";
            card.draggable = true;
            var nm = document.createElement("p");
            nm.className = "sname";
            nm.textContent = sn.name;
            var desc = document.createElement("p");
            desc.className = "sdesc";
            var probe = document.createElement("div");
            probe.innerHTML = sn.html;
            desc.textContent = (probe.textContent || "").trim().replace(/\s+/g, " ").slice(0, 90);
            card.appendChild(nm);
            card.appendChild(desc);
            // Drag: TinyMCE accepts text/html drops and inserts the
            // markup at the drop caret inside any rich region.
            card.addEventListener("dragstart", function (e) {
                e.dataTransfer.setData("text/html", sn.html);
                e.dataTransfer.setData("text/plain", sn.name);
                e.dataTransfer.effectAllowed = "copy";
            });
            // dropEffect is "none" when the drag was cancelled or
            // dropped somewhere that refused it — keep the drawer
            // open in that case.
            card.addEventListener("dragend", function (e) {
                if (e.dataTransfer.dropEffect !== "none") {
                    unnestSnippets();
                    closeDrawer();
                }
            });
            // Click: new-section starting point when one is pending,
            // otherwise insert at the cursor.
            card.addEventListener("click", function () { chooseSnippet(sn); });
            list.appendChild(card);
        });
    }).catch(function (err) {
        list.innerHTML = "";
        var span = document.createElement("span");
        span.className = "empty";
        span.textContent = err.message;
        list.appendChild(span);
    });
}

// Snippets are sibling blocks by design, but an insert while the
// caret sits inside another snippet nests them. Lift any nested
// snippet out to sit right after the block it landed in.
export function unnestSnippets() {
    for (var guard = 0; guard < 4; guard++) {
        var nested = document.querySelectorAll(
            "[data-cms-region] .cms-snippet .cms-snippet," +
            "[data-cms-sections] .cms-snippet .cms-snippet");
        if (!nested.length) return;
        nested.forEach(function (inner) {
            var anc = inner.parentElement && inner.parentElement.closest(".cms-snippet");
            if (anc) anc.insertAdjacentElement("afterend", inner);
        });
    }
}

function chooseSnippet(sn) {
    if (state.pendingSection) {
        var target = state.pendingSection;
        closeDrawer();
        createSection(target, sn.html);
        return;
    }
    insertSnippet(sn);
}

function insertSnippet(sn) {
    var ed = state.lastEditor && !state.lastEditor.removed ? state.lastEditor : null;
    var onDirty = state.lastEditorDirty;
    if (!ed) {
        var first = Object.keys(state.mceEditors)[0];
        if (first) {
            ed = state.mceEditors[first];
            onDirty = function () { markDirty(first); };
        } else if (state.sectionEditors.length) {
            ed = state.sectionEditors[0].ed;
            var rg = state.sectionEditors[0].region;
            onDirty = function () { markSectionsDirty(rg); };
        }
    }
    if (!ed) {
        setMsg("Click into a content area first, then insert the snippet.");
        return;
    }
    ed.focus();
    ed.insertContent(sn.html);
    unnestSnippets();
    if (onDirty) onDirty();
    lockButtons(); // the snippet may have brought a button with it
    closeDrawer(); // the insert worked; get out of the way
    flash("Snippet inserted — click it to edit the text");
}

export function initSnippets() {
    // Pages whose template declares no sections region get a visibly
    // disabled Section button rather than a dead-feeling click.
    if (!document.querySelector("[data-cms-sections]")) {
        $("rail-add").disabled = true;
        $("rail-add").title = "This page type has no sections area";
    }

    // Rail buttons toggle: the button matching the drawer's current mode
    // closes it; the other button switches the drawer to its mode.
    $("rail-snips").addEventListener("click", function () {
        if ($("drawer").classList.contains("on") && !state.pendingSection) {
            closeDrawer();
            return;
        }
        state.pendingSection = null;
        openDrawer();
    });
    $("rail-add").addEventListener("click", function () {
        if ($("drawer").classList.contains("on") && state.pendingSection) {
            closeDrawer();
            return;
        }
        var container = document.querySelector("[data-cms-sections]");
        if (!container) {
            setMsg("This page has no sections area.");
            return;
        }
        state.pendingSection = { region: container.getAttribute("data-cms-sections"), after: null };
        openDrawer();
    });
    $("rail-page").hidden = pageTemplates.length === 0;
    $("rail-page").addEventListener("click", function () {
        if (!pageTemplates.length) return;
        openDialog({
            message: "Create a new page",
            prompt: true,
            placeholder: "Page name",
            required: "Give the page a name.",
            okLabel: "Create page",
            selects: [{
                id: "template",
                label: "Page type",
                value: pageTemplates[0].file,
                options: pageTemplates.map(function (t) { return { value: t.file, label: t.label }; }),
            }],
        }).then(function (values) {
            if (!values || !values.input) return;
            setMsg("Creating page…");
            api("/pages", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ title: values.input, template: values.template }),
            }).then(function (body) {
                // The new page is a draft, so only editors see it —
                // navigate straight into it to start writing.
                window.location.href = body.url;
            }).catch(function (err) { setMsg(err.message); });
        });
    });

    $("drawer-close").addEventListener("click", closeDrawer);
}
