# 設計需求 Brief — Go Full-Stack 3C 電商網站

> 這份文件是要直接貼給另一個負責視覺設計的 Claude 對話使用的 prompt/context。目標是產出**可以直接對應到 server-rendered Go + htmx + Tailwind 架構**的高保真 HTML/CSS mockup,而不是抽象的線框圖或純美術稿。

---

## 1. 專案背景

我們正在打造一個全新的 **3C / 電子產品電商網站**,後端是 Go(`net/http` + `templ` server-side rendering + `htmx` 局部更新),資料庫是 PostgreSQL,金流串接 Stripe Payment Element(嵌入式付款表單)。這是一個全新品牌,目前**還沒有品牌名稱、logo、或正式色彩系統**——這部分需要你在設計過程中一併提出建議。

目標客群:對規格敏感、會比價比規格的一般消費者(手機、筆電、耳機、智慧穿戴、周邊配件等),偏年輕到中壯年、有一定科技產品知識,重視「值得信任的購物體驗」(保固、退換貨政策、金流安全感)。

---

## 2. 品牌方向(品牌名稱已定案,圖形標誌交給 Imagen 2.0 另外處理)

- **品牌名稱:`goen`(全小寫)。** 命名邏輯,請完整保留、不要更動:
  - **Go** —— 後端技術核心(Golang)
  - **ご縁(go-en)** —— 日文真實詞彙,指人與人、人與物之間的「緣分/連結」
  - **五円(go-en)** —— 與「ご縁」同音,日本神社供奉五円硬幣正是取其諧音討吉利(日本神社本廳有相關說明),因此帶有「良緣、好兆頭」的文化聯想
  - 三層意義收斂到電商本質:**讓買家與對的好商品相遇**
- **Wordmark 排版方向:** 主要標準字用全小寫「**goen**」,下方或右側搭配較小字級的輔助文字「**ご縁**」,類似 furigana 標音的層次感。整體要呼應 yomihon 那種低調、現代、偏開源專案感的氣質,**不要用全大寫「GOEN」**,那會偏向企業科技公司感,跟這個品牌的調性不符。這組 wordmark 用真實字體排版即可,不需要圖像生成。
- **圖形標誌(logo mark)不需要你設計**——這部分已經另外交給 Imagen 2.0 生成。你只需要在畫面上**預留 logo 的版位**(header 左上角、favicon、footer),抓一個接近正方形的區塊給圖形標誌 + wordmark 的組合(lockup)使用即可。
- **風格調性:極簡現代(minimalist / modern)**——大量留白、中性色調為主(黑/白/灰階)。可以考慮呼應五円硬幣的黃銅/金色調作為強調色的候選之一,但最終配色會等 Imagen 產出的 logo mark 定案後再回頭確認,現階段請提出 2–3 組候選配色(其中至少一組偏五円黃銅/金色調),不用定案。
- 字體：偏好幾何無襯線字體(如 Inter、Geist、Noto Sans TC 的組合),要能同時處理**繁體中文與英文雙語排版**,兩種語言的字重/行高要視覺一致。
- 請提出:
  1. 色彩 token(primary / neutral 階層 / semantic 色如 success/warning/danger/info,含一組黃銅色調候選)
  2. 字體階層(H1–H6、body、caption、按鈕文字的字級與字重)
  3. wordmark「goen / ご縁」的排版比例建議(主副文字的字級比例、間距、對齊方式)

---

## 3. 技術限制與設計原則(必須遵守,這是能不能被實作出來的關鍵)

1. **沒有前端框架(no React/Vue/SPA)。** 頁面是 Go `templ` 產生的 server-rendered HTML,大部分互動是「整頁換頁」。只有明確標註「htmx 局部更新」的區塊,才會是局部換內容(不整頁刷新)。請在設計稿上**明確標註哪些區塊是 htmx swap 目標**,例如:
   - 購物車數量調整、移除品項 → 局部更新購物車小計
   - 商品列表的篩選/排序 → 局部更新商品格線區塊,不刷新整頁(URL 需可分享,所以篩選狀態要反映在網址上)
   - 結帳流程的步驟切換(地址 → 運送方式 → 付款)→ 可用 htmx 局部換步驟內容,或整頁換頁皆可,兩種都行,但要在稿子上註記你選哪種
   - 「加入購物車」按鈕 → 局部更新導覽列購物車數量 badge + 顯示一個 toast/mini cart drawer
