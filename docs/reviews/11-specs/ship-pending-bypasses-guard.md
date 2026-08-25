# ship-pending-bypasses-guard

**Verdict** CONFIRMED · **Severity** medium · **Origin** external report (verified this round)

**Files** internal/admin/store.go, internal/admin/query.sql, internal/admin/handler.go, migrations/001_initial_schema.up.sql, internal/admin/integration_test.go, internal/db/rules_integration_test.go, internal/db/statedtotals_integration_test.go, internal/ui/pages/account.go, internal/ui/pages/cart.go, cmd/goen/server.go, README.md, CLAUDE.md


## Root cause

The rule "a parcel is recorded only for an order that has entered fulfilment" was implemented as a SIDE EFFECT of the status move rather than as an invariant on the shipment. `Ship` advances `picking → shipped`, and `orders_legal_transition` guards THAT move — so the rule holds only on the one path that performs the UPDATE. When multi-parcel support was added, the `if row.FulfillmentStatus == "picking"` branch made the UPDATE conditional; the guard went with it, and the comment above the branch kept asserting a protection that now covers only the case that does not need it. Two lists of "statuses that may ship" then existed — `fillShippable`'s explicit `picking/shipped/delivered` (store.go:404) and `Ship`'s implicit none — and the rendering half was the only one that had it. This is CLAUDE.md's #13/#30/#31 shape once more: two halves that disagree, with a comment standing in for the missing half, and every guard in the repository blind to it because they all ask what is ABSENT.


## Reproduction — the evidence this rests on

EXECUTED, not reasoned. I wrote a temporary integration test (`internal/admin/zzprobe_integration_test.go`, build tag `integration`, since deleted — `git status --porcelain` shows none of my files) that builds an order exactly as checkout leaves it — pending, unpaid, one line, one HELD reservation via `hold_inventory` — and then calls the real `admin.Store.Ship` against a testcontainer PostgreSQL built from `migrations/001`.

    go test -tags integration -count=1 -run TestProbe ./internal/admin/ -v

Output (verbatim):

    BEFORE: view.CanShip=false shippable=0 status="pending"
    BEFORE: owed=100000 committed=false
    Ship returned: <nil>
    AFTER: status="pending" shipments=1 shipment_lines=1 held=0 consumed=1 events=1 audits=1 outbox=1 stock=11 committed=false
    Advance(picking) after shipping: admin: refused: ERROR: order GO-260824-000001 cannot leave pending unfunded (owes 100000) (SQLSTATE 23514)
    Advance(cancelled) after the bug shipped it: OK
    AFTER CANCEL: status="cancelled" stock=11 (goods already dispatched)

Control run, same fixture, cancelled WITHOUT the bug shipping it:

    CONTROL: stock while held = 9
    CONTROL: status="cancelled" stock after cancel = 10 (released)

So: `Ship` returns nil on a pending unpaid order owing NT$1,000; it writes the shipment, the shipment line, consumes the hold (`consumed=1`, `held=0`), enqueues `order.shipped`, records the order event and the audit row — and the order is still `pending`, still not committed. Nothing raised. The control proves the stock loss is permanent: a normal cancel releases the hold (9 → 10); after the bug shipped it, cancelling leaves stock where it was (11), because the hold is `consumed` and `release_reservation` never sees it.

