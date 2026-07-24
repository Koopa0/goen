//go:build integration

package db_test

// fixtures are the rows every case hangs off: one of everything the catalogue,
// order and payment graphs need, with fixed ids so a case can reference them
// without first creating its own world.
//
// Ids are readable on purpose. When a failure prints
// `55555555-5555-4555-8555-555555555555` it should be obvious that the user is
// meant, not that some opaque value leaked.
const fixtures = `
-- catalogue
INSERT INTO brands (id, slug, name) VALUES
    ('11111111-1111-4111-8111-111111111111', 'pixelight', 'Pixelight');

INSERT INTO categories (id, slug, name) VALUES
    ('22222222-2222-4222-8222-222222222222', 'phones', '手機'),
    ('2222aaaa-2222-4222-8222-222222222222', 'laptops', '筆電');

INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at) VALUES
    ('33333333-3333-4333-8333-333333333333',
     '11111111-1111-4111-8111-111111111111',
     '22222222-2222-4222-8222-222222222222',
     'pixelight-9-pro', 'Pixelight 9 Pro 5G', 'active', now());

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

INSERT INTO store_credit_accounts (user_id) VALUES
    ('55555555-5555-4555-8555-555555555555');

INSERT INTO store_credit_entries (user_id, amount_cents, reason, idempotency_key) VALUES
    ('55555555-5555-4555-8555-555555555555', 100000, 'signup', 'fixture-grant');

INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES
    ('eeee0001-0000-4000-8000-000000000000', '55555555-5555-4555-8555-555555555555',
     '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', true);

-- shipping
INSERT INTO shipping_methods (id, code) VALUES
    ('ffff0001-0000-4000-8000-000000000000', 'home_delivery');

INSERT INTO shipping_method_versions (id, method_id, name, carrier, fee_cents, free_over_cents) VALUES
    ('ffff0002-0000-4000-8000-000000000000', 'ffff0001-0000-4000-8000-000000000000',
     '宅配到府(黑貓)', '黑貓宅急便', 8000, 300000);

-- one complete order: lines and delivery details included, because the
-- deferred trigger refuses an order that has neither
INSERT INTO orders (id, order_number, user_id, shipping_version_id,
                    shipping_method_code, shipping_method_name, shipping_cents) VALUES
    ('66666666-6666-4666-8666-666666666666', 'GO-260721-000387',
     '55555555-5555-4555-8555-555555555555', 'ffff0002-0000-4000-8000-000000000000',
     'home_delivery', '宅配到府(黑貓)', 8000);

INSERT INTO order_lines (id, order_id, variant_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('66660001-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     '44444444-4444-4444-8444-444444444444', 'PXL-9P-256-BL',
     'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 2, 0);

INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('66666666-6666-4666-8666-666666666666', 'ming@example.com', '王小明', '0912345678',
     '110', '台北市', '信義區', '松高路 68 號');

INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES
    ('66660002-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     '黑貓宅急便', '903-2214-8871');

-- a captured payment, so refund cases have something to refund
INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents,
                      captured_amount_cents, paid_at) VALUES
    ('77770001-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     'pi_fixture', 'succeeded', 6788000, 6788000, now());

-- a second order still awaiting payment, for cases that must not run against a
-- settled one
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name) VALUES
    ('6666aaaa-6666-4666-8666-666666666666', 'GO-260721-000388', 'home_delivery', '宅配到府');

INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES
    ('6666a001-0000-4000-8000-000000000000', '6666aaaa-6666-4666-8666-666666666666',
     'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 0);

INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('6666aaaa-6666-4666-8666-666666666666', 'guest@example.com', '訪客', '0900000000',
     '110', '台北市', '信義區', '松高路 1 號');

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

-- Force the deferred order-completeness check to run against the fixtures
-- themselves, so a broken fixture is reported here rather than surfacing as a
-- confusing failure inside somebody's case.
--
-- Then put it back: SET CONSTRAINTS changes the mode for the REST of the
-- transaction, so leaving it immediate would make every later order fail the
-- moment it is inserted — before the lines that complete it can exist.
SET CONSTRAINTS orders_have_lines IMMEDIATE;
SET CONSTRAINTS orders_have_lines DEFERRED;
`
