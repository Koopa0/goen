-- Probe only: a SKU at about 25 days whose range reaches past 30, and two sold-out SKUs with a day.
DO $$
DECLARE
    v_staff uuid := (SELECT id FROM users WHERE lower(email) = 'layout-check@goen.invalid' AND role = 'admin');
    v record;
BEGIN
    SET ROLE admin;
    SELECT id, stock_quantity - safety_stock AS sellable INTO v FROM product_variants WHERE sku = 'FRN-TEA-1';
    PERFORM record_inventory_movement(v.id, -(v.sellable - 9), 'adjustment', 'probe:tea', 'admin', NULL, v_staff);
    FOR v IN SELECT id, stock_quantity - safety_stock AS sellable FROM product_variants
             WHERE sku IN ('MRD-WS3-2') OR sku LIKE 'KOTO-FOLIO%' ORDER BY sku LIMIT 2
    LOOP
        PERFORM record_inventory_movement(v.id, -v.sellable, 'adjustment', 'probe:out:' || v.id, 'admin', NULL, v_staff);
    END LOOP;
    RESET ROLE;
END
$$;
