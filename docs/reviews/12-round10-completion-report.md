# Round 10 completion report

Round 10's implementation is complete. The production implementation ends at
`175b4cf`; the work from Wave 0 through Wave 4 is committed at the required
boundaries, and the later Go/package-design cleanup is committed separately.

This report is an audit handoff, not an acceptance certificate. A fresh reviewer
must reproduce the claims below. The companion cold-review prompt is
`docs/reviews/13-round11-cold-review-prompt.md`; it unlocks
`docs/reviews/13-round11-audit-appendix.md` only after the reviewer seals an
independent Phase A result.

## Evidence limitation — read before the findings

**UNRESOLVED AUDIT-EVIDENCE GAP:** the implementation session's complete terminal
scrollback was not retained. Source, commits, tests, and final gate results are
present, and exact pre-fix reproductions remain in the finding specifications.
Verbatim mutation RED output was recovered for Wave 2 and selected Wave 3
findings; other Wave 3 evidence survives only as abridged excerpts, and exact
pre-fix probes survive for two Wave 4 findings. The implementation-time mutation
stdout for most Wave 0 and Wave 1 findings, Wave 4, and the complete stdout of
Wave 1's inter-finding PASS runs is unavailable.

This report does not turn a specification's pre-fix reproduction into a claimed
implementation transcript, and it does not invent missing output. Each entry
labels its evidence accordingly. This preservation failure cannot be repaired
inside a historical completion report; the Round 11 prompt
explicitly requires a new audit. Separate queued implementation and verification
work is listed at the end.

## Commit topology

| Boundary | Commit(s) | Constraint kept |
| --- | --- | --- |
| Wave 0 | `ca7b921` | Vocabulary and verification instruments together |
| Wave 1 | `d4b13d9` | All schema findings and stated catalogue totals in one commit |
| Wave 2 | `9503115` | Both Stripe webhook findings in one commit |
| Wave 3 step 1 | `e78c7da`, `d336044` | Site path, then origin grammar |
| Wave 3 step 2 | `d2e1bd0` | Startup dependencies, production URL posture, and TOTP key model in one commit |
| Wave 3 remainder | `4565ed1`, `ec04a7a`, `2959313` | ECPay, fixture isolation, rate-limit bounds |
| Wave 4 step 1 | `75088bf` | Stateless static traffic and compression in one commit |
| Wave 4 remainder | `48e62bd`, `9c0c180`, `5d392cf`, `0d0d9d5`, `7c59dd5` | Behaviour-neutral admin split, then one finding per commit |
| Post-wave review cleanup | `7f289b6`, `a310862`, `14db3a6`, `571c13e`, `96dc801`, `d21a015`, `175b4cf` | Go/API/package/i18n and follow-up reliability cleanup |

`TestTheStatedSchemaTotalsAreTheRealOnes` reads the live catalogue and binds four
figures stated in both CLAUDE.md and README.md: 245 CHECK constraints, 82 foreign
keys, 63 unique indexes, and 40 rule triggers. It also binds README.md's 16
`updated_at` triggers and 21 `SECURITY DEFINER` functions.

## Wave 0 — vocabulary and instruments

### 1. `domain-sentinels-swallow-infra-errors` — `ca7b921`

Changed: domain mappings now inspect `pgconn.PgError.ConstraintName`. Loyalty no
longer turns infrastructure failures into `ErrNotEnough`; cart inventory maps
only `inventory_never_negative`; store-credit races have `ErrCreditChanged` and
refresh checkout; returns distinguish `ErrTooMany`; product reads map only
`pgx.ErrNoRows` to absence.

Abridged retained pre-fix excerpts; the full transcript is in the specification:

```text
balance before: 500 points
Redeem returned after 1.503s: cents=0 err=loyalty: not enough points: ERROR: canceling statement due to statement timeout (SQLSTATE 57014)
errors.Is(err, ErrNotEnough) = true
REPRODUCED: an infrastructure fault is reported as ErrNotEnough with 500 points on the ledger

PlaceOrder returned after 1.535s: number="" err=cart: variant unavailable: ERROR: canceling statement due to statement timeout (SQLSTATE 57014)
errors.Is(err, ErrUnavailable) = true
```

PASS locks include `TestProductReadsDistinguishAbsenceFromInfrastructure`,
`TestAnInfrastructureFailureIsNotReportedAsSoldOut`,
`TestInventoryConstraintIsReportedAsSoldOut`,
`TestAChangedCreditBalanceReRendersCheckoutWithTheFreshFigure`, and
`TestAConcurrentReturnRendersTheFreshQuantity`, plus direct loyalty lock
`TestAnInfrastructureFailureIsNotReportedAsAnEmptyBalance`.

**FLAGGED SEMANTIC DECISIONS:** `return_within_shipment` and over-quantity input
map to `ErrTooMany`, not `ErrNotReturnable`. `store_credit_never_negative` maps
to `ErrCreditChanged`, not the inventory-specific `ErrUnavailable`.

### 2. `suites-hardcode-migration-001` — `ca7b921`

Changed: the six integration-suite bootstraps now use `dbtest.Start`, which runs
the migration runner over every `*.up.sql`. The repository guard requires each
suite's `TestMain` to use `dbtest.Start`/`dbtest.Pool`; it does not independently
forbid an additional unused copy of migration-001 setup logic.

Retained pre-fix reproduction:

```text
=== RUN   TestProbe002Applied
    probe_002_test.go:13: migration 002 NOT applied by this suite
--- FAIL: TestProbe002Applied (0.00s)
FAIL    github.com/koopa0/goen/internal/media    2.284s
=== RUN   TestProbe002Applied
    probe_002_test.go:15: migration 002 applied: probe_002
--- PASS: TestProbe002Applied (0.00s)
ok      github.com/koopa0/goen/internal/contact  2.278s
```

PASS lock: `TestEveryIntegrationSuiteUsesTheMigrationRunner`.

### 3. `restore-drill-false-green` — `ca7b921`

Changed: `make restore-drill` now compares exact per-table row counts and a
stable schema catalogue, carries and verifies table/function/column/schema ACLs,
uses unique temporary resources, and fails closed with
`pg_restore --exit-on-error`. The snapshot is exported before concurrent writes
can move the source underneath it.

Retained pre-fix evidence: the old drill printed PASS after a dropped index,
CHECK, and grants; a snapshot race also produced this real count drift:

```text
- carts 7
+ carts 6
```

