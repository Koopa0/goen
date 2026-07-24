# 第三輪自我複審 — 已驗證的發現

本輪由我自己執行(非交給 Codex),用 workflow 展開 9 個審查視角、對真實 PostgreSQL
逐條「實際執行」重現,而非只讀 schema。原始:29 條經驗證,25 CONFIRMED / 4 REFUTED /
0 INCONCLUSIVE。以下是**去重後的根因清單**(25 條原始發現收斂成 13 條根因)。

安全關鍵的前 5 條(F1–F5)我**親手重跑確認**,不是只信 agent 回報。

## 這一輪推翻了上一輪我對你說的話

上一輪我說「schema 強制執行 app 無法繞過的完整性」「全部修完、已驗證」。本輪證明那句話
在**核心宣稱上是錯的**:`goen_app` 有兩條路徑可以繞過守衛。我先前的 mutation 證明
(racemut 5/5 紅)是對的、但**範圍太窄**——它只證明守衛在「正常寫入路徑」上會擋,
從未測試「權限邊界本身」或「pg_temp 這條路」。根因是 F5:我為 CHECK/unique/trigger
建了 catalog-driven 完整性 gate,卻**從沒為權限(grant)建過任何測試**,所以 F2–F4
一直沒被看見。

---

## 類別性突破(這條不修,整個 trigger 模型等於沒有)

### F1 · pg_temp 表遮蔽可讓 `goen_app` 關掉全部 22 個 trigger 守衛 · BLOCK · 我親手驗

全部 22 個 trigger function 都是 `SECURITY INVOKER` 且 `proconfig IS NULL`(沒有釘死
`search_path`)。PostgreSQL 預設 `pg_temp` 在搜尋路徑最前面。`goen_app` 有 `TEMP` 權限,
於是它建一張同名的 temp 表就能遮蔽 trigger 以「非限定名稱」讀取的真實表。

我親手重跑(單一交易、ROLLBACK,無 commit):
```
-- baseline:守衛正常擋下環
UPDATE categories SET parent_id=B WHERE id=A;
  -> ERROR: category ... would become its own ancestor  (categories_acyclic)
-- 加一張空的 pg_temp.categories 之後
CREATE TEMP TABLE categories (id uuid, parent_id uuid, slug text, name text);
UPDATE public.categories SET parent_id=B WHERE id=A;
  -> UPDATE 1        ← 守衛被跳過
-- 結果:public.categories 出現 A->B 且 B->A 的真實環,由 goen_app 寫入
```
同一機制打穿 `refunds_within_capture`(以非限定名讀 `payments`/`refunds`)、
`store_credit_never_negative`、append-only 的 `forbid_change`、訂單狀態轉移等**每一個**
trigger。→ 這使 CLAUDE.md 檔頭「money/stock/history 沒有第二條寫入路徑」不成立。

**最小修法**:對每一個以非限定名參照 relation 的 plpgsql function
`ALTER FUNCTION ... SET search_path = pg_catalog, public`(不含 pg_temp,或把 pg_temp
放最後),就像那 4 個 SECURITY DEFINER 庫存 function 已經做的。縱深防禦再加
`REVOKE TEMP ON DATABASE goen FROM goen_app, PUBLIC`。

---

## 權限模型缺口(目前「單一寫入者」是假的)

### F2 · INSERT 從未被 REVOKE → `goen_app` 直接寫 money/stock · BLOCK · 我親手驗

第二輪只 REVOKE 了 UPDATE/DELETE,漏了 INSERT,也漏了幾張表。實測:
- `payments`:`goen_app` 直接 INSERT 一筆 `status='succeeded', captured=3398000` → 成功
  (`PAYMENT WRITTEN: succeeded captured=3398000`),訂單瞬間變「已付」、0 筆 webhook。
- `product_variants`:直接 INSERT `stock_quantity=999` → 成功(`STOCK MINTED: 999
  movements=0`),projection 與 ledger 出生就背離。
