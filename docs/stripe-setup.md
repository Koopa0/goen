# Stripe 設定與付款流程

沙盒商家 **Goen**(US 帳號)。本文記錄 goen 怎麼接 Stripe、目前確認可用的部分,
以及還需要你動手的一件事。

Stripe 帳號能力的觀察來自 **2026-07-27 的 test API 實測**;SDK 與 wire contract
則在 **2026-08-28** 依目前程式重新驗證。

## 為什麼是 hosted Checkout,不是 Elements

goen 的寫入面規則是「每一個變更都是能在關閉 JavaScript 下運作的
`<form method="post">`」。Elements 會把卡片欄位放在 goen 的頁面上、需要
Stripe.js 才能送出,那會讓**付款變成全站唯一只有 JavaScript 能做的事**。

hosted Checkout 是伺服器端建 session + `303 See Other`,正好是規則要求的形狀,
而且卡片資料從不經過 goen(PCI 範圍縮到一個轉址)。

## 目前狀態

| 項目 | 狀態 | 怎麼確認的 |
|---|---|---|
| API key | 可用 | `GET /v1/account` 回 `Goen` |
| TWD 收款 | 可用 | 用 US 沙盒建了一張 2,590,000 TWD 的 session,成功 |
| card 付款方式 | 已啟用 | `GET /v1/payment_method_configurations` |
| SDK | `stripe-go/v86.4.0` (API `2026-08-26.dahlia`) | HTTP contract tests 與 `make verify-all` 綠 |
| goen 建立 session | 可用 | `POST /orders/{number}/pay` → 303 到 `checkout.stripe.com` |
| **webhook 簽章密鑰** | **缺** | `GET /v1/webhook_endpoints` 回 0 筆 |

`charges_enabled: False` 不影響測試模式的 Checkout —— 上面那張 session 就是在這個
狀態下建出來的。它擋的是正式收款,上線前才需要完成 onboarding。

## 你需要做的一件事:webhook

**這是唯一還缺的東西,也是整個金流最要緊的一環** —— 正常、自動入帳只認簽章
驗證過的 webhook；顧客回到 `success_url` 永遠不會讓訂單變成已付款(那個網址誰都
能開)。唯一人工例外是後台查證 provider-complete、且沒有 flagged event 的 Session
後，透過留有 audit、仍受同一組 capture guards 約束的 recovery outcome 入帳。

開發環境不能用 Dashboard 建 endpoint,因為 Stripe 連不到 `127.0.0.1`。要用 CLI:

```bash
brew install stripe/stripe-cli/stripe     # 我沒有自行安裝軟體
stripe login
stripe listen --forward-to 127.0.0.1:9700/webhooks/stripe
```

`stripe listen` 啟動時會印出一行 `whsec_...`,把它填進 `.env`:

```
GOEN_STRIPE_WEBHOOK_SECRET=whsec_...
```

**這個值每次重新 `stripe listen` 都會變**,所以每次重開都要更新 `.env` 再重啟
`make run`。部署環境則相反:在 Dashboard 建一個指向公開網址的 endpoint,拿到的
密鑰是固定的。

沒有這個密鑰但有 API key,goen **會拒絕啟動**,因為那個組合等於在一個沒有任何
認證的端點上收錢。

### 要訂閱哪些事件

```
checkout.session.completed
checkout.session.async_payment_succeeded
checkout.session.async_payment_failed
checkout.session.expired
```

goen 對這四個採取行動,而且**四個都必須訂閱**。訂閱其他事件也可以 —— 它們會被
記錄進 `payment_webhook_events` 但不觸發任何動作,這是刻意的:歷史留完整,將來
加處理邏輯時事件已經在了。這和「四個已知事件之一,但 `data.object` 不是目前程式
看得懂的形狀」不同:後者會以 `unreadable_event` 寫入 `unreconciled`,在
`/admin/health` 要求人員檢查 endpoint API version,同時仍回 200。Stripe 重送的會是
同一份讀不懂的 bytes,回 500 只會形成重試風暴,不會修好版本落差。

`async_payment_*` 這一對是縱深防禦。session 現在把 `payment_method_types` 釘在
`["card"]`(見下面第 3 點),所以延遲付款方式不該出現;真的出現時
`UnsettledSessionFrom` 會用 ERROR 記下來,因為那代表設定被改過、庫存模型已經不
成立。**延遲付款方式**(轉帳類)的 `checkout.session.completed` 會帶著
`payment_status = unpaid` 送來 ——
goen 正確地不把它當成收款 —— 而真正的成功是隨後另一個事件
`checkout.session.async_payment_succeeded`。少訂閱這一個,顧客付了錢而訂單永遠
停在未付款。失敗那一個和 `expired` 走同一條路:把 goen 開好的 payment row 從
`requires_payment` 收掉,否則對帳分不出「已經死掉的 session」和「還在進行中的」。

