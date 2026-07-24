//go:build integration

package db_test

// One case per constraint, each written and adversarially re-verified against a
// live PostgreSQL 18 before being checked in. Ordinary source from here on.
//
// The catalog is the authority on what must be covered, not this list:
// TestEveryCheckConstraintIsExercised and TestEveryUniqueConstraintIsExercised
// read pg_constraint and pg_index and fail when anything here is missing or
// names a constraint the database does not have. That gate is what keeps this
// file from drifting behind the schema.

var checkCases = []checkCase{
	{
		constraint: "addresses_city_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', E'\t', '信義區', '松高路 68 號', false);`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', false);`,
	},
	{
		constraint: "addresses_district_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', E'\t', '松高路 68 號', false);`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', false);`,
	},
	{
		constraint: "addresses_phone_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', E'\t', '110', '台北市', '信義區', '松高路 68 號', false);`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', false);`,
	},
	{
		constraint: "addresses_postal_code_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', E'\t', '台北市', '信義區', '松高路 68 號', false);`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', false);`,
	},
	{
		constraint: "addresses_recipient_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', E'\t', '0912345678', '110', '台北市', '信義區', '松高路 68 號', false);`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', false);`,
	},
	{
		constraint: "addresses_street_present",
		reject:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', '信義區', E'\t', false);`,
		accept:     `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', false);`,
	},
	{
		constraint: "audit_events_action_present",
		reject:     `INSERT INTO audit_events (action, entity_table) VALUES (E'\t', 'orders');`,
		accept:     `INSERT INTO audit_events (action, entity_table) VALUES ('order.cancel', 'orders');`,
	},
	{
		constraint: "audit_events_entity_present",
		reject:     `INSERT INTO audit_events (action, entity_table) VALUES ('order.cancel', E'\t');`,
		accept:     `INSERT INTO audit_events (action, entity_table) VALUES ('order.cancel', 'orders');`,
	},
	{
		constraint: "brands_name_present",
		reject:     `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'acme', E'	');`,
		accept:     `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'acme', '宏碁');`,
	},
	{
		constraint: "brands_slug_format",
		reject:     `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'Acme', '宏碁');`,
		accept:     `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'acme', '宏碁');`,
	},
	{
		constraint: "cart_items_quantity_in_range",
		reject:     `INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ('bbbb1111-0000-4000-8000-000000000000', '44444444-4444-4444-8444-444444444444', 0);`,
		accept:     `INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ('bbbb1111-0000-4000-8000-000000000000', '44444444-4444-4444-8444-444444444444', 1);`,
	},
	{
		constraint: "categories_name_present",
		reject:     `INSERT INTO categories (id, slug, name) VALUES ('11110002-0000-4000-8000-000000000001', 'tablets', E'	');`,
		accept:     `INSERT INTO categories (id, slug, name) VALUES ('11110002-0000-4000-8000-000000000001', 'tablets', '平板');`,
	},
	{
		constraint: "categories_not_own_parent",
		reject:     `SET LOCAL session_replication_role = replica; INSERT INTO categories (id, parent_id, slug, name) VALUES ('11110002-0000-4000-8000-000000000001', '11110002-0000-4000-8000-000000000001', 'tablets', '平板');`,
		accept:     `SET LOCAL session_replication_role = replica; INSERT INTO categories (id, parent_id, slug, name) VALUES ('11110002-0000-4000-8000-000000000001', '22222222-2222-4222-8222-222222222222', 'tablets', '平板');`,
	},
	{
		constraint: "categories_slug_format",
		reject:     `INSERT INTO categories (id, slug, name) VALUES ('11110002-0000-4000-8000-000000000001', 'Tablets', '平板');`,
		accept:     `INSERT INTO categories (id, slug, name) VALUES ('11110002-0000-4000-8000-000000000001', 'tablets', '平板');`,
	},
	{
		constraint: "checkout_attempts_key_present",
		reject:     `INSERT INTO checkout_attempts (idempotency_key) VALUES (E'\t');`,
		accept:     `INSERT INTO checkout_attempts (idempotency_key) VALUES ('co_11110001-0000');`,
	},
	{
		constraint: "contact_messages_email_present",
		reject:     `INSERT INTO contact_messages (name, email, subject, message) VALUES ('王小明', E'\t', '訂單問題', '請問出貨時間');`,
		accept:     `INSERT INTO contact_messages (name, email, subject, message) VALUES ('王小明', 'ming@example.com', '訂單問題', '請問出貨時間');`,
	},
	{
		constraint: "contact_messages_message_present",
		reject:     `INSERT INTO contact_messages (name, email, subject, message) VALUES ('王小明', 'ming@example.com', '訂單問題', E'\t');`,
		accept:     `INSERT INTO contact_messages (name, email, subject, message) VALUES ('王小明', 'ming@example.com', '訂單問題', '請問出貨時間');`,
	},
	{
		constraint: "contact_messages_name_present",
		reject:     `INSERT INTO contact_messages (name, email, subject, message) VALUES (E'\t', 'ming@example.com', '訂單問題', '請問出貨時間');`,
		accept:     `INSERT INTO contact_messages (name, email, subject, message) VALUES ('王小明', 'ming@example.com', '訂單問題', '請問出貨時間');`,
	},
	{
		constraint: "contact_messages_subject_present",
		reject:     `INSERT INTO contact_messages (name, email, subject, message) VALUES ('王小明', 'ming@example.com', E'\t', '請問出貨時間');`,
		accept:     `INSERT INTO contact_messages (name, email, subject, message) VALUES ('王小明', 'ming@example.com', '訂單問題', '請問出貨時間');`,
	},
	{
		constraint: "faq_entries_answer_present",
		reject:     `INSERT INTO faq_entries (category, question, answer, position) VALUES ('付款', '如何付款?', E'\t', 0);`,
		accept:     `INSERT INTO faq_entries (category, question, answer, position) VALUES ('付款', '如何付款?', '可用信用卡。', 0);`,
	},
	{
		constraint: "faq_entries_category_present",
		reject:     `INSERT INTO faq_entries (category, question, answer, position) VALUES (E'\t', '如何付款?', '可用信用卡。', 0);`,
		accept:     `INSERT INTO faq_entries (category, question, answer, position) VALUES ('付款', '如何付款?', '可用信用卡。', 0);`,
	},
	{
		constraint: "faq_entries_question_present",
		reject:     `INSERT INTO faq_entries (category, question, answer, position) VALUES ('付款', E'\t', '可用信用卡。', 0);`,
		accept:     `INSERT INTO faq_entries (category, question, answer, position) VALUES ('付款', '如何付款?', '可用信用卡。', 0);`,
	},
	{
		constraint: "hero_slides_headline_present",
		reject:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position) VALUES (E'\t', '立即選購', '/c/phones', 5);`,
		accept:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position) VALUES ('夏季新機', '立即選購', '/c/phones', 5);`,
	},
	{
		constraint: "hero_slides_image_has_alt",
		reject:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, image_key, image_alt, position) VALUES ('夏季新機', '立即選購', '/c/phones', 'hero.webp', E'\t', 5);`,
		accept:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, image_key, image_alt, position) VALUES ('夏季新機', '立即選購', '/c/phones', 'hero.webp', '首頁主視覺', 5);`,
	},
	{
		constraint: "hero_slides_primary_cta_present",
		reject:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position) VALUES ('夏季新機', E'\t', '/c/phones', 5);`,
		accept:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position) VALUES ('夏季新機', '立即選購', '/c/phones', 5);`,
	},
	{
		constraint: "hero_slides_secondary_cta_complete",
		reject:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, secondary_cta_label, secondary_cta_href, position) VALUES ('夏季新機', '立即選購', '/c/phones', '了解更多', NULL, 5);`,
		accept:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, secondary_cta_label, secondary_cta_href, position) VALUES ('夏季新機', '立即選購', '/c/phones', '了解更多', '/about', 5);`,
	},
	{
		constraint: "hero_slides_window_ordered",
		reject:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position, starts_at, ends_at) VALUES ('夏季新機', '立即選購', '/c/phones', 5, '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z');`,
		accept:     `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position, starts_at, ends_at) VALUES ('夏季新機', '立即選購', '/c/phones', 5, '2026-08-01T00:00:00Z', '2026-08-02T00:00:00Z');`,
	},
	{
		constraint: "inventory_movements_delta_non_zero",
		reject: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 0, 'adjustment', 'im-delta-1');`,
		accept: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 1, 'adjustment', 'im-delta-1');`,
	},
	{
		constraint: "inventory_movements_delta_direction",
		// A sale takes stock out, so its delta must be negative; a positive
		// 'sale' would post a backwards ledger entry. The neighbour is the same
		// sale with the correct sign.
		reject: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 5, 'sale', 'im-dir-1');`,
		accept: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', -5, 'sale', 'im-dir-1');`,
	},
	{
		constraint: "inventory_movements_key_present",
		reject: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 1, 'adjustment', E'\t');`,
		accept: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 1, 'adjustment', E'\tim-key');`,
	},
	{
		constraint: "inventory_movements_reason_known",
		reject: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 1, 'purchase', 'im-reason-1');`,
		accept: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 1, 'receipt', 'im-reason-1');`,
	},
	{
		constraint: "inventory_reservations_expiry_after_creation",
		reject: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, created_at, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1,
        TIMESTAMPTZ '2026-01-01 00:00:00+08', TIMESTAMPTZ '2026-01-01 00:00:00+08');`,
		accept: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, created_at, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1,
        TIMESTAMPTZ '2026-01-01 00:00:00+08', TIMESTAMPTZ '2026-01-01 00:00:01+08');`,
	},
	{
		constraint: "inventory_reservations_quantity_positive",
		reject: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 0, now() + interval '1 hour');`,
		accept: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, now() + interval '1 hour');`,
	},
	{
		constraint: "inventory_reservations_settled_has_state",
		reject: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, state, settled_at, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, 'held', now(), now() + interval '1 hour');`,
		accept: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, state, settled_at, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, 'held', NULL, now() + interval '1 hour');`,
	},
	{
		constraint: "inventory_reservations_state_known",
		reject: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, state, settled_at, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, 'pending', now(), now() + interval '1 hour');`,
		accept: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, state, settled_at, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, 'consumed', now(), now() + interval '1 hour');`,
	},
	{
		constraint: "invoice_document_lines_amount_non_negative",
		reject:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000003', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 100, -1, 'taxable', 0);`,
		accept:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000003', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 100, 0, 'taxable', 0);`,
	},
	{
		constraint: "invoice_document_lines_unit_price_in_range",
		reject:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000007', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, -1, 0, 'taxable', 0);`,
		accept:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000007', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 0, 0, 'taxable', 0);`,
	},
	{
		constraint: "invoice_document_lines_amount_in_range",
		reject:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000008', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 100, 10000000001, 'taxable', 0);`,
		accept:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000008', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 100, 10000000000, 'taxable', 0);`,
	},
	{
		constraint: "invoice_document_lines_description_present",
		reject:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000001', '99990001-0000-4000-8000-000000000000', E'	', 1, 100, 100, 'taxable', 0);`,
		accept:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000001', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G 星霧藍 256GB', 1, 100, 100, 'taxable', 0);`,
	},
	{
		constraint: "invoice_document_lines_quantity_positive",
		reject:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000002', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 0, 100, 100, 'taxable', 0);`,
		accept:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000002', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 100, 100, 'taxable', 0);`,
	},
	{
		constraint: "invoice_document_lines_tax_type_known",
		reject:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000004', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 100, 100, 'vat', 0);`,
		accept:     `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000004', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 100, 100, 'zero_rated', 0);`,
	},
	{
		constraint: "invoice_documents_allowance_has_original",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000006', '6666aaaa-6666-4666-8666-666666666666', 'invoice', '99990001-0000-4000-8000-000000000000', 'GD-90000006', 100);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000006', '6666aaaa-6666-4666-8666-666666666666', 'invoice', NULL, 'GD-90000006', 100);`,
	},
	{
		constraint: "invoice_documents_amount_positive",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000002', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-90000002', 0);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000002', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-90000002', 1);`,
	},
	{
		constraint: "invoice_documents_kind_known",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'receipt', 'GD-90000001', 100);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-90000001', 100);`,
	},
	{
		constraint: "invoice_documents_number_present",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000003', '6666aaaa-6666-4666-8666-666666666666', 'invoice', E'	', 100);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000003', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-90000003', 100);`,
	},
	{
		constraint: "invoice_documents_status_known",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, status) VALUES ('11110001-0000-4000-8000-000000000004', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-90000004', 100, 'draft');`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, status) VALUES ('11110001-0000-4000-8000-000000000004', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-90000004', 100, 'issued');`,
	},
	{
		constraint: "invoice_documents_voided_has_time",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, status, voided_at) VALUES ('11110001-0000-4000-8000-000000000005', '66666666-6666-4666-8666-666666666666', 'invoice', 'GD-90000005', 100, 'voided', NULL);`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, status, voided_at) VALUES ('11110001-0000-4000-8000-000000000005', '66666666-6666-4666-8666-666666666666', 'invoice', 'GD-90000005', 100, 'voided', now());`,
	},
	{
		constraint: "invoice_preferences_company_has_tax_id",
		reject:     `INSERT INTO invoice_preferences (order_id, invoice_type, tax_id) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'company', NULL);`,
		accept:     `INSERT INTO invoice_preferences (order_id, invoice_type, tax_id) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'company', '12345678');`,
	},
	{
		constraint: "invoice_preferences_mobile_has_carrier",
		reject:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'mobile_carrier', E'	');`,
		accept:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'mobile_carrier', '/AB12345');`,
	},
	{
		constraint: "invoice_preferences_type_known",
		reject:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'paper', NULL, NULL);`,
		accept:     `INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'member_carrier', NULL, NULL);`,
	},
	{
		constraint: "newsletter_subscribers_email_present",
		reject:     `INSERT INTO newsletter_subscribers (email) VALUES (E'\t');`,
		accept:     `INSERT INTO newsletter_subscribers (email) VALUES ('sub@example.com');`,
	},
	{
		constraint: "newsletter_subscribers_email_trimmed",
		reject:     `INSERT INTO newsletter_subscribers (email) VALUES (' sub@example.com');`,
		accept:     `INSERT INTO newsletter_subscribers (email) VALUES ('sub@example.com');`,
	},
	{
		constraint: "order_events_kind_known",
		reject:     `INSERT INTO order_events (id, order_id, kind) VALUES ('11110001-0000-4000-8000-000000000020', '66666666-6666-4666-8666-666666666666', 'bogus');`,
		accept:     `INSERT INTO order_events (id, order_id, kind) VALUES ('11110001-0000-4000-8000-000000000020', '66666666-6666-4666-8666-666666666666', 'placed');`,
	},
	{
		constraint: "order_lines_product_name_present",
		reject:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000002', '6666aaaa-6666-4666-8666-666666666666', 'PXL-TEST-BL', E'\t', 3690000, 1, 5);`,
		accept:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000002', '6666aaaa-6666-4666-8666-666666666666', 'PXL-TEST-BL', '測試手機', 3690000, 1, 5);`,
	},
	{
		constraint: "order_lines_quantity_in_range",
		reject:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000003', '6666aaaa-6666-4666-8666-666666666666', 'PXL-TEST-BL', 'Pixelight 9 Pro 5G', 3690000, 0, 5);`,
		accept:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000003', '6666aaaa-6666-4666-8666-666666666666', 'PXL-TEST-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 5);`,
	},
	{
		constraint: "order_lines_sku_present",
		reject:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', E'\t', 'Pixelight 9 Pro 5G', 3690000, 1, 5);`,
		accept:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'PXL-TEST-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 5);`,
	},
	{
		constraint: "order_lines_unit_price_in_range",
		reject:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000004', '6666aaaa-6666-4666-8666-666666666666', 'PXL-TEST-BL', 'Pixelight 9 Pro 5G', -1, 1, 5);`,
		accept:     `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000004', '6666aaaa-6666-4666-8666-666666666666', 'PXL-TEST-BL', 'Pixelight 9 Pro 5G', 0, 1, 5);`,
	},
	{
		constraint: "order_number_counters_in_range",
		reject:     `INSERT INTO order_number_counters (business_date, last_no) VALUES ('2026-07-24', 0);`,
		accept:     `INSERT INTO order_number_counters (business_date, last_no) VALUES ('2026-07-24', 1);`,
	},
	{
		constraint: "order_private_data_all_or_erased",
		reject:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', '110', '台北市', '信義區', NULL);`,
		accept:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', '110', '台北市', '信義區', '松高路 100 號');`,
	},
	{
		constraint: "order_shipment_lines_quantity_positive",
		reject:     `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity) VALUES ('66666666-6666-4666-8666-666666666666', '66660002-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 0);`,
		accept:     `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity) VALUES ('66666666-6666-4666-8666-666666666666', '66660002-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 1);`,
	},
	{
		constraint: "order_shipments_carrier_present",
		reject:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110001-0000-4000-8000-000000000010', '66666666-6666-4666-8666-666666666666', E'\t', '903-2214-0001');`,
		accept:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110001-0000-4000-8000-000000000010', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-0001');`,
	},
	{
		constraint: "order_shipments_delivered_after_shipped",
		reject:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number, shipped_at, delivered_at) VALUES ('11110001-0000-4000-8000-000000000012', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-0003', '2026-07-01 10:00:00+08', '2026-07-01 09:59:59+08');`,
		accept:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number, shipped_at, delivered_at) VALUES ('11110001-0000-4000-8000-000000000012', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-0003', '2026-07-01 10:00:00+08', '2026-07-01 10:00:00+08');`,
	},
	{
		constraint: "order_shipments_tracking_present",
		reject:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110001-0000-4000-8000-000000000011', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', E'\t');`,
		accept:     `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110001-0000-4000-8000-000000000011', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-0002');`,
	},
	{
		constraint: "orders_cancelled_after_placed",
		reject:     `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name, placed_at, cancelled_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府', '2026-07-24 12:00:00+08', '2026-07-24 11:00:00+08');`,
		accept:     `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name, placed_at, cancelled_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府', '2026-07-24 12:00:00+08', '2026-07-24 12:00:00+08');`,
	},
	{
		constraint: "orders_cancelled_has_time",
		reject:     `UPDATE orders SET fulfillment_status = 'cancelled' WHERE id = '6666aaaa-6666-4666-8666-666666666666';`,
		accept:     `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = '6666aaaa-6666-4666-8666-666666666666';`,
	},
	{
		constraint: "orders_completed_after_placed",
		reject:     `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name, placed_at, completed_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府', '2026-07-24 12:00:00+08', '2026-07-24 11:00:00+08');`,
		accept:     `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name, placed_at, completed_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府', '2026-07-24 12:00:00+08', '2026-07-24 12:00:00+08');`,
	},
	{
		constraint: "orders_completed_has_time",
		// Walks the fixture's PAID order (66666666) through fulfilment; the unpaid
		// order can no longer leave pending (orders_funded_to_leave_pending).
		reject: `UPDATE orders SET fulfillment_status = 'picking' WHERE id = '66666666-6666-4666-8666-666666666666'; UPDATE orders SET fulfillment_status = 'shipped' WHERE id = '66666666-6666-4666-8666-666666666666'; UPDATE orders SET fulfillment_status = 'completed' WHERE id = '66666666-6666-4666-8666-666666666666';`,
		accept: `UPDATE orders SET fulfillment_status = 'picking' WHERE id = '66666666-6666-4666-8666-666666666666'; UPDATE orders SET fulfillment_status = 'shipped' WHERE id = '66666666-6666-4666-8666-666666666666'; UPDATE orders SET fulfillment_status = 'completed', completed_at = now() WHERE id = '66666666-6666-4666-8666-666666666666';`,
	},
	{
		constraint: "orders_currency_is_twd",
		reject:     `INSERT INTO orders (id, currency, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'USD', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
		accept:     `INSERT INTO orders (id, currency, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'TWD', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		constraint: "orders_discount_non_negative",
		reject:     `INSERT INTO orders (id, discount_cents, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', -1, 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
		accept:     `INSERT INTO orders (id, discount_cents, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 0, 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		constraint: "orders_fulfillment_status_known",
		// The orders_start_pending INSERT trigger and orders_legal_transition
		// UPDATE trigger both shadow this CHECK: any non-'pending' status trips
		// a trigger before the CHECK is reached. session_replication_role =
		// replica disables user triggers (CHECKs still fire) for the
		// transaction, isolating the CHECK — the same technique the
		// categories_not_own_parent case uses.
		reject: `SET LOCAL session_replication_role = replica;
		         INSERT INTO orders (id, fulfillment_status, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', '在路上', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
		accept: `SET LOCAL session_replication_role = replica;
		         INSERT INTO orders (id, fulfillment_status, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'pending', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		constraint: "orders_not_both_ended",
		reject:     `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name, cancelled_at, completed_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府', now(), now());`,
		accept:     `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name, cancelled_at, completed_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府', now(), NULL);`,
	},
	{
		constraint: "orders_number_format",
		reject:     `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'GO-260724-00099', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
		accept:     `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'GO-260724-000999', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		constraint: "orders_shipping_code_present",
		// A blank code is also a code that matches no version, so the
		// orders_shipping_snapshot_matches trigger would refuse it first; disable
		// triggers to reach the presence CHECK. The neighbour has a real,
		// matching code and needs no bypass.
		reject: `SET LOCAL session_replication_role = replica;
		         INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', E'\t', '宅配到府');`,
		accept: `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		constraint: "orders_shipping_name_present",
		reject:     `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', E'\t');`,
		accept:     `INSERT INTO orders (id, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		constraint: "orders_shipping_non_negative",
		reject:     `INSERT INTO orders (id, shipping_cents, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', -1, 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
		accept:     `INSERT INTO orders (id, shipping_cents, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 0, 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		constraint: "orders_tax_non_negative",
		reject:     `INSERT INTO orders (id, tax_cents, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', -1, 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
		accept:     `INSERT INTO orders (id, tax_cents, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 0, 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		constraint: "outbox_messages_attempts_non_negative",
		reject:     `INSERT INTO outbox_messages (topic, dedupe_key, payload, attempts) VALUES ('order.confirmation', 'order-66666666-confirmation', '{}'::jsonb, -1);`,
		accept:     `INSERT INTO outbox_messages (topic, dedupe_key, payload, attempts) VALUES ('order.confirmation', 'order-66666666-confirmation', '{}'::jsonb, 0);`,
	},
	{
		constraint: "outbox_messages_topic_present",
		reject:     `INSERT INTO outbox_messages (topic, dedupe_key, payload) VALUES (E'\t', 'order-66666666-confirmation', '{}'::jsonb);`,
		accept:     `INSERT INTO outbox_messages (topic, dedupe_key, payload) VALUES ('order.confirmation', 'order-66666666-confirmation', '{}'::jsonb);`,
	},
	{
		constraint: "password_reset_tokens_expiry_after_creation",
		reject:     `INSERT INTO password_reset_tokens (token_hash, user_id, created_at, expires_at) VALUES ('\x01', '55555555-5555-4555-8555-555555555555', '2026-01-01 00:00:00+00', '2026-01-01 00:00:00+00');`,
		accept:     `INSERT INTO password_reset_tokens (token_hash, user_id, created_at, expires_at) VALUES ('\x01', '55555555-5555-4555-8555-555555555555', '2026-01-01 00:00:00+00', '2026-01-01 00:00:00.000001+00');`,
	},
	{
		constraint: "payment_webhook_events_type_present",
		reject:     `INSERT INTO payment_webhook_events (provider, event_id, type, payload) VALUES ('stripe','evt_rej_type',E'\t','{}'::jsonb);`,
		accept:     `INSERT INTO payment_webhook_events (provider, event_id, type, payload) VALUES ('stripe','evt_acc_type','payment_intent.succeeded','{}'::jsonb);`,
	},
	{
		constraint: "payments_captured_non_negative",
		reject:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents) VALUES ('11110001-0000-4000-8000-000000000004','6666aaaa-6666-4666-8666-666666666666','pi_rej_captured','processing',6788000,-1);`,
		accept:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents) VALUES ('11110001-0000-4000-8000-000000000004','6666aaaa-6666-4666-8666-666666666666','pi_acc_captured','processing',6788000,NULL);`,
	},
	{
		constraint: "payments_currency_is_twd",
		reject:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, currency) VALUES ('11110001-0000-4000-8000-000000000005','6666aaaa-6666-4666-8666-666666666666','pi_rej_currency','requires_payment',6788000,'USD');`,
		accept:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, currency) VALUES ('11110001-0000-4000-8000-000000000005','6666aaaa-6666-4666-8666-666666666666','pi_acc_currency','requires_payment',6788000,'TWD');`,
	},
	{
		constraint: "payments_intended_positive",
		reject:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES ('11110001-0000-4000-8000-000000000003','6666aaaa-6666-4666-8666-666666666666','pi_rej_intended','requires_payment',0);`,
		accept:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES ('11110001-0000-4000-8000-000000000003','6666aaaa-6666-4666-8666-666666666666','pi_acc_intended','requires_payment',1);`,
	},
	{
		constraint: "payments_intended_in_range",
		reject:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES ('11110001-0000-4000-8000-00000000000e','6666aaaa-6666-4666-8666-666666666666','pi_rej_intrange','requires_payment',10000000001);`,
		accept:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES ('11110001-0000-4000-8000-00000000000e','6666aaaa-6666-4666-8666-666666666666','pi_acc_intrange','requires_payment',10000000000);`,
	},
	{
		constraint: "payments_captured_in_range",
		// A capture over the ceiling. The complete-order and one-capture guards
		// are beside the point here, so disable triggers; the range CHECK, the
		// non-negative CHECK and succeeded_is_captured all still fire, and it is
		// captured_in_range that this row trips (intended sits on the ceiling).
		reject: `SET LOCAL session_replication_role = replica;
		         INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at) VALUES ('11110001-0000-4000-8000-00000000000f','6666aaaa-6666-4666-8666-666666666666','pi_rej_caprange','succeeded',10000000000,10000000001,now());`,
		accept: `SET LOCAL session_replication_role = replica;
		         INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at) VALUES ('11110001-0000-4000-8000-00000000000f','6666aaaa-6666-4666-8666-666666666666','pi_acc_caprange','succeeded',10000000000,10000000000,now());`,
	},
	{
		constraint: "payments_last4_format",
		reject:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, card_last4) VALUES ('11110001-0000-4000-8000-000000000006','6666aaaa-6666-4666-8666-666666666666','pi_rej_last4','requires_payment',6788000,'123');`,
		accept:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, card_last4) VALUES ('11110001-0000-4000-8000-000000000006','6666aaaa-6666-4666-8666-666666666666','pi_acc_last4','requires_payment',6788000,'4242');`,
	},
	{
		constraint: "payments_provider_known",
		reject:     `INSERT INTO payments (id, order_id, provider, provider_ref, status, intended_amount_cents) VALUES ('11110001-0000-4000-8000-000000000001','6666aaaa-6666-4666-8666-666666666666','paypal','pi_rej_provider','requires_payment',6788000);`,
		accept:     `INSERT INTO payments (id, order_id, provider, provider_ref, status, intended_amount_cents) VALUES ('11110001-0000-4000-8000-000000000001','6666aaaa-6666-4666-8666-666666666666','stripe','pi_acc_provider','requires_payment',6788000);`,
	},
	{
		constraint: "payments_status_known",
		reject:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES ('11110001-0000-4000-8000-000000000002','6666aaaa-6666-4666-8666-666666666666','pi_rej_status','failed',6788000);`,
		accept:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES ('11110001-0000-4000-8000-000000000002','6666aaaa-6666-4666-8666-666666666666','pi_acc_status','requires_payment',6788000);`,
	},
	{
		constraint: "payments_succeeded_is_captured",
		reject:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at) VALUES ('11110001-0000-4000-8000-000000000007','6666aaaa-6666-4666-8666-666666666666','pi_rej_sic','processing',6788000,100,NULL);`,
		accept:     `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at) VALUES ('11110001-0000-4000-8000-000000000007','6666aaaa-6666-4666-8666-666666666666','pi_acc_sic','processing',6788000,NULL,NULL);`,
	},
	{
		constraint: "product_images_alt_present",
		reject:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'alt-case.webp', E'	', 1);`,
		accept:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'alt-case.webp', '正面圖', 1);`,
	},
	{
		constraint: "product_images_height_positive",
		reject:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, height, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'height-case.webp', '高度測試', 0, 1);`,
		accept:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, height, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'height-case.webp', '高度測試', 1, 1);`,
	},
	{
		constraint: "product_images_position_non_negative",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110003-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','helper-prod','輔助商品','draft'); INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110004-0000-4000-8000-000000000001','11110003-0000-4000-8000-000000000001','pos-case.webp','位置測試', -1);`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110003-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','helper-prod','輔助商品','draft'); INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110004-0000-4000-8000-000000000001','11110003-0000-4000-8000-000000000001','pos-case.webp','位置測試', 0);`,
	},
	{
		constraint: "product_images_storage_key_present",
		reject:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', E'	', '儲存鍵測試', 1);`,
		accept:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'clean-key.webp', '儲存鍵測試', 1);`,
	},
	{
		constraint: "product_images_width_positive",
		reject:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, width, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'width-case.webp', '寬度測試', 0, 1);`,
		accept:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, width, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'width-case.webp', '寬度測試', 1, 1);`,
	},
	{
		constraint: "product_option_values_value_present",
		reject:     `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('11110002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', E'\t');`,
		accept:     `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('11110002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', '玫瑰金');`,
	},
	{
		constraint: "product_options_name_present",
		reject:     `INSERT INTO product_options (id, product_id, name) VALUES ('11110001-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', E'\t');`,
		accept:     `INSERT INTO product_options (id, product_id, name) VALUES ('11110001-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '尺寸');`,
	},
	{
		constraint: "product_reviews_body_present",
		reject:     `INSERT INTO product_reviews (product_id, user_id, rating, body) VALUES ('33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 5, E'\t');`,
		accept:     `INSERT INTO product_reviews (product_id, user_id, rating, body) VALUES ('33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 5, '螢幕很棒,值得推薦');`,
	},
	{
		constraint: "product_reviews_rating_range",
		reject:     `INSERT INTO product_reviews (product_id, user_id, rating, body) VALUES ('33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 0, '螢幕很棒');`,
		accept:     `INSERT INTO product_reviews (product_id, user_id, rating, body) VALUES ('33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 1, '螢幕很棒');`,
	},
	{
		constraint: "product_search_documents_body_present",
		reject:     `INSERT INTO product_search_documents (product_id, body) VALUES ('33333333-3333-4333-8333-333333333333', E'\t');`,
		accept:     `INSERT INTO product_search_documents (product_id, body) VALUES ('33333333-3333-4333-8333-333333333333', 'Pixelight 9 Pro 星霧藍 256GB');`,
	},
	{
		constraint: "product_specs_label_present",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110004-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', E'\t', '有效值', 1);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110004-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', '有效值', 1);`,
	},
	{
		constraint: "product_specs_value_present",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', E'\t', 1);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', 'A18 Pro', 1);`,
	},
	{
		constraint: "product_variants_compare_at_is_higher",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, compare_at_price_cents, position) VALUES ('11110006-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-TEST', 3390000, 3390000, 2);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, compare_at_price_cents, position) VALUES ('11110006-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-TEST', 3390000, 3390001, 2);`,
	},
	{
		constraint: "product_variants_price_in_range",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110007-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-TEST', -1, 2);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110007-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-TEST', 0, 2);`,
	},
	{
		constraint: "product_variants_safety_stock_non_negative",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position) VALUES ('11110008-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-TEST', 3390000, -1, 2);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position) VALUES ('11110008-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-TEST', 3390000, 0, 2);`,
	},
	{
		constraint: "product_variants_sku_format",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110009-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-test', 3390000, 2);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110009-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-TEST', 3390000, 2);`,
	},
	{
		constraint: "product_variants_stock_non_negative",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, position) VALUES ('11110010-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-TEST', 3390000, -1, 2);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, stock_quantity, position) VALUES ('11110010-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-TEST', 3390000, 0, 2);`,
	},
	{
		constraint: "products_active_is_published",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','new-prod','新商品','active', NULL);`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, status, published_at) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','new-prod','新商品','active', now());`,
	},
	{
		constraint: "products_name_present",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','new-prod', E'	', 'draft');`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','new-prod','新商品','draft');`,
	},
	{
		constraint: "products_slug_format",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','New-Prod','新商品','draft');`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','new-prod','新商品','draft');`,
	},
	{
		constraint: "products_status_known",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','new-prod','新商品','pending');`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','new-prod','新商品','draft');`,
	},
	{
		constraint: "promo_banners_cta_complete",
		reject:     `INSERT INTO promo_banners (message, cta_label, cta_href) VALUES ('限時優惠', '領取優惠', NULL);`,
		accept:     `INSERT INTO promo_banners (message, cta_label, cta_href) VALUES ('限時優惠', '領取優惠', '/deals');`,
	},
	{
		constraint: "promo_banners_message_present",
		reject:     `INSERT INTO promo_banners (message) VALUES (E'\t');`,
		accept:     `INSERT INTO promo_banners (message) VALUES ('全站免運');`,
	},
	{
		constraint: "promo_banners_window_ordered",
		reject:     `INSERT INTO promo_banners (message, starts_at, ends_at) VALUES ('限時優惠', '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z');`,
		accept:     `INSERT INTO promo_banners (message, starts_at, ends_at) VALUES ('限時優惠', '2026-08-01T00:00:00Z', '2026-08-02T00:00:00Z');`,
	},
	{
		constraint: "refunds_amount_in_range",
		// The capture can no longer be inflated past 1e10 to clear the way
		// (payments_captured_in_range forbids it), and refunds_guard would reject
		// an over-capture amount first — so disable the trigger to let the range
		// CHECK be the rule under test. CHECKs still fire under replica.
		reject: `SET LOCAL session_replication_role = replica;
		         INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110003-0000-4000-8000-00000000000a','77770001-0000-4000-8000-000000000000','rk-range','pending',10000000001);`,
		accept: `SET LOCAL session_replication_role = replica;
		         INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110003-0000-4000-8000-00000000000a','77770001-0000-4000-8000-000000000000','rk-range','pending',10000000000);`,
	},
	{
		constraint: "refunds_amount_positive",
		reject:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110003-0000-4000-8000-000000000009','77770001-0000-4000-8000-000000000000','rk-rej-amt','pending',0);`,
		accept:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110003-0000-4000-8000-000000000009','77770001-0000-4000-8000-000000000000','rk-acc-amt','pending',1);`,
	},
	{
		constraint: "refunds_failed_has_time",
		reject:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, failed_at) VALUES ('11110003-0000-4000-8000-00000000000d','77770001-0000-4000-8000-000000000000','rk-rej-fht','failed',100000,NULL);`,
		accept:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, failed_at) VALUES ('11110003-0000-4000-8000-00000000000d','77770001-0000-4000-8000-000000000000','rk-acc-fht','failed',100000,now());`,
	},
	{
		constraint: "refunds_request_key_present",
		reject:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110003-0000-4000-8000-00000000000b','77770001-0000-4000-8000-000000000000',E'\t','pending',100000);`,
		accept:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110003-0000-4000-8000-00000000000b','77770001-0000-4000-8000-000000000000','rk-present','pending',100000);`,
	},
	{
		constraint: "refunds_status_known",
		reject:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110003-0000-4000-8000-000000000008','77770001-0000-4000-8000-000000000000','rk-rej-status','processing',100000);`,
		accept:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110003-0000-4000-8000-000000000008','77770001-0000-4000-8000-000000000000','rk-acc-status','pending',100000);`,
	},
	{
		constraint: "refunds_succeeded_has_time",
		reject:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, succeeded_at) VALUES ('11110003-0000-4000-8000-00000000000c','77770001-0000-4000-8000-000000000000','rk-rej-sht','succeeded',100000,NULL);`,
		accept:     `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents, succeeded_at) VALUES ('11110003-0000-4000-8000-00000000000c','77770001-0000-4000-8000-000000000000','rk-acc-sht','succeeded',100000,now());`,
	},
	{
		constraint: "return_request_lines_quantity_positive",
		reject:     `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 0);`,
		accept:     `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660001-0000-4000-8000-000000000000', 1);`,
	},
	{
		constraint: "return_requests_decided_has_time",
		// Tested from the 'requested' side so the new start-requested INSERT
		// trigger does not shadow it: a requested row must have no decided_at.
		reject: `INSERT INTO return_requests (id, order_id, reason, decided_at) VALUES ('11110001-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '退貨', now());`,
		accept: `INSERT INTO return_requests (id, order_id, reason) VALUES ('11110001-0000-4000-8000-000000000001', '66666666-6666-4666-8666-666666666666', '退貨');`,
	},
	{
		constraint: "return_requests_reason_present",
		reject:     `INSERT INTO return_requests (id, order_id, reason) VALUES ('11110001-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', E'\t');`,
		accept:     `INSERT INTO return_requests (id, order_id, reason) VALUES ('11110001-0000-4000-8000-000000000002', '66666666-6666-4666-8666-666666666666', '退貨');`,
	},
	{
		constraint: "return_requests_status_known",
		// An unknown status is necessarily not 'requested', so the start-requested
		// INSERT trigger would refuse it first; disable triggers to reach the
		// CHECK. The legal neighbour is a plain requested row, which needs no bypass.
		reject: `SET LOCAL session_replication_role = replica;
		         INSERT INTO return_requests (id, order_id, status, reason, decided_at) VALUES ('11110001-0000-4000-8000-000000000003', '66666666-6666-4666-8666-666666666666', 'shipped', '退貨', now());`,
		accept: `INSERT INTO return_requests (id, order_id, reason) VALUES ('11110001-0000-4000-8000-000000000003', '66666666-6666-4666-8666-666666666666', '退貨');`,
	},
	{
		constraint: "sale_campaigns_slug_format",
		reject:     `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('autumn_sale', '秋季特賣', now() + interval '7 days');`,
		accept:     `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('autumn-sale', '秋季特賣', now() + interval '7 days');`,
	},
	{
		constraint: "sale_campaigns_title_present",
		reject:     `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('autumn', E'\t', now() + interval '7 days');`,
		accept:     `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('autumn', '秋季特賣', now() + interval '7 days');`,
	},
	{
		constraint: "sale_campaigns_window_ordered",
		reject:     `INSERT INTO sale_campaigns (slug, title, starts_at, ends_at) VALUES ('autumn', '秋季特賣', '2026-08-01T00:00:00Z', '2026-08-01T00:00:00Z');`,
		accept:     `INSERT INTO sale_campaigns (slug, title, starts_at, ends_at) VALUES ('autumn', '秋季特賣', '2026-08-01T00:00:00Z', '2026-08-02T00:00:00Z');`,
	},
	{
		constraint: "sessions_expiry_after_creation",
		reject:     `INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('\x01', '55555555-5555-4555-8555-555555555555', '2026-01-01 00:00:00+00', '2026-01-01 00:00:00+00');`,
		accept:     `INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('\x01', '55555555-5555-4555-8555-555555555555', '2026-01-01 00:00:00+00', '2026-01-01 00:00:00.000001+00');`,
	},
	{
		constraint: "shipping_method_versions_fee_non_negative",
		reject:     `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '測試版本', -1, '2027-01-01');`,
		accept:     `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '測試版本', 0, '2027-01-01');`,
	},
	{
		constraint: "shipping_method_versions_free_over_non_negative",
		reject:     `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, free_over_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '測試版本', 8000, -1, '2027-01-01');`,
		accept:     `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, free_over_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '測試版本', 8000, 0, '2027-01-01');`,
	},
	{
		constraint: "shipping_method_versions_name_present",
		reject:     `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', E'\t', 8000, '2027-01-01');`,
		accept:     `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '快遞', 8000, '2027-01-01');`,
	},
	{
		constraint: "shipping_methods_code_format",
		reject:     `INSERT INTO shipping_methods (id, code) VALUES ('11110001-0000-4000-8000-000000000001', 'store-pickup');`,
		accept:     `INSERT INTO shipping_methods (id, code) VALUES ('11110001-0000-4000-8000-000000000001', 'store_pickup');`,
	},
	{
		constraint: "stock_notifications_email_present",
		reject:     `INSERT INTO stock_notifications (variant_id, email) VALUES ('44444444-4444-4444-8444-444444444444', E'\t');`,
		accept:     `INSERT INTO stock_notifications (variant_id, email) VALUES ('44444444-4444-4444-8444-444444444444', 'ming@example.com');`,
	},
	{
		constraint: "store_credit_entries_amount_non_zero",
		reject: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 0, '購物金調整', 'sc-amount-1');`,
		accept: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 1, '購物金調整', 'sc-amount-1');`,
	},
	{
		constraint: "store_credit_entries_amount_in_range",
		// A positive credit over the ceiling: the guard permits it (the balance
		// only climbs), so the range CHECK is what refuses it — which is the
		// point, since without the bound the guard's running sum could overflow.
		reject: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 10000000001, '購物金調整', 'sc-range');`,
		accept: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 10000000000, '購物金調整', 'sc-range');`,
	},
	{
		constraint: "store_credit_entries_key_present",
		reject: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 1, '購物金調整', E'\t');`,
		accept: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 1, '購物金調整', E'\tsc-key');`,
	},
	{
		constraint: "store_credit_entries_reason_present",
		reject: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 1, E'\t', 'sc-reason-1');`,
		accept: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 1, E'\t購物金', 'sc-reason-1');`,
	},
	{
		constraint: "user_identities_provider_known",
		reject:     `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'facebook', 'google-sub-123');`,
		accept:     `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', 'google-sub-123');`,
	},
	{
		constraint: "user_identities_subject_present",
		reject:     `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', E'\t');`,
		accept:     `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', 'google-sub-123');`,
	},
	{
		constraint: "users_email_present",
		reject:     `INSERT INTO users (id, email) VALUES ('11110003-0000-4000-8000-000000000001', '');`,
		accept:     `INSERT INTO users (id, email) VALUES ('11110003-0000-4000-8000-000000000001', 'fresh@example.com');`,
	},
	{
		constraint: "users_email_trimmed",
		reject:     `INSERT INTO users (id, email) VALUES ('11110003-0000-4000-8000-000000000001', E'fresh@example.com\t');`,
		accept:     `INSERT INTO users (id, email) VALUES ('11110003-0000-4000-8000-000000000001', 'fresh@example.com');`,
	},
	{
		constraint: "users_role_known",
		reject:     `INSERT INTO users (id, email, role) VALUES ('11110003-0000-4000-8000-000000000001', 'fresh@example.com', 'superadmin');`,
		accept:     `INSERT INTO users (id, email, role) VALUES ('11110003-0000-4000-8000-000000000001', 'fresh@example.com', 'staff');`,
	},
	{
		constraint: "warranty_registrations_unit_positive",
		reject:     `INSERT INTO warranty_registrations (id, order_line_id, unit_no, expires_on) VALUES ('11110001-0000-4000-8000-000000000004', '66660001-0000-4000-8000-000000000000', 0, '2027-01-01');`,
		accept:     `INSERT INTO warranty_registrations (id, order_line_id, unit_no, expires_on) VALUES ('11110001-0000-4000-8000-000000000004', '66660001-0000-4000-8000-000000000000', 1, '2027-01-01');`,
	},
}

