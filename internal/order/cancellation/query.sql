-- Its own statement, before CancelLapsedOrder and CancelOrderByCustomer: open_payment and capture_payment
-- lock this row without updating it, so an UPDATE that waited for them would
-- still judge their payments by the snapshot it took before waiting.
-- name: LockOrderByNumber :one
SELECT id FROM orders WHERE order_number = $1 FOR UPDATE;

-- Whether store credit alone paid the order, read before the cancellation
-- returns the credit: checkout queued its 統一發票 then. Only an uncommitted
-- order is cancelled this way, which no card has paid, so owing nothing after a
-- credit spend means credit paid it. Read under the order lock.
-- name: PaidByCreditAlone :one
SELECT coalesce(order_amount_after_credit(o.id) = 0
                AND EXISTS (SELECT 1 FROM store_credit_entries s
                            WHERE s.order_id = o.id AND s.amount_cents < 0),
                false)::boolean AS paid_by_credit
FROM orders o
WHERE o.id = $1;

-- Ordered by variant first so cancellation shares the global stock-root lock
-- order with checkout and returns; id is the stable tie-breaker.
-- name: HeldReservationsForOrder :many
SELECT r.id FROM inventory_reservations r
JOIN orders o ON o.id = r.order_id
WHERE o.order_number = $1 AND r.state = 'held'
ORDER BY r.variant_id, r.id;

-- name: ReleaseReservation :exec
SELECT release_reservation($1);

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