| 事件 | goen 做什麼 |
|---|---|
| `checkout.session.completed`(`paid`) | `capture_payment()` —— 訂單變成已付款 |
| `checkout.session.completed`(`unpaid`) | 已理解但不入帳。ERROR 記錄設定漂移,不標成 unreadable |
| `checkout.session.async_payment_succeeded` | 同 completed(paid):錢真的到了 |
| `checkout.session.async_payment_failed` | `cancel_payment()` |
| `checkout.session.expired` | `cancel_payment()` |

事件還有四個不能自動完成、但一定要留下可處理記錄的終局:

- `unreadable_event`:已訂閱、會採取動作的事件,但 `data.object` 不是目前 binary
  能理解的形狀;先查 endpoint API version 與原始 payload。

- `unattributed_capture`:Stripe 的 paid Checkout Session 在本機沒有 payment row;
  不猜訂單、不補造 payment,用 event 的 `object_ref` 到 Stripe 查。
- `cancelled_order_capture`:錢到時訂單已取消;本機拒絕入帳,由人員在 Stripe 退款。
- `refused_capture`:session 在本機有 payment row,但實收金額、訂單應付金額或另一個
  穩定資料庫不變量拒絕入帳。這代表 Stripe 已收款,不能靠重送修復;在人工確認／退款
  並把事件標為 reconciled 前,同一張訂單不得再建立新的 Checkout Session。

四者都以 ERROR 記錄、出現在 `/admin/health`,並回 200。事件的 `reconciled_at`
只能由一個明確結論設定:Stripe 端每一分錢都已全額退款,或已有 succeeded payment
完整入帳;單純「看過／查過」不能解除付款閘門。原因文字和原始 payload 仍保留作為歷史。

## 流程

### 下單到付款

```
POST /checkout
  └─ 一個交易裡:寫訂單、寫明細、寫配送資料、hold_inventory 保留庫存、
     寫 'placed' 事件、清空購物車
  └─ 303 → /orders/{number}/pay

GET /orders/{number}/pay
  └─ 顯示應付金額。閘門:這個瀏覽器下過這筆單,或登入帳號擁有它。
     其他人拿到的是 404(不是 403 —— 403 等於確認這個編號是真的)

POST /orders/{number}/pay
  └─ 先問資料庫:這筆訂單有沒有任何非終局 session,或仍待人工處理的 provider event?
       待對帳 → 409,不呼叫 Stripe,直到人員完成查核／退款並 reconcile
       同金額 session → 向 Stripe 做一次新的 Retrieve:
         open → 直接轉址過去(不再建第二個)
         expired → 本機改成 cancelled;下次使用新的 attempt generation
         complete → 寫 `requires_reconciliation`,不取消、不替代;等可能仍在路上的
                    paid webhook,同時讓 `/admin/health` 有可操作的 durable alarm
         讀不到或未知狀態 → 保守回 500,不建第二個
       舊金額 session → 也是先 Retrieve:
         open → Stripe 明確 expire 成功後,本機才改 cancelled
         expired → 直接收斂本機 cancelled(涵蓋上次 DB 寫入失敗)
         complete → 以原 session 的 intended amount 寫 `requires_reconciliation`,拒絕替代
         讀不到／未知狀態 → 拒絕替代,避免第二次扣款
       沒有非終局 session → 往下
  └─ 檢查庫存保留還剩多久。不足 31 分鐘(Stripe 的 30 分鐘下限 + 1 分鐘
     建立裕量)就告訴顧客保留已經失效,
     不送他們去一個可能付到別人已經買走的貨的結帳頁
  └─ 建 Stripe Checkout Session
       expires_at = 這筆訂單「庫存保留」的到期時間,不是 now()+30 分鐘
       Idempotency-Key = 訂單編號 + 應付金額 + 第幾次嘗試
  └─ open_payment() 在 order row lock 內重新檢查:
       訂單仍 pending、尚未 funded、應付金額未變、沒有待對帳事件、
       且每張訂單仍只有一個 active payment;通過才寫 requires_payment row
       任何失敗 → 用不繼承瀏覽器取消的短 timeout 關閉剛建立的 Stripe Session;
                   Stripe 明確確認 expired 後寫 cancelled tombstone,讓 attempt/key 前進
  └─ 寫入成功後再向 Stripe 做新的 Retrieve(不能相信 idempotent Create 的舊回應)
       open → 303 → checkout.stripe.com
       expired → 本機 cancelled,不轉址
       complete → 本機 `requires_reconciliation`,不轉址、不開下一個 generation

顧客在 Stripe 頁面付款

success_url → /orders/{number}?paid=1
  └─ 這只是一個頁面。它不會把任何東西標記為已付款。
```

### 自動入帳與稽核復原

