/* ------------------------------------------------------------------ *
 * Dialogs — styled replacements for window.confirm / window.prompt
 * ------------------------------------------------------------------ */

import { $ } from "./shell.js";
import { openPicker } from "./media.js";

var dlgResolve = null;
var dlgIsPrompt = false;
var dlgRequired = ""; // error shown when a required prompt is left empty
var dlgHasFields = false;
var dlgValues = {}; // field id -> current value while a dialog is open
var dlgPreview = null; // optional live-preview renderer (values, container)
var dlgNotes = []; // {el, text(values)} notes recomputed on every change

// dlgChanged re-renders the dialog's live parts — computed notes and the
// preview pane; every field builder calls it after updating dlgValues.
function dlgChanged() {
    dlgNotes.forEach(function (n) {
        var text = n.text(dlgValues);
        n.el.textContent = text;
        n.el.hidden = !text;
    });
    if (dlgPreview) dlgPreview(dlgValues, $("dlg-preview"));
}

// refreshDialog re-runs those live parts for a value that arrives late —
// an image whose proportions are only known once the browser has loaded
// it, say. A no-op when no dialog is open.
export function refreshDialog() {
    if (dlgResolve) dlgChanged();
}

// isDialogOpen lets the global Escape handler know whether a dialog is
// waiting for an answer.
export function isDialogOpen() {
    return !!dlgResolve;
}

export function openDialog(opts) {
    return new Promise(function (resolve) {
        dlgResolve = resolve;
        dlgIsPrompt = !!opts.prompt;
        // opts.required is the error message shown in-form when the
        // prompt input is submitted empty; "" means empty is allowed.
        dlgRequired = opts.required || "";
        clearDialogError();
        // opts.selects is the shorthand for select-only field lists;
        // opts.fields supports typed fields (select, color, image).
        var defs = opts.fields || (opts.selects || []).map(function (f) {
            return { id: f.id, label: f.label, type: "select", options: f.options, value: f.value };
        });
        dlgHasFields = defs.length > 0;
        dlgValues = {};
        dlgNotes = [];
        $("dlg-msg").textContent = opts.message;
        var input = $("dlg-input");
        input.hidden = !opts.prompt;
        input.value = opts.value || "";
        input.placeholder = opts.placeholder || "";
        var fields = $("dlg-fields");
        fields.innerHTML = "";
        defs.forEach(function (f) {
            var wrap = document.createElement("div");
            wrap.className = "fld";
            // In a wide dialog's two-column grid, f.span gives a field
            // the full width — for the ones a half-row would cramp.
            if (f.span) wrap.classList.add("span");
            if (f.tab) wrap.dataset.tab = f.tab;
            // A note is explanation rather than input: no label, no
            // value, and its text is recomputed from the other fields
            // every time one of them changes.
            if (f.type === "note") {
                wrap.classList.add("note");
                var note = document.createElement("p");
                note.className = "fnote";
                dlgNotes.push({ el: note, text: f.text });
                wrap.appendChild(note);
                fields.appendChild(wrap);
                return;
            }
            var label = document.createElement("label");
            label.textContent = f.label;
            wrap.appendChild(label);
            if (f.type === "color") buildColorField(wrap, f);
            else if (f.type === "image") buildImageField(wrap, f);
            else if (f.type === "range") buildRangeField(wrap, f);
            else if (f.type === "text") buildTextField(wrap, f);
            else if (f.type === "textarea") buildTextareaField(wrap, f);
            else if (f.type === "rich") buildRichField(wrap, f);
            else if (f.type === "datetime") buildDatetimeField(wrap, f);
            else if (f.type === "check") buildCheckField(wrap, f);
            else buildSelectField(wrap, f);
            fields.appendChild(wrap);
        });
        dlgPreview = opts.preview || null;
        $("dlg-preview").hidden = !dlgPreview;
        // Tabs: fields carrying a `tab` name show only on that tab;
        // the preview can be pinned to one tab via opts.previewTab.
        var tabBar = $("dlg-tabs");
        tabBar.innerHTML = "";
        tabBar.hidden = !(opts.tabs && opts.tabs.length);
        if (opts.tabs && opts.tabs.length) {
            var switchTab = function (name) {
                tabBar.querySelectorAll("button").forEach(function (b) {
                    b.classList.toggle("on", b.textContent === name);
                });
                fields.querySelectorAll(".fld").forEach(function (w) {
                    w.hidden = !!(w.dataset.tab && w.dataset.tab !== name);
                });
                $("dlg-preview").hidden = !dlgPreview ||
                    (!!opts.previewTab && opts.previewTab !== name);
            };
            opts.tabs.forEach(function (name) {
                var b = document.createElement("button");
                b.type = "button";
                b.textContent = name;
                b.addEventListener("click", function () { switchTab(name); });
                tabBar.appendChild(b);
            });
            switchTab(opts.tabs[0]);
        }
        dlgChanged(); // initial preview render
        var ok = $("dlg-ok");
        ok.textContent = opts.okLabel || "OK";
        ok.classList.toggle("danger", !!opts.danger);
        // opts.wide gives a settings dialog room to breathe — the
        // default width suits a question, not a panel of controls.
        $("dlg").classList.toggle("wide", !!opts.wide);
        // A tabbed dialog is pinned near the top of the screen instead
        // of centred: tabs differ in height, and a centred dialog
        // re-centres on every switch — which slides the tab bar out from
        // under the pointer that is still on its way to the next tab.
        $("dlg").classList.toggle("tabbed", !!(opts.tabs && opts.tabs.length));
        // A panel (the media picker, say) can ask a question of its own:
        // the dialog then has to stack above the panel that opened it,
        // rather than under it as it does when the dialog came first.
        var overPanel = $("overlay").classList.contains("on");
        $("dlg-overlay").classList.toggle("over", overPanel);
        $("dlg").classList.toggle("over", overPanel);
        $("dlg-overlay").classList.add("on");
        $("dlg").classList.add("on");
        (opts.prompt ? input : ok).focus();
    });
}

