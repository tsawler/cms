/* ------------------------------------------------------------------ *
 * Site settings — the wrench menu's "Site settings" entry: menu
 * alignment, site name, and an optional logo, saved through
 * PUT /api/settings. Like menus these have no draft state, so a save
 * is live at once; the dialog then applies the changes to the current
 * page in place so the result shows without a reload.
 * ------------------------------------------------------------------ */

import { mediaEnabled, canPages } from "./state.js";
import { api, setMsg, flash } from "./util.js";
import { openDialog } from "./dialogs.js";

var ALIGNS = ["left", "center", "right"];

// applySettings updates the current page in place: alignment classes on
// every {{cmsNav}} nav, each {{cmsBrand}} span's logo and text, and the
// favicon link {{cmsHead}} emitted. The alignment class lives on the nav
// element itself, which menu.js's re-renders preserve. A brand cleared
// of both logo and text falls back to the template's data-cms-default
// fallback, matching the server.
function applySettings(s) {
    // Only the CMS's own link is touched — clearing the favicon leaves a
    // host template's icon alone, exactly as the server would. Browsers
    // cache favicons hard, so the tab may not repaint until a reload.
    var icon = document.querySelector("link[data-cms-favicon]");
    if (s.faviconUrl) {
        if (!icon) {
            icon = document.createElement("link");
            icon.rel = "icon";
            icon.setAttribute("data-cms-favicon", "");
            document.head.appendChild(icon);
        }
        icon.href = s.faviconUrl;
    } else if (icon) {
        icon.remove();
    }
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
    if (!canPages) return; // the save would 403; the menu entry is hidden too
    api("/settings").then(function (s) {
        var fields = [
            { id: "siteName", label: "Site name", type: "text", value: s.siteName,
                placeholder: "Shown where the template places the brand" },
        ];
        if (mediaEnabled) {
            fields.push({ id: "logo", label: "Logo", type: "image", value: s.logoUrl });
            // The favicon wants the file as uploaded, not the lossy web
            // rendition — a browser tab renders it at 16px and the
            // library's WebP re-encode buys nothing there.
            fields.push({ id: "favicon", label: "Favicon", type: "image",
                value: s.faviconUrl, prefer: "original" });
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
            // The logo and favicon fields are absent when media is
            // disabled; keep the stored URLs rather than wiping them.
            var next = {
                menuAlign: values.menuAlign,
                siteName: values.siteName.trim(),
                logoUrl: values.logo !== undefined ? values.logo : (s.logoUrl || ""),
                faviconUrl: values.favicon !== undefined ? values.favicon : (s.faviconUrl || ""),
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
