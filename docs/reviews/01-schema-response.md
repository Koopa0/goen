# 審查 ① 的處置紀錄 — schema

逐條回應 Codex 的審查。每一項都標明**驗證方式**,不只是「已修正」。

驗證環境:PostgreSQL 18.4(testcontainers)。所有「已證實」都是實際跑過 SQL 的結果。

---

## 先處理:對機器驗證前提的指控

### 「48 個 subtest 沒有做到 79 個 CHECK 各一組」

**指控成立,而且比指控的更嚴重。**

我寫了一支腳本,逐一刪掉 migration 裡的每個具名 CHECK 再跑測試:

```
named CHECK constraints: 65
caught by the suite:     16
deleted without notice:  48
```

**65 個裡有 48 個可以刪掉而測試全綠。** 我在審查 prompt 裡寫的「每個 CHECK 都被餵過
一個必須被拒絕的值」是假話。

處置:整套測試改成**從系統目錄列舉**,而不是靠手寫清單。

- `TestEveryCheckConstraintIsExercised` 讀 `pg_constraint`,任何 CHECK 沒有案例就紅
- 反向也擋:案例指名一個資料庫沒有的約束,同樣紅(避免 schema 改了測試默默失效)
- `TestCheckConstraintsReject` 不再接受「任何錯誤」,要求 `SQLSTATE=23514`
  **且** `PgError.ConstraintName` 等於案例指名的那一個 —— 指控裡說的
  「Google identity case 同時撞兩個 UNIQUE」那類假通過現在會失敗
- 唯一索引與 trigger 各有同樣的完整性閘門

複驗(同一支刪除腳本,對新測試):

```
caught by the suite:     98
deleted without notice:   0
skipped:                  2   ← 多行 CHECK,腳本切不乾淨
```

那 2 個手動刪除驗證,兩個都 CAUGHT。**0 個能溜過去。**

現況:130 個 CHECK、43 個唯一索引、17 個規則 trigger,全部有案例,共 378 個子測試。

### 「外鍵索引測試把 partial index 誤認為完整索引」

**指控成立。** `stock_notifications.variant_id` 只有
`WHERE notified_at IS NULL` 的 partial index,已通知的資料仍會全表掃描。

處置:查詢加上 `i.indpred IS NULL AND i.indisvalid AND i.indislive`。
收緊後又抓出**另外 2 個**同類問題(`carts.user_id`、
`variant_option_values` 的複合外鍵)。三個都補了完整索引。

### 「TestNoStoredDerivedValues 只是欄名黑名單」

**指控成立。** 已刪除該測試。BCNF 的保證改由 schema 本身承擔(見下),
不再假裝有一個測試在證明它。

---

## 1. BCNF

### 「單值欄位形成 ∅ → provider/currency」

**指控成立。** 檔頭的嚴格宣稱是假的。

處置:兩件事一起做。
- 宣稱改寫,明確列出**兩個刻意的例外**(singleton discriminator、read projection),
  以及為什麼保留它們比追求形式 BCNF 有價值
- `currency` 真正釘死:`CHECK (currency = 'TWD')`,orders 與 payments 都有

已證實(修正前):`currency='JPY'` 可寫入 → 分為單位語意立即錯亂。

### 「return_requests.user_id 存在 order_id → user_id」

**指控成立**,而且指出了資安面向:schema 接受「A 的 order_id + B 的 user_id」。

處置:改名 `requested_by_user_id`,並在註解寫明它是**申請人**不是訂單擁有人
(客服也會代開),擁有人一律經 `order_id` 取得。

### 「product_images 若允許重用 key,則 storage_key → width,height」

**指控成立。** 處置:加 `UNIQUE (storage_key)`。一個儲存物件就是一組像素。

### 「檔頭說『存 subtotal 必然違反 BCNF』不精確」

**指控成立。** 決定是對的,理由寫錯了。已改寫成真正的理由:跨表 aggregate 會漂移。

