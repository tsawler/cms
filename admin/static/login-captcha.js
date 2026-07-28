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
    //
    // The token is collected three ways because the widget's API changed:
    // current versions resolve solve() with nothing and dispatch a "solve"
    // event, older ones resolved with the solution object. Reading the
    // instance's token getter covers both. Whichever arrives first wins.
    function solve() {
        if (solving || typeof window.Cap !== "function") return solving;

        var cap = new window.Cap({ apiEndpoint: endpoint });
        solving = new Promise(function (done) {
            var finish = function (token) {
                if (token && !field.value) field.value = token;
                done();
            };
            cap.addEventListener("solve", function (e) {
                finish((e.detail && e.detail.token) || cap.token);
            });
            // A failed challenge still has to release the submit: the server
            // answers an empty token with its verification message, which is
            // a better outcome than a form that never submits.
            cap.addEventListener("error", function () { done(); });
            Promise.resolve(cap.solve()).then(function (solution) {
                finish((solution && solution.token) || cap.token);
            }, function () { done(); });
        });
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
