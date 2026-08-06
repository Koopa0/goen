# 審查請求 ① — goen 資料庫 schema

> 整段複製給 Codex。若 Codex 沒有 repo 存取權,請一併附上
> `migrations/001_initial_schema.up.sql`(912 行)與
> `internal/db/schema_integration_test.go`。

---

## 背景

goen 是一個台灣 3C 電商:繁體中文介面、單一幣別 TWD、Stripe Payment Element 金流、
支援訪客結帳、前台與後台在同一個 Go binary。後端是 Go 1.26 + pgx/v5 + sqlc,
資料庫 PostgreSQL 18。

**專案尚未上線,沒有任何正式資料。** schema 可以自由重新設計,不需要考慮遷移成本 —
所以請直接說「這裡該改成什麼」,不必顧慮相容性。

## 審查目標

審查 `migrations/001_initial_schema.up.sql`(單一檔案定義全部 40 張表),以及驗證它的
`internal/db/schema_integration_test.go`。

**請以「這個 schema 上線半年後會在哪裡出事」的角度審,不是「這裡可以更漂亮」。**

規模參考:40 張表、79 個 CHECK 約束、40 個外鍵、101 個索引。

## 已經被機器驗證過的部分(請不要重複回報這些)

這些不是我的宣稱,是實際跑過的結果 — 如果你認為驗證方式本身有漏洞,那才值得回報:

- **48 個整合測試全綠**,跑在 testcontainers 起的真實 PostgreSQL 18 上。
  每個 CHECK 都被餵過一個「必須被拒絕」的值,並且另外餵過一個相鄰的「必須被接受」的值
  (否則一個「拒絕全部」的約束會看起來跟正確的一樣)。
- **squawk 0 issues**(migration linter,全規則開啟)。
- **每個外鍵都有以它為前綴的索引**,由測試強制。這一條抓出過 3 個真的遺漏。
- **沒有任何衍生值欄位**(total / subtotal / balance / average / count),由測試強制。

## 請優先檢查(依重要性排序)

### 1. BCNF 是否真的成立

檔案開頭聲稱每個關聯都在 BCNF。請逐表檢查是否存在非平凡函數相依 X → Y 而 X 不是超鍵。
特別針對這幾個刻意的決定:

- `orders` 存 `discount_cents / shipping_cents / tax_cents`,**刻意不存** subtotal 與 total
- `order_lines` 存 `unit_price_cents / quantity`,**刻意不存** line_total
- `order_lines` 同時存了 `variant_id` 和 `sku / product_name / variant_label` 快照
- `orders` 同時存了 `shipping_method_code` 與 `shipping_method_name`
- `payments.amount_cents` 是 Stripe 實際扣款金額,與訂單明細算出來的總額**可能不一致**

若你認為其中任何一項其實違反 BCNF,請指出:是哪個 FD、determinant 是什麼、為什麼它不是超鍵。
若你認為「符合 BCNF 但實務上是錯的」,也請說 — 理論正確不等於設計正確。

### 2. 並行與競態(最可能出事的地方,請認真找)

- **超賣**:兩人同時買最後一件,`product_variants.stock_quantity` 這個設計會超賣嗎?
  schema 應該提供什麼(條件式 UPDATE / SELECT FOR UPDATE / 庫存保留表 / EXCLUDE 約束)?
  目前庫存只有一個 `stock_quantity` 欄位,沒有異動流水帳。
- **訂單編號**:格式 `GO-YYMMDD-NNNN`(CHECK 強制),但 schema **沒有提供產生機制**。
  並行下要怎麼產生才不會重號?這個缺口該由 schema 補(sequence / 函數)還是應用層?
- **預設地址**:`addresses` 用 partial unique index 保證每人一個預設。
  「把 A 設為預設」與「把 B 設為預設」並行執行會發生什麼?應用層需要什麼保護?
- **Stripe webhook**:會重送、也可能亂序(succeeded 早於 created 到達)。
  `payment_webhook_events` 的 PK `(provider, provider_ref)` 足夠做冪等嗎?
  亂序時 `payments.status` 會被寫成錯的值嗎?
- **購物車合併**:訪客有 cart(cookie token),登入後要併入會員 cart。
  `carts` 的設計(`user_id` nullable + `token_hash` unique)撐得住並行合併嗎?

### 3. 金額與正確性

金額一律 `bigint`、單位是分(TWD 在 Stripe 是兩位小數,NT$33,900 → 3390000)。

- 有沒有任何地方會發生單位混用(分 vs 元)?
- 多次部分退款時,`refunds` 的模型撐得住嗎?退款總額不得超過付款額,這條要靠什麼保證?
- 免運門檻在 `shipping_methods.free_over_cents`,但訂單存的是算好的 `shipping_cents`。
  這個快照/規則的分離有沒有問題?

