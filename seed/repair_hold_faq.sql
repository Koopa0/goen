-- Match each locale independently so shop-authored answers are preserved.
UPDATE faq_entries
SET answer = '會。送出訂單的同時系統就會保留庫存 60 分鐘，請在下單後 29 分鐘內開始付款。保留時間結束仍未付款的訂單會自動取消，商品回到架上供其他人購買，使用的購物金也會退回。'
WHERE question = '下單之後商品會保留嗎？'
  AND answer = '會。送出訂單的同時系統就會保留庫存 60 分鐘,讓您完成付款。超過時間未付款,商品會回到架上供其他人購買,訂單仍然保留,可以重新付款(若庫存還在)。';

UPDATE faq_entries
SET answer_en = 'Yes. Placing the order holds the stock for 60 minutes; start the payment within 29 minutes of ordering. An order still unpaid when the hold ends is cancelled automatically: the item goes back on the shelf for other people, and any store credit you applied is returned.'
WHERE question = '下單之後商品會保留嗎？'
  AND answer_en = 'Yes. Placing the order holds the stock for 60 minutes so you can pay. After that the item goes back on the shelf for other people, but your order stays and you can pay again if it is still available.';
