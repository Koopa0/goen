-- The state scripts/check-layout.mjs measures, written the way the shop writes
-- it: each section under the role whose pool makes that write in production,
-- and every privileged write through the function the application calls. A
-- state the application cannot reach is refused here, by name, before any page
-- is measured, rather than surfacing later as a page that measured nothing.
--
-- Run by `make check-layout` with -v env=<file>: the values the browser needs
-- are written there and nowhere else. psql stops on the first refusal, and on
-- a \gset that found no row; a NULL column would silently unset its variable,
-- so nothing nullable is read back.

\set ON_ERROR_STOP on
\set VERBOSITY verbose
\o /dev/null

BEGIN;

-- Sessions are inserted rather than signed into, because signing in needs an
-- argon2 hash. Only the digest is stored, as the application stores it, and
-- the tokens are new every run: one checked into the repository would sign
-- anybody in as staff on every database this file has ever been run against.
SELECT encode(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid()), 'hex') AS admin_token,
       encode(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid()), 'hex') AS cust_token,
       encode(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid()), 'hex') AS cart_token,
       encode(uuid_send(gen_random_uuid()) || uuid_send(gen_random_uuid()), 'hex') AS placed_token \gset

SET ROLE admin;

-- upsert_staff refuses an account already on the roster, so a rerun keeps the
-- first run's.
SELECT upsert_staff('layout-check@goen.invalid', '', 'admin')
WHERE NOT EXISTS (SELECT 1 FROM users WHERE lower(email) = 'layout-check@goen.invalid');
SELECT id AS staff_id FROM users
WHERE lower(email) = 'layout-check@goen.invalid' AND role = 'admin' \gset

-- With English copy, because the strip renders on every storefront page and
-- the sweep reads an English page without one as untranslated.
INSERT INTO promo_banners (message, message_short, code, cta_label, cta_href,
                           message_en, message_short_en, cta_label_en)
SELECT '版面檢查用的促銷訊息，長度接近真實的一句文案', '版面檢查促銷', 'LAYOUT10', '看看', '/deals',
       'A layout-check promotion, about as long as a real one', 'Layout promo', 'Look'
WHERE NOT EXISTS (SELECT 1 FROM promo_banners);

INSERT INTO sale_campaigns (slug, title, title_en, starts_at, ends_at)
VALUES ('layout-campaign', 'Layout campaign', 'Layout campaign',
        now() - interval '3 days', now() + interval '1 day')
ON CONFLICT (slug) DO UPDATE
SET starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, is_active = true;
INSERT INTO sale_campaign_products (campaign_id, product_id, position)
SELECT c.id, p.id, (row_number() OVER (ORDER BY p.discounted DESC, p.slug))::integer - 1
FROM sale_campaigns c
CROSS JOIN LATERAL (
    SELECT p.id, p.slug,
           EXISTS (SELECT 1 FROM product_variants v
                   WHERE v.product_id = p.id AND v.is_active
                     AND v.compare_at_price_cents IS NOT NULL) AS discounted
    FROM products p WHERE p.status = 'active'
    ORDER BY 3 DESC, p.slug LIMIT 6
) p
WHERE c.slug = 'layout-campaign'
ON CONFLICT DO NOTHING;

SET ROLE store;

INSERT INTO users (email, full_name, phone)
VALUES ('layout-cust@goen.invalid', '版面顧客', '0912345678')
ON CONFLICT (lower(email)) DO NOTHING;
SELECT id AS customer_id FROM users
WHERE lower(email) = 'layout-cust@goen.invalid' AND role = 'customer' \gset

INSERT INTO sessions (token_hash, user_id, expires_at) VALUES
    (sha256(convert_to(:'admin_token', 'UTF8')), :'staff_id', now() + interval '1 hour'),
    (sha256(convert_to(:'cust_token', 'UTF8')), :'customer_id', now() + interval '1 hour');

