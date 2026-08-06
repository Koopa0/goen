-- What a customer may still register, for one of their orders.
--
-- Bounded by what SHIPPED, not by what was ordered — a warranty starts when the
-- goods reach somebody, and registering cover for a box still in the warehouse
-- would start the clock early. Same rule internal/returns follows, for the same
-- reason.
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
    coalesce(shipped.units, 0)::integer AS shipped_units,
    coalesce(registered.units, 0)::integer AS registered_units
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
LEFT JOIN product_variants pv ON pv.id = ol.variant_id
LEFT JOIN products p ON p.id = pv.product_id
LEFT JOIN LATERAL (
    SELECT sum(sl.quantity) AS units
    FROM order_shipment_lines sl
    JOIN order_shipments s ON s.id = sl.shipment_id
    WHERE sl.order_line_id = ol.id
) shipped ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS units
    FROM warranty_registrations w WHERE w.order_line_id = ol.id
) registered ON true
WHERE o.order_number = @order_number::text
  AND o.user_id = @user_id
ORDER BY ol.position, ol.id;

-- Register one unit.
--
-- expires_on is computed HERE from the shipment date and the product's term,
-- never passed in: an expiry a form could carry is an expiry a customer could
-- choose. The shipment date is the database's own, so this is one clock.
--
-- The whole thing is one statement guarded by a WHERE clause, so ownership,
-- "it shipped", and "the term exists" are all decided under the same read the
-- insert uses. Checking them first in Go would be checking them against a state
-- another request can change in between.
-- name: RegisterWarranty :execrows
INSERT INTO warranty_registrations (order_line_id, unit_no, user_id, serial_number, expires_on)
SELECT ol.id, @unit_no::smallint, @user_id, nullif(@serial_number::text, ''),
       (shipped.at + make_interval(months => p.warranty_months))::date
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
JOIN product_variants pv ON pv.id = ol.variant_id
JOIN products p ON p.id = pv.product_id
JOIN LATERAL (
    SELECT min(s.shipped_at) AS at, sum(sl.quantity) AS units
    FROM order_shipment_lines sl
    JOIN order_shipments s ON s.id = sl.shipment_id
    WHERE sl.order_line_id = ol.id
) shipped ON shipped.at IS NOT NULL
WHERE ol.id = @order_line_id
  AND o.user_id = @user_id
  AND p.warranty_months IS NOT NULL
  AND @unit_no::smallint <= shipped.units;

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
