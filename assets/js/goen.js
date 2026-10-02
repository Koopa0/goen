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
   * The home carousel: scroll-snap does the moving (swipe, trackpad, keyboard),
   * and prefers-reduced-motion is answered in CSS, where scroll-behavior is set.
   * This only wires the arrows and dots and keeps aria-current on the slide in
   * view. There is no autoplay.
   */
  function carousel() {
    const root = document.querySelector(".goen-hero");
    const track = root?.querySelector(".goen-hero__track");
    if (!track || track.children.length < 2) return;

    const slides = [...track.children];
    const dots = [...root.querySelectorAll(".goen-hero__dot")];
    let current = 0;
    const go = (index) => {
      const next = (index + slides.length) % slides.length;
      track.scrollTo({ left: next * track.clientWidth });
    };
    const seen = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        current = slides.indexOf(entry.target);
        dots.forEach((dot, index) => dot.setAttribute("aria-current", String(index === current)));
      }
    }, { root: track, threshold: 0.6 });
    slides.forEach((slide) => seen.observe(slide));

    /*
     * Autoplay. The current line indicator's fill is a CSS animation over the
     * interval and its end advances the slide, so the motion is drawn as it
     * happens. It holds while the pointer is over the carousel or focus is in
     * it, stops for good once the visitor steers it by arrow, indicator, swipe,
     * wheel or key, can be paused by the button (WCAG 2.2.2), and never starts
     * under prefers-reduced-motion.
     */
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
    const pause = root.querySelector(".goen-hero__pause");
    let running = root.hasAttribute("data-autoplay") && !reduced.matches;
    let hovered = false;
    let focused = false;
    let paused = false;

    const render = () => {
      root.classList.toggle("is-playing", running);
      root.classList.toggle("is-held", hovered || focused || paused);
      if (!pause) return;
      pause.hidden = !running;
      pause.dataset.state = paused ? "paused" : "playing";
      pause.setAttribute("aria-label", paused ? pause.dataset.labelPlay : pause.dataset.labelPause);
    };
    // Steering by hand ends autoplay for good: the visitor has taken over.
    const stop = () => {
      if (!running) return;
      running = false;
      render();
    };

    root.querySelectorAll("[data-step]").forEach((button) => {
      button.addEventListener("click", () => {
        stop();
        go(current + Number(button.dataset.step));
      });
    });
    dots.forEach((dot, index) => dot.addEventListener("click", () => {
      stop();
      go(index);
    }));
    for (const type of ["touchstart", "wheel"]) {
      track.addEventListener(type, stop, { passive: true });
    }
    // Tab moves focus and is not steering; the keys that scroll the track are.
    track.addEventListener("keydown", (event) => {
      if (/^(Arrow|Page|Home$|End$)/.test(event.key)) stop();
    });

    if (!running) return;
    root.addEventListener("animationend", (event) => {
      if (event.animationName === "goen-hero-progress" && running) go(current + 1);
    });
    root.addEventListener("pointerenter", () => { hovered = true; render(); });
    root.addEventListener("pointerleave", () => { hovered = false; render(); });
    // Only focus a keyboard put there holds the carousel: a mouse click on the
    // pause button leaves focus on it, which must not freeze the slides again.
    root.addEventListener("focusin", (event) => {
      focused = event.target.matches(":focus-visible");
      render();
    });
    root.addEventListener("focusout", (event) => {
      if (!root.contains(event.relatedTarget)) { focused = false; render(); }
    });
    // Pressing play is an explicit request to move, so the hover or focus that
    // was holding the carousel is set aside until the pointer or focus comes
    // back to it.
    pause?.addEventListener("click", () => {
      paused = !paused;
      if (!paused) { hovered = false; focused = false; }
      render();
    });
    reduced.addEventListener("change", () => { if (reduced.matches) stop(); });
    render();
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

    menu.querySelector("[data-menu-close]")?.addEventListener("click", () => {
      menu.open = false;
      menu.querySelector("summary")?.focus();
    });
  }

  /*
   * Small menus built on <details data-popover>, such as the language menu.
   * The element opens itself; what it does not ship with is closing on Escape
   * with the focus returned to its button, closing on a click elsewhere, and
   * one open at a time.
   */
  function popovers() {
    const pops = document.querySelectorAll("details[data-popover]");
    if (!pops.length) return;

    document.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      for (const pop of pops) {
        if (!pop.open) continue;
        const inside = pop.contains(document.activeElement);
        pop.open = false;
        if (inside) pop.querySelector("summary")?.focus();
      }
    });
    document.addEventListener("click", (event) => {
      for (const pop of pops) {
        if (pop.open && !pop.contains(event.target)) pop.open = false;
      }
    });
    for (const pop of pops) {
      pop.addEventListener("toggle", () => {
        if (!pop.open) return;
        for (const other of pops) if (other !== pop) other.open = false;
      });
    }
  }

  /*
   * A department's panel opens by hover or focus, in CSS. Content that appears
   * that way has to be dismissible without moving the pointer or the focus, so
   * Escape marks the open one dismissed until the pointer leaves or the focus
   * moves out.
   */
  function departmentPanels() {
    const depts = document.querySelectorAll(".goen-dept");
    if (!depts.length) return;

    document.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      for (const dept of depts) {
        if (dept.matches(":hover, :focus-within")) dept.setAttribute("data-dismissed", "");
      }
    });
    for (const dept of depts) {
      const reset = () => dept.removeAttribute("data-dismissed");
      dept.addEventListener("mouseleave", reset);
      dept.addEventListener("focusout", reset);
    }
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
        const atBound = next < Number(field.min || 0) ||
          (field.max !== "" && next > Number(field.max));
        // A bound is aria-disabled, not disabled: the button that reaches it
        // has focus, and disabling a focused button sends focus to the body.
        // The whole stepper being unavailable is the server's disabled.
        if (atBound) step.setAttribute("aria-disabled", "true");
        else step.removeAttribute("aria-disabled");
      });
    };

    document.addEventListener("click", (event) => {
      if (!(event.target instanceof Element)) return;
      const step = event.target.closest("[data-stepper-step]");
      if (!step || step.getAttribute("aria-disabled") === "true") return;
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

    /*
     * A form marked data-autosubmit applies a changed quantity itself. The
     * wait lets someone press + twice before the page answers once, and the
     * form's own submit button stays in the markup for the browser that has
     * no script.
     */
    const waiting = new WeakMap();
    document.addEventListener("change", (event) => {
      if (!(event.target instanceof Element)) return;
      const form = event.target.closest("form[data-autosubmit]");
      if (!form || !event.target.matches("[data-stepper] input")) return;
      clearTimeout(waiting.get(form));
      waiting.set(form, setTimeout(() => {
        if (form.checkValidity()) form.requestSubmit();
      }, 700));
    });

    /*
     * The same form is an htmx request where script runs, and it swaps only
     * the regions its hx-select-oob names. Three things htmx does not do for
     * it are done here:
     *
     * - No view transition. Every swap is a cross-fade of the page by default,
     *   and a quantity change must not repaint anything it did not change.
     * - A refusal is reconciled. The server answers a quantity it cannot
     *   honour with the page showing the quantity it kept, so the field takes
     *   that number back and the notice region carries the reason.
     * - A change made while a request was in flight is sent once after it,
     *   because the request feedback drops a submit that arrives mid-request.
     * - Updates run one at a time across lines (hx-sync on the form), so the
     *   last response is rendered after every change and its summary is whole.
     *
     * A response without the line (it was removed) or no usable response at
     * all is answered by loading the cart, which is always correct.
     */
    const quantity = (form) => form.querySelector("[data-stepper] input");

    document.addEventListener("htmx:config:request", (event) => {
      const ctx = event.detail?.ctx;
      const form = ctx?.request?.form;
      if (!(form instanceof HTMLFormElement) || !form.matches("form[data-autosubmit]")) return;
      ctx.transition = false;
      form.dataset.sent = quantity(form)?.value ?? "";
    });

    document.addEventListener("htmx:finally:request", (event) => {
      const ctx = event.detail?.ctx;
      const form = ctx?.request?.form;
      if (!(form instanceof HTMLFormElement) || !form.matches("form[data-autosubmit]")) return;
      const field = quantity(form);
      const page = ctx.response?.status < 400 && ctx.text
        ? new DOMParser().parseFromString(ctx.text, "text/html")
        : null;
      const kept = field && page ? page.getElementById(field.id) : null;
      if (!kept) {
        window.location.assign("/cart");
        return;
      }
      if (field.value !== form.dataset.sent) {
        // One follow-up with the latest value. The debounce timer a change made
        // during the request left behind would send the same value again.
        clearTimeout(waiting.get(form));
        if (form.checkValidity()) form.requestSubmit();
        return;
      }
      if (kept.value !== field.value) {
        field.value = kept.value;
        field.dispatchEvent(new Event("input", { bubbles: true }));
      }
    });
  }

  // Request state is presentation only. Submitter names and values remain in
  // the payload; native disabled controls would remove them before submission.
  function requestFeedback() {
    const pending = new Map();
    const requests = new WeakMap();
    const restoreAttribute = (element, name, value) => {
      if (value === null) element.removeAttribute(name);
      else element.setAttribute(name, value);
    };
    const begin = (form) => {
      const buttons = [...form.querySelectorAll('button[type="submit"]:not([data-feedback-skip]), input[type="submit"]')]
        .map((button) => [button, button.getAttribute("aria-disabled")]);
      pending.set(form, { busy: form.getAttribute("aria-busy"), buttons });
      form.setAttribute("aria-busy", "true");
      form.setAttribute("data-request-pending", "");
      for (const [button] of buttons) button.setAttribute("aria-disabled", "true");
    };
    const finish = (form) => {
      const state = pending.get(form);
      if (!state) return;
      restoreAttribute(form, "aria-busy", state.busy);
      form.removeAttribute("data-request-pending");
      for (const [button, disabled] of state.buttons) restoreAttribute(button, "aria-disabled", disabled);
      pending.delete(form);
    };
    document.addEventListener("submit", (event) => {
      if (event.defaultPrevented || !(event.target instanceof HTMLFormElement)) return;
      if (pending.has(event.target)) event.preventDefault();
      else begin(event.target);
    });
    document.addEventListener("htmx:before:request", (event) => {
      const ctx = event.detail?.ctx;
      const form = ctx?.request?.form;
      if (!(form instanceof HTMLFormElement)) return;
      // The filter form lets the latest change replace the one in flight
      // (hx-sync), which this guard would cancel as a repeated press.
      if (form.matches(".goen-filters")) return;
      // Do not delete the isConnected clause: a second press queues behind the
      // first, whose response swaps the form out of the page, and htmx then
      // issues the queued request from that detached form, where nothing is
      // pending any more. Without the clause it posts a second time and the
      // product is added twice.
      if (pending.has(form) || !ctx.sourceElement.isConnected) { event.preventDefault(); return; }
      begin(form);
      requests.set(ctx, form);
    });
    // On document, because the source element may be detached by the swap
    // before this fires and an event on a detached node never reaches us.
    document.addEventListener("htmx:finally:request", (event) => {
      const ctx = event.detail?.ctx;
      // A failed filter update swaps nothing, so say so beside the filters. One
      // replaced by a newer change has no response and is not a failure.
      if (ctx?.sourceElement?.matches?.(".goen-filters")) {
        const note = document.querySelector(".goen-filters__error");
        const raw = ctx.response?.raw;
        if (note && raw) note.hidden = raw.ok;
      }
      const form = requests.get(ctx);
      if (!form) return;
      requests.delete(ctx);
      finish(form);
    });
    document.addEventListener("reset", (event) => finish(event.target));
    window.addEventListener("pageshow", () => {
      for (const form of pending.keys()) finish(form);
    });
  }

  requestFeedback();

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
  departmentPanels();
  popovers();
  stepper();
  carousel();
})();
