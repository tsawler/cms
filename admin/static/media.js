// Media library behaviors: the modal dialogs and the upload queue.
// Loaded only on the media page (templateData.PageScript). Framework-free
// and external, because the admin's CSP forbids inline scripts.
(function () {
    "use strict";

    // ---------------------------------------------------------------
    // Dialogs: [data-dialog-open="id"] opens, [data-dialog-close] closes,
    // and a click on the backdrop closes too.
    // ---------------------------------------------------------------
    document.addEventListener("click", function (e) {
        if (!e.target.closest) return;

        var opener = e.target.closest("[data-dialog-open]");
        if (opener) {
            var dlg = document.getElementById(opener.getAttribute("data-dialog-open"));
            if (dlg && !dlg.open) dlg.showModal();
            return;
        }
        if (e.target.closest("[data-dialog-close]")) {
            var open = e.target.closest("dialog");
            if (open) open.close();
            return;
        }
        // The dialog element itself fills the viewport behind its panel,
        // so a click that lands on it is a click on the backdrop.
        if (e.target.matches && e.target.matches("dialog.cms-dialog")) e.target.close();
    });

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

    function highlight(on) {
        var target = uploader.open && dropZone ? dropZone : document.body;
        document.body.classList.toggle("cms-dragging", on && target === document.body);
        if (dropZone) dropZone.classList.toggle("cms-dragging", on && target === dropZone);
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
        highlight(false);
        if (!uploader.open) uploader.showModal();
        addFiles(e.dataTransfer.files);
    });
})();
