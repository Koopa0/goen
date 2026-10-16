-- Show only the latest generation of a return refund. A failed predecessor is
-- evidence, not current work; once its successor succeeds it must not keep the
-- health page red forever. Non-return refunds have no generation lineage.
-- name: OpenRefundCount :one
SELECT count(*)::bigint
FROM refunds r
WHERE r.status IN ('pending', 'requires_action', 'failed', 'cancelled')
  AND (
      r.return_request_id IS NULL
      OR NOT EXISTS (
          SELECT 1 FROM refunds newer
          WHERE newer.return_request_id = r.return_request_id
            AND newer.attempt_no > r.attempt_no
      )
  );

-- This is a bounded diagnostic sample. OpenRefundCount, not the length of this
-- sample, is the health figure rendered above it.
-- name: OpenRefunds :many
SELECT r.request_key, r.status, r.amount_cents, r.created_at,
       coalesce(r.provider_ref, '')::text AS provider_ref,
       o.order_number
FROM refunds r
JOIN payments p ON p.id = r.payment_id
JOIN orders o ON o.id = p.order_id
WHERE r.status IN ('pending', 'requires_action', 'failed', 'cancelled')
  AND (
      r.return_request_id IS NULL
      OR NOT EXISTS (
          SELECT 1 FROM refunds newer
          WHERE newer.return_request_id = r.return_request_id
            AND newer.attempt_no > r.attempt_no
      )
  )
ORDER BY r.created_at
LIMIT $1;

-- Overdue is measured from available_at — when a message became DUE — because
-- the claim lease and the backoff push it forward. copurchase_ever_built is
-- separate from the age because max() over an empty table is NULL, which sqlc
-- infers as non-nullable and pgx then refuses to scan: a fresh deployment only.
-- Both read copurchase_refreshes and not product_copurchases: a rebuild that
-- found no pair of products leaves the projection empty and is still a rebuild.
-- name: WorkerHealth :one
SELECT
    (SELECT count(*) FROM outbox_messages
     WHERE delivered_at IS NULL)::bigint AS outbox_pending,
    (SELECT greatest(coalesce(extract(epoch FROM now() - min(available_at)), 0), 0)
     FROM outbox_messages WHERE delivered_at IS NULL)::bigint AS outbox_oldest_seconds,
    (SELECT count(*) FROM outbox_messages
     WHERE delivered_at IS NULL AND attempts >= @max_attempts::integer)::bigint AS outbox_stuck,
    -- The sweeper's own predicate, not merely expired: release_reservation
    -- refuses a committed or fully-funded order's hold, so counting every
    -- expired row reports stock the sweeper is designed never to release, on a
    -- page whose caption says a backlog means goods nobody can buy. It can only
    -- grow, which is alarm fatigue on the page built to make failure visible.
    (SELECT count(*) FROM inventory_reservations ir
     JOIN orders o ON o.id = ir.order_id
     WHERE ir.state = 'held' AND ir.expires_at < now()
       AND NOT order_is_committed(ir.order_id)
       AND (o.fulfillment_status = 'cancelled'
            OR order_amount_after_credit(ir.order_id) <> 0)
       -- Match ExpiredReservations: reconciliation deliberately pins stock
       -- while provider money may exist, so it is not a sweeper backlog.
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
       )))::bigint AS expired_holds,
    -- LapsedCheckoutSessions' predicate, aged @lapsed_after rather than its
    -- grace: a session the payment sweeper has asked Stripe about pass after
    -- pass and still could not close, so the order keeps its credit and coupon.
    (SELECT count(*) FROM payments p
     JOIN orders o ON o.id = p.order_id
     CROSS JOIN LATERAL (
         SELECT max(ir.expires_at) AS lapsed_at
         FROM inventory_reservations ir WHERE ir.order_id = o.id
     ) h
     WHERE p.provider = 'stripe'
       AND p.status IN ('requires_payment', 'requires_action', 'processing')
       AND o.fulfillment_status = 'pending'
       AND NOT order_is_committed(o.id)
       AND h.lapsed_at < now() - @lapsed_after::interval
       AND NOT EXISTS (
           SELECT 1
           FROM payment_webhook_events e
           JOIN payments ep
             ON ep.provider = e.provider AND ep.provider_ref = e.object_ref
           WHERE ep.order_id = o.id
             AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
       ))::bigint AS lapsed_checkouts,
    (SELECT coalesce(extract(epoch FROM now() - max(refreshed_at)), 0)
     FROM copurchase_refreshes)::bigint AS copurchase_age_seconds,
    EXISTS (SELECT 1 FROM copurchase_refreshes) AS copurchase_ever_built,
    (SELECT count(*) FROM sessions WHERE expires_at <= now())::bigint AS expired_sessions,
    (SELECT count(*) FROM media_objects m
     WHERE NOT EXISTS (SELECT 1 FROM product_images p WHERE p.media_digest = m.digest)
       AND NOT EXISTS (SELECT 1 FROM hero_slides h WHERE h.media_digest = m.digest)
       AND NOT EXISTS (SELECT 1 FROM sale_campaigns c WHERE c.media_digest = m.digest)
       AND NOT EXISTS (SELECT 1 FROM categories k WHERE k.media_digest = m.digest)
       AND m.created_at < now() - interval '24 hours')::bigint AS unreferenced_media;