### 4. 刪除語意與個資

預設 `ON DELETE RESTRICT`,只有子項不能脫離父項時才 CASCADE。請找出:

- 用了 CASCADE 但會刪掉不該刪的歷史(訂單、付款、保固)
- 用了 RESTRICT 但會讓正常營運卡死(下架商品、刪除帳號、合併分類)
- **個資刪除**:使用者要求刪除帳號時,這個 schema 能否在保留訂單財務紀錄的前提下做到?
  `orders.user_id` 是 `ON DELETE SET NULL`,但 `order_addresses` 存了收件人姓名電話地址 —
  這樣算刪乾淨嗎?

### 5. CHECK 約束的漏洞

79 個 CHECK。請找出:

- 哪個 CHECK 其實擋不住它想擋的東西(只擋單向、或可用 NULL 繞過)
- 哪個 CHECK 太嚴,會擋掉合法的營運情境
- `orders_cancelled_has_time` 用雙向綁定 `(status='cancelled') = (cancelled_at IS NOT NULL)`,
  但 `orders_completed_has_time` 只有單向 `status<>'completed' OR completed_at IS NOT NULL`。
  這個不一致是 bug 還是刻意?
- `return_requests_resolved_has_time` 寫成 `status IN ('requested') = (resolved_at IS NULL)`,
  這個寫法正確嗎?(`IN` 單一元素 + 等號比較)
- `orders` 的狀態機有 9 個狀態,但 schema 沒有限制狀態轉移。
  哪些非法轉移(例如 refunded → pending)會造成實質損害,值得用 trigger 擋?

### 6. 索引

外鍵索引已由測試覆蓋。除此之外:

- 商品列表頁要按「分類 + 品牌 + 價格區間 + 容量/顏色 + 只顯示有現貨」篩選,
  再按「綜合/價格升降/最新/評分」排序並分頁。目前索引支援得了嗎?缺什麼?
- `products_category_published_idx` 是 partial index(`WHERE status='active'`)。
  查詢必須怎麼寫才會用到它?這個限制有沒有寫在該寫的地方?
- 有沒有多餘、永遠不會被用到的索引?
- 全文搜尋(header 搜尋框寫「搜尋商品、品牌或規格」)完全沒有索引支援,
  這在 40 表的設計裡是不是該現在就決定(tsvector 欄位 vs 外部搜尋引擎)?

### 7. 真正缺少的東西

這個 schema 是從 8 份 UI 設計稿反推的。請指出任何「電商一定會需要、但這裡沒有」的東西,
**並說明它為什麼不能等到之後再加**。能等的請不要提。

## 明確不需要評論的

- 命名風格、縮排、註解長度
- 「應該用 ORM / 應該用 enum type / 應該用 bigserial 而不是 uuid」這類偏好,
  除非你能說出一個具體會出事的場景
- 尚未實作的功能:優惠券主檔、多幣別、多語系內容 — 已知延後
- 測試檔的寫法,除非測試本身其實驗不到它宣稱驗的東西

## 已知且刻意的決定

不是疏漏,請不要當成發現重複回報。但**如果你認為決定本身是錯的,請說明為什麼**:

| 決定 | 理由 |
|---|---|
| 不存任何衍生值(小計/總計/餘額/平均評分) | 存了就可能與明細矛盾,一律計算 |
| 購物金用 ledger 而非餘額欄位 | 餘額必須等於流水加總,不給它機會漂移 |
| 狀態用 `text` + CHECK 而非 PG enum type | 加狀態變成換約束,不是型別遷移 |
| 主鍵用 `uuidv7()` | 時間排序,索引局部性接近序號但不可猜測 |
| 商品規格 label 是自由文字,無共用字典 | 對應後台 UI 就是自由文字輸入;跨商品比較靠 label 比對 |
| 庫存只有 `stock_quantity`,無異動流水帳 | 已知簡化 —(但第 2 點的超賣問題請照樣提) |
| 優惠券只在 `orders.discount_code` 留字串 | coupon 主檔是 v2 |
| `payments.amount_cents` 可能與明細總額不符 | 刻意讓不一致變成看得見的異常,而不是被 schema 藏起來 |

## 輸出格式

每個發現請給:

```
[嚴重度: 會壞資料 / 會出錯 / 會變慢 / 可改進]
問題:一句話
證據:哪張表哪一行,以及一個具體會失敗的情境(什麼輸入 → 什麼結果)
建議:具體的 SQL 或設計改法
```

**每一節(1–7)都請明確交代結論**,即使是「檢查過,沒有發現問題」。
我需要知道哪些面向真的被看過,而不是被略過。