**MISSING TRANSCRIPT:** the implementation-time failures for the prescribed
independent row/schema/ACL/restore/snapshot self-check mutations were not
retained. This finding was invisible to the pre-existing ordinary gates; its new
self-checking drill is the evidence, so the missing pastes are a material gap and
must be re-run in Round 11.

**FLAGGED SEMANTIC DECISIONS:** ACL coverage was expanded beyond the spec to
include column and schema grants. Pretty constraint definitions are compared
because the non-pretty `pg_get_constraintdef` form was not round-trip stable;
that comparison is scoped to the same PostgreSQL server major. Dump-carried ACL
comparison deliberately excludes cluster-global roles and database GRANTs,
which are not objects the dump transports.

### 4. `deadcode-guards-false-green` — `ca7b921`, completed in `5d392cf`

Changed: the exported-function text-presence guard was replaced by pinned
`golang.org/x/tools/cmd/deadcode@v0.38.0` over production reachability, with an
allowlist that fails for unexplained dead symbols and stale entries. Generated
sqlc methods remain separate because the analyzer treats them as
reflection-live; `TestEveryGeneratedQueryHasAProductionCaller` now checks their
production AST call sites. The dead `RemoveZonePrefix` API and its allow entry
were removed with the owning Wave 4 finding.

Retained pre-fix comparison:

```text
$ go test ./internal/db/ -run 'TestEveryExportedFunctionHasACaller|TestEveryGeneratedQueryHasACaller' -count=1
--- PASS: TestEveryExportedFunctionHasACaller (0.09s)
--- PASS: TestEveryGeneratedQueryHasACaller (0.09s)
```

The real analyzer simultaneously reported 14 unreachable functions, including
`Store.RemoveZonePrefix`, `Store.WithTx`, `PointsFor`, `Store.Answer`,
`ProductView.HasReviews`, and `Question.AnsweredByShop`.

**MISSING TRANSCRIPT:** exact stdout was not retained for the empty allowlist,
test-only caller, bare-name collision, self-referencing wrapper, both allowlist
directions/reason requirement, generated-templ reachability, or generated-query
production-call-site mutations. The final gate does retain this result:

```text
deadcode: PASS — 4 unreachable functions, all explicitly reasoned
```

The four deliberate entries are `internal/i18n.Keys`,
`internal/i18n.MessageFor`, `internal/loyalty.Days`, and
`internal/product.Store.Answer`.

**FLAGGED SEMANTIC DECISIONS:** the analyzer runs on `./...`, not only the main
package, so it sees disconnected production packages. `product.Store.Answer`
stays on the bidirectional reasoned allowlist for the queued storefront reply
route. x/tools' reflection treatment keeps generated sqlc methods artificially
live, so the separate production-AST caller guard is required rather than
pretending the analyzer covers them.

## Wave 1 — schema and durable money state (`d4b13d9`)

Between Wave 1 findings, `make db-reset && make test-integration` passed. Schema
drift ran once, at the end of the wave, and passed. Exact stdout from those runs
was not retained; that limitation applies to every Wave 1 entry below.

### 5. `loyalty-two-defects`

Changed: the ledger now has explicit `award`, `spend`, and `clawback` kinds.
Awards are lots; spend rows name the lot consumed; expiry uses the same lot;
allocation is FIFO by `expires_on`, `created_at`, then `id`; and all operations
are pure INSERTs. A clawback records requested points and shortfall, reverses at
most the lot's unconsumed remainder, admits a durable zero-point event, and is
rendered as a clawback in both locales. Award arithmetic truncates to whole
NT$100 units.

Abridged pre-fix results; the full exact transcript is in the specification:

```text
BEGIN; SELECT award_loyalty_points('00000000-0000-0000-0000-000000000000'::uuid, 0::bigint, current_date+365);
ERROR:  an award must be positive, got 0
CONTEXT:  PL/pgSQL function award_loyalty_points(uuid,bigint,date) line 6 at RAISE

balance_day0=0
balance_day1=-100
```

PASS locks include `TestAnOrderThatEarnsNothingIsStillCaptured`,
`TestTheAwardedPointsUseWholeHundreds`, `TestASpentAwardLeavesTheBalanceWithIt`,
`TestSameDayLotsHaveADeterministicFIFOOrder`,
`TestAZeroClawbackSurvivesHistoryToRenderedHTML`,
`TestPointsCannotBeSpentFromALapsedLot`,
`TestAClawbackReversesOnlyTheLotsUnconsumedRemainder`,
`TestTheLoyaltyAllocatorHoldsOnItsOwn`, and
`TestAClawbackSaysWhatWasRequestedAndWhatWasShort`.

**FLAGGED, USER-APPROVED MODEL DECISIONS:** a zero-point clawback is retained;
the points CHECK is `(points <> 0 OR kind = 'clawback')`; clawbacks require
`requested_points > 0`; reversal is remainder-only and never makes the balance
negative. The former `loyalty_never_negative` trigger was replaced by
`loyalty_lot_guard`; no negative-balance exemption or path remains. Every former
sign-as-kind read moved to `kind`; the completion grep census covered both query
sites and the one CHECK. Although `forbid_change` admits one legacy UPDATE
shape, clawback does not use it; the ledger implementation is INSERT-only.

### 6. `refund-completion-definitions`

Changed: a refund event is recorded when the current pass moves either card or
store-credit money, and that event drives customer history. Existing allowance,
invoice, and other per-order reads remain on canonical `order_refunds`. The
time-windowed `RevenueSince` report is the documented physical exception: it
repeats the view's exact card/positive-credit predicates, and
`TestEveryRefundTotalReadsTheOneView` prevents those semantics from drifting.
Retry does not duplicate an event.

Abridged pre-fix result; the full exact transcript is in the specification:

```text
 card_cents | credit_cents | total
            0 | 3690000      | 3690000
 reports_refunded_cents
                      0
 refunded_events
                 0
```

PASS locks: `TestACreditOnlyRefundIsOnTheCustomersTimeline`,
`TestASplitRefundWhoseCardIsPendingStillRecordsTheCreditThatLanded`,
`TestTheRefundFigureCountsCreditToo`, and
`TestEveryRefundTotalReadsTheOneView`.

**FLAGGED SEMANTIC DECISION:** cancellation credit remains in the canonical
`order_refunds` view; a second per-order definition was not introduced for
reports. The landed time window uses card `succeeded_at`, not the spec body's
`created_at`, because a card refund can remain pending for days and the report
counts when each source actually moved; synchronous credit still uses
`created_at`.

### 7. `refund-does-not-reverse-points`