var uniqueCases = []uniqueCase{
	{
		index:  "store_credit_accounts_user_id_key",
		reject: `INSERT INTO store_credit_accounts (id, user_id) VALUES ('11115002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555');`,
		// A different user may have their own account.
		accept: `INSERT INTO store_credit_accounts (id, user_id) VALUES ('11115002-0000-4000-8000-000000000001', '5555aaaa-5555-4555-8555-555555555555');`,
	},
	{
		index:  "carts_token_hash_key",
		reject: `INSERT INTO carts (id, token_hash) VALUES ('11115003-0000-4000-8000-000000000001', '\x0102');`,
		accept: `INSERT INTO carts (id, token_hash) VALUES ('11115003-0000-4000-8000-000000000001', '\x9999');`,
	},
	{
		index: "carts_one_per_user",
		reject: `INSERT INTO carts (id, user_id, token_hash) VALUES
		 ('11115004-0000-4000-8000-000000000001','55555555-5555-4555-8555-555555555555','\x0401'),
		 ('11115005-0000-4000-8000-000000000001','55555555-5555-4555-8555-555555555555','\x0402');`,
		// Partial on user_id IS NOT NULL, so two guest carts are admitted.
		accept: `INSERT INTO carts (id, user_id, token_hash) VALUES
		 ('11115004-0000-4000-8000-000000000001', NULL, '\x0501'),
		 ('11115005-0000-4000-8000-000000000001', NULL, '\x0502');`,
	},
	{
		index:  "addresses_one_default_per_user",
		reject: `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 99 號', true);`,
		accept: `INSERT INTO addresses (id, user_id, recipient_name, phone, postal_code, city, district, street, is_default) VALUES ('11110001-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 99 號', false);`,
	},
	{
		index:  "brands_slug_key",
		reject: `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'pixelight', '宏碁');`,
		accept: `INSERT INTO brands (id, slug, name) VALUES ('11110001-0000-4000-8000-000000000001', 'pixelight-2', '宏碁');`,
	},
	{
		index:  "categories_slug_key",
		reject: `INSERT INTO categories (id, slug, name) VALUES ('11110002-0000-4000-8000-000000000001', 'phones', '手機二');`,
		accept: `INSERT INTO categories (id, slug, name) VALUES ('11110002-0000-4000-8000-000000000001', 'phones-2', '手機二');`,
	},
	{
		index:  "checkout_attempts_order_key",
		reject: `INSERT INTO checkout_attempts (idempotency_key, order_id) VALUES ('co_key_a', '66666666-6666-4666-8666-666666666666'); INSERT INTO checkout_attempts (idempotency_key, order_id) VALUES ('co_key_b', '66666666-6666-4666-8666-666666666666');`,
		accept: `INSERT INTO checkout_attempts (idempotency_key, order_id) VALUES ('co_key_a', NULL); INSERT INTO checkout_attempts (idempotency_key, order_id) VALUES ('co_key_b', NULL);`,
	},
	{
		index:  "faq_entries_position_key",
		reject: `INSERT INTO faq_entries (category, question, answer, position) VALUES ('運送', '能改收件地址嗎?', '出貨前皆可修改。', 0);`,
		accept: `INSERT INTO faq_entries (category, question, answer, position) VALUES ('運送', '能改收件地址嗎?', '出貨前皆可修改。', 1);`,
	},
	{
		index:  "hero_slides_position_key",
		reject: `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position) VALUES ('第二張主視覺', '立即選購', '/c/laptops', 0);`,
		accept: `INSERT INTO hero_slides (headline, primary_cta_label, primary_cta_href, position) VALUES ('第二張主視覺', '立即選購', '/c/laptops', 1);`,
	},
	{
		index: "inventory_movements_idempotency_key",
		reject: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 1, 'receipt', 'im-dup');
INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 1, 'receipt', 'im-dup');`,
		accept: `INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 1, 'receipt', 'im-dup');
