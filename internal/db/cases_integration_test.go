//go:build integration

package db_test

// One case per constraint, each written and then adversarially re-verified
// against a live PostgreSQL before being checked in. Ordinary source from here
// on — edit freely.
//
// This list is NOT the authority on what must be covered; the catalog is.
// TestEveryCheckConstraintIsExercised and TestEveryUniqueConstraintIsExercised
// read pg_constraint and pg_index and fail when anything here is missing or
// names a constraint that no longer exists. That gate is what stops this file
// drifting behind the schema the way the hand-written list it replaced did:
// that one covered 17 of 65 CHECKs and reported itself as complete.

var checkCases = []checkCase{
	{
		constraint: "addresses_city_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('11110010-0000-4000-8000-000000000001', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', '0922333444', '221', E'\t', '汐止區', '大同路一段 100 號');`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('11110010-0000-4000-8000-000000000002', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', '0922333444', '221', E'\t新北市', '汐止區', '大同路一段 100 號');`,
	},
	{
		constraint: "addresses_district_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('66666666-6666-4666-8666-666666666666', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', '0922333444', '221', '新北市', E'\t', '大同路一段 100 號');`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('6666aaaa-6666-4666-8666-666666666666', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', '0922333444', '221', '新北市', E'\t汐止區', '大同路一段 100 號');`,
	},
	{
		constraint: "addresses_phone_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('1111000e-0000-4000-8000-000000000001', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', E'\t', '221', '新北市', '汐止區', '大同路一段 100 號');`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('1111000e-0000-4000-8000-000000000002', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', E'\t0922333444', '221', '新北市', '汐止區', '大同路一段 100 號');`,
	},
	{
		constraint: "addresses_postal_code_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('1111000f-0000-4000-8000-000000000001', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', '0922333444', E'\t', '新北市', '汐止區', '大同路一段 100 號');`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('1111000f-0000-4000-8000-000000000002', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', '0922333444', E'\t221', '新北市', '汐止區', '大同路一段 100 號');`,
	},
	{
		constraint: "addresses_recipient_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('1111000d-0000-4000-8000-000000000001', '5555aaaa-5555-4555-8555-555555555555', E'\t', '0922333444', '221', '新北市', '汐止區', '大同路一段 100 號');`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('1111000d-0000-4000-8000-000000000002', '5555aaaa-5555-4555-8555-555555555555', E'\t陳美玲', '0922333444', '221', '新北市', '汐止區', '大同路一段 100 號');`,
	},
	{
		constraint: "addresses_street_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('11110012-0000-4000-8000-000000000001', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', '0922333444', '221', '新北市', '汐止區', E'\t');`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street) VALUES ('11110012-0000-4000-8000-000000000002', '5555aaaa-5555-4555-8555-555555555555', '陳美玲', '0922333444', '221', '新北市', '汐止區', E'\t大同路一段 100 號');`,
	},
	{
		constraint: "audit_events_action_present",
		reject: `INSERT INTO audit_events (id, actor_user_id, action, entity_table, entity_id)
VALUES ('11110001-0000-4000-8000-000000000001',
        '55555555-5555-4555-8555-555555555555',
        E'\t', 'orders', '66666666-6666-4666-8666-666666666666');`,
		accept: `INSERT INTO audit_events (id, actor_user_id, action, entity_table, entity_id)
VALUES ('11110001-0000-4000-8000-000000000001',
        '55555555-5555-4555-8555-555555555555',
        'order.refunded', 'orders', '66666666-6666-4666-8666-666666666666');`,
	},
	{
		constraint: "audit_events_entity_present",
		reject: `INSERT INTO audit_events (id, actor_user_id, action, entity_table, entity_id)
VALUES ('11110002-0000-4000-8000-000000000001',
        '55555555-5555-4555-8555-555555555555',
        'order.refunded', E'\t', '66666666-6666-4666-8666-666666666666');`,
		accept: `INSERT INTO audit_events (id, actor_user_id, action, entity_table, entity_id)
VALUES ('11110002-0000-4000-8000-000000000001',
        '55555555-5555-4555-8555-555555555555',
        'order.refunded', 'orders', '66666666-6666-4666-8666-666666666666');`,
	},
	{
		constraint: "brands_name_present",
		reject:     `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'lumen-tech', E'\t');`,
		accept:     `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'lumen-tech', E'\t流明科技');`,
	},
	{
		constraint: "brands_slug_format",
		reject:     `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'lumen--tech', '流明科技');`,
		accept:     `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'lumen-tech', '流明科技');`,
	},
	{
		constraint: "cart_items_quantity_in_range",
		reject:     `INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ('bbbb1111-0000-4000-8000-000000000000', '44444444-4444-4444-8444-444444444444', 0);`,
		accept:     `INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ('bbbb1111-0000-4000-8000-000000000000', '44444444-4444-4444-8444-444444444444', 1); INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ('bbbb1111-0000-4000-8000-000000000000', '4444aaaa-4444-4444-8444-444444444444', 999);`,
	},
	{
		constraint: "categories_name_present",
		reject:     `INSERT INTO categories (id, slug, name) VALUES ('22220001-0000-4000-8000-000000000001', 'tablets', E'\t');`,
		accept:     `INSERT INTO categories (id, slug, name) VALUES ('22220001-0000-4000-8000-000000000001', 'tablets', E'\t平板');`,
	},
	{
		constraint: "categories_not_own_parent",
		reject:     `ALTER TABLE categories DISABLE TRIGGER categories_acyclic; INSERT INTO categories (id, parent_id, slug, name) VALUES ('22220001-0000-4000-8000-000000000001', '22220001-0000-4000-8000-000000000001', 'tablets', '平板');`,
		accept:     `ALTER TABLE categories DISABLE TRIGGER categories_acyclic; INSERT INTO categories (id, parent_id, slug, name) VALUES ('22220001-0000-4000-8000-000000000001', '22222222-2222-4222-8222-222222222222', 'tablets', '平板');`,
	},
	{
		constraint: "categories_slug_format",
		reject:     `INSERT INTO categories (id, slug, name) VALUES ('22220001-0000-4000-8000-000000000001', 'tablets--pro', '平板');`,
		accept:     `INSERT INTO categories (id, slug, name) VALUES ('22220001-0000-4000-8000-000000000001', 'tablets-pro', '平板');`,
	},
	{
		constraint: "checkout_attempts_key_present",
		reject:     `INSERT INTO checkout_attempts (idempotency_key, cart_id) VALUES (E'\t', 'bbbb1111-0000-4000-8000-000000000000');`,
		accept:     `INSERT INTO checkout_attempts (idempotency_key, cart_id) VALUES ('checkout-2026-07-24-0001', 'bbbb1111-0000-4000-8000-000000000000'); INSERT INTO checkout_attempts (idempotency_key, cart_id) VALUES (E'\tcheckout-2026-07-24-0002', 'bbbb1111-0000-4000-8000-000000000000');`,
	},
	{
		constraint: "contact_messages_email_present",
		reject:     `INSERT INTO contact_messages (id, name, email, subject, message) VALUES ('11150002-0000-4000-8000-000000000001', '王小明', E'\t', '訂單問題', '請問我的訂單何時出貨?');`,
		accept:     `INSERT INTO contact_messages (id, name, email, subject, message) VALUES ('11150002-0000-4000-8000-000000000001', '王小明', 'ming@example.com', '訂單問題', '請問我的訂單何時出貨?');`,
	},
	{
		constraint: "contact_messages_message_present",
		reject:     `INSERT INTO contact_messages (id, name, email, subject, message) VALUES ('11150004-0000-4000-8000-000000000001', '王小明', 'ming@example.com', '訂單問題', E'\t');`,
		accept:     `INSERT INTO contact_messages (id, name, email, subject, message) VALUES ('11150004-0000-4000-8000-000000000001', '王小明', 'ming@example.com', '訂單問題', '請問我的訂單何時出貨?');`,
	},
	{
		constraint: "contact_messages_name_present",
		reject:     `INSERT INTO contact_messages (id, name, email, subject, message) VALUES ('11150001-0000-4000-8000-000000000001', E'\t', 'ming@example.com', '訂單問題', '請問我的訂單何時出貨?');`,
		accept:     `INSERT INTO contact_messages (id, name, email, subject, message) VALUES ('11150001-0000-4000-8000-000000000001', '王小明', 'ming@example.com', '訂單問題', '請問我的訂單何時出貨?');`,
	},
	{
		constraint: "contact_messages_subject_present",
		reject:     `INSERT INTO contact_messages (id, name, email, subject, message) VALUES ('11150003-0000-4000-8000-000000000001', '王小明', 'ming@example.com', E'\t', '請問我的訂單何時出貨?');`,
		accept:     `INSERT INTO contact_messages (id, name, email, subject, message) VALUES ('11150003-0000-4000-8000-000000000001', '王小明', 'ming@example.com', '訂單問題', '請問我的訂單何時出貨?');`,
	},
	{
		constraint: "faq_entries_answer_present",
		reject:     `INSERT INTO faq_entries (id, category, question, answer, position) VALUES ('11140003-0000-4000-8000-000000000001', '付款', '可以使用哪些付款方式?', E'\t', 2);`,
		accept:     `INSERT INTO faq_entries (id, category, question, answer, position) VALUES ('11140003-0000-4000-8000-000000000001', '付款', '可以使用哪些付款方式?', '支援信用卡、ATM 轉帳與貨到付款。', 2);`,
	},
	{
		constraint: "faq_entries_category_present",
		reject:     `INSERT INTO faq_entries (id, category, question, answer, position) VALUES ('11140001-0000-4000-8000-000000000001', E'\t', '可以使用哪些付款方式?', '支援信用卡、ATM 轉帳與貨到付款。', 0);`,
		accept:     `INSERT INTO faq_entries (id, category, question, answer, position) VALUES ('11140001-0000-4000-8000-000000000001', '付款', '可以使用哪些付款方式?', '支援信用卡、ATM 轉帳與貨到付款。', 0);`,
	},
	{
		constraint: "faq_entries_question_present",
		reject:     `INSERT INTO faq_entries (id, category, question, answer, position) VALUES ('11140002-0000-4000-8000-000000000001', '付款', E'\t', '支援信用卡、ATM 轉帳與貨到付款。', 1);`,
		accept:     `INSERT INTO faq_entries (id, category, question, answer, position) VALUES ('11140002-0000-4000-8000-000000000001', '付款', '可以使用哪些付款方式?', '支援信用卡、ATM 轉帳與貨到付款。', 1);`,
	},
	{
		constraint: "hero_slides_headline_present",
		reject:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position) VALUES ('11110001-0000-4000-8000-000000000001', E'\t', '立即選購', '/c/laptops', 11);`,
		accept:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position) VALUES ('11110001-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', 11);`,
	},
	{
		constraint: "hero_slides_image_has_alt",
		reject:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, image_key, image_alt, position) VALUES ('11110003-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', 'hero-spring.webp', E'\t', 13);`,
		accept:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, image_key, image_alt, position) VALUES ('11110003-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', 'hero-spring.webp', '春季新機主視覺', 13); INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, image_key, image_alt, position) VALUES ('11110007-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', NULL, E'\t', 16);`,
	},
	{
		constraint: "hero_slides_primary_cta_present",
		reject:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position) VALUES ('11110002-0000-4000-8000-000000000001', '春季新機上市', E'\t', '/c/laptops', 12);`,
		accept:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position) VALUES ('11110002-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', 12);`,
	},
	{
		constraint: "hero_slides_secondary_cta_complete",
		reject:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, secondary_cta_label, secondary_cta_href, position) VALUES ('11110004-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', '了解更多', NULL, 14);`,
		accept:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, secondary_cta_label, secondary_cta_href, position) VALUES ('11110004-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', '了解更多', '/pages/about', 14);`,
	},
	{
		constraint: "hero_slides_window_ordered",
		reject:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position, starts_at, ends_at) VALUES ('11110005-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', 15, '2026-09-01 00:00:00+08', '2026-09-01 00:00:00+08');`,
		accept:     `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position, starts_at, ends_at) VALUES ('11110005-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', 15, '2026-09-01 00:00:00+08', '2026-09-01 00:00:01+08');`,
	},
	{
		constraint: "inventory_movements_delta_non_zero",
		reject:     `INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000001', '44444444-4444-4444-8444-444444444444', 0, 'adjustment', 'im-delta-case');`,
		accept:     `INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000001', '44444444-4444-4444-8444-444444444444', -1, 'adjustment', 'im-delta-case');`,
	},
	{
		constraint: "inventory_movements_key_present",
		reject:     `INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000003', '44444444-4444-4444-8444-444444444444', 3, 'receipt', E'\t');`,
		accept:     `INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000003', '44444444-4444-4444-8444-444444444444', 3, 'receipt', E'\tim-key-case');`,
	},
	{
		constraint: "inventory_movements_reason_known",
		reject:     `INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000002', '44444444-4444-4444-8444-444444444444', 3, 'audit', 'im-reason-case');`,
		accept:     `INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000002', '44444444-4444-4444-8444-444444444444', 3, 'adjustment', 'im-reason-case');`,
	},
	{
		constraint: "inventory_reservations_quantity_positive",
		reject:     `INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, expires_at) VALUES ('11110003-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 0, now() + interval '15 minutes');`,
		accept:     `INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, expires_at) VALUES ('11110003-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, now() + interval '15 minutes');`,
	},
	{
		constraint: "inventory_reservations_settled_has_state",
		reject:     `INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, state, expires_at, settled_at) VALUES ('11110003-0000-4000-8000-000000000003', '66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 2, 'held', now() + interval '15 minutes', now());`,
		accept:     `INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, state, expires_at, settled_at) VALUES ('11110003-0000-4000-8000-000000000003', '66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 2, 'consumed', now() + interval '15 minutes', now());`,
	},
	{
		constraint: "inventory_reservations_state_known",
		reject:     `INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, state, expires_at, settled_at) VALUES ('11110003-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 2, 'expired', now() + interval '15 minutes', now());`,
		accept:     `INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, state, expires_at, settled_at) VALUES ('11110003-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 2, 'released', now() + interval '15 minutes', now());`,
	},
	{
		constraint: "invoice_documents_allowance_has_original",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110006-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'allowance', NULL, 'GD-72031406', 100000, 'issued', NULL);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110006-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', 'allowance', '99990001-0000-4000-8000-000000000000', 'GD-72031406', 100000, 'issued', NULL);`,
	},
	{
		constraint: "invoice_documents_amount_positive",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110004-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031404', 0, 'issued', NULL);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110004-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031404', 1, 'issued', NULL);`,
	},
	{
		constraint: "invoice_documents_kind_known",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110001-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'credit_note', NULL, 'GD-72031401', 100000, 'issued', NULL);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110001-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031401', 100000, 'issued', NULL);`,
	},
	{
		constraint: "invoice_documents_number_present",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110003-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, E'\t', 100000, 'issued', NULL);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110003-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031403', 100000, 'issued', NULL);`,
	},
	{
		constraint: "invoice_documents_status_known",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110002-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031402', 100000, 'cancelled', NULL);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110002-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031402', 100000, 'issued', NULL);`,
	},
	{
		constraint: "invoice_documents_voided_has_time",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110005-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031405', 100000, 'voided', NULL);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110005-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031405', 100000, 'voided', '2026-07-24 10:00:00+08');`,
	},
	{
		constraint: "invoice_preferences_company_has_tax_id",
		reject:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id) VALUES ('66666666-6666-4666-8666-666666666666', 'company', NULL, '1234567');`,
		accept:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id) VALUES ('66666666-6666-4666-8666-666666666666', 'company', NULL, '12345678');`,
	},
	{
		constraint: "invoice_preferences_mobile_has_carrier",
		reject:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id) VALUES ('66666666-6666-4666-8666-666666666666', 'mobile_carrier', E'\t', NULL);`,
		accept:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id) VALUES ('66666666-6666-4666-8666-666666666666', 'mobile_carrier', '/ABC+123', NULL);`,
	},
	{
		constraint: "invoice_preferences_type_known",
		reject:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id) VALUES ('66666666-6666-4666-8666-666666666666', 'paper', NULL, NULL);`,
		accept:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id) VALUES ('66666666-6666-4666-8666-666666666666', 'member_carrier', NULL, NULL);`,
	},
	{
		constraint: "newsletter_subscribers_email_present",
		reject:     `INSERT INTO newsletter_subscribers (id, email) VALUES ('11160001-0000-4000-8000-000000000001', E'\t');`,
		accept:     `INSERT INTO newsletter_subscribers (id, email) VALUES ('11160001-0000-4000-8000-000000000001', 'reader@example.com');`,
	},
	{
		constraint: "newsletter_subscribers_email_trimmed",
		reject:     `INSERT INTO newsletter_subscribers (id, email) VALUES ('11160002-0000-4000-8000-000000000001', ' reader@example.com');`,
		accept:     `INSERT INTO newsletter_subscribers (id, email) VALUES ('11160002-0000-4000-8000-000000000001', 'reader@example.com');`,
	},
	{
		constraint: "order_events_kind_known",
		reject:     `INSERT INTO order_events (id, order_id, kind) VALUES ('1111000a-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'archived');`,
		accept:     `INSERT INTO order_events (id, order_id, kind) VALUES ('1111000a-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'delivered');`,
	},
	{
		constraint: "order_lines_product_name_present",
		reject:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110002-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-9P-512-BL', E'\t', 3690000, 1, 1);`,
		accept:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110002-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 1);`,
	},
	{
		constraint: "order_lines_quantity_in_range",
		reject:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110003-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 3690000, 0, 1);`,
		accept:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110003-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 1);`,
	},
	{
		constraint: "order_lines_sku_present",
		reject:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', E'\t', 'Pixelight 9 Pro 5G', 3690000, 1, 1);`,
		accept:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 1);`,
	},
	{
		constraint: "order_lines_unit_price_in_range",
		reject:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110004-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 10000000001, 1, 1);`,
		accept:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110004-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 10000000000, 1, 1);`,
	},
	{
		constraint: "order_number_counters_in_range",
		reject: `INSERT INTO order_number_counters (business_date, last_no) VALUES
    (DATE '2026-07-24', 0);`,
		accept: `INSERT INTO order_number_counters (business_date, last_no) VALUES
    (DATE '2026-07-24', 1),
    (DATE '2026-07-25', 999999);`,
	},
	{
		constraint: "order_private_data_erased_is_empty",
		reject:     `UPDATE order_private_data SET erased_at = now() WHERE order_id = '6666aaaa-6666-4666-8666-666666666666';`,
		accept:     `UPDATE order_private_data SET erased_at = now(), email = NULL, recipient_name = NULL, phone = NULL, postal_code = NULL, city = NULL, district = NULL, street = NULL WHERE order_id = '6666aaaa-6666-4666-8666-666666666666';`,
	},
	{
		constraint: "order_shipment_lines_quantity_positive",
		reject:     `INSERT INTO order_shipment_lines (shipment_id, order_line_id, quantity) VALUES ('66660002-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 0);`,
		accept:     `INSERT INTO order_shipment_lines (shipment_id, order_line_id, quantity) VALUES ('66660002-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 1);`,
	},
	{
		constraint: "order_shipments_carrier_present",
		reject:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110006-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', E'\t', '903-2214-8872');`,
		accept:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110006-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-8872');`,
	},
	{
		constraint: "order_shipments_delivered_after_shipped",
		reject:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number, shipped_at, delivered_at) VALUES ('11110008-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-8874', '2026-07-20 10:00:00+08', '2026-07-20 09:59:59+08');`,
		accept:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number, shipped_at, delivered_at) VALUES ('11110008-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-8874', '2026-07-20 10:00:00+08', '2026-07-20 10:00:00+08');`,
	},
	{
		constraint: "order_shipments_tracking_present",
		reject:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110007-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', E'\t');`,
		accept:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110007-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-8873');`,
	},
	{
		constraint: "orders_cancelled_after_placed",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, placed_at, cancelled_at) VALUES
    ('11110010-0000-4000-8000-000000000001', 'GO-260722-000010', 'home_delivery', '宅配到府', TIMESTAMPTZ '2026-07-20 09:00:00+08', TIMESTAMPTZ '2026-07-20 08:59:59+08');`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, placed_at, cancelled_at) VALUES
    ('11110010-0000-4000-8000-000000000001', 'GO-260722-000010', 'home_delivery', '宅配到府', TIMESTAMPTZ '2026-07-20 09:00:00+08', TIMESTAMPTZ '2026-07-20 09:00:00+08');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220010-0000-4000-8000-000000000001', '11110010-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110010-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_cancelled_has_time",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, fulfillment_status, placed_at, cancelled_at) VALUES
    ('11110012-0000-4000-8000-000000000001', 'GO-260722-000012', 'home_delivery', '宅配到府', 'cancelled', TIMESTAMPTZ '2026-07-20 09:00:00+08', NULL);`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, fulfillment_status, placed_at, cancelled_at) VALUES
    ('11110012-0000-4000-8000-000000000001', 'GO-260722-000012', 'home_delivery', '宅配到府', 'cancelled', TIMESTAMPTZ '2026-07-20 09:00:00+08', TIMESTAMPTZ '2026-07-20 09:00:00+08');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220012-0000-4000-8000-000000000001', '11110012-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110012-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_completed_after_placed",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, placed_at, completed_at) VALUES
    ('11119001-0000-4000-8000-000000000001', 'GO-260722-000011', 'home_delivery', '宅配到府', TIMESTAMPTZ '2026-07-20 09:00:00+08', TIMESTAMPTZ '2026-07-20 08:59:59+08');`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, placed_at, completed_at) VALUES
    ('11119001-0000-4000-8000-000000000001', 'GO-260722-000011', 'home_delivery', '宅配到府', TIMESTAMPTZ '2026-07-20 09:00:00+08', TIMESTAMPTZ '2026-07-20 09:00:00+08');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220011-0000-4000-8000-000000000001', '11119001-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11119001-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_completed_has_time",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, fulfillment_status, placed_at, completed_at) VALUES
    ('11110013-0000-4000-8000-000000000001', 'GO-260722-000013', 'home_delivery', '宅配到府', 'completed', TIMESTAMPTZ '2026-07-20 09:00:00+08', NULL);`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, fulfillment_status, placed_at, completed_at) VALUES
    ('11110013-0000-4000-8000-000000000001', 'GO-260722-000013', 'home_delivery', '宅配到府', 'completed', TIMESTAMPTZ '2026-07-20 09:00:00+08', TIMESTAMPTZ '2026-07-20 09:00:00+08');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220013-0000-4000-8000-000000000001', '11110013-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110013-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_currency_is_twd",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, currency) VALUES
    ('11110002-0000-4000-8000-000000000001', 'GO-260722-000002', 'home_delivery', '宅配到府', 'USD');`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, currency) VALUES
    ('11110002-0000-4000-8000-000000000001', 'GO-260722-000002', 'home_delivery', '宅配到府', 'TWD');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220002-0000-4000-8000-000000000001', '11110002-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110002-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_discount_non_negative",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, discount_cents) VALUES
    ('11110003-0000-4000-8000-000000000001', 'GO-260722-000003', 'home_delivery', '宅配到府', -1);`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, discount_cents) VALUES
    ('11110003-0000-4000-8000-000000000001', 'GO-260722-000003', 'home_delivery', '宅配到府', 0);
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220003-0000-4000-8000-000000000001', '11110003-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110003-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_fulfillment_status_known",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, fulfillment_status) VALUES
    ('11110008-0000-4000-8000-000000000001', 'GO-260722-000008', 'home_delivery', '宅配到府', 'refunded');`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, fulfillment_status) VALUES
    ('11110008-0000-4000-8000-000000000001', 'GO-260722-000008', 'home_delivery', '宅配到府', 'picking');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220008-0000-4000-8000-000000000001', '11110008-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110008-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_not_both_ended",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, placed_at, cancelled_at, completed_at) VALUES
    ('11110009-0000-4000-8000-000000000001', 'GO-260722-000009', 'home_delivery', '宅配到府', TIMESTAMPTZ '2026-07-20 09:00:00+08', TIMESTAMPTZ '2026-07-20 09:00:00+08', TIMESTAMPTZ '2026-07-20 09:00:00+08');`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, placed_at, cancelled_at, completed_at) VALUES
    ('11110009-0000-4000-8000-000000000001', 'GO-260722-000009', 'home_delivery', '宅配到府', TIMESTAMPTZ '2026-07-20 09:00:00+08', TIMESTAMPTZ '2026-07-20 09:00:00+08', NULL);
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220009-0000-4000-8000-000000000001', '11110009-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110009-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_number_format",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name) VALUES
    ('11110001-0000-4000-8000-000000000001', 'GO-26072-000001', 'home_delivery', '宅配到府');`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name) VALUES
    ('11110001-0000-4000-8000-000000000001', 'GO-260722-000001', 'home_delivery', '宅配到府');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220001-0000-4000-8000-000000000001', '11110001-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110001-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_shipping_code_present",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name) VALUES
    ('11110006-0000-4000-8000-000000000001', 'GO-260722-000006', E'\t', '宅配到府');`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name) VALUES
    ('11110006-0000-4000-8000-000000000001', 'GO-260722-000006', E'\thome_delivery', '宅配到府');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220006-0000-4000-8000-000000000001', '11110006-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110006-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_shipping_name_present",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name) VALUES
    ('11110007-0000-4000-8000-000000000001', 'GO-260722-000007', 'home_delivery', E'\t');`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name) VALUES
    ('11110007-0000-4000-8000-000000000001', 'GO-260722-000007', 'home_delivery', E'\t宅配到府');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220007-0000-4000-8000-000000000001', '11110007-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110007-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_shipping_non_negative",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, shipping_cents) VALUES
    ('11110004-0000-4000-8000-000000000001', 'GO-260722-000004', 'home_delivery', '宅配到府', -1);`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, shipping_cents) VALUES
    ('11110004-0000-4000-8000-000000000001', 'GO-260722-000004', 'home_delivery', '宅配到府', 0);
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220004-0000-4000-8000-000000000001', '11110004-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110004-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "orders_tax_non_negative",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, tax_cents) VALUES
    ('11110005-0000-4000-8000-000000000001', 'GO-260722-000005', 'home_delivery', '宅配到府', -1);`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name, tax_cents) VALUES
    ('11110005-0000-4000-8000-000000000001', 'GO-260722-000005', 'home_delivery', '宅配到府', 0);
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220005-0000-4000-8000-000000000001', '11110005-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110005-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		constraint: "outbox_messages_attempts_non_negative",
		reject: `INSERT INTO outbox_messages (id, topic, dedupe_key, payload, attempts)
