/* ------------------------------------------------------------------ *
 * Media picker
 * ------------------------------------------------------------------ */

import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { cmsPrompt, isDialogOpen, dialogDismiss } from "./dialogs.js";
import { dismissCodePanel } from "./pagecode.js";
import { closeMore } from "./saving.js";
import { closeDrawer } from "./snippets.js";
import { closeMenuPanel } from "./menu.js";

var DOC_ACCEPT = ".pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.odt,.ods,.odp,.txt,.csv,.zip";

var pickerHandler = null; // function(mediaItem) while the picker is open
var pickerKind = "image"; // "image" or "file" while the picker is open
var pickerFolder = ""; // "" = all files, "root" = unfiled, or a folder id
var pickerQuery = "";
var pickerView = "grid";
try {
    pickerView = window.localStorage.getItem("cms-media-view") === "list" ? "list" : "grid";
} catch (e) { /* private mode */ }

export function openPicker(kind, handler) {
    pickerKind = kind;
    pickerHandler = handler;
    pickerFolder = "";
    pickerQuery = "";
    $("search").value = "";
    $("picker-title").textContent = kind === "file" ? "Choose a document" : "Choose an image";
    $("file").setAttribute("accept", kind === "file" ? DOC_ACCEPT : "image/*");
    $("overlay").classList.add("on");
    $("picker").classList.add("on");
    applyView();
    loadFolders();
    loadMedia();
}
export function closePicker() {
    $("picker").classList.remove("on");
    $("overlay").classList.remove("on");
    pickerHandler = null;
}

function applyView() {
    $("grid").className = "items " + pickerView;
    $("view-grid").classList.toggle("on", pickerView === "grid");
    $("view-list").classList.toggle("on", pickerView === "list");
}
function setView(v) {
    pickerView = v;
    try { window.localStorage.setItem("cms-media-view", v); } catch (e) { /* ignore */ }
    applyView();
    renderItems(lastItems);
}

function loadFolders() {
    api("/media/folders", { method: "GET" }).then(function (body) {
        renderFolders(body.folders || []);
    }).catch(function () {
        renderFolders([]);
    });
}

function renderFolders(folders) {
    var side = $("folders");
    side.innerHTML = "";
    var add = function (label, value, count) {
        var btn = document.createElement("button");
        btn.textContent = label;
        if (count !== undefined) {
            var n = document.createElement("span");
            n.className = "n";
            n.textContent = count;
            btn.appendChild(n);
        }
        btn.classList.toggle("on", pickerFolder === value);
        btn.addEventListener("click", function () {
            pickerFolder = value;
            renderFolders(folders);
            loadMedia();
        });
        side.appendChild(btn);
        return btn;
    };
    add("All files", "");
    add("Unfiled", "root");
    folders.forEach(function (f) { add(f.name, String(f.id), f.count); });

    var newf = document.createElement("button");
    newf.className = "newf";
    newf.textContent = "＋ New folder";
    newf.addEventListener("click", function () {
        cmsPrompt("Name the new folder", "e.g. Hero images", "Create folder").then(function (name) {
            if (!name) return;
            api("/media/folders", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ name: name }),
            }).then(function (body) {
                pickerFolder = String(body.folder.id);
                loadFolders();
                loadMedia();
            }).catch(function (err) { setMsg(err.message); });
        });
    });
    side.appendChild(newf);
}

var lastItems = [];

function loadMedia() {
    var grid = $("grid");
    grid.innerHTML = '<span class="empty">Loading…</span>';
    var params = "?kind=" + pickerKind +
        (pickerQuery ? "&q=" + encodeURIComponent(pickerQuery) : "") +
        (pickerFolder ? "&folder=" + encodeURIComponent(pickerFolder) : "");
    api("/media" + params, { method: "GET" }).then(function (body) {
        lastItems = body.media || [];
        renderItems(lastItems);
    }).catch(function (err) {
        grid.innerHTML = "";
        var span = document.createElement("span");
        span.className = "empty";
        span.textContent = err.message;
        grid.appendChild(span);
    });
}

function docBadge(item, forList) {
    var doc = document.createElement("div");
    doc.className = "doc";
    doc.textContent = "📄";
    if (!forList) {
        var ext = document.createElement("span");
        ext.textContent = item.filename.split(".").pop();
        doc.appendChild(ext);
    }
    return doc;
}

function renderItems(items) {
    var grid = $("grid");
    grid.innerHTML = "";
    if (!items || items.length === 0) {
        grid.innerHTML = '<span class="empty">Nothing here — upload a file above, or pick another folder.</span>';
        return;
    }
    items.forEach(function (item) {
        var el;
        if (pickerView === "list") {
            el = document.createElement("div");
            el.className = "row";
            if (item.kind === "file") {
                el.appendChild(docBadge(item, true));
            } else {
                var img = document.createElement("img");
                img.src = item.thumb;
                img.alt = item.alt || "";
                el.appendChild(img);
            }
            var nm = document.createElement("span");
            nm.className = "nm";
            nm.textContent = item.filename;
            var sz = document.createElement("span");
            sz.className = "sz";
            sz.textContent = item.kind === "file" ? item.size : item.width + "×" + item.height;
            el.appendChild(nm);
            el.appendChild(sz);
        } else {
            el = document.createElement("figure");
            if (item.kind === "file") {
                el.appendChild(docBadge(item, false));
            } else {
                var gimg = document.createElement("img");
                gimg.src = item.thumb;
                gimg.alt = item.alt || "";
                el.appendChild(gimg);
            }
            var cap = document.createElement("figcaption");
            cap.textContent = item.filename + (item.kind === "file" ? " · " + item.size : "");
            el.appendChild(cap);
        }
        el.addEventListener("click", function () { pick(item); });
        grid.appendChild(el);
    });
}

function pick(item) {
    if (pickerHandler) pickerHandler(item);
    closePicker();
}

export function initMedia() {
    $("view-grid").addEventListener("click", function () { setView("grid"); });
    $("view-list").addEventListener("click", function () { setView("list"); });

    var searchTimer = null;
    $("search").addEventListener("input", function () {
        clearTimeout(searchTimer);
        searchTimer = setTimeout(function () {
            pickerQuery = $("search").value.trim();
            loadMedia();
        }, 250);
    });

    $("upload").addEventListener("click", function () {
        var input = $("file");
        if (!input.files || input.files.length === 0) return;
        var fd = new FormData();
        fd.append("file", input.files[0]);
        // Upload into the folder being viewed (root/all -> unfiled).
        if (pickerFolder && pickerFolder !== "root") fd.append("folder", pickerFolder);
        setMsg("Uploading…");
        api("/media", { method: "POST", body: fd }).then(function (body) {
            flash("File uploaded");
            input.value = "";
            if (body.media) pick(body.media);
            else loadMedia();
        }).catch(function (err) { setMsg(err.message); });
    });

    $("picker-close").addEventListener("click", closePicker);
    $("overlay").addEventListener("click", closePicker);

    document.addEventListener("keydown", function (e) {
        if (e.key !== "Escape") return;
        // The picker can sit above an open dialog (background image
        // choice), so it takes Escape first.
        if ($("picker").classList.contains("on")) {
            closePicker();
            return;
        }
        if (isDialogOpen()) {
            dialogDismiss();
            return;
        }
        if ($("code-panel").classList.contains("on")) {
            dismissCodePanel();
            return;
        }
        closeMore();
        closeDrawer();
        closeMenuPanel();
    });
}
