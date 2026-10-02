BEGIN;

-- Two small slips make a missing page break visible in the printed PDF.
DO $$
DECLARE
    n integer;
    order_id uuid;
BEGIN
    FOR n IN 1..2 LOOP
        INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
                            shipping_method_name, shipping_cents)
        SELECT next_order_number(), v.id, sm.code, v.name, 0
        FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
        ORDER BY v.effective_at LIMIT 1
        RETURNING id INTO order_id;

        INSERT INTO order_lines (order_id, sku, product_name, variant_label, unit_price_cents, quantity)
        VALUES (order_id, 'LAYOUT-PICK', 'Layout picking fixture', '256 GB', 0, 1);

        INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
        VALUES (order_id, 'picking@goen.invalid', 'Layout recipient', '0912345678', '110', '臺北市', '信義區', '測試路 1 號');

        UPDATE orders SET fulfillment_status = 'picking' WHERE id = order_id;
    END LOOP;
END;
$$;

COMMIT;
