/* ------------------------------------------------------------------ *
 * Snippet drawer — the palette of insertable blocks, the tool-rail
 * buttons that open it, and the "new page" dialog on the rail.
 * ------------------------------------------------------------------ */

import { state, pageTemplates, postsEnabled, mediaEnabled, canPages, canBlogs, canNews, isAdmin } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { openDialog } from "./dialogs.js";
import { markDirty, markSectionsDirty } from "./editing.js";
import { lockButtons } from "./buttons.js";
import { createSection, presetSectionHTML } from "./sections.js";
import { chooseCodeBlock } from "./code.js";
import { cellFor } from "./columns.js";

var snippetsLoaded = false;

/* ---- drawer thumbnails -------------------------------------------
 * Snippet cards show a live miniature of their actual markup. The
 * drawer lives in the editor's shadow root (host CSS can't reach it),
 * so each thumbnail is a tiny iframe that borrows the host page's
 * stylesheets and renders the fragment at page width, scaled down by
 * CSS. Section presets render the same way, wrapped in the section
 * markup their settings would produce (edge-to-edge, so no padding). */

var hostHeadCache = null;
function hostHead() {
    if (hostHeadCache !== null) return hostHeadCache;
    var parts = [];
    document.querySelectorAll("link[rel=stylesheet],style").forEach(function (n) {
        // Skip TinyMCE's injected skin — editor chrome, not site style.
        var href = (n.getAttribute && n.getAttribute("href")) || "";
        if ((n.id || "").indexOf("mce-") === 0 || href.indexOf("tinymce") !== -1) return;
        parts.push(n.outerHTML);
    });
    // JIT setups (Tailwind Play CDN) only generate CSS for classes seen
    // on this page; ship the script so the frame covers its own classes.
    var tw = document.querySelector('script[src*="tailwind"]');
    if (tw) parts.push('<script src="' + tw.src + '"><' + "/script>");
    hostHeadCache = parts.join("");
    return hostHeadCache;
}

// Thumbnails render lazily as cards scroll into view — a long snippet
// list would otherwise spin up every iframe the moment the drawer opens.
var thumbIO = null;
function queueThumb(el, html, pad) {
    if (!thumbIO) {
        thumbIO = new IntersectionObserver(function (entries) {
            entries.forEach(function (en) {
                if (!en.isIntersecting) return;
                thumbIO.unobserve(en.target);
                var frame = document.createElement("iframe");
                frame.setAttribute("aria-hidden", "true");
                frame.setAttribute("tabindex", "-1");
                frame.srcdoc = '<!doctype html><meta charset="utf-8">' + hostHead() +
                    "<style>html,body{margin:0;background:#fff;overflow:hidden}" +
                    "body{padding:" + en.target._thumbPad + "}</style><body>" +
                    en.target._thumbHTML + "</body>";
                en.target.appendChild(frame);
            });
        }, { root: $("snip-list"), rootMargin: "200px" });
    }
    el._thumbHTML = html;
    el._thumbPad = pad || "12px 14px";
    thumbIO.observe(el);
}

// datetimeNow formats the current local time for <input type="datetime-local">.
function datetimeNow() {
    var d = new Date();
    var p = function (n) { return (n < 10 ? "0" : "") + n; };
    return d.getFullYear() + "-" + p(d.getMonth() + 1) + "-" + p(d.getDate()) +
        "T" + p(d.getHours()) + ":" + p(d.getMinutes());
}

export function openDrawer() {
    $("drawer").classList.add("on");
    $("drawer-title").textContent = state.pendingSection ? "Add a section" : "Snippets";
    $("drawer-hint").textContent = state.pendingSection
        ? "Click a starting point for the new section."
        : "Drag a snippet onto the page, or click one to insert it at the cursor.";
    // Section presets are whole-section starting points, not inline
    // blocks — the list shows them (first) only when adding a section.
    $("snip-list").classList.toggle("sections-mode", !!state.pendingSection);
    if (!snippetsLoaded) loadSnippets();
    refreshCategories();
    applyDrawerDrag();
    updateRail();
}

