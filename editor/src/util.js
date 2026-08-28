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

/* cssColorToHex resolves any colour the page can produce to the #rrggbb
 * a colour input speaks. "" for a colour that is absent, transparent, or
 * not a colour at all.
 *
 * Parsing is not enough, and the reason is Tailwind v4: its palette is
 * authored in oklch(), and getComputedStyle hands back the space the
 * colour was written in rather than converting — so a class-derived red
 * reads as "oklch(0.577 0.245 27.325)", which no rgb() regex will ever
 * match. Painting one pixel and reading it back is the conversion the
 * platform actually offers; anything else means shipping a colour-space
 * implementation and revisiting it every time CSS gains a notation.
 *
 * Out-of-gamut colours land on their clamped sRGB neighbour, which is
 * the right answer here: sRGB is what an <input type=color> can express,
 * so it is what the picker must open on.
 *
 * An unparseable value leaves fillStyle untouched, which would silently
 * report whatever was there before — hence two probes from different
 * starting colours, agreeing only when the value actually took. */
var hexProbe = null;
export function cssColorToHex(v) {
    if (!v) return "";
    if (/^#[0-9a-fA-F]{6}$/.test(v)) return v.toLowerCase();
    try {
        if (!hexProbe) {
            hexProbe = document.createElement("canvas");
            hexProbe.width = hexProbe.height = 1;
        }
        var c = hexProbe.getContext("2d", { willReadFrequently: true });
        c.fillStyle = "#000";
        c.fillStyle = v;
        var black = c.fillStyle;
        c.fillStyle = "#fff";
        c.fillStyle = v;
        if (c.fillStyle !== black) return ""; // rejected: not a colour
        c.clearRect(0, 0, 1, 1);
        c.fillRect(0, 0, 1, 1);
        var d = c.getImageData(0, 0, 1, 1).data;
        if (d[3] === 0) return ""; // fully transparent is "no colour"
        var h = function (n) { return ("0" + n.toString(16)).slice(-2); };
        return "#" + h(d[0]) + h(d[1]) + h(d[2]);
    } catch (e) {
        return ""; // no canvas: the picker opens on its default
    }
}
