/* ------------------------------------------------------------------ *
 * Admin tools menu — a wrench button at the left side of the site's
 * top nav, shown whenever the editor is present (i.e. the user is
 * logged in). It offers the everyday admin actions without a trip to
 * the admin area: add page, add news item, add blog post, site
 * settings, site-wide CSS/JS (admins), open the admin panel, and log
 * out.
 *
 * The button lives in the light DOM as an extra <li> at the end of the
 * first {{cmsNav}} nav's item list, so it spaces like one more menu
 * item (pages without a nav pin it to the top-left corner instead);
 * light.css styles it. Item addressing in menu.js skips it (it isn't a
 * .cms-nav-item), and renderNav calls mountAdminTools after each nav
 * re-render to put it back.
 * ------------------------------------------------------------------ */

import { adminPath, csrf, isAdmin, pageTemplates, postsEnabled } from "./state.js";
import { newPageDialog, newPostDialog } from "./snippets.js";
import { openSiteSettings } from "./settings.js";
import { openSiteCode } from "./pagecode.js";

var WRENCH =
    '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M22.7 19l-9.1-9.1c.9-2.3.4-5-1.5-6.9-2-2-5-2.4-7.4-1.3L9 6 6 9 1.6 4.7C.4 7.1.9 10.1 2.9 12.1c1.9 1.9 4.6 2.4 6.9 1.5l9.1 9.1c.4.4 1 .4 1.4 0l2.3-2.3c.5-.4.5-1.1.1-1.4z"/></svg>';

// Logging out is a POST (CSRF-protected), so it goes through a real
// form submission — the server clears the session and redirects.
function logout() {
    var form = document.createElement("form");
    form.method = "post";
    form.action = adminPath + "/logout";
    var token = document.createElement("input");
    token.type = "hidden";
    token.name = "csrf_token";
    token.value = csrf;
    form.appendChild(token);
    document.body.appendChild(form);
    form.submit();
}

function item(tools, label, onClick) {
    var b = document.createElement("button");
    b.type = "button";
    b.textContent = label;
    b.addEventListener("click", function () {
        tools.classList.remove("cms-open");
        onClick();
    });
    return b;
}

export function initAdminTools() {
    var tools = document.createElement("div");
    tools.id = "cms-admin-tools";

    var btn = document.createElement("button");
    btn.type = "button";
    btn.id = "cms-admin-tools-btn";
    btn.title = "Admin tools";
    btn.setAttribute("aria-label", "Admin tools");
    btn.setAttribute("aria-haspopup", "true");
    btn.setAttribute("aria-expanded", "false");
    btn.innerHTML = WRENCH;
    tools.appendChild(btn);

    var menu = document.createElement("div");
    menu.className = "cms-admin-menu";
    if (pageTemplates.length) {
        menu.appendChild(item(tools, "Add page", newPageDialog));
    }
    if (postsEnabled) {
        menu.appendChild(item(tools, "Add news item", function () { newPostDialog("news"); }));
        menu.appendChild(item(tools, "Add blog post", function () { newPostDialog("blog"); }));
    }
    if (menu.children.length) menu.appendChild(document.createElement("hr"));
    menu.appendChild(item(tools, "Site settings", openSiteSettings));
    // Site-wide code is written raw into every page, so the editor is
    // admin-only — matching the server, which ignores these fields in
    // a settings save from anyone else.
    if (isAdmin) menu.appendChild(item(tools, "Site CSS & JS", openSiteCode));
    var admin = document.createElement("a");
    admin.href = adminPath + "/";
    admin.textContent = "Admin panel";
    menu.appendChild(admin);
    menu.appendChild(item(tools, "Log out", logout));
    tools.appendChild(menu);

    btn.addEventListener("click", function (e) {
        e.stopPropagation(); // the document click below would close it again
        var open = !tools.classList.contains("cms-open");
        // Near the right edge the dropdown right-aligns to the button so
        // it can't run off-screen.
        tools.classList.toggle("cms-align-right",
            tools.getBoundingClientRect().left + 200 > window.innerWidth);
        tools.classList.toggle("cms-open", open);
        btn.setAttribute("aria-expanded", open ? "true" : "false");
    });
    var close = function () {
        tools.classList.remove("cms-open");
        btn.setAttribute("aria-expanded", "false");
    };
    document.addEventListener("click", close);
    document.addEventListener("keydown", function (e) {
        if (e.key === "Escape") close();
    });

    holder = document.createElement("li");
    holder.className = "cms-admin-li";
    holder.appendChild(tools);
    mountAdminTools();
}

var holder = null; // the <li> wrapping the tools, once built

// mountAdminTools (re)appends the tools to the end of the nav's item
// list — appendChild simply moves the node, so calling it after every
// nav re-render is safe. Without a nav it falls back to a fixed
// top-left pill, once.
export function mountAdminTools() {
    if (!holder) return;
    var list = document.querySelector("nav[data-cms-menu] .cms-nav-list");
    if (list) {
        list.appendChild(holder);
    } else if (!holder.parentNode) {
        holder.firstChild.classList.add("cms-fixed");
        document.body.appendChild(holder.firstChild);
        holder = null;
    }
}