Changed: `CREATE VIEW order_refunds` is above the `LANGUAGE sql`
`member_spend` function in migration 001, while its GRANT remains below role
creation. Spend is netted per order with a zero floor. Returns create durable,
proportional, lot-aware clawbacks; a points-only failure remains retryable.

Abridged pre-fix result; the full exact transcript is in the specification:

```text
status=delivered still_committed=t spend=7380000 tier=金卡 points=3690 reversing_entries=0
```

PASS locks include `TestAReturnTakesBackItsPointsAndSpend`,
`TestAClawbackOnlyFailureRemainsRetryable`, and the loyalty lot locks above.

**FLAGGED SEMANTIC DECISION:** the view object and its GRANT are deliberately
split because SQL-language function creation requires the object early, while
the GRANT requires roles that do not yet exist. Negative loyalty and an
invariant exemption were rejected by the decided lot model. Proportional
reversal derives from the durable award lot, never the member's current tier;
the final partial return absorbs integer-rounding residue. A resumed split
payout waits for full payout and applies the full refundable amount rather than
clawing back against an intermediate half-settled state.

### 8. `return-retry-ui-gap`

Changed in the same commit as finding 7: queue/view state now distinguishes
outstanding, blocked, stranded, and retryable settlement. Approved returns with
settlement work outstanding—including a points-only clawback failure—offer an
approved-only retry POST. Terminal provider refusal renders manual-intervention
guidance instead of an impossible decision form.

Abridged pre-fix result; the full exact transcript, including action URLs, is in
the specification:

```text
rows in queue: 3
decide forms: 2
probe id present in any action? 0
```

PASS locks: `TestAStalledRefundOffersItsRetryInTheQueue`,
`TestAnApprovedReturnWithMoneyOutstandingOffersToSendItAgain`, and
`TestAClawbackOnlyFailureRemainsRetryable`.

The spec's verified naming correction is that the store method is
`Store.Decide`, not `Store.Approve`. **FLAGGED SEMANTIC DECISION:** terminal provider refusal is an
operator/manual state; it is not presented as an endlessly retryable action.

### 9. `rescission-window-timezone`

Changed: `shop_day` and `shop_today` own shop-calendar conversion, all calendar
business rules route through them, and a source census prevents fresh direct
timezone arithmetic.

Abridged pre-fix result; the specification contains the exact output for both
UTC/Taipei disagreement directions:

```text
UTC rule=false  Taipei shop-day rule=true
UTC rule=true   Taipei shop-day rule=false
```

PASS locks: `TestTheRescissionWindowIsCountedOnTheShopsCalendar` and
`TestEveryCalendarDayGoesThroughTheShopsCalendar`.

Process timezone/display formatting was kept separate from the business-day
rule rather than folded into this schema change.

### 10. `stripe-session-cancel-race`

Changed: `open_payment` locks and validates the order inside the write door;
named constraints retain exact refusal grammar. If Stripe session creation wins
externally but the local transition is then refused, the new provider session is
expired.

Abridged pre-fix result; the full exact race transcript is in the specification:

```text
open sessions observed by cancellation: 0
later open_payment result: requires_payment
order status: cancelled
```

PASS locks: `TestOpeningAPaymentIsRefusedOnACancelledOrder` and
`TestACancelledOrderLeavesNoSessionUnclosed`.

### 11. `ship-pending-bypasses-guard`

Changed: the Store's friendly precheck returns `ErrRefused` before fulfilment,
and database trigger `shipment_order_in_fulfilment` closes direct/crafted
callers. The schema assertion binds the trigger refusal to
`PgError.ConstraintName == "shipment_order_in_fulfilment"`.

Retained pre-fix reproduction:

```text
BEFORE: view.CanShip=false shippable=0 status="pending"
BEFORE: owed=100000 committed=false
Ship returned: <nil>
AFTER: status="pending" shipments=1 shipment_lines=1 held=0 consumed=1 events=1 audits=1 outbox=1 stock=11 committed=false
```

PASS locks: `TestShippingIsRefusedForAnOrderThatWasNeverPicked` and
`TestRulesReject/shipment_order_in_fulfilment`, plus
`TestRulesAccept/shipment_order_in_fulfilment` and the rule-trigger census.

The spec itself verifies the correction to the original finding: the rendered UI
already hid the action. The reachable defect was a crafted authenticated POST
or a future Store caller.

### 12. `concurrent-returns-double-open`

Changed: partial unique index `return_requests_one_open` permits only one open
return per order; PostgreSQL reports that index name in
`PgError.ConstraintName`, which maps to `ErrAlreadyOpen`. Delivery-fee repayment
remains once-only under the sequential and concurrent paths.

Abridged pre-fix result; the full exact transcript is in the specification:

```text
OPEN REQUESTS ON ONE ORDER: 2
SUM REFUNDABLE=230000 ORDER TOTAL OWED=215000 shipping=15000
```

PASS locks: `TestOneOpenReturnPerOrder`, `TestOnlyOneOpenRequestAtATime`,
`TestTheDeliveryFeeIsPaidBackOnce`,
`TestUniqueConstraintsReject/return_requests_one_open`, and
`TestUniqueConstraintsAdmitTheNeighbour/return_requests_one_open`.

The spec itself verifies the correction to the original finding: capture guards
prevented a second payout; the demonstrated harm was an approved return that
could not be paid.

### 13. `argon2-timing-and-phc`

Changed: passwords over 512 bytes are refused before a database read; PHC
parsing also caps unsafe or resource-exhausting Argon2 parameters.
Authentication always returns ordinary bad credentials for the public
overlong-password case.

Retained deterministic pre-fix reproduction:

```text
Authenticate(513-byte, dead pool) -> read user: context canceled   (is ErrBadCredentials: false)
```

The pre-fix timing observation was approximately 31 ms for an overlong password
against a password account, versus approximately 2 ms for the same 513-byte
input on unknown/Google-only paths. Legal-length controls were approximately
30 ms on both sides; the fast path therefore exposed account state.

Mutation-proven locks include `TestAnOverLongPasswordIsRefusedBeforeTheRead` and
`TestVerifyRejectsUnsafeArgonParameters`.
`TestAnOverLongPasswordIsAlwaysBadCredentials` preserves the public behaviour;
the spec explicitly does not treat it as the structural pre-read lock.

**MISSING EVIDENCE / INVISIBLE PROPERTY:** no wall-clock RED should be invented;
timing is intentionally not locked with a flaky duration assertion. Round 11
must audit the call construction and explicitly disclose that limitation. The
pre-read length gate and PHC parser bounds do have deterministic reverse
mutations, but their implementation-time RED stdout was not retained and must be
re-run. **FLAGGED SEMANTIC DECISION:** PHC parameter bounds are defense in depth;
hostile stored hashes were not shown to be attacker-writable.

