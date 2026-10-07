-- Probe only: states the status badge shots must show. Run as the owner with
-- triggers off, no transaction, so one refusal does not stop the rest.
\set VERBOSITY terse
SET session_replication_role = replica;

-- A staff member who has enrolled in two-factor, beside the admin who has not.
SET ROLE admin;
SELECT upsert_staff('shop-lead@goen.invalid', '店長', 'staff');
RESET ROLE;
INSERT INTO staff_totp_credentials (user_id, secret_encrypted, confirmed_at)
SELECT id, '\x01'::bytea, now() FROM users WHERE lower(email) = 'shop-lead@goen.invalid'
ON CONFLICT DO NOTHING;

-- Twelve questions waiting, and one the shop answered.
INSERT INTO product_questions (product_id, user_id, body, created_at)
SELECT p.id, (SELECT id FROM users WHERE role = 'customer' ORDER BY created_at LIMIT 1),
       '請問第 ' || g || ' 個問題：這款有現貨嗎？', now() - make_interval(hours => g)
FROM generate_series(1, 12) AS g
CROSS JOIN LATERAL (SELECT id FROM products WHERE status = 'active' ORDER BY slug OFFSET (g % 3) LIMIT 1) p;
WITH q AS (
    INSERT INTO product_questions (product_id, user_id, body, created_at)
    SELECT id, (SELECT id FROM users WHERE role = 'customer' ORDER BY created_at LIMIT 1),
           '請問可以開統一發票嗎？', now() - interval '2 days'
    FROM products WHERE status = 'active' ORDER BY slug LIMIT 1
    RETURNING id
)
INSERT INTO product_answers (question_id, user_id, body, is_staff, created_at)
SELECT q.id, (SELECT id FROM users WHERE role = 'admin' ORDER BY created_at LIMIT 1),
       '可以，結帳時選擇發票類型即可。', true, now() - interval '1 day'
FROM q;

-- Messages: today, one day, overdue, handled.
INSERT INTO contact_messages (name, email, subject, message, handled_at, created_at) VALUES
    ('王小明', 'a@goen.invalid', '訂單問題', '想確認出貨時間。', NULL, now() - interval '1 hour'),
    ('陳美玲', 'b@goen.invalid', '商品諮詢', '請問這款有沒有白色？', NULL, now() - interval '1 day 2 hours'),
    ('林志豪', 'c@goen.invalid', '保固維修', '保固期內的機器不開機了。', NULL, now() - interval '4 days'),
    ('張雅婷', 'd@goen.invalid', '退換貨', '想問退貨流程。', now() - interval '1 day', now() - interval '6 days');

-- Returns in each of the eight states.
CREATE TEMP TABLE cand AS
SELECT o.id AS order_id, ol.id AS line_id, ol.quantity AS qty,
       row_number() OVER (ORDER BY o.placed_at DESC) AS rn
FROM orders o
JOIN LATERAL (SELECT id, quantity FROM order_lines WHERE order_id = o.id ORDER BY position LIMIT 1) ol ON true
WHERE o.fulfillment_status IN ('delivered', 'completed') AND o.user_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM return_requests r WHERE r.order_id = o.id)
LIMIT 8;

-- The stranded payout owes store credit to an order with no account.
UPDATE orders SET user_id = NULL WHERE id = (SELECT order_id FROM cand WHERE rn = 5);

INSERT INTO return_requests (order_id, requested_by_user_id, reason, status, resolution, decided_at,
                             goods_refund_cents, card_refund_cents, credit_refund_cents,
                             before_shipment, created_at)
SELECT c.order_id, (SELECT user_id FROM orders WHERE id = c.order_id), v.reason, v.status, v.resolution,
       CASE WHEN v.status = 'requested' THEN NULL ELSE now() - interval '1 day' END,
       CASE WHEN v.status IN ('requested', 'rejected') THEN NULL ELSE v.goods END,
       CASE WHEN v.status IN ('requested', 'rejected') THEN NULL ELSE v.card END,
       CASE WHEN v.status IN ('requested', 'rejected') THEN NULL ELSE v.credit END,
       v.before_shipment, now() - interval '2 days'
FROM (VALUES
    (1, '尺寸不合', 'requested', NULL, 0, 0, 0, false),
    (2, '顏色不喜歡', 'approved', '同意退貨', 0, 0, 0, false),
    (3, '商品有瑕疵', 'approved', '同意退貨', 0, 0, 0, false),
    (4, '收到時外盒破損', 'approved', '同意退貨', 1000, 1000, 0, false),
    (5, '重複下單', 'approved', '同意退貨', 1000, 0, 1000, false),
    (6, '超過鑑賞期', 'rejected', '已超過期限', 0, 0, 0, false),
    (7, '功能與描述不同', 'completed', '已退款', 0, 0, 0, false),
    (8, '出貨前取消', 'completed', '已退款', 0, 0, 0, true)
) AS v (n, reason, status, resolution, goods, card, credit, before_shipment)
JOIN cand c ON c.rn = v.n;

INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity,
                                  received_quantity, restocked_quantity)
SELECT c.order_id, r.id, c.line_id, 1,
       CASE WHEN r.reason IN ('商品有瑕疵') THEN 1 END,
       CASE WHEN r.reason IN ('商品有瑕疵') THEN 1 END
FROM return_requests r JOIN cand c ON c.order_id = r.order_id
WHERE r.reason IN ('尺寸不合', '顏色不喜歡', '商品有瑕疵', '收到時外盒破損', '重複下單', '超過鑑賞期', '功能與描述不同');

-- Orders waiting for payment, and orders being picked.
UPDATE orders SET fulfillment_status = 'pending'
WHERE id IN (SELECT o.id FROM orders o
             WHERE o.fulfillment_status = 'cancelled'
               AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.order_id = o.id AND p.status = 'succeeded')
             ORDER BY o.placed_at DESC LIMIT 2);
UPDATE orders SET fulfillment_status = 'picking'
WHERE id IN (SELECT o.id FROM orders o
             WHERE o.fulfillment_status = 'shipped'
               AND NOT EXISTS (SELECT 1 FROM return_requests r WHERE r.order_id = o.id)
             ORDER BY o.placed_at DESC LIMIT 2);
SELECT fulfillment_status, count(*) FROM orders GROUP BY 1 ORDER BY 1;

SELECT status, before_shipment, count(*) FROM return_requests GROUP BY 1, 2 ORDER BY 1, 2;
SELECT count(*) AS questions FROM product_questions;
SELECT count(*) AS messages FROM contact_messages;
SELECT count(*) AS enrolled FROM staff_totp_credentials WHERE confirmed_at IS NOT NULL;
