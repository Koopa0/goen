-- Idempotent on (order_id, provider_ref).
-- name: OpenPayment :one
SELECT open_payment(@order_id, @provider_ref::text, @intended_amount_cents::bigint);

-- name: CapturePayment :one
-- nullif, because an unknown card is NULL and not '': '' is a value that fails
-- payments_last4_format, and the usual event carries no card at all.
SELECT capture_payment(@provider_ref::text, @captured_amount_cents::bigint,
                       nullif(@card_brand::text, ''), nullif(@card_last4::text, ''));

-- name: CancelPayment :exec
SELECT cancel_payment(@provider_ref::text);

-- :execrows is what makes a replay visible to Go: 1 means "ours to process",
-- 0 means "already seen".
-- name: RecordWebhookEvent :execrows
INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload)
VALUES ('stripe', @event_id::text, @type::text, @object_ref, @payload)
ON CONFLICT (provider, event_id) DO NOTHING;

-- name: MarkWebhookProcessed :exec
UPDATE payment_webhook_events SET processed_at = now()
WHERE provider = 'stripe' AND event_id = $1;

-- Processed, and NOT acted on. Marked in the same transaction as the claim, so
-- an event that could not be applied cannot be recorded as seen without also
-- being recorded as needing a person — which is ProcessWebhook's whole rule,
-- applied to the outcome rather than to the effect.
-- name: MarkWebhookUnreconciled :exec
UPDATE payment_webhook_events SET processed_at = now(), unreconciled = @reason::text
WHERE provider = 'stripe' AND event_id = @event_id::text;

-- The webhook is trusted for what happened, never for which order.
-- name: OrderByPaymentRef :one
SELECT o.id, o.order_number, o.fulfillment_status, p.intended_amount_cents
FROM orders o JOIN payments p ON p.order_id = o.id
WHERE p.provider_ref = $1;

-- What an order still OWES, never the gross: payments_capture_matches_order
-- demands the net.
-- name: OrderTotalByNumber :one
SELECT o.id,
       o.order_number,
       o.fulfillment_status,
       -- The cast wraps the whole expression: casting only the sum leaves the
       -- additions at the columns' int width and sqlc types the result int32.
       order_amount_owed(o.id)::bigint AS total_cents,
       coalesce(pd.email, '') AS email
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.order_number = $1;

-- The earliest expiry among an order's live holds; a session's expires_at is set
-- from it, so Stripe stops taking money when the sweeper may release the goods.
-- name: OrderHoldExpiry :one
SELECT ir.expires_at
FROM inventory_reservations ir
WHERE ir.order_id = $1 AND ir.state = 'held'
ORDER BY ir.expires_at
LIMIT 1;

-- Whether a session is open for this order AT THIS FIGURE, since what an order
-- owes can move; 'requires_payment' because a cancelled row cannot take money.
-- name: PaymentAttemptForOrder :one
SELECT
    coalesce((SELECT p.provider_ref FROM payments p
              WHERE p.order_id = o.id
                AND p.status = 'requires_payment'
                AND p.intended_amount_cents = @owed_cents::bigint
              ORDER BY p.created_at DESC
              LIMIT 1), '')::text AS live_session,
    -- Every attempt, not only the live ones: it goes into the Stripe idempotency
    -- key, which Stripe honours for 24 hours.
    (SELECT count(*) FROM payments p WHERE p.order_id = o.id)::integer AS prior_attempts
FROM orders o
WHERE o.order_number = $1;

-- Named from the order rather than the catalogue: a renamed or repriced product
-- must not move at payment.
-- name: OrderLinesForPayment :many
SELECT product_name, variant_label, unit_price_cents, quantity
FROM order_lines WHERE order_id = $1 ORDER BY position, id;

-- name: OrderIsPaid :one
SELECT EXISTS (
    SELECT 1 FROM payments WHERE order_id = $1 AND status = 'succeeded'
);

-- name: RecordPaidEvent :exec
INSERT INTO order_events (order_id, kind, note) VALUES ($1, 'paid', @note);

-- Idempotent on the order, which is why the amount is recomputed here rather
-- than passed: a caller could supply a different one on the retry.
-- name: AwardOrderPoints :one
SELECT award_loyalty_points(
    o.id,
    -- One point per NT$100 times the customer's tier, integer division so a
    -- NT$50 order earns nothing. The multiplier is read from the spend the
    -- customer had BEFORE this order, which this statement is committing.
    ((coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                WHERE ol.order_id = o.id), 0)
      - o.discount_cents + o.shipping_cents + o.tax_cents) / 10000
     * coalesce((SELECT t.points_multiplier_bp FROM membership_tiers t
                 WHERE t.id = member_tier(o.user_id, @window_days::integer, o.id)), 10000)
     / 10000)::bigint,
    (current_date + @validity_days::integer)
)
FROM orders o WHERE o.id = @order_id;

-- Carried in the message rather than read at delivery: erase_user blanks
-- order_private_data, so a later delivery would have nowhere to go.
-- name: OrderRecipient :one
SELECT coalesce(pd.email, '') AS email,
       coalesce(pd.recipient_name, '') AS recipient_name,
       o.locale
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.id = $1;

-- EnqueueMessage is defined in internal/cart/query.sql; sqlc builds one db
-- package for the module.