---

## 2. 並行與競態

全部四項都先**實測重現**,再修,再用 mutation 證明測試看得見。

### 超賣

已證實可重現。處置:
- `inventory_movements` 流水帳(append-only trigger 擋 UPDATE/DELETE)
- `record_inventory_movement()` 是 `stock_quantity` 的**唯一寫入者**,
  可用量檢查寫在 UPDATE 自己的 WHERE 裡 —— 讀與寫是同一個語句對同一個鎖定列
- `inventory_reservations` 給 Payment Element 期間的保留,有 `expires_at` 與掃描索引

測試 `TestStockCannotOversell` 用**決定性交錯**(T1 開著不提交,T2 同時進來)。

### 訂單編號重號

已證實格式 CHECK 擋不住、也沒有產生機制。處置:`order_number_counters`
每營業日一列 + `next_order_number()` 原子遞增。

採納了「四位數每天上限 9,999」的意見,改成**六位**。

### 預設地址並行切換

指控說「不會壞資料,但一個正常請求會失敗」——**正確**。partial unique index 保留為最後防線。
應用層先鎖 user 的做法記在 schema 註解,留給實作階段。

### Webhook 重送與亂序

已證實。處置:
- `payment_webhook_events` 增加 `object_ref`、`provider_created_at`、`payload jsonb`
- `payments_no_regression` trigger:`succeeded/failed/cancelled` 是終態,不會被晚到的事件推回去
- `payments_one_capture_per_order` partial unique:一張訂單不可能有兩筆成功扣款

### 購物車合併

已證實一個 user 可以有多台車。處置:`carts_one_per_user` partial unique
(訪客車 `user_id IS NULL` 不受限,測試明確驗這一點)。

另加 `checkout_attempts(idempotency_key PK, order_id UNIQUE)`,
擋雙擊/HTTP retry 產生兩張訂單兩個 PaymentIntent。

---

## 3. 金額

### 「payments.amount_cents 無法在所有狀態代表實際扣款」

**指控成立。** PaymentIntent 的 amount 是 intended,不是 captured。

處置:拆成 `intended_amount_cents NOT NULL` 與 `captured_amount_cents NULL`,
並用雙向 CHECK 綁定:`(status='succeeded') = (paid_at IS NOT NULL AND captured_amount_cents IS NOT NULL)`。

### 「部分退款可累計超過實扣,且有雙退 crash window」

已證實:capture 100、兩筆 60 都成功。

處置:
- `refunds_within_capture` trigger,**先鎖 payment 再 SUM**
- `provider_ref` 改 nullable + `request_key UNIQUE`。先寫本地 pending row、
  再拿它當 Stripe `Idempotency-Key` —— 消除了「API 成功後 crash 就遺失退款事實」的窗口
- 補齊 `requires_action`/`cancelled` 狀態與 `succeeded_at`/`failed_at`

測試 `TestRefundsCannotRacePastCapture` 用決定性交錯。
Mutation:拿掉 `FOR UPDATE` → **3/3 次紅**。

### 「訂單可算出負總額,可沒有明細沒有地址,付款後可竄改」

三項都已證實。處置:
- `orders_have_lines` deferred constraint trigger:commit 前驗至少一筆明細、
  有收件資料、總額 ≥ 0
- `order_lines_frozen_once_paid` / `orders_money_frozen_once_paid`:
  一旦有成功扣款,明細與金額就是歷史

### 「購物金可透支、可重試雙扣、可事後竄改」

三項都已證實。處置:`store_credit_accounts` anchor row(給 `FOR UPDATE` 用)、
`idempotency_key UNIQUE`、`store_credit_never_negative` trigger(先鎖再 SUM)、
append-only trigger。

Mutation:拿掉鎖 → **3/3 次紅**。

### 「配送方式沒有合法來源或歷史規則版本」

已證實可寫入不存在的 code。處置:`shipping_method_versions` append-only,
訂單存 version FK + 實付運費快照。