### 14. `answer-staff-flag-is-caller-supplied`

Changed: the caller-supplied staff boolean was removed. Customer and staff SQL
doors are separate, and the storefront database role cannot set `is_staff`.

Abridged pre-fix results from separate compile/vet/SQL probes; the exact
transcripts are in the specification:

```text
go build ./... -> BUILD_PASS_WITHOUT_ANSWER
schema privilege on is_staff: true
forged is_staff value: true
```

PASS locks: `TestAStorefrontReplyIsNeverBadgedAsTheShop` and
`TestStoreCannotBadgeAProductAnswerAsTheShop`.
The primary compile-time lock is the API itself: mutating a caller back to
`s.Answer(..., true)` no longer compiles after removal of the `staff` parameter.

**FLAGGED DEFENCE-IN-DEPTH DECISION:** the implementation both split the
customer/staff SQL write doors and revoked the storefront role's `is_staff`
column privilege. `product.Store.Answer` remains only as the customer fixture
seam on the reasoned deadcode allowlist; roadmap A11 owns the storefront route.

The spec itself establishes the correction to the original finding: no
production storefront reply route existed; the change closes a latent authority
defect rather than a live HTTP exploit.

## Wave 2 — Stripe outcomes (`9503115`, one commit)

### 15. `stripe-webhook-tristate`

Changed: webhook classification is ignored, understood, or unreadable. A known
actionable event whose payload cannot be interpreted is durably stored with an
`unreadable_event:` cause, logged at ERROR, and visible in admin health.

Recovered mutation RED:

```text
--- FAIL: TestAnUnreadableKnownEventIsRecordedForAPerson (0.02s)
    integration_test.go:1383: unreconciled = <nil>, want durable unreadable_event cause
FAIL
FAIL    github.com/koopa0/goen/internal/payment    4.151s
```

PASS locks: `TestAKnownEventGoenCannotReadIsNotAnEventItIgnores`,
`TestOnlyAPaidSessionIsACapture`, and
`TestAnUnreadableKnownEventIsRecordedForAPerson`.

**FLAGGED SEMANTIC DECISIONS:** HTTP 200 is deliberate because replaying the
same unreadable bytes cannot repair version/shape drift. Completed-but-unpaid
continues through its existing delayed-payment path. Canonical cause prefixes
remain in the existing text field rather than adding a second enum.

### 16. `webhook-unknown-session-no-record`

Changed: positive money captured for an unknown local session marks the existing
durable webhook-event row/payload unreconciled with `unattributed_capture:` and
the Stripe session in `object_ref`; it does not invent a payment or order
association. Health and logs surface the event while the webhook remains
idempotently acknowledged. The same vocabulary includes
`cancelled_order_capture` for captured money attributed to a cancelled order.

Recovered mutation RED:

```text
--- FAIL: TestTheWebhookFlagsMoneyItCannotAttribute (0.01s)
    integration_test.go:1482: unreconciled = <nil>, want durable unattributed_capture cause
FAIL
FAIL    github.com/koopa0/goen/internal/payment    3.749s
```

PASS locks: `TestTheWebhookFlagsMoneyItCannotAttribute`,
`TestCaptureForAnUnknownSessionIsNotFound`, and
`TestTheWebhookItselfFlagsMoneyForACancelledOrder`.

**FLAGGED SEMANTIC DECISIONS:** unattributed money is durable operator work, not
a failing webhook retry. `reconciled_at` means investigated/resolved, not only
"manually refunded". Payment Link/Dashboard-created sessions alert too: at the
webhook boundary they are indistinguishable from lost local attribution, and an
operator can dismiss a confirmed benign case with `reconciled_at`.

Focused Wave 2 PASS:

```text
ok      github.com/koopa0/goen/internal/payment    1.440s
ok      github.com/koopa0/goen/internal/payment    5.606s
```

## Wave 3 — process surfaces

### 17. `sitepath-triple-slash` — `e78c7da`

Changed: one raw-string, browser-aware `web.SitePath` grammar rejects authority,
scheme, control, host, opaque, slash/backslash, and userinfo forms. Every former
`SafeNext` caller uses it; hero/banner URLs are revalidated on read; a repository
guard catches the known duplicate `strings.HasPrefix` spellings for `/`, `//`,
and `/\` authority forms. Follow-up `7f289b6` revalidates the canonical output
because `url.String()` itself can produce an authority-shaped path.

Abridged retained mutation RED excerpt:

```text
--- FAIL: TestSitePathRefusesAnythingButAPathOnThisSite (0.00s)
    --- FAIL: .../bare_triple_slash (0.00s)
        sitepath_test.go:71: SitePath("///") accepted it as "///"
    --- FAIL: .../triple_slash_userinfo (0.00s)
        sitepath_test.go:71: SitePath("///goen.example@evil.example/") accepted it as "///goen.example@evil.example/"
    --- FAIL: .../quadruple_slash (0.00s)
        sitepath_test.go:71: SitePath("////evil.example/x") accepted it as "////evil.example/x"
FAIL
FAIL    github.com/koopa0/goen/internal/web    0.367s
```

PASS locks include `TestSitePathRefusesAnythingButAPathOnThisSite`,
`TestOnlySitePathDecidesASameSitePath`, and
`TestAnOffSiteHeroCTAIsReplacedAtReadTime`, plus `FuzzSitePath`.

Retained natural fuzz RED excerpt from the follow-up; omissions are preserved
as omissions rather than reconstructed:

```text
--- FAIL: FuzzSitePath
SitePath("/%2f ") returned authority-shaped path "//%20"
... resolves at "%20"
... reparsed as "", ok=false
```

**FLAGGED SEMANTIC DECISION:** no CTA database CHECK was added. WHATWG browser
interpretation remains in the single Go grammar; a SQL approximation would be a
divergent third definition. `ec04a7a` separately fixed exact-ID cleanup for the
poisoned hero fixture; its original shuffled failure stdout is unavailable.

### 18. `baseurl-not-an-origin` — `d336044`, posture in `d2e1bd0`

Changed: `web.SiteOrigin` accepts exact HTTP/HTTPS origins with a non-empty host
and no credentials, path, query, or fragment. Stripe and Google share it;
production HTTPS enforcement belongs to startup posture.

Abridged retained mutation RED excerpt:

```text
--- FAIL: TestSiteOriginRefusesWhatIsNotAnOrigin (0.00s)
    --- FAIL: .../an_https-looking_scheme (0.00s)
        sitepath_test.go:179: SiteOrigin("httpsss://evil.example") accepted it as "httpsss://evil.example" with scheme "httpsss"
    --- FAIL: .../an_http-looking_scheme (0.00s)
        sitepath_test.go:179: SiteOrigin("httpx://evil.example") accepted it as "httpx://evil.example" with scheme "httpx"
