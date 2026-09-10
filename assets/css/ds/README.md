# Design System

A neutral, **multi-product** design system: one token contract + one class-based component layer that every product shares, re-skinned per product by swapping a single accent hue. It powers an e-commerce store, a SaaS app, a BaaS console, auth surfaces, and **koopa.dev** — one product among them.

## Architecture

- **Core** — `styles.css` loads the neutral tokens (`colors_and_type.css`) + the component layer (`components/*.css`, all `ui-*`). Brand-agnostic; references only semantic tokens.
- **Themes** (`themes/accents.css`) — a product picks its brand by adding one class: `<html class="theme-koopa">`. A theme sets only `--accent-h` (hue) and optional `--accent-c` (chroma); light/dark lightness is automatic. One line per product.
- **Packs** (`packs/*.css`) — opt-in domain components: `commerce`, `saas`, `baas`, `auth`, plus `koopa` (content-types + actor identities). A product loads only the packs it needs.

> The rest of this document describes the **koopa.dev product** specifically — its voice, hexagon motif, and content model. Treat it as the reference for that one product/theme, not the neutral core.

## Sources

- **Repo:** `Koopa0/koopa` (private). The Angular 21 frontend lives under `frontend/` with global styles in `frontend/src/styles.css`, the shell layout in `frontend/src/app/app.html`, and real article content in `frontend/public/content/articles/`.
- **Brand docs:** `README.md` (product positioning, tone) and `CLAUDE.md` (content types, tech stack).
- **Logo + glyph:** `frontend/public/logo.png`, `logo-title.png`, `logo-notitle.png`, `koopa.png` — all imported into `assets/`.
- **Brief:** greenfield exploration of public site, admin UI, and design system; dark mode default, light mode supported.

## Product, in one paragraph

koopa is **not a blog**. It is a semantic runtime — goals, projects, tasks, learning observations, content — that multiple AI agents read from and write to through MCP. The public site is the selectively-published tip of that iceberg (articles, essays, build-logs, TILs, notes, bookmarks, digests), organised by **topic**, not by date. The admin is a dense workspace the owner uses daily to review AI drafts, curate RSS, manage the learning engine (FSRS spaced repetition + cognitive observations), and run the content pipeline.

Two users, two completely different pressures on the system:
1. **Koopa (owner)** lives in the admin — needs Linear / Notion-admin density.
2. **Visitors** (developers, peers) arrive with intent — need calm, long-form legibility.

One design system has to serve both without compromising either.

---

## Content fundamentals

Voice is **writerly, opinionated, slightly weird, human**. The product README is the clearest voice reference.

**Tone guide:**
- **Prose > slogans.** Sentences are long when they need to be. Headings are declarative, not hype-y. "How it works" beats "Features that accelerate you."
- **Opinionated, with reasoning.** Every design-philosophy claim is followed by *why*. Example from the source README: "a system that makes decisions for you eventually makes you worse at making decisions yourself." Don't ship a bullet without the thinking behind it.
- **Precise vocabulary.** Words have defined meanings: *Goal ≠ Project ≠ Todo*. *Task ≠ Todo*. *Attempt ≠ Plan completion*. *Observation ≠ Hypothesis*. This system avoids jargon buzzwords but is rigorous about its own terminology.
- **Technical without bragging.** Names tools by name (Genkit, pgvector, FSRS, MCP) without jargon-dropping. Mentions Go stdlib-first, no frameworks, no DDD.
- **Sparingly poetic.** Phrases like "a quiet instrument", "let the work speak", "the system preserves your ownership". Use once per page; never twice.
- **Person:** first-person singular on the public site ("I", "my"). Second-person "you" on admin when addressing the owner. Never "we" — it's one person.
- **Casing:** Sentence case for titles. `Build Log`, not `BUILD LOG`. Product/agent names are capitalised (HQ, Content Studio, Learning Studio, Claude Code).
- **Dates:** Relative on admin ("2 days ago", "in review"). Absolute on public pages (`2025-03-14` ISO, or `Mar 14, 2025`).

**Things it is NOT:**
- ❌ SaaS-landing-page hero copy ("Transform your workflow")
- ❌ Corporate-safe hedging ("Designed to help you...")
- ❌ Medium-clone reading-time-first ("5 min read · 💡 Productivity")
- ❌ Emoji in body copy (see Iconography below)
- ❌ AI-slop maximalism (overwrought gradients + glass + "Powered by AI")

**Specific examples from source:**
- ✅ "It is not a blog. It is not a to-do app. It is not an LLM wrapper with a database behind it."
- ✅ "The AI doesn't remember that you mentioned a project last week. It reads the project's current status..."
- ✅ "auto-carryover is convenient, but it silently erodes your relationship with your own commitments."
- ✅ Content-type labels are plain: `article`, `essay`, `build-log`, `til`, `note`, `bookmark`, `digest`. Lowercase, hyphenated, no icons mandatory.