function buildSelectField(wrap, f) {
    var sel = document.createElement("select");
    f.options.forEach(function (o) {
        var opt = document.createElement("option");
        opt.value = o.value;
        opt.textContent = o.label;
        if (o.value === f.value) opt.selected = true;
        sel.appendChild(opt);
    });
    sel.addEventListener("change", function () { dlgValues[f.id] = sel.value; dlgChanged(); });
    wrap.appendChild(sel);
    dlgValues[f.id] = sel.value; // reflects the fallback when f.value is unknown
}

function buildColorField(wrap, f) {
    var row = document.createElement("div");
    row.className = "crow";
    var inp = document.createElement("input");
    inp.type = "color";
    var current = /^#[0-9a-fA-F]{6}$/.test(f.value || "") ? f.value : "";
    inp.value = current || "#ffffff";
    var txt = document.createElement("span");
    txt.className = "cval";
    var clear = document.createElement("button");
    clear.type = "button";
    clear.textContent = "Clear";
    dlgValues[f.id] = current;
    function show() {
        txt.textContent = dlgValues[f.id] || "None";
        clear.hidden = !dlgValues[f.id];
    }
    inp.addEventListener("input", function () { dlgValues[f.id] = inp.value; show(); dlgChanged(); });
    clear.addEventListener("click", function () { dlgValues[f.id] = ""; show(); dlgChanged(); });
    row.appendChild(inp);
    row.appendChild(txt);
    row.appendChild(clear);
    wrap.appendChild(row);
    show();
}

function buildTextField(wrap, f) {
    var inp = document.createElement("input");
    inp.type = "text";
    inp.className = "tinput";
    inp.placeholder = f.placeholder || "";
    inp.value = f.value || "";
    dlgValues[f.id] = inp.value;
    inp.addEventListener("input", function () { dlgValues[f.id] = inp.value; dlgChanged(); });
    wrap.appendChild(inp);
}

