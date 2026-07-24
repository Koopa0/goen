-- Drops everything 001 created. Development only: goen has no production
-- database to roll back.
--
-- The append-only triggers refuse UPDATE and DELETE, not DROP, so the tables
-- come away cleanly.
SET lock_timeout = '3s';
SET statement_timeout = '120s';

DROP TABLE IF EXISTS
    newsletter_subscribers, contact_messages, faq_entries,
    sale_campaign_products, sale_campaigns, promo_banners, hero_slides,
    audit_events, outbox_messages,
    payment_webhook_events, refunds, payments,
    invoice_documents, invoice_preferences,
    warranty_registrations, return_request_lines, return_requests,
    order_events, order_shipment_lines, order_shipments,
    order_private_data, order_lines, orders, order_number_counters,
    shipping_method_versions, shipping_methods,
    product_reviews, stock_notifications, wishlist_items,
    checkout_attempts, cart_items, carts,
    inventory_reservations, inventory_movements,
    store_credit_entries, store_credit_accounts,
    addresses, password_reset_tokens, sessions, staff_totp_credentials,
    user_identities, users,
    product_search_documents, product_specs, variant_option_values,
    product_variants, product_option_values, product_options,
    product_images, products, categories, brands
CASCADE;

DROP FUNCTION IF EXISTS
    next_order_number(),
    record_inventory_movement(uuid, integer, text, text, text, uuid, uuid),
    sale_campaign_products_guard(), refunds_guard(), payments_check_transition(),
    invoice_documents_guard(), warranty_within_purchase(),
    return_lines_within_purchase(), orders_freeze_money(), order_lines_freeze(),
    orders_check_complete(), orders_check_transition(), store_credit_guard(),
    categories_reject_cycle(), forbid_change(), set_updated_at();
