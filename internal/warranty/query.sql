-- What a customer may still register, for one of their orders.
--
-- Bounded by what was DELIVERED, not by what was dispatched and not by what was
-- ordered. A warranty starts when the goods reach somebody, and this query used
-- to say so in a comment while reading shipped_at: cover counted from dispatch
-- is one to three days short, and every one of those days is taken off the
-- CUSTOMER.
--
-- It could not be written this way when the feature shipped, because
-- order_shipments.delivered_at was read by two pages and written by nothing.
-- applyStatusEffects stamps it now, on BOTH transitions that end a delivery, so
-- 宅配 and 超商取貨 each reach this. /admin/returns already reads that column to
-- decide whether a request is inside 消保法 §19's seven days — two features
-- asking "when did the goods reach somebody" have to read ONE column, or the
-- shop is answering the same question two ways.
--
-- Ownership is IN the query. A registration form that read the order and then
-- checked who owned it in Go is a check somebody can skip by posting straight
-- to the endpoint.
-- name: RegistrableLines :many
SELECT
    ol.id AS order_line_id,
    ol.product_name,
    ol.variant_label,
    p.slug AS product_slug,
    p.warranty_months,
    coalesce(p.warranty_note, '') AS warranty_note,
    coalesce(delivered.units, 0)::integer AS delivered_units,
    coalesce(registered.units, 0)::integer AS registered_units
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
LEFT JOIN product_variants pv ON pv.id = ol.variant_id
LEFT JOIN products p ON p.id = pv.product_id
LEFT JOIN LATERAL (
    -- Only the parcels that ARRIVED. An order shipped in two boxes of which one
    -- has landed can register what landed and no more, which is the same
    -- per-parcel truth /admin/returns reads and the reason partial shipment had
    -- to exist before this could be written.
    SELECT sum(sl.quantity) AS units
    FROM order_shipment_lines sl
    JOIN order_shipments s ON s.id = sl.shipment_id
    WHERE sl.order_line_id = ol.id AND s.delivered_at IS NOT NULL
) delivered ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS units
    FROM warranty_registrations w WHERE w.order_line_id = ol.id
) registered ON true
WHERE o.order_number = @order_number::text
  AND o.user_id = @user_id
ORDER BY ol.position, ol.id;

-- Register one unit.
--
-- expires_on is computed HERE from the DELIVERY date and the product's term,
-- never passed in: an expiry a form could carry is an expiry a customer could
-- choose. The delivery date is the database's own, so this is one clock at both
-- ends — the /admin/messages lesson, which is also why it is not now() plus a
-- term read separately.
--
-- min() across the parcels, so a line split between two boxes takes the date the
-- FIRST of them arrived. That is the reading that favours the shop by the
-- smallest margin available and is still defensible: the customer had a unit of
-- that line in their hands on that day.
--
-- The whole thing is one statement guarded by a WHERE clause, so ownership,
-- "it arrived", and "the term exists" are all decided under the same read the
-- insert uses. Checking them first in Go would be checking them against a state
-- another request can change in between.
-- name: RegisterWarranty :execrows
INSERT INTO warranty_registrations (order_line_id, unit_no, user_id, serial_number, expires_on)
SELECT ol.id, @unit_no::smallint, @user_id, nullif(@serial_number::text, ''),
       (delivered.at + make_interval(months => p.warranty_months))::date
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
JOIN product_variants pv ON pv.id = ol.variant_id
JOIN products p ON p.id = pv.product_id
JOIN LATERAL (
    SELECT min(s.delivered_at) AS at, sum(sl.quantity) AS units
    FROM order_shipment_lines sl
    JOIN order_shipments s ON s.id = sl.shipment_id
    WHERE sl.order_line_id = ol.id AND s.delivered_at IS NOT NULL
) delivered ON delivered.at IS NOT NULL
WHERE ol.id = @order_line_id
  AND o.user_id = @user_id
  AND p.warranty_months IS NOT NULL
  AND @unit_no::smallint <= delivered.units;

-- What this customer has registered.
-- name: MyWarranties :many
SELECT w.id, w.unit_no, coalesce(w.serial_number, '') AS serial_number,
       w.registered_at, w.expires_on,
       (w.expires_on >= current_date)::boolean AS in_force,
       ol.product_name, ol.variant_label, o.order_number,
       coalesce(p.slug, '') AS product_slug
FROM warranty_registrations w
JOIN order_lines ol ON ol.id = w.order_line_id
JOIN orders o ON o.id = ol.order_id
LEFT JOIN product_variants pv ON pv.id = ol.variant_id
LEFT JOIN products p ON p.id = pv.product_id
WHERE w.user_id = @user_id
ORDER BY w.expires_on DESC, w.id;
