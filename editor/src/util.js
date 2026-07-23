/* Small shared helpers: the status toast, and the JSON API wrapper
 * every request goes through. */

import { $ } from "./shell.js";
import { adminPath, csrf } from "./state.js";

var msgTimer = null;

// setMsg shows a toast above the bar and leaves it up until it's
// replaced or cleared — used for progress ("Saving…") and errors.
export function setMsg(text) {
    clearTimeout(msgTimer);
    var toast = $("toast");
    if (text) toast.textContent = text;
    toast.classList.toggle("on", !!text);
}

// flash shows a short confirmation toast that dismisses itself.
export function flash(text) {
    setMsg(text);
    msgTimer = setTimeout(function () { setMsg(""); }, 4000);
}

export function api(path, options) {
    options = options || {};
    options.headers = options.headers || {};
    options.headers["X-CSRF-Token"] = csrf;
    options.credentials = "same-origin";
    return fetch(adminPath + "/api" + path, options).then(function (res) {
        var type = res.headers.get("Content-Type") || "";
        if (type.indexOf("application/json") === -1) {
            throw new Error("Your session may have expired — please log in to the admin again.");
        }
        return res.json().then(function (body) {
            if (!res.ok) throw new Error(body.error || "Something went wrong.");
            return body;
        });
    });
}