// buildTextareaField is a multi-line text input for larger values like
// site-wide CSS/JS. f.rows sets the visible height; f.mono renders the
// value in a monospace font (handy for code).
function buildTextareaField(wrap, f) {
    var ta = document.createElement("textarea");
    ta.className = "tinput" + (f.mono ? " tmono" : "");
    ta.rows = f.rows || 4;
    ta.placeholder = f.placeholder || "";
    ta.value = f.value || "";
    dlgValues[f.id] = ta.value;
    ta.addEventListener("input", function () { dlgValues[f.id] = ta.value; dlgChanged(); });
    wrap.appendChild(ta);
}

/* ---- rich text field (bold, italic, links) ------------------------
 * A notice, a caption, a one-line blurb: a sentence with the odd bold
 * word or link in it, and nothing else — no headings, no lists, no
 * images. That is small enough to build here rather than reach for
 * TinyMCE, which is lazy-loaded only in edit mode, renders its toolbar
 * into the light DOM at the top of the viewport, and would be a strange
 * thing to summon into a modal that can be opened while just reading a
 * page.
 *
 * The value in and out is HTML, held to the tags below. Everything the
 * field produces goes through the same sanitizer as everything it is
 * handed, so what a caller stores is only ever what this list allows —
 * which matters because the server trusts an admin's markup. */

// The link and unlink glyphs, lifted from the icon pack TinyMCE ships
// (editor/tinymce/icons/default, MIT, vendored here) so that the pair
// in this toolbar is the pair an editor already knows from the one that
// floats over the page — the same chain, whole and broken.
var ICON_LINK = '<svg viewBox="0 0 24 24"><path d="M6.2 12.3a1 1 0 0 1 1.4 1.4l-2 2a2 2 0 1 0 2.6 2.8l4.8-4.8a1 1 0 0 0 0-1.4 1 1 0 1 1 1.4-1.3 2.9 2.9 0 0 1 0 4L9.6 20a3.9 3.9 0 0 1-5.5-5.5l2-2Zm11.6-.6a1 1 0 0 1-1.4-1.4l2-2a2 2 0 1 0-2.6-2.8L11 10.3a1 1 0 0 0 0 1.4A1 1 0 1 1 9.6 13a2.9 2.9 0 0 1 0-4L14.4 4a3.9 3.9 0 0 1 5.5 5.5l-2 2Z" fill-rule="nonzero"/></svg>';
var ICON_UNLINK = '<svg viewBox="0 0 24 24"><path d="M6.2 12.3a1 1 0 0 1 1.4 1.4l-2 2a2 2 0 1 0 2.6 2.8l4.8-4.8a1 1 0 0 0 0-1.4 1 1 0 1 1 1.4-1.3 2.9 2.9 0 0 1 0 4L9.6 20a3.9 3.9 0 0 1-5.5-5.5l2-2Zm11.6-.6a1 1 0 0 1-1.4-1.4l2.1-2a2 2 0 1 0-2.7-2.8L11 10.3a1 1 0 0 0 0 1.4A1 1 0 1 1 9.6 13a2.9 2.9 0 0 1 0-4L14.4 4a3.9 3.9 0 0 1 5.5 5.5l-2 2ZM7.6 6.3a.8.8 0 0 1-1 1.1L3.3 4.2a.7.7 0 1 1 1-1l3.2 3.1ZM5.1 8.6a.8.8 0 0 1 0 1.5H3a.8.8 0 0 1 0-1.5H5Zm5-3.5a.8.8 0 0 1-1.5 0V3a.8.8 0 0 1 1.5 0V5Zm6 11.8a.8.8 0 0 1 1-1l3.2 3.2a.8.8 0 0 1-1 1L16 17Zm-2.2 2a.8.8 0 0 1 1.5 0V21a.8.8 0 0 1-1.5 0V19Zm5-3.5a.7.7 0 1 1 0-1.5H21a.8.8 0 0 1 0 1.5H19Z" fill-rule="nonzero"/></svg>';

