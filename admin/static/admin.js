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
