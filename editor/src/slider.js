/* ------------------------------------------------------------------ *
 * Sliders — a run of slides, and the one dialog that arranges them.
 *
 * A slide is not a special kind of object with a headline field and a
 * subtitle field. It is a picture with a box of ordinary content on top,
 * and the content is edited exactly the way content is edited
 * everywhere else on the page: click into it and type, drop a snippet in
 * it, put a button in it. Nothing here knows or cares what is in a
 * slide. That is why this file is short.
 *
 * What it does own is the things that are *about* the run rather than
 * about any one slide: which order the slides play in, how many there
 * are, how the page moves between them, and whether it moves on its own.
 * Those live behind a gear, because they are settings, and settings in
 * this editor are behind a gear.
 *
 * Two things deserve explaining because they are not obvious.
 *
 * The gear reorders slides rather than an in-place toolbar doing it, and
 * that is a deliberate departure from the column and card tools next
 * door. Those act on a thing you can see and point at. A slider shows
 * one slide at a time, so "move this one right" is a sentence about
 * something off screen — and even in edit mode, where all the slides are
 * unstacked so they can be written in, a tall run of full-bleed pictures
 * does not fit on a screen to be dragged around. A list of rows does.
 *
 * And the running slider's own chrome — the arrows and dots that
 * sliderJS builds on the public page — is torn down when edit mode
 * starts. It is generated, not content; it sits exactly where an editor
 * needs to click; and left in place it would be serialized into the
 * page's markup on the next save. collapseSliders and stripSliderChrome
 * below are the two halves of making sure it never is, and they mirror
 * collapseCode/stripCodeBodies in code.js, which solve the same problem
 * for custom-code blocks.
 * ------------------------------------------------------------------ */

import { openDialog } from "./dialogs.js";
import { copyOf } from "./clone.js";

// SLIDER and SLIDE are the classes the module's own markup carries. As
// with the card and question tools, matching on a class rather than on
// shape is what lets somebody hand-write a gallery this leaves alone.
var SLIDER = "cms-slider";
var SLIDE = "cms-slide";

// CHROME is everything sliderJS adds to a slider at runtime: the two
// arrows and the dot strip.
//
// Matched by the editor's own "this is chrome" attribute rather than by
// a list of the classes sliderJS happens to use. A list would be a
// second copy of a decision made in another language in another file,
// and the failure when it drifted would be silent — an unlisted element
// serialized into the page as content, sanitized down to something
// half-real, and then disagreeing with the chrome the script rebuilds on
// the next load. One attribute cannot drift, and it buys the rest of the
// convention too: source.js already strips data-cms-ui from the HTML
// view, and buttons.js already refuses to hang block chrome on it.
var CHROME = "[data-cms-ui]";

// RUNTIME_ATTRS are the bookkeeping attributes sliderJS writes on the
// slider as it runs. Same deal: generated state, not content.
var RUNTIME_ATTRS = ["data-cms-slider-at", "data-cms-slider-built",
    "data-cms-slider-single", "data-cms-slider-last"];

// RUNTIME_CLASSES are the per-slide marks that say which slide is
// showing. On the public page they are the whole point; in saved markup
// they would mean the stored content decides what a visitor sees first,
// which is sliderJS's job and nobody else's.
var RUNTIME_CLASSES = ["cms-slide-on", "cms-slide-prev"];

// scrub takes the runtime state off one slider element.
function scrub(el) {
    el.querySelectorAll(CHROME).forEach(function (n) { n.remove(); });
    RUNTIME_ATTRS.forEach(function (a) { el.removeAttribute(a); });
    el.querySelectorAll("." + SLIDE).forEach(function (s) {
        RUNTIME_CLASSES.forEach(function (c) { s.classList.remove(c); });
        s.removeAttribute("aria-hidden");
        if (s.getAttribute("class") === "") s.removeAttribute("class");
    });
}

// collapseSliders puts every slider on the page back to what is stored:
// the slides, and nothing else. Edit mode runs this before it takes its
// snapshot, so Cancel restores the markup rather than a moment in an
// animation.
export function collapseSliders() {
    document.querySelectorAll("." + SLIDER).forEach(scrub);
}

// stripSliderChrome does the same to a serialized string. A save runs it
// over everything it sends, because Save stays reachable after Done —
// when sliderJS has started up again and put the arrows back.
export function stripSliderChrome(html) {
    if (!html || html.indexOf(SLIDER) === -1) return html;
    var tpl = document.createElement("template");
    tpl.innerHTML = html;
    tpl.content.querySelectorAll("." + SLIDER).forEach(scrub);
    return tpl.innerHTML;
}

// sliderTarget reports the slider a click landed in, or null.
export function sliderTarget(target) {
    if (!target || !target.closest) return null;
    var el = target.closest("." + SLIDER);
    if (!el) return null;
    // Content converted from another site can carry a slider outside a
    // block, so this asks for an editable area rather than a
    // .cms-snippet wrapper — the same allowance the question tool makes.
    if (!el.closest("[data-cms-region],[data-cms-sections]")) return null;
    return el;
}

// slidesOf lists a slider's real slides, skipping the chrome (which
// should not be there while editing, but a stale one must not become a
// row in the dialog) and TinyMCE's bogus filler elements.
export function slidesOf(el) {
    return Array.prototype.filter.call(el.children, function (c) {
        return c.classList && c.classList.contains(SLIDE) &&
            !c.hasAttribute("data-mce-bogus");
    });
}

