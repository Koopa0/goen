# P6 — README, licence, and the open-source presentation

> Self-contained brief. Assume no context beyond this file and the repository.
> **For:** Codex · **Order:** any time.

`goen` is a Traditional-Chinese 3C storefront and a Go full-stack showcase project: one
Go binary, Go 1.27, `net/http` (no framework), `templ` server-rendered HTML,
PostgreSQL 18 via pgx + sqlc, Stripe hosted Checkout, htmx only as progressive
enhancement.

The owner's requirements, verbatim in intent:

- The README should say this is a **Golang full-stack showcase project** — what it is,
  what it does, what it offers — **without much technical detail**.
- **MIT is fine**, but the presentation must be highest quality: as professional as
  **CockroachDB**, **Zed**, or a **Google open-source** project. Not casual.
- **Do not produce a lot of documents.** Fewer, better.

Read the current `README.md`, `CLAUDE.md`, `LICENSE` (if present), and `docs/`.

## What to work out

1. **What a first-time reader needs in the first screen.** They arrive from a link.
   What is this, what does it demonstrate, can I see it running, can I run it in one
   command? Study how CockroachDB, Zed, Tailscale, and one or two Google projects open
   their READMEs — what is above the fold, in what order, and what is deliberately
   pushed to a link.
2. **The line between README and CLAUDE.md.** `CLAUDE.md` is ~2,000 lines of design
   record and it should stay that way; it is the project's most distinctive artifact.
   The README must not become a summary of it. Where exactly does the boundary sit, and
   should CLAUDE.md be linked FROM the README as a feature ("every decision in this
   codebase is written down, here is where")?
3. **What files a professional open-source Go project is expected to have** in 2026 —
   `LICENSE`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, issue and PR
   templates, `CHANGELOG.md`, a `go.mod` module path that matches the repository. For
   each: is it genuinely expected, or cargo cult for a single-author portfolio project?
   The instruction is **fewer documents**, so justify each one you keep.
4. **The licence question properly.** MIT is chosen. Confirm nothing in the tree
   conflicts: the vendored koopa.dev design system in `assets/css/ds/`, the vendored
   htmx pre-release in `assets/js/vendor/`, the Google Fonts `@import`, and every module
   in `go.mod`. Produce the attribution the project actually owes, if any.
5. **Screenshots.** A showcase project is judged on whether it looks real. What images
   does the README need, at what size, and where do they live so the repository does not
   bloat?

## What to deliver

A **complete proposed `README.md`**, ready to commit — not an outline. Plus a short
list of any other file you recommend adding, each with one sentence of justification,
and a list of anything you recommend deleting.

Match the repository's existing prose voice: it explains WHY, it names the defect a
rule exists to catch, and it does not use marketing language. Read a few sections of
`CLAUDE.md` before writing a word.

## Report

The proposed README first, then the add/delete lists.

Then end with the same three lists every brief in this set ends with:

1. **Verified by running** — executed, result observed.
2. **Read but could not verify** — a view formed from the code or the documentation,
   without running it.
3. **Did not look at** — in scope on paper, not examined.
