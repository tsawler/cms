/* ------------------------------------------------------------------ *
 * Site settings — the wrench menu's "Site settings" entry: menu
 * alignment, site name, and an optional logo, saved through
 * PUT /api/settings. Like menus these have no draft state, so a save
 * is live at once; the dialog then applies the changes to the current
 * page in place so the result shows without a reload.
 * ------------------------------------------------------------------ */

import { state, cfg, pageId, notice, adminPath, mediaEnabled, canPages, isSuperadmin } from "./state.js";
import { api, setMsg, flash } from "./util.js";
import { $ } from "./shell.js";
import { openDialog, sanitizeRichHTML } from "./dialogs.js";
import { initRichEditors } from "./richtext.js";
import { markDirty, hasUnsaved, updateBarButtons } from "./editing.js";
import { updateChip } from "./saving.js";

var ALIGNS = ["left", "center", "right"];

// The settings dialog's tabs. Named where they are used as well as
// listed, so a field cannot drift onto a tab that no longer exists.
var BRAND = "Brand", MENU = "Menu", NOTICE = "Notice bar", SEARCH = "Search";

// The notice bar's colour schemes, mirroring render.NoticeStyles — the
// dialog's own copy, used until the server's list arrives with the
// settings (it always does; this is what keeps the field from being
// empty if it ever doesn't).
var NOTICE_STYLES = [
    { key: "dark", label: "Dark" },
    { key: "accent", label: "Accent (blue)" },
    { key: "warning", label: "Warning (amber)" },
    { key: "alert", label: "Alert (red)" },
    { key: "light", label: "Light" },
];

// The placeholder a bar switched on from this dialog starts with when
// the site has no notice written yet. It matches render's
// noticePlaceholder, and like it is never saved unless someone types
// over it — the editor only submits regions it saw edited.
var NOTICE_PLACEHOLDER = "<p>Write the notice here — a holiday closure, a delivery delay, " +
    "anything the whole site needs to say at once.</p>";

// defaultRobotsTxt is what the robots.txt box offers a site that has
// never stored one, so the superadmin starts from a working file instead
// of a blank box: crawl everything except the admin, which sits behind a
// login and has nothing worth indexing.
//
// No Sitemap: line. The server adds one when the sitemap is on, which
// keeps it following the switch and the site's own host name rather than
// freezing either into stored text — and a line typed here would suppress
// that, being read as the author naming their own.
function defaultRobotsTxt() {
    return "User-agent: *\nDisallow: " + adminPath + "/\n";
}

// applyNotice follows the notice-bar settings into the page being
// edited, without a reload: the bar appears the moment it is switched
// on, changes colour under the style field, and gains or loses its close
// button. It rebuilds the markup render/notice.go emits — the same
// bargain menu.js strikes with navHTML, and the same obligation to keep
// the two in step.
//
// The words are not this function's business. They are a shared region
// like the footer: a bar switched on carries whatever the site already
// says (or a placeholder to type over when it says nothing yet), and
// typing in it marks it dirty and saves it like any other content.
function applyNotice(s) {
    var el = document.querySelector("[data-cms-notice]");
    if (!s.noticeBar) {
        if (!el) return;
        // TinyMCE has to let go before the element does, or the editor
        // instance is left attached to a node no longer in the page.
        var ed = state.mceEditors["site:notice"];
        if (ed) {
            // Read the words back through TinyMCE before it lets go:
            // switching the bar off and on again mid-sentence should
            // not cost the sentence.
            notice.html = ed.getContent();
            ed.remove();
            delete state.mceEditors["site:notice"];
        } else {
            notice.html = el.querySelector(".cms-notice-text").innerHTML;
        }
        el.remove();
        return;
    }
    var style = "cms-notice-" + (s.noticeStyle || NOTICE_STYLES[0].key);
    if (el) {
        el.className = "cms-notice " + style + (s.noticeDismissible ? " cms-notice-closable" : "");
        var close = el.querySelector(".cms-notice-close");
        if (s.noticeDismissible && !close) {
            el.querySelector(".cms-notice-inner").appendChild(noticeCloseButton());
        } else if (!s.noticeDismissible && close) {
            close.remove();
        }
        return;
    }
    el = document.createElement("div");
    el.className = "cms-notice " + style + (s.noticeDismissible ? " cms-notice-closable" : "");
    el.setAttribute("data-cms-notice", "");
    var inner = document.createElement("div");
    inner.className = "cms-notice-inner";
    var text = document.createElement("div");
    text.className = "cms-notice-text";
    text.setAttribute("data-cms-region", "site:notice");
    text.setAttribute("data-cms-kind", "html");
    // What the site already says, if anything: the words the server
    // sent with the editor, or whatever was in the bar before it was
    // last switched off. Only a site with no notice at all starts from
    // the placeholder.
    text.innerHTML = noticeBlank(notice.html) ? NOTICE_PLACEHOLDER : notice.html;
    inner.appendChild(text);
    if (s.noticeDismissible) inner.appendChild(noticeCloseButton());
    el.appendChild(inner);
    // Above everything the template drew, which is where the server puts
    // an injected bar too.
    document.body.insertBefore(el, document.body.firstChild);
    // Words restored from the stash that the server has never seen are
    // edits, and have to go on being edits: without this, switching the
    // bar off and on again would leave them showing in the page and
    // absent from the next save.
    if (notice.html && notice.html !== (cfg.notice || "")) markDirty("site:notice");
    // Mid-edit, the new region needs an editor attached to it; the call
    // skips regions that already have one.
    if (state.editing && window.tinymce) initRichEditors();
}