```
POST /webhooks/stripe
  └─ 驗簽章 —— 失敗回 400(重試也不會變對)
  └─ 一個交易裡:
       claim 事件(provider, event_id 主鍵 → 重送會拿到 0 筆)
       capture_payment() 在 SAVEPOINT 內
       成功 → 寫 'paid' 事件
       穩定的不變量拒絕 → rollback 到 SAVEPOINT、寫 `refused_capture`,保留 claim
     └─ 暫時性資料庫錯誤或其他未知失敗 → 整個 rollback,連 claim 也退掉
        (否則 Stripe 重送會被告知「已處理」然後停止 —— 錢在 Stripe、
         訂單永遠未付款)
  └─ 回 200

POST /admin/health/reconcile
  ├─ unapplied event:
  │    只有「已全額退款／已有 succeeded 完整入帳」可以 release event 與付款閘門
  └─ complete payment（沒有 outstanding event）:
       confirmed paid → 以 payment row 的 immutable intent 走 capture_payment，
                        paid timeline、loyalty、outbox、audit 同一交易
       confirmed unpaid/refunded → payment 進 terminal reconciled，才解除付款閘門
       若 stock hold 已 released → paid 動作不顯示；舊頁 POST 也由 DB 拒絕，
                                  必須先在 Stripe 全額退款再 safe-release
```

狀態碼是協定,不是裝飾:

| 情況 | 回應 | 為什麼 |
|---|---|---|
| 簽章不對 | 400 | 重試不會讓偽造變成真的 |
| 重送已處理過的事件 | 200 | 停止重試 |
| goen 沒開過的 session | 200 | 記錄下來但不動作;重試永遠找不到 |
| goen 不處理的事件類型 | 200 | 記錄下來,歷史留完整 |
| 已收款,但穩定金額／狀態不變量拒絕入帳 | 200 | 寫 `refused_capture`,擋住同訂單再付款,由人員對帳或退款;相同事件重送不會改變事實 |
| 暫時性或未知資料庫失敗 | 500 | 整個交易 rollback,讓 Stripe 再試 |

### 出貨

```
POST /admin/orders/{number}/status  (status=picking)
  └─ 未付款的訂單會被 orders_funded_to_leave_pending 擋下

POST /admin/orders/{number}/ship  (carrier + tracking)
  └─ 一個交易裡:出貨單、出貨明細、狀態改 shipped、
     消耗保留的庫存、寫 'shipped' 事件
  └─ status 端點「拒絕」直接設成 shipped —— 出貨表單是唯一的門
```

## 測試卡號

| 卡號 | 結果 |
|---|---|
| `4242 4242 4242 4242` | 成功 |
| `4000 0000 0000 9995` | 餘額不足(拒絕) |
| `4000 0025 0000 3155` | 需要 3-D Secure 驗證 |

到期日填任何未來日期,CVC 任意三碼,郵遞區號任意。

## 幾個容易踩錯的地方

1. **TWD 不要除以 100。** 對「收款」而言 TWD 是普通的兩位小數幣別,goen 的
   `_cents` 直接對應 Stripe 的 `unit_amount`。文件裡那條「TWD 視為零小數」是講
   **手動撥款**(payout)的。除以 100 會少收 100 倍。

2. **`provider_ref` 存的是 Checkout Session id,不是 PaymentIntent id。**
   實測確認:建立 session 時 `payment_intent` 是 `null`,所以 session id 才是
   goen 在必須寫下那一行的當下手上有的識別碼。將來做退款需要 PaymentIntent,
   屆時用 session id 向 Stripe 反查。

3. **`payment_method_types` 必須釘在 `["card"]`,不要拿掉。**

   這一條原本寫的是相反的話——「刻意省略它才會啟用 dynamic payment methods,
   寫死 `["card"]` 會讓之後啟用的每一種付款方式都靜靜失效」。那個理由本身成立,
   但它和 `ExpiresAt` 的決定互相矛盾,而後者是為了讓錢不可能在貨已經回到架上
   之後才到。

   **延遲付款方式**會摧毀它:它的 `checkout.session.completed` 帶
   `payment_status = unpaid`,真正的錢在幾天後才以 `async_payment_succeeded`
   到達——那時 session 早就結束,所以 `ExpiresAt` 管不到;而已完成的 session
   不會觸發 `checkout.session.expired`,所以補救路徑也不通。庫存在下單 60 分鐘
   後被 sweeper 放回架上、可能已經賣掉。`capture_payment` 現在會在同一把 order
   lock 下讀到 released reservation，拒絕把這筆錢記成可履約的 succeeded；簽章
   webhook 會留下 durable `refused_capture`，後台只能先在 Stripe 全額退款再解除
   閘門。這避免「錢收了、貨沒了」被當成正常訂單，但仍代表店家收款後必須人工
   退款，所以不是對延遲付款方式的支援。

   卡片是這個有界 session／60 分鐘 hold 撐得住的付款方式,所以 goen 只提供卡片(Apple Pay 和
   Google Pay 走的就是這個 type)。支援延遲付款是一個**功能**而不是一個開關:
   它需要一個寫得出來的 `processing` 狀態、一個以付款截止日為壽命的 hold、
   一個會避開「錢在飛」訂單的 sweeper,以及店家對「未付款的轉帳可以押幾天庫存」
   的決定。

   `ExcludedPaymentMethodTypes` 是比較窄的替代方案,沒有採用:它讓 Dashboard
   保持權威,對之後新增的付款方式是 fail-open——正是預測性錯誤 #30 說要關掉、
   而不是只是點名的那種形狀。

