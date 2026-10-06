-- A period is [from_at, to_at), cut by the caller on the shop's clock.
-- SOLD orders only, and the total is recomputed from the lines because
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
    -- order refunded before shipment is left out of both figures: it is no sale,
    -- and counting its refund would take it off the net a second time. Not
    -- through sold_orders, which would also drop money that went back on an
    -- order no longer committed.
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
    JOIN sold_orders s ON s.id = o.id
    WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
) t;

-- One row per shop day from first_day to last_day, a day without orders
-- included. The orders and their total are RevenueBetween's, so the days add up
-- to its revenue. The bounds are cut on the shop's clock by the caller.
-- name: PaidByShopDay :many
SELECT
    d.day::date AS day,
    count(t.total)::bigint AS orders,
    coalesce(sum(t.total), 0)::bigint AS revenue_cents
FROM generate_series(@first_day::date, @last_day::date, interval '1 day') AS d(day)
LEFT JOIN (
    SELECT shop_day(o.placed_at) AS day,
           (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                      FROM order_lines ol WHERE ol.order_id = o.id), 0)
            - o.discount_cents + o.shipping_cents + o.tax_cents)::bigint AS total
    FROM orders o
    JOIN sold_orders s ON s.id = o.id
    WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
) t ON t.day = d.day::date
GROUP BY d.day
ORDER BY d.day;

-- The newest sold order by when its money came in, which is read as
-- admin/health reads funded_at (UninvoicedOrders); the total is
-- RevenueBetween's. Elapsed is on the database's clock, as every dashboard age is.
-- Only orders placed since @since are looked at, so the dashboard does not read
-- the whole history; the caller asks again with no bound when none qualifies.
-- name: LatestPaidOrder :one
SELECT o.order_number, f.total_cents,
       coalesce(greatest(extract(epoch FROM now() - f.funded_at), 0), 0)::bigint AS elapsed_seconds
FROM orders o
JOIN sold_orders s ON s.id = o.id
CROSS JOIN LATERAL (
    SELECT coalesce(
               (SELECT min(e.occurred_at) FROM order_events e
                WHERE e.order_id = o.id AND e.kind = 'paid'),
               (SELECT max(p.paid_at) FROM payments p
                WHERE p.order_id = o.id AND p.status = 'succeeded'),
               o.placed_at)::timestamptz AS funded_at,
           (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                      FROM order_lines ol WHERE ol.order_id = o.id), 0)
            - o.discount_cents + o.shipping_cents + o.tax_cents)::bigint AS total_cents
) f
WHERE o.placed_at >= @since::timestamptz
ORDER BY f.funded_at DESC, o.id DESC
LIMIT 1;

-- The campaigns that were on at any time in [from_at, to_at), as the shop days
-- they cover; a campaign's last day is the one its ends_at falls in, and an
-- ends_at at midnight belongs to the day before.
-- name: CampaignsBetween :many
SELECT localized_name(c.title, c.title_en, @locale::text) AS title,
       shop_day(c.starts_at) AS first_day,
       shop_day(c.ends_at - interval '1 microsecond') AS last_day
FROM sale_campaigns c
WHERE c.is_active
  AND c.starts_at < @to_at::timestamptz AND c.ends_at > @from_at::timestamptz
ORDER BY c.starts_at, c.id;

-- The shop day of the latest paid order placed before to_at, counted as
-- PaidByShopDay counts; no row when there is none.
-- name: LatestPaidDay :one
SELECT shop_day(o.placed_at) AS day
FROM orders o
JOIN sold_orders s ON s.id = o.id
WHERE o.placed_at < @to_at::timestamptz
ORDER BY o.placed_at DESC
LIMIT 1;

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
JOIN sold_orders s ON s.id = o.id
LEFT JOIN brands b ON b.id = p.brand_id
WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
GROUP BY p.slug, p.name, b.name
ORDER BY units DESC, revenue_cents DESC
LIMIT @limit_to::integer;

-- Units on decided-yes returns (approved or completed) against units sold, both
-- counted over the sold orders placed in the period, so a product's returned
-- never exceeds its sold. Ties on the count fall to the larger sale, then the
-- name, so the list does not reshuffle between reads.
-- name: ReturnedProductsBetween :many
WITH period_lines AS (
    SELECT ol.id, ol.product_id, ol.quantity
    FROM order_lines ol
    JOIN orders o ON o.id = ol.order_id
    JOIN sold_orders s ON s.id = o.id
    WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
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

-- A department is a top-level category; a product in a deeper one counts toward
-- its root. The orders are those of RevenueBetween, so the departments add up to
-- the line part of its revenue. A line with no product_id (a legacy import) belongs to no department.
-- name: DepartmentSalesBetween :many
WITH RECURSIVE tree AS (
    SELECT id, id AS root_id FROM categories WHERE parent_id IS NULL
    UNION ALL
    SELECT k.id, t.root_id FROM categories k JOIN tree t ON k.parent_id = t.id
)
SELECT
    localized_name(d.name, d.name_en, @locale::text) AS name,
    sum(ol.unit_price_cents * ol.quantity)::bigint AS sales_cents
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
JOIN sold_orders s ON s.id = o.id
JOIN products p ON p.id = ol.product_id
JOIN tree t ON t.id = p.category_id
JOIN categories d ON d.id = t.root_id
WHERE o.placed_at >= @from_at::timestamptz AND o.placed_at < @to_at::timestamptz
GROUP BY d.id, d.name, d.name_en, d.position
ORDER BY sales_cents DESC, d.position, d.id;

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
