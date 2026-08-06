# 第四輪外部複審(Codex,冷讀)的處置

冷讀複審獨立重現了 04 的 F2/F3/F11/F13(兩次審查收斂),並抓到 04 沒有的新缺口,
其中包含一條我真正做錯的:**F1 的 search_path pin 無效**。擁有者裁示「連設計級也一起
做」,以下逐條處置。`make verify-all` 全綠;schema 現況 **142 CHECK / 63 外鍵 /
48 unique / 29 rule trigger / 6 SECURITY DEFINER function**,整合 452 個 subtest。

## Blocker(親手重跑確認 → 修 → 再驗)

| # | 缺口 | 修法 | 重驗 |
|---|---|---|---|
| P1-c | `search_path = pg_catalog, public` **擋不住 pg_temp**(它對 relation 隱式排第一);我的 F1 pin 形同虛設,只剩 TEMP revoke 在撐 | 改成 `pg_catalog, public, pg_temp`(pg_temp 顯式最後);測試從「有設定」升級為 `EndsSearchPathWithPgTemp`(要求 pg_temp 在最後)+ 行為測試 `SearchPathPinDefeatsTempShadowing`(授 TEMP、種 decoy、斷言仍擋) | 授 TEMP 後 cycle 仍被擋;mutation 回退到 `pg_catalog, public` → 兩測試皆紅 |
| P0-a | `GRANT ON ALL TABLES` 涵蓋 golang-migrate 的 `schema_migrations`,goen_app 可竄改版本/dirty(homemade migrator 不建此表,所以測試從沒抓到) | DO block 條件式 `REVOKE ALL ON schema_migrations`(表存在才執行) | 模擬 migrate 路徑:INSERT/UPDATE=false |
| P0-b | goen_app 仍有 `DELETE ON users`,可繞過 erase_user 留 PII | `REVOKE DELETE ON users`;新增 `TestGoenAppCannotDeleteUsers` | DELETE=false、UPDATE=true |

## 便宜硬化(全修 + conformance 測試)

- **store_credit_accounts.user_id 重指** → `REVOKE UPDATE ON store_credit_accounts`(整戶餘額換主人)。
- **goen_readonly 讀憑證/個資** → `REVOKE SELECT` on sessions / password_reset_tokens / staff_totp_credentials / user_identities / order_private_data / payment_webhook_events。
- **庫存 reason 與 delta 方向未綁** → `inventory_movements_delta_direction` CHECK(receipt/release/return 正、hold/sale 負、adjustment 雙向)+ 案。
- **orders_check_complete 三失敗共用一名** → 拆成 orders_have_lines / orders_have_delivery / orders_total_non_negative。
- **return_requests_recount 的 rejected 死分支** → 移除。
- **註解承諾不存在的 posting function** → 改為誠實敘述 + 把「store_credit posting / audit writer / create_variant」記名排到 admin/account 批次。

## 設計級(全修 + 行為測試 + mutation)

- **reservation 付款後死角** → `release_reservation` 先鎖 reservation 再鎖 order,已付款(succeeded payment)拒絕釋放(`inventory_reservation_paid_no_release`);unique index 改 partial `WHERE state='held'` + 補 order_id FK 索引,讓釋放後可重 hold;`hold_inventory` 改收呼叫端 idempotency key(重試冪等 vs 重新 hold 可區分)。測試 `TestReleaseReservationRefusesPaidOrder`(mutation → 紅)。
- **refund 跨訂單** → `refunds_guard` 取 payment 的 order_id,return_request_id 若設定須屬同一 order(`refunds_same_order`)。測試 `TestRefundMustMatchOrder`(mutation → 紅)。
- **refund 結算後可改 provider_ref/return_request_id** → 併入 `refunds_settled_is_history` 凍結集合。
- **已 erased/空白的訂單仍可付款** → `payments_require_complete_order` 改要求 order_private_data `erased_at IS NULL` 且七欄非空白。
- **配送 version/code 可矛盾、付款後可改** → `orders_shipping_snapshot_matches`(code 必須對上 version 的 method;name 是顯示快照,刻意不綁)+ orders_freeze_money 併入凍結 shipping_version_id/code/name。rule case 已加。
- **發票 line 負單價/無上界** → `invoice_document_lines` 補 unit_price ∈ [0,1e10]、amount ≤ 1e10 + 案。

## 記名排程(綁未實作功能,與功能共同設計)