VALUES ('11110003-0000-4000-8000-000000000001', 'order.paid',
        'order:66666666-6666-4666-8666-666666666666',
        '{"order_number": "GO-260721-000387"}'::jsonb, -1);`,
		accept: `INSERT INTO outbox_messages (id, topic, dedupe_key, payload, attempts)
VALUES ('11110003-0000-4000-8000-000000000001', 'order.paid',
        'order:66666666-6666-4666-8666-666666666666',
        '{"order_number": "GO-260721-000387"}'::jsonb, 0);`,
	},
	{
		constraint: "outbox_messages_topic_present",
		reject: `INSERT INTO outbox_messages (id, topic, dedupe_key, payload)
VALUES ('11110004-0000-4000-8000-000000000001', E'\t',
        'order:66666666-6666-4666-8666-666666666666',
        '{"order_number": "GO-260721-000387"}'::jsonb);`,
		accept: `INSERT INTO outbox_messages (id, topic, dedupe_key, payload)
VALUES ('11110004-0000-4000-8000-000000000001', 'order.paid',
        'order:66666666-6666-4666-8666-666666666666',
        '{"order_number": "GO-260721-000387"}'::jsonb);`,
	},
	{
		constraint: "password_reset_tokens_expiry_after_creation",
		reject:     `INSERT INTO password_reset_tokens (token_hash, user_id, created_at, expires_at) VALUES ('\x1111000b', '55555555-5555-4555-8555-555555555555', timestamptz '2026-07-24 09:00:00+08', timestamptz '2026-07-24 09:00:00+08');`,
		accept:     `INSERT INTO password_reset_tokens (token_hash, user_id, created_at, expires_at) VALUES ('\x1111000c', '55555555-5555-4555-8555-555555555555', timestamptz '2026-07-24 09:00:00+08', timestamptz '2026-07-24 09:00:00+08' + interval '1 microsecond');`,
	},
	{
		constraint: "payment_webhook_events_type_present",
		reject: `INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload)
