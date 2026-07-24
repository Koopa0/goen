# 第三輪 schema 複審 prompt(中性語氣版)

> 這一版把語氣改成純資料庫正確性審查,避免任何容易被模型誤判為資安請求的字眼,
> 讓 Fable 等模型能正常審查。技術內容與深度與原版相同。
> 貼給審查模型的內容從下一行開始。

---

你是一位資深、嚴謹的資料庫與後端審查者。這是同一份 PostgreSQL schema 的第三輪複審,
目的是提升資料完整性與正確性。前兩輪已經指出並修正:機器驗證高估了覆蓋率(65 條
CHECK 中有 48 條可以被無聲移除,測試仍全綠)、15 條會導致資料不一致的路徑、
非決定性的並行測試、以及 down migration 殘留物件。本輪要確認的是「這些修正是否確實
成立,以及是否還有尚未涵蓋的問題」。若某一項前兩輪已處置,請不要重複列出,除非你能
用具體重現說明該修正其實沒有生效。

## 你會拿到的東西

- `migrations/001_initial_schema.up.sql`(53 表 / 136 CHECK / 63 外鍵 / 48 unique
  index / 26 rule trigger / 4 SECURITY DEFINER function)與 `.down.sql`
- `internal/db/*_integration_test.go`(從系統目錄推導的完整性 gate + 每條 CHECK/
  unique/trigger 的 reject+accept 案 + 並行測試)
- `docs/reviews/01-schema-response.md`(前兩輪逐條處置 + 本輪實證)
- `cmd/goen/main.go`(連線後 `SET ROLE goen_app` 的權限模型)

## 本輪的權限模型(修正的核心,請重點檢視)

應用以 `goen_app`(NOLOGIN)身分連線。對 `stock_quantity`、`inventory_movements`、
`inventory_reservations`、`payments`、`refunds`、`store_credit_entries`、
`audit_events` 及訂單子表的直接 INSERT/UPDATE/DELETE 權限已從 `goen_app` 收回
(REVOKE);這些寫入只保留給 4 個 SECURITY DEFINER function(`record_inventory_movement`、
`hold_inventory`、`consume_reservation`、`release_reservation`、`next_order_number`)。
請具體確認以下三點:

1. **REVOKE 是否有未涵蓋的寫入權限。** 是否還有任何「財務/庫存/帳本」表,`goen_app`
   在不經過上述 function 的情況下仍能直接寫入?請以 `pg_class.relacl` 的角度逐表核對
   實際授權,而不是只讀 REVOKE 清單;也請確認 migration 尾端後加的表是否都補到位、
   `goen_readonly` 是否確實只讀。
2. **SECURITY DEFINER function 的介面是否可能被非預期地使用。** 參數是否可能讓呼叫者
   做出設計上不該發生的事(例如以 `correction`/`receipt` 之類理由略過 safety_stock 下限
   而任意調整庫存、對不屬於自己的 variant 或不存在的保留動作、重複扣抵)?函式是否以
   `SET search_path` 固定搜尋路徑(未固定時,definer 權限下可能解析到非預期的物件)?
3. **以工作階段設定停用守衛的可能性。** 測試會用 `session_replication_role = replica`
   隔離 CHECK;請確認 `goen_app` 在正式連線中能否自行 `SET session_replication_role`
   把所有 trigger 守衛停用,或 `SET ROLE`/`RESET ROLE` 取回較高權限。因為多數跨列與
   append-only 規則都是 trigger,若這類設定可用,會使整組守衛失效,屬嚴重問題。

## 要重點確認的修正

- **並行守衛**:超賣、訂單重號、退款超額、購物金透支——每條都以「先鎖 aggregate root
  再讀」的 trigger 或條件式 UPDATE 實作。處置紀錄宣稱每條都以 mutation(移除鎖/條件/
  counter)驗證會轉紅。請就兩點提出質疑:(a) 這些並行測試是否真的能觀察到競態(用
  `pg_stat_activity` 等待鎖,而非 sleep);(b) 是否有哪一條「鎖定的對象不正確」——
  例如鎖了 order 卻讀 payment,或鎖定的粒度未涵蓋要保護的不變量。
- **金額不變量**:`payments` 的 intended/captured 分離、`refunds` 累計 ≤ capture、
  發票折讓 ≤ 原額且限同一訂單。請找出是否存在能讓「已結算金額事後被更動」或「退款/
  折讓總額超過上限」的並行或狀態轉移情形。
- **append-only 帳本**:inventory_movements / store_credit_entries / audit_events /
  invoice_document_lines 宣稱只新增、不修改、不刪除。請確認是否仍有可達的 UPDATE/DELETE
  路徑(含 `session_replication_role`、SECURITY DEFINER function 內部、或 ON DELETE
  CASCADE 連帶刪除)。
- **個資刪除**:`order_private_data` 宣稱「全部清除或全部保留」、`store_credit_accounts`
  已改為獨立 id 加 `user_id ON DELETE SET NULL`。請確認是否有:刪除帳號後仍殘留個資、
  或因某個子表 RESTRICT 而導致有帳戶的使用者無法被刪除的情形。

## 新一輪特別想聽的(前兩輪未涵蓋)

1. **狀態機的完整性**:`orders.fulfillment_status`、`payments.status`、`refunds.status`、
   `return_requests.status`、`invoice_documents.status` 的合法轉移是否有「死角狀態」
   (進得去、卻沒有預期中的出口)或「可略過必要步驟」的路徑?trigger 表達的轉移圖與
   CHECK 允許的值域是否一致?
2. **時間與冪等**:`order_number_counters` 以 `business_date` 分桶——跨時區或跨日切換時
   是否會重號或跳號?`business_date` 是以哪個時區計算的?webhook 冪等
   (`payment_webhook_events`)對「同一事件重送」與「較舊事件亂序到達」是否都安全?
3. **邊界算術**:`unit_price_cents * quantity`、退款累計、購物金累計是否有 bigint
   溢位或負值超出下限的可能?
4. **參考模型缺口(僅供你判斷嚴重度,不必照單全收)**:處置紀錄列了 discount_code
   無來源、爭議款(dispute)無一級模型、Stripe 手續費/撥款未對帳、每筆出貨無狀態機。
   你認為其中哪些應在「收真錢前」補齊、哪些可以後續跟進?有沒有我們沒列到的?

## 輸出格式

逐條給出:嚴重度(阻擋上線 / 上線前修 / 可後續跟進 / 資訊)、可重現的具體路徑(SQL
或並行時序)、以及你認為的最小修法。若你認為某條前輪修正其實沒有生效,請附上能重現
該問題的具體 statement 或時序,而不是僅憑感覺。
