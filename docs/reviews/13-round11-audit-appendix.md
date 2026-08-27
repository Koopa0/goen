# Round 11 Phase B audit appendix

Do not read this file until the Phase A findings have been written to the sealed
scratch file and its SHA-256 has been printed. If it was read early, disclose
that Phase A was warm and restart Phase A in a fresh context before continuing.

## Baseline being audited

The production implementation range is `37d20e3..175b4cf`. Repository HEAD may
be a later documentation-only handoff commit. Verify that every commit after
`175b4cf` changes only `docs/reviews/`; otherwise report a baseline mismatch
before attributing any result to Round 10.

Now read, in this order:

1. the sealed Phase A findings;
2. `docs/reviews/11-round10-findings.md`;
3. every file in `docs/reviews/11-specs/`;
4. `docs/reviews/12-round10-completion-report.md`.

Do not overwrite Phase A after learning the builder's frame. Add a separate
Phase B section for confirmations, refutations, and newly discovered facts.

## Boundary claims to verify

Do not review only the final tree; verify the required commit topology:

- Wave 0 is `ca7b921`.
- All Wave 1 schema changes and stated totals are one commit, `d4b13d9`.
- `refund-does-not-reverse-points` and `return-retry-ui-gap` share that Wave 1
  commit because each makes the other's partial state reachable.
- Both Wave 2 findings are the single commit `9503115`.
- Wave 3 step 2—startup dependencies, production URL posture, and TOTP key
  material—is the single commit `d2e1bd0`.
- Wave 4 step 1—stateless static traffic and compression—is the single commit
  `75088bf` because middleware order couples them.

Wave 1 claims that `make db-reset && make test-integration` passed between
findings and that schema drift ran once only at its end. Historical stdout is
not all retained; audit the final schema but do not rewrite missing history.

Read the live PostgreSQL catalogue rather than trusting documentation constants.
The stated totals are 245 CHECK constraints, 82 foreign keys, 63 unique indexes,
40 rule triggers, 16 `updated_at` triggers, and 21 `SECURITY DEFINER` functions.
Verify that `TestTheStatedSchemaTotalsAreTheRealOnes` independently binds the
catalogue to the values stated in both documentation locations.

## Safety preflight for destructive database targets

`make db-reset`, `make schema-drift`, and `make restore-drill` can drop or replace
databases. Before running any of them:

1. inspect `GOEN_DATABASE_URL` without printing credentials;
2. prove it names the disposable local Compose PostgreSQL instance at
   `127.0.0.1:5433` and the `goen` database;
3. run `make db-up`;
4. stop rather than run a destructive target if the host, port, or database is
   different or cannot be established.

Integration suites use isolated testcontainers and are separate from this
preflight. Payment, invoice, and mail exercises must use local fakes or explicit
test/staging credentials. Never create a live charge, production invoice, or
real customer email for this review.

With that preflight satisfied, run commands separately and stop at the first
failure so an earlier exit code cannot be hidden:

```sh
make db-reset
make test-integration
make schema-drift
make restore-drill
```

Run the real server and `make check-layout` where configuration permits it. Name
the exact route or provider seam not reached instead of approving it by proxy.

## Round 10 disposition checklist

Audit each of these 28 findings individually. A green wave or package does not
close its members.

### Wave 0

1. `domain-sentinels-swallow-infra-errors`: inject statement timeout,
   connection, and constraint failures. Prove infrastructure errors do not
   become loyalty/cart/returns/admin domain sentinels or silent redirects, and
   bind schema refusal to `pgconn.PgError.ConstraintName`.
2. `suites-hardcode-migration-001`: add a throwaway next migration and prove
   every integration suite receives it through the real runner.
3. `restore-drill-false-green`: independently mutate a row count, schema
   object, table/function/column/schema ACL, restore exit status, and exported
   snapshot; every mutation must make the drill RED.
4. `deadcode-guards-false-green`: verify the pinned production reachability
   analyzer, stale-allowlist failure in both directions, generated-code caller
   census, and the remaining four reasons.

### Wave 1

5. `loyalty-two-defects`: audit the lot model in the dedicated section below.
6. `refund-completion-definitions`: trace the canonical per-order
   `order_refunds` view, the per-pass moved-money event that drives customer
   history, and the guarded time-window predicates in `RevenueSince` across
   card-only, credit-only, split, retry, and duplicate-event outcomes.
7. `refund-does-not-reverse-points`: trace spend and proportional clawback under
   full, partial, repeated, and clawback-only retry outcomes.
8. `return-retry-ui-gap`: prove every outstanding payout state has exactly one
   possible operator action or explicit manual guidance.
