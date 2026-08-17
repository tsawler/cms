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
        if (e.key === "Enter") {
            e.preventDefault();
            dialogOK();
        }
    });
}
