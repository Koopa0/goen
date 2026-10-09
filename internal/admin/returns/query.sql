-- sqlc.narg on the actor: actor_user_id is nullable with a foreign key, so a
-- zero UUID is not "nobody" — it is an id that does not exist, and the FK
-- refuses it.
-- name: RestockReturnedUnits :exec
SELECT record_inventory_movement(
    @variant_id, @delta::integer, 'return',
    @idempotency_key::text, 'return_request', @request_id, sqlc.narg(actor_user_id)::uuid
);

-- policy_window is the union of THIS request's returned lines against each
-- line's own shipment. A later unrelated parcel on the same order must not
-- reopen a window that line already closed. now() would move a filed request
-- into a later window the day the staff member opens it.
-- name: ReturnQueue :many
SELECT json_build_object('Rank', return_payout_outstanding(r.id), 'Priority', (r.status = 'requested'), 'At', r.created_at, 'ID', r.id)::text AS page_cursor, r.id, r.status, r.reason, r.created_at, r.decided_at, r.before_shipment,
       o.order_number,
       (SELECT coalesce(sum(rl.quantity), 0) FROM return_request_lines rl
        WHERE rl.return_request_id = r.id)::integer AS units,
       return_refundable_amount(r.id)::bigint AS refundable_cents,
       coalesce((
           SELECT CASE
               WHEN bool_and(w.win = 'within') THEN 'within'
               WHEN bool_and(w.win = 'goodwill') THEN 'goodwill'
               WHEN bool_and(w.win = 'after') THEN 'after'
               WHEN bool_and(w.win = 'undelivered') THEN 'undelivered'
               ELSE 'mixed'
           END
           FROM (
               SELECT return_line_policy_window(r.created_at, sh.delivered_at) AS win
               FROM return_request_lines rl
               LEFT JOIN LATERAL (
                   SELECT s.delivered_at
                   FROM order_shipment_lines osl
                   JOIN order_shipments s ON s.id = osl.shipment_id
                   WHERE osl.order_line_id = rl.order_line_id
                     AND s.order_id = r.order_id
                     AND s.delivered_at IS NOT NULL
                   ORDER BY s.delivered_at DESC
                   LIMIT 1
               ) sh ON true
               WHERE rl.return_request_id = r.id
           ) w
       ), 'undelivered')::text AS rescission_window
FROM return_requests r
JOIN orders o ON o.id = r.order_id
-- Recovery is the only retry door. Rank it before the intake queue and before
-- LIMIT, or fifty newer requests can make an older approved-but-unpaid customer
-- disappear from every actionable screen.
WHERE (NOT @has_request::boolean OR r.id = @request_id::uuid)
  AND (NOT @has_cursor::boolean OR (return_payout_outstanding(r.id) < @after_rank::boolean)
       OR (return_payout_outstanding(r.id) = @after_rank::boolean AND (r.status = 'requested') < @after_priority::boolean)
       OR (return_payout_outstanding(r.id) = @after_rank::boolean AND (r.status = 'requested') = @after_priority::boolean AND r.created_at < @after_at::timestamptz)
       OR (return_payout_outstanding(r.id) = @after_rank::boolean AND (r.status = 'requested') = @after_priority::boolean AND r.created_at = @after_at::timestamptz AND r.id < @after_id::uuid))
ORDER BY return_payout_outstanding(r.id) DESC, (r.status = 'requested') DESC, r.created_at DESC, r.id DESC
LIMIT @row_limit::integer;