-- Events accepted and NOT acted on: a known Stripe object this binary could
-- not read, paid money with no local payment row, paid money for an order
-- already cancelled, a completed checkout whose money is still in flight,
-- or a refund goen recorded as succeeded that Stripe later reported failed.
-- Each is still marked processed because retrying the same event
-- changes nothing; the durable reason makes the human action countable
-- instead of leaving only a log line nobody reads.
-- name: UnreconciledPaymentCount :one
SELECT
    ((SELECT count(*) FROM payment_webhook_events
      WHERE unreconciled IS NOT NULL AND reconciled_at IS NULL)
     +
     (SELECT count(*) FROM payments p
      WHERE p.status = 'requires_reconciliation'
        AND NOT EXISTS (
            SELECT 1 FROM payment_webhook_events e
            WHERE e.provider = p.provider AND e.object_ref = p.provider_ref
              AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
        )))::bigint AS unreconciled_payments;

-- The events a person has to act on, named rather than counted: a page saying
-- "1 unreconciled" that cannot say WHICH tells an operator something is wrong
-- and nothing about what to do, which is the reason outbox.Stuck() lists.
-- A refund.failed names goen's refund by provider_ref alone, as the webhook
-- attributed it, and only a succeeded one: the page tells staff that money is
-- back in the Stripe balance, which is not so of a refund goen still has open.
-- name: UnreconciledPayments :many
SELECT e.event_id, e.type, coalesce(e.object_ref, '') AS object_ref,
       e.unreconciled::text AS reason, e.received_at,
       coalesce(o.order_number, '')::text AS refund_order_number,
       coalesce(r.amount_cents, 0)::bigint AS refund_cents
FROM payment_webhook_events e
LEFT JOIN (refunds r
           JOIN payments p ON p.id = r.payment_id
           JOIN orders o ON o.id = p.order_id)
  ON e.type = 'refund.failed' AND r.provider_ref = e.object_ref
     AND r.status = 'succeeded'
WHERE e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
ORDER BY e.received_at
LIMIT 50;

-- Provider-complete payment identities without an outstanding event alarm.
-- These cover the window before a webhook arrives. Understood-but-unpaid
-- completion is an event alarm, not this list. They are excluded when an
-- event alarm already names the same work, so health shows one resolution
-- door rather than two competing ones.
-- name: UnreconciledCompletePayments :many
SELECT o.order_number, p.provider_ref, p.created_at,
       coalesce(NOT EXISTS (
           SELECT 1 FROM inventory_reservations ir
           WHERE ir.order_id = p.order_id AND ir.state = 'released'
       ), false)::boolean AS paid_attribution_allowed
FROM payments p
JOIN orders o ON o.id = p.order_id
WHERE p.status = 'requires_reconciliation'
  AND NOT EXISTS (
      SELECT 1 FROM payment_webhook_events e
      WHERE e.provider = p.provider AND e.object_ref = p.provider_ref
        AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
  )
ORDER BY p.created_at
LIMIT 50;

