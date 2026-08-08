// Client-side validation for the account settings forms (and the login
// code challenge). The server stays the authority — these checks mirror
// its rules so a mistake surfaces beside the field before a round trip,
// not instead of one.
//
// The rhythm is "punish late, reward early": a field is left in peace
// until it is left (blur) or its form is submitted; once it has failed,
// it revalidates on every keystroke so the error vanishes the moment it
// is fixed, and the fix is acknowledged with a confirming border.
//
// Rules and their messages ride in on data attributes (data-v marks a
// validated input), so this file stays translation-free: the templates
// emit the same translated strings the server would.
(function () {
    "use strict";

    function norm(s) { return s.trim().toLowerCase(); }

    // origOf is the value a "has it changed?" rule compares against —
    // data-orig (the stored value, survives an error re-render) when the
    // template provides it, the rendered value otherwise.
    function origOf(input) {
        var o = input.getAttribute("data-orig");
        return o === null ? input.defaultValue : o;
    }

    // changedFrom reports whether the input referenced by sel has moved
    // away from its stored value.
    function changedFrom(sel) {
        var other = document.querySelector(sel);
        return !!other && norm(other.value) !== norm(origOf(other));
    }

    // ruleFor returns the failure message for the input's current value,
    // or "" when it passes. Order matters: emptiness first, shape after,
    // so an empty field never shows a shape complaint.
    function ruleFor(input) {
        var v = input.value.trim();

        var required = input.getAttribute("data-v-required");
        if (required && !v) return required;

        // Required only while a sibling departs from its stored value:
        // the profile form's password, needed once the email changes.
        var reqChanged = input.getAttribute("data-v-reqchanged");
        if (reqChanged && !v && changedFrom(reqChanged)) {
            return input.getAttribute("data-v-reqchanged-msg") || "";
        }
        if (!v) return ""; // nothing below applies to an empty optional field

        if (input.getAttribute("data-v-email") !== null && input.validity.typeMismatch) {
            return input.getAttribute("data-v-email");
        }
        var digits = input.getAttribute("data-v-digits");
        if (digits && !new RegExp("^[0-9]{" + digits + "}$").test(v.replace(/\s+/g, ""))) {
            return input.getAttribute("data-v-digits-msg") || "";
        }
        var minlen = input.getAttribute("data-v-minlen");
        if (minlen && input.value.length < +minlen) { // raw length, matching the server
            return input.getAttribute("data-v-minlen-msg") || "";
        }
        var match = input.getAttribute("data-v-match");
        if (match) {
            var against = document.querySelector(match);
            if (against && input.value !== against.value) {
                return input.getAttribute("data-v-match-msg") || "";
            }
        }
        return "";
    }

    // show paints one field's verdict: the message under the input (the
    // same element a server error renders as, so the two can never
    // stack), a red border while wrong, and — only for a field that had
    // been wrong — a confirming border once it is right.
    function show(input, msg) {
        var field = input.closest(".cms-field");
        if (!field) return;
        var err = field.querySelector(".cms-field-error");
        if (msg) {
            if (!err) {
                err = document.createElement("p");
                err.className = "cms-field-error";
                input.insertAdjacentElement("afterend", err);
            }
            err.textContent = msg;
            if (input.id) {
                err.id = input.id + "-error";
                input.setAttribute("aria-describedby", err.id);
            }
            input.setAttribute("aria-invalid", "true");
            field.classList.add("cms-field-bad");
            field.classList.remove("cms-field-good");
            input.setAttribute("data-was-bad", "1");
        } else {
            if (err) err.remove();
            input.removeAttribute("aria-invalid");
            field.classList.remove("cms-field-bad");
            field.classList.toggle("cms-field-good", input.getAttribute("data-was-bad") === "1");
        }
    }

    function check(input) {
        var msg = ruleFor(input);
        show(input, msg);
        return !msg;
    }

    var wired = document.querySelectorAll("form[data-validate]");
    Array.prototype.forEach.call(wired, function (form) {
        var inputs = form.querySelectorAll("input[data-v]");

        Array.prototype.forEach.call(inputs, function (input) {
            input.addEventListener("blur", function () {
                // An untouched empty field stays unjudged on blur —
                // tabbing through the form is not filling it in wrong.
                if (input.value === "" && input.getAttribute("data-was-bad") !== "1") return;
                check(input);
            });
            input.addEventListener("input", function () {
                if (input.getAttribute("data-was-bad") === "1") check(input);
            });

            // Cross-field wiring. A match rule ("repeat the password")
            // re-judges when either half changes; a required-if-changed
            // rule re-judges with the field it watches, and flags its
            // own field while the requirement is live so the eye is on
            // it before the error would be.
            var matchSel = input.getAttribute("data-v-match");
            if (matchSel) {
                var against = document.querySelector(matchSel);
                if (against) against.addEventListener("input", function () {
                    if (input.value || input.getAttribute("data-was-bad") === "1") check(input);
                });
            }
            var reqSel = input.getAttribute("data-v-reqchanged");
            if (reqSel) {
                var watched = document.querySelector(reqSel);
                var field = input.closest(".cms-field");
                if (watched && field) {
                    var reflect = function () {
                        field.classList.toggle("cms-field-attn", changedFrom(reqSel));
                        if (input.getAttribute("data-was-bad") === "1") check(input);
                    };
                    watched.addEventListener("input", reflect);
                    reflect(); // an error re-render may load mid-change
                }
            }
        });

        form.addEventListener("submit", function (e) {
            // A submitter with its own formaction (the enrollment form's
            // Cancel) is leaving the form, not submitting it — let it go.
            if (e.submitter && e.submitter.hasAttribute("formaction")) return;
            var firstBad = null;
            Array.prototype.forEach.call(inputs, function (input) {
                if (!check(input) && !firstBad) firstBad = input;
            });
            if (firstBad) {
                e.preventDefault();
                firstBad.focus();
            }
        });
    });
})();
