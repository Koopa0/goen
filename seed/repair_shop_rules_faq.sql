-- Match each locale independently so shop-authored answers are preserved.
UPDATE faq_entries
SET answer = '在結帳頁的「折扣碼」欄位輸入即可，大小寫不拘。每筆訂單限用一組折扣碼，折抵金額不會超過商品小計。百分比折扣碼是照購物車內商品目前的售價小計計算，已在特價的商品也一併折抵，特價與折扣碼可以疊加。'
WHERE question IN ('折扣碼要怎麼使用?', '折扣碼要怎麼使用？')
  AND answer = '在結帳頁的「折扣碼」欄位輸入即可,大小寫不拘。每筆訂單限用一組折扣碼,折抵金額不會超過商品小計。';

UPDATE faq_entries
SET answer_en = 'Type it into the discount field at checkout; case does not matter. One code per order, and the discount never exceeds the item subtotal. A percentage code is worked out on the cart subtotal at the items'' current prices, so items already on sale are discounted too: a sale and a code stack.'
WHERE question IN ('折扣碼要怎麼使用?', '折扣碼要怎麼使用？')
  AND answer_en = 'Type it into the discount field at checkout; case does not matter. One code per order, and the discount never exceeds the item subtotal.';

UPDATE faq_entries
SET answer = '不用。goen 支援訪客結帳，只需要填寫收件資訊。註冊後可以查看訂單紀錄、使用願望清單，以及累積點數、使用購物金。點數自取得起一年到期；用點數兌換成的購物金不會到期。'
WHERE question IN ('一定要註冊才能購買嗎?', '一定要註冊才能購買嗎？')
  AND answer = '不用。goen 支援訪客結帳,只需要填寫收件資訊。註冊後可以查看訂單紀錄、使用願望清單,以及累積與使用商店額度。';

UPDATE faq_entries
SET answer_en = 'No. goen supports guest checkout — you only need delivery details. Registering lets you see your order history, keep a wishlist, and earn points and spend store credit. Points expire one year after you earn them; store credit you redeem from points does not expire.'
WHERE question IN ('一定要註冊才能購買嗎?', '一定要註冊才能購買嗎？')
  AND answer_en = 'No. goen supports guest checkout — you only need delivery details. Registering lets you see your order history, keep a wishlist, and earn and spend store credit.';

UPDATE faq_entries
SET answer = '可以。結帳時選擇「公司統編」並填入八位數字的統一編號即可。公司統編發票會存入綠界電子發票載具，寄到結帳時填的電子郵件，可在綠界的載具中查詢。'
WHERE question IN ('可以開公司統編嗎?', '可以開公司統編嗎？')
  AND answer = '可以。結帳時選擇「公司統編」並填入八位數字的統一編號即可。';

UPDATE faq_entries
SET answer_en = 'Yes. Choose "company tax ID" at checkout and enter the eight digits. A company tax ID invoice is stored in the ECPay e-invoice carrier, tied to your checkout email, and can be retrieved there.'
WHERE question IN ('可以開公司統編嗎?', '可以開公司統編嗎？')
  AND answer_en = 'Yes. Choose "company tax ID" at checkout and enter the eight digits.';
