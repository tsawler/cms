/* ------------------------------------------------------------------ *
 * Site settings — the wrench menu's "Site settings" entry: menu
 * alignment, site name, and an optional logo, saved through
 * PUT /api/settings. Like menus these have no draft state, so a save
 * is live at once; the dialog then applies the changes to the current
 * page in place so the result shows without a reload.
 * ------------------------------------------------------------------ */

import { mediaEnabled } from "./state.js";
import { api, setMsg, flash } from "./util.js";
import { openDialog } from "./dialogs.js";

var ALIGNS = ["left", "center", "right"];

// applySettings updates the current page in place: alignment classes on
// every {{cmsNav}} nav, and each {{cmsBrand}} span's logo and text. The
// alignment class lives on the nav element itself, which menu.js's
// re-renders preserve. A brand cleared of both logo and text falls back
// to the template's data-cms-default fallback, matching the server.
function applySettings(s) {
    document.querySelectorAll("nav[data-cms-menu]").forEach(function (nav) {
        ALIGNS.forEach(function (a) { nav.classList.remove("cms-nav-" + a); });
        if (ALIGNS.indexOf(s.menuAlign) !== -1) nav.classList.add("cms-nav-" + s.menuAlign);
    });
    document.querySelectorAll(".cms-brand").forEach(function (el) {
        el.textContent = "";
        var name = s.siteName || (s.logoUrl ? "" : el.dataset.cmsDefault || "");
        if (s.logoUrl) {
            var img = document.createElement("img");
            img.className = "cms-brand-logo";
            img.src = s.logoUrl;
            img.alt = name || el.dataset.cmsDefault || "";
            el.appendChild(img);
        }
        if (name) {
            var txt = document.createElement("span");
            txt.className = "cms-brand-text";
            txt.textContent = name;
            el.appendChild(txt);
        }
    });
}

export function openSiteSettings() {
    api("/settings").then(function (s) {
        var fields = [
            { id: "siteName", label: "Site name", type: "text", value: s.siteName,
                placeholder: "Shown where the template places the brand" },
        ];
        if (mediaEnabled) {
            fields.push({ id: "logo", label: "Logo", type: "image", value: s.logoUrl });
        }
        fields.push({ id: "menuAlign", label: "Menu alignment", type: "select", value: s.menuAlign,
            options: [
                { value: "", label: "Theme default" },
                { value: "left", label: "Left" },
                { value: "center", label: "Center" },
                { value: "right", label: "Right" },
            ] });
        fields.push({ id: "loginInNav", label: "Show a “Log in” link in the menu for logged-out visitors",
            type: "check", value: s.loginInNav });
        openDialog({
            message: "Site settings",
            okLabel: "Save",
            fields: fields,
        }).then(function (values) {
            if (!values) return;
            // The logo field is absent when media is disabled; keep the
            // stored URL rather than wiping it.
            var next = {
                menuAlign: values.menuAlign,
                siteName: values.siteName.trim(),
                logoUrl: values.logo !== undefined ? values.logo : (s.logoUrl || ""),
                loginInNav: values.loginInNav === "1",
                // Site-wide CSS/JS has its own editor (wrench → Site
                // CSS & JS); carry the stored values through so this
                // save doesn't wipe them.
                siteCss: s.siteCss || "",
                siteJs: s.siteJs || "",
            };
            api("/settings", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(next),
            }).then(function () {
                applySettings(next);
                flash("Site settings saved.");
            }).catch(function (err) { setMsg(err.message); });
        });
    }).catch(function (err) { setMsg(err.message); });
}
