/* Small shared helpers: the status message in the bar, and the JSON API
 * wrapper every request goes through. */

import { $ } from "./shell.js";
import { adminPath, csrf } from "./state.js";

var msgTimer = null;

export function setMsg(text) {
    clearTimeout(msgTimer);
    $("msg").textContent = text || "";
    $("msg").hidden = !text;
}

// flash shows a short confirmation that clears itself, so the bar
// doesn't hold on to stale status text (and stale width).
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