VALUES ('stripe', 'evt_type_case', E'\t', 'pi_fixture',
        '{"id": "evt_type_case", "object": "event"}'::jsonb);`,
		accept: `INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload)
VALUES ('stripe', 'evt_type_case', E'\tpayment_intent.succeeded', 'pi_fixture',
        '{"id": "evt_type_case", "object": "event"}'::jsonb);`,
	},
	{
		constraint: "payments_captured_non_negative",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents)
VALUES ('11110004-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_captured_case', 'processing', 3690000, -1);`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents)
VALUES ('11110004-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_captured_case', 'processing', 3690000, 0);`,
	},
	{
		constraint: "payments_currency_is_twd",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, currency)
VALUES ('11110005-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_currency_case', 'requires_payment', 3690000, 'USD');`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, currency)
VALUES ('11110005-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_currency_case', 'requires_payment', 3690000, 'TWD');`,
	},
	{
		constraint: "payments_intended_positive",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
VALUES ('11110003-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_intended_case', 'requires_payment', 0);`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
VALUES ('11110003-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_intended_case', 'requires_payment', 1);`,
	},
	{
		constraint: "payments_last4_format",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, card_brand, card_last4)
VALUES ('11110006-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_last4_case', 'processing', 3690000, 'visa', '123');`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, card_brand, card_last4)
VALUES ('11110006-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_last4_case', 'processing', 3690000, 'visa', '1234');`,
	},
	{
		constraint: "payments_provider_known",
		reject: `INSERT INTO payments (id, order_id, provider, provider_ref, status, intended_amount_cents)
VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'paypal', 'pi_provider_case', 'requires_payment', 3690000);`,
		accept: `INSERT INTO payments (id, order_id, provider, provider_ref, status, intended_amount_cents)
VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'stripe', 'pi_provider_case', 'requires_payment', 3690000);`,
	},
	{
		constraint: "payments_status_known",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
VALUES ('11110002-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_status_case', 'refunded', 3690000);`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
VALUES ('11110002-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_status_case', 'processing', 3690000);`,
	},
	{
		constraint: "payments_succeeded_is_captured",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at)
VALUES ('11110007-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_succeeded_case', 'succeeded', 3690000, NULL, now());`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at)
VALUES ('11110007-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_succeeded_case', 'succeeded', 3690000, 3690000, now());`,
	},
	{
		constraint: "product_images_alt_present",
		reject:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-back.webp', E'\t', 1);`,
		accept:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-back.webp', E'\tPixelight 9 Pro 星霧藍背面', 1);`,
	},
	{
		constraint: "product_images_height_positive",
		reject:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, width, height, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-back.webp', 'Pixelight 9 Pro 星霧藍背面', 1200, 0, 1);`,
		accept:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, width, height, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-back.webp', 'Pixelight 9 Pro 星霧藍背面', 1200, 1, 1);`,
	},
	{
		constraint: "product_images_position_non_negative",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', '光素 9'); INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33330001-0000-4000-8000-000000000001', 'pxl-9-front.webp', '光素 9 正面', -1);`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', '光素 9'); INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33330001-0000-4000-8000-000000000001', 'pxl-9-front.webp', '光素 9 正面', 0);`,
	},
	{
		constraint: "product_images_storage_key_present",
		reject:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', E'\t', 'Pixelight 9 Pro 星霧藍背面', 1);`,
		accept:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', E'\tpxl-9p-back.webp', 'Pixelight 9 Pro 星霧藍背面', 1);`,
	},
	{
		constraint: "product_images_width_positive",
		reject:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, width, height, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-back.webp', 'Pixelight 9 Pro 星霧藍背面', 0, 1200, 1);`,
		accept:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, width, height, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-back.webp', 'Pixelight 9 Pro 星霧藍背面', 1, 1200, 1);`,
	},
	{
		constraint: "product_option_values_value_present",
		reject:     `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('11110001-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', E'\t');`,
		accept:     `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('11110001-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', '午夜綠');`,
	},
	{
		constraint: "product_options_name_present",
		reject:     `INSERT INTO product_options (id, product_id, name) VALUES ('11110002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', E'\t');`,
		accept:     `INSERT INTO product_options (id, product_id, name) VALUES ('11110002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '尺寸');`,
	},
	{
		constraint: "product_reviews_body_present",
		reject:     `INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110003-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 5, E'\t');`,
		accept:     `INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110003-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 5, '拍照非常清晰,續航也很夠用。'); INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110003-0000-4000-8000-000000000002', '33333333-3333-4333-8333-333333333333', '5555aaaa-5555-4555-8555-555555555555', 5, E'\t拍照非常清晰,續航也很夠用。');`,
	},
	{
		constraint: "product_reviews_rating_range",
		reject:     `INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110004-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 0, '螢幕偏色,不推薦。');`,
		accept:     `INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110004-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 1, '螢幕偏色,不推薦。'); INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110004-0000-4000-8000-000000000002', '33333333-3333-4333-8333-333333333333', '5555aaaa-5555-4555-8555-555555555555', 5, '整體表現優秀,非常推薦。');`,
	},
	{
		constraint: "product_search_documents_body_present",
		reject:     `INSERT INTO product_search_documents (product_id, body) VALUES ('33333333-3333-4333-8333-333333333333', E'\t');`,
		accept:     `INSERT INTO product_search_documents (product_id, body) VALUES ('33333333-3333-4333-8333-333333333333', 'Pixelight 9 Pro 5G 星霧藍 256GB');`,
	},
	{
		constraint: "product_specs_label_present",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110004-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', E'\t', '4nm 八核心', 1);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110004-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', '4nm 八核心', 1);`,
	},
	{
		constraint: "product_specs_value_present",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', E'\t', 2);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', '4nm 八核心', 2);`,
	},
	{
		constraint: "product_variants_compare_at_is_higher",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, compare_at_price_cents, position) VALUES ('11110006-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-BL', 3390000, 3390000, 10);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, compare_at_price_cents, position) VALUES ('11110006-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-BL', 3390000, 3390001, 10);`,
	},
	{
		constraint: "product_variants_price_in_range",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110007-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-GY', 10000000001, 11);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110007-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-GY', 10000000000, 11); INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110027-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-FR', 0, 16);`,
	},
	{
		constraint: "product_variants_safety_stock_non_negative",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position) VALUES ('11110008-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-GR', 3390000, -1, 12);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position) VALUES ('11110008-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-GR', 3390000, 0, 12);`,
	},
	{
		constraint: "product_variants_sku_format",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110009-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-128-wh', 3390000, 13);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110009-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-WH', 3390000, 13);`,
	},
	{
		constraint: "product_variants_stock_non_negative",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, position) VALUES ('11110010-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-PK', 3390000, -1, 14);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, position) VALUES ('11110010-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-PK', 3390000, 0, 14);`,
	},
	{
		constraint: "products_active_is_published",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', '光素 9', 'active', NULL);`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', '光素 9', 'active', now());`,
	},
	{
		constraint: "products_name_present",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', E'\t');`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', E'\t光素 9');`,
	},
	{
		constraint: "products_slug_format",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight--9', '光素 9');`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', '光素 9');`,
	},
	{
		constraint: "products_status_known",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', '光素 9', 'published');`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', '光素 9', 'archived');`,
	},
	{
		constraint: "promo_banners_cta_complete",
		reject:     `INSERT INTO promo_banners (id, message, cta_label, cta_href) VALUES ('11120002-0000-4000-8000-000000000001', '週年慶全站 85 折', '看活動', NULL);`,
		accept:     `INSERT INTO promo_banners (id, message, cta_label, cta_href) VALUES ('11120002-0000-4000-8000-000000000001', '週年慶全站 85 折', '看活動', '/deals');`,
	},
	{
		constraint: "promo_banners_message_present",
		reject:     `INSERT INTO promo_banners (id, message) VALUES ('11120001-0000-4000-8000-000000000001', E'\t');`,
		accept:     `INSERT INTO promo_banners (id, message) VALUES ('11120001-0000-4000-8000-000000000001', '週年慶全站 85 折');`,
	},
	{
		constraint: "promo_banners_window_ordered",
		reject:     `INSERT INTO promo_banners (id, message, starts_at, ends_at) VALUES ('11120003-0000-4000-8000-000000000001', '週年慶全站 85 折', '2026-09-01 00:00:00+08', '2026-09-01 00:00:00+08');`,
		accept:     `INSERT INTO promo_banners (id, message, starts_at, ends_at) VALUES ('11120003-0000-4000-8000-000000000001', '週年慶全站 85 折', '2026-09-01 00:00:00+08', '2026-09-01 00:00:01+08');`,
	},
	{
		constraint: "refunds_amount_positive",
		reject: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220001-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-amount-case', 'pending', 0, '商品瑕疵');`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220001-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-amount-case', 'pending', 1, '商品瑕疵');`,
	},
	{
		constraint: "refunds_failed_has_time",
		reject: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason, failed_at)