-- What /admin/messages, /admin/questions and /account/wishlist list; each
-- renders an empty state that passes for a measured page without them.
INSERT INTO contact_messages (name, email, subject, message)
VALUES ('版面檢查', 'layout@goen.invalid', '訂單問題', '想確認一下出貨時間，謝謝。');
SELECT id AS product_id, slug AS product_slug FROM products
WHERE status = 'active' ORDER BY slug LIMIT 1 \gset
SELECT slug AS compare_slug FROM products
WHERE status = 'active' ORDER BY slug OFFSET 1 LIMIT 1 \gset
INSERT INTO product_questions (product_id, user_id, body)
VALUES (:'product_id', :'customer_id', '請問這款有支援快充嗎？盒裝裡面有附充電器嗎？');
-- Keep the unanswered queue entry and also measure the nested answer list,
-- including both present and absent staff names.
INSERT INTO product_questions (product_id, user_id, body)
VALUES (:'product_id', :'customer_id', '請問商品可以使用哪些充電方式？')
RETURNING id AS answered_question_id \gset
-- What the product editor's sales and reviews card draws: paid orders on nine
-- shop days, so the weekly columns have axes, and six reviews of three star
-- values, one of them hidden, so the spread is drawn and the hidden one stays out.
WITH sale_orders AS (
    INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name,
                        shipping_cents, placed_at)
    SELECT :'customer_id', v.id, sm.code, v.name, 0, now() - make_interval(days => 3 * n)
    FROM generate_series(1, 9) AS n,
         (SELECT v.id, v.name, v.method_id FROM shipping_method_versions v ORDER BY v.effective_at LIMIT 1) v
         JOIN shipping_methods sm ON sm.id = v.method_id
    RETURNING id
), sale_lines AS (
    INSERT INTO order_lines (order_id, product_id, sku, product_name, unit_price_cents, quantity)
    SELECT id, :'product_id', 'LAYOUT-STANDING', 'Standing chart fixture', 100, 1 + (row_number() OVER ())::int % 4
    FROM sale_orders
    RETURNING order_id
)
INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
SELECT order_id, 'layout-cust@goen.invalid', '版面顧客', '0912345678', '110', '台北市', '信義區', '松高路 1 號'
FROM sale_lines;
SELECT open_payment(o.id, 'layout-sale-' || o.id, ol.unit_price_cents * ol.quantity)
FROM orders o JOIN order_lines ol ON ol.order_id = o.id WHERE ol.sku = 'LAYOUT-STANDING';
SELECT capture_payment('layout-sale-' || o.id, ol.unit_price_cents * ol.quantity, NULL, NULL)
FROM orders o JOIN order_lines ol ON ol.order_id = o.id WHERE ol.sku = 'LAYOUT-STANDING';
-- store cannot write hidden_at: the one-star review is hidden below, as admin,
-- the way moderation hides one.
INSERT INTO product_reviews (product_id, rating, body)
SELECT :'product_id', r.rating, '版面檢查用的評價'
FROM (VALUES (5), (5), (5), (4), (2), (1)) AS r (rating);
INSERT INTO wishlist_items (user_id, product_id)
VALUES (:'customer_id', :'product_id')
ON CONFLICT (user_id, product_id) DO NOTHING;

-- Money that arrived for an order the shop had already cancelled: one of the
-- alarm tables /admin/health renders only when something is wrong.
INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload)
VALUES ('stripe', 'evt_layout_check', 'checkout.session.completed', 'cs_layout_check', '{}')
ON CONFLICT (provider, event_id) DO NOTHING;
SELECT mark_payment_event_unreconciled('evt_layout_check',
    'cancelled_order_capture: money arrived for an order that was already cancelled');

SET ROLE admin;

SELECT upsert_staff('layout-answer@goen.invalid', 'Mina', 'staff')
WHERE NOT EXISTS (SELECT 1 FROM users WHERE lower(email) = 'layout-answer@goen.invalid');
SELECT id AS named_answer_staff_id FROM users
WHERE lower(email) = 'layout-answer@goen.invalid' AND role = 'staff' AND full_name = 'Mina' \gset
SELECT true AS unnamed_answer_staff
FROM users WHERE id = :'staff_id' AND coalesce(full_name, '') = '' \gset

-- Match AnswerQuestionAsStaff and its audit event in this same transaction.
INSERT INTO product_answers (question_id, user_id, body, is_staff)
SELECT q.id, :'named_answer_staff_id', '你可以使用商品規格列出的充電方式。', true
FROM product_questions q WHERE q.id = :'answered_question_id' AND q.hidden_at IS NULL
RETURNING id AS named_answer_id \gset
SELECT record_audit_event(:'named_answer_staff_id', 'question.answer', 'product_answers', :'answered_question_id', NULL,
    jsonb_build_object('length', length('你可以使用商品規格列出的充電方式。')));
INSERT INTO product_answers (question_id, user_id, body, is_staff)
SELECT q.id, :'staff_id', '如果你需要確認配件，請提供商品型號。', true
FROM product_questions q WHERE q.id = :'answered_question_id' AND q.hidden_at IS NULL
RETURNING id AS unnamed_answer_id \gset
SELECT record_audit_event(:'staff_id', 'question.answer', 'product_answers', :'answered_question_id', NULL,
    jsonb_build_object('length', length('如果你需要確認配件，請提供商品型號。')));

-- The fixture's one-star review, hidden by moderation: the rating spread counts
-- visible reviews only.
UPDATE product_reviews SET hidden_at = now()
WHERE product_id = :'product_id' AND rating = 1 AND body = '版面檢查用的評價';

