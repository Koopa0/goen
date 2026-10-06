-- A payout source commits before this customer-visible append. Keying the row
-- on the return makes a retry safe after a transient database/context failure.
-- The WHERE is a second authority: no caller can announce money which neither
-- the provider ledger nor the store-credit ledger says has moved.
-- name: RecordReturnRefundedEvent :exec
INSERT INTO order_events (
    order_id, kind, note, actor_user_id, return_request_id
)
SELECT r.order_id, 'refunded', (
           SELECT rf.provider_ref
           FROM refunds rf
           WHERE rf.return_request_id = r.id AND rf.status = 'succeeded'
           ORDER BY rf.attempt_no DESC
           LIMIT 1
       ), @actor_user_id, r.id
FROM return_requests r
WHERE r.id = @return_request_id
  AND r.status IN ('approved', 'completed')
  AND (
      EXISTS (
          SELECT 1 FROM refunds rf
          WHERE rf.return_request_id = r.id AND rf.status = 'succeeded'
      )
      OR EXISTS (
          SELECT 1 FROM store_credit_entries e
          WHERE e.idempotency_key = 'return-credit:' || r.id::text
            AND e.amount_cents > 0
      )
  )
ON CONFLICT (return_request_id) WHERE return_request_id IS NOT NULL DO NOTHING;

-- One row per return, carrying its frozen source allocation and exact durable
-- settlement. The queue asks for the whole visible set in one call; an approved
-- retry asks for its one id through the same projection. Provider attempt state
-- is deliberately absent: pending work reuses its key and a known terminal
-- generation appends a successor, so neither makes the recovery button unsafe.
-- name: ReturnPayoutFacts :many
WITH selected AS (
    SELECT r.id, r.order_id, o.user_id, r.status,
           return_refundable_amount(r.id)::bigint AS refundable_cents,
           coalesce(r.card_refund_cents, 0)::bigint AS card_refund_cents,
           coalesce(r.credit_refund_cents, 0)::bigint AS credit_refund_cents
    FROM return_requests r
    JOIN orders o ON o.id = r.order_id
    WHERE r.id = ANY(@request_ids::uuid[])
)
SELECT s.id AS return_request_id,
       s.refundable_cents,
       s.card_refund_cents,
       s.credit_refund_cents,
       (s.user_id IS NOT NULL)::boolean AS has_account,
       coalesce((
           SELECT sum(rf.amount_cents)
           FROM refunds rf
           WHERE rf.return_request_id = s.id AND rf.status = 'succeeded'
       ), 0)::bigint AS card_paid_cents,
       coalesce((
           SELECT sum(sc.amount_cents)
           FROM store_credit_entries sc
           WHERE sc.idempotency_key = 'return-credit:' || s.id::text
       ), 0)::bigint AS credit_paid_cents,
       EXISTS (
           SELECT 1 FROM order_events e
           WHERE e.return_request_id = s.id AND e.kind = 'refunded'
       )::boolean AS refund_event_recorded,
       CASE
           -- Erasure detaches the order owner, but deliberately retains the
           -- order's award lot and loyalty account. A money-settled return may
           -- therefore still owe its idempotent clawback after user deletion.
           WHEN s.refundable_cents <= 0 THEN false
           ELSE (
			   return_loyalty_points_allocation(s.id) > 0
               AND EXISTS (
                   SELECT 1 FROM loyalty_entries e
                   WHERE e.order_id = s.order_id AND e.kind = 'award'
               )
               AND NOT EXISTS (
                   SELECT 1 FROM loyalty_entries e
                   WHERE e.return_request_id = s.id AND e.kind = 'clawback'
               )
           )
       END::boolean AS points_outstanding
FROM selected s
ORDER BY s.id;

-- One auto-committed statement: the door opens and approves the full return,
-- or returns the one it opened before.
-- name: OpenRefundBeforeShipment :one
SELECT open_refund_before_shipment(
    @order_number::text, @reason::text, @actor_user_id::uuid, @request_id::text
)::uuid AS return_request_id;

-- What the order page and the refund confirmation show. Once the door has run
-- the split is the frozen one; before, card_cents is what
-- return_requests_recount freezes for a full return of an order nothing else
-- has refunded: the capture first, credit for the rest.
-- name: BeforeShipmentRefund :one
WITH target AS (
    SELECT o.id, o.fulfillment_status,
           order_is_committed(o.id) AS committed,
           (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                      WHERE ol.order_id = o.id), 0)
            - o.discount_cents + o.shipping_cents + o.tax_cents)::bigint AS total_cents,
           coalesce((SELECT sum(p.captured_amount_cents) FROM payments p
                     WHERE p.order_id = o.id AND p.status = 'succeeded'), 0)::bigint
               AS card_capacity_cents,
           coalesce(order_amount_after_credit(o.id) = 0
                    AND EXISTS (SELECT 1 FROM store_credit_entries s
                                WHERE s.order_id = o.id AND s.amount_cents < 0),
                    false)::boolean AS paid_by_credit
    FROM orders o WHERE o.order_number = @order_number::text
)
SELECT t.id AS order_id, t.fulfillment_status, t.committed, t.paid_by_credit, t.total_cents,
       EXISTS (SELECT 1 FROM order_shipments s WHERE s.order_id = t.id)::boolean AS shipped,
       EXISTS (SELECT 1 FROM return_requests r WHERE r.order_id = t.id)::boolean AS has_return,
       b.id AS return_request_id,
       coalesce(b.status, '')::text AS return_status,
       coalesce(b.card_refund_cents,
                least(t.total_cents, t.card_capacity_cents))::bigint AS card_cents,
       coalesce(b.credit_refund_cents,
                t.total_cents - least(t.total_cents, t.card_capacity_cents))::bigint AS credit_cents