2. **Stripe 是 Payment Element(embedded),不是導轉到 Stripe 網域。** 結帳付款步驟裡,請保留一個明確的「付款表單掛載區塊」(方形留白區域,標註「Stripe Payment Element 掛載處」),裡面的卡號/到期日/CVC 欄位是 Stripe.js 用 iframe 掛進來的,**不要自己刻卡片輸入欄位的視覺**,只要框出容器區域、上方/下方的其他欄位(如電子發票、備註)才需要自己設計。
3. **CSS 用 Tailwind v4 utility classes。** 輸出的 HTML mockup 請直接用 Tailwind 的 utility class 寫(不要用自訂大量 CSS class 或 CSS-in-JS),這樣可以直接複製貼到 `templ` 檔案裡幾乎不用改。
4. **雙語:繁體中文為主、英文為輔。** 需要一個語言切換入口(通常放在 header 右上角或 footer),兩種語言版本文字長度會不同(英文通常更長),設計時預留彈性,不要用固定寬度容器卡死文字。
5. **無障礙(accessibility)要當一回事,不是裝飾。** 至少要達到 WCAG 2.1 AA 對比度、所有互動元件要有清楚的 focus 樣式(不能只有 hover)、圖片要有替代文字的設計空間(alt text 不影響視覺但要在交付時提醒)、表單要有清楚的錯誤提示樣式(不能只靠顏色)。
6. **RWD 斷點:mobile 375px / tablet 768px / desktop 1440px。** 每一個畫面都需要 mobile 版和 desktop 版兩種稿子;tablet 可用推斷,不強制每頁都出。
7. **MVP 階段先不用做深色模式(dark mode)**,先專心把 light mode 做好,dark mode 之後再說。
8. **後台管理(admin)畫面走功能導向即可**,不用花太多心力在美術上,但要跟前台共用同一套 design token(同一個色彩/字體系統),因為技術上是同一個 Go binary、同一套 CSS。

---

## 4. 完整畫面清單

### A. 前台 Storefront(所有頁面都要有 mobile + desktop 版)

1. **首頁 Home**
   - Hero banner(可輪播,主打商品/活動)
   - 分類導覽(手機/筆電/耳機/穿戴/配件等圖示化入口)
   - 精選商品區塊(新品、熱銷、限時優惠各一區)
   - 信任區塊(保固說明、免運門檻、退換貨政策的簡短圖示化說明)
   - Footer(客服、政策連結、社群、電子報訂閱、語言切換)

2. **商品分類/列表頁 Category / Listing**
   - 麵包屑導覽
   - 左側或頂部篩選面板:品牌、價格區間、規格(容量/顏色/尺寸等,3C 常見規格 facet)
   - 排序選項(價格高低、新品、熱銷、評分)
   - 商品卡片格線(圖片、名稱、價格、庫存狀態 badge、快速加入購物車按鈕)
   - 分頁或載入更多
   - **htmx 標註**:篩選/排序/翻頁都是局部更新商品格線區塊

3. **商品詳情頁 Product Detail Page**
   - 圖片主圖 + 縮圖列(支援放大檢視)
   - 商品名稱、價格、庫存狀態、促銷標籤
   - 變體選擇(顏色/容量/型號——3C 常見,選了不同變體價格與庫存要能反映)
   - 加入購物車 / 立即購買 按鈕
   - Tab 或 Accordion 切換:商品描述 / 完整規格表(spec table,3C 必備)/ 保固與退換貨 / 評論評分
   - 相關商品/常一起購買的配件推薦區塊
   - **htmx 標註**:變體切換更新價格與庫存區塊、加入購物車更新導覽列 badge

