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
   * htmx 4 swaps every response but 204 and 304, so a rejected form's 422 with
   * its field errors replaces the old form with nothing configured here.
   */

  /*
   * The home carousel: scroll-snap does the moving (swipe, trackpad, keyboard)
   * and the named tabs are links that work without this script. This draws the
   * arrows and the count and keeps aria-current on the slide in view. Nothing
   * here moves a slide except the visitor's own input.
   */
  function carousel() {
    const root = document.querySelector(".goen-hero");
    const track = root?.querySelector(".goen-hero__track");
    if (!track || track.children.length < 2) return;

    const slides = [...track.children];
    const tabs = [...root.querySelectorAll(".goen-hero__tabs a")];
    const count = root.querySelector("[data-count]");
    let current = 0;
    const go = (index) => {
      const next = (index + slides.length) % slides.length;
      track.scrollTo({ left: next * track.clientWidth });
    };
    const seen = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        // The first report is the slide already shown; the live region speaks
        // only once the visitor has moved the carousel.
        const index = slides.indexOf(entry.target);
        if (index === current) continue;
        current = index;
        tabs.forEach((tab, index) => {
          if (index === current) tab.setAttribute("aria-current", "true");
          else tab.removeAttribute("aria-current");
        });
        if (count) count.textContent = `${current + 1} / ${slides.length}`;
      }
    }, { root: track, threshold: 0.6 });
    slides.forEach((slide) => seen.observe(slide));

    tabs.forEach((tab, index) => tab.addEventListener("click", (event) => {
      event.preventDefault();
      go(index);
    }));
    root.querySelectorAll("[data-step]").forEach((button) => {
      button.addEventListener("click", () => go(current + Number(button.dataset.step)));
    });
    root.querySelectorAll("[data-js]").forEach((el) => el.removeAttribute("hidden"));
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
      if (menu.open && (event.target === menu || !menu.contains(event.target))) menu.open = false;
    });

    menu.querySelector("[data-menu-close]")?.addEventListener("click", () => {
      menu.open = false;
      menu.querySelector("summary")?.focus();
    });

    // What sits above the drawer varies (a notice row, the header's height), so
    // its room is measured from the bar it hangs from. The drawer itself
    // cannot be measured on open: its box is skipped while it fades in.
    const drawer = menu.querySelector(".goen-header__drawer");
    const fit = () => {
      if (!drawer || !menu.open) return;
      const bottom = menu.closest(".goen-header__bar")?.getBoundingClientRect().bottom ?? 0;
      menu.style.setProperty("--drawer-room", `${Math.max(0, window.innerHeight - bottom)}px`);
    };
    menu.addEventListener("toggle", fit);
    window.addEventListener("resize", fit);

    // The language panel opens in the drawer's flow, below the fold of a
    // drawer that scrolls: bring it into view when it opens.
    menu.addEventListener("toggle", (event) => {
      const lang = event.target;
      if (lang instanceof HTMLDetailsElement && lang !== menu && lang.open) {
        lang.scrollIntoView({ block: "nearest" });
      }
    }, true);
  }

  /*
   * On a phone the department row scrolls sideways; the current department is
   * brought to its middle so the visitor sees where they are. The row's width
   * settles when the fonts arrive, so it is set again then.
   */
  function departmentRow() {
    const row = document.querySelector(".goen-header__nav");
    const current = row?.querySelector('[aria-current="page"]');
    if (!row || !current) return;
    const centre = () => {
      if (row.scrollWidth <= row.clientWidth) return;
      const at = current.getBoundingClientRect().left - row.getBoundingClientRect().left + row.scrollLeft;
      row.scrollLeft = at - (row.clientWidth - current.offsetWidth) / 2;
    };
    centre();
    document.fonts?.ready.then(centre);
    // A phone's toolbar collapsing fires resize without changing the width, and
    // would snap back a row the shopper has scrolled.
    let width = window.innerWidth;
    window.addEventListener("resize", () => {
      if (window.innerWidth === width) return;
      width = window.innerWidth;
      centre();
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
    const quantity = (form) => form.querySelector("[data-stepper] input");

    // Only the maximum is left to the server: a number above the stock is
    // answered with the quantity it kept and the reason, which a native
    // validation bubble would pre-empt.
    const send = (form) => {
      const v = quantity(form).validity;
      if (v.badInput || v.valueMissing || v.rangeUnderflow || v.stepMismatch) return;
      form.noValidate = true;
      form.requestSubmit();
    };

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
        send(form);
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
        send(form);
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
    const reads = new WeakMap();
    const replacesRead = (form, ctx) => form instanceof HTMLFormElement && ctx?.request?.method === "GET"
      && form.getAttribute("hx-sync") === "this:replace";
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
    document.addEventListener("htmx:config:request", (event) => {
      const ctx = event.detail?.ctx;
      if (ctx?.request?.form?.matches(".goen-filters")) ctx.transition = false;
    });
    document.addEventListener("htmx:before:request", (event) => {
      const ctx = event.detail?.ctx;
      const form = ctx?.request?.form;
      if (!(form instanceof HTMLFormElement)) return;
      // htmx's replaced request can release its queue after the next one
      // starts. Keep ownership here so a third change still aborts the second.
      if (form.matches(".goen-filters") || replacesRead(form, ctx)) {
        reads.get(form)?.request?.abort?.();
        reads.set(form, ctx);
        if (!form.matches(".goen-filters")) {
          if (!pending.has(form)) begin(form);
          requests.set(ctx, form);
        }
        return;
      }
      // Do not delete the isConnected clause: a second press queues behind the
      // first, whose response swaps the form out of the page, and htmx then
      // issues the queued request from that detached form, where nothing is
      // pending any more. Without the clause it posts a second time and the
      // product is added twice.
      if (pending.has(form) || !ctx.sourceElement.isConnected) { event.preventDefault(); return; }
      begin(form);
      requests.set(ctx, form);
    });
    document.addEventListener("htmx:after:request", (event) => {
      const ctx = event.detail?.ctx;
      const form = ctx?.request?.form;
      if ((form?.matches(".goen-filters") || replacesRead(form, ctx)) && reads.get(form) !== ctx) event.preventDefault();
    });
    document.addEventListener("htmx:before:history:update", (event) => {
      const { sourceElement, response } = event.detail || {};
      const form = sourceElement instanceof HTMLFormElement ? sourceElement : sourceElement?.form;
      // DropEmptyParams may leave HX-Push-Url on an error response.
      if (form?.matches(".goen-filters") && response?.status >= 400) event.preventDefault();
    });
    // On document, because the source element may be detached by the swap
    // before this fires and an event on a detached node never reaches us.
    document.addEventListener("htmx:finally:request", (event) => {
      const ctx = event.detail?.ctx;
      // A timeout and a replaced request both abort without a response. Only
      // the latest request may change the feedback beside the filters.
      const requestForm = ctx?.request?.form;
      const ownsRead = requestForm && reads.get(requestForm) === ctx;
      if (ownsRead) {
        reads.delete(requestForm);
        if (requestForm.matches(".goen-filters")) {
          const note = document.querySelector(".goen-filters__error");
          const raw = ctx.response?.raw;
          if (note) note.hidden = raw?.ok === true;
        }
      }
      const form = requests.get(ctx);
      if (!form) return;
      requests.delete(ctx);
      if (replacesRead(form, ctx) && !ownsRead) return;
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
   * both paths read alike. In a form that keeps the browser's validation, a
   * refused ruled field shows the message in place of the bubble; the checkout
   * opts out of it and is answered by the server, which stays authoritative.
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

    // In a form that keeps the browser's validation, a submit is blocked by it.
    // For a ruled field holding a value the rule refuses, the script gives the message instead of
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
  }

  fieldRules();

  /*
   * The checkout form carries no native validation: a submit always reaches the
   * server, which answers every refused field with its own message, in the order
   * of the form. The page it answers with takes the shopper to the first of them.
   */
  function focusRefused() {
    document
      .querySelector('form[data-focus-refused] :is(input, select, textarea, button)[aria-invalid="true"]')
      ?.focus();
  }

  focusRefused();

  /*
   * A coupon request the server refused (the limiter's 429) or that never
   * arrived swaps nothing, so the reason is written into the coupon's live
   * region from the sentences the button carries. The typed code stays.
   */
  function couponFailure() {
    document.addEventListener("htmx:finally:request", (event) => {
      const ctx = event.detail?.ctx;
      const button = ctx?.sourceElement;
      if (!button?.matches?.("[data-coupon-apply]")) return;
      const status = ctx.response?.status ?? 0;
      if (status > 0 && status < 400) return;
      const region = document.getElementById("coupon-message");
      if (!region) return;
      const note = document.createElement("p");
      note.className = "ui-error-text";
      note.textContent = status === 429 ? button.dataset.busy : button.dataset.failed;
      region.replaceChildren(note);
    });
  }

  couponFailure();
  /*
   * 「收件人同會員資料」. Ticking it is an explicit request: it puts the account's
   * name and phone in the recipient fields, over whatever they held, after
   * remembering that in the form's two hidden fields. Unticking puts it back, but
   * only into a field that still holds the account's value, so nothing typed
   * since is wiped. It fills from data the server rendered on the box, so it
   * costs no request and puts nothing in a URL. The box is kept honest: it shows
   * ticked only while the fields hold the account's values. Without this file
   * the server applies the same rules through the box's 更新 button.
   */
  function recipientBox() {
    const field = (name) => document.querySelector(`#checkout-form [name="${name}"]`);
    // The input events set() dispatches would otherwise untick the box between
    // the name and the phone, while the phone still holds the old value.
    let filling = false;
    const set = (name, value) => {
      const input = field(name);
      if (!input) return;
      input.value = value;
      input.dispatchEvent(new Event("input", { bubbles: true }));
    };
    const syncBox = () => {
      const me = document.querySelector("[data-recipient-me]");
      if (!me || filling) return;
      const own = { name: me.dataset.name, phone: me.dataset.phone };
      const holds = (n) => own[n] === "" || field(n)?.value === own[n];
      me.checked = (own.name !== "" || own.phone !== "") && holds("name") && holds("phone");
    };
    document.addEventListener("change", (event) => {
      const me = event.target;
      if (!(me instanceof HTMLInputElement) || !me.matches("[data-recipient-me]")) return;
      const own = { name: me.dataset.name, phone: me.dataset.phone };
      const on = me.checked;
      filling = true;
      for (const n of ["name", "phone"]) {
        const input = field(n);
        const prev = field(`recipient_prev_${n}`);
        if (!input || own[n] === "") continue;
        if (on) {
          if (input.value !== own[n] && prev) prev.value = input.value;
          set(n, own[n]);
        } else if (input.value === own[n]) {
          set(n, prev?.value ?? "");
          if (prev) prev.value = "";
        }
      }
      if (on && field("email")?.value === "") set("email", me.dataset.email ?? "");
      filling = false;
      syncBox();
    });
    document.addEventListener("input", (event) => {
      if (event.target instanceof HTMLInputElement && ["name", "phone"].includes(event.target.name)) syncBox();
    });
  }

  /*
   * Swaps replace both the bar and its target. Whether the bar shows is kept on
   * <html>, which a swap does not replace, so a new bar is born in the state
   * the old one left and never slides in again.
   */
  function buyBar() {
    const root = document.documentElement;
    let observer = null;
    const bind = () => {
      observer?.disconnect();
      observer = null;
      const bar = document.getElementById("buybar");
      const target = bar ? document.getElementById(bar.dataset.follows ?? "") : null;
      if (!bar || !target || !("IntersectionObserver" in window)) return;
      observer = new IntersectionObserver((entries) => {
        root.dataset.buybar = entries[entries.length - 1].isIntersecting ? "in" : "out";
      });
      observer.observe(target);
    };
    bind();
    document.addEventListener("htmx:after:swap", bind);
  }

  /*
   * The sign-in page's demo account. Its credentials are printed as text for a
   * browser without this file; here the button appears, puts them in the form
   * and signs in with it, as its label says.
   */
  function demoAccount() {
    const fill = document.querySelector("[data-demo-fill]");
    const form = document.querySelector('form[action="/signin"]');
    if (!(fill instanceof HTMLButtonElement) || !(form instanceof HTMLFormElement)) return;
    fill.hidden = false;
    fill.addEventListener("click", () => {
      for (const name of ["email", "password"]) {
        const input = form.elements.namedItem(name);
        if (!(input instanceof HTMLInputElement)) continue;
        input.value = fill.dataset[name] ?? "";
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }
      form.requestSubmit();
    });
  }

  /*
   * The charts' readout: pointing at a day writes that day's row of the chart's
   * own table into one line under the plot, values first, then the series
   * names, then the date and any note. It repeats the table and nothing more;
   * without this the table is the way to read a chart. Escape puts it away, and
   * it stays while the pointer is over it.
   */
  function chartReadout() {
    const NS = "http://www.w3.org/2000/svg";
    const readouts = new Map();

    for (const fig of document.querySelectorAll(".goen-chart")) {
      const frame = fig.querySelector(".goen-chart__frame");
      const table = fig.querySelector(".goen-chart__table");
      const hits = [...fig.querySelectorAll(".goen-chart__hit")];
      const body = table?.tBodies[0];
      if (!frame || !body || !hits.length) continue;
      const heads = [...table.tHead.rows[0].cells];
      const line = fig.querySelector(".goen-chart__readout");
      if (!line) continue;

      const rowText = (row) => {
        const series = [];
        const notes = [];
        for (const [i, head] of heads.entries()) {
          const text = row.cells[i]?.textContent.trim();
          if (!text) continue;
          if (head.dataset.readout === "series") series.push(text + " " + head.textContent.trim());
          else if (head.dataset.readout === "note") notes.push(text);
        }
        return [...series, row.cells[0].textContent.trim(), ...notes].join(" · ");
      };
      // Overlapping hidden rows reserve the tallest readout at the current
      // width and font size; a fixed line count cannot bound campaign names.
      const space = document.createElement("div");
      space.className = "goen-chart__readout-space";
      const size = line.cloneNode(false);
      size.classList.add("goen-chart__readout-size");
      size.setAttribute("aria-hidden", "true");
      for (const row of body.rows) {
        const sample = document.createElement("span");
        sample.textContent = rowText(row);
        size.append(sample);
      }
      line.replaceWith(space);
      space.append(line, size);

      const plot = hits[0].ownerSVGElement;
      const crosshair = hits[0].dataset.x === undefined ? null : document.createElementNS(NS, "line");
      if (crosshair) {
        crosshair.setAttribute("class", "goen-chart__crosshair");
        crosshair.setAttribute("y1", hits[0].getAttribute("y"));
        crosshair.setAttribute("y2", String(+hits[0].getAttribute("y") + +hits[0].getAttribute("height")));
        crosshair.setAttribute("visibility", "hidden");
        plot.insertBefore(crosshair, hits[0]);
      }

      let on = null;
      const hide = () => {
        on?.classList.remove("goen-chart__hit--on");
        on = null;
        line.textContent = "";
        crosshair?.setAttribute("visibility", "hidden");
      };
      const show = (hit) => {
        const row = body.rows[Number(hit.dataset.row)];
        if (!row) return;
        on?.classList.remove("goen-chart__hit--on");
        on = hit;
        line.textContent = rowText(row);
        if (crosshair) {
          crosshair.setAttribute("x1", hit.dataset.x);
          crosshair.setAttribute("x2", hit.dataset.x);
          crosshair.setAttribute("visibility", "visible");
        } else {
          hit.classList.add("goen-chart__hit--on");
        }
        readouts.set(fig, hide);
      };

      for (const hit of hits) {
        hit.addEventListener("pointerenter", () => show(hit));
        hit.addEventListener("pointerdown", () => show(hit));
      }
      const leave = (e) => {
        if (e.pointerType === "mouse" && !frame.contains(e.relatedTarget) && !line.contains(e.relatedTarget)) {
          hide();
          readouts.delete(fig);
        }
      };
      frame.addEventListener("pointerleave", leave);
      line.addEventListener("pointerleave", leave);
    }

    document.addEventListener("keydown", (e) => {
      if (e.key !== "Escape") return;
      for (const hide of readouts.values()) hide();
      readouts.clear();
    });
    document.addEventListener("pointerdown", (e) => {
      for (const [fig, hide] of [...readouts]) {
        if (fig.contains(e.target)) continue;
        hide();
        readouts.delete(fig);
      }
    });
  }

  recipientBox();
  demoAccount();
  handoff();
  buyBar();
  headerMenu();
  departmentRow();
  departmentPanels();
  popovers();
  stepper();
  carousel();
  chartReadout();
})();
