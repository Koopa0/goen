-- Load harness catalogue — pinned fixture for #333. Development only.
-- Identity: load-catalog-v1, seed 7292. Do not hand-edit without bumping LOAD_FIXTURE_ID.
BEGIN;

INSERT INTO brands (id, slug, name) VALUES
    ('a3330001-0000-4000-8000-000000000001', 'load-brand', 'Load Brand')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO categories (id, parent_id, slug, name, name_en, icon_key, position) VALUES
    ('a3330002-0000-4000-8000-000000000002', NULL, 'load-phones', '負載手機', 'Load phones', 'phone', 0)
ON CONFLICT (slug) DO NOTHING;

-- Hot product: skewed browse traffic lands here.
INSERT INTO products (id, brand_id, category_id, slug, name, summary, status, published_at, name_en, summary_en) VALUES
    ('a3330003-0000-4000-8000-000000000003',
     'a3330001-0000-4000-8000-000000000001',
     'a3330002-0000-4000-8000-000000000002',
     'load-hot-phone', 'Load Hot Phone', 'Skewed browse target.', 'active', now(),
     'Load Hot Phone', 'Skewed browse target.')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, is_active) VALUES
    ('a3330004-0000-4000-8000-000000000004',
     'a3330003-0000-4000-8000-000000000003',
     'LOAD-HOT-001', 2990000, 0, true)
ON CONFLICT (sku) DO NOTHING;

-- Flash sale: three sellable units (stock 3, safety 0). Stock-contention oracle.
INSERT INTO products (id, brand_id, category_id, slug, name, summary, status, published_at, name_en, summary_en) VALUES
    ('a3330005-0000-4000-8000-000000000005',
     'a3330001-0000-4000-8000-000000000001',
     'a3330002-0000-4000-8000-000000000002',
     'load-flash-sale', 'Load Flash Sale', 'Limited units.', 'active', now(),
     'Load Flash Sale', 'Limited units.')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, is_active) VALUES
    ('a3330006-0000-4000-8000-000000000006',
     'a3330005-0000-4000-8000-000000000005',
     'LOAD-FLASH-001', 99000, 0, true)
ON CONFLICT (sku) DO NOTHING;

-- Filler catalogue rows for search and category listing.
INSERT INTO products (id, brand_id, category_id, slug, name, summary, status, published_at, name_en, summary_en)
SELECT
    ('a3330010-0000-4000-8000-0000000000' || lpad(n::text, 2, '0'))::uuid,
    'a3330001-0000-4000-8000-000000000001',
    'a3330002-0000-4000-8000-000000000002',
    'load-filler-' || n,
    'Load Filler ' || n,
    'Catalogue padding.',
    'active',
    now(),
    'Load Filler ' || n,
    'Catalogue padding.'
FROM generate_series(1, 8) AS n
ON CONFLICT (slug) DO NOTHING;

INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, is_active)
SELECT
    ('a3330020-0000-4000-8000-0000000000' || lpad(n::text, 2, '0'))::uuid,
    ('a3330010-0000-4000-8000-0000000000' || lpad(n::text, 2, '0'))::uuid,
    'LOAD-FILL-' || lpad(n::text, 3, '0'),
    100000 + n * 1000,
    0,
    true
FROM generate_series(1, 8) AS n
ON CONFLICT (sku) DO NOTHING;

INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
SELECT v.id, m.qty, 'receipt', 'load-seed:' || v.sku
FROM (VALUES
    ('LOAD-HOT-001', 500),
    ('LOAD-FLASH-001', 3),
    ('LOAD-FILL-001', 50),
    ('LOAD-FILL-002', 50),
    ('LOAD-FILL-003', 50),
    ('LOAD-FILL-004', 50),
    ('LOAD-FILL-005', 50),
    ('LOAD-FILL-006', 50),
    ('LOAD-FILL-007', 50),
    ('LOAD-FILL-008', 50)
) AS m(sku, qty)
JOIN product_variants v ON v.sku = m.sku;

INSERT INTO shipping_methods (id, code, destination_kind, position) VALUES
    ('a33300f1-0000-4000-8000-0000000000f1', 'home_delivery', 'address', 0),
    ('a33300f2-0000-4000-8000-0000000000f2', 'store_pickup', 'pickup_point', 1)
ON CONFLICT (code) DO NOTHING;

INSERT INTO shipping_method_versions (id, method_id, name, carrier, name_en, carrier_en, fee_cents, free_over_cents) VALUES
    ('a33300f3-0000-4000-8000-0000000000f3', 'a33300f1-0000-4000-8000-0000000000f1', '宅配到府', '黑貓宅急便', 'Home delivery', 'T-Cat', 8000, 300000),
    ('a33300f4-0000-4000-8000-0000000000f4', 'a33300f2-0000-4000-8000-0000000000f2', '超商取貨', NULL, 'Convenience store pickup', NULL, 6000, 300000)
ON CONFLICT (id) DO NOTHING;

COMMIT;