4. **搜尋結果頁 Search Results**
   - 搜尋框(header 常駐)、搜尋結果與分類列表頁共用格線元件
   - 無結果時的空狀態設計(建議提供熱門分類/商品作為救援)

5. **購物車 Cart**(建議做成右側 drawer,手機版全螢幕)
   - 品項列表(圖片、名稱、變體、單價、數量調整、移除)
   - 小計、預估運費、繼續購物 / 前往結帳按鈕
   - 空購物車狀態設計
   - **htmx 標註**:整個購物車 drawer 內容都是局部更新

6. **結帳流程 Checkout**(多步驟,請設計清楚的步驟指示 stepper)
   - 6a. **入口分岔**:「使用 Email/密碼登入」/「使用 Google 登入(OAuth)」/「以訪客身份結帳(guest checkout)」三選一
   - 6b. 收件資訊(姓名、電話、地址,可從地址簿選擇——僅登入會員可見地址簿)
   - 6c. 運送方式選擇(宅配/超商取貨等,含運費與預估到貨時間)
   - 6d. 付款(**Stripe Payment Element 掛載區**,見第 3 節說明;另含電子發票資訊欄位)
   - 6e. 訂單確認/完成頁(訂單編號、明細、預估到貨、追蹤連結;若是訪客結帳,這裡要有明顯的「建立帳號保留這筆訂單」CTA)

7. **登入 Login**
   - Email + 密碼欄位、「使用 Google 登入」按鈕、忘記密碼連結、前往註冊連結

8. **註冊 Sign Up**
   - Email + 密碼(含密碼強度提示)、或 OAuth 快速註冊

9. **忘記密碼 / 重設密碼 Forgot / Reset Password**

10. **靜態頁面**:關於我們、聯絡我們、FAQ(建議用 accordion 元件)、退換貨政策、隱私權政策、服務條款

### B. 會員中心 Customer Account

11. 帳戶總覽(Dashboard):快捷連結到訂單、地址、個人資料
12. 訂單紀錄列表(Order History):狀態 badge(處理中/已出貨/已完成/已取消)
13. 訂單詳情頁(Order Detail):品項明細、物流追蹤狀態時間軸、發票資訊
14. 收件地址管理(Address Book):新增/編輯/刪除/設為預設
15. 個人資料/密碼修改(Profile Settings):基本資料、綁定的 OAuth 帳號顯示、修改密碼
16. 願望清單 Wishlist(可選,先出設計但標註為 v2 可延後實作)

### C. 後台管理 Admin(功能導向,共用前台 design token 即可)

17. 管理後台登入(獨立於客戶登入頁,視覺上要能明顯區分「這是後台」)
18. 儀表板 Dashboard:今日/本週銷售概況、待處理訂單數量提醒
19. 商品管理 Product CRUD:商品列表、新增/編輯表單(含圖片上傳、變體與庫存設定、規格表編輯)
20. 分類管理 Category management
21. 訂單管理 Order management:列表、篩選狀態、訂單詳情內可更新狀態/處理退款
22. 客戶管理 Customer management:列表、單一客戶的訂單歷史檢視
23. 優惠券/促銷管理 Discount management(可選,標註為 v2)
24. 庫存管理 Inventory:庫存量檢視、低庫存警示

### D. 系統性頁面

25. 404 / 500 錯誤頁(要有清楚的「回首頁」引導,維持品牌調性不要太生硬)
26. Email 樣板:訂單確認信、出貨通知信、密碼重設信(HTML email,注意這類版型要用 table-based layout 相容各家信箱客戶端,不能直接用 Tailwind flex/grid)

---

## 5. 共用元件系統(Design System / Component Inventory)

請額外整理一份「元件庫」頁面,列出以下元件的各種狀態(default / hover / focus / disabled / error / loading):

