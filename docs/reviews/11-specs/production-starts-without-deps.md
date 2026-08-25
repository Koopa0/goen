# production-starts-without-deps

**Verdict** PARTLY · **Severity** high · **Origin** external report (verified this round)

**Files** cmd/goen/main.go, internal/email/sender.go, internal/outbox/outbox.go, internal/outbox/query.sql, internal/payment/handler.go, internal/ui/pages/pay.templ, cmd/goen/server.go, docs/reviews/08-acceptance-prompt.md


## Root cause

A degraded fallback whose failure signature is indistinguishable from success, chosen by absence rather than by decision.

`email.Sender` is a single-method interface whose only failure channel is the returned error, and `internal/outbox` defines nil as "delivered" (outbox.go:79, "returning nil marks it delivered"). LogSender is a *development* stub that satisfies that contract by lying: it performs no delivery and reports none. That is correct for development and is a data-loss oracle in production.

Nothing binds the two facts together. checkProductionPosture exists precisely to refuse "a security feature silently off" and knows the production signal (SecureCookies), but its subject was scoped to security settings and mail was never added — so the one place in the codebase that asks "is this configuration fit to serve" does not ask about the subsystem whose failure mode is silent by construction. The startup Warn is the whole defence, and a warning is a one-shot line in a log that nothing re-reads; every other invariant in this repository is held by something that refuses.

This is the repository's own recorded shape, from CLAUDE.md's list: "two correct halves that disagree" — LogSender is right that development should not dump live reset tokens into a log, and outbox is right that nil means delivered — "which every guard here is blind to because they all ask what is ABSENT." No table is missing a writer, no field is unassigned, no column is unread. The letter is enqueued, handled, and stamped. The only thing absent is the mail.

It is also the exact inverse of internal/email/sender_test.go:169-171, whose doc comment already names the mechanism — "it returns nil, so the outbox stamps the message DELIVERED and nothing looks wrong" — and treats it as a reason not to log the body, never as a reason not to run that way. The consequence was seen and the configuration door was left open.


## Reproduction — the evidence this rests on

EXECUTED, two ways. The claim has two halves; one is refuted, one reproduced end to end.

=== HALF A — Stripe. REFUTED (recorded decision, and the stated consequence is wrong) ===

CLAUDE.md states it explicitly:
  "`GOEN_STRIPE_SECRET_KEY` may be empty (the site still sells; the payment page says 金流尚未啟用), but a key without `GOEN_STRIPE_WEBHOOK_SECRET` refuses to start, because that combination takes money over an endpoint nothing authenticates."

The reasoning still holds and the half-configuration IS refused — payment.NewGateway is called at cmd/goen/main.go:250 inside openProviders and returns an error for key-without-webhook-secret. cmd/goen/main.go:308-311 is the deliberate warning for the fully-empty case:
    if !gateway.Enabled() {
        log.Warn("stripe is not configured; the payment page will say so",
            "set", "GOEN_STRIPE_SECRET_KEY and GOEN_STRIPE_WEBHOOK_SECRET")
    }

The stated consequence ("only hit a 503 at the payment page") is also inaccurate in the repo's favour. GET /orders/{number}/pay renders HTTP 200 with Enabled=false (internal/payment/handler.go:59), and internal/ui/pages/pay.templ:48-60 suppresses the pay button and renders `<p class="ui-alert ui-alert--info" role="status">` carrying i18n.KeyPayDisabled. Only POST Start is 503 (internal/payment/handler.go:87-92), as a rendered notice page, not a bare error.

The stock-hold-before-refusal sequence IS real: checkout never consults the gateway (cmd/goen/server.go:99 passes cart only `sessionCloser(gateway)`, which server.go:570-577 uses solely for the cancel doors), so cart.PlaceOrder calls hold_inventory (internal/cart/query.sql:246) regardless. But that is the documented behaviour of "the site still sells", and cart.SweepForever + release_reservation return the units after cart.HoldTTL. Nothing is lost.

=== HALF B — SMTP. CONFIRMED, reproduced against the live server ===

1. The posture check does not ask about mail. cmd/goen/main.go:125-147:
    // checkProductionPosture refuses a configuration that would serve the site with
    // a security feature silently off. SecureCookies is the production signal:
    // it is false only under the development opt-out GOEN_INSECURE_COOKIES.
    func (cfg *config) checkProductionPosture(log *slog.Logger) error {
        if cfg.TOTPKey == "" {
            if cfg.SecureCookies { return errors.New("GOEN_TOTP_KEY is required: ...") }
            ...
        }
        if cfg.SecureCookies && os.Getenv("GOEN_BASE_URL") == "" { return errors.New(...) }
        return nil
    }
   Two settings refuse; SMTPAddr is never read here.

