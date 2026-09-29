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

  /*
   * The quantity stepper. The field is a native number input that works on its
   * own; these two buttons are the enhancement, and the stylesheet keeps them
   * out of sight until it is told scripting is on.
   *
   * Delegated from the document rather than bound on load, so a buy box that
   * arrives in a later swap needs no second initialisation.
   */
  function stepper() {
    const bound = (field, by) => {
      const min = Number(field.min || 0);
      const max = field.max === "" ? Infinity : Number(field.max);
      return Math.min(max, Math.max(min, Number(field.value || min) + by));
    };

    const atBounds = (box) => {
      const field = box.querySelector("input");
      if (!field) return;
      const value = Number(field.value || 0);
      box.querySelectorAll("[data-stepper-step]").forEach((step) => {
        const next = value + Number(step.dataset.stepperStep);
        step.disabled = field.disabled ||
          next < Number(field.min || 0) ||
          (field.max !== "" && next > Number(field.max));
      });
    };

    document.addEventListener("click", (event) => {
      if (!(event.target instanceof Element)) return;
      const step = event.target.closest("[data-stepper-step]");
      if (!step) return;
      const box = step.closest("[data-stepper]");
      const field = box?.querySelector("input");
      if (!field) return;
      field.value = String(bound(field, Number(step.dataset.stepperStep)));
      field.dispatchEvent(new Event("change", { bubbles: true }));
      atBounds(box);
    });

    document.addEventListener("input", (event) => {
      if (!(event.target instanceof Element)) return;
      const box = event.target.closest("[data-stepper]");
      if (box) atBounds(box);
    });

    document.querySelectorAll("[data-stepper]").forEach(atBounds);
  }

  // Delegation includes fields replaced by a checkout choice. Native browser
  // constraints also work without this accessibility-state enhancement.
  function checkoutConstraints() {
    const constrained = (target) => target instanceof HTMLInputElement &&
      target.hasAttribute("data-checkout-constraint");
    document.addEventListener("focusout", (event) => {
      if (!constrained(event.target)) return;
      event.target.setAttribute("aria-invalid", String(!event.target.validity.valid));
    });
    document.addEventListener("input", (event) => {
      if (!constrained(event.target) || !event.target.validity.valid) return;
      event.target.removeAttribute("aria-invalid");
    });
  }

  checkoutConstraints();
  headerMenu();
  stepper();
})();