### 「price × quantity 可 overflow」

**指控成立。** 處置:`price_cents <= 10000000000`(NT$1 億),
`quantity <= 999`。註解宣稱的三四位數上限現在是真的。

---

## 4. 刪除與個資

### 「訂單 root 不是 append-only,一次 DELETE 可 cascade 掉發票物流事件保固」

已證實。處置:訂單子表全部改 `ON DELETE RESTRICT`,
`order_events` 加 append-only trigger。

### 「有評論的帳號無法刪;沒評論的刪了仍留 PII」

**指控成立,而且答案確實是「這不算刪乾淨」。**

處置:
- `product_reviews.user_id` 改 nullable + `ON DELETE SET NULL`
- PII 拆到 `order_private_data`(email/收件人/電話/地址),
  帶 `erased_at`,並用 CHECK 強制**全清或全留**——半清的列沒人能推理
- `orders` 本身不再持有任何 PII

### 「刪帳會 CASCADE 刪除購物金流水」

已證實。處置:`store_credit_entries.user_id` 改指向
`store_credit_accounts` 並 `ON DELETE RESTRICT`。

### 「已發布商品仍可 hard delete 把評論全刪」

處置:`product_reviews.product_id` 改 RESTRICT。下架用 `status='archived'`。

---

## 5. CHECK 與狀態機

### 「orders.status 把三個生命週期塞進一欄」

**指控成立** —— 這是最結構性的一項。

處置:改名 `fulfillment_status`,只剩 6 個履約狀態。付款狀態以 `payments` 為準,
退款以 `return_requests`/`refunds` 為準。
加 `orders_legal_transition` trigger,禁止 pending→shipped、終態復活等。

### 「cancelled/completed 時間約束不一致,且註解宣稱的互斥不成立」

**指控成立,而且我實測發現更嚴重的後果:雙向 CHECK 讓取消過的訂單永遠無法轉成 refunded。**

處置:改成指控建議的形狀 —— `cancelled_at IS NULL OR completed_at IS NULL` 真正互斥,
兩個時間各自 `>= placed_at`,狀態綁定改單向(狀態會前進,但發生過的時刻是歷史)。

### 「return_requests_resolved_has_time 語法正確,但若 approved 不算 resolved 應改名」

**接受。** 已改名 `decided_at`。

### 「category CHECK 擋不住多節點 cycle」

已證實 A→B→A 可成立。處置:`categories_acyclic` trigger,遞迴查祖先,
並取 `pg_advisory_xact_lock` —— 沒有它,兩個交易各自看起來無環卻能一起提交成環。

### 「btrim presence CHECK 可被 tab 繞過」

已證實 `E'\t'` 可寫入。處置:**全部 presence CHECK 改成 `~ '[^[:space:]]'`**,
並補上原本完全沒有 presence 檢查的欄位(電話、郵遞區號、縣市、行政區、
order line sku/name、配送快照等)。

email 唯一索引問題也已證實(`' a@example.com '` 可與正常帳號並存)。
處置:加 `CHECK (email = btrim(email))`。

### 「限時優惠註解聲稱一定有折扣,但沒有強制」

處置:`sale_campaign_needs_discount` trigger,加入活動前先驗有可售且有 compare-at 的變體。

---

## 6. 索引

### 分頁 tie-breaker

**指控成立。** 批次發布共用 timestamp 會讓 keyset 分頁跳過剩餘商品。
處置:所有列表索引加 `id DESC` 作最終鍵。

### `product_variants_low_stock_idx` 無效

**指控成立** —— 原本索引 `(stock_quantity) WHERE is_active` 無法回答
「低於自己的安全庫存」。處置:predicate 改成欄位比較本身。

### 篩選/排序組合、全文搜尋

採納。已加 `products_category_brand_published_idx`、
`product_variants_sellable_price_idx`、`variant_option_values_value_variant_idx`,
以及 `product_search_documents` + `pg_trgm` GIN(理由同指控:繁中商品文案沒有詞界,
需要的是子字串比對)。

