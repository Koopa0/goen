-- Bounded rewrite of one published refund FAQ. Match each locale to the
-- Stripe-only sentence so a shop-edited answer stays. The catalogue seed
-- cannot do this: its INSERT stops on the first kept brand.
UPDATE faq_entries
SET answer = '退貨經審核同意後,系統依原付款組成退回:卡款立刻向 Stripe 發出退款,店儲退回購物金。卡款入帳時間依發卡銀行而定,通常是數個工作天;額度退回後可立刻使用。'
WHERE question = '退款什麼時候會收到?'
  AND answer = '退貨經審核同意後,系統會立即向 Stripe 發出退款。實際入帳時間依發卡銀行而定,通常是數個工作天。';

UPDATE faq_entries
SET answer_en = 'As soon as a return is approved we pay it back the way you paid: the card share through Stripe, store credit back to your balance. When a card refund lands depends on your card issuer, usually a few working days; credit is available again at once.'
WHERE question = '退款什麼時候會收到?'
  AND answer_en = 'As soon as a return is approved we ask Stripe to refund. When it lands depends on your card issuer, usually a few working days.';
