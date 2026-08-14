-- Open a payment against an order, for a Stripe Checkout Session that has just
-- been created. SECURITY DEFINER, because store cannot write `payments`
-- directly — a born-succeeded payment row is the forgery the revoke prevents.
--
-- It is idempotent on (order_id, provider_ref): a customer who reloads the
-- payment page gets the row that already exists rather than a second one.
-- name: OpenPayment :one
SELECT open_payment(@order_id, @provider_ref::text, @intended_amount_cents::bigint);

-- Record that Stripe captured money. Called ONLY from the verified webhook —
-- never from the browser's return to success_url, which anybody can request.
-- name: CapturePayment :one
-- nullif, because "unknown" is NULL and not the empty string.
-- checkout.session.completed does not expand payment_intent.latest_charge, so
-- the common event carries no card at all. Passing '' sends a value through
-- payments_last4_format, which requires four digits — every real capture is then
-- refused by a CHECK once the money has already been taken.
SELECT capture_payment(@provider_ref::text, @captured_amount_cents::bigint,
                       nullif(@card_brand::text, ''), nullif(@card_last4::text, ''));

-- name: CancelPayment :exec
SELECT cancel_payment(@provider_ref::text);

-- Record a provider webhook. The primary key is (provider, event_id), so a
-- resent event inserts zero rows rather than being processed twice — which is
-- the whole reason this table exists. :execrows is what makes the replay
-- visible to Go: 1 means "ours to process", 0 means "already seen".
-- name: RecordWebhookEvent :execrows
INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload)
VALUES ('stripe', @event_id::text, @type::text, @object_ref, @payload)
ON CONFLICT (provider, event_id) DO NOTHING;

-- name: MarkWebhookProcessed :exec
UPDATE payment_webhook_events SET processed_at = now()
WHERE provider = 'stripe' AND event_id = $1;

-- The order a Stripe session belongs to, for the webhook. The webhook is
-- trusted for WHAT happened, never for WHICH order it happened to: the order is
-- looked up through the payment row goen itself wrote at open time.
-- name: OrderByPaymentRef :one
SELECT o.id, o.order_number, o.fulfillment_status, p.intended_amount_cents
FROM orders o JOIN payments p ON p.order_id = o.id
WHERE p.provider_ref = $1;

-- What an order is owed, recomputed from its own lines. The browser never
-- carries an amount: a form field saying "pay NT$1" is the oldest hole there
-- is, so the figure sent to Stripe is derived here.
-- name: OrderTotalByNumber :one
SELECT o.id,
       o.order_number,
       o.fulfillment_status,
       -- The cast wraps the WHOLE expression, not just the sum. Casting only
       -- the sum leaves the additions at the int width of the columns beside
       -- it, and sqlc types the result int32 — a 21,474,836 dollar ceiling
       -- that nothing in Go would warn about crossing.
       -- What is still OWED, not the gross total. The two differ by the store credit
       -- already spent on this order, and charging the gross has Stripe take money
       -- this database then refuses to record: payments_capture_matches_order demands
       -- the net, so the webhook rolls back forever and the order stays unpaid with
       -- the customer's money at Stripe.
       order_amount_owed(o.id)::bigint AS total_cents,
       coalesce(pd.email, '') AS email
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.order_number = $1;

-- When the stock behind an order goes back on the shelf: the earliest expiry
-- among the holds still live for it.
--
-- A Checkout Session's expires_at is set from this, so Stripe stops accepting
-- money at the same instant the sweeper may release the goods and sell them to
-- somebody else. An expiry of time.Now() + 30 minutes is measured from the CLICK
-- while the hold is measured from PlaceOrder, so a session sized that way
-- outlives the stock behind it by however long the customer sat on the pay page.
--
-- No row is the honest answer for an order holding nothing, and it is not the
-- same as "no deadline": the caller refuses to open a session at all. A
-- coalesced sentinel would have to be compared against and would read as a real
-- date to anyone who forgot to.
-- name: OrderHoldExpiry :one
SELECT ir.expires_at
FROM inventory_reservations ir
WHERE ir.order_id = $1 AND ir.state = 'held'
ORDER BY ir.expires_at
LIMIT 1;

