# 第五輪複審的處置

這一輪的發現在批次 ① 首頁視覺那條線上收到。第一次處置是全部**記名排程**(理由:CSS
批次不該夾帶 schema 修正)。擁有者隨後裁示現在就修,所以除了確實需要功能才談得上的
幾條之外,**全部已修並以 mutation 證明**。

schema 現況(實測 `pg_constraint` / `pg_trigger` / `pg_proc`):**142 CHECK / 63 外鍵 /
44 trigger / 6 SECURITY DEFINER function**。這一輪新增的是 trigger(42→44)與一個非
SECURITY DEFINER 的判準函式,CHECK 數不變。

## 一句話

錯的不是三個守門,是**一個判準有三個出口**。`EXISTS(succeeded payment)` 被當成「這張
訂單已成交」,但零欠款訂單(100% 折扣,或批次 ⑥ 之後的全額購物金)**永遠不可能有**
succeeded payment 列 —— `orders_funded_to_leave_pending` 在
`order_total - credit_applied = 0` 時整條短路、完全不看 payment,而
`payments_succeeded_is_captured` 又禁止零元的 succeeded payment。兩者相乘,那種訂單
的明細、金額與已保留庫存在出貨後仍可被改。

修法不是把條件抄三次(下一個人照樣只改一個),而是收成單一函式
`order_is_committed(uuid)`:**已收到錢,或已離開 pending**。第二半正是覆蓋零欠款的
那一半,而它對所有訂單都成立,因為 `orders_check_transition` 本來就不讓未 funded 的
訂單離開 pending。

001 的註解裡其實早就寫著「a 0-owed order is funded with no payment row, which the
"exists a succeeded payment" definition could not express」—— 作者當時就知道,只是那
個認識沒有傳到另外三個守門。

## 已修(逐條 mutation 證紅)

| # | 缺口 | 修法 | Mutation → 紅 |
|---|---|---|---|
| ① | `order_lines_freeze` / `orders_freeze_money` 用 payment-proxy,已成交零元訂單的金額紀錄可事後竄改 | 兩者改呼叫 `order_is_committed`;constraint 更名為 `*_once_committed`(舊名 `once_paid` 已不誠實) | 把 `order_is_committed` 退回只看 payment → `TestZeroOwedOrderIsCommitted` 三個子測試全紅 |
| ②&nbsp;| `orders.cancelled_at` / `completed_at` 無凍結,稽核時間戳可被改寫 | 新增 `orders_freeze_history` + `orders_history_frozen` trigger,只擋「改寫已設定的值」,首次設定仍允許 | 移除 trigger → rule case 紅 |
| ③ | `sale_campaign_variant_still_valid` 逐列讀舊快照、且只綁 `UPDATE OF` | 改 **AFTER** row(statement 結束才 fire,讀得到真實終態)、加綁 `DELETE`、鎖 product 列擋並發 | 退回 `BEFORE ... UPDATE OF` → `TestCampaignDiscountSurvivesBulkEdits` 兩個子測試紅 |
| ④ | `hold_inventory` 與 `release_reservation` 鎖序相反(ABBA,40P01) | `release_reservation` 改 variant→order,與 `hold_inventory` 一致 | 鎖序改回 order-first → `TestReleaseReservationLocksVariantBeforeOrder` 紅 |
| ⑤ | `home.NewHandler` 少 nil logger 檢查 | 與四個 sibling 一致改為 `store == nil \|\| log == nil` | (已於首頁批次修正並單獨 commit) |
| — | `erase_user` 未清 `invoice_preferences`,`carrier_code`(手機條碼載具)與 `tax_id` 永久留存 | 刪除該列(欄位不能就地 NULL:type CHECK 要求值)。`invoice_documents` 才是稅務紀錄,不動 | 移除該 DELETE → `TestEraseUserLeavesNoPersonalData` 紅 |
| — | `product_reviews.is_verified_purchase` 完全信任 caller,而評分即時由這些列算出 | 新增 `product_reviews_verified_is_real`:宣稱已購驗證時,必須有該作者對同商品的**已成交**訂單。只擋「設為 true 的那一刻」,已為 true 者不再檢查(否則抹除把 user_id 設 NULL 會反過來擋住 `erase_user`) | 移除 trigger → rule case 紅 |

