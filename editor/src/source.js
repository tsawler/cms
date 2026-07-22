/* ------------------------------------------------------------------ *
 * HTML source modal — view and edit the raw markup of a snippet block
 * or a section's content. Reuses the code panel's chrome (highlighted
 * textarea over a <pre>) with an HTML tokenizer, plus a conservative
 * pretty-printer so serialized one-line markup is readable.
 * ------------------------------------------------------------------ */

import { isAdmin } from "./state.js";
import { $ } from "./shell.js";
import { cmsConfirm } from "./dialogs.js";

var BLOCK_TAGS = {};
("address article aside blockquote details dd div dl dt fieldset figcaption " +
    "figure footer form h1 h2 h3 h4 h5 h6 header hr iframe li main nav ol p " +
    "section table tbody td tfoot th thead tr ul video").split(" ")
    .forEach(function (t) { BLOCK_TAGS[t] = true; });
var VOID_TAGS = { area: 1, base: 1, br: 1, col: 1, embed: 1, hr: 1, img: 1,
    input: 1, link: 1, meta: 1, source: 1, track: 1, wbr: 1 };
// Whitespace inside these is meaningful — pass their content through
// untouched and never re-indent it.
var RAW_TAGS = { pre: 1, textarea: 1, script: 1, style: 1 };

