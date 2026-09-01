//go:build integration

package db_test

// One case per constraint. The catalog is the authority on what must be covered:
// TestEveryCheckConstraintIsExercised and TestEveryUniqueConstraintIsExercised fail on a drift.

var checkCases = []checkCase{
	{
		constraint: "coupons_code_format",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', '!!', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_description_present",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', E'\t', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_kind_known",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'barter', 20000, NULL, NULL, 0, NULL, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_value_matches_kind",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'percent', 20000, 2000, NULL, 0, NULL, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'percent', NULL, 2000, NULL, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_amount_positive",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 0, NULL, NULL, 0, NULL, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 1, NULL, NULL, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_percent_in_range",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'percent', NULL, 10001, NULL, 0, NULL, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'percent', NULL, 10000, NULL, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_min_subtotal_non_negative",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, -1, NULL, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_max_discount_positive",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'percent', NULL, 2000, 0, 0, NULL, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'percent', NULL, 2000, 1, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_cap_only_on_percent",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, 50000, 0, NULL, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_window_ordered",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), now() - interval '1 day');`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), now() + interval '1 day');`,
	},
	{
		constraint: "coupons_redemptions_positive",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, 0, 1, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, 1, 1, now(), NULL);`,
	},
	{
		constraint: "coupons_per_customer_positive",
		reject:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 0, now(), NULL);`,
		accept:     `INSERT INTO coupons (id, code, description, kind, amount_cents, percent_bp, max_discount_cents, min_subtotal_cents, max_redemptions, per_customer_limit, starts_at, ends_at) VALUES ('cccc0001-0000-4000-8000-000000000001', 'TESTCODE', '測試', 'amount', 20000, NULL, NULL, 0, NULL, 1, now(), NULL);`,
	},
	{
		constraint: "coupon_redemptions_amount_non_negative",
		reject:     `INSERT INTO coupon_redemptions (id, coupon_id, order_id, amount_cents) VALUES ('cccc0002-0000-4000-8000-000000000001', 'cccc0009-0000-4000-8000-000000000009', '66666666-6666-4666-8666-666666666666', -1);`,
		accept:     `INSERT INTO coupon_redemptions (id, coupon_id, order_id, amount_cents) VALUES ('cccc0002-0000-4000-8000-000000000001', 'cccc0009-0000-4000-8000-000000000009', '6666aaaa-6666-4666-8666-666666666666', 0);`,
	},
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
		reject:     `INSERT INTO categories (id, slug, name, position) VALUES ('11110002-0000-4000-8000-000000000001', 'tablets', E'	', 11);`,
		accept:     `INSERT INTO categories (id, slug, name, position) VALUES ('11110002-0000-4000-8000-000000000001', 'tablets', '平板', 11);`,
	},
	{
		constraint: "categories_name_en_present",
		reject:     `INSERT INTO categories (id, slug, name, name_en, position) VALUES ('00000091-0000-4000-8000-000000000091', 'blank-en', '空白英文', E'\t', 12);`,
		accept:     `INSERT INTO categories (id, slug, name, name_en, position) VALUES ('00000091-0000-4000-8000-000000000091', 'blank-en', '空白英文', 'Blank English', 12);`,
	},
	{
		constraint: "categories_icon_key_known",
		reject:     `INSERT INTO categories (id, slug, name, icon_key, position) VALUES ('00000092-0000-4000-8000-000000000092', 'unknown-icon', '未知圖示', 'rocket', 15);`,
		accept:     `INSERT INTO categories (id, slug, name, icon_key, position) VALUES ('00000092-0000-4000-8000-000000000092', 'known-icon', '已知圖示', 'laptop', 15);`,
	},
	{
		constraint: "categories_not_own_parent",
		reject:     `SET LOCAL session_replication_role = replica; INSERT INTO categories (id, parent_id, slug, name, position) VALUES ('11110002-0000-4000-8000-000000000001', '11110002-0000-4000-8000-000000000001', 'tablets', '平板', 13);`,
		accept:     `SET LOCAL session_replication_role = replica; INSERT INTO categories (id, parent_id, slug, name, position) VALUES ('11110002-0000-4000-8000-000000000001', '22222222-2222-4222-8222-222222222222', 'tablets', '平板', 13);`,
	},
	{
		constraint: "categories_slug_format",
		reject:     `INSERT INTO categories (id, slug, name, position) VALUES ('11110002-0000-4000-8000-000000000001', 'Tablets', '平板', 14);`,
		accept:     `INSERT INTO categories (id, slug, name, position) VALUES ('11110002-0000-4000-8000-000000000001', 'tablets', '平板', 11);`,
	},
	{
		constraint: "checkout_attempts_key_format",
		reject:     `INSERT INTO checkout_attempts (idempotency_key) VALUES ('MDAwMDAwMDAwMDAwMDAwM');`,
		accept:     `INSERT INTO checkout_attempts (idempotency_key) VALUES ('MDAwMDAwMDAwMDAwMDAwMQ');`,
	},
	{
		constraint: "checkout_attempts_key_nonzero",
		reject:     `INSERT INTO checkout_attempts (idempotency_key) VALUES ('AAAAAAAAAAAAAAAAAAAAAA');`,
		accept:     `INSERT INTO checkout_attempts (idempotency_key) VALUES ('MDAwMDAwMDAwMDAwMDAwMg');`,
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
		constraint: "contact_messages_subject_known",
		reject:     `INSERT INTO contact_messages (name, email, subject, message) VALUES ('王小明', 'ming@example.com', '其他', '請問出貨時間');`,
		accept:     `INSERT INTO contact_messages (name, email, subject, message) VALUES ('王小明', 'ming@example.com', '訂單問題', '請問出貨時間');`,
	},
	{
		constraint: "faq_entries_category_en_present",
		reject:     `INSERT INTO faq_entries (id, category, question, answer, category_en, position) VALUES ('1111000e-0000-4000-8000-000000000091', '測試', '問題', '答案', E'\t', 91);`,
		accept:     `INSERT INTO faq_entries (id, category, question, answer, category_en, position) VALUES ('1111000e-0000-4000-8000-000000000091', '測試', '問題', '答案', 'Testing', 91);`,
	},
	{
		constraint: "faq_entries_question_en_present",
		reject:     `INSERT INTO faq_entries (id, category, question, answer, question_en, position) VALUES ('1111000e-0000-4000-8000-000000000092', '測試', '問題', '答案', E'\t', 92);`,
		accept:     `INSERT INTO faq_entries (id, category, question, answer, question_en, position) VALUES ('1111000e-0000-4000-8000-000000000092', '測試', '問題', '答案', 'A question?', 92);`,
	},
	{
		constraint: "faq_entries_answer_en_present",
		reject:     `INSERT INTO faq_entries (id, category, question, answer, answer_en, position) VALUES ('1111000e-0000-4000-8000-000000000093', '測試', '問題', '答案', E'\t', 93);`,
		accept:     `INSERT INTO faq_entries (id, category, question, answer, answer_en, position) VALUES ('1111000e-0000-4000-8000-000000000093', '測試', '問題', '答案', 'An answer.', 93);`,
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
		constraint: "media_objects_digest_format",
		// The digest reaches a URL path, which is why the handler needs no escaping.
		reject: `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('not-a-digest', 'image/png', '\x89504e47'::bytea, 10, 10, 4);`,
		accept: `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'image/png', '\x89504e47'::bytea, 10, 10, 4);`,
	},
	{
		constraint: "media_objects_dimensions_sane",
		// Past the ceiling rather than zero: this stops a decompression bomb being stored.
		reject: `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'image/png', '\x89504e47'::bytea, 9000, 10, 4);`,
		accept: `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', 'image/png', '\x89504e47'::bytea, 10, 10, 4);`,
	},
	{
		constraint: "media_objects_size_matches",
		reject:     `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd', 'image/png', '\x89504e47'::bytea, 10, 10, 999);`,
		accept:     `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee', 'image/png', '\x89504e47'::bytea, 10, 10, 4);`,
	},
	{
		constraint: "media_objects_size_positive",
		reject:     `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff', 'image/png', ''::bytea, 10, 10, 0);`,
		accept:     `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('0000000000000000000000000000000000000000000000000000000000000000', 'image/png', '\x89504e47'::bytea, 10, 10, 4);`,
	},
	{
		constraint: "media_objects_type_supported",
		// Only what goen re-encodes itself: an SVG is a document, and serving one is serving markup.
		reject: `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('1111111111111111111111111111111111111111111111111111111111111111', 'image/svg+xml', '\x89504e47'::bytea, 10, 10, 4);`,
		accept: `INSERT INTO media_objects (digest, content_type, bytes, width, height, byte_size) VALUES ('2222222222222222222222222222222222222222222222222222222222222222', 'image/png', '\x89504e47'::bytea, 10, 10, 4);`,
	},
	{
		constraint: "loyalty_entries_points_nonzero",
		// Zero is a durable fact only for a clawback carrying requested_points;
		// an award or spend still has to move the balance.
		reject: `INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 'award', 0, 'test', 'k-zero', current_date + 365);`,
		accept: `INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 'award', 10, 'test', 'k-nonzero', current_date + 365);`,
	},
	{
		constraint: "loyalty_entries_reason_present",
		reject:     `INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 'award', 10, E'\t', 'k-blank-reason', current_date + 365);`,
		accept:     `INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 'award', 10, 'test', 'k-reason', current_date + 365);`,
	},
	{
		constraint: "loyalty_entries_key_present",
		reject:     `INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 'award', 10, 'test', E'\t', current_date + 365);`,
		accept:     `INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 'award', 10, 'test', 'k-present', current_date + 365);`,
	},
	{
		constraint: "loyalty_entries_kind_shape",
		// A zero clawback is a durable shortfall only when it says what positive
		// request it fell short of. Both rows otherwise have the same legal shape.
		reject: `INSERT INTO loyalty_entries (id, account_id, kind, points, reason, idempotency_key, order_id, expires_on)
		         VALUES ('a1000005-0000-4000-8000-000000000001', 'a0000001-0000-4000-8000-000000000000', 'award', 10, 'seed', 'shape-seed', '66666666-6666-4666-8666-666666666666', shop_today() + 365);
		         INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, order_id, expires_on, lot_id, requested_points, return_request_id)
		         VALUES ('a0000001-0000-4000-8000-000000000000', 'clawback', 0, 'return', 'shape-zero', '66666666-6666-4666-8666-666666666666', shop_today() + 365, 'a1000005-0000-4000-8000-000000000001', 0, '88880001-0000-4000-8000-000000000000');`,
		accept: `INSERT INTO loyalty_entries (id, account_id, kind, points, reason, idempotency_key, order_id, expires_on)
		         VALUES ('a1000005-0000-4000-8000-000000000001', 'a0000001-0000-4000-8000-000000000000', 'award', 10, 'seed', 'shape-seed', '66666666-6666-4666-8666-666666666666', shop_today() + 365);
		         INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, order_id, expires_on, lot_id, requested_points, return_request_id)
		         VALUES ('a0000001-0000-4000-8000-000000000000', 'clawback', 0, 'return', 'shape-zero', '66666666-6666-4666-8666-666666666666', shop_today() + 365, 'a1000005-0000-4000-8000-000000000001', 10, '88880001-0000-4000-8000-000000000000');`,
	},
	{
		constraint: "product_questions_body_present",
		reject:     `INSERT INTO product_questions (product_id, body) SELECT id, E'\t' FROM products LIMIT 1;`,
		accept:     `INSERT INTO product_questions (product_id, body) SELECT id, '一個問題' FROM products LIMIT 1;`,
	},
	{
		constraint: "product_questions_body_bounded",
		reject:     `INSERT INTO product_questions (product_id, body) SELECT id, repeat('問', 1001) FROM products LIMIT 1;`,
		accept:     `INSERT INTO product_questions (product_id, body) SELECT id, repeat('問', 1000) FROM products LIMIT 1;`,
	},
	{
		constraint: "product_answers_body_present",
		reject:     `INSERT INTO product_questions (product_id, body) SELECT id, '一個問題' FROM products LIMIT 1;INSERT INTO product_answers (question_id, body) SELECT q.id, E'\t' FROM product_questions q LIMIT 1;`,
		accept:     `INSERT INTO product_questions (product_id, body) SELECT id, '一個問題' FROM products LIMIT 1;INSERT INTO product_answers (question_id, body) SELECT q.id, '一個回答' FROM product_questions q LIMIT 1;`,
	},
	{
		constraint: "product_answers_body_bounded",
		reject:     `INSERT INTO product_questions (product_id, body) SELECT id, '一個問題' FROM products LIMIT 1;INSERT INTO product_answers (question_id, body) SELECT q.id, repeat('答', 2001) FROM product_questions q LIMIT 1;`,
		accept:     `INSERT INTO product_questions (product_id, body) SELECT id, '一個問題' FROM products LIMIT 1;INSERT INTO product_answers (question_id, body) SELECT q.id, repeat('答', 2000) FROM product_questions q LIMIT 1;`,
	},
	{
		constraint: "product_copurchases_not_self",
		// Without this the pair (X, X) ranks first on every page: X is in every order containing X.
		reject: `INSERT INTO product_copurchases (product_id, other_product_id, orders)
		         SELECT p.id, p.id, 3 FROM products p LIMIT 1;`,
		accept: `INSERT INTO product_copurchases (product_id, other_product_id, orders)
		         VALUES ('33333333-3333-4333-8333-333333333333',
		                 '3333aaaa-3333-4333-8333-333333333333', 3);`,
	},
	{
		constraint: "product_copurchases_orders_positive",
		// Spelled out rather than selected: a SELECT that matches nothing inserts nothing and raises
		// nothing, and the case then reports that the database accepted a row it never wrote.
		reject: `INSERT INTO product_copurchases (product_id, other_product_id, orders)
		         VALUES ('33333333-3333-4333-8333-333333333333',
		                 '3333aaaa-3333-4333-8333-333333333333', 0);`,
		accept: `INSERT INTO product_copurchases (product_id, other_product_id, orders)
		         VALUES ('33333333-3333-4333-8333-333333333333',
		                 '3333aaaa-3333-4333-8333-333333333333', 1);`,
	},
	{
		constraint: "products_warranty_months_sane",
		// NULL stays legal: it is how the shop says it has stated no term, and registration is refused.
		reject: `INSERT INTO products (brand_id, category_id, slug, name, warranty_months) SELECT b.id, c.id, 'warranty-check-a', '保固測試', 0 FROM brands b, categories c LIMIT 1;`,
		accept: `INSERT INTO products (brand_id, category_id, slug, name, warranty_months) SELECT b.id, c.id, 'warranty-check-b', '保固測試', 24 FROM brands b, categories c LIMIT 1;`,
	},
	{
		constraint: "staff_totp_secret_present",
		reject:     `INSERT INTO staff_totp_credentials (user_id, secret_encrypted, confirmed_at, last_step) SELECT id, ''::bytea, NULL, NULL FROM users LIMIT 1;`,
		accept:     `INSERT INTO staff_totp_credentials (user_id, secret_encrypted, confirmed_at, last_step) SELECT id, '\x0102'::bytea, NULL, NULL FROM users LIMIT 1;`,
	},
	{
		constraint: "staff_totp_step_needs_confirmation",
		reject:     `INSERT INTO staff_totp_credentials (user_id, secret_encrypted, confirmed_at, last_step) SELECT id, '\x0102'::bytea, NULL, 12345 FROM users LIMIT 1;`,
		accept:     `INSERT INTO staff_totp_credentials (user_id, secret_encrypted, confirmed_at, last_step) SELECT id, '\x0102'::bytea, now(), 12345 FROM users LIMIT 1;`,
	},
	{
		constraint: "sessions_totp_after_creation",
		reject: `INSERT INTO sessions (token_hash, user_id, expires_at, created_at, totp_verified_at)
		         SELECT sha256('totp-check-a'::bytea), id, now() + interval '1 day',
		                now(), now() - interval '1 hour' FROM users LIMIT 1;`,
		accept: `INSERT INTO sessions (token_hash, user_id, expires_at, created_at, totp_verified_at)
		         SELECT sha256('totp-check-b'::bytea), id, now() + interval '1 day',
		                now(), now() FROM users LIMIT 1;`,
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
		// A sale takes stock out, so its delta must be negative; the neighbour is the correct sign.
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
		// A claim carries the key it claims, or it claims nothing.
		constraint: "invoice_documents_pending_is_claimed",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status) VALUES ('11110001-0000-4000-8000-000000000031', '66666666-6666-4666-8666-666666666666', 'allowance', '99990001-0000-4000-8000-000000000000', '', 100, 'pending');`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, status, request_key) VALUES ('11110001-0000-4000-8000-000000000031', '66666666-6666-4666-8666-666666666666', 'allowance', '99990001-0000-4000-8000-000000000000', '', 100, 'pending', 'allowance:probe:0:100');`,
	},
	{
		constraint: "invoice_documents_request_key_present",
		reject:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, request_key) VALUES ('11110001-0000-4000-8000-000000000032', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-90000032', 100, '   ');`,
		accept:     `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, request_key) VALUES ('11110001-0000-4000-8000-000000000032', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-90000032', 100, 'allowance:probe:0:100');`,
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
		reject:     `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES (E'\t', 'a-token-of-at-least-thirty-two-chars');`,
		accept:     `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('sub@example.com', 'a-token-of-at-least-thirty-two-chars');`,
	},
	{
		constraint: "newsletter_subscribers_email_trimmed",
		reject:     `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES (' sub@example.com', 'a-token-of-at-least-thirty-two-chars');`,
		accept:     `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('sub@example.com', 'a-token-of-at-least-thirty-two-chars');`,
	},
	{
		constraint: "newsletter_subscribers_unsubscribe_token_present",
		reject:     `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('sub@example.com', 'short');`,
		accept:     `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('sub@example.com', 'a-token-of-at-least-thirty-two-chars');`,
	},
	{
		constraint: "newsletter_subscribers_unsubscribed_after_confirmed",
		reject: `INSERT INTO newsletter_subscribers (email, unsubscribe_token, confirmed_at, unsubscribed_at)
		         VALUES ('sub@example.com', 'a-token-of-at-least-thirty-two-chars', now(), now() - interval '1 day');`,
		accept: `INSERT INTO newsletter_subscribers (email, unsubscribe_token, confirmed_at, unsubscribed_at)
		         VALUES ('sub@example.com', 'a-token-of-at-least-thirty-two-chars', now() - interval '1 day', now());`,
	},
	{
		// Lower is sooner, so a negative priority would outrank everything transactional.
		constraint: "outbox_messages_priority_non_negative",
		reject:     `INSERT INTO outbox_messages (topic, dedupe_key, payload, priority) VALUES ('t.priority', 'neg', '{}'::jsonb, -1);`,
		accept:     `INSERT INTO outbox_messages (topic, dedupe_key, payload, priority) VALUES ('t.priority', 'neg', '{}'::jsonb, 100);`,
	},
	{
		constraint: "email_verifications_email_present",
		reject:     `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), E'\t', sha256('v1'::bytea), now() + interval '1 day');`,
		accept:     `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'new@example.com', sha256('v1'::bytea), now() + interval '1 day');`,
	},
	{
		constraint: "email_verifications_email_trimmed",
		reject:     `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), ' new@example.com', sha256('v1'::bytea), now() + interval '1 day');`,
		accept:     `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'new@example.com', sha256('v1'::bytea), now() + interval '1 day');`,
	},
	{
		constraint: "email_verifications_digest_sha256",
		reject:     `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'new@example.com', '\xdead'::bytea, now() + interval '1 day');`,
		accept:     `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'new@example.com', sha256('v1'::bytea), now() + interval '1 day');`,
	},
	{
		// What an interval subtracted instead of added looks like.
		constraint: "email_verifications_expires_after_created",
		reject:     `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'new@example.com', sha256('v1'::bytea), now() - interval '1 day');`,
		accept:     `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'new@example.com', sha256('v1'::bytea), now() + interval '1 day');`,
	},
	{
		constraint: "newsletter_subscribers_locale_known",
		reject:     `INSERT INTO newsletter_subscribers (email, unsubscribe_token, locale) VALUES ('sub@example.com', 'a-token-of-at-least-thirty-two-chars', 'kling-on');`,
		accept:     `INSERT INTO newsletter_subscribers (email, unsubscribe_token, locale) VALUES ('sub@example.com', 'a-token-of-at-least-thirty-two-chars', 'en');`,
	},
	{
		constraint: "newsletter_issues_subject_present",
		reject:     `INSERT INTO newsletter_issues (subject, body) VALUES (E'\t', '內容');`,
		accept:     `INSERT INTO newsletter_issues (subject, body) VALUES ('主旨', '內容');`,
	},
	{
		constraint: "newsletter_issues_body_present",
		reject:     `INSERT INTO newsletter_issues (subject, body) VALUES ('主旨', E'\t');`,
		accept:     `INSERT INTO newsletter_issues (subject, body) VALUES ('主旨', '內容');`,
	},
	{
		constraint: "newsletter_issues_recipients_non_negative",
		reject:     `INSERT INTO newsletter_issues (subject, body, sent_at, recipients) VALUES ('主旨', '內容', now(), -1);`,
		accept:     `INSERT INTO newsletter_issues (subject, body, sent_at, recipients) VALUES ('主旨', '內容', now(), 1);`,
	},
	{
		constraint: "newsletter_issues_unsent_has_no_recipients",
		reject:     `INSERT INTO newsletter_issues (subject, body, recipients) VALUES ('主旨', '內容', 5);`,
		accept:     `INSERT INTO newsletter_issues (subject, body, sent_at, recipients) VALUES ('主旨', '內容', now(), 5);`,
	},
	{
		constraint: "newsletter_confirmations_email_present",
		reject:     `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES (E'\t', sha256('c1'::bytea), now() + interval '1 day');`,
		accept:     `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask@example.com', sha256('c1'::bytea), now() + interval '1 day');`,
	},
	{
		constraint: "newsletter_confirmations_email_trimmed",
		reject:     `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask@example.com ', sha256('c1'::bytea), now() + interval '1 day');`,
		accept:     `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask@example.com', sha256('c1'::bytea), now() + interval '1 day');`,
	},
	{
		constraint: "newsletter_confirmations_digest_sha256",
		reject:     `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask@example.com', '\xdead'::bytea, now() + interval '1 day');`,
		accept:     `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask@example.com', sha256('c1'::bytea), now() + interval '1 day');`,
	},
	{
		constraint: "newsletter_confirmations_expires_after_created",
		reject:     `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask@example.com', sha256('c1'::bytea), now() - interval '1 day');`,
		accept:     `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask@example.com', sha256('c1'::bytea), now() + interval '1 day');`,
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
		constraint: "order_access_grants_digest_sha256",
		reject:     `INSERT INTO order_access_grants (digest, order_id) VALUES ('\x00'::bytea, '6666aaaa-6666-4666-8666-666666666666');`,
		accept:     `INSERT INTO order_access_grants (digest, order_id) VALUES (sha256('a browser token'::bytea), '6666aaaa-6666-4666-8666-666666666666');`,
	},
	{
		// A live row with no PHONE, and the phone rather than the street: a missing street is refused
		// by order_private_data_one_destination, which is a different rule.
		constraint: "order_private_data_all_or_erased",
		reject:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', NULL, '110', '台北市', '信義區', '松高路 100 號', NULL, NULL, NULL);`,
		accept:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', '110', '台北市', '信義區', '松高路 100 號', NULL, NULL, NULL);`,
	},
	{
		// Three of the four address columns, the pickup side complete so
		// order_private_data_one_destination is satisfied and this constraint is the only one left.
		constraint: "order_private_data_address_complete",
		reject:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', NULL, '台北市', '信義區', '松高路 100 號', 'seven_eleven', '123456', '信義門市');`,
		accept:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', '110', '台北市', '信義區', '松高路 100 號', NULL, NULL, NULL);`,
	},
	{
		constraint: "order_private_data_pickup_complete",
		reject:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', '110', '台北市', '信義區', '松高路 100 號', 'seven_eleven', NULL, '信義門市');`,
		accept:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', NULL, NULL, NULL, NULL, 'seven_eleven', '123456', '信義門市');`,
	},
	{
		constraint: "order_private_data_one_destination",
		reject:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', '110', '台北市', '信義區', '松高路 100 號', 'seven_eleven', '123456', '信義門市');`,
		accept:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', NULL, NULL, NULL, NULL, 'seven_eleven', '123456', '信義門市');`,
	},
	{
		constraint: "order_private_data_pickup_brand_known",
		reject:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', NULL, NULL, NULL, NULL, 'seven11', '123456', '信義門市');`,
		accept:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', NULL, NULL, NULL, NULL, 'seven_eleven', '123456', '信義門市');`,
	},
	{
		// The ACCEPT is a real convenience-store code, read from ECPay's GetStoreList on 2026-08-06,
		// and not a six-digit one: a six-digit accept passes under a digits-only predicate too.
		constraint: "order_private_data_pickup_store_code_format",
		reject:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', NULL, NULL, NULL, NULL, 'hi_life', '後庄門市', '後庄門市');`,
		accept:     `DELETE FROM order_private_data WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'; INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street, pickup_brand, pickup_store_code, pickup_store_name) VALUES ('6666aaaa-6666-4666-8666-666666666666', 'test@example.com', '測試', '0912000000', NULL, NULL, NULL, NULL, 'hi_life', 'S884', '後庄門市');`,
	},
	{
		constraint: "shipping_methods_destination_kind",
		reject:     `INSERT INTO shipping_methods (id, code, destination_kind) VALUES ('11110001-0000-4000-8000-000000000009', 'depot_pickup', 'depot');`,
		accept:     `INSERT INTO shipping_methods (id, code, destination_kind) VALUES ('11110001-0000-4000-8000-000000000009', 'depot_pickup', 'pickup_point');`,
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
		// The fixture has already entered fulfilment. Every remaining line is
		// dispatched first because orders_finished_when_shipped would otherwise
		// refuse completion before this timestamp check gets a turn.
		reject: `WITH s AS (INSERT INTO order_shipments (order_id, carrier, tracking_number) VALUES ('66666666-6666-4666-8666-666666666666', '黑貓', 'TRK-REJ') RETURNING id) INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity) SELECT ol.order_id, s.id, ol.id, ol.quantity - coalesce((SELECT sum(sl.quantity) FROM order_shipment_lines sl WHERE sl.order_line_id = ol.id), 0) FROM order_lines ol, s WHERE ol.order_id = '66666666-6666-4666-8666-666666666666' AND ol.quantity > coalesce((SELECT sum(sl.quantity) FROM order_shipment_lines sl WHERE sl.order_line_id = ol.id), 0); UPDATE orders SET fulfillment_status = 'completed' WHERE id = '66666666-6666-4666-8666-666666666666';`,
		accept: `WITH s AS (INSERT INTO order_shipments (order_id, carrier, tracking_number) VALUES ('66666666-6666-4666-8666-666666666666', '黑貓', 'TRK-ACC') RETURNING id) INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity) SELECT ol.order_id, s.id, ol.id, ol.quantity - coalesce((SELECT sum(sl.quantity) FROM order_shipment_lines sl WHERE sl.order_line_id = ol.id), 0) FROM order_lines ol, s WHERE ol.order_id = '66666666-6666-4666-8666-666666666666' AND ol.quantity > coalesce((SELECT sum(sl.quantity) FROM order_shipment_lines sl WHERE sl.order_line_id = ol.id), 0); UPDATE orders SET fulfillment_status = 'completed', completed_at = now() WHERE id = '66666666-6666-4666-8666-666666666666';`,
	},
	{
		constraint: "orders_locale_known",
		// The code and the name are read off the SAME version row, and
		// orders_shipping_snapshot_matches fires first on either invented alone.
		reject: `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name, locale)
		         SELECT '11110001-0000-4000-8000-0000000000f1', 'GO-260101-000901', v.id, m.code, v.name, 'kling-on'
		         FROM shipping_method_versions v JOIN shipping_methods m ON m.id = v.method_id LIMIT 1;`,
		accept: `INSERT INTO orders (id, order_number, shipping_version_id, shipping_method_code, shipping_method_name, locale)
		         SELECT '11110001-0000-4000-8000-0000000000f1', 'GO-260101-000901', v.id, m.code, v.name, 'en'
		         FROM shipping_method_versions v JOIN shipping_methods m ON m.id = v.method_id LIMIT 1;`,
	},
	{
		constraint: "stock_notifications_locale_known",
		reject: `INSERT INTO stock_notifications (variant_id, email, locale)
		         VALUES ((SELECT id FROM product_variants LIMIT 1), 'waiting@example.com', 'kling-on');`,
		accept: `INSERT INTO stock_notifications (variant_id, email, locale)
		         VALUES ((SELECT id FROM product_variants LIMIT 1), 'waiting@example.com', 'en');`,
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
		// Both order triggers shadow this CHECK, so session_replication_role = replica disables user
		// triggers for the transaction; CHECKs still fire.
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
		// A blank code matches no version, so orders_shipping_snapshot_matches refuses it first;
		// disable triggers to reach the presence CHECK.
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
		constraint: "payment_webhook_events_unreconciled_present",
		reject:     `INSERT INTO payment_webhook_events (provider, event_id, type, payload, unreconciled) VALUES ('stripe', 'evt_blank_reason', 'checkout.session.completed', '{}'::jsonb, '   ');`,
		accept:     `INSERT INTO payment_webhook_events (provider, event_id, type, payload, unreconciled) VALUES ('stripe', 'evt_blank_reason', 'checkout.session.completed', '{}'::jsonb, 'money arrived for a cancelled order');`,
	},
	{
		constraint: "payment_webhook_events_reconciled_was_flagged",
		reject:     `INSERT INTO payment_webhook_events (provider, event_id, type, payload, reconciled_at) VALUES ('stripe', 'evt_rec_unflagged', 'checkout.session.completed', '{}'::jsonb, now());`,
		accept:     `INSERT INTO payment_webhook_events (provider, event_id, type, payload, unreconciled, reconciled_at) VALUES ('stripe', 'evt_rec_flagged', 'checkout.session.completed', '{}'::jsonb, 'money arrived for a cancelled order', now());`,
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
		// A capture over the ceiling, triggers disabled: the range CHECK, the non-negative CHECK and
		// succeeded_is_captured all still fire, and captured_in_range is what this row trips.
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
		constraint: "product_images_alt_en_present",
		reject:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, alt_text_en, position) VALUES ('11110008-0000-4000-8000-000000000091', '33333333-3333-4333-8333-333333333333', 'x.png', '有效值', E'\t', 91);`,
		accept:     `INSERT INTO product_images (id, product_id, storage_key, alt_text, alt_text_en, position) VALUES ('11110008-0000-4000-8000-000000000091', '33333333-3333-4333-8333-333333333333', 'x.png', '有效值', 'A valid alt', 91);`,
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
		constraint: "product_option_values_value_en_present",
		reject:     `INSERT INTO product_option_values (id, product_id, option_id, value, value_en) VALUES ('11110006-0000-4000-8000-000000000092', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', '測試值', E'\t');`,
		accept:     `INSERT INTO product_option_values (id, product_id, option_id, value, value_en) VALUES ('11110006-0000-4000-8000-000000000092', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', '測試值', 'Test value');`,
	},
	{
		constraint: "product_option_values_value_present",
		reject:     `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('11110002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', E'\t');`,
		accept:     `INSERT INTO product_option_values (id, product_id, option_id, value) VALUES ('11110002-0000-4000-8000-000000000001', '33333333-3333-4333-8333-333333333333', 'aaaa0001-0000-4000-8000-000000000000', '玫瑰金');`,
	},
	{
		constraint: "product_options_name_en_present",
		reject:     `INSERT INTO product_options (id, product_id, name, name_en, position) VALUES ('11110005-0000-4000-8000-000000000091', '33333333-3333-4333-8333-333333333333', '測試軸', E'\t', 91);`,
		accept:     `INSERT INTO product_options (id, product_id, name, name_en, position) VALUES ('11110005-0000-4000-8000-000000000091', '33333333-3333-4333-8333-333333333333', '測試軸', 'Test axis', 91);`,
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
		constraint: "product_specs_label_en_present",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, label_en, position) VALUES ('11110004-0000-4000-8000-000000000093', '33333333-3333-4333-8333-333333333333', '螢幕', '有效值', E'\t', 93);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, label_en, position) VALUES ('11110004-0000-4000-8000-000000000093', '33333333-3333-4333-8333-333333333333', '螢幕', '有效值', 'Screen', 93);`,
	},
	{
		constraint: "product_specs_value_en_present",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, value_en, position) VALUES ('11110004-0000-4000-8000-000000000094', '33333333-3333-4333-8333-333333333333', '螢幕', '有效值', E'\t', 94);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, value_en, position) VALUES ('11110004-0000-4000-8000-000000000094', '33333333-3333-4333-8333-333333333333', '螢幕', '有效值', 'A valid value', 94);`,
	},
	{
		constraint: "product_specs_label_en_bounded",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, label_en, position) VALUES ('11110004-0000-4000-8000-000000000095', '33333333-3333-4333-8333-333333333333', '螢幕', '有效值', repeat('S', 41), 95);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, label_en, position) VALUES ('11110004-0000-4000-8000-000000000095', '33333333-3333-4333-8333-333333333333', '螢幕', '有效值', repeat('S', 40), 95);`,
	},
	{
		constraint: "product_specs_value_en_bounded",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, value_en, position) VALUES ('11110004-0000-4000-8000-000000000096', '33333333-3333-4333-8333-333333333333', '螢幕', '有效值', repeat('V', 201), 96);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, value_en, position) VALUES ('11110004-0000-4000-8000-000000000096', '33333333-3333-4333-8333-333333333333', '螢幕', '有效值', repeat('V', 200), 96);`,
	},
	{
		constraint: "product_specs_label_bounded",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110004-0000-4000-8000-000000000091', '33333333-3333-4333-8333-333333333333', repeat('螢', 41), '有效值', 91);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110004-0000-4000-8000-000000000091', '33333333-3333-4333-8333-333333333333', repeat('螢', 40), '有效值', 91);`,
	},
	{
		constraint: "product_specs_value_bounded",
		reject:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110004-0000-4000-8000-000000000092', '33333333-3333-4333-8333-333333333333', '螢幕', repeat('吋', 201), 92);`,
		accept:     `INSERT INTO product_specs (id, product_id, label, value, position) VALUES ('11110004-0000-4000-8000-000000000092', '33333333-3333-4333-8333-333333333333', '螢幕', repeat('吋', 200), 92);`,
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
		constraint: "products_name_en_present",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, name_en) VALUES ('11110007-0000-4000-8000-000000000091', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'blank-en-name', '空白英文', E'\t');`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, name_en) VALUES ('11110007-0000-4000-8000-000000000091', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'blank-en-name', '空白英文', 'Blank English');`,
	},
	{
		constraint: "products_summary_en_present",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, summary_en) VALUES ('11110007-0000-4000-8000-000000000092', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'blank-en-summary', '空白英文', E'\t');`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, summary_en) VALUES ('11110007-0000-4000-8000-000000000092', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'blank-en-summary', '空白英文', 'A summary');`,
	},
	{
		constraint: "products_description_en_present",
		reject:     `INSERT INTO products (id, brand_id, category_id, slug, name, description_en) VALUES ('11110007-0000-4000-8000-000000000093', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'blank-en-desc', '空白英文', E'\t');`,
		accept:     `INSERT INTO products (id, brand_id, category_id, slug, name, description_en) VALUES ('11110007-0000-4000-8000-000000000093', '11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', 'blank-en-desc', '空白英文', 'A description');`,
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
		constraint: "promo_banners_message_en_present",
		reject:     `INSERT INTO promo_banners (id, message, message_en) VALUES ('11110009-0000-4000-8000-000000000091', '有效訊息', E'\t');`,
		accept:     `INSERT INTO promo_banners (id, message, message_en) VALUES ('11110009-0000-4000-8000-000000000091', '有效訊息', 'A valid message');`,
	},
	{
		constraint: "promo_banners_message_short_en_present",
		reject:     `INSERT INTO promo_banners (id, message, message_short_en) VALUES ('11110009-0000-4000-8000-000000000092', '有效訊息', E'\t');`,
		accept:     `INSERT INTO promo_banners (id, message, message_short_en) VALUES ('11110009-0000-4000-8000-000000000092', '有效訊息', 'Short');`,
	},
	{
		constraint: "promo_banners_cta_label_en_present",
		reject:     `INSERT INTO promo_banners (id, message, cta_label, cta_href, cta_label_en) VALUES ('11110009-0000-4000-8000-000000000093', '有效訊息', '看看', '/deals', E'\t');`,
		accept:     `INSERT INTO promo_banners (id, message, cta_label, cta_href, cta_label_en) VALUES ('11110009-0000-4000-8000-000000000093', '有效訊息', '看看', '/deals', 'Shop');`,
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
		// refunds_guard would reject an over-capture amount first, so disable the trigger to let the
		// range CHECK be the rule under test. CHECKs still fire under replica.
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
		reject:     `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 0);`,
		accept:     `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1);`,
	},
	// Each ACCEPT is one step inside the rule rather than obviously legal: a comfortably legal
	// accept passes under the rule and under its absence alike.
	{
		constraint: "return_request_lines_received_bounded",
		reject:     `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity, received_quantity, restocked_quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1, 2, 0);`,
		accept:     `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity, received_quantity, restocked_quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1, 1, 0);`,
	},
	{
		constraint: "return_request_lines_restocked_bounded",
		// quantity 1, not 2: the line has shipped one unit, so a claim for two trips
		// return_within_shipment FIRST and the case then proves that rule instead.
		reject: `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity, received_quantity, restocked_quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1, 0, 1);`,
		accept: `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity, received_quantity, restocked_quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1, 1, 1);`,
	},
	{
		constraint: "return_request_lines_inspected_together",
		reject:     `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity, received_quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1, 1);`,
		// NEITHER rather than both: both passes under a rule that merely demanded received_quantity.
		accept: `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1);`,
	},
	{
		constraint: "return_request_lines_note_bounded",
		reject:     `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity, received_quantity, restocked_quantity, inspection_note) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1, 1, 1, repeat('x', 501));`,
		accept:     `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity, received_quantity, restocked_quantity, inspection_note) VALUES ('66666666-6666-4666-8666-666666666666', '88880001-0000-4000-8000-000000000000', '66660003-0000-4000-8000-000000000000', 1, 1, 1, repeat('x', 500));`,
	},
	{
		constraint: "return_requests_decided_has_time",
		// Tested from the 'requested' side so the start-requested INSERT trigger does not shadow it.
		reject: `INSERT INTO return_requests (id, order_id, reason, decided_at) VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', '退貨', now());`,
		accept: `INSERT INTO return_requests (id, order_id, reason) VALUES ('11110001-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', '退貨');`,
	},
	{
		// The ACCEPT is the EMPTY reason, deliberately: Consumer Protection Act §19 I gives a
		// rescission inside seven days with no reason at all, so a non-blank accept would lock nothing.
		constraint: "return_requests_reason_bounded",
		reject:     `INSERT INTO return_requests (id, order_id, reason) VALUES ('11110001-0000-4000-8000-000000000002', '6666aaaa-6666-4666-8666-666666666666', repeat('x', 501));`,
		accept:     `INSERT INTO return_requests (id, order_id, reason) VALUES ('11110001-0000-4000-8000-000000000002', '6666aaaa-6666-4666-8666-666666666666', '');`,
	},
	{
		constraint: "return_requests_status_known",
		// An unknown status is not 'requested', so the start-requested INSERT trigger refuses it first;
		// disable triggers to reach the CHECK.
		reject: `SET LOCAL session_replication_role = replica;
		         INSERT INTO return_requests (id, order_id, status, reason, decided_at) VALUES ('11110001-0000-4000-8000-000000000003', '6666aaaa-6666-4666-8666-666666666666', 'shipped', '退貨', now());`,
		accept: `INSERT INTO return_requests (id, order_id, reason) VALUES ('11110001-0000-4000-8000-000000000003', '6666aaaa-6666-4666-8666-666666666666', '退貨');`,
	},
	{
		constraint: "sale_campaigns_slug_format",
		reject:     `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('autumn_sale', '秋季特賣', now() + interval '7 days');`,
		accept:     `INSERT INTO sale_campaigns (slug, title, ends_at) VALUES ('autumn-sale', '秋季特賣', now() + interval '7 days');`,
	},
	{
		constraint: "sale_campaigns_title_en_present",
		reject:     `INSERT INTO sale_campaigns (id, slug, title, title_en, ends_at) VALUES ('1111000a-0000-4000-8000-000000000091', 'blank-en-title', '有效標題', E'\t', now() + interval '1 day');`,
		accept:     `INSERT INTO sale_campaigns (id, slug, title, title_en, ends_at) VALUES ('1111000a-0000-4000-8000-000000000091', 'blank-en-title', '有效標題', 'A valid title', now() + interval '1 day');`,
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
		constraint: "membership_tiers_code_format",
		reject:     `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('Gold Tier', '金卡', 5000000);`,
		accept:     `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('gold_tier', '金卡', 5000000);`,
	},
	{
		constraint: "membership_tiers_name_en_present",
		reject:     `INSERT INTO membership_tiers (id, code, name, name_en, min_spend_cents, points_multiplier_bp, position) VALUES ('1111000d-0000-4000-8000-000000000091', 'blank_en', '有效名稱', E'\t', 99000000, 10000, 91);`,
		accept:     `INSERT INTO membership_tiers (id, code, name, name_en, min_spend_cents, points_multiplier_bp, position) VALUES ('1111000d-0000-4000-8000-000000000091', 'blank_en', '有效名稱', 'A valid name', 99000000, 10000, 91);`,
	},
	{
		constraint: "membership_tiers_name_present",
		reject:     `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('blanktier', E'\t', 5000001);`,
		accept:     `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('blanktier', '金卡', 5000001);`,
	},
	{
		constraint: "membership_tiers_min_spend_non_negative",
		reject:     `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('negtier', '負卡', -1);`,
		accept:     `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('negtier', '負卡', 5000002);`,
	},
	{
		constraint: "membership_tiers_multiplier_at_least_base",
		reject:     `INSERT INTO membership_tiers (code, name, min_spend_cents, points_multiplier_bp) VALUES ('worsetier', '倒扣卡', 5000003, 9000);`,
		accept:     `INSERT INTO membership_tiers (code, name, min_spend_cents, points_multiplier_bp) VALUES ('worsetier', '倒扣卡', 5000003, 12000);`,
	},
	{
		constraint: "shipping_zones_code_format",
		reject:     `INSERT INTO shipping_zones (id, code, name) VALUES ('11110004-0000-4000-8000-000000000001', 'Off Shore', '離島');`,
		accept:     `INSERT INTO shipping_zones (id, code, name) VALUES ('11110004-0000-4000-8000-000000000001', 'off_shore', '離島');`,
	},
	{
		constraint: "shipping_zones_name_en_present",
		reject:     `INSERT INTO shipping_zones (id, code, name, name_en) VALUES ('1111000c-0000-4000-8000-000000000091', 'blank_en', '有效名稱', E'\t');`,
		accept:     `INSERT INTO shipping_zones (id, code, name, name_en) VALUES ('1111000c-0000-4000-8000-000000000091', 'blank_en', '有效名稱', 'A valid name');`,
	},
	{
		constraint: "shipping_zones_name_present",
		reject:     `INSERT INTO shipping_zones (id, code, name) VALUES ('11110004-0000-4000-8000-000000000002', 'blank', E'\t');`,
		accept:     `INSERT INTO shipping_zones (id, code, name) VALUES ('11110004-0000-4000-8000-000000000002', 'blank', '離島');`,
	},
	{
		// Three digits: the lookup takes left(postal_code, 3), so a five-digit prefix matches nothing.
		constraint: "shipping_zone_prefixes_format",
		reject: `INSERT INTO shipping_zones (id, code, name) VALUES ('11110004-0000-4000-8000-000000000003', 'z', '離島');
		         INSERT INTO shipping_zone_prefixes (prefix, zone_id) VALUES ('89052', '11110004-0000-4000-8000-000000000003');`,
		accept: `INSERT INTO shipping_zones (id, code, name) VALUES ('11110004-0000-4000-8000-000000000003', 'z', '離島');
		         INSERT INTO shipping_zone_prefixes (prefix, zone_id) VALUES ('890', '11110004-0000-4000-8000-000000000003');`,
	},
	{
		// A zero surcharge is the ABSENCE of a row: the lookup coalesces a missing one to no surcharge.
		constraint: "shipping_version_zones_surcharge_positive",
		reject: `INSERT INTO shipping_zones (id, code, name) VALUES ('11110004-0000-4000-8000-000000000004', 'z2', '離島');
		         INSERT INTO shipping_version_zones (version_id, zone_id, surcharge_cents)
		         SELECT v.id, '11110004-0000-4000-8000-000000000004', 0 FROM shipping_method_versions v LIMIT 1;`,
		accept: `INSERT INTO shipping_zones (id, code, name) VALUES ('11110004-0000-4000-8000-000000000004', 'z2', '離島');
		         INSERT INTO shipping_version_zones (version_id, zone_id, surcharge_cents)
		         SELECT v.id, '11110004-0000-4000-8000-000000000004', 20000 FROM shipping_method_versions v LIMIT 1;`,
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
		constraint: "shipping_method_versions_name_en_present",
		reject:     `INSERT INTO shipping_method_versions (id, method_id, name, name_en, fee_cents, effective_at) VALUES ('1111000b-0000-4000-8000-000000000091', 'ffff0001-0000-4000-8000-000000000000', '有效名稱', E'\t', 8000, '2027-02-01');`,
		accept:     `INSERT INTO shipping_method_versions (id, method_id, name, name_en, fee_cents, effective_at) VALUES ('1111000b-0000-4000-8000-000000000091', 'ffff0001-0000-4000-8000-000000000000', '有效名稱', 'A valid name', 8000, '2027-02-01');`,
	},
	{
		constraint: "shipping_method_versions_carrier_en_present",
		reject:     `INSERT INTO shipping_method_versions (id, method_id, name, carrier_en, fee_cents, effective_at) VALUES ('1111000b-0000-4000-8000-000000000092', 'ffff0001-0000-4000-8000-000000000000', '有效名稱', E'\t', 8000, '2027-03-01');`,
		accept:     `INSERT INTO shipping_method_versions (id, method_id, name, carrier_en, fee_cents, effective_at) VALUES ('1111000b-0000-4000-8000-000000000092', 'ffff0001-0000-4000-8000-000000000000', '有效名稱', 'A carrier', 8000, '2027-03-01');`,
	},
	{
		constraint: "shipping_method_versions_name_present",
		reject:     `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', E'\t', 8000, '2027-01-01');`,
		accept:     `INSERT INTO shipping_method_versions (id, method_id, name, fee_cents, effective_at) VALUES ('11110001-0000-4000-8000-000000000001', 'ffff0001-0000-4000-8000-000000000000', '快遞', 8000, '2027-01-01');`,
	},
	{
		// NULL is unmeasured, which the shipping filter reads as "refuse nothing".
		constraint: "product_variants_parcel_longest_sane",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position, parcel_longest_mm) VALUES ('11110008-0000-4000-8000-000000000009', '33333333-3333-4333-8333-333333333333', 'PXL-9P-PARCEL', 3390000, 0, 91, 0);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position, parcel_longest_mm) VALUES ('11110008-0000-4000-8000-000000000009', '33333333-3333-4333-8333-333333333333', 'PXL-9P-PARCEL', 3390000, 0, 91, 180);`,
	},
	{
		constraint: "product_variants_parcel_sum_sane",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position, parcel_sum_mm) VALUES ('11110008-0000-4000-8000-000000000009', '33333333-3333-4333-8333-333333333333', 'PXL-9P-PARCEL', 3390000, 0, 91, 0);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position, parcel_sum_mm) VALUES ('11110008-0000-4000-8000-000000000009', '33333333-3333-4333-8333-333333333333', 'PXL-9P-PARCEL', 3390000, 0, 91, 320);`,
	},
	{
		constraint: "product_variants_parcel_weight_sane",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position, parcel_weight_g) VALUES ('11110008-0000-4000-8000-000000000009', '33333333-3333-4333-8333-333333333333', 'PXL-9P-PARCEL', 3390000, 0, 91, 0);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position, parcel_weight_g) VALUES ('11110008-0000-4000-8000-000000000009', '33333333-3333-4333-8333-333333333333', 'PXL-9P-PARCEL', 3390000, 0, 91, 400);`,
	},
	{
		constraint: "product_variants_parcel_sum_covers_longest",
		reject:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position, parcel_longest_mm, parcel_sum_mm) VALUES ('11110008-0000-4000-8000-000000000009', '33333333-3333-4333-8333-333333333333', 'PXL-9P-PARCEL', 3390000, 0, 91, 500, 400);`,
		accept:     `INSERT INTO product_variants (id, product_id, sku, price_cents, safety_stock, position, parcel_longest_mm, parcel_sum_mm) VALUES ('11110008-0000-4000-8000-000000000009', '33333333-3333-4333-8333-333333333333', 'PXL-9P-PARCEL', 3390000, 0, 91, 180, 320);`,
	},
	{
		// NULL is "no stated limit", which is what makes the filter refuse nothing.
		constraint: "shipping_methods_max_longest_positive",
		reject:     `INSERT INTO shipping_methods (id, code, max_parcel_longest_mm) VALUES ('11110001-0000-4000-8000-000000000009', 'parcel_test', 0);`,
		accept:     `INSERT INTO shipping_methods (id, code, max_parcel_longest_mm) VALUES ('11110001-0000-4000-8000-000000000009', 'parcel_test', 450);`,
	},
	{
		constraint: "shipping_methods_max_sum_positive",
		reject:     `INSERT INTO shipping_methods (id, code, max_parcel_sum_mm) VALUES ('11110001-0000-4000-8000-000000000009', 'parcel_test', 0);`,
		accept:     `INSERT INTO shipping_methods (id, code, max_parcel_sum_mm) VALUES ('11110001-0000-4000-8000-000000000009', 'parcel_test', 1050);`,
	},
	{
		constraint: "shipping_methods_max_weight_positive",
		reject:     `INSERT INTO shipping_methods (id, code, max_parcel_weight_g) VALUES ('11110001-0000-4000-8000-000000000009', 'parcel_test', 0);`,
		accept:     `INSERT INTO shipping_methods (id, code, max_parcel_weight_g) VALUES ('11110001-0000-4000-8000-000000000009', 'parcel_test', 10000);`,
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
		// A positive credit over the ceiling: the guard permits it (the balance only climbs), so the
		// range CHECK is what refuses it.
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
		index:  "membership_tiers_code_key",
		accept: `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('dupcode', '重複', 7000000);`,
		reject: `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('dupcode', '重複', 7000000);
		         INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('dupcode', '另一個', 7000001);`,
	},
	{
		index:  "membership_tiers_min_spend_key",
		accept: `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('bandone', '一', 7000002);`,
		reject: `INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('bandone', '一', 7000002);
		         INSERT INTO membership_tiers (code, name, min_spend_cents) VALUES ('bandtwo', '二', 7000002);`,
	}, {
		index:  "shipping_zones_code_key",
		accept: `INSERT INTO shipping_zones (code, name) VALUES ('offshore_two', '離島二');`,
		reject: `INSERT INTO shipping_zones (code, name) VALUES ('offshore_two', '離島二');
		         INSERT INTO shipping_zones (code, name) VALUES ('offshore_two', '另一個');`,
	}, {
		index: "loyalty_entries_idempotency_key",
		// TWICE. One insert cannot collide with itself, and a case that inserts once reports that the
		// database accepted a duplicate it never wrote.
		reject: `INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 'award', 10, 'test', 'fixture-dup', current_date + 365);
		         INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 'award', 10, 'test', 'fixture-dup', current_date + 365);`,
		accept: `INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on) VALUES ('a0000001-0000-4000-8000-000000000000', 'award', 10, 'test', 'fixture-other', current_date + 365);`,
	},
	{
		index: "return_requests_one_open",
		// The paid fixture order already has an open request. This deliberately
		// uses the separate unpaid fixture, whose absence of an open request makes
		// the first insert meaningful and the second one the collision.
		reject: `INSERT INTO return_requests (id, order_id, reason)
		         VALUES ('11110025-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'first');
		         INSERT INTO return_requests (id, order_id, reason)
		         VALUES ('11110025-0000-4000-8000-000000000002', '6666aaaa-6666-4666-8666-666666666666', 'second');`,
		accept: `INSERT INTO return_requests (id, order_id, reason)
		         VALUES ('11110025-0000-4000-8000-000000000001', '6666aaaa-6666-4666-8666-666666666666', 'only');`,
	},
	{
		index: "coupons_code_key",
		// Case-insensitive: SUMMER20 and summer20 are one code to a customer reading a card.
		reject: `INSERT INTO coupons (id, code, description, kind, amount_cents) VALUES ('cccc0003-0000-4000-8000-000000000001', 'fixturecode', '測試', 'amount', 20000);`,
		accept: `INSERT INTO coupons (id, code, description, kind, amount_cents) VALUES ('cccc0003-0000-4000-8000-000000000001', 'OTHERCODE', '測試', 'amount', 20000);`,
	},
	{
		index:  "coupon_redemptions_order_key",
		reject: `INSERT INTO coupon_redemptions (id, coupon_id, order_id, amount_cents) VALUES ('cccc0004-0000-4000-8000-000000000001', 'cccc0009-0000-4000-8000-000000000009', '66666666-6666-4666-8666-666666666666', 0);`,
		accept: `INSERT INTO coupon_redemptions (id, coupon_id, order_id, amount_cents) VALUES ('cccc0004-0000-4000-8000-000000000001', 'cccc0009-0000-4000-8000-000000000009', '6666aaaa-6666-4666-8666-666666666666', 0);`,
	},
	{
		index:  "store_credit_accounts_user_id_key",
		reject: `INSERT INTO store_credit_accounts (id, user_id) VALUES ('11115002-0000-4000-8000-000000000001', '55555555-5555-4555-8555-555555555555');`,
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
		// One document per request key: ECPay's allowance endpoint carries no
		// idempotency field, so a second press must be refused here or it
		// becomes a second 折讓 at the 財政部.
		index:  "invoice_documents_request_key",
		reject: `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, request_key) VALUES ('11110001-0000-4000-8000-000000000033', '66666666-6666-4666-8666-666666666666', 'allowance', '99990001-0000-4000-8000-000000000000', 'GD-90000033', 100, 'dup-key'); INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, request_key) VALUES ('11110001-0000-4000-8000-000000000034', '66666666-6666-4666-8666-666666666666', 'allowance', '99990001-0000-4000-8000-000000000000', 'GD-90000034', 100, 'dup-key');`,
		accept: `INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, request_key) VALUES ('11110001-0000-4000-8000-000000000033', '66666666-6666-4666-8666-666666666666', 'allowance', '99990001-0000-4000-8000-000000000000', 'GD-90000033', 100, 'dup-key-a'); INSERT INTO invoice_documents (id, order_id, kind, original_id, number, amount_cents, request_key) VALUES ('11110001-0000-4000-8000-000000000034', '66666666-6666-4666-8666-666666666666', 'allowance', '99990001-0000-4000-8000-000000000000', 'GD-90000034', 100, 'dup-key-b');`,
	},
	{
		// NULLS NOT DISTINCT is the half worth exercising: both rows here are
		// ROOTS, so a plain UNIQUE (parent_id, position) would treat their NULL
		// parents as distinct and accept the pair — the header's own order, left
		// to whatever the planner returned.
		index:  "categories_position_key",
		reject: `INSERT INTO categories (id, slug, name, position) VALUES ('11110003-0000-4000-8000-000000000001', 'phones-3', '手機三', 0);`,
		accept: `INSERT INTO categories (id, slug, name, position) VALUES ('11110003-0000-4000-8000-000000000001', 'phones-3', '手機三', 7);`,
	},
	{
		index:  "categories_slug_key",
		reject: `INSERT INTO categories (id, slug, name, position) VALUES ('11110002-0000-4000-8000-000000000001', 'phones', '手機二', 15);`,
		accept: `INSERT INTO categories (id, slug, name, position) VALUES ('11110002-0000-4000-8000-000000000001', 'phones-2', '手機二', 15);`,
	},
	{
		index:  "checkout_attempts_order_key",
		reject: `INSERT INTO checkout_attempts (idempotency_key, order_id) VALUES ('MDAwMDAwMDAwMDAwMDAwMQ', '66666666-6666-4666-8666-666666666666'); INSERT INTO checkout_attempts (idempotency_key, order_id) VALUES ('MDAwMDAwMDAwMDAwMDAwMg', '66666666-6666-4666-8666-666666666666');`,
		accept: `INSERT INTO checkout_attempts (idempotency_key, order_id) VALUES ('MTExMTExMTExMTExMTExMQ', NULL); INSERT INTO checkout_attempts (idempotency_key, order_id) VALUES ('YWJjZGVmZ2hpamtsbW5vcA', NULL);`,
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
		// A SECOND PENDING claim, not a second number. The old index was whole-table,
		// and a pending claim carries '' to say it has no number yet — so the accept
		// below passed under both versions of the rule while two claims on two
		// different orders collided on the empty string, which is #28's paired
		// lesson: a statement both versions admit proves nothing about either.
		accept: `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, status, request_key) VALUES ('11110001-0000-4000-8000-000000000011', '6666aaaa-6666-4666-8666-666666666666', 'invoice', '', 100, 'pending', 'claim-one');
		         INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents, status, request_key) VALUES ('11110001-0000-4000-8000-000000000012', '6666bbbb-6666-4666-8666-666666666666', 'invoice', '', 100, 'pending', 'claim-two');`,
	},
	{
		index: "invoice_documents_one_active_invoice_per_order",
		// Voiding the first — the dimension the partial index excludes — frees the slot for a reissue.
		reject: `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-00000000001a', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-ACT-A', 100);
		         INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-00000000001b', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-ACT-B', 100);`,
		accept: `INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-00000000001a', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-ACT-A', 100);
		         UPDATE invoice_documents SET status = 'voided', voided_at = now() WHERE id = '11110001-0000-4000-8000-00000000001a';
		         INSERT INTO invoice_documents (id, order_id, kind, number, amount_cents) VALUES ('11110001-0000-4000-8000-00000000001b', '6666aaaa-6666-4666-8666-666666666666', 'invoice', 'GD-ACT-B', 100);`,
	},
	{
		index:  "email_verifications_user_key",
		reject: `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'a@example.com', sha256('v1'::bytea), now() + interval '1 day'); INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'b@example.com', sha256('v2'::bytea), now() + interval '1 day');`,
		accept: `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'a@example.com', sha256('v1'::bytea), now() + interval '1 day');`,
	},
	{
		index:  "email_verifications_digest_key",
		reject: `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'a@example.com', sha256('v1'::bytea), now() + interval '1 day'); INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users OFFSET 1 LIMIT 1), 'b@example.com', sha256('v1'::bytea), now() + interval '1 day');`,
		accept: `INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users LIMIT 1), 'a@example.com', sha256('v1'::bytea), now() + interval '1 day'); INSERT INTO email_verifications (user_id, email, digest, expires_at) VALUES ((SELECT id FROM users OFFSET 1 LIMIT 1), 'b@example.com', sha256('v2'::bytea), now() + interval '1 day');`,
	},
	{
		index:  "newsletter_subscribers_email_key",
		reject: `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('Reader@Example.com', 'a-token-of-at-least-thirty-two-chars'); INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('reader@example.com', 'another-token-of-thirty-two-plus-chars');`,
		accept: `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('reader1@example.com', 'a-token-of-at-least-thirty-two-chars'); INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('reader2@example.com', 'another-token-of-thirty-two-plus-chars');`,
	},
	{
		index:  "newsletter_subscribers_unsubscribe_token_key",
		reject: `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('r1@example.com', 'a-token-of-at-least-thirty-two-chars'); INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('r2@example.com', 'a-token-of-at-least-thirty-two-chars');`,
		accept: `INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('r1@example.com', 'a-token-of-at-least-thirty-two-chars'); INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ('r2@example.com', 'another-token-of-thirty-two-plus-chars');`,
	},
	{
		// Three submissions must leave one live link, not three keys sitting in a mailbox.
		index:  "newsletter_confirmations_email_key",
		reject: `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('Ask@Example.com', sha256('c1'::bytea), now() + interval '1 day'); INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask@example.com', sha256('c2'::bytea), now() + interval '1 day');`,
		accept: `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask1@example.com', sha256('c1'::bytea), now() + interval '1 day'); INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask2@example.com', sha256('c2'::bytea), now() + interval '1 day');`,
	},
	{
		index:  "newsletter_confirmations_digest_key",
		reject: `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask1@example.com', sha256('c1'::bytea), now() + interval '1 day'); INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask2@example.com', sha256('c1'::bytea), now() + interval '1 day');`,
		accept: `INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask1@example.com', sha256('c1'::bytea), now() + interval '1 day'); INSERT INTO newsletter_confirmations (email, digest, expires_at) VALUES ('ask2@example.com', sha256('c2'::bytea), now() + interval '1 day');`,
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
		index: "payments_one_active_per_order",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
		         VALUES ('11110004-0000-4000-8000-000000000003','6666aaaa-6666-4666-8666-666666666666','pi_active_first','requires_payment',3690000);
		         INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
		         VALUES ('11110004-0000-4000-8000-000000000004','6666aaaa-6666-4666-8666-666666666666','pi_active_second','processing',3690000);`,
		accept: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
		         VALUES ('11110004-0000-4000-8000-000000000003','6666aaaa-6666-4666-8666-666666666666','pi_active_first','requires_payment',3690000);
		         INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents)
		         VALUES ('11110004-0000-4000-8000-000000000004','6666aaaa-6666-4666-8666-666666666666','pi_terminal_neighbour','cancelled',3690000);`,
	},
	{
		index:  "payments_one_capture_per_order",
		reject: `INSERT INTO payments (id, order_id, provider_ref, status, intended_amount_cents, captured_amount_cents, paid_at) VALUES ('11110004-0000-4000-8000-000000000002','66666666-6666-4666-8666-666666666666','pi_second_cap','succeeded',6790000,6790000,now());`,
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
