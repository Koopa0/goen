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
   * A department's panel opens by hover, in CSS. With this script running it
   * does not open by focus: every sub-link of every department would be a tab
   * stop before the search field. Keyboard users open it from the department's
   * own link with ArrowDown, which moves into the panel; ArrowUp and Down move
   * between its links and Escape closes it and returns to the link. Without
   * the script the stylesheet keeps the panel open while focus is inside the
   * department, so it stays reachable.
   *
   * Content that appears on hover has to be dismissible without moving the
   * pointer or the focus, so Escape also marks a hover-opened panel dismissed
   * until the pointer leaves or the focus moves out.
   */
  function departmentPanels() {
    const depts = document.querySelectorAll(".goen-dept");
    if (!depts.length) return;

    const parts = (dept) => ({
      link: dept.querySelector(":scope > a"),
      items: [...dept.querySelectorAll(".goen-dept__panel a")],
    });
    const setOpen = (dept, open) => {
      const { link } = parts(dept);
      if (open) {
        dept.setAttribute("data-open", "");
        dept.removeAttribute("data-dismissed");
      } else {
        dept.removeAttribute("data-open");
      }
      link?.setAttribute("aria-expanded", String(open));
    };

    for (const dept of depts) {
      const { link } = parts(dept);
      link?.setAttribute("aria-haspopup", "true");
      link?.setAttribute("aria-expanded", "false");

      dept.addEventListener("keydown", (event) => {
        const { link, items } = parts(dept);
        const at = items.indexOf(document.activeElement);
        if (event.key === "ArrowDown" && (document.activeElement === link || at >= 0)) {
          event.preventDefault();
          setOpen(dept, true);
          items[Math.min(at + 1, items.length - 1)]?.focus();
        } else if (event.key === "ArrowUp" && at >= 0) {
          event.preventDefault();
          if (at === 0) {
            setOpen(dept, false);
            link?.focus();
          } else {
            items[at - 1].focus();
          }
        }
      });
      dept.addEventListener("mouseleave", () => dept.removeAttribute("data-dismissed"));
      dept.addEventListener("focusout", (event) => {
        if (event.relatedTarget instanceof Node && dept.contains(event.relatedTarget)) return;
        dept.removeAttribute("data-dismissed");
        setOpen(dept, false);
      });
    }

    document.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      for (const dept of depts) {
        const { link, items } = parts(dept);
        const inside = items.includes(document.activeElement);
        if (dept.hasAttribute("data-open")) {
          setOpen(dept, false);
          if (inside) link?.focus();
        }
        if (dept.matches(":hover")) dept.setAttribute("data-dismissed", "");
      }
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

  /*
   * A field that carries a rule says so in its markup (data-rule, a pattern, the
   * message the server would give), all written from internal/fieldrule so the
   * browser and the server cannot hold two opinions. This only reads them.
   *
   * A field is checked when the shopper leaves it, never while a fresh one is
   * being typed; once it has been marked invalid it is re-checked on every
   * keystroke, so the message goes the moment the value is right. The message
   * lands in the element the 422 page uses (<id>-error, or data-rule-error), so
   * both paths read alike. The browser's own validation still blocks a submit;
   * for a refused ruled field the script shows the message in place of the
   * bubble. The server stays authoritative.
   *
   * Delegated from the document, so a form that arrives in a swap needs no
   * second initialisation. Without this file every form behaves as the server
   * alone makes it.
   */
  function fieldRules() {
    const ruled = (target) => target instanceof HTMLInputElement &&
      (target.hasAttribute("data-rule") || target.hasAttribute("data-checkout-constraint"));

    // goen's web.FoldWidth: full-width ASCII and the ideographic space.
    const fold = (text) => text
      .replace(/[\uFF01-\uFF5E]/gu, (c) => String.fromCharCode(c.charCodeAt(0) - 0xFEE0))
      .replace(/\u3000/gu, " ");

    // The 統編 weighted sum (invoice.ValidTaxID), which no pattern can state:
    // since 2023 the sum is divisible by 5, and a seventh digit of 7 counts for
    // 1 or 0. Mirrored, not shared; the Go test pins the vectors.
    const checks = {
      taxid(raw) {
        const id = fold(raw.trim());
        if (!/^[0-9]{8}$/u.test(id) || id === "00000000") return false;
        const weights = [1, 2, 1, 2, 1, 2, 4, 1];
        let sum = 0;
        let seventh = false;
        for (let i = 0; i < 8; i++) {
          const digit = Number(id[i]);
          if (i === 6 && digit === 7) {
            sum += 1;
            seventh = true;
            continue;
          }
          const product = digit * weights[i];
          sum += Math.floor(product / 10) + (product % 10);
        }
        return sum % 5 === 0 || (seventh && (sum - 1) % 5 === 0);
      },
    };

    const refused = (field) => {
      const value = field.value;
      if (value === "") return false;
      const check = field.dataset.ruleCheck;
      // Clear first: validity.valid must be the pattern's verdict alone when a
      // value that was refused for its checksum is edited.
      field.setCustomValidity("");
      if (field.validity.patternMismatch) return true;
      const max = Number(field.dataset.ruleMax || 0);
      if (max > 0 && Array.from(value.trim()).length > max) return true;
      if (check && checks[check] && !checks[check](value)) {
        field.setCustomValidity(field.dataset.ruleMessage || " ");
        return true;
      }
      return false;
    };

    const messageFor = (field) => {
      const id = field.dataset.ruleError || (field.id ? field.id + "-error" : "");
      return id ? document.getElementById(id) : null;
    };

    const describe = (field, id, on) => {
      const tokens = (field.getAttribute("aria-describedby") || "").split(/\s+/u).filter(Boolean);
      const has = tokens.includes(id);
      if (on && !has) tokens.push(id);
      if (!on && has) tokens.splice(tokens.indexOf(id), 1);
      if (tokens.length) field.setAttribute("aria-describedby", tokens.join(" "));
      else field.removeAttribute("aria-describedby");
    };

    const mark = (field, invalid) => {
      let message = messageFor(field);
      const id = field.dataset.ruleError || (field.id ? field.id + "-error" : "");
      if (invalid) {
        field.setAttribute("aria-invalid", "true");
        if (!message && id && field.dataset.ruleMessage) {
          message = document.createElement("p");
          message.className = "ui-error-text";
          message.id = id;
          field.insertAdjacentElement("afterend", message);
        }
        if (message) {
          if (field.dataset.ruleMessage) message.textContent = field.dataset.ruleMessage;
          message.hidden = false;
          describe(field, message.id, true);
        }
      } else {
        field.removeAttribute("aria-invalid");
        if (message) {
          message.hidden = true;
          describe(field, message.id, false);
        }
      }
    };

    // Submit stays blocked by the browser's own validation. For a ruled field
    // holding a value the rule refuses, the script gives the message instead of
    // the bubble and moves focus to the first such field. Everything else (an
    // empty required field, a constraint the rule does not state) keeps the
    // browser's report. invalid does not bubble, hence the capture phase.
    document.addEventListener("invalid", (event) => {
      const field = event.target;
      if (!ruled(field) || field.value === "" || !refused(field)) return;
      event.preventDefault();
      mark(field, true);
      const form = field.form;
      if (!form || form.dataset.ruleFocusing) return;
      form.dataset.ruleFocusing = "";
      queueMicrotask(() => {
        delete form.dataset.ruleFocusing;
        const first = form.querySelector(":invalid");
        if (first && first.getAttribute("aria-invalid") === "true") first.focus();
      });
    }, true);

    document.addEventListener("focusout", (event) => {
      if (!ruled(event.target)) return;
      // Leaving for a submit button: a message appearing now would move the
      // button from under the pointer and lose the press. The submit itself is
      // validated, and its invalid handler gives the message.
      if (event.relatedTarget instanceof Element && event.relatedTarget.matches('button[type="submit"], input[type="submit"], button:not([type])')) return;
      // An empty field is the server's "required", and a tab through a form
      // should not start by calling every field wrong.
      if (event.target.value === "") {
        if (event.target.getAttribute("aria-invalid") === "true") mark(event.target, false);
        return;
      }
      mark(event.target, refused(event.target));
    });

    document.addEventListener("input", (event) => {
      if (!ruled(event.target) || event.target.getAttribute("aria-invalid") !== "true") return;
      if (event.target.value === "" || !refused(event.target)) mark(event.target, false);
    });
  }

  /*
   * The hop to the carrier's store map is a form that posts to the carrier. A
   * browser with scripting submits it at once; one without shows its button.
   * Not on a return by the back button: the shopper came back from the map to
   * leave it, and sending them forward again would trap them.
   */
  function handoff() {
    const form = document.querySelector("form[data-handoff]");
    if (!form) return;
    const back = performance.getEntriesByType("navigation")[0]?.type === "back_forward";
    if (!back) form.requestSubmit();
    window.addEventListener("pageshow", (event) => {
      if (event.persisted) form.removeAttribute("data-request-pending");
    });
  }

  fieldRules();
  handoff();
  headerMenu();
  departmentPanels();
  popovers();
  stepper();
  carousel();
})();