// One alternation for comments and tags; attribute values may contain
// ">" so quoted stretches are consumed as units.
var TOKEN_RE = /<!--[\s\S]*?-->|<\/?[a-zA-Z][a-zA-Z0-9-]*(?:[^>"']|"[^"]*"|'[^']*')*>/g;

function tokenize(src) {
    var tokens = [];
    var last = 0;
    var m;
    TOKEN_RE.lastIndex = 0;
    while ((m = TOKEN_RE.exec(src)) !== null) {
        if (m.index > last) tokens.push({ text: src.slice(last, m.index) });
        var t = { text: m[0] };
        var tm = /^<(\/?)([a-zA-Z][a-zA-Z0-9-]*)/.exec(m[0]);
        if (tm) {
            t.tag = tm[2].toLowerCase();
            t.close = tm[1] === "/";
        } else {
            t.comment = true;
        }
        tokens.push(t);
        last = m.index + m[0].length;
    }
    if (last < src.length) tokens.push({ text: src.slice(last) });
    return tokens;
}

function isBoundary(t) {
    return !!t && (t.comment === true || (t.tag !== undefined &&
        (BLOCK_TAGS[t.tag] === true || RAW_TAGS[t.tag] === 1)));
}

// formatHTML re-indents markup by breaking lines only at block-element
// boundaries, where whitespace is insignificant — text and inline tags
// keep their exact spacing so the rendered page cannot change. A block
// whose entire content is short inline markup stays on one line.
export function formatHTML(src) {
    var tokens = tokenize(src);

    // inlineSpan: for the block-open at index i, the index of its
    // matching close when everything between is short inline content;
    // -1 when the block deserves its own lines.
    function inlineSpan(i) {
        var t = tokens[i];
        if (VOID_TAGS[t.tag] || /\/>$/.test(t.text)) return -1;
        var len = t.text.length;
        var nest = 0;
        for (var j = i + 1; j < tokens.length; j++) {
            var u = tokens[j];
            len += u.text.length;
            if (len > 110) return -1;
            if (u.tag === t.tag) {
                if (u.close) {
                    if (nest === 0) return j;
                    nest--;
                } else {
                    nest++;
                }
            } else if (isBoundary(u)) {
                return -1;
            }
        }
        return -1;
    }

    var out = "";
    var depth = 0;
    var pending = true; // a line break owed before the next token
    function brk() {
        out = out.replace(/[ \t]+$/, "");
        if (out !== "" && out.charAt(out.length - 1) !== "\n") out += "\n";
        for (var d = 0; d < depth; d++) out += "  ";
    }

    var i = 0;
    while (i < tokens.length) {
        var t = tokens[i];
        if (!t.tag && !t.comment) {
            // Whitespace that only separates structure is re-derived;
            // any other text passes through untouched.
            if (/^\s+$/.test(t.text) && (pending || isBoundary(tokens[i + 1]))) {
                i++;
                continue;
            }
            if (pending) {
                brk();
                out += t.text.replace(/^\s+/, "");
            } else {
                out += t.text;
            }
            pending = false;
            i++;
            continue;
        }
        if (t.comment) {
            brk();
            out += t.text;
            pending = true;
            i++;
            continue;
        }
        if (RAW_TAGS[t.tag] && !t.close) {
            brk();
            out += t.text;
            i++;
            while (i < tokens.length) {
                out += tokens[i].text;
                var wasClose = tokens[i].tag === t.tag && tokens[i].close;
                i++;
                if (wasClose) break;
            }
            pending = true;
            continue;
        }
        if (!BLOCK_TAGS[t.tag]) {
            if (pending) brk();
            out += t.text;
            pending = false;
            i++;
            continue;
        }
        if (t.close) {
            depth = Math.max(0, depth - 1);
            brk();
            out += t.text;
            pending = true;
            i++;
            continue;
        }
        brk();
        var end = inlineSpan(i);
        if (end !== -1) {
            for (var k = i; k <= end; k++) {
                out += tokens[k].tag || tokens[k].comment
                    ? tokens[k].text
                    : tokens[k].text.replace(/\s+/g, " ");
            }
            pending = true;
            i = end + 1;
            continue;
        }
        out += t.text;
        if (!VOID_TAGS[t.tag] && !/\/>$/.test(t.text)) depth++;
        pending = true;
        i++;
    }
    return out.replace(/[ \t]+$/, "");
}

function escHTML(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function highlightAttrs(s) {
    var out = "";
    var re = /([a-zA-Z_:][-.\w:]*)((?:\s*=\s*)(?:"[^"]*"|'[^']*'|[^\s>]+))?/g;
    var last = 0;
    var m;
    while ((m = re.exec(s)) !== null) {
        out += escHTML(s.slice(last, m.index));
        out += '<span class="tok-p">' + escHTML(m[1]) + "</span>";
        if (m[2] !== undefined) {
            var vm = /^(\s*=\s*)([\s\S]*)$/.exec(m[2]);
            out += escHTML(vm[1]) + '<span class="tok-s">' + escHTML(vm[2]) + "</span>";
        }
        last = m.index + m[0].length;
    }
    return out + escHTML(s.slice(last));
}

// highlightSource colors comments, tag names, attributes, and values.
// Cosmetic only, same contract as the CSS/JS panel's highlighter.
function highlightSource(src) {
    var out = "";
    var last = 0;
    var m;
    TOKEN_RE.lastIndex = 0;
    while ((m = TOKEN_RE.exec(src)) !== null) {
        out += escHTML(src.slice(last, m.index));
        var tok = m[0];
        if (tok.slice(0, 4) === "<!--") {
            out += '<span class="tok-c">' + escHTML(tok) + "</span>";
        } else {
            var tm = /^(<\/?)([a-zA-Z][a-zA-Z0-9-]*)([\s\S]*?)(\/?>)$/.exec(tok);
            out += escHTML(tm[1]) + '<span class="tok-k">' + escHTML(tm[2]) + "</span>" +
                highlightAttrs(tm[3]) + escHTML(tm[4]);
        }
        last = m.index + m[0].length;
    }
    // Trailing newline keeps the pre as tall as the textarea's last line.
    return out + escHTML(src.slice(last)) + "\n";
}

// elementSource returns el's markup as an author would have written it:
// editor chrome and TinyMCE's shadow attributes (data-mce-*) stripped,
// button locks (contenteditable) removed.
export function elementSource(el) {
    var clone = el.cloneNode(true);
    clone.querySelectorAll("[data-cms-ui],[data-mce-bogus]").forEach(function (n) { n.remove(); });
    var all = [clone];
    clone.querySelectorAll("*").forEach(function (n) { all.push(n); });
    all.forEach(function (n) {
        for (var i = n.attributes.length - 1; i >= 0; i--) {
            if (n.attributes[i].name.indexOf("data-mce-") === 0) {
                n.removeAttribute(n.attributes[i].name);
            }
        }
        if (n.classList.contains("cms-btn")) n.removeAttribute("contenteditable");
        if (n.getAttribute("class") === "") n.removeAttribute("class");
    });
    return clone.outerHTML;
}

var srcResolve = null; // pending openSource promise; truthy = modal open
var srcOriginal = "";
var srcValidate = null; // optional Apply gate: (text) -> error string or null

function showSourceError(msg) {
    $("src-err").textContent = msg;
    $("src-err").hidden = false;
    $("src-hint").hidden = true;
}

function clearSourceError() {
    $("src-err").hidden = true;
    $("src-hint").hidden = false;
}

export function isSourceOpen() {
    return !!srcResolve;
}

function renderSource() {
    $("src-hl").innerHTML = highlightSource($("src-ta").value);
}

// openSource shows html in the modal and resolves with the edited text
// on Apply, or null when dismissed.
export function openSource(opts) {
    return new Promise(function (resolve) {
        srcResolve = resolve;
        $("src-title").textContent = opts.title || "HTML source";
        // Non-admin saves pass through the server's sanitizer — say so
        // up front rather than letting tags vanish silently on save.
        $("src-hint").textContent = (opts.hint ? opts.hint + " " : "") +
            (isAdmin ? "" : "Scripts and unsafe markup are removed when your changes are saved.");
        srcValidate = opts.validate || null;
        clearSourceError();
        srcOriginal = formatHTML(opts.html || "");
        var ta = $("src-ta");
        ta.value = srcOriginal;
        ta.scrollTop = 0;
        ta.scrollLeft = 0;
        renderSource();
        $("src-hl").scrollTop = 0;
        $("src-hl").scrollLeft = 0;
        $("src-overlay").classList.add("on");
        $("src-panel").classList.add("on");
        ta.focus();
        ta.setSelectionRange(0, 0);
    });
}

function settleSource(value) {
    if (!srcResolve) return;
    $("src-overlay").classList.remove("on");
    $("src-panel").classList.remove("on");
    var resolve = srcResolve;
    srcResolve = null;
    srcValidate = null;
    resolve(value);
}

// dismissSource is the close-without-applying path: confirm first when
// the markup was edited.
export function dismissSource() {
    if (!srcResolve) return;
    if ($("src-ta").value === srcOriginal) {
        settleSource(null);
        return;
    }
    cmsConfirm("Discard your HTML changes?", "Discard changes", true).then(function (yes) {
        if (yes) settleSource(null);
    });
}

export function initSource() {
    $("src-close").addEventListener("click", dismissSource);
    $("src-cancel").addEventListener("click", dismissSource);
    $("src-overlay").addEventListener("click", dismissSource);
    $("src-apply").addEventListener("click", function () {
        if (srcValidate) {
            var err = srcValidate($("src-ta").value);
            if (err) {
                showSourceError(err);
                return; // keep the modal open until the text parses
            }
        }
        settleSource($("src-ta").value);
    });

    // Typing clears the error; the next Apply re-validates.
    $("src-ta").addEventListener("input", function () {
        clearSourceError();
        renderSource();
    });
    $("src-ta").addEventListener("scroll", function () {
        $("src-hl").scrollTop = $("src-ta").scrollTop;
        $("src-hl").scrollLeft = $("src-ta").scrollLeft;
    });
    // Tab indents instead of leaving the field.
    $("src-ta").addEventListener("keydown", function (e) {
        if (e.key !== "Tab") return;
        e.preventDefault();
        var ta = $("src-ta");
        ta.setRangeText("  ", ta.selectionStart, ta.selectionEnd, "end");
        renderSource();
    });
}