-- The amount comes from return_refundable_amount and never from anything the
-- request carried. It is a FUNCTION rather than an expression because the queue
-- needs the same number, and two copies are two figures free to disagree.
-- policy_window is the same union as ReturnQueue, on the same two clocks: a
-- decision that classified from now() would take a day-5 right away on day 20.
-- name: ReturnForDecision :one
SELECT r.id, r.status, r.reason, r.order_id,
       o.order_number, o.fulfillment_status,
       return_refundable_amount(r.id)::bigint AS refundable_cents,
       p.id AS payment_id,
       p.provider_ref,
       p.captured_amount_cents,
       o.user_id,
       coalesce((
           SELECT CASE
               WHEN bool_and(w.win = 'within') THEN 'within'
               WHEN bool_and(w.win = 'goodwill') THEN 'goodwill'
               WHEN bool_and(w.win = 'after') THEN 'after'
               WHEN bool_and(w.win = 'undelivered') THEN 'undelivered'
               ELSE 'mixed'
           END
           FROM (
               SELECT return_line_policy_window(r.created_at, sh.delivered_at) AS win
               FROM return_request_lines rl
               LEFT JOIN LATERAL (
                   SELECT s.delivered_at
                   FROM order_shipment_lines osl
                   JOIN order_shipments s ON s.id = osl.shipment_id
                   WHERE osl.order_line_id = rl.order_line_id
                     AND s.order_id = r.order_id
                     AND s.delivered_at IS NOT NULL
                   ORDER BY s.delivered_at DESC
                   LIMIT 1
               ) sh ON true
               WHERE rl.return_request_id = r.id
           ) w
       ), 'undelivered')::text AS policy_window
FROM return_requests r
JOIN orders o ON o.id = r.order_id
LEFT JOIN payments p ON p.order_id = o.id AND p.status = 'succeeded'
WHERE r.id = $1;

-- received_quantity is NULL until somebody opens the parcel: "not looked at yet"
-- and "looked at, nothing arrived" are different facts. restockable is false for
-- a line whose variant was deleted, so the form cannot offer a refused control.
-- policy_window is THIS line's shipment, never a later parcel on the order.
-- name: ReturnLines :many
SELECT rl.return_request_id, ol.id AS order_line_id, ol.sku, ol.product_name,
       ol.variant_label, ol.unit_price_cents, rl.quantity,
       rl.received_quantity, rl.restocked_quantity,
       coalesce(rl.inspection_note, '')::text AS inspection_note,
       (ol.variant_id IS NOT NULL)::boolean AS restockable,
       r.order_id,
       r.created_at AS requested_at,
       sh.delivered_at,
       return_line_policy_window(r.created_at, sh.delivered_at) AS policy_window
FROM return_request_lines rl
JOIN return_requests r ON r.id = rl.return_request_id
JOIN order_lines ol ON ol.id = rl.order_line_id
LEFT JOIN LATERAL (
    SELECT s.delivered_at
    FROM order_shipment_lines osl
    JOIN order_shipments s ON s.id = osl.shipment_id
    WHERE osl.order_line_id = rl.order_line_id
      AND s.order_id = r.order_id
      AND s.delivered_at IS NOT NULL
    ORDER BY s.delivered_at DESC
    LIMIT 1
) sh ON true
WHERE rl.return_request_id = ANY(@request_ids::uuid[])
ORDER BY rl.return_request_id, ol.position, ol.id;

-- The live assessor and its snapshot are one fact at insert; erasure later
-- clears the live column only.
-- name: InsertEligibilityAssessment :one
INSERT INTO return_eligibility_assessments (
    order_id, return_request_id, version, assessed_by, assessed_by_snapshot, basis
) VALUES (
    @order_id, @return_request_id, @version, @assessed_by::uuid, @assessed_by::uuid, @basis
)
RETURNING id, order_id, return_request_id, version, assessed_by, assessed_by_snapshot,
          assessed_at, basis;

-- name: InsertEligibilityFact :exec
INSERT INTO return_eligibility_facts (
    assessment_id, order_id, return_request_id, order_line_id,
    unused, packaging_complete, accessories_complete,
    requested_at, delivered_at, policy_window
) VALUES (
    @assessment_id, @order_id, @return_request_id, @order_line_id,
    @unused, @packaging_complete, @accessories_complete,
    @requested_at, @delivered_at, @policy_window
);

-- name: NextEligibilityVersion :one
SELECT coalesce(max(version), 0)::int + 1 AS version
FROM return_eligibility_assessments
WHERE return_request_id = @return_request_id;

