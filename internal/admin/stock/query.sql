-- name: AdminVariants :many
SELECT json_build_object('Number', (pv.stock_quantity - pv.safety_stock), 'Name', p.name, 'Position', pv.position, 'ID', pv.id)::text AS page_cursor,
    pv.id,
    pv.sku,
    pv.price_cents,
    pv.compare_at_price_cents,
    pv.stock_quantity,
    pv.safety_stock,
    pv.is_active,
    p.slug,
    p.name AS product_name,
    p.status AS product_status,
    b.name AS brand,
    ARRAY(SELECT localized_name(v.value, v.value_en, @locale::text)
          FROM variant_option_values vov
          JOIN product_options o ON o.id = vov.option_id
          JOIN product_option_values v ON v.id = vov.option_value_id
          WHERE vov.variant_id = pv.id
          ORDER BY o.position, o.id)::text[] AS option_values
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
JOIN brands b ON b.id = p.brand_id
WHERE (@low_only::boolean = false OR pv.stock_quantity <= pv.safety_stock)
AND (@escaped_term::text = ''
       OR pv.sku ILIKE '%' || @escaped_term::text || '%'
       OR p.name ILIKE '%' || @escaped_term::text || '%'
       OR p.name_en ILIKE '%' || @escaped_term::text || '%')
AND (NOT @has_cursor::boolean OR ((pv.stock_quantity - pv.safety_stock) > @after_number::integer)
       OR ((pv.stock_quantity - pv.safety_stock) = @after_number::integer AND p.name > @after_name::text)
       OR ((pv.stock_quantity - pv.safety_stock) = @after_number::integer AND p.name = @after_name::text AND pv.position > @after_position::integer)
       OR ((pv.stock_quantity - pv.safety_stock) = @after_number::integer AND p.name = @after_name::text AND pv.position = @after_position::integer AND pv.id > @after_id::uuid))
ORDER BY (pv.stock_quantity - pv.safety_stock) ASC, p.name ASC, pv.position ASC, pv.id ASC
LIMIT @row_limit::integer;

-- price_cents is read for the audit trail's "before": a reprice recorded without
-- the price it replaced records the least interesting half of the fact.
-- name: AdminVariantBySKU :one
SELECT pv.id, pv.sku, pv.stock_quantity, pv.safety_stock, pv.is_active,
       pv.price_cents, p.name AS product_name, p.slug
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.sku = $1;

-- record_inventory_movement is the ONLY door: admin has no UPDATE on
-- stock_quantity, so a direct write is refused by the database.
-- name: AdjustStock :exec
SELECT record_inventory_movement(
    @variant_id, @delta::integer, 'adjustment',
    @idempotency_key::text, 'admin', NULL, @actor_user_id::uuid
);

-- Whether this exact movement is already in the ledger under the key, so a
-- replay of the form that booked it can be told from a different movement that
-- reuses the key.
-- name: StockMovementApplied :one
SELECT EXISTS (
    SELECT 1 FROM inventory_movements
    WHERE idempotency_key = @idempotency_key::text
      AND variant_id = @variant_id
      AND delta = @delta::integer
      AND reason = @reason::text
);

-- name: SetVariantActive :exec
UPDATE product_variants SET is_active = $2 WHERE id = $1;

-- name: SetVariantPrice :exec
UPDATE product_variants
SET price_cents = @price_cents, compare_at_price_cents = @compare_at_price_cents
WHERE id = @id;

-- Each source discriminator selects its validated parent. A hold names its
-- order; a release names the reservation and a restock names the return.
-- name: VariantMovements :many
SELECT json_build_object('ID', m.id)::text AS page_cursor, m.created_at, m.delta, m.reason, m.source_type,
       coalesce(o.order_number, ro.order_number, rro.order_number, '') AS order_number,
       coalesce(u.full_name, u.email, '') AS actor,
       (SELECT sum(e.delta) FROM inventory_movements e
        WHERE e.variant_id = m.variant_id AND e.id <= m.id)::integer AS running_total
FROM inventory_movements m
JOIN product_variants pv ON pv.id = m.variant_id
LEFT JOIN users u ON u.id = m.actor_user_id
LEFT JOIN orders o ON m.source_type = 'order' AND o.id = m.source_id
LEFT JOIN inventory_reservations r
       ON m.source_type = 'reservation' AND r.id = m.source_id
LEFT JOIN orders ro ON ro.id = r.order_id
LEFT JOIN return_requests rr
       ON m.source_type = 'return_request' AND rr.id = m.source_id
LEFT JOIN orders rro ON rro.id = rr.order_id
WHERE pv.sku = @sku::text
AND (NOT @has_cursor::boolean OR (m.id < @after_id::uuid))
ORDER BY m.id DESC
LIMIT @row_limit::integer;

-- reason 'receipt' and not 'adjustment', which is the whole of it: goods a shop
-- bought must be distinguishable in its own ledger from a corrected miscount.
-- There is no source_id, because goen has no purchasing table to point at.
-- name: ReceiveStock :exec
SELECT record_inventory_movement(
    @variant_id, @delta::integer, 'receipt',
    @idempotency_key::text, 'admin', NULL, @actor_user_id::uuid
);

-- The claim and the enqueue are ONE transaction, so notified_at means "the
-- outbox has this": claiming without enqueuing tells nobody and never retries.
-- The threshold is the one the listing calls in stock, stock > safety_stock.
-- name: ClaimRestockNotices :many
UPDATE stock_notifications sn SET notified_at = now()
WHERE sn.variant_id = $1
  AND sn.notified_at IS NULL
  AND EXISTS (SELECT 1 FROM product_variants pv
              WHERE pv.id = sn.variant_id
                AND pv.is_active
                AND pv.stock_quantity > pv.safety_stock)
RETURNING sn.id, sn.email, sn.locale;

-- Called once per distinct LOCALE in the claimed set, not once per recipient:
-- the product name has to follow the reader as the letter's words do.
-- name: RestockSubject :one
SELECT p.slug,
       localized_name(p.name, p.name_en, @locale::text) AS product_name,
       pv.sku
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.id = @variant_id;