---

## Visual foundations

### Motif: the hexagon shell
The Koopa logo is a turtle with a **hexagon shell and antenna**. Hexagons are the one ornamental element that carries brand without looking like generic geometry. Use them: as empty states, as the tile shape behind status badges, as section dividers (hexagon + hairline), as the fallback avatar. Never as decorative hero background — the brief is allergic to that.

### Color
**Warm paper, light-first.** The neutral core now runs on **warm olive** neutrals (oklch hue ~107) with a **light default** (`#edeae2`-family paper) and a dark twin via `[data-theme="dark"]`. Accent is a single **cyan-teal** pulled straight from the logo — used surgically for links, active states, and the one "now" indicator. Semantic colors (success/warn/error/info) are muted — they never out-shout the accent.

> The **public site** ships its own self-scoped editorial skin (`packs/editorial.css`, class `.ed`) with exact hex tokens — warm paper `#edeae2` + a dark twin on `html.public-dark`. That layer is the source of truth for koopa0.dev's look; the warm core keeps admin/other-product surfaces in the same family.

- **Surfaces:** 4 elevation tokens — `bg`, `panel`, `elevated`, `overlay`. Light default sits high (warm paper); the dark twin drops to oklch lightness 0.15 – 0.31.
- **Text:** 4 tiers — `fg`, `fg-muted`, `fg-subtle`, `fg-faint`. High contrast at top; `fg-faint` is for timestamps and supporting metadata only.
- **Accent:** cyan-teal (hue ~210); darker on paper (`#2c7c91`) for contrast, lifted (`#5cb1c6`) on the dark twin. Paired with a `/14` tint for backgrounds and a `--link` step that clears WCAG AA for body links.
- **Dark twin:** Mirror. Warm charcoal `#14130f` ground, cream inks. Toggled by `[data-theme="dark"]` (core) / `html.public-dark` (editorial).

### Typography
- **Display:** `Space Grotesk` — admin/UI headings (`.h1`–`.h4`), tight tracking, lets the admin feel precise.
- **Editorial voice:** `Newsreader` — the public site's single serif face, used for **both** oversized display statements and long-form article prose. This transitional serif at display weight (~440) is the koopa0.dev hero you see; the same face reads the body. Swapping in a serif for editorial is the biggest signal that reading is respected here.
- **Body / UI:** `Inter` for dense UI and admin.
- **Mono:** `JetBrains Mono` — code, terminals, IDs, shortcuts, and the public site's metadata rail (nav, dates, labels).
- **Scale** (modular, 1.2 minor-third for UI, 1.25 major-third for editorial):
  - UI: 11 / 12 / 13 / 14 / 16 / 18
  - Editorial: 14 / 16 / 17 / 20 / 24 / 30 / 38 / 48
- **Measure:** article body max-width 680px. Admin tables unbounded; admin prose 640px.

### Spacing, radii, shadows
- **Spacing scale:** 2 / 4 / 6 / 8 / 12 / 16 / 20 / 24 / 32 / 40 / 56 / 80. The low end (2–8) serves dense tables; the high end (40–80) serves reading pages. One scale, two usage patterns.
- **Radii:** small `2px` (inputs, chips, small buttons), medium `6px` (cards, panels), large `12px` (dialogs, hero blocks). **No pill buttons.** Never `rounded-full` except on avatars. The brief specifically excludes the "rounded-xl everywhere" SaaS look.
- **Borders:** 1px hairlines, `fg-faint` alpha. Cards mostly rely on border + background, not shadow.
- **Shadows:** only two levels. `shadow-1` for menus/dropdowns, `shadow-2` for dialogs/toasts. Both use true-black with low alpha in dark mode, cool-gray in light. No "glowy" or colored shadows.

### Backgrounds & texture
- **Grain overlay:** A faint (opacity `0.03`) noise SVG is composited on top of the app globally. This is lifted directly from the source — it's the single thing that keeps the UI from reading as clinical. Keep it.
- **No gradients** in fills. No glassmorphism. No full-bleed photo heroes. No illustrations. The product doesn't ship marketing imagery — it ships ideas.
- **Monochrome imagery** only where imagery is unavoidable (Uses page, Projects). Photos are desaturated and warmly tinted.