「同一 variant 必須同時滿足所有 facet」這個查詢正確性問題不是 schema 能解的,
記在註解裡留給列表頁實作。

### partial index 必須字面寫死 `status='active'`

**接受。** 已寫進 `CLAUDE.md` 的「可預測的錯誤」清單。

---

## 7. 真正缺少的東西

全部採納並實作:

| 缺項 | 處置 |
|---|---|
| variant-option graph 可跨商品、可一個 variant 兩個顏色 | 複合外鍵貫穿 product_id;PK 改 `(variant_id, option_id)` |
| 退貨/分批出貨/保固缺 line-level 數量 | `return_request_lines`、`order_shipment_lines`、warranty 改**每台一列**(`unit_no`) |
| 發票混用偏好與已開立文件,無作廢/折讓 | 拆成 `invoice_preferences` + append-only `invoice_documents`(kind: invoice/allowance) |
| 無 transactional outbox | `outbox_messages`,dedupe key + 可用時間 |
| 無稽核/庫存歷史 | `audit_events`、`inventory_movements`,兩者 append-only |

已證實(修正前):買 2 件只能登錄 1 筆保固 —— 指控完全正確。

---

## 未採納的

**沒有。** 這次審查的每一項我都採納了。

三項只做了一半,原因寫在這裡:

1. **預設地址並行切換**:保留 partial unique 作最後防線,先鎖 user 的做法
   寫在註解,等會員中心實作時一起做。schema 層沒有更好的答案。
2. **facet 查詢正確性**(藍色沒貨/黑色有貨誤判):這是查詢寫法問題,不是 schema 問題。
   記在註解,列表頁實作時處理。
3. **storefront read projection**(min_price/rating/rank):同意指控說的
   「這是不存衍生值規則在讀模型上的必要例外」,但現在沒有效能數據支撐,
   等列表頁跑起來、量到再決定。目前只做了搜尋的 projection,因為那個沒有替代方案。

---

## 第一輪處置之後的追加變更

這些發生在處置紀錄寫完之後,一併列出以免複審對照到過期的樹。

**補上三個缺失的外鍵。** 三個欄位名為 `order_id` 卻沒有指向 `orders`:
`inventory_reservations`、`checkout_attempts`、`store_credit_entries`。
實測確認修正前可以寫入指向不存在訂單的孤兒保留(`ROLLBACK` 前 INSERT 成功)。

三者都在 `orders` 之前建立(保留與結帳嘗試在訂單存在之前就寫入),所以外鍵是在
`orders` 建好之後用 `ALTER TABLE` 補的,並加了 `checkout_attempts_order_id_idx`
——原本的 partial unique index 無法服務外鍵自己的查詢。

squawk 的 `adding-foreign-key-constraint` 與 `constraint-missing-not-valid` 在這個
檔案以 `squawk-ignore-file` 關閉,理由寫在檔頭:兩條規則是為了「對已經有人在寫入的
資料表做 ALTER」而存在,而這個檔案是從零建立整個 schema,沒有任何資料表有資料。
**未來的 migration 是不同檔案,規則照樣生效。**

## 現況

| 項目 | 數量 |
|---|---|
| 資料表 | 52 |
| CHECK 約束 | 130 |
| 外鍵 | 61(**0 個缺完整索引**) |
| 唯一索引 | 43 |
| 規則 trigger | 17 |
| migration 行數 | 1,783 |
| 整合測試子測試 | 378 |

驗證:

- `make verify` PASS(fmt / squawk 0 issues / sqlc / vet / lint 0 findings / race)
- `make test-integration` PASS,378 子測試
- CHECK 刪除實驗:**100 個中 0 個能不被察覺地刪除**(修正前為 48/65)
- 並行守衛 mutation:拿掉鎖或原子性 → **4/4 種紅,3/3 次穩定重現**
- migration down→up 往返 PASS

