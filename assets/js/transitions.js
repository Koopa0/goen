/*
 * A photo that is clicked on one page and shown on the next travels between
 * the two (cross-document view transitions). The names are set from here by
 * CSSOM, because the page's CSP refuses inline styles, and only for the one
 * navigation: a name left on an element, or two elements sharing one, would
 * make the browser skip the transition. Without support, or with reduced
 * motion, nothing is named and the pages simply dissolve.
 *
 * This is its own script, loaded without defer: pagereveal fires at the first
 * render, which a deferred script can miss. It runs before the body exists, so
 * every element is looked up inside a handler.
 */
(() => {
  "use strict";

  if (!("onpageswap" in window) || !("onpagereveal" in window)) return;

  const links = 'a[href^="/p/"], a[href^="/c/"]';
  const reduced = matchMedia("(prefers-reduced-motion: reduce)");
  const named = new Set();
  let pressed = null;

  const kind = (path) => (path.startsWith("/p/") ? "product-photo" : path.startsWith("/c/") ? "department-photo" : "");
  const name = (img, photo) => {
    if (!img || !photo) return;
    img.style.viewTransitionName = photo;
    img.style.viewTransitionClass = "photo";
    named.add(img);
  };
  const clear = () => {
    for (const img of named) {
      img.style.viewTransitionName = "";
      img.style.viewTransitionClass = "";
    }
    named.clear();
  };
  // The photo this page shows for itself: the chosen shot of a product, the
  // photo of a department's head.
  const own = () => {
    if (location.pathname.startsWith("/p/")) {
      return [...document.querySelectorAll("#gallery .goen-pdp__shotimg")]
        .find((img) => getComputedStyle(img.closest(".goen-pdp__shot") ?? img).visibility !== "hidden");
    }
    if (location.pathname.startsWith("/c/")) return document.querySelector("img.goen-pagehead__photo");
  };
  const inView = (el) => {
    const box = el.getBoundingClientRect();
    return box.width > 0 && box.top >= 0 && box.left >= 0 && box.bottom <= innerHeight && box.right <= innerWidth;
  };

  document.addEventListener("click", (e) => {
    const plain = e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey;
    pressed = plain ? e.target.closest?.(links) ?? null : null;
  }, true);

  window.addEventListener("pageswap", (e) => {
    if (!e.viewTransition || reduced.matches || !e.activation?.entry) return;
    const to = new URL(e.activation.entry.url).pathname;
    if (e.activation.navigationType === "traverse") name(own(), kind(location.pathname));
    else if (pressed && pressed.pathname === to) name(pressed.querySelector("img"), kind(to));
    const done = () => clear();
    e.viewTransition.finished.then(done, done);
  });

  window.addEventListener("pagereveal", (e) => {
    clear();
    if (!e.viewTransition || reduced.matches) return;
    const activation = window.navigation?.activation;
    if (activation?.navigationType === "traverse") {
      // Back: only a card that is in view, so the photo never flies off screen.
      const from = activation.from ? new URL(activation.from.url).pathname : "";
      const card = [...document.querySelectorAll(links)].find((a) => a.pathname === from && a.querySelector("img") && inView(a.querySelector("img")));
      name(card?.querySelector("img"), kind(from));
    } else {
      name(own(), kind(location.pathname));
    }
    const done = () => clear();
    e.viewTransition.ready.then(done, done);
  });
})();
