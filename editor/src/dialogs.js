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

// dlgChanged re-renders the dialog's preview pane; every field
// builder calls it after updating dlgValues.
function dlgChanged() {
    if (dlgPreview) dlgPreview(dlgValues, $("dlg-preview"));
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
            if (f.tab) wrap.dataset.tab = f.tab;
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
    choose.textContent = "Choose…";
    var clear = document.createElement("button");
    clear.type = "button";
    clear.textContent = "Clear";
    dlgValues[f.id] = f.value || "";
    function show() {
        var v = dlgValues[f.id];
        thumb.hidden = !v;
        if (v) thumb.src = v;
        txt.hidden = !!v;
        txt.textContent = "None";
        clear.hidden = !v;
    }
    choose.addEventListener("click", function () {
        // The media picker opens above the dialog; the dialog stays put
        // and shows the chosen image when the picker closes. The item's
        // generated thumbnail rides along under "<id>_thumb" for callers
        // that want it (the post dialogs).
        openPicker("image", function (item) {
            dlgValues[f.id] = item.web;
            dlgValues[f.id + "_thumb"] = item.thumb || item.web;
            show();
            dlgChanged();
        });
    });
    clear.addEventListener("click", function () {
        dlgValues[f.id] = "";
        dlgValues[f.id + "_thumb"] = "";
        show();
        dlgChanged();
    });
    row.appendChild(thumb);
    row.appendChild(txt);
    row.appendChild(choose);
    row.appendChild(clear);
    wrap.appendChild(row);
    show();
}

function settleDialog(value) {
    if (!dlgResolve) return;
    dlgPreview = null;
    $("dlg-overlay").classList.remove("on");
    $("dlg").classList.remove("on");
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