-- Enough store credit to pay for all four customer orders outright, so
-- neither needs a payment provider to leave pending.
SELECT grant_store_credit(:'customer_id', 9999900, '版面檢查用的退貨樣本', :'staff_id', gen_random_uuid());
SELECT grant_store_credit(:'customer_id', 1000000, 'Batch picking fixture', :'staff_id', gen_random_uuid());
SELECT record_audit_event(:'staff_id', 'credit.grant', 'store_credit_entries', :'customer_id', NULL,
    jsonb_build_object('amount_cents', 9999900, 'reason', '版面檢查用的退貨樣本'));
SELECT record_audit_event(:'staff_id', 'credit.grant', 'store_credit_entries', :'customer_id', NULL,
    jsonb_build_object('amount_cents', 1000000, 'reason', 'Batch picking fixture'));

SET ROLE store;

-- One variant for the cart and all five orders. Its sellable quantity is what
-- the cart's stepper is bounded by, and each run takes four units for good:
-- `make db-reset` is the reset.
SELECT pv.id AS variant_id FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.sku = 'PXL-9-1-1' AND p.status = 'active' AND pv.is_active
  AND pv.stock_quantity > pv.safety_stock \gset
SELECT p.slug AS review_slug FROM product_variants pv
JOIN products p ON p.id = pv.product_id WHERE pv.id = :'variant_id' \gset
SELECT v.id AS ship_version, sm.code AS ship_code, v.name AS ship_name,
       CASE WHEN pv.price_cents >= v.free_over_cents THEN 0 ELSE v.fee_cents END AS ship_cents
FROM shipping_method_versions v
JOIN shipping_methods sm ON sm.id = v.method_id
CROSS JOIN product_variants pv
WHERE sm.code = 'home_delivery' AND sm.is_active AND v.effective_at <= now()
  AND pv.id = :'variant_id'
ORDER BY v.effective_at DESC LIMIT 1 \gset

-- A second best seller, because /admin/reports draws bars only when there are
-- two sellers to compare. Its 1,000 units give the bars' count column four
-- digits; they ride the two picking orders as two lines of 500, since a line
-- holds at most 999. The cheapest variant in stock, so each line's credit fits
-- one grant; the receipt covers the holds, so its stock ends where it began. It
-- is in another department than the first, which gives the department bars two
-- rows to compare.
WITH RECURSIVE up AS (
    SELECT p.id AS product_id, c.id, c.parent_id FROM products p JOIN categories c ON c.id = p.category_id
    UNION ALL
    SELECT u.product_id, c.id, c.parent_id FROM up u JOIN categories c ON c.id = u.parent_id
),
department AS (SELECT product_id, id AS root_id FROM up WHERE parent_id IS NULL)
SELECT pv.id AS seller_variant_id, pv.sku AS seller_sku, pv.price_cents * 500 AS seller_line_cents
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
JOIN department d ON d.product_id = p.id
WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock
  AND d.root_id <> (SELECT dd.root_id FROM department dd
                    JOIN product_variants fv ON fv.product_id = dd.product_id
                    WHERE fv.id = :'variant_id')
ORDER BY pv.price_cents, pv.sku LIMIT 1 \gset

SET ROLE admin;

-- A coupon with a total limit, as the coupon form writes one: the list draws its
-- meter only for a capped coupon.
INSERT INTO coupons (code, description, kind, amount_cents, max_redemptions, per_customer_limit)
VALUES ('LAYOUT-CAP', 'Layout capped coupon', 'amount', 100, 5, 1)
RETURNING id AS capped_coupon_id \gset

SELECT record_inventory_movement(:'seller_variant_id', 1000, 'receipt',
    'layout-check:' || gen_random_uuid(), 'admin', NULL, :'staff_id');
SELECT record_audit_event(:'staff_id', 'stock.receive', 'product_variants', :'seller_variant_id',
    jsonb_build_object('sku', :'seller_sku'), jsonb_build_object('received', 1000));
SELECT grant_store_credit(:'customer_id', :seller_line_cents, 'Best-seller fixture', :'staff_id', gen_random_uuid()),
       record_audit_event(:'staff_id', 'credit.grant', 'store_credit_entries', :'customer_id', NULL,
           jsonb_build_object('amount_cents', :seller_line_cents, 'reason', 'Best-seller fixture'))
FROM generate_series(1, 2);

SET ROLE store;

INSERT INTO carts (token_hash) VALUES (sha256(convert_to(:'cart_token', 'UTF8')))
RETURNING id AS cart_id \gset
INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES (:'cart_id', :'variant_id', 1);

-- Checkout's saved-address select must be exercised as a signed-in customer.
INSERT INTO addresses (user_id, recipient_name, phone, postal_code, city, district, street, is_default)
VALUES (:'customer_id', '版面收件人', '0912345678', '110', '臺北市', '信義區', '測試路 1 號', true);