4. **`checkout.session.completed` 不會展開 `payment_intent.latest_charge`**,
   所以一般事件沒有卡別和末四碼。空字串是一個「值」,會撞上
   `payments_last4_format`(要求四位數字)—— 未知要寫 NULL。

5. **`success_url` 不是事實。** 任何人都能請求那個網址。簽章驗證過的 webhook
   是自動入帳的門。若 provider-complete Session 沒有 flagged/unreconciled event，
   店員在 Stripe 核對後可走留有 audit 的兩種明確結論：以 immutable intent 入帳，
   或確認未收款／已全額退款。若已有待處理 event，paid attribution 會被拒絕；該門
   只能在每一分錢已退回、或已有 succeeded payment 完整入帳時安全解除。

6. **一筆訂單只能有一個開著的 session。** 舊實作每次 POST 都建一個新的 session,
   而 `open_payment` 當時只在 provider reference 上去重;每個 session 有自己的 id。兩個分頁就是
   兩筆真的扣款,而 `payments_one_capture_per_order` 是 `status = 'succeeded'` 的
   partial unique index,所以第二筆 capture 是在**錢已經到 Stripe 之後**才被擋:
   webhook 回 500、Stripe 無限重送、沒有任何東西會退款。
   現在有三層防線:handler 先處理「任何金額」的 active session／待對帳事件;
   Stripe `Idempotency-Key` 合併同一 generation 的併發 create;`open_payment` 再以
   order lock、`payments_one_active_per_order`、funded／amount／reconciliation gate
   做最後 admission。金額變了 key 也會變,所以 key 從來不能取代另外兩層。

7. **session 的 `expires_at` 綁在庫存保留上,不是 `now() + 30 分鐘`。** 庫存從
   `PlaceOrder` 起保留 60 分鐘;顧客可在前 29 分鐘開始新 session,另外留下 Stripe
   最短 30 分鐘壽命與 1 分鐘的 Unix 秒截斷／時鐘偏差裕量。session 直接使用 hold
   的 deadline,所以 Stripe 停止收款與 sweeper 可釋放庫存是同一個時刻。

## 上線前要換的東西

依照 Stripe 官方安全指引,以下三項在正式環境是必要的,現在刻意先不做:

1. **改用受限金鑰(Restricted API Key,`rk_` 開頭)而不是 `sk_`。**
   權限只開給程式實際呼叫的資源與動作:Checkout Session 的建立、讀取與到期;
   PaymentIntent 的讀取;Refund 的建立與列舉。不要為方便給整個帳號寫入權。
   `sk_` 是整個帳號的全權金鑰,外洩的破壞半徑差了一個數量級。做法:先在
   Workbench 看 `sk_` 的請求記錄,照著開一把 test 模式的 `rk_`,用
   `stripe logs tail` 盯 403 補權限,確認後再開 live 模式那把。

2. **webhook 端點加上 Stripe IP 允許清單。** 簽章驗證已經是強保證,IP 清單是
   縱深防禦 —— 它讓偽造請求連被驗簽的機會都沒有。清單在
   `https://docs.stripe.com/ips`。這需要反向代理層設定,不是程式碼。

3. **完成帳號 onboarding**(`charges_enabled` 目前是 `False`)。測試模式不受
   影響,正式收款需要。

**金鑰不要放在原始碼或 committed 的環境檔裡。** 目前 `.env` 和
`stripe-test-api-key.txt` 都在 `.gitignore`,部署時應改用平台的 secrets vault
(AWS Secrets Manager / GCP Secret Manager / Azure Key Vault),環境變數是沒有
vault 時的退路。

## 不需要建立的東西

- **Stripe Products / Prices**:goen 用 `price_data` 在建立 session 時內嵌價格,
  因為訂單是「當時談定的內容」的紀錄,回頭讀目錄會顯示改名或改價後的商品。
- **Stripe Customers**:目前是訪客結帳,只把 email 傳給 session。
- **Stripe Projects**:那是管理第三方基礎設施的 CLI,和金流無關。
