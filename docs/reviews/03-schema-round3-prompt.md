# 給 Codex 的第三輪 schema 複審 prompt

> 貼給 Codex 的內容從下一行開始。

---

你是一位嚴格的資料庫與後端審查者。這是同一份 PostgreSQL schema 的第三輪複審。
前兩輪你已經抓出並被修正:機器驗證造假(48/65 CHECK 可無聲刪除)、15 條資料損毀
路徑、非決定性 race 測試、down migration 殘留。本輪要驗證的是「修正是否真的成立,
以及還有沒有新的洞」。請不要複述前兩輪已處置的項目,除非你能證明修正是假的。

## 你會拿到的東西

- `migrations/001_initial_schema.up.sql`(53 表 / 136 CHECK / 63 外鍵 / 48 unique
  index / 26 rule trigger / 4 SECURITY DEFINER function)與 `.down.sql`
- `internal/db/*_integration_test.go`(catalog-driven 完整性 gate + 每條 CHECK/
  unique/trigger 的 reject+accept 案 + 並行測試)
- `docs/reviews/01-schema-response.md`(前兩輪逐條處置 + 本輪實證)
- `cmd/goen/main.go`(連線後 `SET ROLE goen_app` 的權限模型)

## 本輪的權限模型(修正的核心,請重點攻擊)

應用以 `goen_app`(NOLOGIN)身分連線,對 `stock_quantity`、`inventory_movements`、
`inventory_reservations`、`payments`、`refunds`、`store_credit_entries`、
`audit_events` 及訂單子表的直接 INSERT/UPDATE/DELETE 已被 REVOKE;這些寫入只能經
4 個 SECURITY DEFINER function(`record_inventory_movement`、`hold_inventory`、
`consume_reservation`、`release_reservation`、`next_order_number`)。請具體檢查:

1. **REVOKE 是否有漏網的寫入面。** 有沒有哪張「財務/庫存/帳本」表,`goen_app` 仍能
   繞過 function 直接寫?請以 `pg_class.relacl` 的角度逐表核對,不要只看 REVOKE 清單。
2. **SECURITY DEFINER function 自身是否可被濫用。** 參數有沒有可以讓呼叫者做出
   本不該做的事(例如以 `correction` 理由繞過 safety_stock 下限、或 hold 一個別人的
   variant)?`search_path` 有沒有被釘死(function 是否 `SET search_path`,避免
   definer 權限下的 search_path 注入)?
3. **`session_replication_role = replica` 的暴露面。** 測試用它隔離 CHECK;`goen_app`
   在正式連線能不能自己 `SET session_replication_role` 去關掉所有 trigger 守衛?

## 要重點驗證的修正

- **並行守衛**:超賣、訂單重號、退款超領、購物金透支——每條都有一個「鎖 aggregate
  root 再讀」的 trigger 或 conditional UPDATE。處置紀錄宣稱每條都用 mutation(移除鎖/
  條件/counter)驗過紅。請挑戰:(a) 這些 race 測試是否真的能觀察到 race(用
  `pg_stat_activity` 等鎖,而非 sleep);(b) 有沒有哪條「看起來鎖了但鎖錯行」——例如
  鎖了 order 卻讀 payment、或鎖的粒度不涵蓋要保護的不變量。
- **金額不變量**:`payments` 的 intended/captured 分離、`refunds` 累計 ≤ capture、
  發票折讓 ≤ 原額且同訂單。請找可以讓「已結算金額事後被改」或「退款/折讓總額突破上限」
  的並行或狀態轉移路徑。
- **append-only 帳本**:inventory_movements / store_credit_entries / audit_events /
  invoice_document_lines 宣稱只進不改不刪。請找 UPDATE/DELETE 仍可達的路徑(含
  `session_replication_role`、SECURITY DEFINER function 內部、ON DELETE CASCADE 連坐)。
- **個資刪除**:`order_private_data` 宣稱「全清或全留」、`store_credit_accounts` 改
  獨立 id + `user_id ON DELETE SET NULL`。請找刪帳後仍殘留 PII、或有帳戶就刪不掉的洞。

## 新一輪特別想聽的(前兩輪未涵蓋)

1. **狀態機的完整性**:`orders.fulfillment_status`、`payments.status`、`refunds.status`、
   `return_requests.status`、`invoice_documents.status` 的合法轉移是否有「死角狀態」
   (進得去出不來)或「可跳關」路徑?trigger 表達的轉移圖與 CHECK 允許的值域是否一致?
2. **時間與冪等**:`order_number_counters` 以 `business_date` 分桶——跨時區/跨日切換時
   會不會重號或跳號?`business_date` 取的是哪個時區?webhook 冪等
   (`payment_webhook_events`)對「同事件重送」與「亂序到達」是否都安全?
3. **邊界算術**:`unit_price_cents * quantity`、退款累計、購物金累計是否有 bigint
   overflow 或負值突破的可能?
4. **參考模型缺口(僅供你判斷嚴重度,不必照單全收)**:處置紀錄列了 discount_code
   無來源、爭議款無一級模型、Stripe fee/payout 未對帳、每筆出貨無狀態機。你認為其中
   哪些應該在「收真錢前」就補、哪些可以 fast-follow?有沒有我們沒列到的?

## 輸出格式

逐條給:嚴重度(阻擋上線 / 上線前修 / 可跟進 / 資訊)、可重現的具體路徑(SQL 或
並行時序)、以及你認為的最小修法。若某條你認為前輪修正其實沒成立,請給出讓它失敗的
具體 statement 或時序,而不是「感覺不對」。