/* The bar stores a block — the <p> a rich region always is — and the
 * dialog's field edits what is inside it. These carry a notice across
 * that line in both directions.
 *
 * The field keeps bold, italic and links, which is the whole of what a
 * notice bar has ever wanted; anything else a stored notice might
 * carry is flattened to its words by the field's sanitizer, the same
 * way it would be by the server. */
function noticeToRich(html) {
    // Block ends become line breaks: a two-paragraph notice is two
    // lines in the field, and comes back as two lines in the bar.
    var prepared = String(html || "")
        .replace(/<\/(p|div|li|h[1-6]|blockquote)\s*>/gi, "<br>");
    return sanitizeRichHTML(prepared).replace(/(<br\s*\/?>\s*)+$/i, "");
}

function richToNotice(html) {
    var clean = sanitizeRichHTML(html || "");
    // A box holding only formatting — an empty <strong>, a stray break
    // — is an empty notice, and an empty notice is no bar at all.
    if (noticeBlank(clean)) return "";
    return "<p>" + clean + "</p>";
}

function noticeToText(html) {
    if (!html) return "";
    // Block ends and <br> are the line breaks; everything else is
    // markup the text form does not carry. DOMParser rather than
    // innerHTML so nothing in stored content can load or run while we
    // are only reading words out of it.
    var prepared = String(html)
        .replace(/<br\s*\/?>/gi, "\n")
        .replace(/<\/(p|div|li|h[1-6]|blockquote)\s*>/gi, "\n");
    var doc = new DOMParser().parseFromString(prepared, "text/html");
    return (doc.body.textContent || "").replace(/\n{2,}/g, "\n").trim();
}

// currentNotice is what the bar holds right now: unsaved typing if
// there is any, then whatever was stashed or the server sent. The
// placeholder reads as nothing — it is prompting text, not a notice,
// and offering it in the field would make an editor delete it first.
function currentNotice() {
    var ed = state.mceEditors["site:notice"];
    var el = document.querySelector('[data-cms-region="site:notice"]');
    var html = ed ? ed.getContent() : (el ? el.innerHTML : notice.html);
    if (noticeToText(html) === noticeToText(NOTICE_PLACEHOLDER)) return "";
    return html || "";
}

// writeNotice puts the dialog's words into the bar on the page, through
// TinyMCE where it has hold of the region so the editor's own idea of
// the content does not go stale. An emptied notice shows the
// placeholder again: the bar is still on, and there has to be
// something to click into.
function writeNotice(html) {
    var shown = html || NOTICE_PLACEHOLDER;
    var ed = state.mceEditors["site:notice"];
    if (ed) {
        ed.setContent(shown);
        ed.setDirty(false);
        return;
    }
    var el = document.querySelector('[data-cms-region="site:notice"]');
    if (el) el.innerHTML = shown;
}