9. `rescission-window-timezone`: test both UTC/Taipei disagreement directions
   and census all calendar-day business rules.
10. `stripe-session-cancel-race`: force both race orders and prove no cancelled
    order retains an open provider session.
11. `ship-pending-bypasses-guard`: call `Ship` on pending and unpaid orders;
    assert no shipment, reservation, event, audit, or outbox writes. Exercise
    the database trigger separately and assert its exact constraint name.
12. `concurrent-returns-double-open`: hold one transaction open across another;
    prove one open request, once-only delivery fee, and payable arithmetic.
13. `argon2-timing-and-phc`: prove an over-512-byte password is refused before a
    database read for existent and nonexistent accounts. Deterministically
    mutate PHC length/format/parameter bounds; audit timing construction under
    the exception described below.
14. `answer-staff-flag-is-caller-supplied`: inspect the staff-flag-free
    storefront API, distinct customer/staff SQL write doors, and storefront column
    privilege. Confirm whether the customer-reply route's absence is still an
    explicit decision rather than accidentally dead functionality.

### Wave 2

15. `stripe-webhook-tristate`: enumerate ignored, understood, actionable but
    unreadable, unsupported-version, and completed-but-unpaid outcomes; verify
    durable operator visibility and idempotency.
16. `webhook-unknown-session-no-record`: send positive captured money with no
    local session and for a cancelled order; prove one durable operator record,
    no guessed attribution, and safe replay.

### Wave 3

17. `sitepath-triple-slash`: test raw browser resolution for slash/backslash
    authority forms, controls, schemes, hosts, userinfo, and poisoned persisted
    hero/banner values. Prove there is one grammar.
18. `baseurl-not-an-origin`: exercise origin shape independently from production
    TLS posture, including credentials, path, query, fragment, and lookalike
    schemes in Stripe and Google consumers.
19. `production-starts-without-deps`: start the real binary with missing,
    malformed, and half-configured SMTP/Stripe/BaseURL/proxy settings. Posture
    failure must happen before pools or external providers create effects.
20. `totp-key-derivation`: for non-empty input, test exact decoded key length and
    supported encodings, passphrases, repeated bytes, wrong keys, stale
    ciphertext, both locales, and the absence of the old hash-derived
    compatibility path; verify empty input is only the documented disabled mode.
21. `ecpay-remote-first`: verify a void uses the recorded issue date and inspect
    the recorded decision to keep provider-number allocation remote-first.
22. `ratelimit-unbounded`: attack retained byte length, maximum live keys,
    raw/digested namespace collision, oldest-entry eviction and equal-timestamp
    tie behaviour, idle sweeps, concurrency, and over-policy sign-in/reset inputs.

### Wave 4

23. `static-assets-hit-the-database`: use the real router and a driver tracer to
    prove assets/media make zero visitor-state queries while an ordinary page
    still makes a query. Audit security headers and cache `Vary` behaviour.
24. `no-compression`: audit asset and dynamic negotiation, q-values, wildcard,
    identity, threshold, HEAD, bodyless statuses, partial and pre-encoded
    responses, ETag/range/Vary, flush/controller interfaces, panics, and every
    secret-bearing opt-out.
25. `shipping-name-en-wiped`: preserve and deliberately clear both English
    shipping fields through version append and audit payloads.
26. `setzoneprefixes-appends`: verify atomic replace, clear, invalid rollback,
    concurrent whole-set edits, exact error grammar, and row-local accessible
    rerendering.
27. `parse-helpers-collapse-states`: prove malformed, overflow, negative, and
    over-ceiling input cannot become the accepted blank/zero value in every
    protected count writer; audit the separately queued remaining parsers too.
28. `checkout-autocomplete-and-inputmode`: inspect the rendered form and browser
    semantics for every identity/contact/address field.

## The loyalty model to audit as one model

- An award is a lot.
- Every spend row names the lot it consumed.
- Expiry and spending use the same lot date.
- FIFO is `expires_on`, then `created_at`, then `id`, including two same-day
  lots with identical timestamps.
- A clawback is its own `kind`, never a disguised spend.
- It reverses only the unconsumed remainder and never creates a negative balance.
- A zero-point clawback remains a real history event.
- `requested_points > 0` belongs to clawbacks, and the points CHECK admits zero
  only for `kind = 'clawback'`.
- The ledger is pure INSERT-only; no admitted UPDATE shape may implement it.
- PointsHistory shows requested and shortfall figures in both locales.
- Every former sign-as-kind query and CHECK reads `kind`.