var RICH_TAGS = { STRONG: 1, EM: 1, A: 1, BR: 1 };
// <b> and <i> are the same intent in older markup and in whatever a
// browser leaves behind; they are folded in rather than dropped.
var RICH_ALIAS = { B: "STRONG", I: "EM" };

// richHref keeps a link to something a link can safely be: the web, an
// address on this site, a mail or phone link. A bare domain gets the
// scheme it obviously meant. Anything else — javascript:, data: — comes
// back empty and the link is dropped, keeping its words.
function richHref(href) {
    var h = (href || "").trim();
    if (!h) return "";
    if (/^(https?:|mailto:|tel:)/i.test(h)) return h;
    if (h.charAt(0) === "/" || h.charAt(0) === "#") return h;
    if (/^[\w-]+(\.[\w-]+)+(\/|\?|$)/.test(h)) return "https://" + h;
    return "";
}

export function sanitizeRichHTML(html) {
    // DOMParser rather than innerHTML: this runs over content from the
    // server as well as from the field, and parsing must not load or
    // run anything on the way past.
    var doc = new DOMParser().parseFromString(
        '<body><div id="r">' + (html || "") + "</div></body>", "text/html");
    var root = doc.getElementById("r");
    (function walk(parent) {
        Array.prototype.slice.call(parent.childNodes).forEach(function (n) {
            if (n.nodeType === 3) return; // text survives as it is
            if (n.nodeType !== 1) { parent.removeChild(n); return; }
            walk(n);
            var name = RICH_ALIAS[n.nodeName] || n.nodeName;
            if (name === "A" && !richHref(n.getAttribute("href"))) name = "";
            if (!RICH_TAGS[name]) {
                // Unwrap rather than delete: a <p> or a <span> around
                // the words is not the words' fault.
                while (n.firstChild) parent.insertBefore(n.firstChild, n);
                parent.removeChild(n);
                return;
            }
            if (name !== n.nodeName) {
                var swap = doc.createElement(name.toLowerCase());
                while (n.firstChild) swap.appendChild(n.firstChild);
                parent.replaceChild(swap, n);
                n = swap;
            }
            Array.prototype.slice.call(n.attributes).forEach(function (a) {
                if (n.nodeName === "A" && a.name === "href") {
                    n.setAttribute("href", richHref(a.value));
                    return;
                }
                n.removeAttribute(a.name);
            });
        });
    })(root);
    return root.innerHTML;
}

// richSel is the field's own selection, or null when the caret is
// somewhere else. Shadow roots carry their own selection in the engines
// that implement it, and the document's in the ones that don't; asking
// the field's root for it covers both, and the containment check means
// a stale or foreign selection makes the buttons do nothing rather than
// something surprising.
function richSel(ed) {
    var root = ed.getRootNode();
    var sel = root.getSelection ? root.getSelection() : document.getSelection();
    if (!sel || !sel.rangeCount) return null;
    var range = sel.getRangeAt(0);
    var node = range.commonAncestorContainer;
    if (!ed.contains(node.nodeType === 1 ? node : node.parentNode)) return null;
    return { sel: sel, range: range };
}

// richClosest walks up to the field, never past it.
function richClosest(node, name, ed) {
    var n = node && node.nodeType === 1 ? node : node && node.parentNode;
    while (n && n !== ed) {
        if (n.nodeName === name) return n;
        n = n.parentNode;
    }
    return null;
}

function richUnwrap(el) {
    var parent = el.parentNode;
    while (el.firstChild) parent.insertBefore(el.firstChild, el);
    parent.removeChild(el);
    parent.normalize();
}

// richSurround wraps a range in el, falling back to a cut-and-paste for
// the ranges surroundContents refuses — a selection that starts inside
// one element and ends inside another.
function richSurround(range, el) {
    try {
        range.surroundContents(el);
    } catch (e) {
        el.appendChild(range.extractContents());
        range.insertNode(el);
    }
}