-- A tier above the fixture customer's spend, so the customer page draws its meter.
SET ROLE admin;
INSERT INTO membership_tiers (code, name, name_en, min_spend_cents)
VALUES ('layout_fixture', '版面檢查會員', 'Layout fixture', 10000000000)
ON CONFLICT DO NOTHING;
SET ROLE store;

-- Five orders placed as checkout places them. The guest's is unpaid and is the
-- payment page. The customer's four are paid in store credit: INVOICE_ORDER is
-- delivered, returned and refunded; RETURN_FORM_ORDER is delivered with nothing
-- sent back, which is the only state that renders the return form.
INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
VALUES (NULL, :'ship_version', :'ship_code', :'ship_name', :ship_cents)
RETURNING id AS placed_id, order_number AS placed_order \gset
INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
VALUES (:'customer_id', :'ship_version', :'ship_code', :'ship_name', :ship_cents)
RETURNING id AS invoice_id, order_number AS invoice_order \gset
INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
VALUES (:'customer_id', :'ship_version', :'ship_code', :'ship_name', :ship_cents)
RETURNING id AS form_id, order_number AS return_form_order \gset

-- Two more orders stay in picking so the batch print probe has two slips.
INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
VALUES (:'customer_id', :'ship_version', :'ship_code', :'ship_name', :ship_cents)
RETURNING id AS picking_a_id \gset
INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
VALUES (:'customer_id', :'ship_version', :'ship_code', :'ship_name', :ship_cents)
RETURNING id AS picking_b_id \gset

INSERT INTO order_lines (order_id, product_id, variant_id, sku, product_name, variant_label,
                         warranty_note, warranty_months, unit_price_cents, quantity, position)
SELECT l.order_id, p.id, pv.id, pv.sku, p.name,
       (SELECT string_agg(ov.value, ' · ' ORDER BY po.position, po.id)
        FROM variant_option_values vov
        JOIN product_options po ON po.id = vov.option_id
        JOIN product_option_values ov ON ov.id = vov.option_value_id
        WHERE vov.variant_id = pv.id),
       p.warranty_note, p.warranty_months, pv.price_cents, l.quantity, l.position
FROM (VALUES (:'placed_id'::uuid, :'variant_id'::uuid, 1, 0),
             (:'invoice_id', :'variant_id', 1, 0),
             (:'form_id', :'variant_id', 1, 0),
             (:'picking_a_id', :'variant_id', 1, 0),
             (:'picking_b_id', :'variant_id', 1, 0),
             (:'picking_a_id', :'seller_variant_id', 500, 1),
             (:'picking_b_id', :'seller_variant_id', 500, 1)) AS l (order_id, variant_id, quantity, position)
JOIN product_variants pv ON pv.id = l.variant_id
JOIN products p ON p.id = pv.product_id;

-- Checkout's whole hold window (cart.holdTTL): a shorter one renders the pay
-- page's window-closed state, which carries the same marker.
SELECT hold_inventory(order_id, variant_id, quantity, interval '60 minutes',
                      'hold:' || order_id || ':' || variant_id)
FROM order_lines WHERE order_id IN (:'placed_id', :'invoice_id', :'form_id', :'picking_a_id', :'picking_b_id');
INSERT INTO order_events (order_id, kind)
VALUES (:'placed_id', 'placed'), (:'invoice_id', 'placed'), (:'form_id', 'placed'),
       (:'picking_a_id', 'placed'), (:'picking_b_id', 'placed');
SELECT spend_store_credit(id, -order_amount_after_credit(id))
FROM orders WHERE id IN (:'invoice_id', :'form_id', :'picking_a_id', :'picking_b_id');
INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email) VALUES
    (:'placed_id', 'member_carrier', '版面檢查', 'layout@goen.invalid'),
    (:'invoice_id', 'member_carrier', '版面顧客', 'layout-cust@goen.invalid'),
    (:'form_id', 'member_carrier', '版面顧客', 'layout-cust@goen.invalid'),
    (:'picking_a_id', 'member_carrier', 'Layout packer', 'layout-cust@goen.invalid'),
    (:'picking_b_id', 'member_carrier', 'Layout packer', 'layout-cust@goen.invalid');
INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street) VALUES
    (:'placed_id', 'layout@goen.invalid', '版面檢查', '0912345678', '110', '台北市', '信義區', '松高路 1 號'),
    (:'invoice_id', 'layout-cust@goen.invalid', '版面顧客', '0912345678', '110', '台北市', '信義區', '松高路 1 號'),
    (:'form_id', 'layout-cust@goen.invalid', '版面顧客', '0912345678', '110', '台北市', '信義區', '松高路 1 號'),
    (:'picking_a_id', 'layout-cust@goen.invalid', 'Layout packer', '0912345678', '110', '台北市', '信義區', '松高路 1 號'),
    (:'picking_b_id', 'layout-cust@goen.invalid', 'Layout packer', '0912345678', '110', '台北市', '信義區', '松高路 1 號');
