# Implementation prompt — round 10

Hand this to the implementing agent. It is written to be pasted whole.

---

You are implementing verified defect fixes in the goen repository
(`/Users/dtws/ecommerce`, module `github.com/koopa0/goen`): a Traditional-Chinese
3C storefront — Go 1.26, `net/http` with no framework, `templ`, PostgreSQL via
`pgx`+`sqlc`, Stripe hosted Checkout, ECPay e-invoice.

## Your source material

- **`docs/reviews/11-round10-findings.md`** — the list, the wave order, the
  collisions. **Read it in full before writing any code.** The waves are not a
  suggestion: they were derived from which fixes define vocabulary that others
  speak, and from which fixes make a new state reachable that another fix is
  what shows.
- **`docs/reviews/11-specs/<id>.md`** — one file per finding. Each carries the
  root cause, the reproduction the finding rests on, the blast radius, the fix
  and the lock. Read the whole spec for a finding before starting it.
- **`CLAUDE.md`** — this project's decisions, with reasoning, and a numbered
  list of predictable mistakes. It wins over general practice everywhere the two
  disagree.
- **`.claude/rules/`** — imported Go and web rules, in force.

## What has already been done for you

Every finding was verified against a **running system** — a live database, a
running server, and in two cases the real ECPay staging API. The reproduction
section of each spec is the evidence, not an argument. You do not need to
re-derive whether a defect is real.

You **do** need to re-run a reproduction before you fix it, for one reason: it
is how you know your fix worked. A spec whose reproduction you never saw fail is
a spec you are implementing blind.

## The rules that bind this work

**1. Locks are proven by mutation.** Every fix carries a test. Before you call
that test a lock: break the fix, watch the test go RED, restore the fix, watch it
go GREEN. If the test passes with the behaviour removed, it is not a lock —
CLAUDE.md records five separate occasions where a false-green test sat beside
the defect it was written to catch. Each spec's final section tells you the
specific mutation to apply.

Where a property genuinely cannot be proven behaviourally (the timing fix), say
so plainly and record the mutation as GREEN, the way this repository already
does for its constant-time compare. Do not dress it up.

**2. A fix is new work.** When you finish one, ask what it made newly reachable.
The findings document lists what is already covered by an existing rule and what
is not — the "not" list is where you must design rather than implement.

**3. Do not re-litigate a decision this repository has recorded.** If a spec
seems to contradict CLAUDE.md, stop and say so rather than picking a side. Two
claims in this review were refuted for exactly that reason, and one earlier
external review re-filed an already-closed finding because it read a superseded
line as current state.

**4. The generated boundary is real.** `internal/db` is `sqlc` output and
`*_templ.go` is `templ` output. Change the `.sql` or the `.templ` and
regenerate. Never hand-edit either.

**5. Money and stock go through the schema.** Rules that would corrupt money,
stock or history are refused by PostgreSQL, where there is no second write path.
A new invariant belongs in a CHECK or a trigger with a named constraint, not in
a handler — and a caller distinguishes refusals by `PgError.ConstraintName`,
never by matching the error text (mistake #32).

**6. Every mutation is a plain form that works with scripting off.** A rejected
form re-renders at `422` with the submitted values intact, `aria-invalid` on
each refused control and `aria-describedby` pointing at its message. A refusal
reachable from the buying mainline is a `422`, never a `500`.

**7. Chrome is bilingual.** Any customer- or staff-facing string goes through
`internal/i18n` in both locales. `TestNoChromeStringIsHardCoded` enforces it.

## Before you start each finding

```
git status --porcelain          # must be empty
make verify                     # must pass — know your baseline
```

## Before you call each finding done

```
make verify                     # && not ;  — a pipe reports green from a failed step
make test-integration           # needs Docker; the schema fixes live here
git status --porcelain          # clean but for your intended change
```

For the wave 1 schema fixes, additionally:

```
make db-reset && make migrate-up
make schema-drift               # once at the END of wave 1, not after each fix
```

For anything touching a rendered page, `make check-layout` (needs Chrome and a
running `make run`).

## Report back per finding

State plainly:

- what you changed and where;
- **the mutation you applied and that you watched the test go red** — this is
  the part that is not optional;
- what the fix made newly reachable, and what covers it;
- anything in the spec that turned out to be wrong.

### When a spec does not match the code

The specs were written from a verified reproduction against a running system,
but they were written by a reader, and readers mistype. **The code always wins.**
What that means depends on which kind of mismatch it is, and the two are not
close:

**A naming slip — resolve it, note it in your report, keep going.** A path,
symbol or identifier that does not exist but has exactly one obvious counterpart
in the tree is a typo, not an ambiguity. `migrations/001_init.up.sql` when the
repository contains exactly one migration named `001_initial_schema.up.sql` is
this kind. Resolving it is not inventing a difference away — there is no
difference to invent. Halting on it costs a round trip and answers nothing.

**A semantic mismatch — stop and report, implement nothing.** The spec says a
function does X and it does Y; the quoted code is not what is at that line; the
fix depends on a constraint, column or branch that is not there; two specs
require incompatible things. Here the spec's reasoning may rest on something
untrue, and guessing which half to keep is exactly how a fix lands wrong. Say
what you found, say what you think it should be, and wait.

**The test, when you are unsure which you are looking at:** would a reasonable
reader agree there is exactly one thing this could mean? One candidate is a
slip. Two or none is a mismatch.

Files a spec names that **do not exist yet because the fix creates them** are
neither — `cmd/goen/main_test.go`, `internal/web/compress.go`,
`internal/db/shopcalendar_test.go` and others are new files the work is meant to
add. Read the spec's fix section before concluding a path is wrong. The
reproduction sections also name temporary probe files (`zz_probe_*.go`) that the
verifier deleted after use; those are history, not instructions.

## Where to begin

Wave 0, in the order given. `domain-sentinels-swallow-infra-errors` is first of
everything: three later fixes add database refusals that have to be mapped in
the error grammar it establishes.

## One decision has already been made for you

`11-specs/refund-does-not-reverse-points.md` proposes that a refund clawback be
allowed to overdraw the points balance, and adds an exemption to
`loyalty_never_negative` to permit it. **That is overruled.** The reasoning is in
the findings document under "The clawback decision"; the short form:

> A clawback reverses the **unconsumed remainder** of the lot the refunded order
> created, and never more. The balance does not go negative. Do not add a
> `reason = 'return'` exemption to `loyalty_never_negative` — the lot model makes
> the clamp structural, so that trigger stays the authority.
>
> Record the shortfall where the shop can read it. Do not chase the value the
> customer kept: a redemption wrote `store_credit_entries` with reason
> `'points'`, and that ledger has its own reversal rules.

Where a spec and the findings document disagree, **the findings document wins**
and you say so in your report rather than reconciling them silently. This is the
only such conflict; the specs are otherwise authoritative.

The other half of that finding — `member_spend` counting refunded orders toward
the rolling-year tier — is not a judgement call. Implement it as the spec says.
