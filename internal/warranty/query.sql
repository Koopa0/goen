-- What a customer may still register, bounded by delivered_at and never by
-- shipped_at: cover counted from dispatch is one to three days short, all of
-- them off the customer. A unit in an approved return can no longer start cover,
-- so registrable units are the delivered ones less the returned ones. Ownership
-- is in the query, so it cannot be skipped.
-- name: RegistrableLines :many
SELECT
    ol.id AS order_line_id,
    ol.product_name,
    ol.variant_label,
    p.slug AS product_slug,
    ol.warranty_months,
    coalesce(ol.warranty_note, '') AS warranty_note,
    greatest(coalesce(delivered.units, 0) - coalesce(returned.units, 0), 0)::integer AS delivered_units,
    coalesce(returned.units, 0)::integer AS returned_units,
    coalesce(registered.units, 0)::integer AS registered_units
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
LEFT JOIN products p ON p.id = ol.product_id
LEFT JOIN LATERAL (
    -- Only the parcels that ARRIVED: an order shipped in two boxes of which one
    -- has landed can register what landed and no more.
    SELECT sum(sl.quantity) AS units
    FROM order_shipment_lines sl
    JOIN order_shipments s ON s.id = sl.shipment_id
    WHERE sl.order_line_id = ol.id AND s.delivered_at IS NOT NULL
) delivered ON true
LEFT JOIN LATERAL (
    SELECT sum(rl.quantity) AS units
    FROM return_request_lines rl
    JOIN return_requests rr ON rr.id = rl.return_request_id
    WHERE rl.order_line_id = ol.id AND rr.status IN ('approved', 'completed')
) returned ON true
LEFT JOIN LATERAL (
    SELECT count(*) AS units
    FROM warranty_registrations w WHERE w.order_line_id = ol.id
) registered ON true
WHERE o.order_number = @order_number::text
  AND o.user_id = @user_id
ORDER BY ol.position, ol.id;

-- Register one unit. expires_on is computed here from the delivery date and the
-- promise copied onto the order, never from today's mutable catalogue. Unit n
-- is the n-th unit of the line counted across its parcels in shipment order, and
-- its cover starts on the day THAT parcel arrived: a later box does not inherit
-- the first box's earlier date. An approved return takes units off the count.
-- name: RegisterWarranty :execrows
INSERT INTO warranty_registrations (order_line_id, unit_no, user_id, serial_number, expires_on)
SELECT ol.id, @unit_no::smallint, @user_id, nullif(@serial_number::text, ''),
       (shop_day(parcel.delivered_at) + make_interval(months => ol.warranty_months))::date
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
JOIN LATERAL (
    SELECT p.delivered_at
    FROM (
        SELECT s.delivered_at, sl.quantity,
               sum(sl.quantity) OVER (ORDER BY s.shipped_at, s.id) - sl.quantity AS units_before
        FROM order_shipment_lines sl
        JOIN order_shipments s ON s.id = sl.shipment_id
        WHERE sl.order_line_id = ol.id
    ) p
    WHERE @unit_no::smallint > p.units_before
      AND @unit_no::smallint <= p.units_before + p.quantity
      AND p.delivered_at IS NOT NULL
) parcel ON true
WHERE ol.id = @order_line_id
  AND o.order_number = @order_number::text
  AND o.user_id = @user_id
  AND ol.warranty_months IS NOT NULL
  AND @unit_no::smallint <= (
      SELECT coalesce(sum(sl.quantity), 0)
      FROM order_shipment_lines sl
      JOIN order_shipments s ON s.id = sl.shipment_id
      WHERE sl.order_line_id = ol.id AND s.delivered_at IS NOT NULL
  ) - (
      SELECT coalesce(sum(rl.quantity), 0)
      FROM return_request_lines rl
      JOIN return_requests rr ON rr.id = rl.return_request_id
      WHERE rl.order_line_id = ol.id AND rr.status IN ('approved', 'completed')
  );

-- What this customer has registered.
-- name: MyWarranties :many
SELECT w.id, w.unit_no, coalesce(w.serial_number, '') AS serial_number,
       w.registered_at, w.expires_on,
       (w.expires_on >= shop_today())::boolean AS in_force,
       ol.product_name, ol.variant_label, o.id AS order_id, o.order_number,
       coalesce(p.slug, '') AS product_slug
FROM warranty_registrations w
JOIN order_lines ol ON ol.id = w.order_line_id
JOIN orders o ON o.id = ol.order_id
LEFT JOIN products p ON p.id = ol.product_id
WHERE w.user_id = @user_id
ORDER BY w.expires_on DESC, w.id;
