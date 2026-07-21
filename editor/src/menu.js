/* ------------------------------------------------------------------ *
 * Menu editing — in place, on navs rendered by {{cmsNav "key"}}.
 *
 * While editing, menu items are right-clickable (long-press on touch)
 * to open a settings modal, "＋" chips add items, and items drag to
 * rearrange — including into or out of a dropdown (one level only,
 * and only with a mouse; touch gets the modal, not drag). Menus have
 * no draft state: every change saves immediately and the nav is
 * re-rendered client-side from the edited data. The markup built here
 * mirrors navHTML in render/render.go — keep the two in sync.
 *
 * Items are addressed by position, not id: a path [i] or [i, j] of
 * indexes into the menu tree, recomputed from the DOM on every
 * interaction (ReplaceMenu reassigns ids on each save, so positions
 * are the stable handle).
 * ------------------------------------------------------------------ */

import { state } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { cmsConfirm } from "./dialogs.js";

var menus = null; // menu key -> [{label,pageId,url,newTab,dropdown,children}]
var pages = null; // [{id, title, slug, status}]
var loadPromise = null;

function navEls() {
    return Array.prototype.slice.call(document.querySelectorAll("nav[data-cms-menu]"));
}

function normalize(items) {
    return (items || []).map(function (it) {
        return {
            label: it.label || "",
            pageId: it.pageId || 0,
            url: it.url || "",
            newTab: !!it.newTab,
            dropdown: !!it.dropdown,
            children: normalize(it.children),
        };
    });
}

function loadData() {
    if (loadPromise) return loadPromise;
    var keys = [];
    navEls().forEach(function (nav) {
        var k = nav.getAttribute("data-cms-menu");
        if (k && keys.indexOf(k) === -1) keys.push(k);
    });
    loadPromise = Promise.all([
        api("/pages", { method: "GET" }),
        Promise.all(keys.map(function (k) {
            return api("/menu?menu=" + encodeURIComponent(k), { method: "GET" });
        })),
    ]).then(function (results) {
        pages = results[0].pages || [];
        menus = {};
        results[1].forEach(function (body) { menus[body.menu] = normalize(body.items); });
    }).catch(function (err) {
        loadPromise = null; // allow a retry on the next interaction
        throw err;
    });
    return loadPromise;
}

function whenLoaded(fn) {
    loadData().then(fn).catch(function (err) { flash(err.message); });
}

/* ---- addressing rendered items ---- */

function menuKeyOf(el) {
    var nav = el.closest("nav[data-cms-menu]");
    return nav ? nav.getAttribute("data-cms-menu") : null;
}

// itemIndex counts the li's position among its .cms-nav-item siblings
// (skipping the "＋" chip and any host-inserted extras).
function itemIndex(li) {
    var n = 0, el = li;
    while ((el = el.previousElementSibling)) {
        if (el.classList.contains("cms-nav-item")) n++;
    }
    return n;
}

function pathOf(li) {
    var idx = itemIndex(li);
    if (li.parentElement.classList.contains("cms-nav-sub")) {
        return [itemIndex(li.parentElement.closest("li")), idx];
    }
    return [idx];
}

function itemAt(key, path) {
    var it = (menus[key] || [])[path[0]];
    if (it && path.length === 2) return (it.children || [])[path[1]];
    return it;
}

function removeAt(key, path) {
    if (path.length === 1) menus[key].splice(path[0], 1);
    else menus[key][path[0]].children.splice(path[1], 1);
}

function pageById(id) {
    for (var i = 0; i < (pages || []).length; i++) {
        if (pages[i].id === id) return pages[i];
    }
    return null;
}

function itemURL(item) {
    if (item.pageId) {
        var p = pageById(item.pageId);
        return p ? "/" + p.slug : null;
    }
    return item.url || null;
}

/* ---- rendering (mirrors navHTML in render/render.go) ---- */

