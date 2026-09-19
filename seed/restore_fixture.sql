-- Representative commerce state for restore-drill business checks.
-- Loads after migrations on an owned database. No customer secrets appear in
-- the business manifest; rows here exist only to exercise restore oracles.
BEGIN;

-- Catalogue anchor from the schema conformance fixtures.
INSERT INTO brands (id, slug, name) VALUES
    ('11111111-1111-4111-8111-111111111111', 'pixelight', 'Pixelight')
ON CONFLICT (id) DO NOTHING;

INSERT INTO categories (id, slug, name, position) VALUES
    ('22222222-2222-4222-8222-222222222222', 'phones', '手機', 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at) VALUES
    ('33333333-3333-4333-8333-333333333333',
     '11111111-1111-4111-8111-111111111111',
     '22222222-2222-4222-8222-222222222222',
     'pixelight-9-pro', 'Pixelight 9 Pro 5G', 'active', now())
ON CONFLICT (id) DO NOTHING;

INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, safety_stock, position) VALUES
    ('44444444-4444-4444-8444-444444444444',
     '33333333-3333-4333-8333-333333333333', 'PXL-9P-256-BL', 3390000, 14, 2, 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, email, full_name) VALUES
    ('55555555-5555-4555-8555-555555555555', 'restore@example.com', '還原測試')
ON CONFLICT (id) DO NOTHING;

INSERT INTO shipping_methods (id, code) VALUES
    ('ffff0001-0000-4000-8000-000000000000', 'home_delivery')
ON CONFLICT (id) DO NOTHING;

INSERT INTO shipping_method_versions (id, method_id, name, carrier, fee_cents, free_over_cents) VALUES
    ('ffff0002-0000-4000-8000-000000000000', 'ffff0001-0000-4000-8000-000000000000',
     '宅配到府(黑貓)', '黑貓宅急便', 8000, 300000)
ON CONFLICT (id) DO NOTHING;

-- Stored image bytes: digest must match sha256(bytes) for the manifest oracle.
WITH img AS (
    SELECT decode('010203726573746f7265', 'hex') AS bytes,
           encode(sha256(decode('010203726573746f7265', 'hex')), 'hex') AS digest
)
INSERT INTO media_objects (digest, content_type, byte_size, width, height, bytes)
SELECT digest, 'image/png', octet_length(bytes), 10, 10, bytes
FROM img
ON CONFLICT (digest) DO NOTHING;

INSERT INTO product_images (id, product_id, storage_key, alt_text, position)
SELECT 'dddd0001-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333',
       encode(sha256(decode('010203726573746f7265', 'hex')), 'hex'), '還原測試商品圖', 0
WHERE NOT EXISTS (
    SELECT 1 FROM product_images WHERE id = 'dddd0001-0000-4000-8000-000000000000'
);

-- Paid and shipped order.
INSERT INTO orders (id, order_number, user_id, shipping_version_id,
                    shipping_method_code, shipping_method_name, shipping_cents) VALUES
    ('66666666-6666-4666-8666-666666666666', 'GO-260914-000001',
     '55555555-5555-4555-8555-555555555555', 'ffff0002-0000-4000-8000-000000000000',
     'home_delivery', '宅配到府(黑貓)', 8000)
ON CONFLICT (id) DO NOTHING;

INSERT INTO order_lines (id, order_id, variant_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('66660001-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     '44444444-4444-4444-8444-444444444444', 'PXL-9P-256-BL',
     'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('66666666-6666-4666-8666-666666666666', 'restore-paid@example.com', '還原付費', '0912000001',
     '110', '台北市', '信義區', '松高路 1 號')
ON CONFLICT (order_id) DO NOTHING;

INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents,
                      captured_amount_cents, paid_at) VALUES
    ('77770001-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     'pi_restore_paid', 'succeeded', 3398000, 3398000, now())
ON CONFLICT (id) DO NOTHING;

UPDATE orders SET fulfillment_status = 'picking'
WHERE id = '66666666-6666-4666-8666-666666666666';

INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES
    ('66660002-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666',
     '黑貓宅急便', '903-RESTORE-001')
ON CONFLICT (id) DO NOTHING;

INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity) VALUES
    ('66666666-6666-4666-8666-666666666666', '66660002-0000-4000-8000-000000000000',
     '66660001-0000-4000-8000-000000000000', 1)
ON CONFLICT DO NOTHING;

UPDATE orders SET fulfillment_status = 'shipped'
WHERE id = '66666666-6666-4666-8666-666666666666';

INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, state, expires_at, settled_at)
VALUES ('88880001-0000-4000-8000-000000000001',
        '66666666-6666-4666-8666-666666666666',
        '44444444-4444-4444-8444-444444444444', 1, 'consumed', now() + interval '1 hour', now())
ON CONFLICT DO NOTHING;

-- Unpaid order with an active hold.
INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name) VALUES
    ('6666aaaa-6666-4666-8666-666666666666', 'GO-260914-000002',
     'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府')
ON CONFLICT (id) DO NOTHING;

