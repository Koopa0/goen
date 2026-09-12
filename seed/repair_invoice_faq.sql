-- Bounded rewrite of the published invoice FAQ. A missing merchant id is a
-- deployment, not an unfinished product. Match the question so no other
-- catalogue row moves. Rewrite only a locale whose answer still exactly matches
-- the known stale copy; a merchant-edited locale is left alone.
UPDATE faq_entries
SET answer = CASE
        WHEN answer = '結帳時可以選擇會員載具、手機條碼載具或公司統編,系統會記錄您的選擇。電子發票的實際開立需要串接加值中心,這部分尚未完成。'
        THEN '結帳時可以選擇會員載具、手機條碼載具或公司統編,系統會記錄您的選擇。這份部署若已設定綠界加值中心,後台會依該選擇開立電子發票;尚未設定時不會開立,後台會說明原因。'
        ELSE answer
    END,
    answer_en = CASE
        WHEN answer_en = 'At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and we record your choice. Actually issuing the electronic invoice needs an integration with a certified provider, which is not built yet.'
        THEN 'At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and we record your choice. When this deployment has ECPay credentials the back office issues the electronic invoice against that choice; without them nothing is filed, and the back office says so.'
        ELSE answer_en
    END
WHERE question = '發票怎麼開立?';
