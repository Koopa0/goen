-- Reservations whose hold has run out and whose order never got funded.
-- name: ExpiredReservations :many
SELECT ir.id
FROM inventory_reservations ir
JOIN orders o ON o.id = ir.order_id
WHERE ir.state = 'held'
  AND ir.expires_at < now()
  AND NOT order_is_committed(ir.order_id)
  -- Committed is not the whole question: a zero-owed order has no payment row and
  -- sits at 'pending' while the customer has already paid in full.
  AND (o.fulfillment_status = 'cancelled' OR order_amount_after_credit(ir.order_id) <> 0)
  -- A complete Session / verified capture awaiting a human outcome may already
  -- hold money. Keep its goods pinned until paid attribution commits the order,
  -- or an explicit refund/unpaid resolution releases the payment gate.
  AND (o.fulfillment_status = 'cancelled' OR (
      NOT EXISTS (
          SELECT 1 FROM payments p
          WHERE p.order_id = ir.order_id
            AND p.status = 'requires_reconciliation'
      )
      AND NOT EXISTS (
          SELECT 1
          FROM payment_webhook_events e
          JOIN payments p
            ON p.provider = e.provider AND p.provider_ref = e.object_ref
          WHERE p.order_id = ir.order_id
            AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
      )
  ))
ORDER BY ir.expires_at
LIMIT $1;

-- Unpaid orders none of whose holds is live any more. A Checkout Session must
-- end before the order's hold, so such an order can never be paid. The rest of
-- the predicate keeps any order that has money or may still take some: zero-owed
-- is paid in full while pending, and a live session, a provider-complete session
-- awaiting its webhook, or an unresolved provider event may already hold money.
-- CancelLapsedOrder repeats this predicate under the order lock.
-- name: LapsedUnpaidOrders :many
SELECT o.order_number
FROM orders o
WHERE o.fulfillment_status = 'pending'
  AND NOT order_is_committed(o.id)
  AND order_amount_after_credit(o.id) <> 0
  AND EXISTS (SELECT 1 FROM inventory_reservations ir WHERE ir.order_id = o.id)
  AND NOT EXISTS (
      SELECT 1 FROM inventory_reservations ir
      WHERE ir.order_id = o.id AND ir.state = 'held' AND ir.expires_at >= now()
  )
  AND NOT EXISTS (
      SELECT 1 FROM payments p
      WHERE p.order_id = o.id
        AND p.status IN ('requires_payment', 'requires_action', 'processing',
                         'requires_reconciliation')
  )
  AND NOT EXISTS (
      SELECT 1
      FROM payment_webhook_events e
      JOIN payments p
        ON p.provider = e.provider AND p.provider_ref = e.object_ref
      WHERE p.order_id = o.id
        AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
  )
ORDER BY o.placed_at
LIMIT $1;

-- LapsedUnpaidOrders' predicate, read again after LockOrderByNumber: `pending`
-- refuses a second cancellation, and the payment clauses now see every payment
-- committed before the lock was granted.
-- name: CancelLapsedOrder :execrows
UPDATE orders o SET fulfillment_status = 'cancelled', cancelled_at = now()
WHERE o.id = $1
  AND o.fulfillment_status = 'pending'
  AND NOT order_is_committed(o.id)
  AND order_amount_after_credit(o.id) <> 0
  AND EXISTS (SELECT 1 FROM inventory_reservations ir WHERE ir.order_id = o.id)
  AND NOT EXISTS (
      SELECT 1 FROM inventory_reservations ir
      WHERE ir.order_id = o.id AND ir.state = 'held' AND ir.expires_at >= now()
  )
  AND NOT EXISTS (
      SELECT 1 FROM payments p
      WHERE p.order_id = o.id
        AND p.status IN ('requires_payment', 'requires_action', 'processing',
                         'requires_reconciliation')
  )
  AND NOT EXISTS (
      SELECT 1
      FROM payment_webhook_events e
      JOIN payments p
        ON p.provider = e.provider AND p.provider_ref = e.object_ref
      WHERE p.order_id = o.id
        AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
  );
