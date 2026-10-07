-- Probe only: the two states the line review asks to see, written the way
-- scripts/check-layout.sql writes its own, on top of what it left.
-- Run after it: psql -v customer_id=<CUSTOMER_ID> -v env=<file> -f scripts/lines-probe-seed.sql

\set ON_ERROR_STOP on
\o /dev/null

BEGIN;

SELECT encode(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid()), 'hex') AS cart2_token,
       encode(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid()), 'hex') AS cart3_token \gset

SET ROLE admin;
SELECT id AS staff_id FROM users
WHERE lower(email) = 'layout-check@goen.invalid' AND role = 'admin' \gset

-- Four variants of four products in stock: three for the cart, one the cart
-- keeps while its stock goes to the safety stock, which is sold out.
SELECT array_agg(v.id ORDER BY v.rn) AS pick_ids FROM (
    SELECT pv.id, row_number() OVER (ORDER BY pv.price_cents, pv.sku) AS rn
    FROM product_variants pv JOIN products p ON p.id = pv.product_id
    WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock + 5
      AND pv.sku <> 'PXL-9-1-1'
) v WHERE v.rn IN (3, 5, 7, 9) \gset
SELECT pv.id AS soldout_id, pv.stock_quantity AS soldout_stock, pv.safety_stock AS soldout_safety
FROM product_variants pv WHERE pv.id = (:'pick_ids'::uuid[])[4] \gset
SELECT record_inventory_movement(:'soldout_id', :soldout_safety - :soldout_stock, 'adjustment',
    'lines-probe:' || gen_random_uuid(), 'admin', NULL, :'staff_id');

SET ROLE store;
INSERT INTO carts (token_hash) VALUES (sha256(convert_to(:'cart2_token', 'UTF8')))
RETURNING id AS cart2_id \gset
INSERT INTO cart_items (cart_id, variant_id, quantity)
SELECT :'cart2_id', (:'pick_ids'::uuid[])[n], q
FROM (VALUES (1, 1), (2, 2), (3, 1), (4, 1)) AS t (n, q);

-- The same three lines without the sold-out one: checkout stays reachable.
INSERT INTO carts (token_hash) VALUES (sha256(convert_to(:'cart3_token', 'UTF8')))
RETURNING id AS cart3_id \gset
INSERT INTO cart_items (cart_id, variant_id, quantity)
SELECT :'cart3_id', (:'pick_ids'::uuid[])[n], q
FROM (VALUES (1, 1), (2, 2), (3, 1)) AS t (n, q);

-- An order of two lines that leaves in two parcels, delivered.
SELECT v.id AS ship_version, sm.code AS ship_code, v.name AS ship_name, v.fee_cents AS ship_cents
FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
WHERE sm.code = 'home_delivery' AND sm.is_active AND v.effective_at <= now()
ORDER BY v.effective_at DESC LIMIT 1 \gset
INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
VALUES (:'customer_id', :'ship_version', :'ship_code', :'ship_name', :ship_cents)
RETURNING id AS two_id, order_number AS two_order \gset
INSERT INTO order_lines (order_id, product_id, variant_id, sku, product_name, variant_label,
                         warranty_note, warranty_months, unit_price_cents, quantity, position)
SELECT :'two_id', p.id, pv.id, pv.sku, p.name, NULL, p.warranty_note, p.warranty_months,
       pv.price_cents, 1, n - 1
FROM (VALUES (1), (2)) AS t (n)
JOIN product_variants pv ON pv.id = (:'pick_ids'::uuid[])[n]
JOIN products p ON p.id = pv.product_id;
SELECT hold_inventory(order_id, variant_id, quantity, interval '60 minutes',
                      'hold:' || order_id || ':' || variant_id)
FROM order_lines WHERE order_id = :'two_id';
INSERT INTO order_events (order_id, kind) VALUES (:'two_id', 'placed');
INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
VALUES (:'two_id', 'member_carrier', '版面顧客', 'layout-cust@goen.invalid');
INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
VALUES (:'two_id', 'layout-cust@goen.invalid', '版面顧客', '0912345678', '110', '台北市', '信義區', '松高路 1 號');

SET ROLE admin;
SELECT grant_store_credit(:'customer_id', 5000000, 'Probe fixture', :'staff_id', gen_random_uuid());
SET ROLE store;
SELECT spend_store_credit(:'two_id', -order_amount_after_credit(:'two_id'));
SET ROLE admin;
UPDATE orders SET fulfillment_status = 'picking' WHERE id = :'two_id';
INSERT INTO order_events (order_id, kind) VALUES (:'two_id', 'paid');
SELECT award_loyalty_points(:'two_id');
INSERT INTO order_events (order_id, kind, actor_user_id) VALUES (:'two_id', 'picking', :'staff_id');

INSERT INTO order_shipments (order_id, carrier, tracking_number)
VALUES (:'two_id', 'black_cat', 'LINESA' || translate(:'two_order', 'GO-', '')),
       (:'two_id', 'hct', 'LINESB' || translate(:'two_order', 'GO-', ''));
INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
SELECT s.order_id, s.id, ol.id, ol.quantity
FROM order_shipments s
JOIN order_lines ol ON ol.order_id = s.order_id
WHERE s.order_id = :'two_id'
  AND ol.position = CASE s.carrier WHEN 'black_cat' THEN 0 ELSE 1 END;
SELECT consume_reservation_partial(id, quantity) FROM inventory_reservations
WHERE order_id = :'two_id' AND state = 'held';
UPDATE orders SET fulfillment_status = 'shipped' WHERE id = :'two_id';
INSERT INTO order_events (order_id, kind, note, actor_user_id)
SELECT order_id, 'shipped', carrier || ' ' || tracking_number, :'staff_id'
FROM order_shipments WHERE order_id = :'two_id';
UPDATE orders SET fulfillment_status = 'delivered' WHERE id = :'two_id';
UPDATE order_shipments SET delivered_at = greatest(now(), shipped_at)
WHERE order_id = :'two_id' AND delivered_at IS NULL;
INSERT INTO order_events (order_id, kind, actor_user_id) VALUES (:'two_id', 'delivered', :'staff_id');

COMMIT;

\o :env
\qecho CART2_TOKEN=:cart2_token
\qecho CART3_TOKEN=:cart3_token
\qecho TWO_PARCEL_ORDER=:two_order
\o