---

# 第二輪複審(Codex)的處置

第二輪複審在 PostgreSQL 18.4 上重跑並執行反例 SQL,判定「仍不宜上線」,列出多條可直接
寫壞資料的路徑。以下逐條處置,每項都**實測重現 → 修正 → 實測確認已擋**。

## 對機器驗證前提的再指控(全部成立)

| 指控 | 實測 | 處置 |
|---|---|---|
| A. CHECK 覆蓋:刪除實驗只 mutation 了 100/130(30 個多行寫法抓不到);catalog gate 的 `_not_null` 後綴過濾是陷阱;以 `conname` 為鍵在跨表重名時會失效 | 屬實 | 移除名稱過濾(改靠 `contype='c'`);新增 `TestCheckConstraintNamesAreUnique` 守衛「名稱全域唯一」這個前提,未來重名會紅並指示改 table-qualified |
| A. 「378 子測試」實為 378 pass + 4 skip | 屬實 | 數字更正 |
| B. race test 是 300ms 時序猜測,非決定性;`requireExactlyOne` 接受任何 loser error | 屬實 | 改用 `pg_stat_activity` 輪詢確認 T2 真的在等鎖才 commit T1;10 秒內未進 lock-wait 即 fail |
| B. 「mutation 4/4 紅」與測試註解矛盾 | **屬實,這是我兩份文件互相矛盾而未察** | 承認:單獨移除庫存條件式會逃逸(CHECK 是第二道防線),需兩者一起移除才紅。這是 defense-in-depth,不能計入 4/4 killed |
| B. race cleanup 用已取消的 context,無法重跑 | 屬實 | `mustExec` 改用 `context.WithoutCancel` |
| C. FK 索引檢查可被 INCLUDE 欄與複合欄順序騙過;一律排除 partial 造成重複索引 | 屬實 | 加 `indpred IS NULL / indisready`;刪除 3 個重複索引(`carts_user_id_idx`、`checkout_attempts_order_id_idx`、`products_id_self_key`);新增 `TestForeignKeySetIsComplete` 釘死 63 個外鍵名(刪 FK 會紅) |

## 資料面指控(15 條全部實測重現並修正)

| # | 指控 | 處置 |
|---|---|---|
| D | 失敗付款可帶 captured 金額,且可對它退款 | `payments_succeeded_is_captured` 改真正的雙向 all-or-none;`refunds_guard` 額外要求 payment `status='succeeded'` |
| — | 退款可換 payment、succeeded 降級再退、事後改小 capture 繞過上限 | refund/payment 身分與金額 immutable(`payments_settled_is_history`、`refunds_no_regression`、guard 監聽 payment_id) |
| J | 已付款訂單可「新增」明細(trigger 只擋 UPDATE/DELETE) | `order_lines_frozen_once_paid` 涵蓋 INSERT,並鎖 order |
| K | 訂單完整性只在 INSERT 檢查一次,commit 後可掏空 | 付款成功時 `payments_require_complete_order` 鎖 order 重驗明細/地址/總額 |
| L/M/N | 庫存:可直接 UPDATE `stock_quantity`、直接 INSERT 流水帳、保留超量、扣貨忽略 safety_stock | **app role + SECURITY DEFINER**:`goen_app` 撤銷直接寫入,只能經 function;`inventory_reservations` 三個 lifecycle function;扣貨 floor = safety_stock |
| P | 出貨可裝別張訂單明細、超過購買量 | composite FK(同訂單)+ `shipment_within_purchase` 上限 |
| Q | 退貨可跨訂單、rejected 改 approved 繞過上限 | composite FK + `return_requests_legal_transition` + recount |
| S/T | 可直接 INSERT 已出貨訂單、order_number 手寫、shipping_version 可空 | `orders_start_pending`、`order_number DEFAULT next_order_number()`、`shipping_version_id NOT NULL` |
| F | company 發票無統編、mobile 無載具、hero 圖無 alt(NULL 洞) | CHECK 補 `IS NOT NULL AND ...` |
| E | 個資半清(只清姓名);有購物金帳戶的 user 刪不掉 | `order_private_data` 改真正全清/全留;`store_credit_accounts` 改獨立 id + user_id `ON DELETE SET NULL`,ledger 指 account_id |
| — | 發票折讓可 self-ref、跨訂單、超原額;無明細 | `invoice_allowance_valid`(同訂單、≤原額、鎖原發票)+ `invoice_document_lines` |
| — | 購物金 reversal 可重複、跨帳戶、錯金額 | `UNIQUE(reverses_id)` + guard 驗同帳戶且恰為負額 |
| — | sale campaign 加入後可清光折扣 | `sale_campaign_variant_still_valid` |
| — | RESTRICT ≠ append-only,財務歷史仍可逐項 DELETE | `goen_app` 撤銷 payments/refunds/訂單子表的 DELETE/TRUNCATE |
| — | email `btrim` 可被首尾 tab 繞過 | 改 `!~ '^[[:space:]]|[[:space:]]$'` |