- 按鈕(primary / secondary / ghost / danger,含 icon-only 版本)
- 表單元件(text input、select、checkbox、radio、textarea)含錯誤驗證狀態顯示方式
- 商品卡片(含缺貨、促銷標籤、新品標籤等 badge 變化)
- 數量調整器(quantity stepper)
- 標籤/徽章(stock status、sale、new、free shipping)
- Modal / Drawer(購物車 drawer、確認刪除 modal)
- Toast / 通知提示(加入購物車成功、表單錯誤提示)
- 分頁(pagination)
- Tabs / Accordion(商品詳情頁、FAQ 頁用)
- 麵包屑(breadcrumbs)
- 規格比較表(spec table,3C 商品必備,行列要能清楚比較多個變體/型號的差異)
- 評分星星(rating stars,若第一版有評論功能)
- 訂單狀態時間軸(order status timeline)
- 空狀態(empty state):空購物車、無搜尋結果、無訂單紀錄、無願望清單

---

## 6. 明確不需要做的(避免範圍擴大)

- 不需要設計原生 App(iOS/Android)介面,只做響應式網頁
- 不需要多租戶/多商城機制,這是單一品牌店面
- 不需要複雜的 CMS 頁面編輯器介面,靜態頁面內容視為固定版型
- 不需要 dark mode(v1 階段)
- 不需要設計複雜的多層次會員分級/積分系統介面(先做基本會員即可)

---

## 7. 交付格式要求

請針對第 4 節列出的每一個畫面,產出:

1. **高保真 HTML/CSS mockup**(用 Tailwind utility class 寫,不要用外部 CSS 檔),桌面版(1440px 寬)與手機版(375px 寬)各一份,可以用互動式 HTML 檔案呈現(不需要真的能送出表單,靜態畫面即可,但按鈕/連結的 hover 狀態要能看到)。
2. 內容請填入**真實感的繁中電子產品文案**(不要用 lorem ipsum),例如具體的手機/耳機/筆電型號名稱、規格數字、價格(可用 NT$ 表示),讓後續工程實作時能直接參考文案結構與長度。
3. 在畫面上用註解或旁邊的說明文字,**標註哪些區塊是 htmx 局部更新區、哪些是 Stripe Payment Element 掛載區**(對應第 3 節的規則)。
4. 先交付品牌方向(第 2 節)讓我們確認後,再展開其餘畫面,避免整批畫面因為色彩/字體方向不合又要重工。

---

## 8. 交付進度追蹤

- [x] 品牌名稱定案:`goen`(見第 2 節)
- [x] Logo mark 生成 prompt 已交給 Imagen 2.0(見 `logo-brief.md`)
- [x] 共用元件庫(第 5 節全項)已完成:按鈕(4 變體 × 5 狀態)、表單(含錯誤/disabled)、商品卡(4 種 badge)、數量調整器、篩選 chips、評分星星、麵包屑/Tabs/分頁/結帳 stepper、訂單狀態時間軸、購物車 drawer、確認 modal、toast、FAQ accordion、規格比較表、4 種空狀態,以及 htmx(藍虛線)/ Stripe Payment Element(灰虛線)標註圖例(可在 Tweaks 隱藏)
- [ ] 畫面分批交付(每批含 1440 桌機版 + 375 手機版),順序:
  1. 首頁 Home ← 設計已交付,尚未實作
  2. 商品列表 + 搜尋結果 ← 設計已交付,尚未實作
  3. 商品詳情頁 PDP ← 設計已交付,尚未實作
  4. 購物車 + 結帳流程 ← 設計已交付,尚未實作
  5. 登入 / 註冊 ← 設計已交付,尚未實作
  6. 會員中心 ← 設計已交付,尚未實作
  7. 後台管理 ← 設計已交付,尚未實作
  8. 錯誤頁 + Email 樣板 ← 設計已交付,尚未實作

### 已實作(Go)

- [x] **關於我們 + 聯絡我們**(`goen About Contact.dc.html`)— 含全站 Header/Footer、
  聯絡表單(PostgreSQL 寫入、htmx 局部更新、no-JS PRG 後備)、電子報訂閱、404 頁。
  同時建立整個 Go 專案基底(templ / pgx / sqlc / migrations / Docker / CI gate)。
  實作慣例與決策記錄在 `CLAUDE.md`。

每一批的詳細補充需求另見 `docs/screens/NN-xxx.md`。