// noticeBlank mirrors render's own emptiness test: TinyMCE leaves the
// paragraph behind when a region is emptied, so "nothing written here"
// arrives as "<p><br></p>" far more often than as "".
function noticeBlank(html) {
    if (!html) return true;
    if (html.indexOf("<img") !== -1) return false;
    return !html.replace(/<[^>]*>/g, "").replace(/&nbsp;|&#160;|&#xa0;/g, " ").trim();
}

function noticeCloseButton() {
    var b = document.createElement("button");
    b.type = "button";
    b.className = "cms-notice-close";
    b.setAttribute("aria-label", "Dismiss this notice");
    b.innerHTML = "&times;";
    return b;
}

// applySettings updates the current page in place: alignment classes on
// every {{cmsNav}} nav, each {{cmsBrand}} span's logo and text, and the
// favicon link {{cmsHead}} emitted. The alignment class lives on the nav
// element itself, which menu.js's re-renders preserve. A brand cleared
// of both logo and text falls back to the template's data-cms-default
// fallback, matching the server.
function applySettings(s) {
    // The robots instruction, the same way: only the tag the server
    // marked as ours, so a host template's own is left alone. This is
    // the visible half of the switch — the header the server sends with
    // every response is the half that actually keeps crawlers off.
    var robots = document.querySelector("meta[data-cms-robots]");
    if (s.mode === "development") {
        if (!robots) {
            robots = document.createElement("meta");
            robots.name = "robots";
            robots.setAttribute("data-cms-robots", "");
            document.head.appendChild(robots);
        }
        robots.content = "noindex, nofollow";
    } else if (robots) {
        robots.remove();
    }
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
    applyNotice(s);
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

// retitle follows a renamed site into the browser tab. A template that
// builds its <title> with {{cmsSiteName "Fallback"}} shows the stored
// name there, but the title is plain text with no marker around the
// name, so the editor can only find it by value: the name that was
// showing before the save — the stored one, or failing that the brand's
// template fallback, which is the same words in the scaffold — and swap
// it for the new one. A title the name cannot be found in is left alone;
// the next load renders it from the server anyway.
function retitle(prevName, nextName) {
    var brand = document.querySelector(".cms-brand");
    var fallback = brand ? brand.dataset.cmsDefault || "" : "";
    var from = prevName || fallback;
    var to = nextName || fallback;
    if (!from || from === to) return;
    var at = document.title.lastIndexOf(from);
    if (at === -1) return;
    document.title = document.title.slice(0, at) + to + document.title.slice(at + from.length);
}

export function openSiteSettings() {
    if (!canPages) return; // the save would 403; the menu entry is hidden too
    api("/settings").then(function (s) {
        // The notice as the field edits it: unsaved typing in the bar
        // wins over the stored copy, so opening this dialog can never
        // quietly discard a sentence someone is in the middle of.
        var noticeRich = noticeToRich(currentNotice());

        // Four groups, each a tab: who the site is, how it is navigated,
        // what it is announcing today, and how it meets search engines.
        // The panel had grown past a screenful as one list, which put
        // Save below the fold and made the last field look like the end
        // of an unrelated form.
        var fields = [
            { id: "siteName", label: "Site name", type: "text", value: s.siteName, tab: BRAND,
                span: true, placeholder: "Shown where the template places the brand" },
        ];
        if (mediaEnabled) {
            fields.push({ id: "logo", label: "Logo", type: "image", value: s.logoUrl, tab: BRAND });
            // The favicon wants the file as uploaded, not the lossy web
            // rendition — a browser tab renders it at 16px and the
            // library's WebP re-encode buys nothing there.
            fields.push({ id: "favicon", label: "Favicon", type: "image", tab: BRAND,
                value: s.faviconUrl, prefer: "original" });
        }
        fields.push({ id: "menuAlign", label: "Menu alignment", type: "select", value: s.menuAlign,
            tab: MENU, span: true,
            options: [
                { value: "", label: "Theme default" },
                { value: "left", label: "Left" },
                { value: "center", label: "Center" },
                { value: "right", label: "Right" },
            ] });
        fields.push({ id: "loginInNav", label: "Show a “Log in” link in the menu for logged-out visitors",
            type: "check", value: s.loginInNav, tab: MENU, span: true });
        // The notice bar. Only its switch and its look are settings —
        // the words are a shared region, written in the bar itself, so
        // that they translate and publish like the footer does. The note
        // is where that is said out loud, because a dialog that offers
        // to show a notice and never asks for one needs to explain
        // where the typing happens.
        fields.push({ id: "noticeBar", label: "Show a notice bar at the top of every page",
            type: "check", value: s.noticeBar, tab: NOTICE, span: true });
        // The wording, right here rather than only in the bar. Without
        // it, switching the bar on left the placeholder standing in the
        // page — one stray Save away from being the notice the site
        // actually published. Bold, italic and links come along, since
        // a notice that cannot link to the page explaining it is half a
        // notice.
        fields.push({ id: "noticeText", label: "What it says", type: "rich",
            tab: NOTICE, span: true, value: noticeRich,
            placeholder: "Closed Monday 25 August \u2014 orders ship Tuesday." });
        fields.push({ id: "noticeStyle", label: "Notice bar colour", type: "select",
            value: s.noticeStyle, tab: NOTICE, span: true,
            options: (s.noticeStyles || NOTICE_STYLES).map(function (n) {
                return { value: n.key, label: n.label };
            }) });
        fields.push({ id: "noticeDismissible", label: "Let visitors close the notice",
            type: "check", value: s.noticeDismissible, tab: NOTICE, span: true });
        fields.push({ type: "note", span: true, tab: NOTICE, text: function (v) {
            if (v.noticeBar !== "1") {
                return "Off — no bar on any page. Switching it on adds a thin strip above " +
                    "everything else, for the one thing the whole site has to say at once: a " +
                    "holiday closure, a delivery delay.";
            }
            var dismiss = v.noticeDismissible === "1"
                ? " Visitors can close it, and it stays closed for them until you change the " +
                  "wording — a new notice shows again."
                : " There is no close button: the bar stays until you switch it off here.";
            return "The wording is content, not a setting: it is saved as a draft here and goes " +
                "live with the next Publish, in the language you are editing. A bar with nothing " +
                "written in it shows to nobody. You can also write it in the bar on the page " +
                "itself — it is a shared region like the footer." + dismiss;
        } });
        // Whether the site may be indexed is a superadmin's switch. The
        // note under it spells out what the current choice does, because
        // "development" and "production" name a state, not a
        // consequence, and the consequence is the whole point.
        if (isSuperadmin) {
            fields.push({ id: "mode", label: "Site mode", type: "select", value: s.mode,
                tab: SEARCH, span: true,
                options: [
                    { value: "development", label: "Development — keep out of search engines" },
                    { value: "production", label: "Production — live and findable" },
                ] });
            fields.push({ type: "note", span: true, tab: SEARCH, text: function (v) {
                return v.mode === "development"
                    ? "Search engines are asked not to index the site. Anyone with the address can " +
                      "still read it — this hides the site from search, it does not make it private."
                    : "The site is open to search engines. It can take days or weeks for pages to " +
                      "appear in results.";
            } });
            // A sitemap of every published, public page. Off leaves
            // /sitemap.xml to the host app, the same bargain the
            // robots.txt box strikes.
            fields.push({ id: "sitemap", label: "Publish a sitemap at /sitemap.xml",
                type: "check", value: s.sitemap, tab: SEARCH, span: true });
            fields.push({ type: "note", span: true, tab: SEARCH, text: function (v) {
                if (v.sitemap !== "1") {
                    return "Off — the CMS serves nothing at /sitemap.xml, leaving the address to " +
                        "the app hosting it.";
                }
                return v.mode === "development"
                    ? "Listed once the site is in production. A site in development publishes no " +
                      "sitemap — it is asking not to be crawled."
                    : "Every published, public page is listed, in every language, and the address " +
                      "is added to the robots.txt below.";
            } });
            // The live site's robots.txt, in the same hands as the mode:
            // both decide what crawlers are told. Left empty the CMS
            // serves nothing at that address, so an app already serving
            // its own file keeps doing so. The placeholder shows this
            // site's own sitemap address rather than a made-up one.
            // Nothing stored yet shows the default rather than an empty
            // box. Whether one was stored decides what the note says
            // below, so it is read once here, before the dialog's own
            // copy of the value starts changing.
            var storedRobots = !!(s.robotsTxt || "").trim();
            fields.push({ id: "robotsTxt", label: "robots.txt", type: "textarea", mono: true,
                tab: SEARCH, span: true,
                rows: 6, value: storedRobots ? s.robotsTxt : defaultRobotsTxt(),
                placeholder: "User-agent: *\nDisallow: /private\n\nSitemap: " +
                    window.location.origin + "/sitemap.xml" });
            fields.push({ type: "note", span: true, tab: SEARCH, text: function (v) {
                if (v.mode === "development") {
                    return "Served once the site is in production. While it is in development the " +
                        "CMS serves its own “Disallow: /” instead, so this file cannot invite " +
                        "crawlers into an unfinished site.";
                }
                if (!(v.robotsTxt || "").trim()) {
                    return "Empty — the CMS serves nothing at /robots.txt, leaving the address to " +
                        "the app hosting it.";
                }
                // The difference matters: on a site with nothing stored,
                // saving the dialog is what takes /robots.txt over from
                // the app hosting it, and the box was filled in by us
                // rather than by anyone here.
                var sitemapLine = v.sitemap === "1"
                    ? ", with a Sitemap: line added unless you write your own"
                    : "";
                if (!storedRobots) {
                    return "A starting point — nothing is stored yet. Saving serves this at " +
                        "/robots.txt" + sitemapLine + ", taking that address over from the app " +
                        "hosting the site; clearing the box hands it back.";
                }
                return "Served at /robots.txt" + sitemapLine +
                    ". Crawlers may cache it for a day or so before they notice a change.";
            } });
        }
        // Search is superadmin-only, and a tab with nothing behind it
        // would be a dead end — so the bar is built from what this user
        // can actually see.
        var tabs = [BRAND, MENU, NOTICE];
        if (isSuperadmin) tabs.push(SEARCH);
        openDialog({
            message: "Site settings",
            okLabel: "Save",
            wide: true,
            tabs: tabs,
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
                // save doesn't wipe them. The mode and robots.txt fields
                // are absent for everyone but superadmins, and carry
                // through the same way — the server ignores them from
                // anyone else in any case.
                siteCss: s.siteCss || "",
                siteJs: s.siteJs || "",
                mode: values.mode !== undefined ? values.mode : (s.mode || ""),
                robotsTxt: values.robotsTxt !== undefined ? values.robotsTxt : (s.robotsTxt || ""),
                sitemap: values.sitemap !== undefined ? values.sitemap === "1" : !!s.sitemap,
                noticeBar: values.noticeBar === "1",
                noticeStyle: values.noticeStyle,
                noticeDismissible: values.noticeDismissible === "1",
            };
            // The wording is content, so it does not ride the settings
            // PUT: it goes to the regions endpoint as the shared region
            // it is, and lands as a draft that Publish makes live. Only
            // when it actually changed — leaving it alone is what keeps
            // a notice that carries a link or bold text from being
            // flattened by a visit to this dialog.
            // Compare what the field holds with what it was given,
            // both sanitized, so a save that did not touch the wording
            // writes nothing — which is what keeps a visit to this
            // dialog from rewriting a notice it merely displayed.
            var typed = sanitizeRichHTML(values.noticeText || "");
            var changed = typed !== noticeRich;
            var nextNotice = changed ? richToNotice(typed) : notice.html;
            if (changed) {
                // Before applySettings, so a bar being switched on in
                // this same save is inserted carrying these words
                // rather than the placeholder.
                notice.html = nextNotice;
            }
            api("/settings", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(next),
            }).then(function () {
                applySettings(next);
                retitle(s.siteName, next.siteName);
                if (!changed) {
                    flash("Site settings saved.");
                    return;
                }
                // Again, after applySettings: switching the bar off in
                // this same save stashes the words it found in the page,
                // which are the ones being replaced.
                notice.html = nextNotice;
                writeNotice(nextNotice);
                return api("/pages/" + pageId + "/regions", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({
                        locale: cfg.locale,
                        regions: { "site:notice": nextNotice },
                    }),
                }).then(function () {
                    // The region is now the server's, so it is no longer
                    // an unsaved edit — but it is an unpublished one,
                    // and the chip has to say so.
                    delete state.dirty["site:notice"];
                    if (!hasUnsaved()) $("save").disabled = true;
                    if (state.pageStatus === "published") state.hasUnpublished = true;
                    updateChip();
                    updateBarButtons();
                    flash("Saved — publish to put the notice live");
                });
            }).catch(function (err) { setMsg(err.message); });
        });
    }).catch(function (err) { setMsg(err.message); });
}
