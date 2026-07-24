/* ------------------------------------------------------------------ *
 * Locales — the edit bar's language switcher (flip to another locale's
 * render of the same page and edit it in place) and the "remove this
 * translation" action that reverts the current locale's content to the
 * default language.
 * ------------------------------------------------------------------ */

import { cfg, state, locales, defaultLocale, pageId } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg } from "./util.js";
import { cmsConfirm } from "./dialogs.js";
import { hasUnsaved } from "./editing.js";

// localeHref is the current page's URL in another locale: the default
// locale is unprefixed, others live under /<code>/.
function localeHref(code) {
    var slug = cfg.slug || "";
    if (code === defaultLocale) return slug ? "/" + slug : "/";
    return "/" + code + (slug ? "/" + slug : "");
}

export function initLocales() {
    var locs = $("locs");
    if (locales.length < 2) {
        locs.hidden = true;
        return;
    }
    locales.forEach(function (code) {
        var b = document.createElement("button");
        b.type = "button";
        b.className = "loc" + (code === cfg.locale ? " on" : "");
        b.textContent = code.toUpperCase();
        b.title = code === cfg.locale ? "Currently editing this language"
            : "Switch to this language";
        b.addEventListener("click", function () {
            if (code === cfg.locale) return;
            if (!hasUnsaved()) {
                window.location.href = localeHref(code);
                return;
            }
            cmsConfirm("Switch language? Your unsaved changes will be lost.", "Switch")
                .then(function (ok) { if (ok) window.location.href = localeHref(code); });
        });
        locs.appendChild(b);
    });
    // Shown only while editing (setEditing): outside edit mode the
    // site's own navigation works normally for switching languages.
    locs.hidden = true;

    // Remove-this-translation lives in the ⋯ menu, non-default locales
    // only (visibility is handled with the other menu items).
    $("revert-locale").addEventListener("click", function () {
        cmsConfirm("Remove the " + cfg.locale.toUpperCase() + " translation of this page? " +
            "It goes back to showing the default language. Like other edits, " +
            "the removal is applied to the live site when you publish.", "Remove", true)
            .then(function (ok) {
                if (!ok) return;
                setMsg("Removing translation…");
                api("/pages/" + pageId + "/revert-locale", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ locale: cfg.locale }),
                }).then(function () {
                    window.location.reload();
                }).catch(function (err) { setMsg(err.message); });
            });
    });
}