Test the interaction with `member_spend`, `order_refunds`, tier multipliers,
expiry-on-read, retry idempotency, store credit, and partial returns. Inspect the
intentional migration ordering: `CREATE VIEW order_refunds` must precede the
`LANGUAGE sql` function, while its GRANT remains after roles exist.

## Cross-finding interaction audits

### Refunds, returns, and invoices

Trace card and store-credit components independently through decision, payment,
retry, timeline, reports, credit notes, invoice allowances, audit, and rendered
actions. Test provider refusal, ambiguous transport failure, disconnect after a
provider accepted a filing, clawback-only failure, settled retry, duplicate
retry, and delivery-fee repayment.

### Payments and webhooks

An HTTP 200 is not evidence that captured money was attributed. Every actionable
outcome must leave one operator-visible record and remain idempotent. Test
payment-session cancellation and webhook arrival in both orders.

### Startup and secret material

Validate the real binary, not only helper functions. Invalid production posture
must win before database or provider construction. Verify optional-Stripe and
development-LogSender behaviour against the recorded decisions rather than
silently changing them.

### Router, static traffic, and compression

The current real-router integration exercises asset, media, and home paths. It
does not by itself prove the whole route-registration, middleware-order, and
authorization-wrapper matrix. Audit that remaining boundary explicitly. Return
ownership still requires a separate handler-boundary check.

### Go and package design

Review the post-wave refactors as code, not as cosmetic commits:

- admin product, shipping, and return workflows should have feature-cohesive
  files without gratuitous one-file packages;
- return payout facts should be batched and view construction explicit;
- internal APIs and closed enums should be narrow and idiomatic;
- i18n should have files named for business concepts, with no production
  `admin_*.go` naming convention.

For i18n, require an actual dependency/lifecycle boundary before recommending a
subpackage. Also reject catch-all files that mix unrelated rules. Reproduce two
separate manifests: a sorted key tuple
`(exported key symbol, key id, ZhHant, En)` and a package-scope exported-API
manifest. The builder reports 1,463 key tuples and 1,482 total exported API
objects; verify both collections and their equality to the pre-refactor baseline
instead of treating the counts as a hash.

## Properties ordinary gates cannot close

Five properties require a dedicated applied mutation and pasted RED:

1. compression negotiation and representation semantics;
2. checkout autocomplete/inputmode meaning;
3. static requests avoiding session/cart database work;
4. restore-drill detecting its own independent mutations;
5. production startup refusal under real configurations.

Argon2 timing is the explicit exception: do not add a flaky wall-clock assertion.
Audit the pre-read length check and call construction, state the residual timing
limitation, and still require deterministic mutation RED for the length gate and
PHC parser bounds.

Historical REDs cannot be reproduced unchanged on fixed HEAD. Use a disposable
pre-fix worktree or an explicit current-tree reverse mutation. Show that the
mutation applied, paste the failing output, restore it, and verify a clean tree.

## Review method and dispositions

- Begin at **UNVERIFIED**, not REFUTED. Promote to confirmed or refuted only on
  primary evidence; otherwise report uncertain or unreached.
- Never infer acceptance from a test name. Mutate the protected behaviour and
  show the lock RED.
- Prefer the executable path, SQL catalogue, browser behaviour, provider test
  response, or mutation-proven test over prose.
- For money, stock, history, privacy, or authorization, find the authoritative
  write door and try a second door.
- Ask which existing guard should have caught each defect and why it stayed
  green.
- Fresh-context subreviews should receive only the sealed Phase A brief for
  independent money/stock, auth/privacy, concurrency, HTTP/browser,
  Go/package-design, and verification-instrument passes.
- Say exactly what was not reached. Silence is not approval.

For review findings use these dispositions:

- **fixed at reviewed commit**;
- **open** with a queue recommendation by name;
- **intentional** under a cited recorded decision;
- **partial**; or
- **unverifiable** with the missing evidence named.

Do not use "fixed here" because this is a read-only review session.

## Required final output

Lead with new findings, ordered by severity. For each include a tight file/line
reference, triggering input and wrong outcome, exact reproduction, the guard
that stayed green and why, smallest sound fix boundary, and a mutation-proven
lock proposal.

Then provide:

1. the sealed Phase A SHA-256 and whether Phase A remained cold;
2. a 28-row Round 10 disposition matrix;
3. challenged semantic decisions, prominently separated;
4. refuted candidate findings with primary evidence;
5. areas not reached and why;
6. exact final gate transcripts; and
7. final `git status --porcelain` for the main and every temporary worktree.

Do not batch-approve a wave, package, or severity class. Lack of reproduction is
unverifiable, uncertain, or unreached—not silent acceptance and not refutation.