2. cmd/goen/main.go:453-459:
    func newSender(cfg *config, log *slog.Logger) email.Sender {
        if cfg.SMTPAddr == "" {
            log.Warn("no SMTP configured; email will be written to the log", "set", "GOEN_SMTP_ADDR")
            return email.LogSender{Log: log}
        }

3. internal/email/sender.go:41-48 — LogSender.Send omits the body by default and returns nil:
        s.Log.InfoContext(ctx, "email (not sent: no SMTP configured)", attrs...)
        return nil

4. internal/outbox/outbox.go:161-165 — nil from the handler is the delivered signal:
        if err := h(ctx, m.Payload); err != nil { s.reschedule(ctx, m, err); return false }
        if err := s.q.MarkOutboxDelivered(ctx, m.ID); err != nil {

LIVE REPRODUCTION (running dev server, 127.0.0.1:9700):
    $ curl -s -o /dev/null -w "%{http_code}\n" -X POST http://127.0.0.1:9700/forgot \
        --data-urlencode "email=layout-cust@goen.invalid"
    303
    $ psql "$GOEN_DATABASE_URL" -c "select topic, attempts, delivered_at, last_error, payload->>'email' from outbox_messages order by id desc limit 5;"
             topic          | attempts |         delivered_at          | last_error |            to
    ------------------------+----------+-------------------------------+------------+---------------------------
     account.email_verify   |        1 | 2026-08-24 03:06:47.488697+00 |            | timing-probe@goen.invalid
     account.password_reset |        1 | 2026-08-24 03:06:37.494534+00 |            | layout-cust@goen.invalid
     order.shipped          |        1 | 2026-08-24 02:24:22.5202+00   |            | layout-cust@goen.invalid
     order.placed           |        1 | 2026-08-24 02:24:22.517486+00 |            | layout@goen.invalid

A live password reset was stamped delivered_at, attempts=1, last_error NULL, and no mail left the process. `.invalid` is a reserved TLD, so a real SMTPSender could not have returned success for any of these rows — attempts=1 with no error is itself proof the LogSender path ran.

TEMPORARY GO TEST (written at cmd/goen/zz_probe_test.go, run, then DELETED — `git status --porcelain | grep -c cmd/goen` = 0; the other zz_probe files in the tree belong to concurrent agents, not to me):
    cfg := &config{SMTPAddr: "", SecureCookies: true, TOTPKey: "k"}
    t.Setenv("GOEN_BASE_URL", "https://shop.example")
    cfg.checkProductionPosture(log)   -> nil
    newSender(cfg, log)               -> email.LogSender{ShowBody:false}
    s.Send(ctx, &email.Message{...})  -> nil
Output:
    checkProductionPosture ACCEPTED a production config with no SMTP
    LogSender.Send returned nil -> outbox stamps delivered_at; body never logged, never sent
    --- PASS

NOT A RECORDED DECISION. `grep -rn -i "smtp|LogSender" CLAUDE.md docs/roadmap.md` returns nothing. The only prose on it is docs/reviews/08-acceptance-prompt.md:42 — "Stripe, SMTP and TOTP keys may be empty — the features then say so rather than half-working, and **two of them now refuse to start** in a production posture" — which is exactly wrong about SMTP: mail does not say so, it half-works. Stripe says so on the page; TOTP and BASE_URL are the two that refuse. README.md:204 documents the fallback as a mechanism ("Empty writes mail to the log instead of sending it") without deciding that production may run that way.


## Blast radius

Only the SMTP half survives. A deployment in a production posture (SecureCookies true, i.e. GOEN_INSECURE_COOKIES unset) that forgets GOEN_SMTP_ADDR starts cleanly, serves every page, and silently discards EVERY transactional letter goen produces, for as long as it runs.

What is lost, from cmd/goen/main.go's startWorkers topic registrations: account.password_reset, account.email_verify, order.placed, order.paid (receipt), order.shipped (dispatch notice), the restock notice, the newsletter welcome carrying the unsubscribe token, and every bulk newsletter issue.

Silent in every direction:
- The customer sees a 303 and a "we have sent you a link" page. A locked-out customer is permanently locked out: argon2 makes the password unrecoverable and /forgot is the only door, so the account is dead with no error anywhere.
- The operator gets ONE slog.Warn line at boot (cmd/goen/main.go:456) and nothing afterwards. /admin/health cannot see it: every predicate it reads is `delivered_at IS NULL` (internal/outbox/query.sql:7 for pending, :34 for StuckOutbox), and these rows are stamped delivered. The page reports a healthy, empty queue while no mail has ever been sent.
- The row itself is not evidence: last_error is NULL and attempts is 1, indistinguishable from a genuine delivery. After outbox.Retain (30 days) the row is swept and even the recipient list is gone.
- Legally material for one message: CLAUDE.md records that the order confirmation carries 消保法 §18 I's disclosure and that under §19 III a shop that does not provide it extends the rescission right to four months. Silently unsent means every order carries that tail.

Not reachable by accident in development — GOEN_INSECURE_COOKIES=1 is the dev posture and the warning is appropriate there. It bites exactly once, on a real deployment, and produces no signal that anything is wrong.

Second, narrower path to the same silence, from a deployment that DID configure SMTP: cmd/goen/main.go:462-467, when GOEN_SMTP_USER is set and GOEN_SMTP_ADDR is not host:port, logs Error and returns LogSender — same silent success, but reached by an operator who believes mail is on. (A malformed addr with no SMTP user is safe: SMTPSender fails at dial, the outbox reschedules, and StuckOutbox surfaces it — which is the correct shape and shows what the auth path should do.)


## Fix

Two edits, both in cmd/goen/main.go. Nothing in internal/, no schema change, no generated code.

--- 1. checkProductionPosture refuses an unconfigured mailer in a production posture ---

cmd/goen/main.go, inside `func (cfg *config) checkProductionPosture(log *slog.Logger) error` (starts line 128), after the existing TOTPKey block and before the GOEN_BASE_URL block, add a block with the SAME two-armed shape as the TOTP one:

    // An empty address leaves newSender on email.LogSender, which returns nil —
    // and nil is what the outbox reads as delivered (outbox.go:161). So every
    // password reset, verification link, receipt and dispatch notice is stamped
    // sent and goes nowhere, with attempts=1 and no last_error, invisible to
    // /admin/health because every predicate it reads is delivered_at IS NULL.
    if cfg.SMTPAddr == "" {
        if cfg.SecureCookies {
            return errors.New("GOEN_SMTP_ADDR is required: without it mail is " +
                "written to the log and marked delivered, so password resets and " +
                "order mail are lost with nothing to show for it. Set it, or set " +
                "GOEN_INSECURE_COOKIES=1 for local development")
        }
        log.Warn("GOEN_SMTP_ADDR is not set; mail is logged, NOT sent, and marked delivered",
            "set", "GOEN_SMTP_ADDR")
    }

Widen the doc comment above the function from "a security feature silently off" to "a security feature silently off, or a subsystem that reports success without doing its work" — the comment currently states a scope narrower than what the function must now cover, which is CLAUDE.md mistake #31's shape.

Delete the now-duplicated warning at cmd/goen/main.go:456-457 inside newSender: the posture check owns this sentence, and two places saying it is the drift this file warns about. newSender keeps only the fallback.

--- 2. newSender's malformed-address path becomes fatal instead of a silent LogSender ---

Change the signature at cmd/goen/main.go:454 to `func newSender(cfg *config, log *slog.Logger) (email.Sender, error)`. The SplitHostPort failure at lines 462-467 must return the error rather than `email.LogSender{Log: log}`:

    host, _, err := net.SplitHostPort(cfg.SMTPAddr)
    if err != nil {
        return nil, fmt.Errorf("GOEN_SMTP_ADDR %q is not host:port: %w", cfg.SMTPAddr, err)
    }

It must NOT keep the LogSender fallback for this case, and must NOT keep logging Error and continuing: an operator who set GOEN_SMTP_ADDR and GOEN_SMTP_USER believes mail is on, and this is the one path that silently contradicts them.

Because the failure must be fatal at startup and not inside a worker goroutine, the sender is built in `run()` and passed down rather than constructed inside startWorkers:
  - add a `sender email.Sender` field to `workerDeps` (cmd/goen/main.go:474-483);
  - in `run()`, immediately after the `cfg.checkProductionPosture` call at line 313, add:
        sender, senderErr := newSender(&cfg, log)
        if senderErr != nil { return senderErr }
  - at the workerDeps literal (line ~330) pass `sender: sender`;
  - at cmd/goen/main.go:489, replace `Sender: newSender(d.cfg, d.log)` with `Sender: d.sender`.

Constraints. Do NOT make LogSender.Send return an error — it is the correct development sender and an error there would make the outbox reschedule every dev letter until MaxAttempts, filling /admin/health's stuck list with noise. Do NOT add a second SMTP-shape check inside checkProductionPosture (i.e. do not call net.SplitHostPort there too): the rule about what a valid address looks like lives in newSender alone, and a copy in the posture check is exactly the two-halves-that-disagree pattern CLAUDE.md #13/#30/#31 record. Do NOT introduce an interface so a test can substitute a sender — .claude/rules/package-organization.md and interfaces.md forbid tests as a reason, and email.Sender already exists as a production abstraction. Keep the escape hatch as GOEN_INSECURE_COOKIES=1, the production signal the function already uses; do not invent a new GOEN_ALLOW_NO_MAIL variable.

Also update docs/reviews/08-acceptance-prompt.md:42, which currently claims SMTP "says so rather than half-working" and that two settings refuse — after this it is three, and SMTP is one of them.


## The lock, and how to see it fail first

New file cmd/goen/main_test.go, package main (no _test package: checkProductionPosture and newSender are unexported). No Docker, no build tag — it belongs in `make verify`, so it must be in the race-tested unit set.

TEST 1 — TestAProductionPostureRefusesAnUnconfiguredMailer

Table-driven over the four corners, asserting an ERROR NAMING THE VARIABLE and not merely that an error occurred (.claude/rules, and CLAUDE.md mistake #8 — bind the assertion to which rule refused, or the TOTP/BASE_URL refusals in the same function satisfy it):

  | SMTPAddr | SecureCookies | want                                        |
  |----------|---------------|---------------------------------------------|
  | ""       | true          | error whose text contains "GOEN_SMTP_ADDR"  |
  | ""       | false         | nil, and the warn log contains GOEN_SMTP_ADDR |
  | "smtp:587" | true        | nil                                         |
  | "smtp:587" | false        | nil                                         |

Every row sets TOTPKey non-empty and t.Setenv("GOEN_BASE_URL", "https://shop.example") so the two EXISTING refusals cannot be the thing that fires — without that, row 1 passes with the new block deleted, because the TOTP arm returns an error first. That is the false-green this test must not have. Assert `strings.Contains(err.Error(), "GOEN_SMTP_ADDR")` on row 1, and `err == nil` on rows 3 and 4 so a blanket refusal cannot pass. Row 2 captures the logger into a bytes.Buffer and asserts the dev path still starts and still says so.

TEST 2 — TestAMalformedSMTPAddressIsFatalAndNeverTheLogSender

  a) cfg{SMTPAddr: "not-a-host-port", SMTPUser: "u"} -> newSender returns a non-nil error whose text contains "GOEN_SMTP_ADDR"; assert the returned Sender is nil, and specifically that it is NOT an email.LogSender (a type assertion, so replacing the error with the old fallback fails here rather than only at the err!=nil line).
  b) cfg{SMTPAddr: "smtp.example:587", SMTPUser: "u"} -> err nil, sender is email.SMTPSender with TLSName "smtp.example".
  c) cfg{SMTPAddr: "", ...} -> err nil, sender is email.LogSender with ShowBody false. This pins the dev fallback so the fix cannot be over-applied into breaking local development.