-- The placed-order cookie carries this token; the URL carries the number.
INSERT INTO order_access_grants (digest, order_id)
VALUES (sha256(convert_to(:'placed_token', 'UTF8')), :'placed_id');

-- One redemption on a paid order, so the capped coupon's meter has a filled
-- part. The order carries no discount, so the redemption records none.
SELECT redeem_coupon(:'capped_coupon_id', :'invoice_id', :'customer_id', 0);

-- /admin/reports draws its running totals only from seven shop days with paid
-- orders, so the report gets seven, none today. The latest is NT$1,234,567, which
-- gives the chart's end label seven digits to measure at 320. Each is placed and
-- paid as the reports tests pay one: a succeeded payment is what puts a pending
-- order in committed_orders, off the picking queue.
WITH revenue_orders AS (
    INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name,
                        shipping_cents, placed_at)
    SELECT :'customer_id', :'ship_version', :'ship_code', :'ship_name', 0,
           now() - make_interval(days => day_ago)
    FROM generate_series(1, 7) AS day_ago
    RETURNING id, placed_at
), revenue_lines AS (
    INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
    SELECT r.id, 'LAYOUT-REVENUE', 'Revenue chart fixture',
           CASE WHEN r.placed_at = (SELECT max(placed_at) FROM revenue_orders) THEN 123456700 ELSE 3000000 END,
           1
    FROM revenue_orders r
    RETURNING order_id
)
INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
SELECT order_id, 'layout-cust@goen.invalid', '版面顧客', '0912345678', '110', '台北市', '信義區', '松高路 1 號'
FROM revenue_lines;
SELECT open_payment(o.id, 'layout-rev-' || o.id, ol.unit_price_cents)
FROM orders o JOIN order_lines ol ON ol.order_id = o.id
WHERE ol.sku = 'LAYOUT-REVENUE';
SELECT capture_payment('layout-rev-' || o.id, ol.unit_price_cents, NULL, NULL)
FROM orders o JOIN order_lines ol ON ol.order_id = o.id
WHERE ol.sku = 'LAYOUT-REVENUE';

-- /admin/campaigns/layout-campaign draws its columns from day 3 of the campaign
-- (it began three days ago, so this is day 4) and ten units of its listed
-- products across three days with sales, before and during. Four units of its
-- first product on each of the seven days from yesterday back: three during the
-- campaign, four in the four days before it, paid as above.
WITH campaign_orders AS (
    INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name,
                        shipping_cents, placed_at)
    SELECT :'customer_id', :'ship_version', :'ship_code', :'ship_name', 0,
           now() - make_interval(days => day_ago)
    FROM generate_series(1, 7) AS day_ago
    RETURNING id
), campaign_lines AS (
    INSERT INTO order_lines (order_id, product_id, sku, product_name, unit_price_cents, quantity)
    SELECT o.id, (SELECT cp.product_id FROM sale_campaign_products cp
                  JOIN sale_campaigns c ON c.id = cp.campaign_id
                  WHERE c.slug = 'layout-campaign' ORDER BY cp.position LIMIT 1),
           'LAYOUT-CAMPAIGN', 'Campaign results fixture', 1000, 4
    FROM campaign_orders o
    RETURNING order_id
)
INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
SELECT order_id, 'layout-cust@goen.invalid', '版面顧客', '0912345678', '110', '台北市', '信義區', '松高路 1 號'
FROM campaign_lines;
SELECT open_payment(o.id, 'layout-camp-' || o.id, ol.unit_price_cents * ol.quantity)
FROM orders o JOIN order_lines ol ON ol.order_id = o.id
WHERE ol.sku = 'LAYOUT-CAMPAIGN';
SELECT capture_payment('layout-camp-' || o.id, ol.unit_price_cents * ol.quantity, NULL, NULL)
FROM orders o JOIN order_lines ol ON ol.order_id = o.id
WHERE ol.sku = 'LAYOUT-CAMPAIGN';

SET ROLE admin;

-- Picking is what commits a credit-funded order and earns its points.
UPDATE orders SET fulfillment_status = 'picking' WHERE id IN (:'invoice_id', :'form_id', :'picking_a_id', :'picking_b_id');
INSERT INTO order_events (order_id, kind) VALUES (:'invoice_id', 'paid'), (:'form_id', 'paid'), (:'picking_a_id', 'paid'), (:'picking_b_id', 'paid');
SELECT award_loyalty_points(id) FROM orders WHERE id IN (:'invoice_id', :'form_id', :'picking_a_id', :'picking_b_id');
INSERT INTO order_events (order_id, kind, actor_user_id)
VALUES (:'invoice_id', 'picking', :'staff_id'), (:'form_id', 'picking', :'staff_id'),
       (:'picking_a_id', 'picking', :'staff_id'), (:'picking_b_id', 'picking', :'staff_id');