function itemLI(item, top) {
    var li = document.createElement("li");
    li.className = "cms-nav-item";
    if (item.dropdown && top) {
        li.className += " cms-nav-drop";
        var btn = document.createElement("button");
        btn.type = "button";
        btn.className = "cms-nav-link cms-nav-toggle";
        btn.setAttribute("aria-expanded", "false");
        btn.setAttribute("aria-haspopup", "true");
        btn.textContent = item.label;
        var caret = document.createElement("span");
        caret.className = "cms-nav-caret";
        caret.setAttribute("aria-hidden", "true");
        btn.appendChild(caret);
        li.appendChild(btn);
        var sub = document.createElement("ul");
        sub.className = "cms-nav-sub";
        item.children.forEach(function (c) { sub.appendChild(itemLI(c, false)); });
        if (state.editing) sub.appendChild(addChip());
        li.appendChild(sub);
        return li;
    }
    var a = document.createElement("a");
    a.className = "cms-nav-link";
    var url = itemURL(item);
    a.href = url || "#";
    if (state.editing) a.draggable = false; // our pointer drag, not the native link drag
    if (url && url.indexOf("http") !== 0 && url === window.location.pathname) {
        a.className += " cms-active";
        a.setAttribute("aria-current", "page");
    }
    if (item.newTab) {
        a.target = "_blank";
        a.rel = "noopener";
    }
    a.textContent = item.label;
    li.appendChild(a);
    return li;
}

function addChip() {
    var li = document.createElement("li");
    li.className = "cms-nav-addli";
    var b = document.createElement("button");
    b.type = "button";
    b.title = "Add a menu item";
    b.textContent = "＋";
    b.addEventListener("click", function (e) {
        e.preventDefault();
        e.stopPropagation(); // keep the document click handler from closing the dropdown
        var key = menuKeyOf(li);
        var sub = li.parentElement.classList.contains("cms-nav-sub") ? li.parentElement : null;
        openModal(key, null, sub ? [itemIndex(sub.closest("li"))] : null);
    });
    li.appendChild(b);
    return li;
}

function renderNav(nav) {
    var key = nav.getAttribute("data-cms-menu");
    if (!menus || menus[key] === undefined) return;
    // Keep dropdowns open across the re-render — adding several items
    // to one dropdown shouldn't snap it shut every time.
    var openIdx = [];
    nav.querySelectorAll("li.cms-nav-drop.cms-open").forEach(function (li) {
        openIdx.push(itemIndex(li));
    });
    var ul = document.createElement("ul");
    ul.className = "cms-nav-list";
    menus[key].forEach(function (item) { ul.appendChild(itemLI(item, true)); });
    if (state.editing) ul.appendChild(addChip());
    nav.innerHTML = "";
    nav.appendChild(ul);
    openIdx.forEach(function (i) {
        var li = ul.children[i];
        if (li && li.classList.contains("cms-nav-drop")) openDrop(li);
    });
}

function renderAll() {
    navEls().forEach(renderNav);
}

function saveMenu(key) {
    setMsg("Saving menu…");
    api("/menu", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ menu: key, items: menus[key] }),
    }).then(function () {
        renderAll();
        flash("Menu updated");
    }).catch(function (err) {
        flash(err.message);
        // Re-fetch so the local tree matches what is actually stored.
        loadPromise = null;
        whenLoaded(renderAll);
    });
}

/* ---- the item modal ---- */

var modal = null; // {key, path|null, parentPath|null, item} while open
var comboPageId = 0;
var confirmDropLoss = false; // second-OK guard when a dropdown's items would go

export function isMenuModalOpen() {
    return !!modal;
}

export function closeMenuModal() {
    modal = null;
    $("mm-overlay").classList.remove("on");
    $("mm").classList.remove("on");
}

function mmError(msg) {
    $("mm-err").textContent = msg || "";
    $("mm-err").hidden = !msg;
}

function currentKind() {
    if ($("mm-kind-drop").checked) return "dropdown";
    if ($("mm-kind-url").checked) return "url";
    return "page";
}

function setKind(kind) {
    $("mm-kind-page").checked = kind === "page";
    $("mm-kind-url").checked = kind === "url";
    $("mm-kind-drop").checked = kind === "dropdown";
    $("mm-page-fld").hidden = kind !== "page";
    $("mm-url-fld").hidden = kind !== "url";
    $("mm-tab-fld").hidden = kind === "dropdown";
}

function comboSet(pageId) {
    comboPageId = pageId || 0;
    var p = pageById(comboPageId);
    $("mm-page").value = p ? (p.title || "(untitled)") : "";
    $("mm-page-list").hidden = true;
}

function comboFilter() {
    var q = $("mm-page").value.trim().toLowerCase();
    var list = $("mm-page-list");
    list.innerHTML = "";
    var shown = 0;
    (pages || []).forEach(function (p) {
        if (shown >= 30) return;
        var title = p.title || "(untitled)";
        if (q && title.toLowerCase().indexOf(q) === -1 && p.slug.indexOf(q) === -1) return;
        shown++;
        var b = document.createElement("button");
        b.type = "button";
        b.textContent = title + (p.status === "published" ? "" : " (draft)");
        b.addEventListener("click", function () { comboSet(p.id); });
        list.appendChild(b);
    });
    if (!shown) {
        var none = document.createElement("div");
        none.className = "none";
        none.textContent = "No matching pages.";
        list.appendChild(none);
    }
    list.hidden = false;
}