function richSelectContents(sel, el) {
    var r = document.createRange();
    r.selectNodeContents(el);
    sel.removeAllRanges();
    sel.addRange(r);
}

// richToggle turns a format on for the selection, or off when the
// selection already sits inside one — the same button doing both, which
// is what every editor has taught people to expect.
function richToggle(ed, tag) {
    var got = richSel(ed);
    if (!got) return;
    var inside = richClosest(got.range.commonAncestorContainer, tag, ed);
    if (inside) {
        richUnwrap(inside);
        return;
    }
    if (got.range.collapsed) return; // nothing selected: nothing to format
    var el = document.createElement(tag.toLowerCase());
    richSurround(got.range, el);
    richSelectContents(got.sel, el);
}

function richInsert(ed, node) {
    var got = richSel(ed);
    if (!got) {
        ed.appendChild(node);
        return;
    }
    got.range.deleteContents();
    got.range.insertNode(node);
    got.range.setStartAfter(node);
    got.range.collapse(true);
    got.sel.removeAllRanges();
    got.sel.addRange(got.range);
}

function buildRichField(wrap, f) {
    var box = document.createElement("div");
    box.className = "rich";

    var tools = document.createElement("div");
    tools.className = "rtb";
    var ed = document.createElement("div");
    ed.className = "rted";
    ed.contentEditable = "true";
    ed.spellcheck = true;
    ed.innerHTML = sanitizeRichHTML(f.value || "");
    if (f.placeholder) ed.setAttribute("data-placeholder", f.placeholder);

    dlgValues[f.id] = sanitizeRichHTML(f.value || "");

    var sync = function () {
        dlgValues[f.id] = sanitizeRichHTML(ed.innerHTML);
        ed.classList.toggle("empty", !ed.textContent.trim());
        marks();
        dlgChanged();
    };

    // The toolbar lights up for the format the caret is sitting in, so
    // the buttons report state as well as set it.
    var btns = {};
    var marks = function () {
        var got = richSel(ed);
        ["STRONG", "EM", "A"].forEach(function (tag) {
            var on = !!(got && richClosest(got.range.commonAncestorContainer, tag, ed));
            if (btns[tag]) btns[tag].classList.toggle("on", on);
        });
    };

    var button = function (label, title, onClick) {
        var b = document.createElement("button");
        b.type = "button";
        b.title = title;
        b.innerHTML = label;
        // The selection has to survive the click, and pressing a button
        // takes focus away from the words it is meant to act on.
        b.addEventListener("mousedown", function (e) { e.preventDefault(); });
        b.addEventListener("click", function (e) { e.preventDefault(); onClick(); });
        tools.appendChild(b);
        return b;
    };

    btns.STRONG = button("<b>B</b>", "Bold (⌘B)", function () {
        richToggle(ed, "STRONG");
        sync();
    });
    btns.EM = button("<i>I</i>", "Italic (⌘I)", function () {
        richToggle(ed, "EM");
        sync();
    });

    /* Links get a row of their own inside the toolbar rather than a
     * dialog: only one dialog can be open at a time, and this field is
     * already inside it. The row remembers the range it was opened
     * over, because clicking into the address box moves the caret out
     * of the words being linked. */
    var urlRow = document.createElement("div");
    urlRow.className = "rturl";
    urlRow.hidden = true;
    var urlInput = document.createElement("input");
    urlInput.type = "text";
    urlInput.placeholder = "https://example.com or /page";
    var urlOK = document.createElement("button");
    urlOK.type = "button";
    urlOK.textContent = "Link";
    var urlCancel = document.createElement("button");
    urlCancel.type = "button";
    urlCancel.textContent = "Cancel";
    urlRow.appendChild(urlInput);
    urlRow.appendChild(urlOK);
    urlRow.appendChild(urlCancel);

    var pending = null; // the range the address box is being filled for
    var closeURL = function () {
        urlRow.hidden = true;
        pending = null;
        urlInput.value = "";
    };
    var applyURL = function () {
        var href = richHref(urlInput.value);
        if (!href) {
            urlInput.classList.add("invalid");
            urlInput.focus();
            return;
        }
        var link = pending && richClosest(pending.commonAncestorContainer, "A", ed);
        if (link) {
            link.setAttribute("href", href);
        } else if (pending) {
            link = document.createElement("a");
            link.setAttribute("href", href);
            richSurround(pending, link);
        }
        closeURL();
        // Back to the words, with the new link selected: the caret was
        // left in the address box otherwise, which puts the toolbar out
        // of step with the page and makes the next keystroke go
        // somewhere surprising.
        ed.focus();
        var got = richSel(ed);
        if (link && got) richSelectContents(got.sel, link);
        sync();
    };

    btns.A = button(ICON_LINK, "Link", function () {
        var got = richSel(ed);
        var existing = got && richClosest(got.range.commonAncestorContainer, "A", ed);
        if (!got || (got.range.collapsed && !existing)) {
            // Nothing to hang a link on. Say so rather than opening a
            // box that cannot do anything.
            ed.classList.add("nosel");
            setTimeout(function () { ed.classList.remove("nosel"); }, 600);
            return;
        }
        pending = got.range.cloneRange();
        urlRow.hidden = false;
        urlInput.classList.remove("invalid");
        urlInput.value = existing ? existing.getAttribute("href") : "";
        urlInput.focus();
        urlInput.select();
    });
    button(ICON_UNLINK, "Remove link", function () {
        var got = richSel(ed);
        var a = got && richClosest(got.range.commonAncestorContainer, "A", ed);
        if (a) {
            richUnwrap(a);
            sync();
        }
    });

    urlOK.addEventListener("mousedown", function (e) { e.preventDefault(); });
    urlOK.addEventListener("click", applyURL);
    urlCancel.addEventListener("mousedown", function (e) { e.preventDefault(); });
    urlCancel.addEventListener("click", closeURL);
    urlInput.addEventListener("input", function () { urlInput.classList.remove("invalid"); });
    urlInput.addEventListener("keydown", function (e) {
        // Kept off the dialog's own Enter handler, which would save and
        // close over the top of an address being typed.
        e.stopPropagation();
        if (e.key === "Enter") { e.preventDefault(); applyURL(); }
        if (e.key === "Escape") { e.preventDefault(); closeURL(); }
    });

    ed.addEventListener("input", sync);
    ed.addEventListener("keyup", marks);
    ed.addEventListener("mouseup", marks);
    ed.addEventListener("focus", marks);
    ed.addEventListener("keydown", function (e) {
        var mod = e.metaKey || e.ctrlKey;
        if (mod && (e.key === "b" || e.key === "B")) {
            // Chrome bolds contenteditable on ⌘B by itself, with
            // whatever markup it prefers; this keeps it to <strong>.
            e.preventDefault();
            richToggle(ed, "STRONG");
            sync();
            return;
        }
        if (mod && (e.key === "i" || e.key === "I")) {
            e.preventDefault();
            richToggle(ed, "EM");
            sync();
            return;
        }
        if (e.key === "Enter") {
            // A line break, not a submit and not the <div> a browser
            // would otherwise leave behind.
            e.preventDefault();
            e.stopPropagation();
            richInsert(ed, document.createElement("br"));
            sync();
        }
    });
    // Paste arrives as words. A notice pasted out of a mail client
    // brings a stylesheet with it otherwise, and every bit of it would
    // be stripped on the way to storage anyway.
    ed.addEventListener("paste", function (e) {
        e.preventDefault();
        var text = (e.clipboardData || window.clipboardData).getData("text/plain");
        if (text) richInsert(ed, document.createTextNode(text));
        sync();
    });

    box.appendChild(tools);
    box.appendChild(urlRow);
    box.appendChild(ed);
    wrap.appendChild(box);
    ed.classList.toggle("empty", !ed.textContent.trim());
}

