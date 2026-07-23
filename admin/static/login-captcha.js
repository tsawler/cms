// Invisible login CAPTCHA. The login form carries data-cap-endpoint when
// Cap runs in programmatic mode: solve the proof-of-work challenge in the
// background with the Cap class (exposed by the Cap server's widget.js,
// which the login page loads) and put the token in the hidden cap-token
// field — the user never sees a widget. Loaded as an external file because
// the admin's CSP forbids inline scripts.
(function () {
    "use strict";

    var form = document.querySelector("form[data-cap-endpoint]");
    if (!form) return;
    var endpoint = form.getAttribute("data-cap-endpoint");
    var field = form.querySelector('input[name="cap-token"]');
    var solving = null;

    // Returns a promise that fills the token field, or null when the Cap
    // script isn't available (e.g. the Cap server is down).
    function solve() {
        if (!solving && typeof window.Cap === "function") {
            solving = new window.Cap({ apiEndpoint: endpoint }).solve()
                .then(function (solution) { field.value = solution.token; });
        }
        return solving;
    }

    // Start solving as soon as the user shows intent, so the challenge is
    // usually done before they finish typing their password.
    form.addEventListener("input", solve, { once: true });

    form.addEventListener("submit", function (e) {
        if (field.value) return; // solved — let the submit through
        var pending = solve();
        // Without the Cap script there is no token to wait for; submit and
        // let the server respond with its verification message.
        if (!pending) return;
        e.preventDefault();
        var btn = form.querySelector('button[type="submit"]');
        if (btn) btn.disabled = true;
        var submit = function () { form.submit(); };
        pending.then(submit, submit);
    });
})();
