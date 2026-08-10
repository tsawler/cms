// CMS admin behaviors. Kept tiny and dependency-free; loaded as an external
// file because the admin's CSP forbids inline scripts.
(function () {
    "use strict";

    // Dialogs: [data-dialog-open="id"] opens one, [data-dialog-close]
    // closes the one it sits in, and a click on the backdrop closes too.
    document.addEventListener("click", function (e) {
        if (!e.target.closest) return;

        var opener = e.target.closest("[data-dialog-open]");
        if (opener) {
            var dlg = document.getElementById(opener.getAttribute("data-dialog-open"));
            if (dlg && !dlg.open) dlg.showModal();
            return;
        }
        var ok = e.target.closest("[data-dialog-confirm]");
        if (ok) {
            var owner = ok.closest("dialog");
            if (owner) owner.close("ok");
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

    // Confirmations: <form data-confirm="Question? Detail."> and
    // <a data-confirm="Question? Detail.">
    //
    // window.confirm is synchronous and a dialog is not, so a form that
    // needs confirming is stopped, asked about, and — on a yes — submitted
    // again. `answered` marks the form that already got its yes so the
    // second pass falls straight through. A link is simpler: on a yes the
    // browser just follows its href.
    var confirmDialog = document.getElementById("cms-confirm");
    var pending = null;   // {form, submitter} or {href} waiting on an answer
    var answered = null;  // the form that may now submit

    // The button that submitted the form already names the action and
    // carries its tone, so the dialog says "Delete page" in red rather
    // than a generic "OK" — the action keeps its name through the flow.
    function submitterOf(e, form) {
        return e.submitter || form.querySelector("button[type=submit], button:not([type])");
    }

    // Messages read "Question? Detail." — the question is the heading and
    // the rest is the fine print. A message with no question mark is all
    // heading.
    function splitMessage(msg) {
        var i = msg.indexOf("?");
        if (i > 0 && i < msg.length - 1) return [msg.slice(0, i + 1), msg.slice(i + 1).trim()];
        return [msg, ""];
    }

    // ask opens the dialog with the message split into heading and fine
    // print, the confirm button named after the action it stands for.
    function ask(msg, okLabel, danger) {
        var parts = splitMessage(msg);
        var detail = confirmDialog.querySelector("[data-confirm-detail]");
        confirmDialog.querySelector("[data-confirm-title]").textContent = parts[0];
        detail.textContent = parts[1];
        detail.hidden = !parts[1];

        var okBtn = confirmDialog.querySelector("[data-dialog-confirm]");
        okBtn.textContent = okLabel || confirmDialog.getAttribute("data-t-ok");
        okBtn.className = "cms-btn cms-btn-sm " + (danger ? "cms-btn-danger" : "cms-btn-primary");

        confirmDialog.returnValue = "";
        confirmDialog.showModal();
        // Destructive actions open on Cancel, so a reflexive Return is the
        // safe answer rather than the irreversible one.
        (danger ? confirmDialog.querySelector("[data-dialog-close]") : okBtn).focus();
    }

    document.addEventListener("submit", function (e) {
        var form = e.target;
        if (!form.getAttribute) return;
        var msg = form.getAttribute("data-confirm");
        if (!msg) return;

        if (form === answered) { answered = null; return; }

        e.preventDefault();
        var submitter = submitterOf(e, form);

        // No dialog on the page, or a browser without <dialog>: ask the
        // old way rather than let a destructive action through unasked.
        if (!confirmDialog || !confirmDialog.showModal) {
            if (window.confirm(msg)) {
                answered = form;
                form.requestSubmit(submitter);
            }
            return;
        }

        pending = { form: form, submitter: submitter };
        ask(msg, submitter && submitter.textContent.trim(),
            !!(submitter && submitter.classList.contains("cms-btn-danger")));
    });

    // Confirmed links — the sidebar's action entries (admin.Section's
    // Confirm field). Stopped, asked, and followed on a yes; the link's
    // own text names the action on the dialog's confirm button.
    document.addEventListener("click", function (e) {
        var link = e.target.closest ? e.target.closest("a[data-confirm]") : null;
        if (!link) return;
        var msg = link.getAttribute("data-confirm");
        if (!msg) return;

        e.preventDefault();
        if (!confirmDialog || !confirmDialog.showModal) {
            if (window.confirm(msg)) window.location.assign(link.href);
            return;
        }
        pending = { href: link.href };
        ask(msg, link.textContent.trim(), false);
    });

    if (confirmDialog) {
        confirmDialog.addEventListener("close", function () {
            var asked = pending;
            pending = null;
            if (!asked || confirmDialog.returnValue !== "ok") return;
            if (asked.href) {
                window.location.assign(asked.href);
                return;
            }
            answered = asked.form;
            asked.form.requestSubmit(asked.submitter);
        });
    }

    // Auto-submitting selects: <select data-autosubmit> posts its form on
    // change (used for "move to folder").
    document.addEventListener("change", function (e) {
        if (e.target.matches && e.target.matches("select[data-autosubmit]")) {
            e.target.form.submit();
        }
    });

    // Snippet form: the "Section preset" type reveals the section
    // settings fieldset (#preset-settings).
    document.addEventListener("change", function (e) {
        if (e.target.matches && e.target.matches("select[data-preset-toggle]")) {
            var settings = document.getElementById("preset-settings");
            if (settings) settings.hidden = e.target.value !== "preset";
        }
    });

    // Copy-to-clipboard: <button data-copy="https://...">
    document.addEventListener("click", function (e) {
        var btn = e.target.closest ? e.target.closest("[data-copy]") : null;
        if (!btn) return;
        // A second click while "Copied!" is showing would capture that as
        // the label to restore, and the button would keep it for good.
        if (btn.dataset.copying) return;
        var original = btn.textContent;
        navigator.clipboard.writeText(btn.getAttribute("data-copy")).then(function () {
            btn.dataset.copying = "1";
            btn.textContent = document.body.getAttribute("data-t-copied") || "Copied!";
            setTimeout(function () {
                btn.textContent = original;
                delete btn.dataset.copying;
            }, 1500);
        }, function () {
            // Clipboard refused (insecure context, denied permission).
            // Say nothing rather than claim a copy that did not happen.
        });
    });

    // Slug suggestion on the new-page form: fill [data-slug-target] from
    // [data-slug-source] until the user edits the slug themselves.
    var source = document.querySelector("[data-slug-source]");
    var target = document.querySelector("[data-slug-target]");
    if (source && target) {
        var touched = target.value !== "";
        target.addEventListener("input", function () { touched = true; });
        source.addEventListener("input", function () {
            if (touched) return;
            target.value = source.value
                .toLowerCase()
                .normalize("NFD").replace(/[̀-ͯ]/g, "") // strip accents
                .replace(/[^a-z0-9]+/g, "-")
                .replace(/^-+|-+$/g, "");
        });
    }
})();
