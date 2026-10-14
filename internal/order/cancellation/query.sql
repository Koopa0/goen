-- The actor is the staff member who cancelled; none for the customer's own
-- cancellation and, with by_system, the sweeper's at the payment deadline. Both
-- are carried structurally because the customer's own order page renders any
-- note in whatever language it was written.
-- name: RecordCancellation :exec
INSERT INTO order_events (order_id, kind, actor_user_id, by_system)
VALUES (@order_id, 'cancelled', @actor_user_id, @by_system::boolean);

-- What a cancellation notice needs to decide whether money may have reached
-- Stripe for this unpaid order: the status of every payment it has had, and
-- whether any of their provider events ever needed a person.
-- name: OrderPaymentFacts :one
SELECT ARRAY(
           SELECT DISTINCT p.status FROM payments p WHERE p.order_id = $1 ORDER BY p.status
       )::text[] AS statuses,
       EXISTS (
           SELECT 1
           FROM payments p
           JOIN payment_webhook_events e
             ON e.provider = p.provider AND e.object_ref = p.provider_ref
           WHERE p.order_id = $1 AND e.unreconciled IS NOT NULL
       )::boolean AS provider_flagged;

-- Called in the cancellation's own transaction, beside the stock release: the
-- status change is what makes both legal.
-- name: ReverseOrderCredit :one
SELECT reverse_order_credit(@order_id)::bigint AS returned_cents;
