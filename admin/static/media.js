// Media library behaviors: the modal dialogs and the upload queue.
// Loaded only on the media page (templateData.PageScript). Framework-free
// and external, because the admin's CSP forbids inline scripts.
(function () {
    "use strict";

    // Opening and closing dialogs is shared admin chrome; admin.js owns it.
    var uploader = document.getElementById("cms-uploader");
    if (!uploader) return;

    // ---------------------------------------------------------------
    // Upload queue
    // ---------------------------------------------------------------
    var MAX_CONCURRENT = 2; // one request per file: the server caps each body

    var uploadURL = uploader.getAttribute("data-upload-url");
    var csrfToken = uploader.getAttribute("data-csrf");
    var pickPane = uploader.querySelector("[data-upload-pick]");
    var queueEl = uploader.querySelector("[data-upload-queue]");
    var titleEl = uploader.querySelector("[data-upload-title]");
    var fileInput = uploader.querySelector("[data-upload-input]");
    var folderSel = uploader.querySelector("[data-upload-folder]");
    var dropZone = uploader.querySelector("[data-dropzone]");
    var moreBtn = uploader.querySelector("[data-upload-more]");
    var stopBtn = uploader.querySelector("[data-upload-stop]");

    var jobs = [];
    var active = 0;
    var succeeded = 0;

    // t looks up a translated string the template put on the dialog, and
    // fills {placeholders} from vars.
    function t(name, vars) {
        var s = uploader.getAttribute("data-t-" + name) || "";
        if (vars) {
            Object.keys(vars).forEach(function (k) {
                s = s.replace("{" + k + "}", vars[k]);
            });
        }
        return s;
    }

    function humanSize(bytes) {
        if (bytes < 1024) return bytes + " B";
        var units = ["KB", "MB", "GB"];
        var n = bytes / 1024;
        for (var i = 0; i < units.length; i++) {
            if (n < 1024 || i === units.length - 1) {
                return (n < 10 ? n.toFixed(1) : Math.round(n)) + " " + units[i];
            }
            n = n / 1024;
        }
    }

    // buildRow renders one queue entry. Built node by node rather than from
    // an HTML string so a filename can never be read as markup.
    function buildRow(job) {
        var li = document.createElement("li");
        li.className = "cms-upload-item";

        var name = document.createElement("span");
        name.className = "cms-upload-name";
        name.textContent = job.file.name;
        name.title = job.file.name;

        var size = document.createElement("span");
        size.className = "cms-upload-size";
        size.textContent = humanSize(job.file.size);

        var track = document.createElement("span");
        track.className = "cms-upload-track";
        job.bar = document.createElement("i");
        track.appendChild(job.bar);

        job.stateEl = document.createElement("span");
        job.stateEl.className = "cms-upload-state";
        job.stateEl.textContent = t("queued");

        job.cancelBtn = document.createElement("button");
        job.cancelBtn.type = "button";
        job.cancelBtn.className = "cms-upload-cancel";
        job.cancelBtn.textContent = "✕";
        job.cancelBtn.setAttribute("aria-label", t("cancel"));
        job.cancelBtn.addEventListener("click", function () { cancel(job); });

        li.appendChild(name);
        li.appendChild(size);
        li.appendChild(track);
        li.appendChild(job.stateEl);
        li.appendChild(job.cancelBtn);
        job.row = li;
        return li;
    }

    function setProgress(job, fraction) {
        job.bar.style.width = Math.round(fraction * 100) + "%";
    }

    function setState(job, text, modifier) {
        job.stateEl.textContent = text;
        job.row.className = "cms-upload-item" + (modifier ? " cms-upload-" + modifier : "");
    }

    // note adds a full-width line under a row's columns: why it failed, or
    // why it did not land where it was aimed. Keeping prose out of the
    // state column is what stops a long message reflowing the grid.
    function note(job, text, isError) {
        var el = document.createElement("span");
        el.className = "cms-upload-note" + (isError ? " cms-upload-note-error" : "");
        el.textContent = text;
        job.row.appendChild(el);
    }

    function addFiles(list) {
        if (!list || !list.length) return;
        var folder = folderSel ? folderSel.value : "";
        for (var i = 0; i < list.length; i++) {
            var job = { file: list[i], folder: folder, state: "queued", xhr: null };
            queueEl.appendChild(buildRow(job));
            jobs.push(job);
        }
        pickPane.hidden = true;
        queueEl.hidden = false;
        moreBtn.hidden = true;
        stopBtn.hidden = false;
        pump();
    }

    function pump() {
        while (active < MAX_CONCURRENT) {
            var next = null;
            for (var i = 0; i < jobs.length; i++) {
                if (jobs[i].state === "queued") { next = jobs[i]; break; }
            }
            if (!next) break;
            start(next);
        }

        var pending = jobs.some(function (j) {
            return j.state === "queued" || j.state === "uploading";
        });
        if (pending) {
            var done = jobs.filter(function (j) { return j.state !== "queued" && j.state !== "uploading"; }).length;
            titleEl.textContent = t("progress", { done: done, total: jobs.length });
        } else {
            finish();
        }
    }

    function start(job) {
        job.state = "uploading";
        active++;
        setState(job, "", "");
        setProgress(job, 0);

        var form = new FormData();
        form.append("file", job.file);
        form.append("folder", job.folder);

        var xhr = new XMLHttpRequest();
        job.xhr = xhr;
        xhr.open("POST", uploadURL, true);
        xhr.setRequestHeader("X-CSRF-Token", csrfToken);

        xhr.upload.addEventListener("progress", function (e) {
            if (!e.lengthComputable) return;
            setProgress(job, e.loaded / e.total);
            job.stateEl.textContent = Math.round((e.loaded / e.total) * 100) + "%";
        });
        xhr.addEventListener("load", function () {
            active--;
            var body = null;
            try { body = JSON.parse(xhr.responseText); } catch (err) { /* not JSON: server error page */ }
            if (xhr.status >= 200 && xhr.status < 300 && body && body.ok) {
                job.state = "done";
                succeeded++;
                setProgress(job, 1);
                setState(job, t("done"), "ok");
                job.cancelBtn.remove();
                if (body.filed === false) note(job, t("unfiled"), false);
            } else {
                fail(job, (body && body.error) || t("failed"));
            }
            pump();
        });
        xhr.addEventListener("error", function () {
            active--;
            fail(job, t("failed"));
            pump();
        });
        xhr.addEventListener("abort", function () {
            active--;
            pump();
        });
        xhr.send(form);
    }

    function fail(job, message) {
        job.state = "failed";
        setProgress(job, 0);
        setState(job, t("failedstate"), "failed");
        note(job, message, true);
        job.cancelBtn.remove();
    }

    function cancel(job) {
        if (job.state === "uploading" && job.xhr) {
            job.state = "canceled";
            setState(job, t("canceled"), "muted");
            job.cancelBtn.remove();
            job.xhr.abort(); // the abort handler releases the slot and pumps
            return;
        }
        if (job.state === "queued") {
            job.state = "canceled";
            setState(job, t("canceled"), "muted");
            job.cancelBtn.remove();
            pump();
        }
    }

    function finish() {
        stopBtn.hidden = true;
        moreBtn.hidden = false;
        var failed = jobs.filter(function (j) { return j.state === "failed"; }).length;
        if (failed) {
            titleEl.textContent = t("partial", { failed: failed });
        } else if (succeeded) {
            titleEl.textContent = t("complete");
        } else {
            titleEl.textContent = t("title"); // everything was canceled
        }
    }

    stopBtn.addEventListener("click", function () {
        jobs.forEach(function (job) {
            if (job.state === "queued" || job.state === "uploading") cancel(job);
        });
    });

    moreBtn.addEventListener("click", function () {
        pickPane.hidden = false;
        if (fileInput) fileInput.focus();
    });

    fileInput.addEventListener("change", function () {
        addFiles(fileInput.files);
        fileInput.value = ""; // so the same file can be picked again
    });

    // Uploads change what the listing should show, so a dialog that
    // uploaded anything closes by reloading rather than leaving a stale
    // page behind. Closing mid-transfer stops what is left first: the
    // reload would kill those requests anyway, and a row that reported
    // progress and then silently vanished is worse than one marked
    // canceled.
    uploader.addEventListener("close", function () {
        jobs.forEach(function (job) {
            if (job.state === "queued" || job.state === "uploading") cancel(job);
        });
        if (succeeded > 0) window.location.reload();
    });

    // ---------------------------------------------------------------
    // Drag and drop: files can be dropped anywhere on the page, not only
    // in the dialog. A drop outside opens the dialog and starts the
    // transfer, which is what makes the Upload button the fallback path
    // rather than the only one.
    // ---------------------------------------------------------------
    function hasFiles(e) {
        var dt = e.dataTransfer;
        if (!dt || !dt.types) return false;
        for (var i = 0; i < dt.types.length; i++) {
            if (dt.types[i] === "Files") return true;
        }
        return false;
    }

    // dragDepth counts enter/leave pairs: dragging over a child fires
    // leave on the parent, and without the count the highlight flickers.
    var dragDepth = 0;
    var dropFolder = null; // folder tile currently under the pointer

    function highlight(on) {
        var target = uploader.open && dropZone ? dropZone : document.body;
        document.body.classList.toggle("cms-dragging", on && target === document.body);
        if (dropZone) dropZone.classList.toggle("cms-dragging", on && target === dropZone);
        if (!on) markFolder(null);
    }

    // markFolder tracks the folder tile files would land in, so dropping
    // onto a folder files them there — the same gesture Finder uses.
    function markFolder(tile) {
        if (tile === dropFolder) return;
        if (dropFolder) dropFolder.classList.remove("cms-drop-target");
        dropFolder = tile;
        if (dropFolder) dropFolder.classList.add("cms-drop-target");
    }

    document.addEventListener("dragenter", function (e) {
        if (!hasFiles(e)) return;
        e.preventDefault();
        dragDepth++;
        highlight(true);
    });
    document.addEventListener("dragover", function (e) {
        if (!hasFiles(e)) return;
        e.preventDefault(); // without this the browser navigates to the file
        e.dataTransfer.dropEffect = "copy";
        if (!uploader.open) markFolder(e.target.closest ? e.target.closest(".cms-item-folder") : null);
    });
    document.addEventListener("dragleave", function (e) {
        if (!hasFiles(e)) return;
        dragDepth = Math.max(0, dragDepth - 1);
        if (dragDepth === 0) highlight(false);
    });
    document.addEventListener("drop", function (e) {
        if (!hasFiles(e)) return;
        e.preventDefault();
        dragDepth = 0;

        // Dropped on a folder: that folder is the destination, whatever
        // the dialog's select last said.
        var tile = e.target.closest ? e.target.closest(".cms-item-folder") : null;
        if (tile && folderSel) {
            var id = tile.getAttribute("data-folder-id");
            if (id) folderSel.value = id;
        }
        highlight(false);

        if (!uploader.open) uploader.showModal();
        addFiles(e.dataTransfer.files);
    });
})();