- `refunds`:INSERT 仍在(保護只靠 `refunds_guard` 的附帶鎖,而該鎖又被 F1 打穿)。
- `order_number_counters`:可直接寫,繞過原子 counter。
- `invoice_document_lines`:UPDATE/DELETE 未 REVOKE(append-only 只剩 trigger 一層,F1 可穿)。

**最小修法**:把 INSERT 併入 REVOKE(`REVOKE INSERT, UPDATE, DELETE ON payments,
refunds, product_variants, order_number_counters FROM goen_app;`
+ `REVOKE UPDATE, DELETE, TRUNCATE ON invoice_document_lines FROM goen_app;`);合法的
initial-row 寫入改走 SECURITY DEFINER function 或 owner 服務,強制出生狀態
(payment 只能生 `requires_payment`、variant 只能生 `stock_quantity=0` 並補一筆 receipt)。

### F3 · SECURITY DEFINER function 保留 PUBLIC EXECUTE → 唯讀角色能寫 · before-launch · 我親手驗

`proacl` 為 `=X/goen`(開頭空字串=PUBLIC 有 EXECUTE)。`goen_readonly` 或任何角色都能
呼叫 `record_inventory_movement` / `hold_inventory` / … 去寫 stock/ledger/reservation。

**最小修法**:建立函式後 `REVOKE EXECUTE ON FUNCTION ... FROM PUBLIC;` 再
`GRANT EXECUTE ... TO goen_app;`。

### F4 · `RESET ROLE` 還原成 superuser · before-launch · 我親手驗

實測 `SET ROLE goen_app` → `is_superuser=off`,`RESET ROLE` → `is_superuser=on`。因為
開發連線的 login role 就是 superuser/owner,一句 `RESET ROLE` 就讓所有 REVOKE 失效。
`.env.example` 有寫「正式環境要用 non-superuser、goen_app 成員的 login role」,但**沒有
任何東西強制或測試它**,而且 dev 直接以 superuser 跑。附帶:那 4 個 SECURITY DEFINER
function 的 owner 也是 superuser,definer 權限=superuser。

**最小修法**:migration 建立並文件化一個 non-login/non-super 的 owner role
(function 改由它擁有)與一個 non-super 的 `goen_web` login(僅為 goen_app 成員);
`openPool` 啟動時斷言 `SET ROLE goen_app` 後既非 superuser 也非 table owner,否則 fail-fast;
整合測試加同一斷言。

### F5 · 權限/REVOKE 模型零測試覆蓋(F2–F4 沒被看見的原因)· before-launch

conformance suite 從不以 `goen_app` 連線,所以權限邊界完全沒被測。

**最小修法**:在 `coverage_integration_test.go` 加 catalog-driven 權限測試——`SET ROLE
goen_app` 後,對 money/stock/ledger 表集合逐一斷言 `has_table_privilege(...)=false`
(期望值由 `pg_class.relacl` / `aclexplode` 推導,非手寫清單);斷言 `session_replication_role`
被拒、`RESET ROLE` 後非 superuser。並依 testing.md「Locks Are Proven by Mutation」證它會紅。

---

## 完整性邏輯(就算角色設定完美,這些仍是缺陷 · agent 實測)

### F6 · 已結算 refund 的金額可被改 → 真實退款可累計超過實扣 · before-launch
`payments` 有 `payments_settled_is_history`,`refunds` 沒有對應 trigger。
修:加 `refunds_settled_is_history`(OLD.status in succeeded/failed/cancelled 時凍結
amount_cents/payment_id/request_key/時間欄)+ 禁刪已成功 refund。

### F7 · `payments`/`store_credit_entries` 金額無上界 → bigint 溢位而非乾淨拒絕 · follow-up
其他 money 欄都有 `<= 1e10`,這兩者沒有,`store_credit_guard` 累加會 22003 溢位而不是
raise `store_credit_never_negative`。修:補對稱 range CHECK。

