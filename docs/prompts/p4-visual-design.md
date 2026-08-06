# P4 — Visual design direction

> Self-contained brief. Assume no context beyond this file and the repository.
> **For:** Claude Design · **Order:** any time; this changes the product rather than the
> code.

This is a **design brief**, not a research task. It is the one brief in this set that
does not end with the three lists — it delivers artefacts instead.

## What exists

`goen` is a Traditional-Chinese 3C 選品店 — a curated shop, not a marketplace. Its
whole promise is 規格看得懂: specs you can actually understand, a comparison table, and
honest copy. Server-rendered HTML, no client framework, htmx only as enhancement.

The current visual language is the **koopa.dev design system**, vendored verbatim at
`assets/css/ds/` — plain CSS with `oklch` tokens, no Tailwind, no build step. Page
composition lives in `assets/css/app/app.css` as real classes (`goen-*`; `ui-*` belongs
to the design system). The 1440 and 375 artboards are folded into one responsive
document.

The owner's assessment: **it is too restrained and too conservative.** The reference
points they want considered are the **Apple Store** and the **Google Store**.

## What to produce

A design direction for the storefront, delivered as `.dc.html` in the Claude Design
project, covering at minimum:

- **Home** — hero, category tiles, recommended grid, guarantee strip, promotional strip
- **Listing / search results** — including the facet panel
- **Product detail** — gallery, variant picker, spec table, reviews, Q&A, buy panel
- **Compare** — two to four products side by side
- **Cart and checkout** — including the delivery-method chooser and the address form
- **The account hub**

## The constraints that are not negotiable

These come from the architecture, and a design that violates them cannot be built here:

1. **Every mutation is a plain `<form method="post">` that works with scripting off.**
   No design that depends on a click handler to submit, to open a picker, or to reveal a
   required field. The variant picker is a set of LINKS; the delivery-method chooser is
   a set of LINKS. A carousel that needs JavaScript to rotate cannot be the hero.
2. **No second client framework, no second CSS framework, no component library with a
   runtime, no second icon system.** Anything you draw has to be expressible as CSS over
   the existing design system, or as an addition to that system.
3. **Two locales, one layout.** Traditional Chinese and English, and Chinese text is
   typically shorter — a design tuned to English line lengths breaks. Product names,
   descriptions and policy prose may be Chinese on an English page by deliberate policy.
4. **Real content, not lorem.** Prices in NT$, real-length Chinese product names, spec
   labels like 螢幕 / 6.3" OLED.
5. **Accessibility is gated in CI**: exactly one `h1`, no skipped heading level, every
   `img` has an `alt` attribute, every control and link has an accessible name, no
   positive `tabindex`. Design with that in mind rather than around it.

## What to say, not just draw

- What specifically makes Apple/Google Store feel confident, and which of those moves
  are available **without** JavaScript?
- Where the current design is conservative in a way that reads as unfinished, versus
  conservative in a way that reads as deliberate — those are different, and only the
  first should change.
- Typography and spacing scale: what changes, and what that costs in the CJK case.
- Photography direction, since imagery is currently a placeholder.

## Image assets (a separate brief, for whoever generates them)

List exactly what is needed: subject, aspect ratio, and the pixel widths the srcset
serves (the renderer's allowlist is currently 400 and 800, source bounded at 40 MP /
8000 px). Product photography for a 3C catalogue — phones, headphones, cases, cables —
plus hero imagery and category tiles. Say what must be consistent across the set
(background, lighting, angle) so they read as one shop.