-- closeReturn already holds the order row. A concurrent Assess waits on
-- that same lock, so this read does not take FOR UPDATE: admin has INSERT
-- and SELECT only, and PostgreSQL would refuse the lock without UPDATE.
-- name: LatestEligibilityAssessment :one
SELECT id, order_id, return_request_id, version, assessed_by, assessed_by_snapshot,
       assessed_at, basis
FROM return_eligibility_assessments
WHERE return_request_id = @return_request_id
ORDER BY version DESC
LIMIT 1;

-- name: EligibilityFacts :many
SELECT assessment_id, order_id, return_request_id, order_line_id,
       unused, packaging_complete, accessories_complete,
       requested_at, delivered_at, policy_window
FROM return_eligibility_facts
WHERE assessment_id = @assessment_id;

-- name: LatestEligibilityAssessments :many
SELECT DISTINCT ON (return_request_id)
    id, order_id, return_request_id, version, assessed_by, assessed_by_snapshot,
    assessed_at, basis
FROM return_eligibility_assessments
WHERE return_request_id = ANY(@request_ids::uuid[])
ORDER BY return_request_id, version DESC;

-- name: EligibilityFactsForAssessments :many
SELECT assessment_id, order_id, return_request_id, order_line_id,
       unused, packaging_complete, accessories_complete,
       requested_at, delivered_at, policy_window
FROM return_eligibility_facts
WHERE assessment_id = ANY(@assessment_ids::uuid[]);

-- `received_quantity IS NULL` makes a line inspectable ONCE: the restock behind
-- it posts a movement keyed on (request, line), so a second inspection would be
-- swallowed by that index and show a corrected count over unmoved stock.
-- name: InspectReturnLine :execrows
UPDATE return_request_lines rl
SET received_quantity = @received::integer,
    restocked_quantity = @restocked::integer,
    inspection_note = nullif(@note::text, '')
FROM return_requests r
WHERE r.id = rl.return_request_id
  AND rl.return_request_id = @request_id
  AND rl.order_line_id = @order_line_id
  AND r.status = 'approved'
  AND rl.received_quantity IS NULL;

-- The cast on variant_id is load-bearing: the column is nullable and the WHERE
-- clause excludes the NULLs, but sqlc reads the declaration and not the
-- predicate, so without it every caller unwraps a NullUUID that cannot be null.
-- name: ReturnRestockLines :many
SELECT ol.variant_id::uuid AS variant_id,
       rl.restocked_quantity::integer AS quantity, rl.order_line_id
FROM return_request_lines rl
JOIN order_lines ol ON ol.id = rl.order_line_id
WHERE rl.return_request_id = @request_id
  AND rl.restocked_quantity > 0
  AND ol.variant_id IS NOT NULL
-- record_inventory_movement locks the variant; use the same global order as
-- checkout and reservation release, with line id only as a stable tie-breaker.
ORDER BY ol.variant_id, rl.order_line_id;

-- return_requests_completed_is_inspected refuses this while any line is
-- un-inspected. `status = 'approved'` is restated for DecideReturn's reason: it
-- is what makes two staff members closing one return resolve to one winner.
-- name: LockReturnOrder :one
SELECT o.id
FROM orders o JOIN return_requests r ON r.order_id = o.id
WHERE r.id = @id
FOR UPDATE OF o;

-- name: CompleteReturn :execrows
UPDATE return_requests
SET status = 'completed', resolution = coalesce(nullif(@resolution::text, ''), resolution)
WHERE id = @id AND status = 'approved';

-- :execrows, because `status = 'requested'` here is the ONLY place the question
-- is asked under a lock: as :exec, the loser of two simultaneous decisions
-- updates zero rows, SQL calls that success, and an audit row claims a decision
-- nobody made — after paying a refund.
-- name: DecideReturn :execrows
UPDATE return_requests
SET status = @status::text, resolution = @resolution, decided_at = now()
WHERE id = @id AND status = 'requested';