### F8 · append-only `forbid_change` 擋掉 `actor_user_id` 的 ON DELETE SET NULL → actor user 刪不掉 · before-launch
audit/ledger 的 `actor_user_id ON DELETE SET NULL` 與 append-only 觸發器矛盾:RI 要 null 掉
它,trigger 卻擋所有 UPDATE。修:`forbid_change` 放行「唯一變動是 actor_user_id 由非 NULL
變 NULL」這種 RI 驅動更新,其餘照擋;或改為刻意的匿名化路徑。

### F9 · 刪 user 後 `order_private_data`、`stock_notifications` 仍留完整個資 · before-launch
(修正了上一輪「個資全清或全留」的過度宣稱:CHECK 允許 erased 狀態,但**沒有任何東西
強制**刪 user 時去清。)修:走 SECURITY DEFINER `erase_user(uuid)` 一次交易內把 orders.user_id
null 掉、`order_private_data` 七欄清空並蓋 `erased_at`、清 `stock_notifications` email。

### F10 · `return_requests` 可直接 INSERT 成 approved/completed/rejected,跳過轉移機 · before-launch
修:加 BEFORE INSERT 觸發器(orders_start_pending 模式)拒絕 NEW.status <> 'requested'。

### F11 · 作廢發票可被「反作廢」(voided→issued),已申報稅務歷史可被改寫 · before-launch
修:`invoice_documents_guard` 讓 'voided' 成為終態(OLD.voided 且 NEW<>voided 時 raise)。

---

## 政策/表面(INFO · agent 實測)

### F12 · orders 出貨狀態:shipped 無法取消(宅配退件死路)、shipped→completed 跳過 delivered
是政策決定被留為隱含。修:在 `orders_check_transition` 明確編碼所選政策
(shipped→cancelled 是否允許;completed 是否必經 delivered)。

### F13 · `payments_no_regression` 引用 'failed',但 `payments_status_known` CHECK 永不容許該值
死碼。修:從該 trigger 的終態集合移除 'failed'(refunds 那條合法保留,因 refunds CHECK 有 failed)。

---

## 這輪確認「沒問題」的(4 REFUTED + 仍成立的)

- 半清個資路徑不可達(CHECK `order_private_data_all_or_erased` 擋下)。
- webhook 去重(PK provider+event_id)、payments 凍結/no-regression 健全;'failed' 分支是死碼(見 F13)。
- 那 4 個 **SECURITY DEFINER** function **有**釘死 `search_path=public, pg_temp`(public 在前,temp 蓋不掉)——F1 只打得到沒釘的 22 個 trigger function。
- `next_order_number` 用 `Asia/Taipei` 顯式計算 business_date,跨時區穩健、冪等。
- `session_replication_role` 對 `goen_app` 是**拒絕**的(superuser-only)——但 F1 的 pg_temp 達成同樣的「關掉 trigger」效果,所以結論不變。
- 並行守衛在「正常寫入路徑」上仍會擋(racemut 5/5 紅);它們的問題是**可被 F1/F2 從側面繞過**,不是不會擋。

---

## 處置(全部修完並實測 + mutation 驗證)

擁有者選擇「F1–F11 全修」。以下全數修正,`make verify-all` 全綠
(fmt-check → sqlc-check → vet → lint → test-race → integration → govulncheck
「No vulnerabilities」)。schema 現況:**139 CHECK / 63 外鍵 / 48 unique index /
28 rule trigger / 6 SECURITY DEFINER function**。每一條的封堵都親手重跑原始
exploit 確認被擋,權限類再加 mutation 證明測試會紅。

