/* ------------------------------------------------------------------ *
 * Copying a piece of content.
 *
 * Every duplicate the editor makes — a block, a column, a block placed
 * beside itself — is cloneNode plus one chore, and it is the chore that
 * is worth having in a single place, because getting it wrong fails
 * quietly and late.
 *
 * Two kinds of attribute must not survive a copy:
 *
 *   - TinyMCE's shadows. It mirrors the attributes it rewrites
 *     (data-mce-style, data-mce-href) so it can put back what an edit
 *     would otherwise lose, and it trusts the shadow over the real
 *     attribute when it serializes. A clone carrying a stale one has
 *     the original's style or address written back over its own on the
 *     next save — the exact bug applyButtonSettings and
 *     applySnippetSettings call syncStyleShadow to avoid.
 *   - The editor's own marks. cms-col-active says which column the
 *     column tool has hold of *now*, and a copy of the selection is not
 *     the selection. They are stripped at serialization as well (see
 *     source.js and the class filter in richtext.js); a copy should not
 *     carry them even that far.
 * ------------------------------------------------------------------ */

// MARKS are the classes the editor puts on content to say what it is
// currently acting on, as against what the content is.
var MARKS = ["cms-col-active"];

// copyOf returns a detached, saveable clone of el and everything in it.
// What it deliberately does not do is rename anything: a copied block
// keeps its data-cms-code key, so two placeholders share one library
// entry, which is what a code block's own starter markup is written to
// expect ("a block used twice still finds its own markup").
export function copyOf(el) {
    var clone = el.cloneNode(true);
    var all = [clone];
    clone.querySelectorAll("*").forEach(function (n) { all.push(n); });
    all.forEach(function (n) {
        for (var i = n.attributes.length - 1; i >= 0; i--) {
            if (n.attributes[i].name.indexOf("data-mce-") === 0) {
                n.removeAttribute(n.attributes[i].name);
            }
        }
        MARKS.forEach(function (m) { n.classList.remove(m); });
        // classList.remove leaves class="" behind on an element whose
        // only class was a mark, and an empty attribute would go on to
        // be saved as content.
        if (n.hasAttribute("class") && !n.getAttribute("class")) {
            n.removeAttribute("class");
        }
    });
    return clone;
}
