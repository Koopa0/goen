-- Match each locale independently so shop-authored answers are preserved.
UPDATE faq_entries
SET answer = '信用卡由 Stripe 處理,goen 不會接觸到您的卡片資料。登入後可在結帳時選擇使用帳號內可用的商店額度,剩餘金額再以信用卡付款。折扣碼是價格折抵,不是付款方式;可先輸入有效折扣碼,再確認應付金額。'
WHERE question = '可以用哪些方式付款?'
  AND answer = '目前接受信用卡付款,由 Stripe 處理,goen 不會接觸到您的卡片資料。付款頁面在 Stripe 網域上,完成後會自動回到訂單頁。';

UPDATE faq_entries
SET answer_en = 'Credit cards are handled by Stripe; goen never sees your card details. When signed in, you can choose to apply your available store credit at checkout and pay any remaining amount by card. A discount code reduces the price; it is not a payment method. Apply a valid code before checking the amount due.'
WHERE question = '可以用哪些方式付款?'
  AND answer_en = 'Credit card, handled by Stripe. goen never sees your card details: the payment page is on Stripe''s own domain and you return to your order afterwards.';

