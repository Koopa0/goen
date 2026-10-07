-- Probe only. Picks three single-variant products, makes one sold out and one
-- low on stock, and builds a cart holding all three. Writes SOLD_SLUG, LOW_SLUG,
-- LOW_SLUG2 and MIXED_CART_TOKEN to the file named by :env.
\set ON_ERROR_STOP on

SELECT id AS staff_id FROM users
WHERE lower(email) = 'layout-check@goen.invalid' AND role = 'admin' \gset

CREATE TEMP TABLE picks AS
SELECT row_number() OVER (ORDER BY p.slug) AS n, p.slug, min(pv.id::text)::uuid AS vid,
       min(pv.stock_quantity) AS stock, min(pv.safety_stock) AS safety
FROM products p JOIN product_variants pv ON pv.product_id = p.id
WHERE p.status = 'active' AND pv.is_active AND p.slug NOT LIKE 'pixelight%' AND p.slug <> 'meridian-watch-c1'
GROUP BY p.id, p.slug
HAVING count(*) = 1 AND min(pv.stock_quantity) > min(pv.safety_stock) + 8;

SELECT slug AS sold_slug, vid AS sold_vid, stock - safety AS sold_delta FROM picks WHERE n = 1 \gset
SELECT slug AS low_slug, vid AS low_vid, stock - safety - 3 AS low_delta FROM picks WHERE n = 2 \gset
SELECT slug AS norm_slug, vid AS norm_vid FROM picks WHERE n = 3 \gset

SELECT encode(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid()), 'hex') AS mixed_token \gset

SET ROLE store;
INSERT INTO carts (token_hash) VALUES (sha256(convert_to(:'mixed_token', 'UTF8')))
RETURNING id AS mixed_cart_id \gset
INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES
    (:'mixed_cart_id', :'sold_vid', 1),
    (:'mixed_cart_id', :'low_vid', 2),
    (:'mixed_cart_id', :'norm_vid', 1);

SET ROLE admin;
SELECT record_inventory_movement(:'sold_vid', -(:sold_delta), 'adjustment', 'text200:' || gen_random_uuid(), 'admin', NULL, :'staff_id');
SELECT record_inventory_movement(:'low_vid', -(:low_delta), 'adjustment', 'text200:' || gen_random_uuid(), 'admin', NULL, :'staff_id');

\o :env
\qecho SOLD_SLUG=:sold_slug
\qecho LOW_SLUG=:low_slug
\qecho NORM_SLUG=:norm_slug
\qecho MIXED_CART_TOKEN=:mixed_token
\o