// openModal edits the item at path, or collects a new one appended at
// the top level (parentPath null) / inside that dropdown (parentPath).
function openModal(key, path, parentPath) {
    var item = path ? itemAt(key, path) : {
        label: "", pageId: 0, url: "", newTab: false, dropdown: false, children: [],
    };
    if (!item) return;
    modal = { key: key, path: path, parentPath: parentPath || null, item: item };
    confirmDropLoss = false;
    $("mm-title").textContent = path ? "Menu item" : "New menu item";
    $("mm-label").value = item.label;
    setKind(item.dropdown ? "dropdown" : (item.url && !item.pageId ? "url" : "page"));
    comboSet(item.pageId);
    $("mm-url").value = item.url;
    $("mm-newtab").checked = item.newTab;
    // Dropdowns go one level deep, so the choice only exists at the top.
    $("mm-kind-drop-row").hidden = path ? path.length === 2 : !!parentPath;
    $("mm-remove").hidden = !path;
    mmError("");
    $("mm-overlay").classList.add("on");
    $("mm").classList.add("on");
    $("mm-label").focus();
}

function modalOK() {
    var m = modal;
    if (!m) return;
    var label = $("mm-label").value.trim();
    if (!label) {
        mmError("Give the item some menu text.");
        return;
    }
    var kind = currentKind();
    var url = $("mm-url").value.trim();
    if (kind === "page" && !comboPageId) {
        mmError("Choose the page the item links to.");
        return;
    }
    if (kind === "url" && !/^(\/|https?:\/\/|mailto:|tel:)/.test(url)) {
        mmError("Enter a web address like https://… or a path like /contact.");
        return;
    }
    var it = m.item;
    if (it.dropdown && kind !== "dropdown" && it.children.length && !confirmDropLoss) {
        confirmDropLoss = true;
        mmError("This dropdown holds " + it.children.length +
            (it.children.length === 1 ? " item" : " items") +
            ", which will be removed with it — press OK again to continue.");
        return;
    }
    it.label = label;
    it.dropdown = kind === "dropdown";
    it.pageId = kind === "page" ? comboPageId : 0;
    it.url = kind === "url" ? url : "";
    it.newTab = kind !== "dropdown" && $("mm-newtab").checked;
    if (kind !== "dropdown") it.children = [];
    if (!m.path) {
        if (m.parentPath) itemAt(m.key, m.parentPath).children.push(it);
        else menus[m.key].push(it);
    }
    closeMenuModal();
    saveMenu(m.key);
}

function modalRemove() {
    var m = modal;
    if (!m || !m.path) return;
    var it = m.item;
    closeMenuModal();
    var extra = it.dropdown && it.children.length
        ? " and the " + it.children.length + (it.children.length === 1 ? " item" : " items") + " inside it"
        : "";
    cmsConfirm('Remove "' + it.label + '"' + extra + " from the menu?", "Remove", true)
        .then(function (yes) {
            if (!yes) return;
            removeAt(m.key, m.path);
            saveMenu(m.key);
        });
}

/* ---- drag to rearrange (mouse only; touch edits via long-press) ---- */

var drag = null; // {key, li, startX, startY, active, path, drop}
var indEl = null;

function indicator() {
    if (!indEl) {
        indEl = document.createElement("div");
        indEl.id = "cms-nav-ind";
        document.body.appendChild(indEl);
    }
    return indEl;
}

function hideIndicator() {
    if (indEl) indEl.style.display = "none";
}

function listItems(list) {
    var out = [];
    for (var i = 0; i < list.children.length; i++) {
        var c = list.children[i];
        if (c.classList.contains("cms-nav-item") && c !== drag.li) out.push(c);
    }
    return out;
}

// insertIndex finds where the pointer falls in the list: the count of
// items (ignoring the dragged one) whose midpoint the pointer passed.
// The top-level list flows horizontally, sub-lists vertically.
function insertIndex(list, e, vertical) {
    var idx = 0;
    listItems(list).forEach(function (c) {
        var r = c.getBoundingClientRect();
        var mid = vertical ? r.top + r.height / 2 : r.left + r.width / 2;
        if ((vertical ? e.clientY : e.clientX) > mid) idx++;
    });
    return idx;
}