FAIL
FAIL    github.com/koopa0/goen/internal/web    0.760s
```

PASS locks include `TestSiteOriginRefusesWhatIsNotAnOrigin`,
`TestStripeReturnURLsUseTheSiteOriginGrammar`,
`TestGoogleRedirectUsesTheSiteOriginGrammar`, and
`TestProductionPostureRefusesAnUnsafeBaseURL`, plus `FuzzSiteOrigin`.

**FLAGGED SEMANTIC DECISIONS:** origin shape is a web grammar; TLS is a
deployment-posture rule. Development HTTP remains valid. Disabled integrations
do not validate configuration they never consume.

### 19. `production-starts-without-deps` — `d2e1bd0`, hardened by `7f289b6`

Changed: production refuses absent SMTP. A malformed SMTP host is startup-fatal
when authenticated SMTP is configured and never falls back to logging;
unauthenticated malformed SMTP remains an `SMTPSender` whose delivery error is
rescheduled and surfaced by outbox health, also never `LogSender`. Development
retains an explicit warned `LogSender`. Empty Stripe remains an accepted disabled
mode; a secret key without its webhook secret is fatal, while webhook-only with
no secret key remains disabled. The follow-up commit validates posture and
constructs providers before opening the database, so invalid local configuration
creates no earlier external effects.

Abridged retained mutation RED excerpt:

```text
--- FAIL: TestAProductionPostureRefusesAnUnconfiguredMailer (0.00s)
    --- FAIL: .../missing_in_production (0.00s)
        main_test.go:95: checkProductionPosture error = <nil>, want GOEN_SMTP_ADDR refusal
FAIL
FAIL    github.com/koopa0/goen/cmd/goen    0.385s
```

PASS locks include `TestAProductionPostureRefusesAnUnconfiguredMailer`,
`TestAMalformedSMTPAddressIsFatalAndNeverTheLogSender`, and
`TestPostureReportsTheFirstBrokenDependency`. Follow-up lock
`TestLocalConfigurationIsPreparedBeforeExternalDependencies` makes local
posture/notifier/proxy/provider diagnostics win over a malformed database URL.
The reverse-mutation outcome exposed the database error first; its exact stdout
was not retained.

**FLAGGED SEMANTIC DECISION / PARTIAL SPEC REFUTATION:** fully unconfigured
Stripe is a documented degraded mode, not a production-startup error. Missing
SMTP is fatal only in production. Authenticated malformed SMTP is an early
configuration refusal; unauthenticated malformed delivery remains a visible
runtime failure rather than silently becoming the development log sender.

### 20. `totp-key-derivation` — `d2e1bd0`

Changed: for non-empty input, `ParseKey` accepts exactly 32 decoded bytes from
the supported hex/base64 encodings. Empty or whitespace input deliberately
returns a nil key for disabled development mode. It refuses passphrases, wrong
lengths, and identical-byte placeholders; AES receives decoded bytes directly
instead of a SHA-256 normalized passphrase. A changed/unreadable key becomes
`ErrSecretUnreadable` with recovery guidance in both locales, not a wrong-code
response.

Abridged retained mutation RED excerpt:

```text
--- FAIL: TestOnlyThirtyTwoRandomBytesIsAKey (0.00s)
    --- FAIL: .../refuse_a-key-from-t (0.00s)
        twofactor_test.go:84: ParseKey accepted "a-key-from-the-environment" as c678c07be6dac9067af3efdafed401ea49926f34b78493db7728db57f1be5a69
    --- FAIL: .../refuse_000000000000 (0.00s)
        twofactor_test.go:84: ParseKey accepted "0000000000000000000000000000000000000000000000000000000000000000" as 60e05bd1b195af2f94112fa7197a5c88289058840ce7c6df9693756bc6250f55
--- FAIL: TestTheKeyIsTheDecodedBytesAndNotAHashOfThem (0.00s)
    twofactor_test.go:127: sha256(the configured text) still opens the ciphertext
FAIL
FAIL    github.com/koopa0/goen/internal/twofactor    0.407s
```

PASS locks include `TestOnlyThirtyTwoRandomBytesIsAKey`,
`TestTheKeyIsTheDecodedBytesAndNotAHashOfThem`,
`TestStartupRefusesATOTPKeyThatIsNotAKey`, and
`TestAStaleKeyIsExplainedInsteadOfBlamingTheCode`.

**FLAGGED SEMANTIC DECISIONS:** this is a clean break with no legacy KDF
compatibility; key format is enforced in development too; only the decidable
identical-byte placeholder is rejected rather than pretending to estimate
entropy. The measured database had zero enrolled credentials and the
application was undeployed.

### 21. `ecpay-remote-first` — `4565ed1`

Changed: voiding requires and forwards the invoice's recorded `issued_at`;
wire format follows ECPay's UTC-labelled calendar convention. A zero issue date
is refused before the provider call.

Recovered mutation RED:

```text
--- FAIL: TestAVoidSendsTheInvoicesOwnIssueDate (0.00s)
    invoice_test.go:334: InvoiceDate = "2026-08-27", want the invoice's own date "2026-08-24"
FAIL
FAIL    github.com/koopa0/goen/internal/invoice    0.382s
```

Exact commit-message evidence:

```text
The live ECPay staging call for LA25024809 refused InvoiceDate 2026-08-23 with 1600003, then accepted the same invoice number with its recorded 2026-08-24 issue date.
```

PASS locks: `TestAVoidSendsTheInvoicesOwnIssueDate` and
`TestStoreVoidSendsTheRecordedIssueDate`.

Naming slip resolved: the landed Store/Gateway test name is more precise than
the spec's "issued yesterday" name. **FLAGGED SEMANTIC DECISION / PARTIAL SPEC
REFUTATION:** issuance remains remote-first because the provider allocates the
invoice number; a local-first row could claim a filing that never existed.

### 22. `ratelimit-unbounded` — `2959313`

Changed: retained key representation is capped at 128 bytes with a SHA-256
suffix and a distinct hashed-key namespace. Every limiter states `MaxKeys`, idle
sweeps are amortized, and capacity evicts an oldest live key.
Over-policy sign-in/reset addresses receive the ordinary indistinguishable
response before reaching a limiter.

Recovered mutation RED:

```text
--- FAIL: TestALongKeyIsNotStoredWhole (0.00s)
    ratelimit_test.go:34: stored key is 65544 bytes, want at most 128