function buildDatetimeField(wrap, f) {
    var inp = document.createElement("input");
    inp.type = "datetime-local";
    inp.className = "tinput";
    inp.value = f.value || "";
    dlgValues[f.id] = inp.value;
    inp.addEventListener("input", function () { dlgValues[f.id] = inp.value; dlgChanged(); });
    wrap.appendChild(inp);
}

function buildCheckField(wrap, f) {
    // The field's own label is the clickable text, so suppress the
    // heading label openDialog already added.
    var heading = wrap.querySelector("label");
    if (heading) heading.remove();
    var lab = document.createElement("label");
    lab.className = "chk";
    var cb = document.createElement("input");
    cb.type = "checkbox";
    cb.checked = !!f.value;
    dlgValues[f.id] = cb.checked ? "1" : "";
    cb.addEventListener("change", function () {
        dlgValues[f.id] = cb.checked ? "1" : "";
        dlgChanged();
    });
    lab.appendChild(cb);
    lab.appendChild(document.createTextNode(f.label));
    wrap.appendChild(lab);
}

function buildRangeField(wrap, f) {
    var row = document.createElement("div");
    row.className = "rrow";
    var range = document.createElement("input");
    range.type = "range";
    range.min = f.min || 0;
    range.max = f.max || 100;
    range.step = 1;
    var num = document.createElement("input");
    num.type = "number";
    num.min = range.min;
    num.max = range.max;
    num.className = "rnum";
    var v = parseInt(f.value, 10);
    if (isNaN(v)) v = 0;
    range.value = num.value = v;
    dlgValues[f.id] = String(v);
    range.addEventListener("input", function () {
        num.value = range.value;
        dlgValues[f.id] = range.value;
        dlgChanged();
    });
    num.addEventListener("input", function () {
        var n = parseInt(num.value, 10);
        if (isNaN(n)) return;
        n = Math.max(+range.min, Math.min(+range.max, n));
        range.value = n;
        dlgValues[f.id] = String(n);
        dlgChanged();
    });
    row.appendChild(range);
    row.appendChild(num);
    wrap.appendChild(row);
}

