# ecpay-remote-first

**Verdict** PARTLY · **Severity** high · **Origin** external report (verified this round)

**Files** internal/invoice/issue.go, internal/invoice/store.go, internal/invoice/invoice_test.go, internal/invoice/integration_test.go, docs/roadmap.md, CLAUDE.md


## Root cause

HALF 2: a comment asserting a precondition that nothing in the code enforces — CLAUDE.md's own mistake #31/#34 shape. issue.go:138-139 says ECPay "accept today's for one issued today, which is the only case this reaches", and no caller, view model, template or constraint restricts the void to the issue day. The correct value was already in hand and already used correctly by the neighbouring operation: Store.Void reads the row via LiveInvoice (which selects d.issued_at) and throws the date away at store.go:176, while Store.Allowance passes live.IssuedAt at store.go:263. One question — "what date does this document bear" — answered two ways in two functions of one file, which is CLAUDE.md's #13 shape ("two correct halves that disagree"), and no guard here can see it because every guard asks what is ABSENT.

The deeper reason it survived: the live-staging test suite fixed the wire format of Issue and Allowance (invoice_test.go:252, :351) and never of Invalid, so the one endpoint whose date field is strictly validated is the one endpoint whose request body no test has ever read.

HALF 1: not a defect. A recorded architectural decision — the provider allocates the number, so nothing local can be written before it answers — whose stated reasoning still holds. What the decision does not name is that the post-provider write inherits the HTTP request's cancellation, so an ordinary client disconnect, not just a crash, lands in the accepted window.


## Reproduction — the evidence this rests on

Two halves, verified separately. Everything below was EXECUTED (real ECPay staging API + the dev Postgres), not reasoned about. Probe file was internal/invoice/zz_probe_test.go, package `invoice`, now deleted.

=== HALF 2 — the void date: CONFIRMED, reproduced against the real provider ===

internal/invoice/issue.go:135-142, Gateway.Void:

    _, err := g.call(ctx, "/B2CInvoice/Invalid", invalidRequest{
        MerchantID: g.merchantID,
        InvoiceNo:  number,
        // ECPay want the invoice's own date and accept today's for one issued
        // today, which is the only case this reaches.
        InvoiceDate: time.Now().Format("2006-01-02"),
        Reason:      truncate(reason, 20),
    })

Probe A — issue a real staging invoice, then void it twice, once with a date that is not the issue date and once with its own:

    ISSUED number=LA25024809 issuedAt=2026-08-24T11:07:18Z ref=7295
    VOID with wrong date 2026-08-23 => invoice: refused by the provider: 無發票號碼資料 (RtnCode 1600003)
    VOID with own date   2026-08-24 => <nil>

ECPay's /B2CInvoice/Invalid validates InvoiceDate against the invoice's actual issue date and answers 1600003. Gateway.Void always sends today's, so a void succeeds on the issue day and can NEVER succeed afterwards.

The comment's "which is the only case this reaches" is enforced nowhere. The void form is offered for any live invoice regardless of age: pages.CanVoidInvoice (internal/ui/pages/admin.go:409-413) is `LiveInvoice() ok && InvoicingEnabled` — no date test; the form is internal/ui/pages/admin.templ:472-482; the route is cmd/goen/server.go:257. Store.Void (internal/invoice/store.go:165-186) reads the invoice and calls `s.gateway.Void(ctx, live.Number, reason)` at :176 with no date at all.

The repository already knows the correct semantics one function away: Store.Allowance passes `InvoiceDate: live.IssuedAt` (store.go:263) — the ORIGINAL date — while Void passes time.Now(). LiveInvoice (internal/invoice/query.sql:80-87) already returns `d.issued_at`, so the right value is in scope at store.go:176 and is discarded.

There is no test of the Invalid wire body anywhere. `grep -rn "Void" internal/invoice/*_test.go` returns exactly one hit, invoice_test.go:151-152, which only asserts ErrDisabled on an unconfigured gateway. This is not a weak lock, it is no lock.

Probe B — the timezone trap in the obvious fix (dev DB round trip). parseECPayTime (issue.go:~225) parses ECPay's reply with no zone, so it yields the provider's Taipei wall clock LABELLED UTC; invoice_documents.issued_at is timestamptz (migrations/001_initial_schema.up.sql:2322) and pgx hands it back in the process's local zone:

    parsed        = 2026-08-24 20:00:00 +0000 UTC   Format=2026-08-24
    round-tripped = 2026-08-25 04:00:00 +0800 CST   Format=2026-08-25  UTC().Format=2026-08-24

