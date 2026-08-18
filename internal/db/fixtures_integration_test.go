//go:build integration

package db_test

// fixtures are the rows every case hangs off, with fixed ids so a case can reference them.
const fixtures = `
-- catalogue
INSERT INTO brands (id, slug, name) VALUES
    ('11111111-1111-4111-8111-111111111111', 'pixelight', 'Pixelight');

-- Positions stated rather than defaulted: both are roots, and
-- categories_position_key is NULLS NOT DISTINCT, so two rows taking the
-- DEFAULT 0 are the collision the index exists to refuse.
INSERT INTO categories (id, slug, name, position) VALUES
    ('22222222-2222-4222-8222-222222222222', 'phones', '手機', 0),
    ('2222aaaa-2222-4222-8222-222222222222', 'laptops', '筆電', 1);

INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at) VALUES
    ('33333333-3333-4333-8333-333333333333',
     '11111111-1111-4111-8111-111111111111',
     '22222222-2222-4222-8222-222222222222',
     'pixelight-9-pro', 'Pixelight 9 Pro 5G', 'active', now()),
    -- A SECOND product, in draft so it needs no variant: with one product a pair-shaped
    -- rule's cross join is empty, and the case reports an accepted row it never wrote.
    ('3333aaaa-3333-4333-8333-333333333333',
     '11111111-1111-4111-8111-111111111111',
     '22222222-2222-4222-8222-222222222222',
     'pixelight-9', 'Pixelight 9 5G', 'draft', NULL);

INSERT INTO product_options (id, product_id, name) VALUES
    ('aaaa0001-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333', '顏色'),
    ('aaaa0002-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333', '容量');

INSERT INTO product_option_values (id, product_id, option_id, value) VALUES
    ('bbbb0001-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333',
     'aaaa0001-0000-4000-8000-000000000000', '星霧藍'),
    ('bbbb0002-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333',
     'aaaa0001-0000-4000-8000-000000000000', '曜石黑'),
    ('bbbb0003-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333',
     'aaaa0002-0000-4000-8000-000000000000', '256GB');

INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, safety_stock, position) VALUES
    ('44444444-4444-4444-8444-444444444444',
     '33333333-3333-4333-8333-333333333333', 'PXL-9P-256-BL', 3390000, 14, 2, 0),
    ('4444aaaa-4444-4444-8444-444444444444',
     '33333333-3333-4333-8333-333333333333', 'PXL-9P-512-BL', 3690000, 6, 2, 1);

INSERT INTO variant_option_values (product_id, variant_id, option_id, option_value_id) VALUES
    ('33333333-3333-4333-8333-333333333333', '44444444-4444-4444-8444-444444444444',
     'aaaa0001-0000-4000-8000-000000000000', 'bbbb0001-0000-4000-8000-000000000000');

INSERT INTO product_specs (id, product_id, label, value, position) VALUES
    ('cccc0001-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333',
     '螢幕', '6.7" LTPO OLED', 0);

INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES
    ('dddd0001-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333',
     'pxl-9p-front.webp', 'Pixelight 9 Pro 星霧藍正面', 0);

-- people
INSERT INTO users (id, email, full_name) VALUES
    ('55555555-5555-4555-8555-555555555555', 'Ming@Example.com', '王小明'),
    ('5555aaaa-5555-4555-8555-555555555555', 'hua@example.com', '李大華');

-- The two tables that key on the ADDRESS and not on the account, so a DELETE of a user
-- cannot reach them. Without both the erasure guard probes an empty schema and cannot fail.
INSERT INTO contact_messages (name, email, subject, message) VALUES
    ('王小明', 'Ming@Example.com', '出貨進度',
     '我的地址是台北市信義區松高路 1 號,電話 0912345678,想問訂單什麼時候出貨');

INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES
    ('Ming@Example.com', 'fixture-unsubscribe-token-000000000000');

INSERT INTO store_credit_accounts (id, user_id) VALUES
    ('a0000001-0000-4000-8000-000000000000', '55555555-5555-4555-8555-555555555555');

INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key) VALUES
    ('a0000001-0000-4000-8000-000000000000', 100000, 'signup', 'fixture-grant');

INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES
    ('eeee0001-0000-4000-8000-000000000000', '55555555-5555-4555-8555-555555555555',
     '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', true);

-- shipping
INSERT INTO shipping_methods (id, code) VALUES
    ('ffff0001-0000-4000-8000-000000000000', 'home_delivery');

INSERT INTO shipping_method_versions (id, method_id, name, carrier, fee_cents, free_over_cents) VALUES
    ('ffff0002-0000-4000-8000-000000000000', 'ffff0001-0000-4000-8000-000000000000',
     '宅配到府(黑貓)', '黑貓宅急便', 8000, 300000);

-- One complete order: the deferred trigger refuses an order with no lines and no delivery details.
INSERT INTO orders (id, order_number, user_id, shipping_version_id,
                    shipping_method_code, shipping_method_name, shipping_cents) VALUES
    ('66666666-6666-4666-8666-666666666666', 'GO-260721-000387',
     '55555555-5555-4555-8555-555555555555', 'ffff0002-0000-4000-8000-000000000000',
     'home_delivery', '宅配到府(黑貓)', 8000);

INSERT INTO order_lines (id, order_id, variant_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('66660001-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     '44444444-4444-4444-8444-444444444444', 'PXL-9P-256-BL',
     'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 2, 0),
    -- A SECOND line, the one the shipment below covers: one line cannot be both shipped and
    -- unshipped. Deliberately cheap — every capture must equal the order total exactly
    -- (payments_capture_matches_order), which refunds_within_capture's cases are calibrated to.
    ('66660003-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     '44444444-4444-4444-8444-444444444444', 'PXL-9P-256-BK',
     '保護貼', '9H 鋼化', 1000, 2, 1);

INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('66666666-6666-4666-8666-666666666666', 'ming@example.com', '王小明', '0912345678',
     '110', '台北市', '信義區', '松高路 68 號');

INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES
    ('66660002-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     '黑貓宅急便', '903-2214-8871');

-- What was in that parcel, and ONE of the two deliberately: a line shipped in full cannot tell
-- return_within_shipment from shipment_within_purchase, so the return cases pass either way.
INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity) VALUES
    ('66666666-6666-4666-8666-666666666666', '66660002-0000-4000-8000-000000000000',
     '66660003-0000-4000-8000-000000000000', 1);

-- a captured payment, so refund cases have something to refund
INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents,
                      captured_amount_cents, paid_at) VALUES
    ('77770001-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     -- 3390000*2 + 1000*2 + 8000 shipping: payments_capture_matches_order compares to the cent.
     'pi_fixture', 'succeeded', 6790000, 6790000, now());

-- A second order still awaiting payment, for cases that must not run against a settled one.
INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name) VALUES
    ('6666aaaa-6666-4666-8666-666666666666', 'GO-260721-000388',
     'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');

INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES
    ('6666a001-0000-4000-8000-000000000000', '6666aaaa-6666-4666-8666-666666666666',
     'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 0);

INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('6666aaaa-6666-4666-8666-666666666666', 'guest@example.com', '訪客', '0900000000',
     '110', '台北市', '信義區', '松高路 1 號');

-- A zero-owed order: a 100% discount funds it, so it leaves pending legally with no payment row.
INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code,
                    shipping_method_name, discount_cents) VALUES
    ('6666bbbb-6666-4666-8666-666666666666', 'GO-260721-000389',
     'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府', 100000);

INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES
    ('6666b001-0000-4000-8000-000000000000', '6666bbbb-6666-4666-8666-666666666666',
     'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 100000, 1, 0);

INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('6666bbbb-6666-4666-8666-666666666666', 'free@example.com', '免單', '0900000001',
     '110', '台北市', '信義區', '松高路 1 號');

-- Out of pending through the legal door: orders_funded_to_leave_pending skips its payment
-- check at zero owed, and the UPDATE proves the state is reachable rather than inserted.
UPDATE orders SET fulfillment_status = 'picking'
WHERE id = '6666bbbb-6666-4666-8666-666666666666';

-- An invoice carrier is a personal identifier, and it outlived erasure until erase_user
-- learned to drop it.
INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code) VALUES
    ('66666666-6666-4666-8666-666666666666', 'mobile_carrier', '/ABC+123');

INSERT INTO return_requests (id, order_id, reason) VALUES
    ('88880001-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666', '不合用');

INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES
    ('99990001-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     'invoice', 'GD-72031288', 6788000);

INSERT INTO sale_campaigns (id, slug, title, ends_at) VALUES
    ('aaaa1111-0000-4000-8000-000000000000', 'summer', '夏季開學祭', now() + interval '7 days');

INSERT INTO carts (id, token_hash) VALUES
    ('bbbb1111-0000-4000-8000-000000000000', '\x0102');

INSERT INTO faq_entries (id, category, question, answer, position) VALUES
    ('cccc1111-0000-4000-8000-000000000000', '運送', '多久到貨?', '兩個工作天。', 0);

INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position) VALUES
    ('dddd1111-0000-4000-8000-000000000000', 'Pixelight 9 系列', '立即選購', '/c/phones', 0);

INSERT INTO promo_banners (id, message) VALUES
    ('eeee1111-0000-4000-8000-000000000000', '全站滿 NT$3,000 免運');

-- A coupon and a redemption to collide with. The redemption's amount matches the paid fixture
-- order's discount_cents (0), as coupon_redemption_matches_order requires.
INSERT INTO coupons (id, code, description, kind, amount_cents) VALUES
    ('cccc0009-0000-4000-8000-000000000009', 'FIXTURECODE', '固定金額測試', 'amount', 20000);

INSERT INTO coupon_redemptions (id, coupon_id, order_id, amount_cents) VALUES
    ('cccc000a-0000-4000-8000-00000000000a', 'cccc0009-0000-4000-8000-000000000009',
     '66666666-6666-4666-8666-666666666666', 0);

-- Run the deferred completeness check against the fixtures themselves, then put it back:
-- SET CONSTRAINTS changes the mode for the REST of the transaction, and leaving it immediate
-- would fail every later order at insert, before the lines completing it can exist.
SET CONSTRAINTS orders_have_lines IMMEDIATE;
SET CONSTRAINTS orders_have_lines DEFERRED;
`