| # | 修法 | 重跑 exploit 結果 |
|---|---|---|
| F1 | 全 22 個 trigger function 釘 `search_path = pg_catalog, public`(DO block 迭代非 extension 的 plpgsql function);`REVOKE TEMPORARY ... FROM PUBLIC` | pg_temp 遮蔽 categories 環、forge pg_temp.payments 打 refunds_guard → 皆 `permission denied to create temporary tables` |
| F2 | `REVOKE INSERT` 併入 payments/refunds/product_variants;`REVOKE INSERT,UPDATE,DELETE ON order_number_counters`;`REVOKE UPDATE,DELETE,TRUNCATE ON invoice_document_lines`;append-only 的 order_events/shipping_method_versions 補 `REVOKE UPDATE` | goen_app INSERT 已結算 payment / stock=999 variant → 皆 `permission denied for table` |
| F3 | DO block 對每個 goen function `REVOKE EXECUTE ... FROM PUBLIC`;5 個可呼叫者 `GRANT EXECUTE TO goen_app` | goen_readonly 呼叫 record_inventory_movement → `permission denied for function` |
| F4 | migration 建 `goen_web`(LOGIN NOSUPERUSER, IN ROLE goen_app);`next_order_number` 改 SECURITY DEFINER;`main.go` 啟動守衛:`SET ROLE goen_app` 後仍為 superuser 則拒絕啟動,superuser login 則警告 | 應用在 dev(superuser login)警告後正常啟動,/healthz /readyz 200 |
| F5 | `coverage_integration_test.go` 新增權限 conformance:`TestGoenAppHasNoDirectWriteToMoney`、`TestAppendOnlyTablesDenyUpdateDelete`(catalog-driven)、`TestGoenAppCannotDisableTriggers`、`TestGoenAppIsNotSuperuser`、`TestGoenAppHasNoTempPrivilege`、`TestEveryStoredFunctionPinsSearchPath`、`TestNoStoredFunctionIsPublicExecute` | 5 條 mutation(re-grant/停止 pin/停止 revoke)各使對應測試 3/3 紅 |
| F6 | 新增 `refunds_settled_is_history`(BEFORE UPDATE OR DELETE),凍結已結算 refund 的金額與身分、禁刪已成功 refund | 改 succeeded refund 60000→100000 → `refund ... is settled; its amount and identity are history` |
| F7 | `payments` 補 `intended/captured_in_range (<=1e10)`;`store_credit_entries` 補 `amount_in_range (±1e10)` | intended>1e10 → `payments_intended_in_range`;credit>1e10 → `store_credit_entries_amount_in_range`(非 22003 溢位) |
| F8 | `forbid_change` 放行「唯一變動是 actor_user_id 由非 NULL 變 NULL」的 RI 驅動更新(jsonb 比對,對無此欄的表為 no-op) | 刪除一個是 audit actor 的 user → `DELETE 1`,actor_user_id 變 NULL |
| F9 | 新增 SECURITY DEFINER `erase_user(uuid)`:清空該 user 所有訂單的 order_private_data 七欄並蓋 erased_at、刪 stock_notifications、刪 user | erase_user 後 PII email=NULL name=NULL erased=true、notif count=0、user count=0 |
| F10 | 新增 `return_requests_start_requested`(BEFORE INSERT),非 'requested' 出生即拒 | INSERT 出生 'approved' → `a new return request must start requested` |
| F11 | `invoice_documents_guard` 讓 'voided' 成終態 | voided→issued → `a voided document cannot be re-issued` |
| F13 | `payments_check_transition` 移除永不可能的 'failed' | trigger 終態集合與 CHECK 值域一致 |

### 仍開放(非本輪範圍)

- **F12(政策)**:出貨狀態 shipped 無法取消(宅配退件)、shipped→completed 是否必經
  delivered——需要你的政策決定,尚未編碼。列為待決,非缺陷。
- **INFO**:那 6 個 SECURITY DEFINER function 目前 owner 仍是 superuser(dev 也以
  superuser 連線)。F4 的啟動守衛與 `goen_web` 已把「連線角色可被 RESET 回 superuser」
  這條主路堵住;把 function owner 改成一個最小權限的非 superuser role 是進一步的
  縱深防禦,列為後續。

新增的完整性 gate 會抓漏:新的 CHECK/trigger 沒有案、新的 plpgsql function 沒釘
search_path、append-only 表沒 revoke UPDATE/DELETE、money 表被重新 GRANT——任一都會讓
build 紅。