INSERT INTO order_lines (id, order_id, variant_id, sku, product_name, unit_price_cents, quantity, position) VALUES
    ('6666a001-0000-4000-8000-000000000000', '6666aaaa-6666-4666-8666-666666666666',
     '44444444-4444-4444-8444-444444444444', 'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', 3390000, 1, 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('6666aaaa-6666-4666-8666-666666666666', 'restore-unpaid@example.com', '還原未付', '0912000002',
     '110', '台北市', '信義區', '松高路 2 號')
ON CONFLICT (order_id) DO NOTHING;

INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, state, expires_at)
VALUES ('88880002-0000-4000-8000-000000000002',
        '6666aaaa-6666-4666-8666-666666666666',
        '44444444-4444-4444-8444-444444444444', 1, 'held', now() + interval '30 minutes')
ON CONFLICT DO NOTHING;

-- Expired checkout: payment row cancelled after Stripe confirmed expiry.
INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name) VALUES
    ('6666bbbb-6666-4666-8666-666666666666', 'GO-260914-000003',
     'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府')
ON CONFLICT (id) DO NOTHING;

INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES
    ('6666b001-0000-4000-8000-000000000000', '6666bbbb-6666-4666-8666-666666666666',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', 3390000, 1, 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('6666bbbb-6666-4666-8666-666666666666', 'restore-expired@example.com', '還原過期', '0912000003',
     '110', '台北市', '信義區', '松高路 3 號')
ON CONFLICT (order_id) DO NOTHING;

INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES
    ('77770002-0000-4000-8000-000000000000', '6666bbbb-6666-4666-8666-666666666666',
     'cs_restore_expired', 'cancelled', 3398000)
ON CONFLICT (id) DO NOTHING;

UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
WHERE id = '6666bbbb-6666-4666-8666-666666666666';

INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, state, created_at, expires_at, settled_at)
VALUES ('88880003-0000-4000-8000-000000000003',
        '6666bbbb-6666-4666-8666-666666666666',
        '44444444-4444-4444-8444-444444444444', 1, 'released',
        now() - interval '2 hours', now() - interval '1 hour', now())
ON CONFLICT DO NOTHING;

-- Open return still awaiting staff decision.
INSERT INTO return_requests (id, order_id, reason) VALUES
    ('99990001-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '還原部分退')
ON CONFLICT (id) DO NOTHING;

INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity) VALUES
    ('66666666-6666-4666-8666-666666666666', '99990001-0000-4000-8000-000000000001',
     '66660001-0000-4000-8000-000000000000', 1)
ON CONFLICT DO NOTHING;

-- Partial capture refund recorded outside a return claim.
INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, provider_ref, succeeded_at) VALUES
    ('aaaa0001-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
     'restore-partial-refund', 'succeeded', 500000, 're_restore_partial', now())
ON CONFLICT (id) DO NOTHING;

-- Reconciliation still open on a second payment shape.
INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name) VALUES
    ('6666cccc-6666-4666-8666-666666666666', 'GO-260914-000004',
     'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府')
ON CONFLICT (id) DO NOTHING;

INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES
    ('6666c001-0000-4000-8000-000000000000', '6666cccc-6666-4666-8666-666666666666',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', 1200000, 1, 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('6666cccc-6666-4666-8666-666666666666', 'restore-recon@example.com', '還原對帳', '0912000004',
     '110', '台北市', '信義區', '松高路 4 號')
ON CONFLICT (order_id) DO NOTHING;

INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES
    ('77770003-0000-4000-8000-000000000000', '6666cccc-6666-4666-8666-666666666666',
     'pi_restore_recon', 'requires_reconciliation', 1208000)
ON CONFLICT (id) DO NOTHING;

-- Pending and completed asynchronous work.
INSERT INTO outbox_messages (id, topic, dedupe_key, payload, attempts, available_at, delivered_at) VALUES
    ('bbbb0001-0000-4000-8000-000000000001', 'order.paid', 'restore-delivered',
     '{"order_number":"GO-260914-000001"}'::jsonb, 1, now() - interval '1 hour', now() - interval '59 minutes'),
    ('bbbb0002-0000-4000-8000-000000000002', 'order.shipped', 'restore-pending',
     '{"locale":"zh-Hant","order_number":"GO-260914-000001","email":"restore-paid@example.com","name":"還原付費","carrier":"黑貓宅急便","tracking":"903-RESTORE-001"}'::jsonb,
     0, now(), NULL)
ON CONFLICT (id) DO NOTHING;

INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at) VALUES
    ('3333aaaa-3333-4333-8333-333333333333',
     '11111111-1111-4111-8111-111111111111',
     '22222222-2222-4222-8222-222222222222',
     'pixelight-9', 'Pixelight 9 5G', 'draft', NULL)
ON CONFLICT (id) DO NOTHING;

-- Stale recommendation projection to rebuild after restart.
INSERT INTO product_copurchases (product_id, other_product_id, orders) VALUES
    ('33333333-3333-4333-8333-333333333333', '3333aaaa-3333-4333-8333-333333333333', 2)
ON CONFLICT DO NOTHING;

SET CONSTRAINTS orders_have_lines IMMEDIATE;
SET CONSTRAINTS orders_have_lines DEFERRED;

COMMIT;
