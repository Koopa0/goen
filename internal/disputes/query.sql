-- Open disputes for the staff queue. Overdue is computed against @now, not
-- database now(), so tests can inject a clock.
-- name: DisputeQueue :many
SELECT d.id,
       d.provider_ref,
       d.charge_ref,
       d.amount_cents,
       d.currency,
       d.status,
       d.reason,
       d.evidence_due_at,
       d.disposition,
       d.reviewed_at,
       d.created_at,
       o.order_number,
       p.provider_ref AS payment_session_ref,
       (d.evidence_due_at IS NOT NULL AND d.evidence_due_at < @now::timestamptz)::boolean AS overdue
FROM payment_disputes d
LEFT JOIN payments p ON p.id = d.payment_id
LEFT JOIN orders o ON o.id = p.order_id
WHERE d.status IN ('warning_needs_response', 'warning_under_review',
                   'needs_response', 'under_review')
   OR (d.reviewed_at IS NULL
       AND d.status IN ('won', 'lost', 'charge_refunded', 'warning_closed'))
ORDER BY overdue DESC,
         d.evidence_due_at NULLS LAST,
         d.created_at DESC
LIMIT $1;

-- name: ReviewPaymentDispute :exec
SELECT review_payment_dispute(@dispute_id::uuid, @actor::uuid, @disposition::text);