So `live.IssuedAt.Format("2006-01-02")` is WRONG for every invoice issued 16:00–23:59 Taipei on a deployment whose TZ is Asia/Taipei. `.UTC().Format(...)` recovers exactly the string ECPay sent.

Probe C — the sibling endpoint, checked so the fix is not over-applied. /B2CInvoice/Allowance does NOT validate the date:

    issued LA25024811 at 2026-08-24 11:09:12 +0000 UTC
    Allowance with date 2026-08-25 => <nil>
    Allowance with date 2026-08-24 => <nil>

Only Invalid is strict. Do not "fix" Allowance on symmetry.

=== HALF 1 — remote-first Issue: REFUTED as a re-filed documented decision ===

The ordering is exactly as the reviewer states: internal/invoice/store.go:110 `doc, err := s.gateway.Issue(ctx, ...)`, then :124 `if err := s.file(ctx, ...)`, same ctx, and file() opens its own transaction at :129.

But CLAUDE.md records this deliberately, in the invoice section:
  "For an INVOICE the provider is called FIRST and the row written after, because the number is theirs to allocate. That leaves the window the refund path already documents, in the recoverable direction: a document filed with the 加值中心 and absent here is visible from ECPay's console ... A row claiming a filing that does not exist would be visible from nowhere."
And internal/invoice/query.sql:43-45: "File an issued document, AFTER the provider accepted it: the number is theirs to allocate, and invoice_documents_number_present cannot tell an invented one from a real one." The CHECK it names is at migrations/001:2339-2341. The reasoning still holds, and the allowance path took the opposite ordering for a stated reason (no idempotency field on that endpoint).

Two sub-points I did check rather than assume:

Probe D — the reviewer's "a retry fails because RelateNumber is a duplicate" is FACTUALLY RIGHT, and the reply carries no recovery information:
    first  Issue(PROBE442475000) => number="LA25024810" err=<nil>
    second Issue(PROBE442475000) => number=""           err=invoice: refused by the provider: B2C開立發票 自訂編號重覆，請重新設定 (RtnCode 5070357)
`attempt` (internal/invoice/query.sql:29-30) counts invoice_documents rows, which is 0 when the local write was lost, so relateNumber() (store.go:410-415) returns the unsuffixed order number and the retry repeats the same key. The reply does not return the already-allocated number, so goen cannot self-heal — only a human reading ECPay's console can.
Note in passing: CLAUDE.md's sentence "re-issuing is refused by invoice_documents_one_active_invoice_per_order" is inaccurate for this window — with no local row that partial unique index (migrations/001:2368-2370) has nothing to refuse. What actually refuses is ECPay's 5070357. The outcome the decision claims (no second document) is right; the mechanism it names is not.

Probe E — the one thing the decision does NOT cover, the reviewer's "if the user disconnects, the context is cancelled". Store.file receives the request context (r.Context(), internal/admin/handler.go:2337 -> store.IssueInvoice -> invoice.Store.Issue), which net/http cancels on client disconnect. Against the dev DB:
    pool.Begin(cancelled ctx)   => context canceled
    tx.Commit(after cancel)     => timeout: context already done: context canceled
So an operator closing the tab or a proxy timing out after ECPay allocated the number loses the local row — a far more ordinary trigger than the process crash the decision contemplates, and one the codebase already knows how to close (internal/media/render.go:101 uses context.WithoutCancel for exactly this shape).


## Blast radius

HALF 2 (confirmed): every void of a 統一發票 attempted on any day after it was issued. CLAUDE.md states the stakes itself — "A 統一發票 cannot be edited, so the only correction is void-then-reissue" — so this is the sole correction path for a wrongly-issued tax document, and it works only within the calendar day of issuance. In practice a wrong invoice is noticed after the customer complains, which is the next day at the earliest, so the working case is the rare one and the broken case is every real one.

