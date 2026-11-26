/*
 * goen — the one script the storefront and the back office share.
 *
 * Every write is a plain <form method="post"> that the server answers on its
 * own, so nothing here decides whether a write happens. The storefront assumes
 * this file runs: the menu is a dialog it opens, and the search field's
 * shortcut is its own.
 */
(() => {
  "use strict";

  /*
   * htmx 4 swaps every response but 204 and 304, so a rejected form's 422 with
   * its field errors replaces the old form with nothing configured here.
   */

  /*
   * A card's colours. Where a colour has a photograph of its own, its swatch
   * is a button that puts that photograph on the card, in place, and marks
   * itself pressed; the card's link still leads to the product. On the
   * document, so a shelf htmx swaps in later is served too.
   */
  function cardSwatches() {
    document.addEventListener("click", (event) => {
      const swatch = event.target.closest?.("[data-swatch]");
      if (!(swatch instanceof HTMLButtonElement)) return;
      const img = swatch.closest(".goen-tile__cell")?.querySelector(".goen-tile__img");
      if (!(img instanceof HTMLImageElement) || !swatch.dataset.swatch) return;
      // srcset first: with it set, src is only the fallback.
      if (swatch.dataset.swatchSrcset) img.srcset = swatch.dataset.swatchSrcset;
      else img.removeAttribute("srcset");
      img.src = swatch.dataset.swatch;
      for (const other of swatch.parentElement.querySelectorAll("[data-swatch]")) {
        other.setAttribute("aria-pressed", String(other === swatch));
      }
    });
  }

  /*
   * A card's quick add answers with the product page, from which htmx takes the
   * outcome and the header's cart link. The home page's free-delivery meter is
   * not on that page, so a quick add that went through tells the page, and the
   * meter reads itself again (hx-trigger="goen:cart from:body").
   */
  function cartChanged() {
    document.addEventListener("htmx:after:request", (event) => {
      const source = event.detail?.ctx?.sourceElement;
      const form = source?.closest?.("form[data-quick-add]");
      const status = event.detail?.ctx?.response?.status;
      if (!form || !(status >= 200 && status < 300)) return;
      document.body.dispatchEvent(new CustomEvent("goen:cart"));
    });
  }

  /*
   * The menu is a modal <dialog>. The browser keeps focus inside it, closes it
   * on Escape and gives focus back to the button that opened it; what it does
   * not do is open from that button, close from the drawer's own close button,
   * or close on a press on the backdrop, which lands on the dialog itself.
   *
   * Closing waits for the slide out where the browser can keep a closing
   * dialog in the top layer until then (transition-behavior: allow-discrete on
   * display and overlay); elsewhere it closes at once rather than vanish
   * mid-slide.
   */
  function menu() {
    const dialog = document.getElementById("site-menu");
    const opener = document.querySelector("[data-menu-open]");
    if (typeof dialog?.showModal !== "function" || !opener) return;

    opener.setAttribute("aria-expanded", "false");
    opener.addEventListener("click", () => {
      dialog.showModal();
      opener.setAttribute("aria-expanded", "true");
    });
    dialog.addEventListener("close", () => opener.setAttribute("aria-expanded", "false"));
    dialog.querySelector("[data-menu-close]")?.addEventListener("click", () => dialog.close());
    dialog.addEventListener("click", (event) => {
      if (event.target === dialog) dialog.close();
    });
  }

  /*
   * The list under the search field, which htmx fills as the shopper types. The
   * field is a combobox over it: ArrowDown and ArrowUp move the active option,
   * Enter opens it, Escape closes the list, and leaving the form closes it too.
   * The list shows whenever it says something, so it is read from its contents
   * after each swap rather than from htmx's events.
   */
  function searchSuggest() {
    const field = document.getElementById("site-search");
    const panel = document.getElementById("site-search-suggest");
    const form = field?.form;
    if (!(field instanceof HTMLInputElement) || !panel || !form) return;
    let active = -1;
    const options = () => [...panel.querySelectorAll('[role="option"]')];
    const choose = (index) => {
      const all = options();
      active = index;
      all.forEach((option, at) => option.setAttribute("aria-selected", String(at === index)));
      const chosen = all[index];
      if (chosen) {
        field.setAttribute("aria-activedescendant", chosen.id);
        chosen.scrollIntoView({ block: "nearest" });
      } else {
        field.removeAttribute("aria-activedescendant");
      }
    };
    const show = (open) => {
      panel.hidden = !open;
      field.setAttribute("aria-expanded", String(open && options().length > 0));
      if (!open) choose(-1);
    };
    const says = () => field.value.trim() !== "" && panel.textContent.trim() !== "";
    new MutationObserver(() => {
      active = -1;
      field.removeAttribute("aria-activedescendant");
      show(document.activeElement === field && says());
    }).observe(panel, { childList: true });

    field.addEventListener("focus", () => show(says()));
    field.addEventListener("keydown", (event) => {
      const all = options();
      if (event.key === "ArrowDown" && all.length) {
        event.preventDefault();
        show(true);
        choose(Math.min(active + 1, all.length - 1));
      } else if (event.key === "ArrowUp" && all.length && !panel.hidden) {
        event.preventDefault();
        choose(active - 1);
      } else if (event.key === "Enter" && active >= 0 && !panel.hidden) {
        const link = all[active]?.querySelector("a");
        if (link) {
          event.preventDefault();
          location.assign(link.href);
        }
      } else if (event.key === "Escape" && !panel.hidden) {
        event.preventDefault();
        show(false);
      }
    });
    form.addEventListener("focusout", (event) => {
      if (!(event.relatedTarget instanceof Node) || !form.contains(event.relatedTarget)) show(false);
    });
  }

  /*
   * The bar follows the scroll only while it is short: at large text or in a
   * short window it grows past 12% of the window, and a bar that tall stuck
   * over the page would hide what has focus under it. Measured rather than
   * queried, because no media query sees the reader's text size.
   */
  function stickyBar() {
    const header = document.querySelector(".goen-header");
    if (!header) return;
    const fit = () => header.toggleAttribute("data-static", header.offsetHeight > window.innerHeight * 0.12);
    fit();
    new ResizeObserver(fit).observe(header);
    window.addEventListener("resize", fit);
  }

  /*
   * The search field's shortcut: ⌘K on a Mac, Ctrl K elsewhere, named in the
   * field so it can be learnt. A shortcut that is already the page's, inside a
   * field being typed in, is left alone.
   */
  function searchShortcut() {
    const field = document.getElementById("site-search");
    if (!(field instanceof HTMLInputElement)) return;
    const header = field.closest(".goen-header");
    const opener = document.querySelector("[data-search-open]");
    // Below 640 the field is under the bar only once its button is pressed;
    // the shortcut opens it the same way.
    const reveal = (open) => {
      header?.toggleAttribute("data-search-open", open);
      opener?.setAttribute("aria-expanded", String(open));
    };
    opener?.addEventListener("click", () => {
      const open = !header?.hasAttribute("data-search-open");
      reveal(open);
      if (open) field.focus();
    });
    const mac = /Mac|iPhone|iPad/.test(navigator.userAgentData?.platform ?? navigator.platform);
    const hint = field.form?.querySelector("[data-shortcut]");
    if (hint) {
      hint.textContent = mac ? "\u2318K" : "Ctrl K";
      hint.hidden = false;
    }
    document.addEventListener("keydown", (event) => {
      if (event.key.toLowerCase() !== "k" || event.altKey || event.shiftKey) return;
      if (!(mac ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey)) return;
      event.preventDefault();
      reveal(true);
      field.focus();
      field.select();
    });
  }

  /*
   * On a phone the department row scrolls sideways; the current department is
   * brought to its start, after the gutter, so the visitor sees where they are
   * and no word is cut at the edge. The row's width settles when the fonts
   * arrive, so it is set again then.
   */
  function departmentRow() {
    const row = document.querySelector(".goen-header__nav");
    const current = row?.querySelector('[aria-current="page"]');
    if (!row || !current) return;
    const links = row.querySelectorAll(":scope > a");
    const start = (link) => link.getBoundingClientRect().left - row.getBoundingClientRect().left + row.scrollLeft;
    // The current department starts where the row's own gutter does, with the
    // one before it out of sight behind the edge: centring it left half a
    // word of that one showing.
    const centre = () => {
      const room = row.scrollWidth - row.clientWidth;
      if (room <= 0) return;
      const gutter = parseFloat(getComputedStyle(row).scrollPaddingInlineStart) || 0;
      let to = Math.min(Math.max(start(current) - gutter, 0), room);
      // Among the last the row cannot go as far, and a department can be left
      // straddling the edge: that one starts at the gutter instead, while the
      // current one stays whole in view.
      const cut = Array.from(links).find((link) => start(link) < to - 0.5 && start(link) + link.offsetWidth > to + 0.5);
      if (cut && start(current) + current.offsetWidth <= start(cut) - gutter + row.clientWidth) to = Math.max(start(cut) - gutter, 0);
      row.scrollLeft = to;
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
   * with the focus returned to its button, closing when focus leaves or on a
   * click elsewhere, and one open at a time.
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
      pop.addEventListener("focusout", (event) => {
        if (event.relatedTarget && !pop.contains(event.relatedTarget)) pop.open = false;
      });
      pop.addEventListener("toggle", () => {
        if (!pop.open) return;
        for (const other of pops) if (other !== pop) other.open = false;
      });
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
    // The buy box is replaced by each choice; ownership must survive its DOM node.
    let productChoice = null;
    const isProductChoice = (ctx) => ctx?.sourceElement?.matches("a.goen-swatch[hx-get]");
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
    // A form that changes one control in place (a filter, the compare toggle)
    // is no navigation, so it does not cross-fade the whole page.
    document.addEventListener("htmx:config:request", (event) => {
      const ctx = event.detail?.ctx;
      const form = ctx?.request?.form;
      if (form?.matches(".goen-filters") || form?.matches(".goen-comparetoggle")) ctx.transition = false;
    });
    document.addEventListener("htmx:before:request", (event) => {
      const ctx = event.detail?.ctx;
      if (isProductChoice(ctx)) {
        productChoice?.request?.abort?.();
        productChoice = ctx;
        return;
      }
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
      if (isProductChoice(ctx) && productChoice !== ctx) event.preventDefault();
      const form = ctx?.request?.form;
      if ((form?.matches(".goen-filters") || replacesRead(form, ctx)) && reads.get(form) !== ctx) event.preventDefault();
    });
    document.addEventListener("htmx:before:history:update", (event) => {
      const { sourceElement, response } = event.detail || {};
      const form = sourceElement instanceof HTMLFormElement ? sourceElement : sourceElement?.form;
      // DropEmptyParams may leave HX-Push-Url on an error response.
      if ((form?.matches(".goen-filters") || form?.matches(".goen-search-sort")) && response?.status >= 400) event.preventDefault();
    });
    // On document, because the source element may be detached by the swap
    // before this fires and an event on a detached node never reaches us.
    document.addEventListener("htmx:finally:request", (event) => {
      const ctx = event.detail?.ctx;
      if (productChoice === ctx) productChoice = null;
      // A timeout and a replaced request both abort without a response. Only
      // the latest request may change the feedback beside the form.
      const requestForm = ctx?.request?.form;
      const ownsRead = requestForm && reads.get(requestForm) === ctx;
      if (ownsRead) {
        reads.delete(requestForm);
        if (requestForm.matches(".goen-filters") || requestForm.matches(".goen-search-sort")) {
          const note = document.querySelector(requestForm.matches(".goen-search-sort")
            ? "#goen-search-sort-error" : ".goen-filters__error");
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

  // A choice changes the view, while a purchase keeps its submitted identity.
  function productResponseOrder() {
    let revision = 0;
    let outcome = null;
    const writes = new Set();
    const submissions = new WeakMap();
    const appendOutcome = (root) => {
      const form = root.querySelector("#pdp-add");
      form?.querySelector("#added")?.remove();
      if (outcome && outcome.variant === form?.querySelector('[name="variant"]')?.value)
        form.append(outcome.notice.cloneNode(true));
    };
    const isChoice = (ctx) => ctx?.sourceElement?.matches("a.goen-swatch[hx-get]");
    const isPurchase = (ctx) => ctx?.request?.method === "POST" && ctx.request.form?.id === "pdp-add";
    document.addEventListener("htmx:before:request", (event) => {
      if (event.defaultPrevented) return;
      const ctx = event.detail?.ctx;
      if (isChoice(ctx)) {
        if (writes.size === 0) outcome = null;
        revision++;
      } else if (isPurchase(ctx)) {
        submissions.set(ctx, {
          revision,
          variant: ctx.request.form.querySelector('[name="variant"]')?.value,
        });
        writes.add(ctx);
      }
    });
    document.addEventListener("htmx:after:request", (event) => {
      if (event.defaultPrevented) return;
      const ctx = event.detail?.ctx;
      if (!isChoice(ctx) && !isPurchase(ctx)) return;
      const live = document.getElementById("buybox");
      if (!live || !ctx.text) return;
      const response = new DOMParser().parseFromString(ctx.text, "text/html");
      if (!response.getElementById("buybox")) return;
      // Each request captured a target that another response may have replaced.
      ctx.target = live;
      if (isPurchase(ctx)) {
        const submitted = ctx.request.form;
        const submission = submissions.get(ctx);
        const notice = response.getElementById("added");
        outcome = notice && submission?.variant ? { variant: submission.variant, notice: notice.cloneNode(true) } : null;
        if (submission?.revision !== revision || submitted !== live.querySelector("#pdp-add")) {
          ctx.swap = "none";
          ctx.selectOOB = "#cart-link";
          // Retire the submitted form just as a normal response swap would.
          // A queued second press must not post from this still-connected form.
          if (submitted.isConnected && submitted === live.querySelector("#pdp-add")) {
            const replacement = submitted.cloneNode(true);
            replacement.removeAttribute("aria-busy");
            replacement.removeAttribute("data-request-pending");
            replacement.classList.remove(htmx.config.requestClass);
            for (const button of replacement.querySelectorAll('button[type="submit"]:not([data-feedback-skip]), input[type="submit"]')) {
              button.removeAttribute("aria-disabled");
            }
            submitted.replaceWith(replacement);
            htmx.process(replacement);
          }
          appendOutcome(live);
        }
      } else if (outcome) {
        appendOutcome(response);
        ctx.text = response.documentElement.outerHTML;
      }
    });
    document.addEventListener("htmx:finally:request", (event) => writes.delete(event.detail?.ctx));
  }

  productResponseOrder();

  /*
   * A field that carries a rule says so in its markup (data-rule, a pattern, the
   * message the server would give), all written from internal/ui/fieldrule so the
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

    // goen's formtext.FoldWidth: full-width ASCII and the ideographic space.
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
  /*
   * The buy box's price is watched too: while all of it is on screen above the
   * bar, the bar carries data-price="hidden" and shows no second price. A price
   * the bar covers is not on screen. A swapped-in bar takes the last answer at
   * once, before the observer speaks again.
   */
  function buyBar() {
    const root = document.documentElement;
    let observer = null;
    let priceObserver = null;
    let priceInView = false;
    const bind = () => {
      observer?.disconnect();
      priceObserver?.disconnect();
      observer = null;
      priceObserver = null;
      const bar = document.getElementById("buybar");
      const target = bar ? document.getElementById(bar.dataset.follows ?? "") : null;
      if (!bar || !target || !("IntersectionObserver" in window)) return;
      const price = document.querySelector("#buybox .goen-pdp__price");
      if (!price) priceInView = false;
      const mark = () => {
        if (priceInView) bar.dataset.price = "hidden";
        else delete bar.dataset.price;
      };
      mark();
      observer = new IntersectionObserver((entries) => {
        root.dataset.buybar = entries[entries.length - 1].isIntersecting ? "in" : "out";
      });
      observer.observe(target);
      if (!price) return;
      priceObserver = new IntersectionObserver((entries) => {
        priceInView = entries[entries.length - 1].isIntersecting;
        mark();
      }, { rootMargin: `0px 0px -${bar.offsetHeight}px 0px`, threshold: 1 });
      priceObserver.observe(price);
    };
    bind();
    document.addEventListener("htmx:after:swap", bind);
  }

  /*
   * A control that swaps itself away takes keyboard focus with it, and focus
   * falls to <body>. Such a control names, in data-focus-after-swap, the id the
   * swapped-in content should hand focus to. On document, because the source
   * element is detached by the swap and an event on it never reaches us. A
   * swap htmx runs inside a view transition lands after the request is
   * finished, so the hand-over is tried again once the swap is done.
   */
  function focusAfterSwap() {
    const handOver = (event) => {
      const source = event.detail?.ctx?.sourceElement;
      const id = source?.getAttribute?.("data-focus-after-swap");
      if (id && !source.isConnected) document.getElementById(id)?.focus();
    };
    document.addEventListener("htmx:finally:request", handOver);
    document.addEventListener("htmx:after:swap", handOver);
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

  /*
   * The compare bar's row of ticked products. The bar is the form's own tail,
   * which submits the ticked boxes to /compare without this; here, once the
   * form's data-compare-min are ticked, a list of them is put before the limit
   * and the submit: each one's photograph, its name, and a button that unticks
   * its box. The row is always rebuilt from the boxes, never kept beside them,
   * so a box unticked on its tile, a swap of the results or a page restored
   * from the back-forward cache cannot leave an entry behind. Nothing is
   * announced: the box's own state is what a screen reader hears.
   */
  function compareRow() {
    const ticked = (form) => Array.from(form.elements).filter((el) =>
      el instanceof HTMLInputElement && el.type === "checkbox" && el.name === "p" && el.checked);
    const glyph = () => {
      const ns = "http://www.w3.org/2000/svg";
      const svg = document.createElementNS(ns, "svg");
      svg.setAttribute("width", "12");
      svg.setAttribute("height", "12");
      svg.setAttribute("viewBox", "0 0 14 14");
      svg.setAttribute("aria-hidden", "true");
      const path = document.createElementNS(ns, "path");
      path.setAttribute("d", "M2 2l10 10M12 2 2 12");
      path.setAttribute("stroke", "currentColor");
      path.setAttribute("stroke-width", "1.5");
      svg.append(path);
      return svg;
    };
    // The tile's own photograph at its 400px rendition: the srcset's 400w
    // candidate, else whatever the tile loaded.
    const small = (img) => {
      for (const candidate of (img.getAttribute("srcset") ?? "").split(",")) {
        const [url, width] = candidate.trim().split(/\s+/);
        if (url && width === "400w") return url;
      }
      return img.getAttribute("src") ?? "";
    };
    const entry = (form, box) => {
      const cell = box.closest(".goen-tile__cell");
      const name = cell?.querySelector(".goen-tile__name")?.textContent.trim() || box.value;
      const item = document.createElement("li");
      item.className = "goen-compare-pick__item";
      item.dataset.slug = box.value;
      const frame = document.createElement("span");
      frame.className = "goen-compare-pick__photo";
      const img = cell?.querySelector(".goen-tile__img");
      if (img) {
        const photo = document.createElement("img");
        photo.src = small(img);
        photo.alt = "";
        photo.width = 56;
        photo.height = 56;
        photo.decoding = "async";
        frame.append(photo);
      }
      const label = document.createElement("span");
      label.className = "goen-compare-pick__name";
      label.textContent = name;
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "goen-compare-pick__remove";
      remove.setAttribute("aria-label", (form.dataset.compareRemove ?? "%s").replace("%s", () => name));
      remove.append(glyph());
      item.append(frame, label, remove);
      return item;
    };
    const sync = (form) => {
      const bar = form.querySelector(".goen-compare-pick__go");
      if (!bar) return;
      const boxes = ticked(form);
      let row = bar.querySelector(".goen-compare-pick__row");
      if (boxes.length < (Number(form.dataset.compareMin) || 2)) {
        row?.remove();
        return;
      }
      if (!row) {
        row = document.createElement("ul");
        row.className = "goen-compare-pick__row";
        bar.prepend(row);
      }
      const kept = new Map(Array.from(row.children, (item) => [item.dataset.slug, item]));
      boxes.forEach((box, index) => {
        const item = kept.get(box.value) ?? entry(form, box);
        kept.delete(box.value);
        if (row.children[index] !== item) row.insertBefore(item, row.children[index] ?? null);
      });
      kept.forEach((item) => item.remove());
    };
    const syncAll = () => {
      const form = document.getElementById("compare-pick");
      if (form instanceof HTMLFormElement) sync(form);
    };
    document.addEventListener("change", (event) => {
      const box = event.target;
      if (box instanceof HTMLInputElement && box.form?.id === "compare-pick") sync(box.form);
    });
    // Focus goes to the next remove button, else the one before, else the
    // submit, so it never falls to <body> with the entry it was on.
    document.addEventListener("click", (event) => {
      const remove = event.target instanceof Element ? event.target.closest(".goen-compare-pick__remove") : null;
      const form = remove?.form;
      if (!remove || !form) return;
      const slug = remove.closest(".goen-compare-pick__item")?.dataset.slug;
      const before = Array.from(form.querySelectorAll(".goen-compare-pick__remove"));
      const at = before.indexOf(remove);
      for (const box of ticked(form)) {
        if (box.value === slug) box.checked = false;
      }
      sync(form);
      const after = Array.from(form.querySelectorAll(".goen-compare-pick__remove"));
      (after[at] ?? after[at - 1] ?? form.querySelector('.goen-compare-pick__go [type="submit"]'))?.focus();
    });
    syncAll();
    document.addEventListener("htmx:after:settle", syncAll);
    window.addEventListener("pageshow", syncAll);
  }

  /*
   * A press that loads a new page is answered at once rather than when that
   * page paints: the pressed link is marked, and the root carries
   * data-navigating, from which app.css draws a bar if the load outlasts a
   * moment. Only plain same-origin loads in this tab; a click or a submit that
   * something else took over (htmx, the duplicate-submit refusal) arrives here
   * already prevented, which is why this listens after every other handler.
   */
  function navigationPending() {
    const root = document.documentElement;
    let pressed = null;
    let presses = 0;
    const start = (link) => {
      presses++;
      pressed?.removeAttribute("data-navigation-pending");
      pressed = link;
      link?.setAttribute("data-navigation-pending", "");
      root.setAttribute("data-navigating", "");
    };
    const clear = () => {
      pressed?.removeAttribute("data-navigation-pending");
      pressed = null;
      root.removeAttribute("data-navigating");
    };

    document.addEventListener("click", (e) => {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const link = e.target.closest?.("a[href]");
      if (!link || (link.target && link.target !== "_self") || link.hasAttribute("download")) return;
      const url = new URL(link.href);
      if (url.origin !== location.origin) return;
      if (url.hash && url.pathname === location.pathname && url.search === location.search) return;
      start(link);
    });
    document.addEventListener("submit", (e) => {
      if (e.defaultPrevented) return;
      const form = e.target;
      const by = e.submitter;
      const method = (by?.getAttribute("formmethod") ?? form.getAttribute("method") ?? "get").toLowerCase();
      const target = by?.getAttribute("formtarget") ?? form.getAttribute("target");
      if (method === "dialog" || (target && target !== "_self")) return;
      // A button's formAction falls back to the page's URL, not the form's action.
      const action = by?.hasAttribute("formaction") ? by.formAction : form.getAttribute("action") ?? "";
      if (new URL(action, location.href).origin === location.origin) start(null);
    });
    // Back to a page the browser kept: it returns as it was left, mid-press.
    window.addEventListener("pageshow", clear);
    // A stopped load aborts its navigation. So does a second press, which has
    // marked its own link by then and must keep it.
    window.navigation?.addEventListener("navigate", (e) => {
      const at = presses;
      e.signal.addEventListener("abort", () => {
        if (presses === at) clear();
      });
    });
  }

  /*
   * The bag drawer. After an add to the cart succeeds, from any form that
   * posts to /cart/items through htmx, the cart's own fragment fills the
   * frame's empty dialog and the dialog opens. Success is read from the
   * answer, whatever the form and wherever the server sent it: the header's
   * count in the page that came back is higher than the one on screen. The
   * fragment comes by a plain fetch, so it is not one of the page's own htmx
   * requests, and the drawer opens once that request has swapped, so what
   * had focus is the page's own new control. The browser keeps focus inside
   * the dialog, closes it on Escape and gives focus back; the close button and
   * a press on the backdrop are this script's, and so is finding the pressed
   * button again by its id when the swap replaced it while the drawer was open.
   */
  function bagDrawer() {
    const dialog = document.getElementById("bag-drawer");
    if (typeof dialog?.showModal !== "function") return;
    const units = (root) => Number.parseInt(root.querySelector("#cart-link .goen-header__count")?.textContent ?? "", 10) || 0;
    const adds = (ctx) => {
      if (String(ctx?.request?.method).toUpperCase() !== "POST") return false;
      try {
        return new URL(ctx.request.action, location.href).pathname === "/cart/items";
      } catch {
        return false;
      }
    };
    const sent = new WeakMap();
    const pending = new WeakMap();
    let turn = 0;
    let back = { element: null, id: "" };

    document.addEventListener("htmx:before:request", (event) => {
      const ctx = event.detail?.ctx;
      if (event.defaultPrevented || !adds(ctx)) return;
      const pressed = document.activeElement;
      sent.set(ctx, { units: units(document), id: pressed instanceof HTMLElement ? pressed.id : "" });
    });
    document.addEventListener("htmx:after:request", (event) => {
      const ctx = event.detail?.ctx;
      const before = sent.get(ctx);
      if (!before || !(ctx.response?.status < 400) || !ctx.text) return;
      const answer = new DOMParser().parseFromString(ctx.text, "text/html");
      if (units(answer) <= before.units) return;
      const mine = ++turn;
      pending.set(ctx, {
        mine, id: before.id,
        body: fetch("/cart/drawer", { credentials: "same-origin", headers: { Accept: "text/html" } })
          .then((response) => (response.ok ? response.text() : ""))
          .then((text) => new DOMParser().parseFromString(text, "text/html").getElementById("bag-drawer-body"))
          .catch(() => null),
      });
    });
    document.addEventListener("htmx:finally:request", async (event) => {
      const ctx = event.detail?.ctx;
      const opening = pending.get(ctx);
      if (!opening) return;
      pending.delete(ctx);
      const body = await opening.body;
      // A later add, or a fragment that did not come, opens nothing here.
      if (!body || opening.mine !== turn) return;
      dialog.replaceChildren(document.importNode(body, true));
      if (dialog.open) return;
      const focused = document.activeElement;
      back = { element: focused instanceof HTMLElement && focused !== document.body ? focused : null, id: opening.id };
      dialog.showModal();
    });

    dialog.addEventListener("click", (event) => {
      if (event.target === dialog || event.target.closest?.("[data-bag-close]")) dialog.close();
    });
    dialog.addEventListener("close", () => {
      const landed = document.activeElement;
      if (landed && landed !== document.body && !dialog.contains(landed)) return;
      const again = back.element?.isConnected ? back.element : back.id ? document.getElementById(back.id) : null;
      again?.focus();
    });
  }

  /*
   * The product page's add button says what the press costs: the unit price
   * times the quantity beside it. The server writes it for one, as the field
   * starts; a step or a typed number changes it here. A number the field
   * cannot read leaves the sum as it was.
   */
  function linePrice() {
    const sum = (cents) => "NT$" + Math.trunc(cents / 100).toLocaleString("en-US");
    const update = (event) => {
      const field = event.target;
      if (!(field instanceof HTMLInputElement) || !field.matches("#pdp-add [data-stepper] input")) return;
      const price = field.form?.querySelector("[data-unit-cents]");
      const quantity = Number(field.value);
      if (!price || field.validity.badInput || !Number.isInteger(quantity) || quantity < 1) return;
      price.textContent = sum(Number(price.dataset.unitCents) * quantity);
    };
    document.addEventListener("input", update);
    document.addEventListener("change", update);
  }

  recipientBox();
  demoAccount();
  handoff();
  buyBar();
  linePrice();
  focusAfterSwap();
  menu();
  stickyBar();
  searchShortcut();
  searchSuggest();
  departmentRow();
  popovers();
  stepper();
  cardSwatches();
  cartChanged();
  chartReadout();
  compareRow();
  navigationPending();
  bagDrawer();
})();
