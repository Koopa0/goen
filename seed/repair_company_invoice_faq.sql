-- Preserve merchant-edited answers while updating the known company-invoice copy.
UPDATE faq_entries
SET answer = CASE WHEN answer = '可以。結帳時選擇「公司統編」並填入八位數字的統一編號即可。' THEN '可以。結帳時選「公司統編」，填入公司名稱與八位統編。預設依結帳 Email 留存在綠界，也可選手機條碼；兩者均保留公司資訊，不提供紙本寄送。' ELSE answer END,
    answer_en = CASE WHEN answer_en = 'Yes. Choose "company tax ID" at checkout and enter the eight digits.' THEN 'Yes. Choose "company tax ID" and enter the registered company name and eight-digit tax ID. The default is an ECPay carrier held against your checkout email; you may choose a mobile barcode instead. Both retain the company identity. Paper delivery is not offered.' ELSE answer_en END
WHERE question = '可以開公司統編嗎?';
