/*
 * goen — the one enhancement file.
 *
 * Everything here is optional by construction: every write path is a plain
 * <form method="post"> that the server answers on its own, and every
 * disclosure is a native <details>. Remove this file and the site still works,
 * one full page load at a time.
 */
(() => {
  "use strict";

  /*
   * A rejected form comes back as 422 with the re-rendered form carrying its
   * field errors — exactly what should replace the old one. htmx 4 swaps every
   * response but 204 and 304, so that 422 (and a 500 error page) swaps on its
   * own; the htmx-2 before-swap shim that used to admit it is gone. Nothing to
   * configure here.
   */

  /*
   * The header's category menu is a native <details>. Closing it on Escape and
   * on outside click is the ceremony the element does not ship with.
   */
  function headerMenu() {
    const menu = document.querySelector("[data-menu]");
    if (!menu) return;

    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && menu.open) {
        menu.open = false;
        menu.querySelector("summary")?.focus();
      }
    });

    document.addEventListener("click", (event) => {
      if (menu.open && !menu.contains(event.target)) menu.open = false;
    });
  }

  headerMenu();
})();