SELECT record_audit_event(:'staff_id', 'order.advance', 'orders', id, NULL,
    jsonb_build_object('number', order_number, 'status', 'picking'))
FROM orders WHERE id IN (:'invoice_id', :'form_id', :'picking_a_id', :'picking_b_id');

-- Tracking numbers are unique per carrier, so each run ships under its own.
INSERT INTO order_shipments (order_id, carrier, tracking_number)
SELECT id, 'black_cat', 'LAYOUT' || translate(order_number, 'GO-', '')
FROM orders WHERE id IN (:'invoice_id', :'form_id');
INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
SELECT s.order_id, s.id, ol.id, ol.quantity
FROM order_shipments s JOIN order_lines ol ON ol.order_id = s.order_id
WHERE s.order_id IN (:'invoice_id', :'form_id');
SELECT consume_reservation_partial(id, quantity) FROM inventory_reservations
WHERE order_id IN (:'invoice_id', :'form_id') AND state = 'held';
UPDATE orders SET fulfillment_status = 'shipped' WHERE id IN (:'invoice_id', :'form_id');
INSERT INTO order_events (order_id, kind, note, actor_user_id)
SELECT order_id, 'shipped', '黑貓宅急便 ' || tracking_number, :'staff_id'
FROM order_shipments WHERE order_id IN (:'invoice_id', :'form_id');
SELECT record_audit_event(:'staff_id', 'order.ship', 'orders', order_id, NULL,
    jsonb_build_object('carrier', carrier, 'tracking', tracking_number))
FROM order_shipments WHERE order_id IN (:'invoice_id', :'form_id');

-- Delivered stamps the parcel too: warranty cover and the 消保法 §19 window run
-- from the parcel's arrival, not from the order's status.
UPDATE orders SET fulfillment_status = 'delivered' WHERE id IN (:'invoice_id', :'form_id');
UPDATE order_shipments SET delivered_at = greatest(now(), shipped_at)
WHERE order_id IN (:'invoice_id', :'form_id') AND delivered_at IS NULL;
INSERT INTO order_events (order_id, kind, actor_user_id)
VALUES (:'invoice_id', 'delivered', :'staff_id'), (:'form_id', 'delivered', :'staff_id');
SELECT record_audit_event(:'staff_id', 'order.advance', 'orders', id, NULL,
    jsonb_build_object('number', order_number, 'status', 'delivered'))
FROM orders WHERE id IN (:'invoice_id', :'form_id');

-- Ten paid orders of one unit on a SKU with twelve sellable and a ledger that
-- starts twenty days back, so the stock section of /admin/reports estimates
-- it: about 24 days, a range that reaches past 30, a warning and the range
-- bar. Placed and funded as the picking orders above are; the receipt covers
-- the ten holds. The seed's receipt is the whole stock the SKU began with, so
-- it moves back twenty days, as seed/demo_history.sql moves it: nothing was
-- there to sell before it.
SELECT pv.id AS estimate_variant_id, pv.stock_quantity AS estimate_stock, pv.safety_stock AS estimate_safety
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock
  AND pv.product_id NOT IN (SELECT product_id FROM product_variants
                            WHERE id IN (:'variant_id', :'seller_variant_id'))
ORDER BY pv.price_cents, pv.sku LIMIT 1 \gset
RESET ROLE;
SET LOCAL session_replication_role = replica;
UPDATE inventory_movements SET created_at = now() - interval '20 days'
WHERE variant_id = :'estimate_variant_id' AND reason = 'receipt' AND idempotency_key LIKE 'seed:%';
SET LOCAL session_replication_role = origin;
SET ROLE admin;
SELECT record_inventory_movement(:'estimate_variant_id', 12 + :estimate_safety - :estimate_stock, 'adjustment',
    'layout-check:' || gen_random_uuid(), 'admin', NULL, :'staff_id')
WHERE :estimate_stock <> 12 + :estimate_safety;
SELECT record_inventory_movement(:'estimate_variant_id', 10, 'receipt',
    'layout-check:' || gen_random_uuid(), 'admin', NULL, :'staff_id');
SELECT grant_store_credit(:'customer_id', pv.price_cents + :ship_cents + 100, 'Estimate fixture', :'staff_id', gen_random_uuid())
FROM product_variants pv, generate_series(1, 10) WHERE pv.id = :'estimate_variant_id';

SET ROLE store;

