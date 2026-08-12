/* CMS in-place editor.
 *
 * Injected into pages viewed by logged-in editors. The glue chrome
 * (toolbar, media picker) is rendered inside Shadow DOM so the host page's
 * CSS cannot restyle it. Rich HTML regions are edited with TinyMCE in
 * inline mode — content keeps the page's own styles while a floating
 * selection toolbar provides formatting. TinyMCE is self-hosted alongside
 * this script and loaded lazily on the first press of "Edit".
 *
 * This entry point only wires the modules together. The init order
 * mirrors the original single-file script: listener registration order
 * matters for the capture-phase click handlers (spacer and video slots
 * before button chrome before image regions) and for who wins a shared
 * click.
 */

import { initShell, initLightDom, initBarMin } from "./shell.js";
import { initDialogs } from "./dialogs.js";
import { initEditing } from "./editing.js";
import { initTitle } from "./title.js";
import { initVideoSlots } from "./videos.js";
import { initButtons } from "./buttons.js";
import { initSaving } from "./saving.js";
import { initPageCode } from "./pagecode.js";
import { initPageSettings } from "./pagemeta.js";
import { initSource } from "./source.js";
import { initSnippets } from "./snippets.js";
import { initPostSettings } from "./postsettings.js";
import { initLocales } from "./locales.js";
import { initSections } from "./sections.js";
import { initMedia } from "./media.js";
import { initMenu } from "./menu.js";
import { initAdminTools } from "./admintools.js";

initShell(); // shadow-DOM chrome first: everything else looks up $(...)
initDialogs();
initLightDom(); // TinyMCE toolbar strip + light-DOM editing styles
initEditing();
initTitle(); // the page title, where a template prints it with cmsTitle
initVideoSlots(); // before initButtons: slot clicks beat snippet chrome
initButtons();
initSaving();
initPageCode();
initPageSettings();
initSource(); // the HTML-source modal for snippet blocks and sections
initBarMin(); // restores the remembered minimized state before first paint
initSnippets();
initPostSettings();
initLocales();
initSections();
initMedia();
initMenu();
initAdminTools(); // the wrench menu beside the site nav
