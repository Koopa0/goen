-- A period is [from_at, to_at), cut by the caller on the shop's clock.
-- COMMITTED orders only, and the total is recomputed from the lines because
-- orders carries no total column. Integer division on the average, so no float
-- touches money, and greatest(count, 1) because an empty window divides by zero.
-- name: RevenueBetween :one
SELECT
    count(*)::bigint AS orders,
    coalesce(sum(t.total), 0)::bigint AS revenue_cents,
    (coalesce(sum(t.total), 0) / greatest(count(*), 1))::bigint AS average_cents,
    -- Float, not money: only the report's noise test reads it.
    coalesce(sum(t.total::float8 * t.total), 0)::float8 AS sum_of_squares,
    -- What went back, counted by when each source moved: succeeded_at for a card
    -- refund (created_at can be days earlier while Stripe still says pending),
    -- created_at for the synchronous credit post.
    -- The positive-credit predicate matches order_refunds, where a change of that
    -- definition belongs, so the 折讓 form and the invoice bound move with it. An
    -- order refunded before shipment is left out of both figures: its refund
    -- already cancels it out of committed revenue, and counting it again would
    -- take it off the net twice.
    (coalesce((SELECT sum(r.amount_cents) FROM refunds r
               JOIN payments p ON p.id = r.payment_id
               WHERE r.status = 'succeeded'
                 AND r.succeeded_at >= @from_at::timestamptz AND r.succeeded_at < @to_at::timestamptz
                 AND NOT EXISTS (SELECT 1 FROM return_requests b
                                 WHERE b.order_id = p.order_id AND b.before_shipment)), 0)::bigint
     + coalesce((SELECT sum(e.amount_cents) FROM store_credit_entries e
                 WHERE e.order_id IS NOT NULL AND e.amount_cents > 0
                   AND e.created_at >= @from_at::timestamptz AND e.created_at < @to_at::timestamptz
                   AND NOT EXISTS (SELECT 1 FROM return_requests b
                                   WHERE b.order_id = e.order_id AND b.before_shipment)), 0)::bigint
    )::bigint AS refunded_cents
FROM (
    SELECT (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                      FROM order_lines ol WHERE ol.order_id = o.id), 0)
            - o.discount_cents + o.shipping_cents + o.tax_cents)::bigint AS total
    FROM orders o
    JOIN committed_orders c ON c.id = o.id
    WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
      AND NOT EXISTS (SELECT 1 FROM return_requests b
                      WHERE b.order_id = o.id AND b.before_shipment)
) t;

-- name: BestSellersBetween :many
SELECT
    p.slug,
    p.name,
    coalesce(b.name, '') AS brand,
    sum(ol.quantity)::bigint AS units,
    sum(ol.unit_price_cents * ol.quantity)::bigint AS revenue_cents
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
JOIN products p ON p.id = ol.product_id
JOIN committed_orders c ON c.id = o.id
LEFT JOIN brands b ON b.id = p.brand_id
WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
GROUP BY p.slug, p.name, b.name
ORDER BY units DESC, revenue_cents DESC
LIMIT @limit_to::integer;

-- Units on decided-yes returns (approved or completed) against units sold, both
-- counted over the orders placed in the period, so a product's returned never
-- exceeds its sold. Orders refunded before shipment are left out of both, as in
-- RevenueBetween: no goods came back. Ties on the count fall to the larger sale,
-- then the name, so the list does not reshuffle between reads.
-- name: ReturnedProductsBetween :many
WITH period_lines AS (
    SELECT ol.id, ol.product_id, ol.quantity
    FROM order_lines ol
    JOIN orders o ON o.id = ol.order_id
    JOIN committed_orders c ON c.id = o.id
    WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
      AND NOT EXISTS (SELECT 1 FROM return_requests b
                      WHERE b.order_id = o.id AND b.before_shipment)
), sold AS (
    SELECT product_id, sum(quantity)::bigint AS units
    FROM period_lines GROUP BY product_id
), returned AS (
    SELECT pl.product_id, sum(rl.quantity)::bigint AS units
    FROM return_request_lines rl
    JOIN return_requests rr ON rr.id = rl.return_request_id
    JOIN period_lines pl ON pl.id = rl.order_line_id
    WHERE rr.status IN ('approved', 'completed')
    GROUP BY pl.product_id
)
SELECT
    p.slug,
    p.name,
    coalesce(b.name, '') AS brand,
    r.units AS returned_units,
    s.units AS sold_units
FROM returned r
JOIN sold s ON s.product_id = r.product_id
JOIN products p ON p.id = r.product_id
LEFT JOIN brands b ON b.id = p.brand_id
ORDER BY r.units DESC, s.units DESC, p.name, p.slug
LIMIT @limit_to::integer;

-- NOT a conversion rate: goen collects no traffic data. This is the fraction of
-- started orders that were paid for. A LEFT JOIN and a CASE, never a per-row
-- function call — measured at 106 ms over 14,000 orders against 7.7 ms.
-- name: CheckoutCompletionBetween :one
SELECT
    count(*)::bigint AS placed,
    coalesce(sum(CASE WHEN c.id IS NOT NULL THEN 1 ELSE 0 END), 0)::bigint AS committed
FROM orders o
LEFT JOIN committed_orders c ON c.id = o.id
WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz;

-- Every active variant that sold in [from_at, to_at) or has nothing a sale may
-- take. Sales are counted in orders as well as units: the report's sample size
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
    JOIN committed_orders c ON c.id = o.id
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
