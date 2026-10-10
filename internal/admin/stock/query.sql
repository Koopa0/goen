-- name: AdminVariants :many
SELECT json_build_object('Number', (pv.stock_quantity - pv.safety_stock), 'Name', p.name, 'Position', pv.position, 'ID', pv.id)::text AS page_cursor,
    pv.id,
    pv.sku,
    pv.price_cents,
    pv.compare_at_price_cents,
    pv.stock_quantity,
    pv.safety_stock,
    pv.is_active,
    pv.preorder_release_on,
    p.slug,
    p.name AS product_name,
    p.status AS product_status,
    coalesce(b.name, '') AS brand,
    ARRAY(SELECT localized_name(v.value, v.value_en, @locale::text)
          FROM variant_option_values vov
          JOIN product_options o ON o.id = vov.option_id
          JOIN product_option_values v ON v.id = vov.option_value_id
          WHERE vov.variant_id = pv.id
          ORDER BY o.position, o.id)::text[] AS option_values
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
LEFT JOIN brands b ON b.id = p.brand_id
WHERE (@sold_out_only::boolean = false OR (pv.is_active AND p.status = 'active' AND pv.stock_quantity <= pv.safety_stock))
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

-- name: AdminVariantBySKU :one
SELECT pv.id, pv.sku, pv.stock_quantity, pv.safety_stock,
       p.name AS product_name, p.slug
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.sku = $1;

-- What a stock-desk write replaces, read under the row lock the write then
-- holds: read before the transaction, a concurrent write can change it first
-- and the audit row's "before" names a value this write never saw.
-- name: LockVariantForChange :one
SELECT stock_quantity, is_active, price_cents, preorder_release_on
FROM product_variants WHERE id = $1 FOR NO KEY UPDATE;

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

-- The stock at the end of each shop day from first_day to last_day, worked back
-- from stock_quantity through the movements after that day. The column and the
-- ledger are read in this one statement, so a movement committed meanwhile is in
-- both or in neither; the movements are bounded below only, because the column
-- already holds every one of them. record_inventory_movement writes both, so the
-- two agree. A day without movements is included.
-- name: VariantStockByDay :many
WITH v AS (
    SELECT id, stock_quantity FROM product_variants WHERE sku = @sku::text
), moved AS (
    SELECT shop_day(m.created_at) AS day,
           sum(m.delta) AS delta,
           coalesce(sum(m.delta) FILTER (WHERE m.reason = 'receipt'), 0) AS received,
           count(*) FILTER (WHERE m.reason = 'receipt') AS receipts,
           count(*) AS moves
    FROM inventory_movements m
    JOIN v ON v.id = m.variant_id
    WHERE m.created_at >= @from_at::timestamptz
    GROUP BY 1
)
SELECT d.day::date AS day,
       (v.stock_quantity - coalesce((SELECT sum(l.delta) FROM moved l WHERE l.day > d.day::date), 0))::integer AS stock,
       coalesce(t.received, 0)::integer AS received,
       coalesce(t.receipts, 0)::integer AS receipts,
       coalesce(t.moves, 0)::integer AS moves
FROM v
CROSS JOIN generate_series(@first_day::date, @last_day::date, interval '1 day') AS d(day)
LEFT JOIN moved t ON t.day = d.day::date
ORDER BY d.day;

-- reason 'receipt' and not 'adjustment', which is the whole of it: goods a shop
-- bought must be distinguishable in its own ledger from a corrected miscount.
-- There is no source_id, because goen has no purchasing table to point at.
-- name: ReceiveStock :exec
SELECT record_inventory_movement(
    @variant_id, @delta::integer, 'receipt',
    @idempotency_key::text, 'admin', NULL, @actor_user_id::uuid
);

-- name: SetVariantArrival :exec
UPDATE product_variants SET preorder_release_on = sqlc.narg('arrival_on')::date
WHERE id = $1;

-- Every active variant that sold in [from_at, to_at) or has nothing a sale may
-- take. Sales are counted in orders as well as units: the estimate's sample size
-- is the orders, since one order of ten units is one event. Ranking and the
-- estimate are the page's.
-- name: StockAtRisk :many
SELECT
    pv.id AS variant_id,
    pv.sku,
    p.name AS product_name,
    p.slug,
    pv.stock_quantity,
    pv.safety_stock,
    sold.units::bigint AS units_sold,
    sold.orders::bigint AS orders_sold
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
JOIN LATERAL (
    SELECT coalesce(sum(ol.quantity), 0) AS units, count(DISTINCT o.id) AS orders
    FROM order_lines ol
    JOIN orders o ON o.id = ol.order_id
    JOIN sold_orders c ON c.id = o.id
    WHERE ol.variant_id = pv.id
      AND o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
) sold ON true
WHERE pv.is_active AND p.status = 'active'
  AND (sold.orders > 0 OR pv.stock_quantity <= pv.safety_stock)
ORDER BY pv.sku;

-- The ledger since from_at, from which a variant's stock at from_at is rolled
-- back and the days it had anything to sell are counted.
-- name: StockMovementsSince :many
SELECT m.variant_id, m.created_at, m.delta
FROM inventory_movements m
WHERE m.variant_id = ANY(@variant_ids::uuid[]) AND m.created_at >= @from_at::timestamptz
ORDER BY m.variant_id, m.created_at, m.id;
