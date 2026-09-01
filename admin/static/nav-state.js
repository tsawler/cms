// Reapplies the saved sidebar state before the body renders, so a
// collapsed menu never flashes open on its way to a new page. Loaded
// blocking (no defer) from the layout's head for exactly that reason —
// it must run before first paint, and it is small enough to afford that.
// The class goes on <html> because <body> does not exist yet.
//
// admin.js owns the toggle itself; the localStorage key here and there
// must match. Storage can be unreadable (private windows, blocked site
// data), in which case the menu simply starts expanded.
(function () {
    "use strict";
    try {
        if (localStorage.getItem("cms-nav-collapsed") === "1") {
            document.documentElement.classList.add("cms-nav-collapsed");
        }
    } catch (e) { /* no storage: the default, expanded, is fine */ }
})();