-- Durable e-invoice operations which either explicitly alarmed or have remained
-- pending beyond several worker polls. Succeeded evidence and a staff claim's
-- rejection, which that person saw, are not an active health alarm. A system
-- issue's rejection was seen by nobody, so it stays while the order still owes
-- an invoice and no later issue exists. With no 加值中心 configured an operation
-- never sent is waiting for one, not stranded; one already sent stays.
-- name: StrandedInvoiceClaims :many
SELECT op.id AS operation_id, o.order_number, op.kind, op.status,
       op.amount_cents, op.reconcile_attempts, op.send_attempts,
       coalesce(op.last_error, '')::text AS last_error, op.created_at,
       (op.kind = 'allowance'
        AND op.send_attempts > op.resend_authorizations
        AND ((op.status = 'pending' AND op.last_error = 'allowance_not_yet_visible')
             OR (op.status = 'attention' AND op.last_error = 'allowance_buyer_unconfirmed'))
        AND op.last_send_at IS NOT NULL
        AND op.last_send_at <= now() - interval '15 minutes'
        AND (op.lease_until IS NULL OR op.lease_until <= now()))::boolean
           AS can_authorize_resend,
       count(*) OVER () AS total,
       coalesce(greatest(extract(epoch FROM now() - min(op.created_at) OVER ()), 0), 0)::bigint
           AS oldest_seconds
FROM invoice_operations op
JOIN orders o ON o.id = op.order_id
WHERE op.status = 'attention'
   OR (op.status = 'pending' AND op.created_at < now() - interval '15 minutes'
       AND (@invoicing_enabled::boolean OR op.send_attempts > 0))
   OR (op.status = 'rejected' AND op.actor_kind = 'system'
       AND (order_is_committed(op.order_id)
            OR (o.fulfillment_status = 'pending' AND order_amount_after_credit(op.order_id) = 0))
       AND NOT EXISTS (SELECT 1 FROM invoice_operations later
                       WHERE later.order_id = op.order_id AND later.kind = 'issue'
                         AND later.created_at > op.created_at))
ORDER BY op.created_at
LIMIT 50;

-- Orders with money received and no invoice operation at all, read without
-- trusting the outbox message that should have claimed one: `store` can delete
-- or squat it. Money received is a committed order, or a pending one store
-- credit paid in full at checkout, whose placed_at is then when it was paid. A
-- sale with nothing to file, and one with a live invoice filed before
-- operations existed, owe no claim. The amount is the whole NT$ the issuer
-- (claim_invoice_issue) would file, by the same expression over the same bigint
-- total, so an order under NT$1 is not listed as owing a claim it would refuse.
-- Newest first, so the order that just went wrong is on top; the total says
-- how many more there are.
-- name: UninvoicedOrders :many
SELECT o.order_number, f.funded_at, f.amount_cents, count(*) OVER () AS total,
       coalesce(greatest(extract(epoch FROM now() - min(f.funded_at) OVER ()), 0), 0)::bigint
           AS oldest_seconds
FROM orders o
JOIN order_totals t ON t.order_id = o.id
CROSS JOIN LATERAL (
    SELECT coalesce(
               (SELECT min(e.occurred_at) FROM order_events e
                WHERE e.order_id = o.id AND e.kind = 'paid'),
               (SELECT max(p.paid_at) FROM payments p
                WHERE p.order_id = o.id AND p.status = 'succeeded'),
               o.placed_at)::timestamptz AS funded_at,
           (t.total_cents / 100 * 100)::bigint AS amount_cents
) f
WHERE (order_is_committed(o.id)
       OR (o.fulfillment_status = 'pending' AND order_amount_after_credit(o.id) = 0))
  AND f.amount_cents > 0
  AND f.funded_at < now() - @older_than::interval
  AND NOT EXISTS (SELECT 1 FROM invoice_operations op
                  WHERE op.order_id = o.id AND op.kind = 'issue')
  AND NOT EXISTS (SELECT 1 FROM invoice_documents d
                  WHERE d.order_id = o.id AND d.kind = 'invoice' AND d.status <> 'voided')
ORDER BY f.funded_at DESC, o.id DESC
LIMIT 50;

-- Issued invoices of cancelled orders that nothing relieved and nothing is
-- correcting: ECPay's void window had passed, the void was refused, or no
-- 加值中心 was configured to send one. One with an operation still active is on
-- the stranded-claims list instead.
-- name: CancelledOrderInvoices :many
SELECT o.order_number, d.number, d.amount_cents, d.issued_at, count(*) OVER () AS total,
       coalesce(greatest(extract(epoch FROM now() - min(o.cancelled_at) OVER ()), 0), 0)::bigint
           AS oldest_seconds