-- Whether a Checkout Session is already open for this order at this figure, and
-- how many payment rows it has had.
--
-- This is the FIRST line of defence against one order being charged twice.
-- Without it every POST to the pay route creates a fresh Stripe session and a
-- fresh requires_payment row, because open_payment only dedupes on
-- (order_id, provider_ref) and each session has its own id. Two tabs are then two
-- sessions and two real charges, with payments_one_capture_per_order refusing the
-- second capture only AFTER the money is at Stripe — the webhook 500s, Stripe
-- retries forever, nothing refunds.
--
-- The amount is part of the question and not a detail. What an order owes can
-- legitimately move (store credit reversed, a coupon applied), and a session for
-- the old figure must NOT be handed back: the customer would be charged a total
-- the order no longer owes. A stale session is left to expire instead, which it
-- does with the stock hold it was created against.
--
-- 'requires_payment' rather than "not succeeded": a cancelled row is a session
-- Stripe has finished with, and sending somebody back to it is sending them to a
-- page that cannot take their money.
-- name: PaymentAttemptForOrder :one
SELECT
    coalesce((SELECT p.provider_ref FROM payments p
              WHERE p.order_id = o.id
                AND p.status = 'requires_payment'
                AND p.intended_amount_cents = @owed_cents::bigint
              ORDER BY p.created_at DESC
              LIMIT 1), '')::text AS live_session,
    -- Not a count of live sessions — a count of every attempt this order has
    -- made. It goes into the Stripe idempotency key so that a customer whose
    -- first session died is not handed the dead one back for the next 24 hours.
    (SELECT count(*) FROM payments p WHERE p.order_id = o.id)::integer AS prior_attempts
FROM orders o
WHERE o.order_number = $1;

-- The lines Stripe should show on its hosted page. Named from the order, not
-- the catalogue: an order is a record of what was agreed, and re-reading the
-- product would show a renamed or repriced item at payment time.
-- name: OrderLinesForPayment :many
SELECT product_name, variant_label, unit_price_cents, quantity
FROM order_lines WHERE order_id = $1 ORDER BY position, id;

-- Whether an order already has money against it, so the payment page can send a
-- paid order to its confirmation instead of opening a second session.
-- name: OrderIsPaid :one
SELECT EXISTS (
    SELECT 1 FROM payments WHERE order_id = $1 AND status = 'succeeded'
);

-- The 'paid' history entry, written in the same transaction as the capture so
-- an order cannot be paid without its history saying when.
-- name: RecordPaidEvent :exec
INSERT INTO order_events (order_id, kind, note) VALUES ($1, 'paid', @note);

-- Award the points a captured order earned.
--
-- Called from inside the webhook's transaction, so the points and the payment
-- commit together. Idempotent on the order — a webhook Stripe delivered twice
-- awards once — which is why the amount is recomputed here rather than passed:
-- a caller supplying it could supply a different one on the retry.
-- name: AwardOrderPoints :one
SELECT award_loyalty_points(
    o.id,
    -- One point per NT$100 of what the order came to, times what the
    -- customer's tier earns. Integer division, so the fraction is dropped
    -- rather than rounded — a NT$50 order earns nothing, and rounding up would
    -- pay out on the smallest possible purchase.
    --
    -- The multiplier is read HERE, inside the capture's transaction, from the
    -- spend the customer had BEFORE this order counted: the tier is derived
    -- from committed orders, and this one is being committed by the very
    -- statement that would read it. Awarding the new tier's rate on the order
    -- that earned the tier is a benefit nobody promised.
    ((coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                WHERE ol.order_id = o.id), 0)
      - o.discount_cents + o.shipping_cents + o.tax_cents) / 10000
     * coalesce((SELECT t.points_multiplier_bp FROM membership_tiers t
                 WHERE t.id = member_tier(o.user_id, @window_days::integer, o.id)), 10000)
     / 10000)::bigint,
    (current_date + @validity_days::integer)
)
FROM orders o WHERE o.id = @order_id;

-- Who to tell that an order was paid, and what they paid.
--
-- Read inside the capture's own transaction and carried in the message, not
-- looked up at delivery: erase_user blanks order_private_data, so a message
-- delivered after an erasure would have nowhere to go. A receipt is a snapshot
-- of what was true when the money moved.
-- name: OrderRecipient :one
SELECT coalesce(pd.email, '') AS email,
       coalesce(pd.recipient_name, '') AS recipient_name,
       o.locale
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.id = $1;

-- name: EnqueueMessage :exec is defined in internal/cart/query.sql. sqlc builds
-- ONE db package for the module, so it is written once and called from here.