/* ---- category dropdown -------------------------------------------
 * Snippets may carry a group (config-only); the dropdown files cards
 * under those categories. Options are rebuilt per mode — a category
 * whose blocks are all section presets isn't offered while inserting
 * inline — and ungrouped cards (admin-created, or host config without
 * groups) pool under "Custom". With fewer than two categories in play
 * the row stays hidden and the drawer looks exactly as before. */

var lastCatMode = null;
function refreshCategories() {
    var sel = $("snip-cat");
    var sections = $("snip-list").classList.contains("sections-mode");
    if (sections !== lastCatMode) {
        lastCatMode = sections;
        sel.value = "";
        closeSearch(); // a stale query would silently empty the new mode
    }
    var groups = [];
    var hasCustom = false;
    $("snip-list").querySelectorAll(".snip").forEach(function (card) {
        // In inline mode preset cards are hidden, so their categories
        // would be empty filters — skip them.
        if (!sections && card.classList.contains("preset")) return;
        var g = card.getAttribute("data-group");
        if (!g) { hasCustom = true; return; }
        if (groups.indexOf(g) === -1) groups.push(g);
    });
    if (hasCustom) groups.push("Custom");
    $("drawer-cat").hidden = groups.length < 2;
    var current = sel.value;
    sel.innerHTML = "";
    var all = document.createElement("option");
    all.value = "";
    all.textContent = "All categories";
    sel.appendChild(all);
    groups.forEach(function (g) {
        var opt = document.createElement("option");
        opt.value = g;
        opt.textContent = g;
        sel.appendChild(opt);
    });
    sel.value = groups.indexOf(current) !== -1 ? current : "";
    applyCategoryFilter();
}

// applyCategoryFilter hides cards the category dropdown or the search
// query rules out (one class serves both), and shows a "nothing
// matches" note when the two together empty the list.
function applyCategoryFilter() {
    var v = $("snip-cat").value;
    var q = ($("snip-q").value || "").trim().toLowerCase();
    var sections = $("snip-list").classList.contains("sections-mode");
    var any = false;
    $("snip-list").querySelectorAll(".snip").forEach(function (card) {
        var g = card.getAttribute("data-group") || "Custom";
        var hide = !!v && g !== v;
        if (!hide && q) {
            var nameEl = card.querySelector(".sname");
            hide = (nameEl ? nameEl.textContent : "").toLowerCase().indexOf(q) === -1;
        }
        card.classList.toggle("cat-hide", hide);
        // Preset cards don't show in inline mode, so they can't count
        // toward "something is visible" there.
        if (!hide && (sections || !card.classList.contains("preset"))) any = true;
    });
    var none = $("snip-nomatch");
    if (!none) {
        none = document.createElement("span");
        none.id = "snip-nomatch";
        none.className = "empty";
        none.textContent = "Nothing matches.";
        $("snip-list").appendChild(none);
    }
    none.hidden = any;
}

// The search row: collapsed until the magnifier in the drawer header
// opens it; closing clears the query so nothing stays filtered by an
// invisible input.
function toggleSearch() {
    if ($("drawer-search").hidden) {
        $("drawer-search").hidden = false;
        $("drawer-search-btn").classList.add("on");
        $("snip-q").focus();
    } else {
        closeSearch();
    }
}

function closeSearch() {
    $("drawer-search").hidden = true;
    $("drawer-search-btn").classList.remove("on");
    if ($("snip-q").value !== "") {
        $("snip-q").value = "";
        applyCategoryFilter();
    }
}