It is not silent to the operator but it is silent about its cause: internal/admin/handler.go:2384-2387 maps invoice.ErrRejected to a `?invoicefailed=1` redirect and logs "invoice void refused" with RtnMsg 無發票號碼資料 ("no such invoice number") — which sends whoever reads it to the invoice NUMBER, the one field that is correct. The invoice stays live at the 加值中心 and live in invoice_documents; CanIssueInvoice (internal/ui/pages/admin.go:387-396) then refuses to offer a reissue because a non-voided invoice exists, so the order is stuck with a wrong tax document and no button that can move it. Retrying tomorrow fails identically, forever.

docs/roadmap.md:44-45 records the feature as done with "真的作廢與折讓" (a real void and allowance), and CLAUDE.md lists "four things the live staging API taught"; this is a fifth it did not, so both documents currently assert a capability that has never worked past midnight.

HALF 1 (refuted): the window is real but bounded and visible — the invoice exists at ECPay's console, no second document can be filed (5070357), and no money moves. Its blast radius is one order's invoice needing manual reconciliation, which is the cost the recorded decision accepted in exchange for never writing a row that claims a filing nobody made.


## Fix

PRIMARY FIX — pass the invoice's own issue date to the void, in ECPay's own convention.

1. internal/invoice/issue.go:125 — widen the signature:
     func (g *Gateway) Void(ctx context.Context, number string, issuedAt time.Time, reason string) error
   and at :140 replace
     InvoiceDate: time.Now().Format("2006-01-02"),
   with
     InvoiceDate: issuedAt.UTC().Format("2006-01-02"),
   The .UTC() is load-bearing and MUST NOT be dropped: parseECPayTime (issue.go, bottom of file) parses ECPay's InvoiceDate with no zone, so an issued_at holds the provider's Taipei wall clock labelled UTC; invoice_documents.issued_at is timestamptz (migrations/001_initial_schema.up.sql:2322) and pgx returns it in the process's local zone. Measured: an ECPay reply of "2026-08-24 20:00:00" round-trips as 2026-08-25 04:00:00 +0800, so a plain .Format sends tomorrow's date and earns 1600003 for every invoice issued 16:00–23:59 Taipei. .UTC().Format reproduces exactly the string ECPay sent.
   Also add a guard beside the existing number/reason ones: an `issuedAt.IsZero()` void is ErrRejected ("which day was it issued?"), so a caller that forgets the field fails in words rather than sending 0001-01-01.
   REPLACE the comment at :138-139. It states a restriction nothing enforces. New wording should say what is now true: ECPay's Invalid endpoint validates InvoiceDate against the invoice's own issue date and answers 1600003 無發票號碼資料 for a mismatch — a wrong date is indistinguishable from a wrong number — and that the value is the provider's own, hence .UTC().