FAIL
FAIL    github.com/koopa0/goen/internal/ratelimit    0.358s
```

PASS locks include `TestALongKeyIsNotStoredWhole`,
`TestTwoLongKeysStayDistinct`, `TestALongKeyDoesNotCollideWithItsEncodedForm`,
`TestTheKeyCountIsCapped`, `TestTheOldestKeyMakesRoomAtTheCap`,
`TestTheSweepIsAmortised`, and `TestTheLimiterIsSafeUnderConcurrency`.

**FLAGGED SEMANTIC HARDENING:** the hashed/raw namespace goes beyond the spec;
without it, an attacker-supplied short raw key could equal another key's encoded
long representation and share its allowance. Equal `seen` timestamps have no
deterministic tie-break and follow Go map iteration; no business semantic depends
on which equally-old limiter key is evicted.

Focused Wave 3 PASS output:

```text
ok      github.com/koopa0/goen/internal/web         1.369s
ok      github.com/koopa0/goen/internal/account     1.473s
ok      github.com/koopa0/goen/internal/payment     1.237s
ok      github.com/koopa0/goen/cmd/goen             1.413s
ok      github.com/koopa0/goen/internal/twofactor   1.398s
ok      github.com/koopa0/goen/internal/invoice     1.410s
ok      github.com/koopa0/goen/internal/ratelimit   1.478s
```

## Wave 4 — rendered surfaces and gates

### 23. `static-assets-hit-the-database` — `75088bf`

Changed: exact static, media, probe, and webhook route segments bypass locale,
session, and cart database middleware while retaining recovery, request IDs,
logging, security headers, and CSP. The real-router integration lock counts
driver queries and proves ordinary HTML still reaches the database.

Exact mutation RED output is unavailable. The retained pre-fix database
measurement is:

```text
base: carts=3936 sessions=1889
10x /static/... with cookies -> carts=3946 sessions=1899 (+10, +10)
10x /media/... with cookies  -> carts=3956 sessions=1909 (+10, +10)
10x /static/... no cookies   -> carts=3956 sessions=1909 (0)
```

PASS locks include `TestAnAssetRequestNeverReachesPerVisitorMiddleware`,
`TestNothingStatelessRendersChrome`, and
`TestTheRouterKeepsAssetsStatelessAndCompressesPages`.

This property was invisible to the pre-existing unit/layout gates. The new
driver-count integration lock is its direct evidence, so its missing RED paste
is a material audit-evidence gap.

**FLAGGED ROUTING DECISION:** an exact segment-bounded `statelessPrefixes`
filter stays inside the ordinary security/observability chain and bypasses only
locale, session, and cart work, also removing `Vary: Cookie` from immutable
assets. It is a deliberate subset of chrome-free prefixes, not an equal list.
Mounting static/media handlers outside the main chain was rejected because it
would duplicate or lose recovery, logging, CSP, and `nosniff` policy.

### 24. `no-compression` — `75088bf`, same commit as finding 23

Changed: embedded assets are precompressed when worthwhile and negotiated with
representation-specific validators. Dynamic responses use lazy stdlib gzip with
correct `Accept-Encoding`, status, HEAD, partial, flush/controller, `Vary`, ETag,
and pre-encoded semantics. The six protected surfaces—reset GET, rejected reset
POST, email verification, newsletter confirm, newsletter unsubscribe, and TOTP
enrolment—opt out through a private response marker.

Exact mutation RED output is unavailable. Retained pre-fix wire evidence:

```text
/static/css/app/app.css:
HTTP/1.1 200
Content-Length: 82856
Content-Type: text/css; charset=utf-8
no Content-Encoding
no Accept-Encoding in Vary

/:
Transfer-Encoding: chunked
no Content-Encoding
body 29693 bytes
```

PASS locks span the asset representation suite, the full dynamic-compression
semantics suite, `TestTheRouterKeepsAssetsStatelessAndCompressesPages`,
`TestSecretBearingAccountPagesAreNotCompressed`,
`TestSecretBearingNewsletterPagesAreNotCompressed`, and
`TestTheEnrolmentSecretPageIsNotCompressed`.

**FLAGGED SEMANTIC DECISIONS:** compression is disabled for secret-bearing HTML
rather than attempting token-aware heuristics. The implementation uses stdlib
gzip only; thresholds and representation rules remain explicit code. Static
precompressed assets own representation-specific validators and are never
double-wrapped by dynamic gzip; the static handler uses the internal
`web.NoCompress` marker to retain that representation authority. The private
marker must never become a response header. This property was invisible to the
pre-existing gates; the new dedicated locks cover it, but their missing RED
paste is a material audit-evidence gap.

### 25. `shipping-name-en-wiped` — `9c0c180`

Changed: append-only shipping-version forms carry both English translation
fields and include them in the audit event. An untouched submission preserves
their exact values; deliberate blanks become SQL NULL.

Exact specification-time allowlist-removal mutation RED; the pre-fix guard was
otherwise GREEN, and implementation-time reverse-mutation stdout is unavailable:

```text
--- FAIL: TestEveryViewModelFieldIsAssigned (0.03s)
    fields_test.go:55: AdminShippingMethod.CarrierEn is declared and never assigned.
    fields_test.go:55: AdminShippingMethod.NameEn is declared and never assigned.
```

PASS locks: `TestEveryViewModelFieldIsAssigned` and
`TestPublishingAVersionKeepsItsEnglishName`. The behavioural lock covers both
preservation and deliberate clearing back to SQL NULL.

**FLAGGED FORM/SCHEMA DECISION:** an untouched append-version form carries both
English values forward; an intentionally blank field clears to SQL NULL. The
implementation does not coalesce old values or add NOT NULL, because either
would make deliberate clearing impossible.

### 26. `setzoneprefixes-appends` — `5d392cf`

Changed: the entire prefix set is replaced atomically under a per-zone lock.
Blank-as-clear is distinct from invalid create input; invalid values remain on
their own accessible error row. The dead single-prefix removal API and its
deadcode allow entry were removed in the same finding.

Exact pre-fix probe RED; implementation-time mutation stdout is unavailable:

```text
--- FAIL: TestProbeSetZonePrefixesDropsNothing (0.01s)
    after setting the field to "100" the zone holds "100 200"
    zone holds "100 200", want "100"
