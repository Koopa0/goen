# Round 11 cold review session prompt

Paste this whole file—and only this file—into a fresh review session. Do not
paste the Phase B appendix or any earlier review report into the initial context.

---

You are conducting a cold, adversarial review of the whole goen repository at
`/Users/dtws/ecommerce` (module `github.com/koopa0/goen`). This is a review, not
an implementation session. Do not change the main checkout, commit, or push.
Temporary probes and reverse mutations are allowed only in a disposable
worktree under the rules below.

Your work has two phases. Phase A must be completed and cryptographically sealed
before you read the Phase B material. This is a real context-isolation rule: an
instruction to ignore text already placed in your context does not make a review
cold.

## Phase A — form findings without the builder's frame

### Files you must not read yet

Do not open, search, summarize, or delegate these paths before Phase A is sealed:

- `docs/reviews/11-round10-findings.md`
- `docs/reviews/11-specs/`
- `docs/reviews/12-round10-completion-report.md`
- `docs/reviews/13-round11-audit-appendix.md`

Do not inspect commit messages whose subjects or bodies describe Round 10 fixes.
Do not ask a subagent that inherited earlier Round 10 context to participate in
Phase A. If any blocked material enters the context, disclose it immediately and
restart Phase A in a genuinely fresh session.

### Read-only baseline

Record, without filtering output:

```sh
git status --porcelain
git branch --show-current
git rev-parse HEAD
```

Run the non-destructive baseline gates as separate commands:

```sh
make verify
make test-integration
make deadcode
```

Do not run `make db-reset`, `make schema-drift`, or `make restore-drill` yet.
Those targets require the Phase B destructive-database preflight.

If configuration permits, run the real server against disposable local services
and run `make check-layout`. Use only local fakes or explicit test/staging
credentials. Never create a live payment, production invoice, or customer email.

### Independent review

Read the production code, migrations, tests, `CLAUDE.md`, README, operational
docs, and `.claude/rules/`, excluding the blocked review material above. Review
the application as a running system as well as source code.

Cover at least these neutral risk dimensions without assuming a prior finding:

- money, inventory, ledgers, refunds, and external-provider reconciliation;
- authentication, authorization, privacy, secret handling, and startup posture;
- transaction boundaries, idempotency, concurrency, and retries;
- browser URL interpretation, forms, HTTP caching/representation semantics, and
  real router/middleware order;
- schema constraints, role/column privileges, migrations, backup/restore, and
  verification instruments;
- Go error grammar, package cohesion, API width, naming, generated-code
  ownership, standard-library use, and i18n organization.

Exercise real workflows where safe: browse, sign in, administer catalogue and
shipping, place and fulfil an order, return/refund it, use loyalty/store credit,
and inspect operational health. When an external dependency prevents a path,
name exactly what was not reached.

Fresh-context subreviews are encouraged for money/stock, auth/privacy,
concurrency, HTTP/browser, Go/package design, and verification machinery. Give
them only this neutral Phase A brief and no inherited review conclusions.

For each candidate finding record:

1. severity and tight file/line reference;
2. triggering input and observed wrong outcome;
3. exact command/output or primary evidence;
4. which existing guard stayed green and why;
5. smallest sound fix boundary; and
6. a proposed lock and the mutation that should prove it RED.

Also record investigated hypotheses that primary evidence refuted and every area
not reached. Start uncertain claims as **UNVERIFIED**; lack of reproduction is
not refutation and not acceptance.

Temporary probes or reverse mutations must live in a disposable worktree, not
the main checkout. Show that each mutation applied, restore it, and finish that
worktree clean. Do not mutate any non-disposable database.

### Seal Phase A

Write the complete Phase A result outside the repository at:

```text
/tmp/goen-round11-phase-a.md
```

Include the baseline transcripts and all candidate/refuted/unreached sections.
Then run:

```sh
shasum -a 256 /tmp/goen-round11-phase-a.md
date -u +%Y-%m-%dT%H:%M:%SZ
```

Print one line in the session before continuing:

```text
PHASE_A_SEALED <sha256> <utc-timestamp>
```

Do not alter the sealed file after printing the hash. Any later correction goes
in Phase B and explicitly says why Phase A was wrong.

## Phase B — audit the completed round

Only after `PHASE_A_SEALED` has been printed, read and follow
`docs/reviews/13-round11-audit-appendix.md` in full. It supplies the historical
baseline, all 28 required dispositions, safety preflight, interaction audits,
known decisions, mutation-evidence requirements, and the final output contract.

The final answer must keep Phase A observations distinguishable from facts
learned during Phase B. Never rewrite a cold finding into hindsight.