MUTATION PROOF — each must be SEEN red, and the mutation must be seen to APPLY (CLAUDE.md false-green mode #3: verify with `git diff` that the edit landed, never a grep count):

  M1. Delete the whole new `if cfg.SMTPAddr == ""` block from checkProductionPosture.
      Expect: TEST 1 row 1 RED — "want an error naming GOEN_SMTP_ADDR, got nil". Rows 2-4 stay green.
      Confirm the test is not passing for the wrong reason by ALSO running row 1 with TOTPKey emptied: it must then fail with the TOTP message, not the SMTP one.

  M2. Restore the block but flip the guard to `if cfg.SMTPAddr == "" && !cfg.SecureCookies`.
      Expect: TEST 1 row 1 RED and rows 3-4 still green — proves the test is bound to the production posture and not to "SMTP empty" alone.

  M3. In newSender, revert the SplitHostPort arm to `return email.LogSender{Log: log}, nil`.
      Expect: TEST 2(a) RED on the "is not email.LogSender" assertion. This is the assertion that matters: an `err != nil` check alone would also go red, but the type assertion is what states the actual rule — a configured deployment must never silently degrade to the log.

  M4. In newSender, return an error for the empty-address case too.
      Expect: TEST 2(c) RED. This locks the deliberate development fallback so a later reader cannot "harden" it into refusing every dev run.

REGRESSION GUARD, run and recorded: `make verify` twice (CLAUDE.md mistake #22 — a gate that repairs what it checks), plus `go test ./internal/email/ ./internal/outbox/` to show internal/email/sender_test.go:172 TestTheLogSenderNeverWritesTheBody is untouched — LogSender's contract does not change, only who is allowed to construct it.

NOT LOCKABLE, and recorded as such rather than dressed up: that a real production deployment loses mail cannot be asserted from a test — it needs an environment. The startup refusal is the lock, which is why the assertion is on checkProductionPosture's return and not on any observable of the outbox.
