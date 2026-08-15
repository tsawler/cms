/* ------------------------------------------------------------------ *
 * CSS & JS panel (admin-only): a wide two-tab code editor.
 * Highlighting uses the classic trick of a transparent-text textarea
 * stacked over a <pre> that holds the colored tokens.
 *
 * One panel, two scopes: "page" (the bar's more menu) edits this
 * page's code via /pages/:id/code; "site" (the wrench menu) edits the
 * site-wide CSS/JS stored in the settings via /settings. Each field
 * takes plain code or full markup — <style>, <link>, and <script>
 * tags are written into the page as-is.
 * ------------------------------------------------------------------ */

import { isAdmin, pageId } from "./state.js";
import { $ } from "./shell.js";
import { api, setMsg, flash } from "./util.js";
import { cmsConfirm } from "./dialogs.js";
import { hasUnsaved } from "./editing.js";

var codeState = { css: "", js: "",
    tab: "css", loaded: false, dirty: false, scope: "page" };

function escHTML(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// highlight tokenizes src with one alternation regex per language;
// unmatched stretches pass through unstyled. Cosmetic only — nothing
// downstream depends on it being a real parser.
function highlight(src, lang) {
    var re, cls;
    if (lang === "css") {
        re = /(\/\*[\s\S]*?\*\/)|("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')|(@[\w-]+)|([-\w]+(?=\s*:))|(#[0-9a-fA-F]{3,8}\b|\b\d+(?:\.\d+)?(?:px|em|rem|%|vh|vw|fr|s|ms|deg)?\b)/g;
        cls = ["tok-c", "tok-s", "tok-k", "tok-p", "tok-n"];
    } else {
        re = /(\/\/[^\n]*|\/\*[\s\S]*?\*\/)|("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|`(?:[^`\\]|\\.)*`)|(\b(?:async|await|break|case|catch|class|const|continue|debugger|default|delete|do|else|export|extends|finally|for|function|if|import|in|instanceof|let|new|of|return|static|super|switch|this|throw|try|typeof|var|void|while|with|yield|true|false|null|undefined)\b)|(\b\d+(?:\.\d+)?\b)/g;
        cls = ["tok-c", "tok-s", "tok-k", "tok-n"];
    }
    var out = "";
    var last = 0;
    var m;
    while ((m = re.exec(src)) !== null) {
        out += escHTML(src.slice(last, m.index));
        var cl = "";
        for (var i = 1; i < m.length; i++) {
            if (m[i] !== undefined) { cl = cls[i - 1]; break; }
        }
        out += '<span class="' + cl + '">' + escHTML(m[0]) + "</span>";
        last = m.index + m[0].length;
    }
    // Trailing newline keeps the pre as tall as the textarea's last line.
    return out + escHTML(src.slice(last)) + "\n";
}

function renderCode() {
    $("code-hl").innerHTML = highlight($("code-ta").value, codeState.tab);
}

function stashCode() {
    codeState[codeState.tab] = $("code-ta").value;
}

function setCodeTab(tab) {
    stashCode();
    codeState.tab = tab;
    $("code-tab-css").classList.toggle("on", tab === "css");
    $("code-tab-js").classList.toggle("on", tab === "js");
    var ta = $("code-ta");
    ta.value = codeState[tab];
    ta.scrollTop = 0;
    ta.scrollLeft = 0;
    renderCode();
    $("code-hl").scrollTop = 0;
    $("code-hl").scrollLeft = 0;
    ta.focus();
}

function openCodePanel(scope) {
    // The two scopes share the panel and its cached state; switching
    // drops the other scope's cache so its content is refetched. Only
    // saved content can be cached here — the panel never closes dirty.
    if (codeState.scope !== scope) {
        codeState.scope = scope;
        codeState.loaded = false;
        codeState.dirty = false;
    }
    var site = scope === "site";
    $("code-title").textContent = site ? "Site CSS & JS" : "Page CSS & JS";
    $("code-hint").textContent = (site
        ? "Every public page. "
        : "This page only. ") +
        "Plain code works, and so do full <style>, <link>, and <script> " +
        "tags — they go into the page as-is. CSS lands in <head>, " +
        "JavaScript before </body>.";
    $("code-overlay").classList.add("on");
    $("code-panel").classList.add("on");
    if (codeState.loaded) {
        setCodeTab(codeState.tab);
        return;
    }
    $("code-ta").value = "";
    renderCode();
    var load = site
        ? api("/settings").then(function (s) {
            return { css: s.siteCss, js: s.siteJs };
        })
        : api("/pages/" + pageId + "/code", { method: "GET" });
    load.then(function (body) {
        codeState.css = body.css || "";
        codeState.js = body.js || "";
        codeState.loaded = true;
        codeState.dirty = false;
        // Show whichever tab has content first; CSS wins a tie.
        codeState.tab = !codeState.css && codeState.js ? "js" : "css";
        // Sync the input before setCodeTab: its stash of the
        // still-empty field must not wipe the fetched values.
        $("code-ta").value = codeState[codeState.tab];
        setCodeTab(codeState.tab);
    }).catch(function (err) {
        closeCodePanel();
        setMsg(err.message);
    });
}

function closeCodePanel() {
    $("code-overlay").classList.remove("on");
    $("code-panel").classList.remove("on");
}

// dismissCodePanel is the "close without saving" path: confirm when
// there are unsaved code edits, and drop them so the next open
// refetches clean state.
export function dismissCodePanel() {
    stashCode();
    if (!codeState.dirty) {
        closeCodePanel();
        return;
    }
    cmsConfirm("Discard your unsaved CSS and JavaScript changes?", "Discard changes", true)
        .then(function (yes) {
            if (!yes) return;
            codeState.loaded = false;
            codeState.dirty = false;
            closeCodePanel();
        });
}

// openSiteCode is the wrench menu's "Site CSS & JS" entry.
export function openSiteCode() {
    openCodePanel("site");
}

export function initPageCode() {
    $("code-btn").hidden = !isAdmin;

    $("code-btn").addEventListener("click", function () { openCodePanel("page"); });
    $("code-tab-css").addEventListener("click", function () { setCodeTab("css"); });
    $("code-tab-js").addEventListener("click", function () { setCodeTab("js"); });
    $("code-close").addEventListener("click", dismissCodePanel);
    $("code-cancel").addEventListener("click", dismissCodePanel);
    $("code-overlay").addEventListener("click", dismissCodePanel);

    $("code-ta").addEventListener("input", function () {
        codeState.dirty = true;
        stashCode();
        renderCode();
    });
    $("code-ta").addEventListener("scroll", function () {
        $("code-hl").scrollTop = $("code-ta").scrollTop;
        $("code-hl").scrollLeft = $("code-ta").scrollLeft;
    });
    // Tab indents instead of leaving the field.
    $("code-ta").addEventListener("keydown", function (e) {
        if (e.key !== "Tab") return;
        e.preventDefault();
        var ta = $("code-ta");
        var start = ta.selectionStart;
        ta.setRangeText("  ", start, ta.selectionEnd, "end");
        codeState.dirty = true;
        stashCode();
        renderCode();
    });

    $("code-save").addEventListener("click", function () {
        stashCode();
        setMsg("Saving…");
        // Site scope: the settings PUT carries the full settings object,
        // so re-fetch the rest at save time — a stale copy would clobber
        // settings-dialog edits made while this panel sat open.
        //
        // The superadmin-only fields (mode, robotsTxt) are deliberately
        // left out rather than echoed back: the server carries an absent
        // one through unchanged, and sending the mode from here is how a
        // save of unrelated CSS once flipped a development site live.
        var put = codeState.scope === "site"
            ? api("/settings").then(function (s) {
                return api("/settings", {
                    method: "PUT",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ menuAlign: s.menuAlign,
                        siteName: s.siteName, logoUrl: s.logoUrl,
                        faviconUrl: s.faviconUrl, loginInNav: s.loginInNav,
                        siteCss: codeState.css, siteJs: codeState.js }),
                });
            })
            : api("/pages/" + pageId + "/code", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ css: codeState.css, js: codeState.js }),
            });
        put.then(function () {
            codeState.dirty = false;
            // Drop the cache so the next open refetches saved state.
            codeState.loaded = false;
            closeCodePanel();
            // The CSS/JS only take effect on a fresh render. Reload for
            // it unless that would throw away unsaved content edits.
            if (hasUnsaved()) {
                flash("Saved — the CSS/JS will apply on the next page load");
            } else {
                window.location.reload();
            }
        }).catch(function (err) { setMsg(err.message); });
    });
}