function showLineIndicator(list, idx, vertical) {
    var items = listItems(list);
    var el = indicator();
    var lr = list.getBoundingClientRect();
    if (vertical) {
        var y = idx < items.length
            ? items[idx].getBoundingClientRect().top - 2
            : (items.length ? items[items.length - 1].getBoundingClientRect().bottom : lr.top) + 1;
        el.style.cssText = "display:block;left:" + (lr.left + 6) + "px;top:" + y + "px;width:" +
            Math.max(lr.width - 12, 24) + "px;height:3px";
    } else {
        var x = idx < items.length
            ? items[idx].getBoundingClientRect().left - 4
            : (items.length ? items[items.length - 1].getBoundingClientRect().right : lr.left) + 2;
        var ref = (items[0] || list).getBoundingClientRect();
        el.style.cssText = "display:block;left:" + x + "px;top:" + ref.top + "px;width:3px;height:" + ref.height + "px";
    }
}

function showNestIndicator(li) {
    var r = li.getBoundingClientRect();
    indicator().style.cssText = "display:block;left:" + (r.left - 3) + "px;top:" + (r.top - 3) + "px;width:" +
        (r.width + 6) + "px;height:" + (r.height + 6) + "px;" +
        "background:rgba(47,95,224,.14);border:2px solid #2f5fe0;border-radius:6px";
}

function openDrop(li) {
    li.classList.add("cms-open");
    var b = li.querySelector(".cms-nav-toggle");
    if (b) b.setAttribute("aria-expanded", "true");
}

// updateDrop decides, from the pointer position, where a drop would
// land: {parentItem: null, index} for the top level, {parentItem,
// index} inside a dropdown. parentItem is the model object, not an
// index — the dragged item's removal may shift indexes.
function updateDrop(e) {
    var d = drag;
    d.drop = null;
    var el = document.elementFromPoint(e.clientX, e.clientY);
    var nav = el && el.closest ? el.closest("nav[data-cms-menu]") : null;
    if (!nav || nav.getAttribute("data-cms-menu") !== d.key) {
        hideIndicator();
        return;
    }
    var dragged = itemAt(d.key, d.path);
    var isParent = d.path.length === 1 && dragged && dragged.dropdown;
    var sub = el.closest(".cms-nav-sub");
    if (sub) {
        if (isParent) { // one level only: a dropdown can't hold a dropdown
            hideIndicator();
            return;
        }
        var idx = insertIndex(sub, e, true);
        d.drop = { parentItem: itemAt(d.key, [itemIndex(sub.closest("li"))]), index: idx };
        showLineIndicator(sub, idx, true);
        return;
    }
    var overDrop = el.closest("li.cms-nav-drop");
    if (overDrop && overDrop !== d.li && !isParent) {
        // The middle of a dropdown parent nests the item at the end (and
        // opens the dropdown for precise placement); the edges reorder
        // around it like any other item.
        var r = overDrop.getBoundingClientRect();
        if (e.clientX > r.left + r.width * 0.25 && e.clientX < r.right - r.width * 0.25) {
            openDrop(overDrop);
            var parentItem = itemAt(d.key, [itemIndex(overDrop)]);
            d.drop = { parentItem: parentItem, index: parentItem.children.length };
            showNestIndicator(overDrop);
            return;
        }
    }
    var list = nav.querySelector(".cms-nav-list");
    if (!list) {
        hideIndicator();
        return;
    }
    var tIdx = insertIndex(list, e, false);
    d.drop = { parentItem: null, index: tIdx };
    showLineIndicator(list, tIdx, false);
}

function applyDrop(d) {
    var item = itemAt(d.key, d.path);
    if (!item) return;
    var before = JSON.stringify(menus[d.key]);
    removeAt(d.key, d.path);
    if (d.drop.parentItem) d.drop.parentItem.children.splice(d.drop.index, 0, item);
    else menus[d.key].splice(d.drop.index, 0, item);
    if (JSON.stringify(menus[d.key]) === before) {
        renderAll(); // dropped where it started; nothing to save
        return;
    }
    saveMenu(d.key);
}

/* ---- edit-mode wiring ---- */

// setMenuEditing re-renders every cmsNav nav for the new mode: with
// "＋" chips and drag affordances while editing, plain when done.
export function setMenuEditing(on) {
    if (on) {
        if (navEls().length) whenLoaded(renderAll);
        return;
    }
    if (modal) closeMenuModal();
    if (menus) renderAll();
}

var pressTimer = null;

