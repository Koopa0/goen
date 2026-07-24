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
   * htmx swaps 2xx responses only. goen answers a rejected form with 422 and
   * the re-rendered form carrying its field errors — which is exactly what
   * should replace the old one. Admit that status rather than weakening the
   * response to a 200 that claims the submission succeeded.
   */
  function admitValidationResponses() {
    document.addEventListener("htmx:beforeSwap", (event) => {
      if (event.detail.xhr.status === 422) {
        event.detail.shouldSwap = true;
        event.detail.isError = false;
      }
    });
  }

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

  admitValidationResponses();
  headerMenu();
})();