Code, verified line by line:
- `internal/admin/store.go:451` reads `row, err := q.OrderIDByNumber(ctx, number)` (query at `internal/admin/query.sql:207-208` returns `id, fulfillment_status`) and then does NOT gate on it. `CreateShipment` (456), `fillParcel` (463) and everything after run for any status.
- `internal/admin/store.go:467-476`:
      // Skipped for a SECOND parcel, which leaves the order where it already is.
      // Only picking may become shipped, and orders_legal_transition is what stops
      // a shipment being recorded against an order nobody has picked.
      if row.FulfillmentStatus == "picking" {
              if advErr := q.AdvanceOrder(...); ...
  The comment is FALSE for the non-picking branch: with no `orders` UPDATE, `CREATE TRIGGER orders_legal_transition BEFORE UPDATE OF fulfillment_status ON orders` (`migrations/001_initial_schema.up.sql:1632`) never fires. This is the repo's own mistake #31 shape — a comment naming a rule the line beneath it does not invoke.
- Nothing else refuses: `order_shipments` (001:1761-1779) has only carrier/tracking/delivered_at CHECKs and no status trigger; `shipment_lines_within_purchase` (001:1804) only checks quantity against what was bought; `consume_reservation_partial` (001:953-989) checks the reservation state and quantity and reads no order status; `ShippableLines` (`internal/admin/query.sql:180-199`) filters on outstanding quantity only. `INSERT INTO order_shipments` exists in exactly one query file (`internal/admin/query.sql:169`).
- The route is unguarded too: `cmd/goen/server.go:236` → `internal/admin/handler.go:225-266` parses the form and calls `Store.Ship` with no status check.

ONE PART OF THE CLAIM IS REFUTED — the lead's addition about the UI. `internal/admin/store.go:404` gates the whole of `fillShippable`:
      if status != "picking" && status != "shipped" && status != "delivered" {
              return nil
      }
so `view.CanShip = len(view.Shippable) > 0` (store.go:422) is false for a pending order — my probe printed `view.CanShip=false shippable=0`. The back office does NOT render the dispatch form on a pending order. Reaching `Ship` on a pending order therefore takes a hand-made POST from an authenticated, step-up-verified staff session (curl/script), or any future second caller of `Store.Ship`; `http.NewCrossOriginProtection` (cmd/goen/server.go:349) rules out a cross-site POST.


## Blast radius

Who: the shop and the customer of any order dispatched before it entered fulfilment. How often: not reachable from the rendered back office today (the form is status-gated), so this is a missing invariant plus a false claim of enforcement rather than a live one-click path — it fires on a hand-made or scripted POST by a staff member, or the first day a second caller of `Store.Ship` appears (a bulk-dispatch import, a carrier callback, a queue worker).

What it costs when it fires, all measured above: goods leave the warehouse for an order that has paid nothing (`owed=100000`, no payment row) and can never be marked picked afterwards — `Advance(picking)` is refused by `orders_funded_to_leave_pending`, so the order is wedged at `pending`. The customer is emailed a dispatch notice (`outbox=1`, `order.shipped`), while their own order page reads 尚未付款 with a 前往付款 button: `AccountOrderView.AwaitingPayment()` and `OrderView.AwaitingPayment()` (`internal/ui/pages/account.go:173`, `internal/ui/pages/cart.go:531`) are `Status == "pending" && !Committed && OwedCents > 0` — all three true. From there the customer may simply CANCEL the unpaid order: `pending → cancelled` is legal, it succeeds (probe), and the stock never comes back because the hold is already `consumed` — one unit off the shelf permanently, invisible to `/admin/health` (which counts expired HELD reservations) and to the ledger, since `consume_reservation_partial` posts no movement by design. The order history now shows a cancelled order with a dispatched parcel and an audit row asserting a shipment.

Silent in every direction: no error, no ERROR log, no constraint name, no page that shows the discrepancy. It is exactly the class CLAUDE.md says the schema exists to prevent — "anything that would corrupt money, stock or history is refused by PostgreSQL, where there is no second path" — and here the corruption of stock and history is refused by nothing.


## Fix

The rule belongs in BOTH the schema and the store, and NOT in `CanShip` — `fillShippable` already gates on status correctly (store.go:404) and needs no change. It cannot go in `orders_check_transition`: there is no `orders` UPDATE to hook.

1) SCHEMA — the authority. In `migrations/001_initial_schema.up.sql` (amend in place; `002` is deliberately not cut — CLAUDE.md: "the owner's call is to keep amending, because nothing is deployed"), immediately after the `shipment_within_purchase` trigger (currently ends ~line 1830) and well before the final privilege `DO` block (mistake #19 — keep that last), add:

    -- A parcel is recorded only for an order that has ENTERED fulfilment. Ship()
    -- moves picking -> shipped and orders_legal_transition guards that move — but
    -- a second parcel skips the UPDATE entirely, so for every status except
    -- picking that trigger never fires and cannot be what refuses this. A pending
    -- unpaid order could be dispatched, its holds consumed and its customer
    -- emailed, with nothing raised.
    CREATE FUNCTION order_shipments_order_in_fulfilment() RETURNS trigger
    LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
    DECLARE
        o_status text;
    BEGIN
        -- The aggregate root is locked before it is read: without it a concurrent
        -- cancel and a concurrent dispatch each pass a test the other invalidates.
        SELECT fulfillment_status INTO o_status
        FROM orders WHERE id = NEW.order_id FOR UPDATE;
        IF o_status IS DISTINCT FROM 'picking'
           AND o_status IS DISTINCT FROM 'shipped'
           AND o_status IS DISTINCT FROM 'delivered' THEN
            RAISE EXCEPTION 'order % is % and has not entered fulfilment',
                NEW.order_id, coalesce(o_status, 'missing')
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'shipment_order_in_fulfilment';
        END IF;
        RETURN NEW;
    END;
    $$;

    CREATE TRIGGER shipment_order_in_fulfilment
        BEFORE INSERT ON order_shipments
        FOR EACH ROW EXECUTE FUNCTION order_shipments_order_in_fulfilment();

The admitted set is exactly `fillShippable`'s (`picking`, `shipped`, `delivered`) and the comment must say so, since two copies of one list is how this defect was born. `search_path` is pinned (`TestEveryStoredFunctionEndsSearchPathWithPgTemp`). Lock order is safe: `Ship` takes orders → reservation; `release_reservation` takes variant → orders; no cycle, because neither `consume_reservation_partial` nor this trigger touches `product_variants`.

2) STORE — the sentence a person can act on. In `internal/admin/store.go`, immediately after `row, err := q.OrderIDByNumber(ctx, number)` (line 451-454) and BEFORE `CreateShipment`:

    // A parcel is only recorded for an order that has entered fulfilment. The
    // same set fillShippable renders the form for; the trigger
    // shipment_order_in_fulfilment is the authority, and this is the sentence.
    switch row.FulfillmentStatus {
    case "picking", "shipped", "delivered":
    default:
        return fmt.Errorf("%w: order %s is %s and has not been picked",
            ErrRefused, number, row.FulfillmentStatus)
    }