// pictureOf returns the <img> a slide shows, or null while it is still
// an unfilled photo slot.
function pictureOf(slide) {
    return slide.querySelector("img");
}

// thumbOf is what the dialog shows for a slide: its picture's address,
// or "" for a slide whose slot is still empty.
function thumbOf(slide) {
    var img = pictureOf(slide);
    return img ? img.getAttribute("src") || "" : "";
}

var TRANSITIONS = [
    { value: "fade", label: "Fade" },
    { value: "slide", label: "Slide" },
];

// Autoplay as a short list of intervals rather than a checkbox and a
// number. "Every 5 seconds" is the whole decision; a free number invites
// 800ms, which is unreadable, and it would need validating.
var AUTOPLAY = [
    { value: "", label: "Off — visitors move it themselves" },
    { value: "4000", label: "Every 4 seconds" },
    { value: "6000", label: "Every 6 seconds" },
    { value: "9000", label: "Every 9 seconds" },
];

// blankSlide builds a new slide from an existing one, so an added slide
// inherits whatever that site's slides look like — the same rule the
// card and question tools follow. The picture is the one just chosen;
// the words go back to placeholders, because a copy of the neighbour's
// headline is a headline somebody has to notice and rewrite.
function blankSlide(model, img, alt) {
    var slide = copyOf(model);
    setPicture(slide, img, alt);
    var h = slide.querySelector("h1,h2,h3,h4,h5,h6");
    if (h) h.textContent = "A headline for this slide";
    var body = slide.querySelector(".cms-slide-body");
    var ps = body ? body.querySelectorAll("p") : [];
    // The first paragraph is the supporting line. Anything after it —
    // a second paragraph, the paragraph holding a button — is kept: a
    // site whose slides all carry a "Book now" button should get one on
    // the new slide too, and the label is the one thing about a button
    // that is the same on every slide.
    if (ps.length) ps[0].textContent = "One line about what this picture is showing.";
    return slide;
}

// setPicture puts a chosen image into a slide, whether the slide is
// still showing its "Click to add a photo" slot or already has one.
//
// The <img> it writes is the shape photos.js writes for a slot click, so
// the two ways of choosing a slide's picture produce identical markup —
// otherwise a slide filled from the gear and one filled by clicking it
// would be two different things in the database.
function setPicture(slide, url, alt) {
    if (!url) return;
    var img = pictureOf(slide);
    if (img) {
        img.setAttribute("src", url);
        img.setAttribute("data-cms-web", url);
        if (alt) img.setAttribute("alt", alt);
        return;
    }
    var slot = slide.querySelector("[data-cms-photo-slot]");
    var next = document.createElement("img");
    next.setAttribute("src", url);
    next.setAttribute("alt", alt || "");
    next.setAttribute("loading", "lazy");
    next.setAttribute("data-cms-web", url);
    next.setAttribute("class", "w-full object-cover");
    if (slot) slot.parentNode.replaceChild(next, slot);
    else slide.insertBefore(next, slide.firstChild);
}

// openSliderSettings is the gear. It resolves once the dialog is
// dismissed; the caller wraps the changes in an undo transaction.
export function openSliderSettings(el, apply) {
    var slides = slidesOf(el);
    var rows = slides.map(function (s) {
        return { ref: s, img: thumbOf(s), alt: "" };
    });
    openDialog({
        message: "Slider",
        okLabel: "Apply",
        wide: true,
        fields: [
            { id: "transition", label: "Transition", type: "select",
                value: el.getAttribute("data-cms-slider") || "fade",
                options: TRANSITIONS },
            { id: "auto", label: "Move on its own", type: "select",
                value: el.getAttribute("data-cms-slider-auto") || "",
                options: AUTOPLAY },
            { id: "slides", label: "Slides", type: "slides", span: true,
                value: rows, addLabel: "Add a slide…" },
            { type: "note", span: true, text: function () {
                return "Each slide's words are edited on the page itself — " +
                    "while you are editing, the slides are laid out one under " +
                    "another so you can reach every one.";
            } },
        ],
    }).then(function (values) {
        if (!values) return;
        apply(function () { applySlider(el, values); });
    });
}

// applySlider writes the dialog's answer back to the block.
//
// Rebuilt from the rows rather than diffed against them: the rows are
// the order, and appendChild on an element already in the parent moves
// it, so replaying the list in order is both the reorder and the
// insertion, with no index arithmetic to get wrong.
function applySlider(el, values) {
    var rows = values.slides || [];
    var model = slidesOf(el)[0];

    var keep = [];
    rows.forEach(function (row) {
        if (row.ref && row.ref.parentNode === el) {
            // An existing slide: keep its words, take any new picture.
            if (row.img && row.img !== thumbOf(row.ref)) setPicture(row.ref, row.img, row.alt);
            keep.push(row.ref);
        } else if (model) {
            keep.push(blankSlide(model, row.img, row.alt));
        }
    });
    // A slider with no slides cannot be clicked back into existence, so
    // the dialog refuses to remove the last one and this is only a
    // backstop for a caller that got there another way.
    if (!keep.length) return;
    slidesOf(el).forEach(function (s) {
        if (keep.indexOf(s) === -1) s.remove();
    });
    keep.forEach(function (s) { el.appendChild(s); });

    var t = values.transition === "slide" ? "slide" : "fade";
    el.setAttribute("data-cms-slider", t);
    if (values.auto) el.setAttribute("data-cms-slider-auto", values.auto);
    else el.removeAttribute("data-cms-slider-auto");
}