VALUES ('22220005-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-failed-case', 'failed', 100000, '刷退失敗', NULL);`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason, failed_at)
VALUES ('22220005-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-failed-case', 'failed', 100000, '刷退失敗', now());`,
	},
	{
		constraint: "refunds_request_key_present",
		reject: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220002-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        E'\t', 'pending', 100000, '商品瑕疵');`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220002-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        E'\trk-present-case', 'pending', 100000, '商品瑕疵');`,
	},
	{
		constraint: "refunds_status_known",
		reject: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220003-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-status-case', 'refunded', 100000, '七天鑑賞期退貨');`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220003-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-status-case', 'requires_action', 100000, '七天鑑賞期退貨');`,
	},
	{
		constraint: "refunds_succeeded_has_time",
		reject: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason, succeeded_at)
VALUES ('22220004-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-succeeded-case', 'succeeded', 100000, '七天鑑賞期退貨', NULL);`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason, succeeded_at)
VALUES ('22220004-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-succeeded-case', 'succeeded', 100000, '七天鑑賞期退貨', now());`,
	},
	{
		constraint: "return_request_lines_quantity_positive",
		reject: `INSERT INTO return_request_lines (return_request_id, order_line_id, quantity) VALUES
    ('88880001-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 0);`,
		accept: `INSERT INTO return_request_lines (return_request_id, order_line_id, quantity) VALUES
    ('88880001-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 1);`,
	},
	{
		constraint: "return_requests_decided_has_time",
		reject: `INSERT INTO return_requests (id, order_id, status, reason, decided_at) VALUES
    ('11110001-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666',
     'approved', '螢幕有亮點', NULL);`,
		accept: `INSERT INTO return_requests (id, order_id, status, reason, decided_at) VALUES
    ('11110001-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666',
     'approved', '螢幕有亮點', now());`,
	},
	{
		constraint: "return_requests_reason_present",
		reject: `INSERT INTO return_requests (id, order_id, reason) VALUES
    ('11110001-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', E'\t');`,
		accept: `INSERT INTO return_requests (id, order_id, reason) VALUES
    ('11110001-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', E'\t螢幕有亮點');`,
	},
	{
		constraint: "return_requests_status_known",
		reject: `INSERT INTO return_requests (id, order_id, status, reason, decided_at) VALUES
    ('11110001-0000-4000-8000-000000000003', '66666666-6666-4666-8666-666666666666',
     'cancelled', '螢幕有亮點', now());`,
		accept: `INSERT INTO return_requests (id, order_id, status, reason, decided_at) VALUES
    ('11110001-0000-4000-8000-000000000003', '66666666-6666-4666-8666-666666666666',
     'completed', '螢幕有亮點', now());`,
	},
	{
		constraint: "sale_campaigns_slug_format",
		reject:     `INSERT INTO sale_campaigns (id, slug, title, ends_at) VALUES ('11130001-0000-4000-8000-000000000001', 'Autumn', '秋季換機祭', '2026-10-01 00:00:00+08');`,
		accept:     `INSERT INTO sale_campaigns (id, slug, title, ends_at) VALUES ('11130001-0000-4000-8000-000000000001', 'autumn', '秋季換機祭', '2026-10-01 00:00:00+08'); INSERT INTO sale_campaigns (id, slug, title, ends_at) VALUES ('11130005-0000-4000-8000-000000000001', 'autumn-2026-sale', '秋季換機祭續辦', '2026-10-01 00:00:00+08');`,
	},
	{
		constraint: "sale_campaigns_title_present",
		reject:     `INSERT INTO sale_campaigns (id, slug, title, ends_at) VALUES ('11130002-0000-4000-8000-000000000001', 'autumn', E'\t', '2026-10-01 00:00:00+08');`,
		accept:     `INSERT INTO sale_campaigns (id, slug, title, ends_at) VALUES ('11130002-0000-4000-8000-000000000001', 'autumn', '秋季換機祭', '2026-10-01 00:00:00+08');`,
	},
	{
		constraint: "sale_campaigns_window_ordered",
		reject:     `INSERT INTO sale_campaigns (id, slug, title, starts_at, ends_at) VALUES ('11130003-0000-4000-8000-000000000001', 'autumn', '秋季換機祭', '2026-10-01 00:00:00+08', '2026-10-01 00:00:00+08');`,
		accept:     `INSERT INTO sale_campaigns (id, slug, title, starts_at, ends_at) VALUES ('11130003-0000-4000-8000-000000000001', 'autumn', '秋季換機祭', '2026-10-01 00:00:00+08', '2026-10-01 00:00:01+08');`,
	},
	{
		constraint: "sessions_expiry_after_creation",
		reject:     `INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('\x11110009', '55555555-5555-4555-8555-555555555555', timestamptz '2026-07-24 09:00:00+08', timestamptz '2026-07-24 09:00:00+08');`,
		accept:     `INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('\x1111000a', '55555555-5555-4555-8555-555555555555', timestamptz '2026-07-24 09:00:00+08', timestamptz '2026-07-24 09:00:00+08' + interval '1 microsecond');`,
	},
	{
		constraint: "shipping_method_versions_fee_non_negative",
		reject: `INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110202-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '超商取貨', '統一超商', -1, NULL, TIMESTAMPTZ '2026-08-02 10:00:00+08');`,
		accept: `INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110202-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '超商取貨', '統一超商', 0, NULL, TIMESTAMPTZ '2026-08-02 10:00:00+08');`,
	},
	{
		constraint: "shipping_method_versions_free_over_non_negative",
		reject: `INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110203-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '超商取貨', '統一超商', 8000, -1, TIMESTAMPTZ '2026-08-03 10:00:00+08');`,
		accept: `INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110203-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '超商取貨', '統一超商', 8000, 0, TIMESTAMPTZ '2026-08-03 10:00:00+08');`,
	},
	{
		constraint: "shipping_method_versions_name_present",
		reject: `INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110201-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', E'\t', '統一超商', 8000, NULL, TIMESTAMPTZ '2026-08-01 10:00:00+08');`,
		accept: `INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110201-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', E'\t超商取貨', '統一超商', 8000, NULL, TIMESTAMPTZ '2026-08-01 10:00:00+08');`,
	},
	{
		constraint: "shipping_methods_code_format",
		reject: `INSERT INTO shipping_methods (id, code) VALUES
    ('11110101-0000-4000-8000-000000000001', 'store-pickup');`,
		accept: `INSERT INTO shipping_methods (id, code) VALUES
    ('11110101-0000-4000-8000-000000000001', 'store_pickup');`,
	},
	{
		constraint: "stock_notifications_email_present",
		reject:     `INSERT INTO stock_notifications (id, variant_id, email) VALUES ('11110005-0000-4000-8000-000000000001', '44444444-4444-4444-8444-444444444444', E'\t');`,
		accept:     `INSERT INTO stock_notifications (id, variant_id, email) VALUES ('11110005-0000-4000-8000-000000000001', '44444444-4444-4444-8444-444444444444', 'ming@example.com'); INSERT INTO stock_notifications (id, variant_id, email) VALUES ('11110005-0000-4000-8000-000000000002', '44444444-4444-4444-8444-444444444444', E'\thua@example.com');`,
	},
	{
		constraint: "store_credit_entries_amount_non_zero",
		reject:     `INSERT INTO store_credit_entries (id, user_id, amount_cents, reason, idempotency_key) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 0, '客服補償', 'sce-amount-case');`,
		accept:     `INSERT INTO store_credit_entries (id, user_id, amount_cents, reason, idempotency_key) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', -1, '客服補償', 'sce-amount-case');`,
	},
	{
		constraint: "store_credit_entries_key_present",
		reject:     `INSERT INTO store_credit_entries (id, user_id, amount_cents, reason, idempotency_key) VALUES ('11110001-0000-4000-8000-000000000003', '55555555-5555-4555-8555-555555555555', 50000, '客服補償', E'\t');`,
		accept:     `INSERT INTO store_credit_entries (id, user_id, amount_cents, reason, idempotency_key) VALUES ('11110001-0000-4000-8000-000000000003', '55555555-5555-4555-8555-555555555555', 50000, '客服補償', E'\tsce-key-case');`,
	},
	{
		constraint: "store_credit_entries_reason_present",
		reject:     `INSERT INTO store_credit_entries (id, user_id, amount_cents, reason, idempotency_key) VALUES ('11110001-0000-4000-8000-000000000002', '55555555-5555-4555-8555-555555555555', 50000, E'\t', 'sce-reason-case');`,
		accept:     `INSERT INTO store_credit_entries (id, user_id, amount_cents, reason, idempotency_key) VALUES ('11110001-0000-4000-8000-000000000002', '55555555-5555-4555-8555-555555555555', 50000, E'\t客服補償', 'sce-reason-case');`,
	},
	{
		constraint: "user_identities_provider_known",
		reject:     `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110005-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'facebook', 'sub-000123');`,
		accept:     `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110005-0000-4000-8000-000000000002', '55555555-5555-4555-8555-555555555555', 'google', 'sub-000123');`,
	},
	{
		constraint: "user_identities_subject_present",
		reject:     `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110006-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', E'\t');`,
		accept:     `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110006-0000-4000-8000-000000000002', '55555555-5555-4555-8555-555555555555', 'google', E'\tsub-000123');`,
	},
	{
		constraint: "users_email_present",
		reject:     `INSERT INTO users (id, email, full_name) VALUES ('11110001-0000-4000-8000-000000000001', E'\t', '空白帳號');`,
		accept:     `INSERT INTO users (id, email, full_name) VALUES ('11110001-0000-4000-8000-000000000002', E'chen\tming@example.com', '空白帳號');`,
	},
	{
		constraint: "users_email_trimmed",
		reject:     `INSERT INTO users (id, email, full_name) VALUES ('11110002-0000-4000-8000-000000000001', ' chen@example.com', '陳美玲');`,
		accept:     `INSERT INTO users (id, email, full_name) VALUES ('11110002-0000-4000-8000-000000000002', 'chen @example.com', '陳美玲');`,
	},
	{
		constraint: "users_role_known",
		reject:     `INSERT INTO users (id, email, role) VALUES ('11110003-0000-4000-8000-000000000001', 'owner@example.com', 'owner');`,
		accept:     `INSERT INTO users (id, email, role) VALUES ('11110003-0000-4000-8000-000000000002', 'owner@example.com', 'staff');`,
	},
	{
		constraint: "warranty_registrations_unit_positive",
		reject: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000004', '66660001-0000-4000-8000-000000000000',
     0, DATE '2028-07-24');`,
		accept: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000004', '66660001-0000-4000-8000-000000000000',
     1, DATE '2028-07-24');`,
	},
}