- **發票 header=SUM(lines) + ≥1 line 對帳** — 需要 draft→issued 開立流程與 fixture 重設計;與發票批次一起。
- **erase_user 擦 audit_events/order_events payload 個資** — 這兩張表的 writer 尚未實作(見上「posting function」),payload 結構未定、也還沒有資料可擦;與 audit/order-event writer 一起設計(需要 forbid_change 再開一個「payload 匿名化」例外)。
- **return_requests 零明細核准** — 低嚴重度(空核准不動任何庫存/退款),且與 fixture 的 return_within_purchase 測試相衝;與退貨流程一起。
- **order_number 可被指定值覆蓋** — DEFAULT 可被明值蓋過;強制經 trigger 會動到大量既有測試的顯式編號,與 checkout 一起。
- **store_credit / audit / create_variant posting functions** — admin/account 批次(⑦/⑥)。

## 標準符合度

- **COMMENT ON COLUMN 全零** — 你已同意延後那批,維持延後。
- **CLAUDE.md departures** — 補記第 3(updated_at trigger)、第 4(uuidv7)個 departure。
- **GRANT ON ALL TABLES 只涵蓋當下的表** — migration 002+ 每張新表要記得補 GRANT/REVOKE(無 ALTER DEFAULT PRIVILEGES);記為 checklist。
- **(parent, position) unique 不可 deferred**、**counter 單列熱點 + 每日上限**、**down migration 刪 unrelated 物件(dev-only)** — 記錄為已知 trade-off。

## 駁回 / 已不成立

- 「refunds INSERT grant 是空頭支票」——04 已 `REVOKE INSERT ON refunds`,goen_app 根本插不了,現狀非 bug。
- 4 個 SECURITY DEFINER 庫存函式的 search_path——reviewer 自己確認 `public, pg_temp`(public 在前)安全;本輪一併正規化為 `pg_catalog, public, pg_temp`。

## 驗證

`make verify-all` 全綠(含 govulncheck「No vulnerabilities」)。所有 blocker 與設計級
修正親手重跑原始 exploit 確認被擋;新增測試各以 mutation 證明會紅;並行守衛 racemut
5/5、權限鎖 privmut 5/5;應用於 superuser dev 連線警告後正常啟動,/healthz、/readyz 200。

---

## 第五輪冷讀複審(另一個 session 的 blindspot 重現)的處置

該輪冷讀 schema、對乾淨 PG18 + 實際 migration 親手重現 6 條四輪都沒提過的缺口
(repro 在 `scratchpad/blindspot-repro.sql`)。我用它自己的 repro 對修正後的 migration
重跑逐條確認。`make verify-all` 全綠;現況 **142 CHECK / 63 外鍵 / 49 unique / 29 rule
trigger**。

### 現在修(schema 級,親手重跑 repro 確認 + mutation 證明測試會紅)

| # | 缺口 | 修法 | 重跑 repro 結果 |
|---|---|---|---|
| R1 | `payments_require_complete_order` 從不比對 capture 與訂單總額——NT$1 capture 讓 NT$33,980 訂單變已付,溢收同樣不擋 | 同一 trigger 內加 `payments_capture_matches_order`:capture = subtotal − discount + shipping + tax − 該訂單購物金扣抵(目前無購物金流程 → 即 capture = 總額);註明 batch ④ 的購物金/貨到付款要改這行 | `ERROR: order ... is owed 3398000 ... but the capture is 100`。測試 `TestCaptureMustMatchOrderTotal`(underpay/overpay/exact)mutation → 紅 |
| R3 | 同一訂單可開兩張 issued 發票 | partial unique `(order_id) WHERE kind='invoice' AND status<>'voided'`(允許折讓、作廢後可重開)+ uniqueCase | `ERROR: duplicate key ... invoice_documents_one_active_invoice_per_order`。mutation → 紅 |
| R4 | `erase_user` 漏清 `orders.customer_note`(顧客自填、常含 PII) | erase_user 加一句 `UPDATE orders SET customer_note = NULL`(staff_note 內部留;不觸 orders_freeze_money) | `NOTE AFTER: [<NULL>]` |
| R5 | goen_app 可整列刪改 `payment_webhook_events`(去重帳 + 原始憑證)→ 重送事件被當新事件再處理 | `REVOKE UPDATE, DELETE` + `GRANT UPDATE (processed_at)`(app 只需蓋處理時間)| `ERROR: permission denied for table payment_webhook_events`。測試 `TestGoenAppCannotEraseWebhookLedger` mutation → 紅 |

### 政策決定(擁有者已拍板)+ R2 即刻實作

**擁有者決策:goen 不出貨未收足的訂單。** 純線上、Stripe-only,貨到付款不在任何 batch
計畫;若未來要支援,那是新的資金來源,屆時另做決定。依此:

- **R2 → fixed in this PR。** `orders_check_transition` 的離開-pending 分支加
  `orders_funded_to_leave_pending`:pending→picking 要求訂單 **funded** =(應收 = 總額 −
  購物金折抵 = 0)或(已有 succeeded payment,而 payments_capture_matches_order 保證它
  captured 恰為應收)。pending→cancelled 不受限。這也順帶給出「什麼是已付訂單」的正確定義
  ——0 應收訂單天生 funded、無需 payment row,補上「存在 succeeded payment」表達不了的那一半。
  legal 檢查排在 funded 之前,所以 pending→shipped 仍以 orders_legal_transition 拒絕。
  測試 `TestOrderCannotLeavePendingUnfunded`(unpaid→picking 拒、paid→picking 接受、
  unpaid→cancelled 接受),mutation → 紅;reviewer 的 REPRO 2 重跑 →
  `order ... cannot leave pending unfunded (owes 3398000)`。測試 fixture 中所有走到
  picking 的案例已改掛付款訂單(有界翻修,如擁有者所預期)。未來 COD/購物金加在同一行。
- **R6 維持記名綁 batch ④。** webhook 晚到的重 hold/退款沒有 schema 能單方表達的形式
  (DB 擋不住 Stripe 已收的錢),需 app 在 webhook 落地時重新 hold、不到就自動退款。
- **保存政策三條(綁 batch ⑥/⑦)**:guest 訂單無抹除路徑(需依 email 抹除流程)、已通知的 stock_notifications 永久留 email、contact_messages 無保存期限。

### 資訊級(reviewer 自己降級後仍記錄)

- **TWD 整除 100**:查證 Stripe 文件後——收款可帶兩位小數,只有 payout 要求整除;但台灣統一發票金額為整數 NT$、payout 也會卡,故「價格 % 100 = 0」值得當政策考慮,非缺陷。
- **結算後時間軸可改**:orders_freeze_money 凍結金額與運送快照,但 placed_at/cancelled_at/completed_at/user_id 在已付訂單仍可改;user_id 可變可能是 guest 認領所需,時間戳為低嚴重度歷史可竄改,列資訊級。

### 覆審者的總評

「gate 通過、四輪修正成立,但設計面因上述未命名的洞,只能給『機械面通過』」——本輪把其中 schema
級的 4 條(R1/R3/R4/R5)修掉,設計級的 2 叢(資金模型、保存政策)記名待擁有者決策。

---

## 第六輪冷讀:funded/capture 修正暴露的下一個洞(H1/H1b/H2)的處置

正如 review-process 那句「a fix that closes one hole routinely opens the next」。R2
的 funded 守衛與 R1 的 capture-match 共用的購物金記帳算式,在**沖銷(reversal)**面前
是錯的,且沖銷路徑與訂單路徑沒有共同的鎖。三條都在乾淨容器上重現,現已全部修正並
以 mutation 證紅。`make verify-all` 全綠、racemut 5/5。

| # | 缺口 | 修法 | 驗證 |
|---|---|---|---|
| H1 | `credit_applied` 只算 `amount_cents < 0` 的支出列,沖銷的 +額不在視野——花購物金再沖銷(餘額全回),訂單照樣離開 pending | 兩個守衛(orders_check_transition、payments_require_complete_order)的算式改成**淨支出**:用 `reverses_id` LEFT JOIN 把每筆支出與其沖銷配對相加,被沖銷的支出貢獻歸零。兩處用**同一式子**,故 funded 與 capture-match 不會分歧 | reviewer 的 repro:C1–C4 全對、H1 `cannot leave pending unfunded (owes 3398000)`;`TestReversedCreditDoesNotFundOrder` mutation→紅 |
| H1b | 同一算式,capture-match 同一個洞:折抵後沖銷,收 net 少的 capture 被接受 | 同上(共用式子) | H1b `owed 3398000 ... but the capture is 2398000`;測試 mutation→紅 |
| H2 | 沖銷只鎖 `store_credit_accounts`、從不碰 orders;T1 pending→picking 握 order lock、T2 沖銷 0 秒 commit,合起來「已 picking + 餘額已退 + 0 付款」(可預期錯誤 #9) | `store_credit_guard`:凡動到某訂單的 posting(支出、或沖銷一筆帶 order_id 的支出),先 `FOR UPDATE` 鎖那張訂單,並加狀態規則——支出只在 pending 且未付;沖銷只在「pending 且未付」或「cancelled」(出貨後補償走 refund 或新正向 entry,不是沖銷)。有了訂單鎖,兩交易不論誰先 commit,後者都看得到前者 | `TestCreditReversalCannotRacePastFulfilment`(raceOutcome + requireExactlyOne)、`TestCreditPostingRespectsOrderState`;各 mutation→紅(去鎖→race 紅、去狀態規則→紅) |

沒有新增 schema 物件(新規則 `store_credit_posting_matches_order` 由既有的
`store_credit_never_negative` trigger 拋出;淨支出算式是 inline)。三個 repro
(`scratchpad/round6-accept.sql` + H2 兩段並行)可作回歸驗證。待擁有者再驗一輪。