WITH placed AS (
    INSERT INTO orders (user_id, shipping_version_id, shipping_method_code, shipping_method_name, shipping_cents)
    SELECT :'customer_id', :'ship_version', :'ship_code', :'ship_name', :ship_cents
    FROM generate_series(1, 10)
    RETURNING id
)
SELECT array_agg(id) AS estimate_orders FROM placed \gset
INSERT INTO order_lines (order_id, product_id, variant_id, sku, product_name,
                         warranty_note, warranty_months, unit_price_cents, quantity, position)
SELECT o.id, p.id, pv.id, pv.sku, p.name, p.warranty_note, p.warranty_months, pv.price_cents, 1, 0
FROM unnest(:'estimate_orders'::uuid[]) AS o (id)
JOIN product_variants pv ON pv.id = :'estimate_variant_id'
JOIN products p ON p.id = pv.product_id;
SELECT hold_inventory(order_id, variant_id, quantity, interval '60 minutes',
                      'hold:' || order_id || ':' || variant_id)
FROM order_lines WHERE order_id = ANY (:'estimate_orders'::uuid[]);
INSERT INTO order_events (order_id, kind) SELECT id, 'placed' FROM unnest(:'estimate_orders'::uuid[]) AS o (id);
SELECT spend_store_credit(id, -order_amount_after_credit(id)) FROM orders WHERE id = ANY (:'estimate_orders'::uuid[]);
INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
SELECT id, 'member_carrier', 'Layout packer', 'layout-cust@goen.invalid' FROM unnest(:'estimate_orders'::uuid[]) AS o (id);
INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
SELECT id, 'layout-cust@goen.invalid', 'Layout packer', '0912345678', '110', '台北市', '信義區', '松高路 1 號'
FROM unnest(:'estimate_orders'::uuid[]) AS o (id);

SET ROLE admin;

UPDATE orders SET fulfillment_status = 'picking' WHERE id = ANY (:'estimate_orders'::uuid[]);
INSERT INTO order_events (order_id, kind) SELECT id, 'paid' FROM unnest(:'estimate_orders'::uuid[]) AS o (id);
INSERT INTO order_events (order_id, kind, actor_user_id)
SELECT id, 'picking', :'staff_id' FROM unnest(:'estimate_orders'::uuid[]) AS o (id);

SET ROLE store;

-- Registered before the return, which takes the unit off what may be
-- registered.
INSERT INTO warranty_registrations (order_line_id, unit_no, user_id, serial_number, expires_on)
SELECT ol.id, 1, o.user_id, 'LAYOUTSN' || translate(o.order_number, 'GO-', ''),
       (shop_day(s.delivered_at) + make_interval(months => ol.warranty_months))::date
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
JOIN order_shipment_lines sl ON sl.order_line_id = ol.id
JOIN order_shipments s ON s.id = sl.shipment_id
WHERE ol.order_id = :'invoice_id'
RETURNING serial_number AS layout_serial \gset

-- RETURN_FORM_ORDER is delivered with nothing returned: its order page draws the right-to-cancel track and states
-- the registered warranty's end.
INSERT INTO warranty_registrations (order_line_id, unit_no, user_id, serial_number, expires_on)
SELECT ol.id, 1, o.user_id, 'LAYOUTSN' || translate(o.order_number, 'GO-', ''),
       (shop_day(s.delivered_at) + make_interval(months => ol.warranty_months))::date
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
JOIN order_shipment_lines sl ON sl.order_line_id = ol.id
JOIN order_shipments s ON s.id = sl.shipment_id
WHERE ol.order_id = :'form_id' AND ol.warranty_months IS NOT NULL;

INSERT INTO return_requests (order_id, requested_by_user_id, reason)
VALUES (:'invoice_id', :'customer_id', '尺寸不合，想換一個顏色')
RETURNING id AS return_id \gset
INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
SELECT order_id, :'return_id', id, quantity FROM order_lines WHERE order_id = :'invoice_id';

SET ROLE admin;

-- The decision, then the payout in admin/refunds' order: the credit, the
-- refunded event, the points clawback. Short of all three the return stays an
-- outstanding payout and /admin/returns measures that state instead.
UPDATE return_requests SET status = 'approved', resolution = '版面檢查同意退貨', decided_at = now()
WHERE id = :'return_id' AND status = 'requested';
SELECT record_audit_event(:'staff_id', 'return.decide', 'return_requests', :'return_id', NULL,
    jsonb_build_object('decision', 'approved', 'resolution', '版面檢查同意退貨',
                       'policy_window', 'within', 'entitlement', 'statutory'));
SELECT compensate_return_with_credit(id, credit_refund_cents, :'staff_id')
FROM return_requests WHERE id = :'return_id';
INSERT INTO order_events (order_id, kind, actor_user_id, return_request_id)
VALUES (:'invoice_id', 'refunded', :'staff_id', :'return_id');
SELECT reverse_return_points(:'return_id');
SELECT return_payout_outstanding(:'return_id') AS payout_outstanding \gset
\if :payout_outstanding
DO $$ BEGIN RAISE EXCEPTION 'the layout return is approved but its payout is incomplete'; END $$;
\endif