var uniqueCases = []uniqueCase{
	{
		index: "carts_one_per_user",
		reject: `INSERT INTO carts (id, user_id, token_hash) VALUES
		 ('11112002-0000-4000-8000-000000000001','55555555-5555-4555-8555-555555555555','\x0401'),
		 ('11112003-0000-4000-8000-000000000001','55555555-5555-4555-8555-555555555555','\x0402');`,
		// The index is partial on user_id IS NOT NULL, so guest carts — which
		// are the common case — are outside it and must still be admitted.
		accept: `INSERT INTO carts (id, user_id, token_hash) VALUES
		 ('11112004-0000-4000-8000-000000000001', NULL, '\x0501'),
		 ('11112005-0000-4000-8000-000000000001', NULL, '\x0502');`,
	},
	{
		index:  "carts_token_hash_key",
		reject: `INSERT INTO carts (id, token_hash) VALUES ('11112001-0000-4000-8000-000000000001', '\x0102');`,
		accept: `INSERT INTO carts (id, token_hash) VALUES ('11112001-0000-4000-8000-000000000001', '\x0399');`,
	},
	{
		index:  "addresses_one_default_per_user",
		reject: `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110013-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '221', '新北市', '汐止區', '大同路一段 100 號', true);`,
		accept: `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110013-0000-4000-8000-000000000002', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '221', '新北市', '汐止區', '大同路一段 100 號', false);`,
	},
	{
		index:  "brands_slug_key",
		reject: `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'pixelight', '流明科技');`,
		accept: `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'pixelight-tw', '流明科技');`,
	},
	{
		index:  "categories_slug_key",
		reject: `INSERT INTO categories (id, slug, name) VALUES ('22220001-0000-4000-8000-000000000001', 'phones', '平板');`,
		accept: `INSERT INTO categories (id, slug, name) VALUES ('22220001-0000-4000-8000-000000000001', 'phones-pro', '平板');`,
	},
	{
		index:  "checkout_attempts_order_key",
		reject: `INSERT INTO checkout_attempts (idempotency_key, cart_id, order_id) VALUES ('checkout-a1', 'bbbb1111-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666'); INSERT INTO checkout_attempts (idempotency_key, cart_id, order_id) VALUES ('checkout-a2', 'bbbb1111-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666');`,
		accept: `INSERT INTO checkout_attempts (idempotency_key, cart_id, order_id) VALUES ('checkout-a1', 'bbbb1111-0000-4000-8000-000000000000', '66666666-6666-4666-8666-666666666666'); INSERT INTO checkout_attempts (idempotency_key, cart_id, order_id) VALUES ('checkout-a2', 'bbbb1111-0000-4000-8000-000000000000', '6666aaaa-6666-4666-8666-666666666666');`,
	},
	{
		index:  "faq_entries_position_key",
		reject: `INSERT INTO faq_entries (id, category, question, answer, position) VALUES ('11140004-0000-4000-8000-000000000001', '運送', '可以指定到貨時段嗎?', '可於結帳時選擇時段。', 0);`,
		accept: `INSERT INTO faq_entries (id, category, question, answer, position) VALUES ('11140004-0000-4000-8000-000000000001', '付款', '可以指定到貨時段嗎?', '可於結帳時選擇時段。', 0);`,
	},
	{
		index:  "hero_slides_position_key",
		reject: `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position) VALUES ('11110006-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', 0);`,
		accept: `INSERT INTO hero_slides (id, headline, primary_cta_label, primary_cta_href, position) VALUES ('11110006-0000-4000-8000-000000000001', '春季新機上市', '立即選購', '/c/laptops', 1);`,
	},
	{
		index:  "inventory_movements_idempotency_key",
		reject: `INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000004', '44444444-4444-4444-8444-444444444444', 3, 'receipt', 'im-unique-case'); INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000005', '4444aaaa-4444-4444-8444-444444444444', 5, 'return', 'im-unique-case');`,
		accept: `INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000004', '44444444-4444-4444-8444-444444444444', 3, 'receipt', 'im-unique-case'); INSERT INTO inventory_movements (id, variant_id, delta, reason, idempotency_key) VALUES ('11110002-0000-4000-8000-000000000005', '4444aaaa-4444-4444-8444-444444444444', 5, 'return', 'im-unique-case-2');`,
	},
	{
		index:  "inventory_reservations_order_variant_key",
		reject: `INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, expires_at) VALUES ('11110003-0000-4000-8000-000000000004', '66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 2, now() + interval '15 minutes'); INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, expires_at) VALUES ('11110003-0000-4000-8000-000000000005', '66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, now() + interval '30 minutes');`,
		accept: `INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, expires_at) VALUES ('11110003-0000-4000-8000-000000000004', '66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 2, now() + interval '15 minutes'); INSERT INTO inventory_reservations (id, order_id, variant_id, quantity, expires_at) VALUES ('11110003-0000-4000-8000-000000000005', '66666666-6666-4666-8666-666666666666', '4444aaaa-4444-4444-8444-444444444444', 1, now() + interval '30 minutes');`,
	},
	{
		index:  "invoice_documents_number_key",
		reject: `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110007-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031288', 100000, 'issued', NULL);`,
		accept: `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, voided_at) VALUES ('11110007-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-72031289', 100000, 'issued', NULL);`,
	},
	{
		index:  "newsletter_subscribers_email_key",
		reject: `INSERT INTO newsletter_subscribers (id, email) VALUES ('11160003-0000-4000-8000-000000000001', 'Reader@Example.com'); INSERT INTO newsletter_subscribers (id, email) VALUES ('11160004-0000-4000-8000-000000000001', 'reader@example.com');`,
		accept: `INSERT INTO newsletter_subscribers (id, email) VALUES ('11160003-0000-4000-8000-000000000001', 'Reader@Example.com'); INSERT INTO newsletter_subscribers (id, email) VALUES ('11160004-0000-4000-8000-000000000001', 'reader2@example.com');`,
	},
	{
		index:  "order_lines_position_key",
		reject: `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110005-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 0);`,
		accept: `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110005-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-9P-512-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 1);`,
	},
	{
		index:  "order_shipments_tracking_key",
		reject: `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110009-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-8871');`,
		accept: `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110009-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '新竹物流', '903-2214-8871'), ('11110009-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-8875');`,
	},
	{
		index: "orders_number_key",
		reject: `INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name) VALUES
    ('11110014-0000-4000-8000-000000000001', 'GO-260721-000387', 'home_delivery', '宅配到府');`,
		accept: `SET CONSTRAINTS orders_have_lines DEFERRED;
INSERT INTO orders (id, order_number, shipping_method_code, shipping_method_name) VALUES
    ('11110014-0000-4000-8000-000000000001', 'GO-260722-000014', 'home_delivery', '宅配到府');
INSERT INTO order_lines (id, order_id, sku, product_name, variant_label,
                         unit_price_cents, quantity, position) VALUES
    ('22220014-0000-4000-8000-000000000001', '11110014-0000-4000-8000-000000000001',
     'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', '星霧藍 256GB', 3390000, 1, 0);
INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                postal_code, city, district, street) VALUES
    ('11110014-0000-4000-8000-000000000001', 'meiling@example.com', '陳美玲', '0922333444',
     '110', '台北市', '信義區', '松智路 17 號');
SET CONSTRAINTS orders_have_lines IMMEDIATE;`,
	},
	{
		index: "outbox_messages_dedupe_key",
		reject: `INSERT INTO outbox_messages (id, topic, dedupe_key, payload) VALUES
    ('11110005-0000-4000-8000-000000000001', 'order.paid',
     'order:66666666-6666-4666-8666-666666666666',
     '{"order_number": "GO-260721-000387"}'::jsonb),
    ('11110005-0000-4000-8000-000000000002', 'order.paid',
     'order:66666666-6666-4666-8666-666666666666',
     '{"order_number": "GO-260721-000387"}'::jsonb);`,
		accept: `INSERT INTO outbox_messages (id, topic, dedupe_key, payload) VALUES
    ('11110005-0000-4000-8000-000000000001', 'order.paid',
     'order:66666666-6666-4666-8666-666666666666',
     '{"order_number": "GO-260721-000387"}'::jsonb),
    ('11110005-0000-4000-8000-000000000002', 'order.invoiced',
     'order:66666666-6666-4666-8666-666666666666',
     '{"order_number": "GO-260721-000387"}'::jsonb);`,
	},
	{
		index: "payments_one_capture_per_order",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at)
VALUES ('11110008-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666',
        'pi_second_capture', 'succeeded', 6788000, 6788000, now());`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
VALUES ('11110008-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666',
        'pi_second_capture', 'failed', 6788000);`,
	},
	{
		index: "payments_provider_ref_key",
		reject: `INSERT INTO payments (id, order_id, provider, provider_ref, status, intended_amount_cents)
VALUES ('11110009-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'stripe', 'pi_fixture', 'processing', 3690000);`,
		accept: `INSERT INTO payments (id, order_id, provider, provider_ref, status, intended_amount_cents)
VALUES ('11110009-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'stripe', 'pi_fixture_2', 'processing', 3690000);`,
	},
	{
		index:  "product_images_position_key",
		reject: `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-back.webp', 'Pixelight 9 Pro 星霧藍背面', 0);`,
		accept: `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-back.webp', 'Pixelight 9 Pro 星霧藍背面', 1);`,
	},
	{
		index:  "product_images_storage_key_key",
		reject: `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-front.webp', 'Pixelight 9 Pro 星霧藍背面', 1);`,
		accept: `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('dddd0002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-back.webp', 'Pixelight 9 Pro 星霧藍背面', 1);`,
	},
	{
		index:  "product_option_values_option_key",
		reject: `ALTER TABLE product_option_values DROP CONSTRAINT product_option_values_pkey; INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('bbbb0001-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', '午夜綠');`,
		accept: `ALTER TABLE product_option_values DROP CONSTRAINT product_option_values_pkey; INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('11110012-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', '午夜綠');`,
	},
	{
		index:  "product_option_values_value_key",
		reject: `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('66666666-6666-4666-8666-666666666666', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', '星霧藍');`,
		accept: `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('66666666-6666-4666-8666-666666666666', '33333333-3333-4333-8333-333333333333', 'aaaa0002-0000-4000-8000-000000000000', '星霧藍');`,
	},
	{
		index:  "product_options_name_key",
		reject: `INSERT INTO product_options (id, product_id, name) VALUES ('11110013-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '顏色');`,
		accept: `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('11110020-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '2222aaaa-2222-4222-8222-222222222222', 'pixelight-air', 'Pixelight Air'); INSERT INTO product_options (id, product_id, name) VALUES ('11110013-0000-4000-8000-000000000001', '11110020-0000-4000-8000-000000000001', '顏色'); INSERT INTO product_options (id, product_id, name) VALUES ('11110024-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '尺寸');`,
	},
	{
		index:  "product_options_product_key",
		reject: `ALTER TABLE product_options DROP CONSTRAINT product_options_pkey; INSERT INTO product_options (id, product_id, name) VALUES ('aaaa0001-0000-4000-8000-000000000000', '33333333-3333-4333-8333-333333333333', '尺寸');`,
		accept: `ALTER TABLE product_options DROP CONSTRAINT product_options_pkey; INSERT INTO product_options (id, product_id, name) VALUES ('11110014-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '尺寸');`,
	},
	{
		index:  "product_reviews_author_key",
		reject: `INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110021-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 5, '第一則評價:非常滿意。'); INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110022-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 3, '第二則評價:續航普通。');`,
		accept: `INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110021-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 5, '第一則評價:非常滿意。'); INSERT INTO product_reviews (id, product_id, user_id, rating, body) VALUES ('11110022-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '5555aaaa-5555-4555-8555-555555555555', 3, '第二則評價:續航普通。');`,
	},
	{
		index:  "product_specs_position_key",
		reject: `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110015-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', '4nm 八核心', 0);`,
		accept: `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('11110022-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '2222aaaa-2222-4222-8222-222222222222', 'pixelight-air', 'Pixelight Air'); INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110015-0000-4000-8000-000000000001', '11110022-0000-4000-8000-000000000001', '處理器', '4nm 八核心', 0); INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110026-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', '4nm 八核心', 1);`,
	},
	{
		index:  "product_variants_position_key",
		reject: `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110016-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-SV', 3390000, 0);`,
		accept: `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('11110021-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '2222aaaa-2222-4222-8222-222222222222', 'pixelight-air', 'Pixelight Air'); INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110016-0000-4000-8000-000000000001', '11110021-0000-4000-8000-000000000001', 'PXL-9P-128-SV', 3390000, 0); INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110025-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-SB', 3390000, 2);`,
	},
	{
		index:  "product_variants_product_key",
		reject: `ALTER TABLE product_variants DROP CONSTRAINT product_variants_pkey CASCADE; INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('44444444-4444-4444-8444-444444444444', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-GD', 3390000, 15);`,
		accept: `ALTER TABLE product_variants DROP CONSTRAINT product_variants_pkey CASCADE; INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110017-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-128-GD', 3390000, 15);`,
	},
	{
		index:  "product_variants_sku_key",
		reject: `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('11110023-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '2222aaaa-2222-4222-8222-222222222222', 'pixelight-air', 'Pixelight Air'); INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110018-0000-4000-8000-000000000001', '11110023-0000-4000-8000-000000000001', 'PXL-9P-256-BL', 3390000, 0);`,
		accept: `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('11110023-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '2222aaaa-2222-4222-8222-222222222222', 'pixelight-air', 'Pixelight Air'); INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110018-0000-4000-8000-000000000001', '11110023-0000-4000-8000-000000000001', 'PXL-9P-256-GY', 3390000, 0);`,
	},
	{
		index:  "products_id_self_key",
		reject: `ALTER TABLE products DROP CONSTRAINT products_pkey CASCADE; INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33333333-3333-4333-8333-333333333333', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', '光素 9');`,
		accept: `ALTER TABLE products DROP CONSTRAINT products_pkey CASCADE; INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9', '光素 9');`,
	},
	{
		index:  "products_slug_key",
		reject: `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9-pro', '光素 9 Pro 複製');`,
		accept: `INSERT INTO products (id, brand_id, category_id, slug, name) VALUES ('33330001-0000-4000-8000-000000000001', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'pixelight-9-pro-max', '光素 9 Pro 複製');`,
	},
	{
		index: "refunds_provider_ref_key",
		reject: `INSERT INTO refunds (id, payment_id, request_key, provider_ref, status, amount_cents, reason, succeeded_at)
VALUES ('22220007-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-ref-0001', 're_dup_0001', 'succeeded', 100000, '七天鑑賞期退貨', now());
INSERT INTO refunds (id, payment_id, request_key, provider_ref, status, amount_cents, reason, succeeded_at)
VALUES ('22220007-0000-4000-8000-000000000002', '77770001-0000-4000-8000-000000000000',
        'rk-ref-0002', 're_dup_0001', 'succeeded', 100000, '七天鑑賞期退貨', now());`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, provider_ref, status, amount_cents, reason)
