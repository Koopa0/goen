-- Business manifest for restore drills. Aggregates commerce state without
-- customer secrets: no emails, names, addresses, tokens or session material.
SELECT line FROM (
    -- Key every stock balance and its supporting holds and movements so
    -- corruption on different variants cannot cancel out in a global total.
    SELECT 'inventory' || chr(9) || id::text || chr(9) || stock_quantity::text || chr(9) || safety_stock::text AS line
      FROM product_variants
    UNION ALL
    SELECT 'variant_reservation' || chr(9) || variant_id::text || chr(9) || state || chr(9) || count(*)::text || chr(9) || sum(quantity)::text
      FROM inventory_reservations
     GROUP BY variant_id, state
    UNION ALL
    SELECT 'variant_movement' || chr(9) || variant_id::text || chr(9) || reason || chr(9) || count(*)::text || chr(9) || sum(delta)::text
      FROM inventory_movements
     GROUP BY variant_id, reason
    UNION ALL
    SELECT 'reservation' || chr(9) || state || chr(9) || count(*)::text || chr(9) || coalesce(sum(quantity), 0)::text AS line
      FROM inventory_reservations
     GROUP BY state
    UNION ALL
    SELECT 'payment' || chr(9) || status || chr(9) || count(*)::text || chr(9) ||
           coalesce(sum(captured_amount_cents), 0)::text || chr(9) || coalesce(sum(intended_amount_cents), 0)::text
      FROM payments
     GROUP BY status
    UNION ALL
    SELECT 'refund' || chr(9) || status || chr(9) || count(*)::text || chr(9) || coalesce(sum(amount_cents), 0)::text
      FROM refunds
     GROUP BY status
    UNION ALL
    SELECT 'order' || chr(9) || fulfillment_status || chr(9) || count(*)::text
      FROM orders
     GROUP BY fulfillment_status
    UNION ALL
    SELECT 'store_credit' || chr(9) || reason || chr(9) || coalesce(sum(amount_cents), 0)::text
      FROM store_credit_entries
     GROUP BY reason
    UNION ALL
    SELECT 'loyalty' || chr(9) || kind || chr(9) || coalesce(sum(points), 0)::text
      FROM loyalty_entries
     GROUP BY kind
    UNION ALL
    SELECT 'media' || chr(9) || digest || chr(9) || encode(sha256(bytes), 'hex')
      FROM media_objects
    UNION ALL
    SELECT 'outbox_pending' || chr(9) || topic || chr(9) || count(*)::text
      FROM outbox_messages
     WHERE delivered_at IS NULL
     GROUP BY topic
    UNION ALL
    SELECT 'copurchase' || chr(9) || count(*)::text || chr(9) || coalesce(sum(orders), 0)::text
      FROM product_copurchases
    UNION ALL
    SELECT 'invoice_op_pending' || chr(9) || count(*)::text
      FROM invoice_operations
     WHERE completed_at IS NULL
) manifest
ORDER BY line;