// applyDrawerDrag turns dragging off for as long as a section is
// pending. A drop is handled by TinyMCE, which lands the markup at the
// drop caret in some existing rich region — it cannot create the section
// that was just asked for, and closing the drawer afterwards discards
// the pending one. So the snippet ends up somewhere the editor never
// meant to put it, with no new section anywhere. Clicking is the only
// thing that can do the job, so it is the only thing offered.
function applyDrawerDrag() {
    var allow = !state.pendingSection;
    $("snip-list").querySelectorAll("[data-cms-draggable]").forEach(function (card) {
        card.draggable = allow;
    });
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

// codeCard is the drawer's entry for a custom-code block. Clicking it
// asks which library entry to place, then inserts that entry's
// placeholder the same way any other snippet's markup is inserted.
function codeCard() {
    var card = document.createElement("div");
    card.className = "snip code";
    var nm = document.createElement("p");
    nm.className = "sname";
    nm.textContent = "Custom code";
    var tag = document.createElement("span");
    tag.className = "stag";
    tag.textContent = "Admin";
    nm.appendChild(tag);
    var desc = document.createElement("p");
    desc.className = "sdesc";
    desc.textContent = "Markup with its own JavaScript, kept in the code library.";
    card.appendChild(nm);
    card.appendChild(desc);
    // The drawer stays open behind the chooser: closing it here would
    // clear a pending "Add a section", and chooseSnippet closes it
    // anyway once something is actually placed.
    card.addEventListener("click", function () {
        chooseCodeBlock(function (html) {
            chooseSnippet({ name: "Custom code", html: html });
        });
    });
    return card;
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
        // Custom code is admin-only and has no markup of its own to
        // preview: it is a reference to a library entry, chosen when the
        // card is clicked. It leads the list rather than being filed
        // under a category, because it is a different kind of thing.
        if (isAdmin) list.appendChild(codeCard());
        body.snippets.forEach(function (sn) {
            var card = document.createElement("div");
            card.className = sn.settings ? "snip preset" : "snip";
            if (sn.group) card.setAttribute("data-group", sn.group);
            var nm = document.createElement("p");
            nm.className = "sname";
            nm.textContent = sn.name;
            if (sn.settings) {
                var tag = document.createElement("span");
                tag.className = "stag";
                tag.textContent = "Section";
                nm.appendChild(tag);
            }
            var thumb = document.createElement("div");
            thumb.className = sn.settings ? "sthumb sect" : "sthumb";
            if (sn.settings) queueThumb(thumb, presetSectionHTML(sn.html, sn.settings), "0");
            else queueThumb(thumb, sn.html);
            var desc = document.createElement("p");
            desc.className = "sdesc";
            var probe = document.createElement("div");
            probe.innerHTML = sn.html;
            desc.textContent = (probe.textContent || "").trim().replace(/\s+/g, " ").slice(0, 90);
            card.appendChild(nm);
            card.appendChild(thumb);
            card.appendChild(desc);
            // Drag: TinyMCE accepts text/html drops and inserts the
            // markup at the drop caret inside any rich region. Presets
            // aren't draggable — their settings only mean something on
            // a section wrapper, which a drop can't create. The marker
            // says this card may drag at all; applyDrawerDrag decides
            // whether it does right now.
            if (!sn.settings) card.setAttribute("data-cms-draggable", "");
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
        // The list arrives after the drawer opened, so the cards learn
        // here whether dragging is on for the mode they arrived into,
        // and the category dropdown learns what groups exist.
        applyDrawerDrag();
        refreshCategories();
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
//
// Unless it landed in a column, which is the one place inside a block
// that content is *meant* to go: putting the caret in a column and
// inserting a button is how a button gets into that column, and lifting
// it out to sit under the whole row — full width, in nobody's column —
// is the opposite of what was asked. So a block in a cell stays where
// it was put, and stays a block: its own outline, its own move,
// duplicate, restyle and delete, all acting inside the column, exactly
// as they would outside one. A cell is the one container that holds
// blocks rather than being made of them.
//
// The guard counts passes that actually moved something. Cell nesting
// is permanent and always matches the query, so a loop that counted
// query results instead would spin its full four passes on every call
// for pages that have a column on them.
export function unnestSnippets() {
    for (var guard = 0; guard < 4; guard++) {
        var nested = document.querySelectorAll(
            "[data-cms-region] .cms-snippet .cms-snippet," +
            "[data-cms-sections] .cms-snippet .cms-snippet");
        if (!nested.length) return;
        var lifted = false;
        nested.forEach(function (inner) {
            var anc = inner.parentElement && inner.parentElement.closest(".cms-snippet");
            if (!anc) return;
            if (cellFor(inner)) return;
            anc.insertAdjacentElement("afterend", inner);
            lifted = true;
        });
        if (!lifted) return;
    }
}

function chooseSnippet(sn) {
    if (state.pendingSection) {
        var target = state.pendingSection;
        closeDrawer();
        createSection(target, sn.html, sn.settings);
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
        // The last sections area is the page's main content — a template
        // with more than one puts its banner first and its content last,
        // and "add a section" from the rail means the content. Adding to
        // a banner is done from the banner's own button, which knows to
        // stop at one.
        var areas = document.querySelectorAll("[data-cms-sections]");
        if (!areas.length) {
            setMsg("This page has no sections area.");
            return;
        }
        var container = areas[areas.length - 1];
        state.pendingSection = { region: container.getAttribute("data-cms-sections"), after: null };
        openDrawer();
    });
    $("rail-page").hidden = pageTemplates.length === 0 || !canPages;
    $("rail-page").addEventListener("click", newPageDialog);

    $("rail-post").hidden = !postsEnabled || (!canBlogs && !canNews);
    $("rail-post").addEventListener("click", function () { newPostDialog(canBlogs ? "blog" : "news"); });

    $("drawer-close").addEventListener("click", closeDrawer);
    $("snip-cat").addEventListener("change", applyCategoryFilter);
    $("drawer-search-btn").addEventListener("click", toggleSearch);
    $("snip-q").addEventListener("input", applyCategoryFilter);
    $("snip-q").addEventListener("keydown", function (e) {
        if (e.key === "Escape") {
            e.stopPropagation(); // just close the search, not the drawer
            closeSearch();
        }
    });
}

// newPageDialog collects a name and template for a new page, creates it,
// and navigates into the draft. Shared by the tool rail and the admin
// tools menu.
export function newPageDialog() {
    if (!pageTemplates.length || !canPages) return;
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
}

// newPostDialog collects the details for a new blog or news post with the
// "Publish under" select preset to feed, creates it, and navigates into
// the draft. Shared by the tool rail and the admin tools menu.
export function newPostDialog(feed) {
    if (!postsEnabled || (!canBlogs && !canNews)) return;
    // Only permitted feeds are offered, and the preset falls back to a
    // permitted one; the server refuses the other feed regardless.
    var feedOptions = [];
    if (canBlogs) feedOptions.push({ value: "blog", label: "Blog" });
    if (canNews) feedOptions.push({ value: "news", label: "News" });
    if ((feed === "news" && !canNews) || (feed !== "news" && !canBlogs)) {
        feed = feedOptions[0].value;
    }
    var fields = [
        {
            id: "feed",
            label: "Publish under",
            type: "select",
            value: feed === "news" ? "news" : "blog",
            options: feedOptions,
        },
        { id: "summary", label: "Summary", type: "text",
            placeholder: "Shown in listings and feeds (optional)" },
        { id: "date", label: "Date", type: "datetime", value: datetimeNow() },
    ];
    if (mediaEnabled) {
        // One image serves both slots: it becomes the background of the
        // post's banner section and its card in the listings. Leaving it
        // empty means no banner, and the header region's own "Add
        // section" button is how one gets added later.
        fields.push({ id: "image", label: "Header & listing image", type: "image", value: "" });
    }
    openDialog({
        message: "Create a new post",
        prompt: true,
        placeholder: "Post title",
        required: "Give the post a title.",
        okLabel: "Create post",
        fields: fields,
    }).then(function (values) {
        if (!values || !values.input) return;
        setMsg("Creating post…");
        api("/posts", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
                title: values.input,
                feed: values.feed,
                summary: values.summary || "",
                published_at: values.date || "",
                thumbnail_media_id: values.image_id || 0,
            }),
        }).then(function (body) {
            // The new post is a draft, so only editors see it —
            // navigate straight into it to start writing.
            window.location.href = body.url;
        }).catch(function (err) { setMsg(err.message); });
    });
}