INSERT INTO inventory_movements (variant_id, delta, reason, idempotency_key)
VALUES ('44444444-4444-4444-8444-444444444444', 1, 'receipt', 'im-dup-2');`,
	},
	{
		index: "inventory_reservations_order_variant_key",
		reject: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, now() + interval '1 hour');
INSERT INTO inventory_reservations (order_id, variant_id, quantity, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, now() + interval '1 hour');`,
		accept: `INSERT INTO inventory_reservations (order_id, variant_id, quantity, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '44444444-4444-4444-8444-444444444444', 1, now() + interval '1 hour');
INSERT INTO inventory_reservations (order_id, variant_id, quantity, expires_at)
VALUES ('66666666-6666-4666-8666-666666666666', '4444aaaa-4444-4444-8444-444444444444', 1, now() + interval '1 hour');`,
	},
	{
		index:  "invoice_document_lines_position_key",
		reject: `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000005', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 100, 100, 'taxable', 0); INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000006', '99990001-0000-4000-8000-000000000000', '折讓明細', 1, 100, 100, 'taxable', 0);`,
		accept: `INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000005', '99990001-0000-4000-8000-000000000000', 'Pixelight 9 Pro 5G', 1, 100, 100, 'taxable', 0); INSERT INTO invoice_document_lines (id, document_id, description, quantity, unit_price_cents, amount_cents, tax_type, position) VALUES ('11110002-0000-4000-8000-000000000006', '99990001-0000-4000-8000-000000000000', '折讓明細', 1, 100, 100, 'taxable', 1);`,
	},
	{
		index:  "invoice_documents_number_key",
		reject: `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000010', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-72031288', 100);`,
		accept: `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-000000000010', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-90000010', 100);`,
	},
	{
		index: "invoice_documents_one_active_invoice_per_order",
		// A second live invoice for the same order is refused; voiding the first
		// (the dimension the partial index excludes) frees the slot for a reissue.
		reject: `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-00000000001a', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-ACT-A', 100);
		         INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-00000000001b', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-ACT-B', 100);`,
		accept: `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-00000000001a', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-ACT-A', 100);
		         UPDATE invoice_documents SET status = 'voided', voided_at = now() WHERE id = '11110001-0000-4000-8000-00000000001a';
		         INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-00000000001b', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-ACT-B', 100);`,
	},
	{
		index:  "newsletter_subscribers_email_key",
		reject: `INSERT INTO newsletter_subscribers (email) VALUES ('Reader@Example.com'); INSERT INTO newsletter_subscribers (email) VALUES ('reader@example.com');`,
		accept: `INSERT INTO newsletter_subscribers (email) VALUES ('reader1@example.com'); INSERT INTO newsletter_subscribers (email) VALUES ('reader2@example.com');`,
	},
	{
		index:  "order_lines_position_key",
		reject: `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000005', '6666aaaa-6666-4666-8666-666666666666', 'PXL-TEST-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 0);`,
		accept: `INSERT INTO order_lines (id, order_id, sku, product_name, unit_price_cents, quantity, position) VALUES ('11110001-0000-4000-8000-000000000005', '6666aaaa-6666-4666-8666-666666666666', 'PXL-TEST-BL', 'Pixelight 9 Pro 5G', 3690000, 1, 1);`,
	},
	{
		index:  "order_shipments_tracking_key",
		reject: `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110001-0000-4000-8000-000000000013', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-8871');`,
		accept: `INSERT INTO order_shipments (id, order_id, carrier, tracking_number) VALUES ('11110001-0000-4000-8000-000000000013', '66666666-6666-4666-8666-666666666666', '黑貓宅急便', '903-2214-9999');`,
	},
	{
		index:  "orders_number_key",
		reject: `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'GO-260721-000387', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
		accept: `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name) VALUES ('11110001-0000-4000-8000-000000000001', 'GO-260724-000999', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府');`,
	},
	{
		index:  "outbox_messages_dedupe_key",
		reject: `INSERT INTO outbox_messages (topic, dedupe_key, payload) VALUES ('order.shipped', 'order-66666666-shipped', '{}'::jsonb); INSERT INTO outbox_messages (topic, dedupe_key, payload) VALUES ('order.shipped', 'order-66666666-shipped', '{}'::jsonb);`,
		accept: `INSERT INTO outbox_messages (topic, dedupe_key, payload) VALUES ('order.shipped', 'order-66666666-shipped', '{}'::jsonb); INSERT INTO outbox_messages (topic, dedupe_key, payload) VALUES ('order.shipped', 'order-77777777-shipped', '{}'::jsonb);`,
	},
	{
		index:  "payments_one_capture_per_order",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at) VALUES ('11110004-0000-4000-8000-000000000002','66666666-6666-4666-8666-666666666666','pi_second_cap','succeeded',6788000,6788000,now());`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES ('11110004-0000-4000-8000-000000000002','66666666-6666-4666-8666-666666666666','pi_pending_same_order','requires_payment',6788000);`,
	},
	{
		index:  "payments_provider_ref_key",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES ('11110004-0000-4000-8000-000000000001','6666aaaa-6666-4666-8666-666666666666','pi_fixture','requires_payment',6788000);`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents) VALUES ('11110004-0000-4000-8000-000000000001','6666aaaa-6666-4666-8666-666666666666','pi_unique_new','requires_payment',6788000);`,
	},
	{
		index:  "product_images_position_key",
		reject: `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'dup-pos.webp', '重複位置', 0);`,
		accept: `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'dup-pos.webp', '重複位置', 1);`,
	},
	{
		index:  "product_images_storage_key_key",
		reject: `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'pxl-9p-front.webp', '重複儲存鍵', 1);`,
		accept: `INSERT INTO product_images (id, product_id, storage_key, alt_text, position) VALUES ('11110005-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'unique-key.webp', '重複儲存鍵', 1);`,
	},
	{
		index:  "product_option_values_value_key",
		reject: `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('11110012-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', '星霧藍');`,
		accept: `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('11110012-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'aaaa0002-0000-4000-8000-000000000000', '星霧藍');`,
	},
	{
		index:  "product_options_name_key",
		reject: `INSERT INTO product_options (id, product_id, name) VALUES ('11110013-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '顏色');`,
		accept: `INSERT INTO product_options (id, product_id, name) VALUES ('11110013-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '尺寸');`,
	},
	{
		index:  "product_reviews_author_key",
		reject: `INSERT INTO product_reviews (product_id, user_id, rating, body) VALUES ('33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 5, '第一則評論'); INSERT INTO product_reviews (product_id, user_id, rating, body) VALUES ('33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 4, '第二則評論');`,
		accept: `INSERT INTO product_reviews (product_id, user_id, rating, body) VALUES ('33333333-3333-4333-8333-333333333333', '55555555-5555-4555-8555-555555555555', 5, '第一則評論'); INSERT INTO product_reviews (product_id, user_id, rating, body) VALUES ('33333333-3333-4333-8333-333333333333', '5555aaaa-5555-4555-8555-555555555555', 4, '第二則評論');`,
	},
	{
		index:  "product_specs_position_key",
		reject: `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110015-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', 'A18 Pro', 0);`,
		accept: `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110015-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', '處理器', 'A18 Pro', 1);`,
	},
	{
		index:  "product_variants_position_key",
		reject: `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110016-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-POS', 3390000, 0);`,
		accept: `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110016-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-POS', 3390000, 2);`,
	},
	{
		index:  "product_variants_sku_key",
		reject: `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110018-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-256-BL', 3390000, 2);`,
		accept: `INSERT INTO product_variants (id, product_id, sku, price_cents, position) VALUES ('11110018-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'PXL-9P-NEW', 3390000, 2);`,
	},
	{
		index:  "products_slug_key",
		reject: `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','pixelight-9-pro','新商品','draft');`,
		accept: `INSERT INTO products (id, brand_id, category_id, slug, name, status) VALUES ('11110006-0000-4000-8000-000000000001','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','pixelight-9-pro-2','新商品','draft');`,
	},
	{
		index:  "refunds_provider_ref_key",
		reject: `INSERT INTO refunds (id, payment_id, request_key, provider_ref, status, amount_cents) VALUES ('11110006-0000-4000-8000-000000000001','77770001-0000-4000-8000-000000000000','rk-pra','pr_dup','pending',100000); INSERT INTO refunds (id, payment_id, request_key, provider_ref, status, amount_cents) VALUES ('11110006-0000-4000-8000-000000000002','77770001-0000-4000-8000-000000000000','rk-prb','pr_dup','pending',100000);`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, provider_ref, status, amount_cents) VALUES ('11110006-0000-4000-8000-000000000001','77770001-0000-4000-8000-000000000000','rk-pra',NULL,'pending',100000); INSERT INTO refunds (id, payment_id, request_key, provider_ref, status, amount_cents) VALUES ('11110006-0000-4000-8000-000000000002','77770001-0000-4000-8000-000000000000','rk-prb',NULL,'pending',100000);`,
	},
	{
		index:  "refunds_request_key_key",
		reject: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110005-0000-4000-8000-000000000001','77770001-0000-4000-8000-000000000000','rk-dup','pending',100000); INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110005-0000-4000-8000-000000000002','77770001-0000-4000-8000-000000000000','rk-dup','pending',100000);`,
		accept: `INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110005-0000-4000-8000-000000000001','77770001-0000-4000-8000-000000000000','rk-dup-a','pending',100000); INSERT INTO refunds (id, payment_id, request_key, status, amount_cents) VALUES ('11110005-0000-4000-8000-000000000002','77770001-0000-4000-8000-000000000000','rk-dup-b','pending',100000);`,
	},
	{
		index:  "sale_campaigns_slug_key",
		reject: `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('summer', '另一檔夏季活動', now() + interval '7 days');`,
		accept: `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('winter', '冬季暖身購', now() + interval '7 days');`,
	},
	{
		index:  "shipping_method_versions_effective_key",
		reject: `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '測試版本', 8000, now());`,
		accept: `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '測試版本', 8000, '2027-01-01');`,
	},
	{
		index:  "shipping_methods_code_key",
		reject: `INSERT INTO shipping_methods (id, code) VALUES ('11110001-0000-4000-8000-000000000001', 'home_delivery');`,
		accept: `INSERT INTO shipping_methods (id, code) VALUES ('11110001-0000-4000-8000-000000000001', 'store_pickup');`,
	},
	{
		index:  "stock_notifications_pending_key",
		reject: `INSERT INTO stock_notifications (variant_id, email) VALUES ('44444444-4444-4444-8444-444444444444', 'Notify@example.com'); INSERT INTO stock_notifications (variant_id, email) VALUES ('44444444-4444-4444-8444-444444444444', 'notify@example.com');`,
		accept: `INSERT INTO stock_notifications (variant_id, email, notified_at) VALUES ('44444444-4444-4444-8444-444444444444', 'Notify@example.com', now()); INSERT INTO stock_notifications (variant_id, email, notified_at) VALUES ('44444444-4444-4444-8444-444444444444', 'notify@example.com', now());`,
	},
	{
		index: "store_credit_entries_idempotency_key",
		reject: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 1, '購物金調整', 'fixture-grant');`,
		accept: `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