### 順手補掉的兩個盲點

- **`search_path` pin 的批次 ALTER 與其守門測試都只掃 `plpgsql`。** `LANGUAGE sql`
  的函式一樣會被 `pg_temp` 遮蔽,所以那個過濾條件是一個等著被踩的洞。兩邊都改成掃所
  有語言(`prokind IN ('f','p')`)。目前沒有非 plpgsql 函式 —— 正因為沒有,現在改最
  便宜。
- **dev seed 自己在說謊。** 22 筆評論全部 `is_verified_purchase = true`,但 seed 裡
  既沒有 user 也沒有訂單。守門要成真,demo 資料就不能靠它是假的;`gen_seed.py` 與
  `dev_catalog.sql` 都改為 `false`。

## Role 改名(擁有者裁示)

`goen_app` / `goen_web` / `goen_readonly` → **`store` / `store_svc` / `reporting`**。

原本的名字分不出誰是登入身分、誰是權限集合(`app` 和 `web` 是同一個東西的兩種說法)。
新名字各自回答一個問題:`store` = 顧客側請求能做什麼、`store_svc` = 誰連進來、
`reporting` = 報表能讀什麼。`admin` / `admin_svc` 在 migration 註解裡留名給批次 ⑦,
但不建立空 role。

過程中否決了 `_login` 後綴:`LOGIN` 在 PostgreSQL 是「可否開連線」的 role 屬性,與網
站的會員登入無關,而這個 codebase 裡 login 這個字已經被會員登入佔走 —— 擁有者第一眼
就讀成「訪客 vs 會員」,下一個讀 schema 的人也會。

## 仍記名排程(需要功能才談得上)

| 缺口 | 為什麼現在不修 | 排到 |
|---|---|---|
| 退貨↔退款↔已出貨數量三者未對帳(`return_lines_within_purchase` 以訂購數為上限、`refunds_guard` 以 capture 為上限,兩者不相干) | 上限該取「已出貨數」與「退貨 line 金額」,而這要與退貨流程一起設計;現有 fixture 的 `return_within_purchase` 案例也會衝突 | 批次 ⑦ |
| 發票折讓與退款未交叉核對(同一筆錢可「全額退現」又「全額開折讓」) | 是否為 bug 取決於業務意圖,台灣實務上折讓通常伴隨退款 —— 這是待裁示的對帳點,不是可自行決定的 invariant | 發票批次 + 業務裁示 |
| `order_number_counters`:`last_no ≤ 999999` 天花板、同日結帳序列化熱點、`'YYMMDD'` 百年邊界 | 規模/長壽命邊緣,不是缺陷。擁有者裁示現階段還在開發,談不到上限 | 批次 ④ 吞吐量測時 |

`audit_events` / `order_events` payload 的個資盤點仍在 05 的記名清單上(那兩張表的
writer 尚未實作,payload 結構未定)。

## 這一輪學到的

- **同一個錯誤判準會有多個出口。** 找到一個之後,要問的是「這個判準還寫在哪裡」,而
  不是「這個守門怎麼修」。
- **`-run` 打錯字的測試會「通過」。** M3/M6 第一次跑出綠燈,是因為我猜錯測試名、
  `-run` 沒選到任何測試。做 mutation 時必須先確認 `-run` 真的選到了東西(`-v` 數
  `=== RUN`),否則綠燈毫無意義。
- **測試自己會抓到自己的瑕疵。** `TestCampaignDiscountSurvivesBulkEdits` 第一版刪
  variant 時先撞到 `order_lines_frozen_once_committed`(級聯更新已成交訂單的 line),
  斷言的是錯的守門 —— 正是 CLAUDE.md 第 8 條。改用沒被賣掉的 variant 才隔離出來。
