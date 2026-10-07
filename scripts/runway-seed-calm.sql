-- Probe only: restock every SKU that was running out, so no estimate is under 30 days.
DO $$
DECLARE
    v_staff uuid := (SELECT id FROM users WHERE lower(email) = 'layout-check@goen.invalid' AND role = 'admin');
    v record;
BEGIN
    SET ROLE admin;
    FOR v IN SELECT id FROM product_variants WHERE sku IN ('DRC-BNS-1', 'DRC-BNS-2', 'NMB-B2-2', 'FRN-TEA-1')
    LOOP
        PERFORM record_inventory_movement(v.id, 400, 'receipt', 'probe:calm:' || v.id, 'admin', NULL, v_staff);
    END LOOP;
    RESET ROLE;
END
$$;