## down migration

複審指出殘留 `pg_trgm` 且無自動 gate。down 改用 DO block 迭代刪除所有 table(除
`schema_migrations`)與所有非 extension-owned function —— 不會漏 function,也不會砍掉
migrate 自己的版本表。實測 down→up 往返乾淨。

## 商務領域參考(Vendure / Saleor / Medusa / Stripe)驗證的決定

擁有者提供的四個參考,驗證了本輪的核心設計:variant 才是 SKU(非 product)、庫存是
movement 而非單一 quantity、checkout reservation 有到期時間、payment 是獨立 aggregate
且 intended/captured 分離、退款是 payment 下的子帳而非獨立金額。這些都已在 schema 裡。

### 對照四套領域模型後找到的缺口(依「現在便宜/之後昂貴」分級)

判準沿用本專案一貫的原則:一個缺口若「現在補很便宜、之後補很貴(要改資料或補歷史)」
就現在補;若「加了才知道怎麼設計」就等功能長出來。

**上線收真錢前(before real money)**
- **`orders.discount_code` 是無來源的自由文字。** 與複審抓到的 shipping 同一類缺陷:
  `shipping_method_code/name` 曾是自由文字,直到 `shipping_method_versions` 給它一個凍結
  的真相來源。`discount_code text` 目前沒有背後的 promotion 表,也沒有任何約束保證
  `discount_cents` 與該碼的條款相符——折扣可被捏造或對不上。**現在沒有 code path 會寫它
  (checkout 是批次 ④),所以今天不是資料損毀,是批次 ④ 的設計前提**:任何 coupon 功能
  上線前,`discount_code` 必須指向一張有凍結條款的 promotion 版本表,重演 shipping 的作法。
- **爭議款(Stripe Dispute / 銀行 chargeback)沒有一級模型。** 原始 webhook 事件已由
  `payment_webhook_events` 完整留存(金流真相不會遺失),所以不是「上線前擋 PR」等級;
  但 dispute 有自己的生命週期(needs_response → under_review → won/lost)與資金影響
  (款項被抽回),`refunds` 表涵蓋不了。列為 fast-follow 的爭議帳,今天先確認 webhook
  留存足以事後補建即可。

**快速跟進(fast-follow,與 checkout ④ 一起)**
- **Stripe 手續費與撥款對帳(Payout / Balance Transaction)。** 目前只記毛額 payment,不記
  Stripe 抽的 fee,也不記款項分批撥入銀行的 payout。少了它無法對「實際入帳多少」。可先
  從 webhook 事件投影出來,不必即刻建表。
- **每筆出貨的物流狀態機。** `order_shipments` 只有 `shipped_at`/`delivered_at` 兩個時間戳,
  用時間推斷狀態;四套參考都給 shipment 一個顯式生命週期(label_printed → in_transit →
  delivered → failed → returned_to_sender)。宅配退件/失敗在台灣很常見,值得一個顯式 status。
