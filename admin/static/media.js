// Media library behaviors: the modal dialogs and the upload queue.
// Loaded only on the media page (templateData.PageScript). Framework-free
// and external, because the admin's CSP forbids inline scripts.

// captureFrame draws an early frame of a video onto a canvas and resolves
// with a JPEG blob. Always resolves — null on any failure or after a
// timeout — so a stubborn video just goes without a poster. Both blocks
// below use it: the uploader on the local file being sent (as a blob:
// URL), the inspector on an already-stored video it is previewing.
// crossOrigin is set for a stored video served from another origin
// (bucket/CDN); without CORS headers there the canvas would be tainted
// and the capture fails, which the try/catch turns into a null.
function captureFrame(src, crossOrigin) {
    return new Promise(function (resolve) {
        var video = document.createElement("video");
        var done = false;
        var timer = setTimeout(function () { finish(null); }, 5000);
        function finish(blob) {
            if (done) return;
            done = true;
            clearTimeout(timer);
            video.removeAttribute("src");
            resolve(blob);
        }
        video.muted = true;
        video.preload = "auto";
        if (crossOrigin) video.crossOrigin = "anonymous";
        video.addEventListener("error", function () { finish(null); });
        video.addEventListener("loadeddata", function () {
            // Skip a beat in: the very first frame is often black.
            try { video.currentTime = Math.min(0.5, (video.duration || 1) / 2); }
            catch (e) { finish(null); }
        });
        video.addEventListener("seeked", function () {
            try {
                if (!video.videoWidth) { finish(null); return; }
                var canvas = document.createElement("canvas");
                canvas.width = video.videoWidth;
                canvas.height = video.videoHeight;
                canvas.getContext("2d").drawImage(video, 0, 0);
                canvas.toBlob(function (blob) { finish(blob); }, "image/jpeg", 0.85);
            } catch (e) { finish(null); }
        });
        video.src = src;
    });
}

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
    var doneBtn = uploader.querySelector("[data-upload-done]");

    var jobs = [];
    var active = 0;
    var succeeded = 0;

    // KIND_TABS maps the kind the server reports to the tab that lists
    // it, mirroring kindForTab on the server.
    var KIND_TABS = { image: "images", video: "videos", file: "documents" };

    // Where the finished files landed. The listing is partitioned by kind
    // — one tab each — so a batch is often something the tab in front of
    // the user does not list; closing reads these to pick a view that
    // actually shows what was just sent. See destination().
    var uploadedTabs = {};
    var anyUnfiled = false;

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
        // While transfers run, Stop is the way out; a clickable Done here
        // would silently cancel whatever is still in flight.
        doneBtn.disabled = true;
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

    function isVideoFile(file) {
        return file.type.indexOf("video/") === 0 || /\.(mp4|webm)$/i.test(file.name);
    }

    function start(job) {
        job.state = "uploading";
        active++;
        setState(job, "", "");
        setProgress(job, 0);

        // Videos ride along with a poster frame captured in the browser —
        // the server stores them as uploaded and can't extract one itself.
        // The capture occupies the job's slot: it is part of sending that
        // file, and letting another transfer start meanwhile would put
        // three requests on the wire when the cap says two.
        if (isVideoFile(job.file)) {
            var url = URL.createObjectURL(job.file);
            captureFrame(url).then(function (blob) {
                URL.revokeObjectURL(url);
                if (job.state !== "uploading") return; // canceled during capture
                send(job, blob);
            });
            return;
        }
        send(job, null);
    }

    function send(job, poster) {
        var form = new FormData();
        form.append("file", job.file);
        form.append("folder", job.folder);
        if (poster) form.append("poster", poster, "poster.jpg");

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
                if (body.media && KIND_TABS[body.media.kind]) {
                    uploadedTabs[KIND_TABS[body.media.kind]] = true;
                }
                if (body.filed === false) {
                    anyUnfiled = true;
                    note(job, t("unfiled"), false);
                }
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
        if (job.state === "uploading") {
            job.state = "canceled";
            setState(job, t("canceled"), "muted");
            job.cancelBtn.remove();
            // Mid-transfer the abort handler releases the slot and pumps;
            // mid-capture there is no request yet, so release it here —
            // the capture callback sees the state and never sends.
            if (job.xhr) job.xhr.abort();
            else { active--; pump(); }
            return;
        }
        if (job.state === "queued") {
            job.state = "canceled";
            setState(job, t("canceled"), "muted");
            job.cancelBtn.remove();
            pump();
        }
    }

    // currentTab is the tab being browsed, mirroring the server's
    // mediaTab: anything unknown or absent is the images tab.
    function currentTab() {
        var m = /[?&]tab=([^&]*)/.exec(window.location.search);
        var tab = m ? decodeURIComponent(m[1]) : "";
        return tab === "documents" || tab === "videos" ? tab : "images";
    }

    // destination is the tab to land on once the dialog closes, or "" to
    // stay put and reload. A batch of one kind that this tab does not
    // list would otherwise appear to have vanished: the reload would come
    // back to the same tab, which lists only its own kind. A mixed batch
    // has no single right answer, so it reloads and at least shows the
    // part that belongs here.
    //
    // Uploads that switch tabs are always unfiled — the destination menu
    // only offers this tab's folders, and the server unfiles anything
    // aimed at a folder of the wrong kind — so the plain tab view shows
    // them, and dropping the current folder and search is deliberate.
    function destination() {
        var tabs = Object.keys(uploadedTabs);
        if (tabs.length !== 1 || tabs[0] === currentTab()) return "";
        return window.location.pathname + "?tab=" + tabs[0];
    }

    function finish() {
        stopBtn.hidden = true;
        moreBtn.hidden = false;
        doneBtn.disabled = false;

        // Every file landed: nothing in the queue needs reading, so close
        // — the close handler reloads the listing. A failure or a cancel
        // keeps the dialog open so its rows and notes can be seen, and so
        // does a file that missed its folder: closing on the same tick
        // that writes that note would flash it past unread.
        var allDone = jobs.length > 0 && jobs.every(function (j) { return j.state === "done"; });
        if (allDone && !anyUnfiled) {
            uploader.close();
            return;
        }

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
    // uploaded anything closes by navigating rather than leaving a stale
    // page behind — to the tab that lists what was sent when that is not
    // the tab already on screen, otherwise a reload in place. Closing
    // mid-transfer stops what is left first: the navigation would kill
    // those requests anyway, and a row that reported progress and then
    // silently vanished is worse than one marked canceled.
    uploader.addEventListener("close", function () {
        jobs.forEach(function (job) {
            if (job.state === "queued" || job.state === "uploading") cancel(job);
        });
        if (succeeded === 0) return;
        var dest = destination();
        if (dest) window.location.href = dest;
        else window.location.reload();
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
    var downloadLink = selbar.querySelector("[data-sel-download]");
    var clearBtn = selbar.querySelector("[data-sel-clear]");
    // Both delete forms carry data-confirm, so each is found by its own
    // marker rather than by having the only confirmation in the bar.
    var deleteForm = selbar.querySelector("[data-sel-delete]");
    var folderDeleteForm = selbar.querySelector("[data-sel-folder-delete]");
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

    // ---------------------------------------------------------------
    // Download
    // ---------------------------------------------------------------

    // Following the link is what a download normally is, and it reports
    // nothing back: a file that never arrived looks exactly like one that
    // did. Fetching it instead gives an answer to say out loud. The blob
    // that costs is the reason for the ceiling — buffering half a
    // gigabyte of video in memory to be able to say "done" is a worse
    // trade than letting the browser stream it and saying so up front.
    var maxFetchedBytes = 64 << 20;

    function saveBlob(blob, name) {
        var url = window.URL.createObjectURL(blob);
        var link = document.createElement("a");
        link.href = url;
        link.download = name || "download";
        document.body.appendChild(link);
        link.click();
        link.remove();
        // Not revoked on the spot: a browser that has not started reading
        // the blob yet would find it gone and save nothing.
        setTimeout(function () { window.URL.revokeObjectURL(url); }, 60000);
    }

    function say(key, name) {
        var toast = window.cmsToast;
        if (!toast) return;
        var text = downloadLink.getAttribute("data-t-" + key) || "";
        toast(text.replace("{name}", name), key === "failed" ? "error" : "ok");
    }

    if (downloadLink) downloadLink.addEventListener("click", function (e) {
        var href = downloadLink.getAttribute("href");
        var item = selected().filter(function (x) { return x.getAttribute("data-id"); })[0];
        if (!href || !item) return;

        var name = item.getAttribute("data-name") || "";
        if (Number(item.getAttribute("data-size") || 0) > maxFetchedBytes) {
            // The link does the work; all we can honestly report is that
            // it started.
            say("started", name);
            return;
        }

        e.preventDefault();
        if (downloadLink.dataset.busy) return;
        downloadLink.dataset.busy = "1";
        window.fetch(href).then(function (res) {
            if (!res.ok) throw new Error("HTTP " + res.status);
            return res.blob();
        }).then(function (blob) {
            saveBlob(blob, name);
            say("done", name);
        }).catch(function () {
            // A 404 for an object the bucket lost, a session that expired
            // while the page sat open, a network that dropped: all of it
            // is one thing to the person who clicked.
            say("failed", name);
        }).then(function () {
            delete downloadLink.dataset.busy;
        });
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
            // Whichever delete the selection is showing is the one the key
            // presses — including the folder one, so a folder selected with
            // the arrows is deleted the same way a file is.
            var form = folderDeleteForm && !folderDeleteForm.hidden ? folderDeleteForm : deleteForm;
            var submit = form.querySelector("button[type=submit]");
            if (!submit.disabled) {
                e.preventDefault();
                // requestSubmit, not submit, so the confirmation still runs;
                // naming the button gives the dialog its label and tone.
                form.requestSubmit(submit);
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

    // setDownload points a download link at one entry, or takes its href
    // away. An anchor cannot be disabled, so without a file it loses the
    // href — which is also what stops it being focused or followed — and
    // says so to assistive tech.
    function setDownload(link, item) {
        if (!link) return;
        var template = link.getAttribute("data-href-template");
        var id = item && item.getAttribute("data-id");
        if (template && id) {
            link.href = template.replace("{id}", id);
            link.removeAttribute("aria-disabled");
        } else {
            link.removeAttribute("href");
            link.setAttribute("aria-disabled", "true");
        }
    }

    function sync() {
        var sel = selected();
        var files = sel.filter(function (x) { return x.getAttribute("data-id"); });

        // Blanked rather than removed: the bar keeps its space so the grid
        // under it never jumps between the two clicks of a double-click.
        if (sel.length === 0) selbar.setAttribute("data-empty", "");
        else selbar.removeAttribute("data-empty");

        countEl.textContent = countEl.getAttribute("data-t-one") && sel.length === 1
            ? countEl.getAttribute("data-t-one")
            : (countEl.getAttribute("data-t-many") || "{n}").replace("{n}", sel.length);

        // Moving needs files; a folder in the selection has neither an id
        // nor anywhere to be moved to.
        moveSel.disabled = files.length === 0;
        moveSel.value = "";
        if (upBtn) upBtn.disabled = files.length === 0;
        deleteForm.querySelector("button").disabled = files.length === 0;

        // One folder on its own is deleted as a folder. Any file in the
        // selection means the file Delete is the one that was meant, and a
        // second folder makes the target ambiguous, so both fall back.
        var folder = sel.length === 1 && files.length === 0 && isFolder(sel[0]) ? sel[0] : null;
        if (folderDeleteForm) {
            folderDeleteForm.hidden = !folder;
            deleteForm.hidden = !!folder;
            if (folder) {
                folderDeleteForm.action = folderDeleteForm.getAttribute("data-action-template")
                    .replace("{id}", folder.getAttribute("data-folder-id"));
                var btn = folderDeleteForm.querySelector("button");
                var empty = Number(folder.getAttribute("data-count") || 0) === 0;
                btn.disabled = !empty;
                if (empty) btn.removeAttribute("title");
                else btn.title = btn.getAttribute("data-t-notempty") || "";
            }
        }

        // Reuse the shared [data-copy] handler by handing it the URL.
        var single = files.length === 1 ? files[0].getAttribute("data-url") : "";
        copyBtn.disabled = !single;
        if (single) copyBtn.setAttribute("data-copy", single);
        else copyBtn.removeAttribute("data-copy");

        // Same rule as Copy link: one file names one thing to download.
        // A folder has no object of its own behind it, and several files
        // would have to be zipped to be one download.
        setDownload(downloadLink, files.length === 1 ? files[0] : null);

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
        var renameForm = inspector.querySelector("[data-inspector-rename]");
        var altForm = inspector.querySelector("[data-inspector-alt]");

        preview.textContent = "";
        factsEl.textContent = "";

        if (files.length !== 1) {
            nameEl.textContent = inspector.getAttribute("data-t-none") || "";
            nameEl.classList.add("cms-muted");
            renameForm.hidden = true;
            altForm.hidden = true;
            return;
        }

        var el = files[0];
        nameEl.textContent = el.getAttribute("data-name");
        nameEl.classList.remove("cms-muted");

        var thumb = el.getAttribute("data-thumb");
        if (el.getAttribute("data-kind") === "video") {
            // metadata only: selecting a tile should not start pulling a
            // half-gigabyte file. The proxy serves Range requests, so the
            // player seeks fine once someone presses play.
            var player = document.createElement("video");
            player.controls = true;
            player.preload = "metadata";
            player.src = el.getAttribute("data-url");
            var posterURL = el.getAttribute("data-poster");
            if (posterURL) player.poster = posterURL;
            else backfillPoster(el, player);
            preview.appendChild(player);
        } else if (thumb) {
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

        // The name is edited without its extension: the extension names
        // the format, which a rename can't change, so it sits after the
        // input as a fixed suffix.
        renameForm.hidden = false;
        renameForm.action = renameForm.getAttribute("data-action-template")
            .replace("{id}", el.getAttribute("data-id"));
        var fullName = el.getAttribute("data-name") || "";
        var ext = el.getAttribute("data-ext");
        var stem = fullName;
        if (ext && fullName.toLowerCase().slice(-(ext.length + 1)) === "." + ext.toLowerCase()) {
            stem = fullName.slice(0, -(ext.length + 1));
        }
        renameForm.querySelector("input[name=name]").value = stem;
        renameForm.querySelector("[data-rename-ext]").textContent = ext ? "." + ext : "";

        // Alt text is for pictures; a PDF has no picture to describe.
        var isImage = el.getAttribute("data-kind") === "image";
        altForm.hidden = !isImage;
        if (isImage) {
            altForm.action = altForm.getAttribute("data-action-template")
                .replace("{id}", el.getAttribute("data-id"));
            altForm.querySelector("input[name=alt]").value = el.getAttribute("data-alt") || "";
        }
    }

    // ---------------------------------------------------------------
    // Poster backfill: a video uploaded without a poster frame (the no-JS
    // form, an upload older than poster capture) gets one the first time
    // somebody previews it here. The server can't decode video, so a
    // browser with the file on screen is the only place a frame for a
    // stored video can come from.
    // ---------------------------------------------------------------
    function backfillPoster(el, player) {
        var template = inspector.getAttribute("data-poster-template");
        var csrf = inspector.getAttribute("data-csrf");
        var id = el.getAttribute("data-id");
        if (!template || !csrf || !id) return;
        // One try per page view: a failure (an uncooperative codec, a
        // cross-origin bucket without CORS) would otherwise repeat on
        // every click of the same tile.
        if (el.hasAttribute("data-poster-tried")) return;
        el.setAttribute("data-poster-tried", "1");

        var src = el.getAttribute("data-url");
        var cross;
        try { cross = new URL(src, window.location.href).origin !== window.location.origin; }
        catch (err) { return; }

        captureFrame(src, cross).then(function (blob) {
            if (!blob) return null;
            var form = new FormData();
            form.append("poster", blob, "poster.jpg");
            return window.fetch(template.replace("{id}", id), {
                method: "POST",
                headers: { "X-CSRF-Token": csrf },
                body: form,
            }).then(function (res) {
                return res.ok ? res.json() : null;
            }).then(function (body) {
                if (body && body.media) applyPoster(el, player, body.media);
            });
        }).catch(function () { /* decorative; the video still plays */ });
    }

    // applyPoster threads a fresh poster into everything on the page that
    // shows this video: the entry's data, its tile, and the player when it
    // is still the one on display.
    function applyPoster(el, player, media) {
        el.setAttribute("data-poster", media.poster || "");
        el.setAttribute("data-thumb", media.thumb || "");
        if (media.width) el.setAttribute("data-dims", media.width + "×" + media.height);

        var tile = el.querySelector(".cms-item-thumb");
        if (tile && media.thumb) {
            tile.textContent = "";
            var img = document.createElement("img");
            img.src = media.thumb;
            img.alt = "";
            img.loading = "lazy";
            img.draggable = false;
            tile.appendChild(img);
        }
        var dims = el.querySelector(".cms-item-dims");
        if (dims && media.width) dims.textContent = media.width + "×" + media.height;

        if (player && player.isConnected && media.poster) player.poster = media.poster;
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
    // Drag to move
    //
    // Files are dragged onto a folder to file them, or onto the "All
    // media" crumb to send them back to the root. This is an accelerator
    // for the selection bar, never the only way: the move dropdown and
    // the ↑ All media button do the same work for anyone who can't drag.
    //
    // Uploads from the desktop are a different gesture handled above, and
    // the two never collide — an OS drag carries "Files", this one
    // carries a type of our own, and each ignores what it doesn't own.
    // ---------------------------------------------------------------
    var DRAG_TYPE = "application/x-cms-media";
    var dragForm = document.querySelector("[data-drag-move]");
    var dropEl = null;

    // Only the types are readable during a drag — getData is withheld
    // until drop — so the payload is what marks a drag as one of ours.
    function draggingItems(e) {
        var types = e.dataTransfer && e.dataTransfer.types;
        if (!types) return false;
        for (var i = 0; i < types.length; i++) {
            if (types[i] === DRAG_TYPE) return true;
        }
        return false;
    }

    function markDrop(el) {
        if (dropEl === el) return;
        if (dropEl) dropEl.classList.remove("cms-drop-target");
        dropEl = el;
        if (dropEl) dropEl.classList.add("cms-drop-target");
    }

    function fileIDs() {
        return selected()
            .map(function (x) { return x.getAttribute("data-id"); })
            .filter(Boolean);
    }

    itemsEl.addEventListener("dragstart", function (e) {
        var el = e.target.closest ? e.target.closest(".cms-item") : null;
        // Folders have no id and nowhere to go; neither has anything to drag.
        if (!el || !el.getAttribute("data-id")) { e.preventDefault(); return; }

        // Dragging something outside the selection makes it the selection,
        // the way a file manager does — otherwise the drop would move
        // whatever happened to be selected instead of what was grabbed.
        if (!isSelected(el)) { selectOnly(el); sync(); }

        var ids = fileIDs();
        if (!ids.length) { e.preventDefault(); return; }
        e.dataTransfer.setData(DRAG_TYPE, ids.join(","));
        e.dataTransfer.effectAllowed = "move";
        itemsEl.classList.add("cms-dragging-items");
    });

    itemsEl.addEventListener("dragend", function () {
        itemsEl.classList.remove("cms-dragging-items");
        markDrop(null);
    });

    // dropTarget wires one destination. `folder` is what the move form
    // posts: a folder id, or "root" for unfiled.
    function dropTarget(el, folder) {
        if (!el || !folder) return;

        function over(e) {
            if (!draggingItems(e)) return;
            e.preventDefault(); // without this the drop never fires
            e.dataTransfer.dropEffect = "move";
            markDrop(el);
        }
        el.addEventListener("dragenter", over);
        el.addEventListener("dragover", over);

        el.addEventListener("dragleave", function (e) {
            // Crossing onto a child fires leave on the parent; those are
            // not departures, and acting on them makes the target flicker.
            if (e.relatedTarget && el.contains(e.relatedTarget)) return;
            if (dropEl === el) markDrop(null);
        });

        el.addEventListener("drop", function (e) {
            if (!draggingItems(e)) return;
            e.preventDefault(); // a drop on a link would otherwise navigate
            markDrop(null);
            if (!dragForm || !fileIDs().length) return;
            dragForm.querySelector("input[name=folder]").value = folder;
            // The shared [data-sel-form] handler fills in the ids.
            dragForm.requestSubmit();
        });
    }

    // Folder entries only exist at the root, and the crumb only inside a
    // folder, so exactly one of these is ever wired on a given view.
    function wireDropTargets() {
        items.filter(isFolder).forEach(function (el) {
            dropTarget(el, el.getAttribute("data-folder-id"));
        });
        dropTarget(document.querySelector("[data-crumb-root]"), "root");
    }

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
    // After applySort, which is what fills `items`. Sorting re-appends the
    // same nodes rather than rebuilding them, so wiring once holds.
    wireDropTargets();
    sync();
})();