// Media browser: the Finder-style listing. Selection lives on the listing
// rather than on each entry — the actions that used to sit on every tile
// (alt text, folder, copy, delete) now act on whatever is selected, which
// is what lets a tile be a thumbnail and a name and nothing else.
(function () {
    "use strict";

    var itemsEl = document.querySelector("[data-items]");
    if (!itemsEl) return;

    var wrap = itemsEl.closest(".cms-items-wrap");
    var selbar = document.querySelector("[data-selbar]");
    var countEl = selbar.querySelector("[data-sel-count]");
    var moveSel = selbar.querySelector("[data-sel-move]");
    var upBtn = selbar.querySelector("[data-sel-up]"); // rendered only inside a folder
    var copyBtn = selbar.querySelector("[data-sel-copy]");
    var clearBtn = selbar.querySelector("[data-sel-clear]");
    var deleteForm = selbar.querySelector("form[data-confirm]");
    var inspector = document.querySelector("[data-inspector]");
    var inspectorToggle = document.querySelector("[data-inspector-toggle]");

    var items = [];
    var anchor = null; // where a shift-click range starts

    function refresh() {
        items = Array.prototype.slice.call(itemsEl.querySelectorAll(".cms-item"));
    }
    refresh();

    function isFolder(el) { return el.classList.contains("cms-item-folder"); }
    function isSelected(el) { return el.classList.contains("cms-selected"); }
    function selected() { return items.filter(isSelected); }

    function setSelected(el, on) {
        el.classList.toggle("cms-selected", on);
        el.setAttribute("aria-selected", on ? "true" : "false");
    }

    function selectOnly(el) {
        items.forEach(function (x) { setSelected(x, x === el); });
        anchor = el;
    }

    function selectRange(from, to) {
        var a = items.indexOf(from), b = items.indexOf(to);
        if (a < 0 || b < 0) return;
        var lo = Math.min(a, b), hi = Math.max(a, b);
        items.forEach(function (x, i) { setSelected(x, i >= lo && i <= hi); });
    }

    function clearSelection() {
        items.forEach(function (x) { setSelected(x, false); });
        anchor = null;
        sync();
    }

    function open(el) {
        var href = el.getAttribute("data-open");
        if (!href) return;
        // A folder is somewhere to go; a file is something to look at.
        if (isFolder(el)) window.location.href = href;
        else window.open(href, "_blank", "noopener");
    }

    // ---------------------------------------------------------------
    // Pointer
    // ---------------------------------------------------------------
    itemsEl.addEventListener("mousedown", function (e) {
        // Shift-clicking a range would otherwise select the text in it.
        if (e.shiftKey) e.preventDefault();
    });

    itemsEl.addEventListener("click", function (e) {
        var el = e.target.closest(".cms-item");
        if (!el) { clearSelection(); return; }
        if (e.shiftKey && anchor) {
            selectRange(anchor, el);
        } else if (e.metaKey || e.ctrlKey) {
            setSelected(el, !isSelected(el));
            anchor = el;
        } else {
            selectOnly(el);
        }
        itemsEl.focus({ preventScroll: true });
        sync();
    });

    itemsEl.addEventListener("dblclick", function (e) {
        var el = e.target.closest(".cms-item");
        if (el) open(el);
    });

    // ---------------------------------------------------------------
    // Keyboard
    // ---------------------------------------------------------------

    // columns reports how many entries share the first row, which is what
    // up/down has to step by in the icon view. The list view is one column.
    function columns() {
        if (wrap.classList.contains("cms-view-list") || !items.length) return 1;
        var top = items[0].offsetTop, n = 0;
        for (var i = 0; i < items.length; i++) {
            if (items[i].offsetTop !== top) break;
            n++;
        }
        return Math.max(1, n);
    }

    function step(key) {
        var cols = columns();
        switch (key) {
            case "ArrowLeft": return -1;
            case "ArrowRight": return 1;
            case "ArrowUp": return -cols;
            case "ArrowDown": return cols;
        }
        return 0;
    }

    itemsEl.addEventListener("keydown", function (e) {
        if (e.key === "Escape") { clearSelection(); return; }

        if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "a") {
            e.preventDefault();
            items.forEach(function (x) { setSelected(x, true); });
            sync();
            return;
        }

        if (e.key === "Enter") {
            var one = selected();
            if (one.length === 1) { e.preventDefault(); open(one[0]); }
            return;
        }

        if (e.key === "Delete" || e.key === "Backspace") {
            if (selected().some(function (x) { return x.getAttribute("data-id"); })) {
                e.preventDefault();
                // requestSubmit, not submit, so the confirmation still runs;
                // naming the button gives the dialog its label and tone.
                deleteForm.requestSubmit(deleteForm.querySelector("button[type=submit]"));
            }
            return;
        }

        var delta = step(e.key);
        if (!delta) return;
        e.preventDefault();

        var current = selected();
        var from = current.length ? items.indexOf(current[current.length - 1]) : -1;
        var next = Math.min(items.length - 1, Math.max(0, from + delta));
        if (from < 0) next = 0;
        var target = items[next];
        if (!target) return;

        if (e.shiftKey && anchor) selectRange(anchor, target);
        else selectOnly(target);
        target.scrollIntoView({ block: "nearest" });
        sync();
    });

    // ---------------------------------------------------------------
    // Selection bar and inspector
    // ---------------------------------------------------------------
    function sync() {
        var sel = selected();
        var files = sel.filter(function (x) { return x.getAttribute("data-id"); });

        selbar.hidden = sel.length === 0;
        countEl.textContent = countEl.getAttribute("data-t-one") && sel.length === 1
            ? countEl.getAttribute("data-t-one")
            : (countEl.getAttribute("data-t-many") || "{n}").replace("{n}", sel.length);

        // Moving and deleting need files; a folder in the selection has
        // neither an id nor anywhere to be moved to.
        moveSel.disabled = files.length === 0;
        moveSel.value = "";
        if (upBtn) upBtn.disabled = files.length === 0;
        deleteForm.querySelector("button").disabled = files.length === 0;

        // Reuse the shared [data-copy] handler by handing it the URL.
        var single = files.length === 1 ? files[0].getAttribute("data-url") : "";
        copyBtn.disabled = !single;
        if (single) copyBtn.setAttribute("data-copy", single);
        else copyBtn.removeAttribute("data-copy");

        renderInspector(files);
    }

    var inspectorOpen = false;
    try {
        inspectorOpen = window.localStorage.getItem("cms-admin-media-inspector") === "1";
    } catch (err) { /* private mode */ }

    function renderInspector(files) {
        if (!inspector) return;
        inspector.hidden = !inspectorOpen;
        if (!inspectorOpen) return;

        var preview = inspector.querySelector("[data-inspector-preview]");
        var nameEl = inspector.querySelector("[data-inspector-name]");
        var factsEl = inspector.querySelector("[data-inspector-facts]");
        var altForm = inspector.querySelector("[data-inspector-alt]");
        var copy = inspector.querySelector("[data-inspector-copy]");

        preview.textContent = "";
        factsEl.textContent = "";

        if (files.length !== 1) {
            nameEl.textContent = inspector.getAttribute("data-t-none") || "";
            nameEl.classList.add("cms-muted");
            altForm.hidden = true;
            copy.hidden = true;
            return;
        }

        var el = files[0];
        nameEl.textContent = el.getAttribute("data-name");
        nameEl.classList.remove("cms-muted");

        var thumb = el.getAttribute("data-thumb");
        if (thumb) {
            var img = document.createElement("img");
            img.src = thumb;
            img.alt = "";
            preview.appendChild(img);
        }

        [
            ["kind", el.getAttribute("data-ext")],
            ["size", el.getAttribute("data-size-human")],
            ["dims", el.getAttribute("data-dims")],
            ["added", el.getAttribute("data-added-human")]
        ].forEach(function (pair) {
            if (!pair[1]) return;
            var dt = document.createElement("dt");
            dt.textContent = factsEl.getAttribute("data-t-" + pair[0]) || pair[0];
            var dd = document.createElement("dd");
            dd.textContent = pair[1];
            factsEl.appendChild(dt);
            factsEl.appendChild(dd);
        });

        // Alt text is for pictures; a PDF has no picture to describe.
        var isImage = el.getAttribute("data-kind") === "image";
        altForm.hidden = !isImage;
        if (isImage) {
            altForm.action = altForm.getAttribute("data-action-template")
                .replace("{id}", el.getAttribute("data-id"));
            altForm.querySelector("input[name=alt]").value = el.getAttribute("data-alt") || "";
        }

        copy.hidden = false;
        copy.setAttribute("data-copy", el.getAttribute("data-url") || "");
    }

    if (inspectorToggle) {
        inspectorToggle.addEventListener("click", function () {
            inspectorOpen = !inspectorOpen;
            inspectorToggle.setAttribute("aria-pressed", inspectorOpen ? "true" : "false");
            inspectorToggle.classList.toggle("cms-active", inspectorOpen);
            try {
                window.localStorage.setItem("cms-admin-media-inspector", inspectorOpen ? "1" : "0");
            } catch (err) { /* private mode */ }
            sync();
        });
        inspectorToggle.setAttribute("aria-pressed", inspectorOpen ? "true" : "false");
        inspectorToggle.classList.toggle("cms-active", inspectorOpen);
    }

    clearBtn.addEventListener("click", clearSelection);
    moveSel.addEventListener("change", function () {
        if (moveSel.value !== "") moveSel.form.requestSubmit();
    });

    // Bulk forms carry the selection as repeated id fields, rebuilt at
    // submit time so they can never describe a stale selection.
    document.addEventListener("submit", function (e) {
        var form = e.target.closest && e.target.closest("[data-sel-form]");
        if (!form) return;
        form.querySelectorAll('input[name="id"]').forEach(function (n) { n.remove(); });
        selected().forEach(function (el) {
            var id = el.getAttribute("data-id");
            if (!id) return;
            var input = document.createElement("input");
            input.type = "hidden";
            input.name = "id";
            input.value = id;
            form.appendChild(input);
        });
    });

    // ---------------------------------------------------------------
    // View toggle and sorting, both remembered per browser
    // ---------------------------------------------------------------
    var viewToggle = document.querySelector("[data-view-toggle]");

    function applyView(v) {
        wrap.classList.toggle("cms-view-list", v === "list");
        if (viewToggle) {
            viewToggle.querySelectorAll("button").forEach(function (b) {
                b.classList.toggle("cms-active", b.getAttribute("data-view") === v);
            });
        }
        try { window.localStorage.setItem("cms-admin-media-view", v); } catch (err) { /* private mode */ }
    }

    if (viewToggle) {
        viewToggle.addEventListener("click", function (e) {
            var btn = e.target.closest("button[data-view]");
            if (btn) applyView(btn.getAttribute("data-view"));
        });
    }
    var storedView = null;
    try { storedView = window.localStorage.getItem("cms-admin-media-view"); } catch (err) { /* private mode */ }
    applyView(storedView === "list" ? "list" : "grid");

    var sortKey = "name";
    var sortDir = 1;
    try {
        var stored = (window.localStorage.getItem("cms-admin-media-sort") || "").split(":");
        if (stored[0]) { sortKey = stored[0]; sortDir = stored[1] === "-1" ? -1 : 1; }
    } catch (err) { /* private mode */ }

    function value(el, key) {
        switch (key) {
            case "size": return Number(el.getAttribute("data-size") || 0);
            case "added": return Number(el.getAttribute("data-added") || 0);
            case "dims": return parseInt(el.getAttribute("data-dims") || "0", 10) || 0;
            case "kind": return el.getAttribute("data-ext") || "";
            default: return el.getAttribute("data-name") || "";
        }
    }

    function applySort() {
        var folders = items.filter(isFolder);
        var files = items.filter(function (x) { return !isFolder(x); });
        var cmp = function (a, b) {
            var x = value(a, sortKey), y = value(b, sortKey);
            if (typeof x === "string") return x.localeCompare(y) * sortDir;
            return (x - y) * sortDir;
        };
        files.sort(cmp);
        // Folders sort by name whatever the column: they have no size and
        // no dimensions, so sorting them by one would just scramble them.
        folders.sort(function (a, b) {
            return (a.getAttribute("data-name") || "").localeCompare(b.getAttribute("data-name") || "") * sortDir;
        });
        folders.concat(files).forEach(function (el) { itemsEl.appendChild(el); });
        refresh();

        document.querySelectorAll("[data-sort]").forEach(function (btn) {
            var on = btn.getAttribute("data-sort") === sortKey;
            btn.classList.toggle("cms-active", on);
            btn.setAttribute("aria-sort", on ? (sortDir === 1 ? "ascending" : "descending") : "none");
        });
        try {
            window.localStorage.setItem("cms-admin-media-sort", sortKey + ":" + sortDir);
        } catch (err) { /* private mode */ }
    }

    document.querySelectorAll("[data-sort]").forEach(function (btn) {
        btn.addEventListener("click", function () {
            var key = btn.getAttribute("data-sort");
            if (key === sortKey) sortDir = -sortDir;
            else { sortKey = key; sortDir = 1; }
            applySort();
        });
    });

    applySort();
    sync();
})();