`ErrRefused` is already mapped by `internal/admin/handler.go:260` to `?refused=1` with a WARN log; no handler change is needed.

3) THE FALSE COMMENT. Rewrite `internal/admin/store.go:467-469` so it stops naming `orders_legal_transition` as the protection: say that only `picking` advances, that a second parcel leaves the order where it is, and that what refuses an unpicked order is the status check above plus `shipment_order_in_fulfilment` — not the transition trigger, which this branch never reaches.

4) STATED TOTALS. Adding one rule trigger makes 39 → 40. Update `README.md:115` and `CLAUDE.md:1220` ("...62 unique indexes and 39 rule triggers..."), or `TestTheStatedSchemaTotalsAreTheRealOnes` fails. (`CLAUDE.md:1105`'s "~39" is approximate and may stay.)

5) FIXTURES THE TRIGGER WILL CORRECTLY BREAK. Several fixtures insert `order_shipments` against an order still at `pending` — `internal/admin/integration_test.go:584` (`returnedOrder`), `:654` (`couponedShippedOrder`), `:3449`, `:5163`, `:5879`, plus `internal/returns/integration_test.go:82` and `internal/warranty/integration_test.go:165`. Each already captures a payment, so move the order `pending → picking → shipped` before inserting the parcel. These are fixtures for a state the application cannot produce (CLAUDE.md #26/#31); do not weaken the trigger to keep them green. Run `make test-integration` and fix every failure this way.

6) Out of scope, unchanged: `orders_finished_when_shipped` still guards the other end (an order may not be `completed` while a line is short), and the documented short-ship decision — refuse rather than release — is untouched.


## The lock, and how to see it fail first

Two locks, one per half, each proven by mutation (the pre-fix RED is already on record: my probe shows `Ship returned: <nil>` with `shipments=1 consumed=1 outbox=1` on a pending unpaid order).

A) STORE LOCK — `internal/admin/integration_test.go`, `TestShippingIsRefusedForAnOrderThatWasNeverPicked`. Fixture: copy `pickingOrderHoldingStock` (line 275) minus `open_payment`/`capture_payment` and minus the move to `picking` — a pending, unpaid order holding stock, which is what checkout actually leaves. Then:
  - `err := s.Ship(ctx, number, admin.Dispatch{Carrier: "黑貓宅急便", Tracking: "T-"+number}, uuid.NullUUID{})`; require `errors.Is(err, admin.ErrRefused)`.
  - Assert NOTHING was written, which is the half that makes this a lock on the real defect rather than on the error string: `count(*) = 0` in `order_shipments` and `order_shipment_lines` for the order, the reservation still `state = 'held'` (not `consumed`), no `order_events` row with `kind = 'shipped'`, no `outbox_messages` row with `topic = 'order.shipped'` for that order number, and `stock_quantity` unchanged.
  - Keep a positive companion assertion in the same test or beside it: the SAME order, funded and moved to `picking`, ships successfully — otherwise a fixture that simply cannot ship would pass (mistake #28: an accept both versions of the rule admit proves nothing).

B) DATABASE LOCK BOUND BY NAME — add a case to `ruleCases` in `internal/db/rules_integration_test.go` keyed on the trigger name `shipment_order_in_fulfilment`, so `TestEveryRuleTriggerIsExercised` (which derives its corpus from `pg_trigger.tgname`) and `TestEveryRaisedRuleIsAssertedByName` (which wants the name in a Go string literal) both see it. `reject`: insert an `order_shipments` row for an order at `pending`. `accept`: the identical insert for the same order moved to `picking`. The assertion binds to `PgError.ConstraintName == "shipment_order_in_fulfilment"`, never to the message text (mistake #8 / #32).

MUTATIONS, each of which must be SEEN in the source after editing, not inferred from a grep count (false-green mode #3):
  1. Delete the `switch row.FulfillmentStatus` block from `Ship` and re-run (A). It must still be RED — the failure now arriving as a `PgError` from the trigger. This proves the database half holds when the store forgets, which is the whole reason the rule is not in Go alone.
  2. Restore (1); delete `CREATE TRIGGER shipment_order_in_fulfilment` from `001` and re-run (B). It must be RED (missing coverage / no refusal). Re-run (A) with BOTH the trigger and the store switch removed: it must be RED for the original reason — `Ship` returns nil and writes the shipment — which reproduces today's defect exactly.
  3. Restore both; (A) and (B) green, and `make test-integration` green after the fixtures in fix step 5 are corrected.

Not lockable and not to be dressed up: the trigger's `FOR UPDATE` on the order row cannot be shown failing by two goroutines and a start channel (#9); if it is asserted at all, hold T1's transaction open across T2's insert, or record the concurrency claim as unproven rather than covered by a test that cannot fail.