-- A second product with returns, so /admin/reports draws the returned-products
-- bars rather than the one-sentence state: a quarter of the second best seller's
-- 1,000 units, the row with the longest text on the page. The parcel never
-- shipped, so the rows go in with triggers off, completed with nothing owed.
RESET ROLE;
SET LOCAL session_replication_role = replica;
INSERT INTO return_requests (order_id, requested_by_user_id, reason, status, decided_at,
                             goods_refund_cents, card_refund_cents, credit_refund_cents)
VALUES (:'picking_a_id', :'customer_id', '版面檢查：退貨商品列', 'completed', now(), 0, 0, 0)
RETURNING id AS returned_row_id \gset
INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
SELECT order_id, :'returned_row_id', id, 250 FROM order_lines
WHERE order_id = :'picking_a_id' AND variant_id = :'seller_variant_id';
SET LOCAL session_replication_role = origin;
SET ROLE admin;

-- The invoice on INVOICE_ORDER, filed as the invoice worker files one: claim,
-- lease, send, settle with the provider's number. The doors answer a refusal
-- with false or the zero uuid rather than an error, so each is read through a
-- WHERE whose missing row stops psql.
SELECT gen_random_uuid() AS invoice_worker \gset
SELECT claim_invoice_issue(:'invoice_order', :'staff_id', 'layout-check:' || :'invoice_order') AS issue_op \gset
SELECT true AS issue_leased
WHERE lease_invoice_operation(:'issue_op', :'invoice_worker', interval '5 minutes') = :'issue_op' \gset
SELECT true AS issue_sent WHERE mark_invoice_operation_sent(:'issue_op', :'invoice_worker') \gset
SELECT document_id AS invoice_document
FROM (
    SELECT settle_invoice_issue(
               :'issue_op', :'invoice_worker',
               'LC' || lpad(floor(random() * 100000000)::bigint::text, 8, '0'),
               lpad(floor(random() * 10000)::integer::text, 4, '0'), now(),
               array_agg(l ->> 'description' ORDER BY n),
               array_agg((l ->> 'quantity')::integer ORDER BY n),
               array_agg((l ->> 'unit_price_cents')::bigint ORDER BY n),
               array_agg((l ->> 'amount_cents')::bigint ORDER BY n)) AS document_id
    FROM invoice_operations op
    CROSS JOIN LATERAL jsonb_array_elements(op.request_payload -> 'lines') WITH ORDINALITY AS t(l, n)
    WHERE op.id = :'issue_op'
) settled
WHERE document_id <> '00000000-0000-0000-0000-000000000000' \gset

-- A 折讓 the provider has not answered, on /admin/health's alarm table. Alarmed
-- rather than left pending, because a pending claim is listed only once it is
-- 15 minutes old and no door backdates one. The 折讓 form on
-- /admin/orders/INVOICE_ORDER still renders: it reads issued documents, not
-- claims.
SELECT claim_invoice_allowance(:'invoice_document', gen_random_uuid(), :'staff_id',
                               'layout-check-allowance:' || :'invoice_order') AS allowance_op \gset
SELECT true AS allowance_leased
WHERE lease_invoice_operation(:'allowance_op', :'invoice_worker', interval '5 minutes') = :'allowance_op' \gset
SELECT true AS allowance_sent WHERE mark_invoice_operation_sent(:'allowance_op', :'invoice_worker') \gset
SELECT true AS allowance_alarmed
WHERE alarm_invoice_operation(:'allowance_op', :'invoice_worker', 'allowance_multiple_unknown_candidates') \gset

SELECT v.id AS pickup_ship FROM shipping_method_versions v
JOIN shipping_methods sm ON sm.id = v.method_id
WHERE sm.code = 'store_pickup' ORDER BY v.effective_at DESC LIMIT 1 \gset

COMMIT;

\o :env
\qecho ADMIN_TOKEN=:admin_token
\qecho CUST_TOKEN=:cust_token
\qecho CART_TOKEN=:cart_token
\qecho PLACED_TOKEN=:placed_token
\qecho PLACED_ORDER=:placed_order
\qecho INVOICE_ORDER=:invoice_order
\qecho RETURN_FORM_ORDER=:return_form_order
\qecho CUSTOMER_ID=:customer_id
\qecho LAYOUT_SERIAL=:layout_serial
\qecho PRODUCT_SLUG=:product_slug
\qecho REVIEW_SLUG=:review_slug
\qecho COMPARE_SLUG=:compare_slug
\qecho PICKUP_SHIP=:pickup_ship
\o