```

PASS locks: `TestAZonesPostalCodesAreTheWholeSet`,
`TestAZonePrefixInfrastructureFailureIsNotADomainRefusal`,
`TestARefusedZonePrefixEditStaysOnItsOwnRow`,
`TestConcurrentZonePrefixSetsCommitOneWholeKnownLastSet`, and
`TestABlankPrefixSetIsDistinctFromAMissingCreationPrefix`.

**FLAGGED SEMANTIC DECISION:** whole-set editing is serialized per zone; last
completed edit wins as an atomic set. Prefix-by-prefix merge semantics were
rejected because the form represents the whole set. Blank on an existing edit
means clear; blank on create remains invalid. Upserts run before the omission
sweep so a prefix moved into this zone is assigned before old omissions are
removed. Reversing those statements remains GREEN because both occur in one
transaction; that ordering is a construction-audit decision, not a claimed
mutation lock.

### 27. `parse-helpers-collapse-states` — `0d0d9d5`

Changed: the protected admin count fields now distinguish malformed, overflow,
negative, and over-ceiling input from blank or valid zero. `parseBoundedInt`
intentionally maps blank and zero to the same accepted `(0, true)` domain value;
it no longer maps invalid text to that value. The handlers retain each field's
domain rule.

Exact mutation RED output is unavailable. Retained pre-fix probe evidence:

```text
parseSafetyStock("")        = 0 ; warranty_months refused = false
parseSafetyStock("12o")     = 0 ; warranty_months refused = false
parseSafetyStock("-3")      = 0 ; warranty_months refused = false
parseSafetyStock("2 4")     = 0 ; warranty_months refused = false
parseSafetyStock("١٢")      = 0 ; warranty_months refused = false
parseSafetyStock("24.0")    = 0 ; warranty_months refused = false
parseSafetyStock("1000001") = 0 ; warranty_months refused = false
variant parcel = 0/0/0 errs=map[]          (inputs "45o", "-1", "10,000")
over-ceiling longest 6000 -> 6000, errs=map[]
```

PASS locks include `TestAFormNumberKeepsInvalidDistinctFromZero`,
`TestAParcelSumCoversItsLongestSideBeforeWriting`,
`TestParcelAndSafetyBoundsGuardStoreCallers`,
`TestAMethodParcelLimitRefusalKeepsTheRawText`,
`TestAMistypedWarrantyTermIsRefusedNotDropped`, and
`TestAMistypedParcelDimensionDoesNotBecomeUnmeasured`.

**FLAGGED SEMANTIC DECISION:** an accepted zero may mean "unstated" for these
fields and remains a feature rule. Parsing owns syntax/bounds and must not turn
invalid input into that accepted zero. The spec proposed exported
`ParseOptionalCount`/`ParseCount`; implementation uses one unexported
`parseBoundedInt` because the protected fields share blank/zero behaviour and a
private helper avoids an unnecessary public API or boolean configuration flag.

### 28. `checkout-autocomplete-and-inputmode` — `7c59dd5`

Changed: checkout and account-address controls now carry field-specific browser
identities and keyboard hints for email, name, telephone, postal code, city,
district, and street. Pickup, invoice, coupon, note, and address-label controls
have explicit `autocomplete="off"` decisions where no truthful browser token
exists. The commit retained and newly locked the invoice/coupon off decisions
that already existed while adding the missing decisions elsewhere. Existing
labels, validation IDs, and submitted values remain intact.

Exact mutation RED output is unavailable. Retained pre-fix source probe:

```text
grep -c inputmode -> 0