VALUES ('a0000001-0000-4000-8000-000000000000', 1, '購物金調整', 'fixture-grant-2');`,
	},
	{
		index: "store_credit_entries_reverses_key",
		reject: `INSERT INTO store_credit_entries (id, account_id, amount_cents, reason, idempotency_key)
VALUES ('11110001-0000-4000-8000-000000000001', 'a0000001-0000-4000-8000-000000000000', 30000, '購物金補發', 'sc-orig');
INSERT INTO store_credit_entries (id, account_id, amount_cents, reason, idempotency_key, reverses_id)
VALUES ('11110001-0000-4000-8000-000000000002', 'a0000001-0000-4000-8000-000000000000', -30000, '沖銷', 'sc-rev-1', '11110001-0000-4000-8000-000000000001');
INSERT INTO store_credit_entries (id, account_id, amount_cents, reason, idempotency_key, reverses_id)
VALUES ('11110001-0000-4000-8000-000000000003', 'a0000001-0000-4000-8000-000000000000', -30000, '沖銷', 'sc-rev-2', '11110001-0000-4000-8000-000000000001');`,
		accept: `INSERT INTO store_credit_entries (id, account_id, amount_cents, reason, idempotency_key)
VALUES ('11110001-0000-4000-8000-000000000001', 'a0000001-0000-4000-8000-000000000000', 30000, '購物金補發', 'sc-orig');
INSERT INTO store_credit_entries (id, account_id, amount_cents, reason, idempotency_key, reverses_id)
VALUES ('11110001-0000-4000-8000-000000000002', 'a0000001-0000-4000-8000-000000000000', -30000, '沖銷', 'sc-rev-1', '11110001-0000-4000-8000-000000000001');
INSERT INTO store_credit_entries (id, account_id, amount_cents, reason, idempotency_key, reverses_id)
VALUES ('11110001-0000-4000-8000-000000000003', 'a0000001-0000-4000-8000-000000000000', -1, '購物金調整', 'sc-rev-2', NULL);`,
	},
	{
		index:  "user_identities_provider_subject_key",
		reject: `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', 'shared-subject'); INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000002', '5555aaaa-5555-4555-8555-555555555555', 'google', 'shared-subject');`,
		accept: `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', 'shared-subject'); INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000002', '5555aaaa-5555-4555-8555-555555555555', 'google', 'other-subject');`,
	},
	{
		index:  "user_identities_user_provider_key",
		reject: `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', 'subject-one'); INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000002', '55555555-5555-4555-8555-555555555555', 'google', 'subject-two');`,
		accept: `INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555', 'google', 'subject-one'); INSERT INTO user_identities (id, user_id, provider, provider_subject) VALUES ('11110002-0000-4000-8000-000000000002', '5555aaaa-5555-4555-8555-555555555555', 'google', 'subject-two');`,
	},
	{
		index:  "users_email_key",
		reject: `INSERT INTO users (id, email) VALUES ('11110003-0000-4000-8000-000000000001', 'MING@EXAMPLE.COM');`,
		accept: `INSERT INTO users (id, email) VALUES ('11110003-0000-4000-8000-000000000001', 'other@example.com');`,
	},
	{
		index:  "warranty_registrations_serial_key",
		reject: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES ('11110002-0000-4000-8000-000000000001', '66660001-0000-4000-8000-000000000000', 1, 'SN-DUP-001', '2027-01-01'); INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES ('11110002-0000-4000-8000-000000000002', '66660001-0000-4000-8000-000000000000', 2, 'SN-DUP-001', '2027-01-01');`,
		accept: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES ('11110002-0000-4000-8000-000000000003', '66660001-0000-4000-8000-000000000000', 1, NULL, '2027-01-01'); INSERT INTO warranty_registrations (id, order_line_id, unit_no, serial_number, expires_on) VALUES ('11110002-0000-4000-8000-000000000004', '66660001-0000-4000-8000-000000000000', 2, NULL, '2027-01-01');`,
	},
	{
		index:  "warranty_registrations_unit_key",
		reject: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, expires_on) VALUES ('11110003-0000-4000-8000-000000000001', '66660001-0000-4000-8000-000000000000', 1, '2027-01-01'); INSERT INTO warranty_registrations (id, order_line_id, unit_no, expires_on) VALUES ('11110003-0000-4000-8000-000000000002', '66660001-0000-4000-8000-000000000000', 1, '2027-01-01');`,
		accept: `INSERT INTO warranty_registrations (id, order_line_id, unit_no, expires_on) VALUES ('11110003-0000-4000-8000-000000000003', '66660001-0000-4000-8000-000000000000', 1, '2027-01-01'); INSERT INTO warranty_registrations (id, order_line_id, unit_no, expires_on) VALUES ('11110003-0000-4000-8000-000000000004', '66660001-0000-4000-8000-000000000000', 2, '2027-01-01');`,
	},
}