export function initMenu() {
    // Right-click while editing: the item's settings, or "add an item"
    // from the nav's empty parts. Capture, so host handlers can't
    // swallow it.
    document.addEventListener("contextmenu", function (e) {
        if (!state.editing || !e.target.closest) return;
        var nav = e.target.closest("nav[data-cms-menu]");
        if (!nav) return;
        e.preventDefault();
        e.stopPropagation();
        if (modal) return;
        var li = e.target.closest("li.cms-nav-item");
        whenLoaded(function () {
            openModal(nav.getAttribute("data-cms-menu"), li ? pathOf(li) : null, null);
        });
    }, true);

    // While editing, menu links must not navigate away — clicking one
    // explains the right-click flow instead. Dropdown toggles keep
    // working so items inside stay reachable.
    document.addEventListener("click", function (e) {
        if (!state.editing || !e.target.closest) return;
        if (e.target.closest("nav[data-cms-menu] a.cms-nav-link")) {
            e.preventDefault();
            e.stopPropagation();
            flash("Right-click a menu item to edit it; drag to rearrange");
        }
    }, true);

    // Native link dragging would swallow our pointer-based drag.
    document.addEventListener("dragstart", function (e) {
        if (state.editing && e.target.closest && e.target.closest("nav[data-cms-menu]")) {
            e.preventDefault();
        }
    }, true);

    // Long-press opens the same modal on touch (no right-click there).
    document.addEventListener("pointerdown", function (e) {
        if (!state.editing || e.pointerType === "mouse" || !e.target.closest) return;
        var li = e.target.closest("nav[data-cms-menu] li.cms-nav-item");
        if (!li) return;
        var sx = e.clientX, sy = e.clientY;
        var cancel = function () {
            clearTimeout(pressTimer);
            document.removeEventListener("pointerup", cancel, true);
            document.removeEventListener("pointercancel", cancel, true);
            document.removeEventListener("pointermove", onMove, true);
        };
        var onMove = function (me) {
            if (Math.abs(me.clientX - sx) + Math.abs(me.clientY - sy) > 10) cancel();
        };
        pressTimer = setTimeout(function () {
            cancel();
            whenLoaded(function () { openModal(menuKeyOf(li), pathOf(li), null); });
        }, 550);
        document.addEventListener("pointerup", cancel, true);
        document.addEventListener("pointercancel", cancel, true);
        document.addEventListener("pointermove", onMove, true);
    }, true);

    // Mouse drag to rearrange.
    document.addEventListener("pointerdown", function (e) {
        if (!state.editing || e.pointerType !== "mouse" || e.button !== 0 || !e.target.closest) return;
        var li = e.target.closest("nav[data-cms-menu] li.cms-nav-item");
        if (!li || !menus) return;
        drag = { key: menuKeyOf(li), li: li, startX: e.clientX, startY: e.clientY, active: false };
    }, true);
    document.addEventListener("pointermove", function (e) {
        if (!drag) return;
        if (!drag.active) {
            if (Math.abs(e.clientX - drag.startX) + Math.abs(e.clientY - drag.startY) < 8) return;
            drag.active = true;
            drag.path = pathOf(drag.li);
            drag.li.classList.add("cms-nav-dragli");
            document.body.classList.add("cms-nav-dragging");
            indicator().style.display = "block";
        }
        e.preventDefault(); // no text selection while dragging
        updateDrop(e);
    }, true);
    document.addEventListener("pointerup", function () {
        if (!drag) return;
        var d = drag;
        drag = null;
        if (!d.active) return;
        d.li.classList.remove("cms-nav-dragli");
        document.body.classList.remove("cms-nav-dragging");
        hideIndicator();
        if (d.drop) applyDrop(d);
    }, true);

    $("mm-ok").addEventListener("click", modalOK);
    $("mm-cancel").addEventListener("click", closeMenuModal);
    $("mm-overlay").addEventListener("click", closeMenuModal);
    $("mm-remove").addEventListener("click", modalRemove);
    $("mm").addEventListener("keydown", function (e) {
        if (e.key === "Enter" && e.target.id !== "mm-page") {
            e.preventDefault();
            modalOK();
        }
    });
    ["mm-kind-page", "mm-kind-url", "mm-kind-drop"].forEach(function (id) {
        $(id).addEventListener("change", function () { setKind(currentKind()); });
    });
    $("mm-page").addEventListener("focus", comboFilter);
    $("mm-page").addEventListener("input", function () {
        comboPageId = 0;
        comboFilter();
    });
}
