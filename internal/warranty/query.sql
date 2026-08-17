-- What a customer may still register, bounded by delivered_at and never by
-- shipped_at: cover counted from dispatch is one to three days short, all of
-- them off the customer. Ownership is in the query, so it cannot be skipped.
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
    -- Only the parcels that ARRIVED: an order shipped in two boxes of which one
    -- has landed can register what landed and no more.
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

-- Register one unit. expires_on is computed here from the delivery date and the
-- product's term, never passed in, and min() across the parcels runs a split
-- line from the day the first box arrived.
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