### Motion
Restrained. Explicit list:
- **Route transitions:** 100ms fade-out / 150ms fade-in, content-area only (lifted from source).
- **Menus / popovers:** 120ms slide-down, ease-out.
- **Hover:** color-only, 120ms ease. No scale, no lift.
- **Press:** 60ms, background goes one surface-step deeper. No scale.
- **Toasts:** 200ms slide-in from top.
- **Nothing bounces.** Cubic-bezier is `(0.2, 0, 0, 1)` or linear for opacity-only.
- **Reduced-motion:** all durations collapse to 0.01ms per the source CSS.

### Hover / press / focus states
- **Hover:** subtle background tint (`fg/5` in dark, `fg/8` in light) OR text color shift from `fg-muted` to `fg`. Never both.
- **Press:** one surface-step deeper background, no transform.
- **Focus:** visible 2px outline, `zinc-400` / `zinc-600` respectively. Global rule, zero-specificity. **Critical** — this exists in the source CSS and must be preserved.
- **Disabled:** 40% opacity, `cursor-not-allowed`.

### Transparency & blur
- **Header backdrop:** `bg-zinc-950/80` + `backdrop-blur-md`. Only place blur is used.
- **Overlays:** solid panels with borders. Modals dim with `black/60` scrim, no blur.
- **Never** rely on transparency for hierarchy — borders and backgrounds do that.

### Layout rules
- Max content width 1280px (`max-w-7xl` in source). Admin uses full viewport.
- Sticky header with `backdrop-blur`. Footer hairline-separated.
- Content padding: 16px mobile, 24px tablet, 32px desktop.
- Tables prefer 32px row-height in admin, 44px in admin-spacious.
- Focus ring is global — never stripped.

---

## Iconography

- **Primary icon set: [Lucide](https://lucide.dev/).** The source uses `lucide-icon` components everywhere; stroke-1.5, 18–20px default. This is loaded via CDN for prototypes (`https://unpkg.com/lucide@latest`).
- **Stroke weight:** 1.5px consistent. Never filled icons.
- **Sizing:** 14 (inline/dropdown chevrons), 16 (buttons), 18 (toolbar/nav), 20 (mobile), 24 (empty states).
- **Color:** inherits `currentColor`; always matches the surrounding text tier.
- **Social / brand icons:** X (Twitter) is the one exception — source ships an inline SVG path. GitHub + LinkedIn + Mail use Lucide.
- **No emoji in UI copy.** The source never uses them in product surfaces.
- **No unicode glyphs** as icons (no `→`, `✓`, `★` as functional affordance — always a real icon).
- **Fallback glyph:** the hexagon silhouette from the koopa mark, used for avatars and empty-state illustrations.
- **No custom illustration** shipped with this system. Where a figure is needed, use a real photograph (desaturated) or a placeholder hexagon. If a future illustration system is commissioned, it should match the stroke-1.5 line weight.

Icon assets are referenced from CDN in the preview cards; they are not copied as individual SVGs.

---

## Index

| File | What it is |
|---|---|
| `README.md` | You are here. Brand context, tone, foundations. |
| `USAGE.md` | **Integration guide** — how to consume tokens + `ui-*` classes in Angular, templ, Alpine, htmx. |
| `ARCHITECTURE.md` | **Design-philosophy / spec** — layering, dependency rules, conventional↔ours token map, light/dark, conventions. |
| `bridge.css` | conventional semantic names (`--primary`, `--muted-foreground`, `--ring`, `--sidebar-*`) mapped to our tokens. |
| `styles.css` | Root entry point. `@import`s tokens + the component layer. Consumers link this one file. |
| `colors_and_type.css` | CSS variables for every token (color, type, spacing, radii, shadow, motion). Base + semantic. |
| `components/*.css` | **Class-based component layer** (`ui-*`). Framework-agnostic — same markup in Angular / templ / Alpine / htmx. |
| `themes/accents.css` | Per-product accent themes — `.theme-<name>` sets one hue. Swap the whole brand with one class. |
| `packs/*.css` | Opt-in domain components: `commerce`, `saas`, `baas`, `auth`, `koopa`. Load only what a product needs. |
| `templates/` | Starting layouts as Design Components: Article page, Admin content list, Dashboard / Now. |
| `assets/` | Logo variants, favicon, hero mark, grain overlay SVG. |
| `preview/` | Atomic design-system cards rendered as small HTMLs, surfaced in the Design System tab. |

### Consuming the system

Link one stylesheet — `styles.css` — and use `ui-*` classes. No JS framework required; every component is plain HTML + a class, so the same markup works verbatim in **Angular templates, `a-h/templ`, Alpine, and htmx**. Interactive components (dropdown, tabs, command palette, drawer, toast) are markup + a little Alpine (or native `<details>` / htmx swaps). See `USAGE.md` for per-stack snippets and the full class reference.

Each UI kit has its own `README.md`, `index.html` entry, and per-component `.jsx` files — see the kit folders.
