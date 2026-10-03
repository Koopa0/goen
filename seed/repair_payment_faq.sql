-- Match each locale independently so shop-authored answers are preserved.
UPDATE faq_entries
SET answer = '目前接受信用卡付款，由 Stripe 處理，goen 不會接觸到你的卡片資料。付款頁面在 Stripe 網域上，完成後會自動回到訂單頁。登入後，帳號內可用的購物金會在結帳時自動折抵，剩餘金額再以信用卡付款。折扣碼是價格折抵，不是付款方式。'
WHERE question IN ('可以用哪些方式付款?', '可以用哪些方式付款？')
  AND answer = '目前接受信用卡付款,由 Stripe 處理,goen 不會接觸到您的卡片資料。付款頁面在 Stripe 網域上,完成後會自動回到訂單頁。';

UPDATE faq_entries
SET answer_en = 'Credit card, handled by Stripe. goen never sees your card details: the payment page is on Stripe''s own domain and you return to your order afterwards. When you are signed in, your available store credit comes off the order automatically at checkout, and any remaining amount is paid by card. A discount code reduces the price; it is not a payment method.'
WHERE question IN ('可以用哪些方式付款?', '可以用哪些方式付款？')
  AND answer_en = 'Credit card, handled by Stripe. goen never sees your card details: the payment page is on Stripe''s own domain and you return to your order afterwards.';

