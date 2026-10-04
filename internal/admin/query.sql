-- Staff have verified at Stripe that this provider-complete Session was paid.
-- The SECURITY DEFINER function accepts no amount from the operator: it posts
-- the payment row's immutable intent through capture_payment and returns the
-- order facts needed for payment-owned side effects in this admin tx.
-- name: AttributeCompletePaymentPaid :one
WITH attributed AS MATERIALIZED (
    SELECT attribute_complete_payment_paid(@provider_ref::text) AS payment_id
)
SELECT p.order_id, o.order_number, p.intended_amount_cents AS amount_cents
FROM attributed a
JOIN payments p ON p.id = a.payment_id
JOIN orders o ON o.id = p.order_id;

