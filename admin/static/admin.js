// CMS admin behaviors. Kept tiny and dependency-free; loaded as an external
// file because the admin's CSP forbids inline scripts.
(function () {
    "use strict";

    // Confirmation prompts: <form data-confirm="Are you sure?">
    document.addEventListener("submit", function (e) {
        var msg = e.target.getAttribute && e.target.getAttribute("data-confirm");
        if (msg && !window.confirm(msg)) {
            e.preventDefault();
        }
    });

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
        navigator.clipboard.writeText(btn.getAttribute("data-copy")).then(function () {
            var original = btn.textContent;
            btn.textContent = "Copied!";
            setTimeout(function () { btn.textContent = original; }, 1500);
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