- **貨到付款手續費(COD surcharge)。** Vendure 用 Surcharge 建模任意加費;台灣 3C 的貨到
  付款手續費目前無處可放(orders 只有 discount/shipping/tax 三欄)。checkout 若支援貨到
  付款就需要。

**功能長出來再加(when feature arrives)**
- 禮物卡作為可購買商品(Saleor GiftCard)——與現有「內部購物金」(`store_credit_*`,已是
  append-only ledger + 每帳戶餘額)不同:禮物卡有可轉讓的碼、到期、可被買。現在不需要。
- 價目表 / 客群定價 / B2B(Medusa PriceList、Saleor ChannelListing)——目前 variant 單一
  `price_cents` + 時段折扣 `sale_campaigns` 已夠。
- 儲存的付款方式 / 訂閱(Stripe SetupIntent / Mandate)——goen 是一次性購買。
- 多倉庫——`inventory_movements` 已以 variant 為鍵,將來加 `location_id` 即可,不必現在拆。

**刻意不做(skip,已在檔頭記錄理由)**
- 多幣別 / Region / Channel:TWD、台灣單一市場。
- 多稅率稅務引擎:單一 5% 內含營業稅,統一發票已由 `invoice_documents` 建模。
- Medusa 式 module 聯邦拆分:與本專案「package by feature、無 service/repository 分層」相斥。

四套系統反過來確認了 goen 沒有走錯的地方:沒有把 stock 當單一數字、沒有把 refund 當
獨立金額、沒有讓 payment 只有一個 amount 欄。缺口集中在「還沒開始做的 checkout/金流
批次」,而非已完成的目錄/訂單骨架。

## 現況(修正後,實測)

53 表 / 136 CHECK / 63 外鍵 / 48 unique index / 26 rule trigger / 4 SECURITY DEFINER
function。`make verify-all` 全綠(fmt-check → sqlc-check → vet → lint → test-race →
integration → govulncheck「No vulnerabilities found」),squawk 0 issues。整合測試
406 個 subtest 通過、6 個合理 skip。應用於 `SET ROLE goen_app` 下端到端運作正常
(關於/聯絡我們寫入、/healthz、/readyz)。

### 這輪修正的實證(不是宣稱)

- **CHECK 全數上鎖**:逐一刪除每一條 CHECK 再跑套件,105 條可被腳本機械隔離者
  全被 `TestCheckConstraintsReject` 抓到(0 條無聲刪除);另 2 條多行 CHECK
  (`payments_succeeded_is_captured`、`order_private_data_all_or_erased`)手動刪除亦
  紅。其餘則由 catalog-driven 完整性 gate + reject/accept 案覆蓋——新增一條沒有案
  的 CHECK 會讓 build 紅。
- **並行守衛全數 load-bearing**:5 條 mutation 各重複 3 次,全部 3/3 紅——
  - 移除庫存 safety_stock 下限(保留 `>=0` CHECK)→ `inventory_never_negative` 紅
  - 同時移除下限與 `>=0` CHECK → `TestStockCannotOversell` 紅
  - refund 讀 capture 不加 `FOR UPDATE` → `TestRefundsCannotRacePastCapture` 紅
  - store credit 結算不鎖帳戶 → `TestStoreCreditCannotRacePastBalance` 紅
  - 訂單編號改 `MAX()+1` 取代原子 counter → `TestOrderNumbersAreUniqueUnderConcurrency` 紅
  - 一個誠實的觀察:單獨移除庫存下限、只在「衝到零」的 race 場景下,是 `>=0`
    CHECK 在擋(縱深防禦),`TestStockCannotOversell` 不會紅;下限的獨有職責
    (sale/hold 不得低於 safety_stock)由 per-row 的 `inventory_never_negative` 鎖住,
    上面已證。
