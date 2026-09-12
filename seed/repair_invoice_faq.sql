-- Bounded rewrite of the published invoice FAQ. A missing merchant id is a
-- deployment, not an unfinished product. Match the question so no other
-- catalogue row moves. The catalogue seed cannot do this: its INSERT stops
-- on the first kept brand.
UPDATE faq_entries
SET answer = '結帳時可以選擇會員載具、手機條碼載具或公司統編,系統會記錄您的選擇。這份部署若已設定綠界加值中心,後台會依該選擇開立電子發票;尚未設定時不會開立,後台會說明原因。',
    answer_en = 'At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and we record your choice. When this deployment has ECPay credentials the back office issues the electronic invoice against that choice; without them nothing is filed, and the back office says so.'
WHERE question = '發票怎麼開立?';