VALUES ('22220007-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-ref-0001', NULL, 'pending', 100000, '七天鑑賞期退貨');
INSERT INTO refunds (id, payment_id, request_key, provider_ref, status, amount_cents, reason)
VALUES ('22220007-0000-4000-8000-000000000002', '77770001-0000-4000-8000-000000000000',
        'rk-ref-0002', NULL, 'pending', 100000, '七天鑑賞期退貨');`,
	},
	{
		index: "refunds_request_key_key",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents,
                      captured_amount_cents, paid_at)
VALUES ('11110010-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_idem_second', 'succeeded', 3690000, 3690000, now());
INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220006-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-idem-0001', 'pending', 100000, '七天鑑賞期退貨');
INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220006-0000-4000-8000-000000000002', '11110010-0000-4000-8000-000000000001',
        'rk-idem-0001', 'pending', 100000, '七天鑑賞期退貨');`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents,
                      captured_amount_cents, paid_at)
VALUES ('11110010-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666',
        'pi_idem_second', 'succeeded', 3690000, 3690000, now());
INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220006-0000-4000-8000-000000000001', '77770001-0000-4000-8000-000000000000',
        'rk-idem-0001', 'pending', 100000, '七天鑑賞期退貨');
INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, reason)
VALUES ('22220006-0000-4000-8000-000000000002', '11110010-0000-4000-8000-000000000001',
        'rk-idem-0002', 'pending', 100000, '七天鑑賞期退貨');`,
	},
	{
		index:  "sale_campaigns_slug_key",
		reject: `INSERT INTO sale_campaigns (id, slug, title, ends_at) VALUES ('11130004-0000-4000-8000-000000000001', 'summer', '夏季開學祭續辦', '2026-10-01 00:00:00+08');`,
		accept: `INSERT INTO sale_campaigns (id, slug, title, ends_at) VALUES ('11130004-0000-4000-8000-000000000001', 'summer-2', '夏季開學祭續辦', '2026-10-01 00:00:00+08');`,
	},
	{
		index: "shipping_method_versions_effective_key",
		reject: `INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110204-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '超商取貨', '統一超商', 8000, NULL, TIMESTAMPTZ '2026-08-04 10:00:00+08');
INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110205-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '超商取貨(冷藏)', '統一超商', 8000, NULL, TIMESTAMPTZ '2026-08-04 10:00:00+08');`,
		accept: `INSERT INTO shipping_methods (id, code) VALUES
    ('11110103-0000-4000-8000-000000000001', 'locker_pickup');
INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110204-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '超商取貨', '統一超商', 8000, NULL, TIMESTAMPTZ '2026-08-04 10:00:00+08');
INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110205-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '超商取貨(冷藏)', '統一超商', 8000, NULL, TIMESTAMPTZ '2026-08-05 10:00:00+08');
INSERT INTO shipping_method_versions
    (id, method_id, name, carrier, fee_cents, free_over_cents, effective_at) VALUES
    ('11110206-0000-4000-8000-000000000001', '11110103-0000-4000-8000-000000000001', '超商寄放', '全家', 6000, NULL, TIMESTAMPTZ '2026-08-04 10:00:00+08');`,
	},
	{
		index: "shipping_methods_code_key",
		reject: `INSERT INTO shipping_methods (id, code) VALUES
    ('11110102-0000-4000-8000-000000000001', 'home_delivery');`,
		accept: `INSERT INTO shipping_methods (id, code) VALUES
    ('11110102-0000-4000-8000-000000000001', 'convenience_store');`,
	},
	{
		index:  "stock_notifications_pending_key",
		reject: `INSERT INTO stock_notifications (id, variant_id, email) VALUES ('11110031-0000-4000-8000-000000000001', '44444444-4444-4444-8444-444444444444', 'Ming@Example.com'); INSERT INTO stock_notifications (id, variant_id, email) VALUES ('11110032-0000-4000-8000-000000000001', '44444444-4444-4444-8444-444444444444', 'ming@example.com');`,
		accept: `INSERT INTO stock_notifications (id, variant_id, email, notified_at) VALUES ('11110031-0000-4000-8000-000000000001', '44444444-4444-4444-8444-444444444444', 'Ming@Example.com', now()); INSERT INTO stock_notifications (id, variant_id, email) VALUES ('11110032-0000-4000-8000-000000000001', '44444444-4444-4444-8444-444444444444', 'ming@example.com');`,
	},
	{
		index:  "store_credit_entries_idempotency_key",
		reject: `INSERT INTO store_credit_accounts (user_id) VALUES ('5555aaaa-5555-4555-8555-555555555555'); INSERT INTO store_credit_entries (id, user_id, amount_cents, reason, idempotency_key) VALUES ('11110001-0000-4000-8000-000000000004', '5555aaaa-5555-4555-8555-555555555555', 50000, '客服補償', 'fixture-grant');`,
		accept: `INSERT INTO store_credit_accounts (user_id) VALUES ('5555aaaa-5555-4555-8555-555555555555'); INSERT INTO store_credit_entries (id, user_id, amount_cents, reason, idempotency_key) VALUES ('11110001-0000-4000-8000-000000000004', '5555aaaa-5555-4555-8555-555555555555', 50000, '客服補償', 'fixture-grant-2');`,
	},
	{
		index:  "user_identities_provider_subject_key",
		reject: `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110007-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', 'sub-000777'); INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110007-0000-4000-8000-000000000002', '5555aaaa-5555-4555-8555-555555555555', 'google', 'sub-000777');`,
		accept: `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110007-0000-4000-8000-000000000003', '55555555-5555-4555-8555-555555555555', 'google', 'sub-000777'); INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110007-0000-4000-8000-000000000004', '5555aaaa-5555-4555-8555-555555555555', 'google', 'sub-000778');`,
	},
	{
		index:  "user_identities_user_provider_key",
		reject: `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110008-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', 'sub-000881'); INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110008-0000-4000-8000-000000000002', '55555555-5555-4555-8555-555555555555', 'google', 'sub-000882');`,
		accept: `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110008-0000-4000-8000-000000000003', '55555555-5555-4555-8555-555555555555', 'google', 'sub-000881'); INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110008-0000-4000-8000-000000000004', '5555aaaa-5555-4555-8555-555555555555', 'google', 'sub-000882');`,
	},
	{
		index:  "users_email_key",
		reject: `INSERT INTO users (id, email, full_name) VALUES ('11110004-0000-4000-8000-000000000001', 'MING@EXAMPLE.COM', '王小明的分身');`,
		accept: `INSERT INTO users (id, email, full_name) VALUES ('11110004-0000-4000-8000-000000000002', 'MING2@EXAMPLE.COM', '王小明的分身');`,
	},
	{
		index: "warranty_registrations_serial_key",
		reject: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000007', '66660001-0000-4000-8000-000000000000',
     1, 'PXL9P-SN-000001', DATE '2028-07-24');
INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000008', '6666a001-0000-4000-8000-000000000000',
     1, 'PXL9P-SN-000001', DATE '2028-07-24');`,
		accept: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000007', '66660001-0000-4000-8000-000000000000',
     1, 'PXL9P-SN-000001', DATE '2028-07-24');
INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000008', '6666a001-0000-4000-8000-000000000000',
     1, 'PXL9P-SN-000002', DATE '2028-07-24');
INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000009', '66660001-0000-4000-8000-000000000000',
     2, NULL, DATE '2028-07-24');`,
	},
	{
		index: "warranty_registrations_unit_key",
		reject: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000005', '66660001-0000-4000-8000-000000000000',
     1, NULL, DATE '2028-07-24');
INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000006', '66660001-0000-4000-8000-000000000000',
     1, NULL, DATE '2028-07-24');`,
		accept: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000005', '66660001-0000-4000-8000-000000000000',
     1, NULL, DATE '2028-07-24');
INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000006', '66660001-0000-4000-8000-000000000000',
     2, NULL, DATE '2028-07-24');
INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES
    ('11110001-0000-4000-8000-000000000009', '6666a001-0000-4000-8000-000000000000',
     1, NULL, DATE '2028-07-24');`,
	},
}
