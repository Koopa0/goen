-- Idempotent on (order_id, provider_ref).
-- name: OpenPayment :one
SELECT open_payment(@order_id, @provider_ref::text, @intended_amount_cents::bigint);

-- Serialize creation of a local payment identity with every webhook carrying
-- that same provider reference. This must run inside ProcessWebhook's tx.
-- name: LockPaymentProviderRef :exec
SELECT lock_payment_provider_ref('stripe', @provider_ref::text);

-- name: CapturePayment :one
-- nullif, because an unknown card is NULL and not '': '' is a value that fails
-- payments_last4_format, and the usual event carries no card at all.
SELECT capture_payment(@provider_ref::text, @captured_amount_cents::bigint,
                       nullif(@card_brand::text, ''), nullif(@card_last4::text, ''));

-- name: CancelPayment :exec
SELECT cancel_payment(@provider_ref::text);

-- Stripe has confirmed this rejected/uncertain session expired. Persist that
-- terminal provider fact so its idempotency generation cannot be reused.
-- name: RecordExpiredPayment :one
SELECT record_expired_payment(
    @order_id::uuid,
    @provider_ref::text,
    @intended_amount_cents::bigint
);

-- Stripe reports this unadmitted Session complete, which is terminal at the
-- provider but does not by itself say whether money moved.
-- name: RecordCompletePayment :one
SELECT record_complete_payment(
    @order_id::uuid,
    @provider_ref::text,
    @intended_amount_cents::bigint
);

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
-- name: MarkWebhookUnreconciled :one
SELECT mark_payment_event_unreconciled(@event_id::text, @reason::text);

-- The webhook is trusted for what happened, never for which order.
-- name: OrderByPaymentRef :one
SELECT o.id, o.order_number, o.fulfillment_status,
       p.intended_amount_cents, p.status
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

-- The one non-terminal session for this order. A session for an old figure is
-- still a place the customer can pay, so the caller must expire it before
-- opening a replacement rather than filtering it out.
-- name: PaymentAttemptForOrder :one
SELECT
    coalesce(active.provider_ref, '')::text AS live_session,
    coalesce(active.intended_amount_cents, 0)::bigint AS live_session_amount_cents,
    coalesce(active.intended_amount_cents = @owed_cents::bigint, false)::boolean
        AS live_session_matches_owed,
    (EXISTS (
         SELECT 1 FROM payments blocked
         WHERE blocked.order_id = o.id
           AND blocked.status = 'requires_reconciliation'
     ) OR EXISTS (
         SELECT 1
         FROM payment_webhook_events e
         JOIN payments p
           ON p.provider = e.provider AND p.provider_ref = e.object_ref
         WHERE p.order_id = o.id
           AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
     ))::boolean AS needs_reconciliation,
    -- Every attempt, not only the live ones: it goes into the Stripe idempotency
    -- key, which Stripe honours for 24 hours.
    (SELECT count(*) FROM payments p WHERE p.order_id = o.id)::integer AS prior_attempts
FROM orders o
LEFT JOIN LATERAL (
    SELECT p.provider_ref, p.intended_amount_cents
    FROM payments p
    WHERE p.order_id = o.id
      AND p.status IN ('requires_payment', 'requires_action', 'processing')
    ORDER BY p.created_at DESC, p.id DESC
    LIMIT 1
) active ON true
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
    -- One point per whole NT$100 times the customer's tier. sum(bigint) is
    -- numeric, so casting only the final expression rounded NT$50 to one point;
    -- the cast on the sum makes both divisions integer and keeps the database
    -- as the one production definition of this money rule. The multiplier is
    -- read from the spend the customer had BEFORE this order.
    ((coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)::bigint FROM order_lines ol
                WHERE ol.order_id = o.id), 0)
      - o.discount_cents + o.shipping_cents + o.tax_cents) / 10000
     * coalesce((SELECT t.points_multiplier_bp FROM membership_tiers t
                 WHERE t.id = member_tier(o.user_id, @window_days::integer, o.id)), 10000)
     / 10000)::bigint,
    (shop_today() + @validity_days::integer)
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
