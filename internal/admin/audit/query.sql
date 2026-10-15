-- name: RecordAuditEvent :one
SELECT record_audit_event(@actor, @action::text, @entity_table::text,
                          sqlc.narg('entity_id')::uuid,
                          @before, @after, sqlc.narg('request_id')::text);

-- A person is named by their CURRENT email, never by full_name: anyone may
-- change their own name on /account/profile, so a name on an audit row would let
-- an actor pass their rows to a colleague. Staff cannot change their email
-- (ChangeEmail refuses them), and it is unique. The row stores only ids; the
-- address is read here at render time, and an erased account falls back to the
-- recorded id.
-- name: AuditEvents :many
SELECT json_build_object('At', a.occurred_at, 'ID', a.id)::text AS page_cursor, a.action, a.entity_table, a.entity_id, a.before, a.after,
       a.request_id, a.occurred_at,
       coalesce(u.email, a.actor_id_snapshot::text, '') AS actor,
       (a.actor_kind = 'system')::boolean AS by_system,
       -- What a person calls the record: the order number (for a return, refund or
       -- payment, its order's), the SKU, or the slug. Empty where no name exists
       -- or the record is gone; the row still renders.
       coalesce(CASE a.entity_table
           WHEN 'orders' THEN (SELECT o.order_number FROM orders o WHERE o.id = a.entity_id)
           WHEN 'return_requests' THEN (SELECT o.order_number FROM return_requests r
                                        JOIN orders o ON o.id = r.order_id WHERE r.id = a.entity_id)
           WHEN 'payments' THEN (SELECT o.order_number FROM payments p
                                 JOIN orders o ON o.id = p.order_id WHERE p.id = a.entity_id)
           WHEN 'refunds' THEN (SELECT o.order_number FROM refunds rf
                                JOIN payments p ON p.id = rf.payment_id
                                JOIN orders o ON o.id = p.order_id WHERE rf.id = a.entity_id)
           WHEN 'products' THEN (SELECT pr.slug FROM products pr WHERE pr.id = a.entity_id)
           WHEN 'product_variants' THEN (SELECT pv.sku FROM product_variants pv WHERE pv.id = a.entity_id)
           -- A staff change names the person it acted on by their CURRENT address; the
           -- row holds only the id, so an erased account leaves it blank. A customer
           -- view has its own column below.
           WHEN 'users' THEN CASE WHEN a.action <> 'customer.view' THEN
               (SELECT tu.email FROM users tu WHERE tu.id = a.entity_id)
           END
       END, '')::text AS subject,
       coalesce(CASE a.entity_table
           WHEN 'products' THEN (SELECT pr.slug FROM products pr WHERE pr.id = a.entity_id)
           WHEN 'product_variants' THEN (SELECT pr.slug FROM product_variants pv
                                         JOIN products pr ON pr.id = pv.product_id WHERE pv.id = a.entity_id)
       END, '')::text AS product_slug,
       coalesce(CASE WHEN a.action = 'customer.view' THEN
           (SELECT cu.email FROM users cu WHERE cu.id = a.entity_id)
       END, '')::text AS customer_email
FROM audit_events a
LEFT JOIN users u ON u.id = a.actor_user_id
WHERE (NOT @has_cursor::boolean OR (a.occurred_at < @after_at::timestamptz)
       OR (a.occurred_at = @after_at::timestamptz AND a.id < @after_id::uuid))
ORDER BY a.occurred_at DESC, a.id DESC
LIMIT @row_limit::integer;