FROM invoice_documents d
JOIN orders o ON o.id = d.order_id
WHERE o.fulfillment_status = 'cancelled'
  AND o.cancelled_at < now() - @older_than::interval
  AND d.kind = 'invoice' AND d.status = 'issued'
  AND d.amount_cents > coalesce((
      SELECT sum(a.amount_cents) FROM invoice_documents a
      WHERE a.original_id = d.id AND a.kind = 'allowance' AND a.status = 'issued'), 0)
  AND NOT EXISTS (SELECT 1 FROM invoice_operations op
                  WHERE op.order_id = o.id AND op.status IN ('pending', 'attention'))
ORDER BY o.cancelled_at DESC, d.id DESC
LIMIT 50;

-- Live invoices of orders that gave money back which no 折讓 relieves and
-- nothing is filing: a return refunded after shipment, whose allowance staff
-- file from the order page, or a refund before shipment or of unsent units
-- whose own filing failed or found no 加值中心. The amount is
-- claim_invoice_allowance's, so the row lists what the order page's button
-- would file: whole dollars of order_refunds capped at the invoice, less every
-- allowance not voided. One with an operation still active is on the
-- stranded-claims list instead, and a cancelled order's on
-- CancelledOrderInvoices. Owed since the later of the invoice and the last
-- money that went back, so a new refund on an order whose earlier ones were
-- relieved gets the whole margin.
-- name: UnrelievedRefunds :many
SELECT o.order_number, d.number, owed.amount_cents, owed.since, count(*) OVER () AS total,
       coalesce(greatest(extract(epoch FROM now() - min(owed.since) OVER ()), 0), 0)::bigint
           AS oldest_seconds
FROM invoice_documents d
JOIN orders o ON o.id = d.order_id
JOIN order_refunds r ON r.order_id = d.order_id
CROSS JOIN LATERAL (
    SELECT (floor(least(d.amount_cents::numeric, r.card_cents::numeric + r.credit_cents::numeric)
                  / 100) * 100
            - coalesce((SELECT sum(a.amount_cents) FROM invoice_documents a
                        WHERE a.original_id = d.id AND a.status <> 'voided'), 0))::bigint
               AS amount_cents,
           greatest(d.issued_at,
                    (SELECT max(f.succeeded_at) FROM refunds f
                     JOIN payments p ON p.id = f.payment_id
                     WHERE p.order_id = d.order_id AND f.status = 'succeeded'),
                    (SELECT max(e.created_at) FROM store_credit_entries e
                     WHERE e.order_id = d.order_id AND e.amount_cents > 0))::timestamptz AS since
) owed
WHERE d.kind = 'invoice' AND d.status = 'issued'
  AND o.fulfillment_status <> 'cancelled'
  AND owed.amount_cents > 0
  AND owed.since < now() - @older_than::interval
  AND NOT EXISTS (SELECT 1 FROM invoice_operations op
                  WHERE op.order_id = d.order_id AND op.status IN ('pending', 'attention'))
ORDER BY owed.since DESC, d.id DESC
LIMIT 50;

-- A human has independently checked ECPay and confirmed the missing Allowance.
-- The database rechecks age/state/lease and records actor + request atomically.
-- name: AuthorizeInvoiceAllowanceResend :one
SELECT authorize_invoice_allowance_resend(
    @operation_id::uuid, @actor_user_id::uuid, @request_id::text
)::boolean AS authorized;

-- Staff explicitly confirmed every provider-side cent was refunded or already
-- represented by a succeeded payment. The function also terminates a linked
-- active payment, so safe release cannot leave a completed Session resumable.
-- name: ReleasePaymentEvent :one
SELECT release_payment_event(@event_id::text);

-- Staff have confirmed that this complete Session took no money, or that all
-- of it was refunded at Stripe. This is the only outcome that permits a later
-- Checkout generation; paid attribution has a separate capture path.
-- name: ReleaseCompletePayment :one
SELECT release_complete_payment(@provider_ref::text);

-- name: OrderNumbersByProviderRef :many
SELECT p.provider_ref, o.order_number
FROM payments p
JOIN orders o ON o.id = p.order_id
WHERE p.provider = 'stripe'
  AND p.provider_ref = ANY(@provider_refs::text[]);