FROM target t
LEFT JOIN return_requests b ON b.order_id = t.id AND b.before_shipment;

-- A pending order store credit alone paid, cancelled by staff as its customer
-- could cancel it: the predicate is BeforeShipmentRefund's paid_by_credit on an
-- uncommitted pending order with no return, so a replay, or an order picked or
-- paid since the confirmation, cancels nothing. Run it after
-- LockOrderByNumber: a capture holds the order lock without updating the row,
-- so an UPDATE that waited for it would judge payment by an older snapshot.
-- name: CancelCreditPaidOrder :execrows
UPDATE orders o SET fulfillment_status = 'cancelled', cancelled_at = now()
WHERE o.order_number = @order_number::text
  AND o.fulfillment_status = 'pending'
  AND NOT order_is_committed(o.id)
  AND order_amount_after_credit(o.id) = 0
  AND EXISTS (SELECT 1 FROM store_credit_entries s
              WHERE s.order_id = o.id AND s.amount_cents < 0)
  AND NOT EXISTS (SELECT 1 FROM return_requests r WHERE r.order_id = o.id);

-- Nothing of a refund before shipment went out, so every line is closed as
-- received and restocked nothing; return_requests_completed_is_inspected then
-- admits the completion.
-- name: CloseUnshippedReturnLines :execrows
UPDATE return_request_lines
SET received_quantity = 0, restocked_quantity = 0
WHERE return_request_id = @return_request_id AND received_quantity IS NULL;

-- The database derives the payment, request key, amount and reason from the
-- approved return. It also records this request's actor before any provider
-- operation begins, so a retry by another staff member remains attributable.
-- name: ClaimReturnRefundExecution :one
SELECT claim_return_refund_execution(
    @return_request_id::uuid, @actor_user_id::uuid, @request_id::text
);

-- A separate statement deliberately reads after the claim committed. A SELECT
-- invoking a mutating function keeps its outer snapshot and cannot see the row
-- that function just inserted.
-- name: RefundExecution :one
SELECT r.id AS refund_id, r.request_key,
       p.provider_ref AS payment_provider_ref,
       r.amount_cents, r.status
FROM refunds r
JOIN payments p ON p.id = r.payment_id
WHERE r.id = @refund_id
  AND r.status IN ('pending', 'requires_action');

-- Each provider state has its own door. A caller cannot pair a status with the
-- wrong identity/timestamp shape through one stringly settle function.
-- name: RecordRefundPending :one
SELECT record_refund_pending(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- name: RecordRefundRequiresAction :one
SELECT record_refund_requires_action(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- name: RecordRefundSucceeded :one
SELECT record_refund_succeeded(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- name: RecordRefundFailed :one
SELECT record_refund_failed(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- name: RecordRefundCancelled :one
SELECT record_refund_cancelled(
    @refund_id::uuid, @provider_ref::text, @actor_user_id::uuid, @request_id::text
);

-- Only a rejection specifically returned by Stripe's CREATE endpoint takes the
-- no-provider-object door. Lookup, transport and decode errors stay pending.
-- name: RecordRefundAPIRejection :one
SELECT record_refund_api_rejection(
    @refund_id::uuid, @actor_user_id::uuid, @request_id::text
);

-- A NEW POSITIVE entry and not a reversal of the spend, which the schema
-- prescribes for an order that has shipped: a reversal un-funds the order, and
-- this one was paid for and went out. Idempotent on the return.
-- name: CompensateReturnWithCredit :one
SELECT compensate_return_with_credit(
    @return_id::uuid, @amount_cents::bigint, sqlc.narg(actor)::uuid
)::uuid AS entry_id;

-- name: ReverseReturnPoints :one
-- The return is the sole capability. The database derives its order, durable
-- refund amount and award proportion after verifying that the payout landed.
SELECT reverse_return_points(@return_id::uuid)::bigint AS points_reversed;

-- What has actually gone back to the customer on this order, so an allowance
-- form can default to it. A staff member typing a refund figure from memory is
-- how the wrong number reaches the 財政部.
-- What the 折讓 form offers, which must be what an allowance is allowed to
-- relieve: both sources, from the one view. Card-only defaulted the form to the
-- card half of a split refund, so the 統一發票 kept recording a reversed sale.
-- name: SettledRefundsForOrder :one
SELECT (card_cents + credit_cents)::bigint AS refunded_cents
FROM order_refunds
WHERE order_number = @order_number::text;

-- The staff member who opened a refund before shipment: every invoice claim it
-- makes is theirs, whoever presses Resume.
-- name: RefundOpenedBy :one
SELECT requested_by_user_id FROM return_requests WHERE id = $1 AND before_shipment;