autocomplete tokens on checkout:
1 autocomplete="off"
1 autocomplete="street-address"
```

PASS lock `TestEveryCheckoutFieldTellsTheBrowserWhatItIs` derives its control
corpus from multiple rendered checkout branches plus the account-address form.
Expected token and explicit-off decisions live in maps checked in both
directions against that rendered corpus. This browser meaning was invisible to
the pre-existing layout and Go gates. The new rendered lock runs under
`make verify`; its missing mutation RED paste is a material audit-evidence gap.

**FLAGGED BROWSER DECISIONS:** checkout carries no `enterkeyhint` because Enter
submits rather than advances. Telephone uses `type="tel"` and
`autocomplete="tel"` but deliberately has no `inputmode`, so `+886` remains
enterable. Pickup store code also has no numeric input mode because valid
Hi-Life codes can begin with a letter; it uses character capitalization with
spellcheck off. Postal code uses numeric. Controls with no truthful WHATWG
autofill token explicitly use `autocomplete="off"`.

## Post-wave Go and package-design cleanup

These commits do not change the required wave topology:

- `7f289b6` moves configuration/posture checks ahead of all external dependency
  construction and adds canonical-output fuzz closure to `SitePath`/`SiteOrigin`.
- `a310862` narrows internal APIs and uses closed enums where the domain is
  closed.
- `14db3a6` organizes translations by the feature that owns the wording.
- `571c13e` batches return payout reads and splits return handler/store workflow
  files around cohesive responsibilities. The product/shipping feature split
  belongs to behaviour-neutral Wave 4 commit `48e62bd`.
- `96dc801` completes locally after an invoice provider accepted a filing but
  the caller disconnected.
- `d21a015` makes return queue view construction explicit.
- `175b4cf` removes the production `admin_*.go` i18n naming scheme and organizes
  the one registry package by business domain.

Retained post-wave locks and decisions:

- `7f289b6` also removes package-global logging from assets by injecting the
  configured request logger, locked by
  `TestAWriteFailureUsesTheConfiguredRequestLogger`. TOTP crypto details are
  private (`keyBytes`, `secretCipher`, and their constructor/methods); `Store`
  remains the consumer API.
- `a310862` unexports zone/rate-limit capacity constants, removes production
  `Limiter.Size` and exported `payment.Actionable` test seams, renames
  `takenBy` to constraint-specific `hasConstraint`, and removes unused
  `PointsEntry.Reason`. Ledger-kind presentation is exhaustive and panics on an
  unknown internal kind, locked by
  `TestEveryPointsEntryKindHasACompletePresentation` and
  `TestAnUnknownPointsEntryKindIsAProgrammingError`.
  **FLAGGED CLOSED-ENUM DECISION:** an unknown database-backed internal kind is
  a programmer/schema drift error and fails fast instead of rendering a generic
  customer label.
- `571c13e` locks `Returns()` to exactly three named queries—`ReturnQueue`,
  `ReturnLines`, and `ReturnPayoutFacts`—independent of approved-row count with
  `TestTheReturnQueueUsesThreeQueriesForAnyNumberOfApprovedRows`. Exact N+1
  mutation stdout is unavailable. `TestBlockedReturnDiagnosticRouting`
  distinguishes `source mismatch keeps its figures` from `terminal provider
  state is not a source diagnostic` at `fillReturnPayoutState`'s classification
  and error-figure boundary. Source inspection shows the Handler logging queued
  issues; this is a proportionate classification lock, not an end-to-end logger
  assertion. Former four-method `Invoicer` is split into one-method
  `InvoiceReader` and three-method `InvoiceWriter`; `retryApprovedReturn` reads
  identity from the row and stays within five parameters. Store remains a
  concrete `*pgxpool.Pool` because a query-count test is not a production reason
  for a DBTX interface. Return Store workflow and handlers move to
  `returns_store.go` and `returns_handler.go` in the same package.
- `96dc801` changes only the local filing after a provider accepted an invoice.
  It uses `context.WithoutCancel(parent)` plus a fixed 15-second timeout, retains
  context values, and never repeats the provider call. Retained natural RED:

  ```text
  --- FAIL: TestAnAcceptedInvoiceIsFiledAfterTheRequestLeaves
  file an accepted invoice after request cancellation: begin invoice filing: context canceled
  ```

  Retained timeout-mutation RED:

  ```text
  filingContext() deadline = 0001-01-01..., ok=false; want 15s from now
  ```

  Locks: `TestAnAcceptedInvoiceIsFiledAfterTheRequestLeaves` and
  `TestFilingContextKeepsValuesDropsCancellationAndAddsADeadline`.
- `d21a015` replaces implicit cross-layer construction with an explicit
  `pages.AdminReturnsView{Rows: queue.Rows, Notice: noticeFor(r)}` while keeping
  payout issues private and the public view render-only. Retained natural RED:

  ```text
  --- FAIL: TestEveryViewModelFieldIsAssigned
  AdminReturnsView.Notice is declared and never assigned.
  AdminReturnsView.Rows is declared and never assigned.
  ```

The i18n result deliberately remains one package. Translation catalogue objects
share one registry, lifecycle, completeness census, and exported API; adding
subpackage boundaries only to imitate visual folders would add dependencies
without creating an ownership boundary. File names now describe domains and
features instead of the caller role. A retained cold census reported equality at
1,463 translation-key objects and 1,482 total exported package objects. The
manifest generator, exact serialized bytes, and trustworthy full digest were not
retained, so this report does not claim a hash. Round 11 must independently
reproduce the key-tuple and exported-API manifests; equal counts alone do not
prove equal content.

## Final gates at implementation tip `175b4cf`

```text
make verify
verify: PASS (unit tests only — make verify-all adds the database suite)
```

Retained outcome: `make test-integration` exited 0 with every integration
package reporting `ok` under `-race -shuffle=on`. The target emits package
lines rather than a synthesized final PASS sentence.

```text
make check-layout
layout check PASS
```

The checked layout corpus contains 113 viewports; that count is a catalogue
fact, not part of the exact two-line tail above.

Exact retained `make deadcode` tail:

```text
deadcode: PASS — 4 unreachable functions, all explicitly reasoned
```

Per the decided sequence, `make schema-drift` ran once at the end of Wave 1,
not after later non-schema work. The implementation worktree was clean at
`175b4cf` before these review handoff documents were added.

The documentation handoff re-ran both code gates without changing production
files. Exact `make verify` tail:

```text
verify: PASS (unit tests only — make verify-all adds the database suite)
```

The handoff's `make test-integration` exited 0 with every emitted package line
reporting `ok` under `-tags=integration -race -shuffle=on`; it has no final
summary line of its own.

## Semantic decisions requiring audit

Every decision is described beside its finding. The decisions most likely to
change expected semantics are repeated here so they cannot hide in prose:

1. **Loyalty lot model:** zero-point clawbacks are durable; clawbacks reverse
   only unconsumed remainder; no negative balance or invariant exemption.
2. **Refund definition:** canonical `order_refunds` includes store credit,
   including cancellation credit.
3. **Return retry:** terminal provider refusal is manual operator work, not an
   unlimited retry action.
4. **Site paths:** browser path safety has one Go grammar and no approximate SQL
   CTA CHECK.
5. **Production dependencies:** SMTP is required in production; completely
   disabled Stripe remains an accepted documented mode.
6. **TOTP key:** clean break to exactly 32 decoded bytes for non-empty input,
   with no passphrase-KDF compatibility; empty remains development-only disabled
   mode.
7. **ECPay:** void date is fixed, but issue remains remote-first because the
   provider allocates the authoritative number.
8. **Rate-limit keys:** raw and digested keys occupy separate namespaces.
9. **Compression:** token-bearing HTML opts out entirely.
10. **Zone prefix editing:** the form replaces one atomic whole set.

## Anything not resolved

All 28 requested finding boundaries have landed implementations. The following
work or audit evidence remains open and must not be blurred into that statement.

Evidence preservation is incomplete: exact implementation-time mutation
transcripts are missing for most Wave 0 and Wave 1 findings and for Wave 4.
Five invisible properties still need a new dedicated pasted RED:
`restore-drill`, static requests making no visitor-state queries, compression
semantics, checkout browser-field semantics, and real production-startup
refusal/order. Startup has only a retained abridged RED excerpt. Argon2 is the
sixth special case: its length/PHC guards need deterministic mutation RED, while
its timing claim needs a construction audit and explicit limitation rather than
a wall-clock RED. The i18n reorganization also needs independently reproducible
content/API manifests because only their prior counts, not a trustworthy
serialized artifact and digest, survived the session.

The specifications deliberately named adjacent work that remains in
`docs/roadmap.md`:

- A9 narrows the remaining broad admin `ErrRefused` classifications;
- A10 makes migration static-analysis guards read the full migration history;
- A11 builds the customer product-answer route whose Store seam remains on the
  bidirectional deadcode allowlist;
- A12 allows a return's independently payable store-credit half to settle after
  a terminal card-provider refusal;
- A13 renders back-office timestamps in the shop zone rather than process local
  time; and
- A14 fixes the remaining admin/warranty parsers that still collapse malformed
  money/count input into an unstated zero.

Two verification-apparatus gaps from the findings document also remain only
partially addressed: the new real-router test covers asset, media, and home
middleware behaviour but not the full registration/auth-wrapper matrix, and
return ownership still lacks a direct HTTP handler-boundary test. Pool sizing
and per-pool `statement_timeout` remain separate measurement-driven capacity
questions, not changes smuggled into the static-traffic fix.

The new cold-review session must re-run every missing mutation, audit these
remaining boundaries, and never infer acceptance from this report.
