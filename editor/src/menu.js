/* ------------------------------------------------------------------ *
 * Menu panel — edits navigation items (data only; the template owns
 * the nav markup, so saving reloads the page to re-render it)
 * ------------------------------------------------------------------ */

import { state } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg } from "./util.js";
import { closeDrawer, updateRail } from "./snippets.js";

var menuData = null; // [{label, pageId, url, newTab}]
var menuPages = null; // [{id, title, slug, status}]

function openMenuPanel() {
    closeDrawer();
    $("menu-drawer").classList.add("on");
    if (!menuData) loadMenu();
    updateRail();
}
export function closeMenuPanel() {
    $("menu-drawer").classList.remove("on");
    updateRail();
}

function loadMenu() {
    $("menu-list").innerHTML = '<span class="empty">Loading…</span>';
    Promise.all([
        api("/menu?menu=main", { method: "GET" }),
        api("/pages", { method: "GET" }),
    ]).then(function (results) {
        menuData = results[0].items || [];
        menuPages = results[1].pages || [];
        renderMenuRows();
    }).catch(function (err) {
        $("menu-list").innerHTML = "";
        var span = document.createElement("span");
        span.className = "empty";
        span.textContent = err.message;
        $("menu-list").appendChild(span);
    });
}

function menuError(msg) {
    var el = $("menu-err");
    el.textContent = msg || "";
    el.hidden = !msg;
}

function renderMenuRows() {
    var list = $("menu-list");
    list.innerHTML = "";
    menuError("");
    if (menuData.length === 0) {
        list.innerHTML = '<span class="empty">No menu items yet — add your first one below.</span>';
    }
    menuData.forEach(function (item, i) {
        var row = document.createElement("div");
        row.className = "mrow";

        var tools = document.createElement("div");
        tools.className = "mtools";
        [["↑", "Move up"], ["↓", "Move down"], ["✕", "Remove"]].forEach(function (b, ti) {
            var btn = document.createElement("button");
            btn.type = "button";
            btn.textContent = b[0];
            btn.title = b[1];
            btn.addEventListener("click", function () {
                if (ti === 0 && i > 0) {
                    menuData.splice(i - 1, 0, menuData.splice(i, 1)[0]);
                } else if (ti === 1 && i < menuData.length - 1) {
                    menuData.splice(i + 1, 0, menuData.splice(i, 1)[0]);
                } else if (ti === 2) {
                    menuData.splice(i, 1);
                } else {
                    return;
                }
                renderMenuRows();
            });
            tools.appendChild(btn);
        });
        row.appendChild(tools);

        var label = document.createElement("input");
        label.type = "text";
        label.placeholder = "Label";
        label.value = item.label;
        label.addEventListener("input", function () { item.label = label.value; });
        row.appendChild(label);

        var sel = document.createElement("select");
        menuPages.forEach(function (p) {
            var opt = document.createElement("option");
            opt.value = String(p.id);
            opt.textContent = (p.title || "(untitled)") + (p.status === "published" ? "" : " (draft)");
            if (item.pageId === p.id) opt.selected = true;
            sel.appendChild(opt);
        });
        var custom = document.createElement("option");
        custom.value = "custom";
        custom.textContent = "Custom address…";
        if (!item.pageId) custom.selected = true;
        sel.appendChild(custom);
        sel.addEventListener("change", function () {
            if (sel.value === "custom") {
                item.pageId = 0;
            } else {
                item.pageId = parseInt(sel.value, 10);
                item.url = "";
            }
            renderMenuRows();
        });
        row.appendChild(sel);

        if (!item.pageId) {
            var url = document.createElement("input");
            url.type = "text";
            url.placeholder = "https://example.com or /contact";
            url.value = item.url || "";
            url.addEventListener("input", function () { item.url = url.value; });
            row.appendChild(url);

            var chk = document.createElement("label");
            chk.className = "mchk";
            var cb = document.createElement("input");
            cb.type = "checkbox";
            cb.checked = !!item.newTab;
            cb.addEventListener("change", function () { item.newTab = cb.checked; });
            chk.appendChild(cb);
            chk.appendChild(document.createTextNode("Open in a new tab"));
            row.appendChild(chk);
        }

        list.appendChild(row);
    });
}

export function initMenu() {
    $("rail-menu").addEventListener("click", function () {
        if ($("menu-drawer").classList.contains("on")) closeMenuPanel();
        else openMenuPanel();
    });
    $("menu-close").addEventListener("click", closeMenuPanel);

    $("menu-add").addEventListener("click", function () {
        if (!menuData) return;
        var first = menuPages && menuPages.length ? menuPages[0].id : 0;
        menuData.push({ label: "", pageId: first, url: "", newTab: false });
        renderMenuRows();
        var inputs = $("menu-list").querySelectorAll(".mrow input[type=text]");
        if (inputs.length) inputs[inputs.length - (first ? 1 : 2)].focus();
    });

    $("menu-save").addEventListener("click", function () {
        if (!menuData) return;
        for (var i = 0; i < menuData.length; i++) {
            if (!menuData[i].label.trim()) {
                menuError("Every menu item needs a label.");
                return;
            }
            if (!menuData[i].pageId && !(menuData[i].url || "").trim()) {
                menuError("Custom links need a web address.");
                return;
            }
        }
        menuError("");
        setMsg("Saving menu…");
        api("/menu", {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ menu: "main", items: menuData }),
        }).then(function () {
            // The template renders the nav, so reload to show the result.
            state.dirty = {};
            state.sectionsDirty = {};
            window.location.reload();
        }).catch(function (err) { menuError(err.message); setMsg(""); });
    });
}