function buildImageField(wrap, f) {
    var row = document.createElement("div");
    row.className = "irow";
    var thumb = document.createElement("img");
    var txt = document.createElement("span");
    txt.className = "cval";
    var choose = document.createElement("button");
    choose.type = "button";
    choose.textContent = f.chooseLabel || "Choose…";
    // f.noClear is for a field that stands for something already on the
    // page — an image placed in content is replaced or deleted, never
    // emptied, and its own trash can does the deleting.
    var clear = null;
    if (!f.noClear) {
        clear = document.createElement("button");
        clear.type = "button";
        clear.textContent = "Clear";
    }
    // The address is what the preview shows; the library id rides along
    // under "<id>_id" for callers that store the image rather than embed
    // it (the post dialogs), so the site can choose the size each slot
    // needs when it renders. It is 0 for an image from outside the
    // library, which only ever arrives as a stored value.
    dlgValues[f.id] = f.value || "";
    dlgValues[f.id + "_id"] = f.mediaId || 0;
    // Both renditions of a freshly chosen file, under "<id>_web" and
    // "<id>_orig", for callers that embed the image and let the reader
    // switch between them later (the image gear). Empty until something
    // is chosen, which is how those callers tell "left alone" from
    // "changed to this".
    dlgValues[f.id + "_web"] = "";
    dlgValues[f.id + "_orig"] = "";
    function show() {
        var v = dlgValues[f.id];
        thumb.hidden = !v;
        if (v) thumb.src = v;
        txt.hidden = !!v;
        txt.textContent = "None";
        if (clear) clear.hidden = !v;
    }
    choose.addEventListener("click", function () {
        // The media picker opens above the dialog; the dialog stays put
        // and shows the chosen image when the picker closes.
        openPicker("image", function (item) {
            // The web rendition is a downscaled, lossy WebP — right for
            // an image placed on a page, wrong for a field that wants the
            // file as uploaded (the favicon). f.prefer picks the other.
            // SVGs are unaffected: every rendition of one is the SVG.
            dlgValues[f.id] = (f.prefer === "original" && item.original) || item.web;
            dlgValues[f.id + "_id"] = item.id || 0;
            dlgValues[f.id + "_web"] = item.web || "";
            dlgValues[f.id + "_orig"] = item.original || "";
            show();
            dlgChanged();
        });
    });
    if (clear) {
        clear.addEventListener("click", function () {
            dlgValues[f.id] = "";
            dlgValues[f.id + "_id"] = 0;
            dlgValues[f.id + "_web"] = "";
            dlgValues[f.id + "_orig"] = "";
            show();
            dlgChanged();
        });
    }
    row.appendChild(thumb);
    row.appendChild(txt);
    row.appendChild(choose);
    if (clear) row.appendChild(clear);
    wrap.appendChild(row);
    show();
}