2. internal/invoice/store.go:176 — pass it:
     if voidErr := s.gateway.Void(ctx, live.Number, live.IssuedAt, reason); voidErr != nil {
   `live` already carries IssuedAt: internal/invoice/query.sql:80-87 (LiveInvoice) selects d.issued_at. No query change, no sqlc regeneration, no migration, no schema change. Do not hand-edit internal/db.

3. Update invoice_test.go:151 — the existing disabled-gateway assertion calls the two-argument form and must gain a date argument.

WHAT THE FIX MUST NOT DO:
- Must NOT change Gateway.Allowance's date handling on symmetry. Measured against staging: /B2CInvoice/Allowance accepted both the correct date and the following day for invoice LA25024811. Only Invalid is strict. Changing Allowance would be a change with no measured basis, and issue.go:157's plain .Format is harmless there today; if anyone later makes it match, it needs the same .UTC().
- Must NOT touch parseECPayTime's `time.Now()` fallback without deciding the convention. That branch returns a LOCAL-zoned time whose .UTC() date can be a day off, so it silently disagrees with the parsed branch. If it is touched at all, it must produce the Taipei wall clock labelled UTC so both branches mean the same thing. The convention-free alternative — carry ECPay's own InvoiceDate string on Document rather than a time.Time — is a larger change and is not required.
- Must NOT add a same-day restriction to CanVoidInvoice to make the comment true. The invoice cannot be edited, the void is the only correction the 財政部 accepts, and hiding the button would remove the correction path rather than fix it.

DOCUMENTATION (both files currently assert a capability that has never worked past midnight):
- docs/roadmap.md:44-45 says 「真的作廢與折讓」. Leave the claim but it only becomes true with this fix.
- CLAUDE.md's invoice section lists "Four things the live staging API taught that its field list does not say, each now a test". Add the fifth: Invalid validates InvoiceDate against the invoice's own issue date; a mismatch is 1600003 無發票號碼資料, whose message names the number and not the date. Update the count in the sentence.

HALF 1 — no fix required; the ordering is a recorded decision. One narrow, cheap hardening is worth queueing BY NAME rather than smuggling into this fix: at internal/invoice/store.go:124, call s.file with context.WithoutCancel(ctx) (plus a bounded WithTimeout), the pattern already in internal/media/render.go:101. Measured: pool.Begin on a cancelled context returns "context canceled" and Commit returns "timeout: context already done", so an operator closing the tab after ECPay allocated a number is enough to lose the row. That narrows the accepted window from "client disconnect or crash" to "crash", which is what the decision actually contemplates. If it is done, CLAUDE.md's sentence "re-issuing is refused by invoice_documents_one_active_invoice_per_order" should also be corrected: with no local row that partial index has nothing to refuse, and what actually refuses a repeat is ECPay's RelateNumber (5070357) — measured.


## The lock, and how to see it fail first

There is currently NO test of the /B2CInvoice/Invalid request body — invoice_test.go:151-152 is the only Void reference and asserts only ErrDisabled — so this is a new lock, not a strengthened one.

LOCK 1 (unit, the wire format) — internal/invoice/invoice_test.go, package `invoice`:
`TestAVoidSendsTheInvoicesOwnIssueDate`. Use the httptest.Server pattern the Issue and Allowance wire tests already use (invoice_test.go:252 and :351): the handler decodes the envelope, calls g.open on Data, unmarshals into `invalidRequest`, records InvoiceDate, and answers a sealed {"RtnCode":1}.
The fixture must carry BOTH hazards or it cannot see the defect:
 - issuedAt := parseECPayTime("2026-08-24 20:00:00") — an EVENING time, i.e. one whose Taipei wall-clock date differs from its date in a non-UTC local zone;
 - t.Setenv("TZ", "Asia/Taipei") in the subtest (or construct the value through the same pgx round trip), so time.Local is not UTC. On a UTC runner the .UTC() mutation stays green and the lock proves nothing — that is the same false-green mode CLAUDE.md records for the warranty month-count assertion.
Assert the recorded InvoiceDate == "2026-08-24" exactly, against the value passed in and never against "today", or the test dates itself.

MUTATIONS THAT MUST BE SEEN RED, and the edit must be seen to apply (CLAUDE.md false-green mode #3 — grep the file after editing, do not trust a count):
 (a) restore `InvoiceDate: time.Now().Format("2006-01-02")` at issue.go:140 → RED with the recorded date being the run date instead of 2026-08-24. This is the lock on the finding itself.
 (b) drop only the `.UTC()`, leaving `issuedAt.Format(...)` → RED with "2026-08-25". This is the lock on the timezone half, and it is the mutation that proves the fixture's evening time and TZ are doing work.
 (c) at store.go:176 pass `time.Now()` instead of `live.IssuedAt` → this unit test stays GREEN, which is why lock 2 exists.

LOCK 2 (integration, the caller) — internal/invoice/integration_test.go, //go:build integration:
`TestAnInvoiceIssuedYesterdayCanStillBeVoided`. File a row through RecordInvoiceDocument with issued_at = now() - interval '1 day' (a fixture that reaches the state under test — CLAUDE.md mistake #33: a row aged to just inside the window passes either way), point the Store's gateway at a recording httptest server, call Store.Void, and assert the wire InvoiceDate equals the row's own issued_at rendered in the provider's convention — NOT today's date, and NOT merely "the void returned nil", since a recording stub returns success whatever date it is handed.
MUTATION: revert store.go:176 to the two-argument call (or pass time.Now()) → must go RED. Without this lock only the Gateway is held and the caller is free to drift back, which is the shape that produced the defect in the first place.

NOT A LOCK, and deliberately excluded: a test that calls the real staging API. It was the right instrument to FIND this (and did: LA25024809 refused with 1600003 for 2026-08-23, accepted for 2026-08-24) and is the wrong instrument to gate a build on — it needs the network, files real documents against a shared public merchant id, and cannot construct an invoice issued yesterday. Record the staging transcript in the fix's commit message as the evidence for what the httptest stub is standing in for.