function settleDialog(value) {
    if (!dlgResolve) return;
    dlgPreview = null;
    $("dlg-overlay").classList.remove("on");
    $("dlg").classList.remove("on");
    $("dlg-overlay").classList.remove("over");
    $("dlg").classList.remove("over");
    var resolve = dlgResolve;
    dlgResolve = null;
    resolve(value);
}

// cmsConfirm resolves true/false; cmsPrompt resolves the entered text
// or null when dismissed.
export function cmsConfirm(message, okLabel, danger) {
    return openDialog({ message: message, okLabel: okLabel, danger: danger });
}
export function cmsPrompt(message, placeholder, okLabel, value) {
    return openDialog({ message: message, prompt: true, placeholder: placeholder, okLabel: okLabel, value: value });
}

// showDialogError marks the prompt input invalid with a message under
// it — the same in-form treatment the admin's server-rendered forms
// use, instead of HTML5 bubbles.
function showDialogError(msg) {
    $("dlg-err").textContent = msg;
    $("dlg-err").hidden = false;
    $("dlg-input").classList.add("invalid");
    $("dlg-input").focus();
}
function clearDialogError() {
    $("dlg-err").hidden = true;
    $("dlg-input").classList.remove("invalid");
}

function dialogOK() {
    if (dlgIsPrompt && dlgRequired && $("dlg-input").value.trim() === "") {
        showDialogError(dlgRequired);
        return; // keep the dialog open until there's a value
    }
    if (dlgHasFields) {
        var values = {};
        Object.keys(dlgValues).forEach(function (k) { values[k] = dlgValues[k]; });
        // A dialog may combine a text input with fields; the input's
        // value rides along under "input".
        if (dlgIsPrompt) values.input = $("dlg-input").value.trim();
        settleDialog(values);
        return;
    }
    settleDialog(dlgIsPrompt ? $("dlg-input").value.trim() : true);
}
export function dialogDismiss() { settleDialog(dlgIsPrompt || dlgHasFields ? null : false); }

export function initDialogs() {
    $("dlg-ok").addEventListener("click", dialogOK);
    // Typing clears the error; the next OK re-validates.
    $("dlg-input").addEventListener("input", clearDialogError);
    $("dlg-cancel").addEventListener("click", dialogDismiss);
    $("dlg-overlay").addEventListener("click", dialogDismiss);
    $("dlg").addEventListener("keydown", function (e) {
        if (e.key !== "Enter") return;
        // Not from a field where Enter is a line break — this handler
        // was swallowing newlines in every textarea the dialogs have
        // (the robots.txt box among them).
        var t = e.target;
        if (t && (t.tagName === "TEXTAREA" || t.isContentEditable)) return;
        e.preventDefault();
        dialogOK();
    });
}
