# Stripe 設定與付款流程

沙盒商家 **Goen**(US 帳號)。本文記錄 goen 怎麼接 Stripe、目前確認可用的部分,
以及還需要你動手的一件事。

所有數字都是 **2026-07-27 對真的 Stripe test API 實測**的結果,不是推論。

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
| SDK | `stripe-go/v86` (API `2026-06-24.dahlia`) | `make verify-all` 綠 |
| goen 建立 session | 可用 | `POST /orders/{number}/pay` → 303 到 `checkout.stripe.com` |
| **webhook 簽章密鑰** | **缺** | `GET /v1/webhook_endpoints` 回 0 筆 |

`charges_enabled: False` 不影響測試模式的 Checkout —— 上面那張 session 就是在這個
狀態下建出來的。它擋的是正式收款,上線前才需要完成 onboarding。

## 你需要做的一件事:webhook

**這是唯一還缺的東西,也是整個金流最要緊的一環** —— goen 只認簽章驗證過的
webhook,顧客回到 `success_url` 不會讓任何訂單變成已付款(那個網址誰都能開)。

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
加處理邏輯時事件已經在了。

`async_payment_*` 這一對不是可有可無的。goen 刻意不傳 `payment_method_types`
(見下面第 3 點),所以 dynamic payment methods 是開的;**延遲付款方式**(轉帳
類)的 `checkout.session.completed` 會帶著 `payment_status = unpaid` 送來 ——
goen 正確地不把它當成收款 —— 而真正的成功是隨後另一個事件
`checkout.session.async_payment_succeeded`。少訂閱這一個,顧客付了錢而訂單永遠
停在未付款。失敗那一個和 `expired` 走同一條路:把 goen 開好的 payment row 從
`requires_payment` 收掉,否則對帳分不出「已經死掉的 session」和「還在進行中的」。

| 事件 | goen 做什麼 |
|---|---|
| `checkout.session.completed`(`paid`) | `capture_payment()` —— 訂單變成已付款 |
| `checkout.session.completed`(`unpaid`) | 只記錄。延遲付款方式還在處理中 |
| `checkout.session.async_payment_succeeded` | 同 completed(paid):錢真的到了 |
| `checkout.session.async_payment_failed` | `cancel_payment()` |
| `checkout.session.expired` | `cancel_payment()` |

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
  └─ 金額從訂單明細「重新計算」,不看表單任何欄位
  └─ 先問資料庫:這筆訂單「就這個金額」還有沒有開著的 session?
       有 → 向 Stripe 讀回那一個,還開著就直接轉址過去(不再建第二個)
             已經結束(付掉/處理中/過期)→ 送回訂單頁,不建第二個
             讀不到(網路錯) → 500,一樣不建第二個
       沒有 → 往下
  └─ 檢查庫存保留還剩多久。不足 30 分鐘(Stripe 的下限)就告訴顧客保留已經失效,
     不送他們去一個可能付到別人已經買走的貨的結帳頁
  └─ 建 Stripe Checkout Session
       expires_at = 這筆訂單「庫存保留」的到期時間,不是 now()+30 分鐘
       Idempotency-Key = 訂單編號 + 應付金額 + 第幾次嘗試
  └─ open_payment() 寫下 payment row(狀態 requires_payment)—— 在轉址之前
  └─ 303 → checkout.stripe.com

顧客在 Stripe 頁面付款

success_url → /orders/{number}?paid=1
  └─ 這只是一個頁面。它不會把任何東西標記為已付款。
```

### 錢真正入帳的唯一路徑

```
POST /webhooks/stripe
  └─ 驗簽章 —— 失敗回 400(重試也不會變對)
  └─ 一個交易裡:
       claim 事件(provider, event_id 主鍵 → 重送會拿到 0 筆)
       capture_payment()
       寫 'paid' 事件
     └─ 任何一步失敗 → 整個 rollback,連 claim 也退掉
        (否則 Stripe 重送會被告知「已處理」然後停止 —— 錢在 Stripe、
         訂單永遠未付款)
  └─ 回 200
```

狀態碼是協定,不是裝飾:

| 情況 | 回應 | 為什麼 |
|---|---|---|
| 簽章不對 | 400 | 重試不會讓偽造變成真的 |
| 重送已處理過的事件 | 200 | 停止重試 |
| goen 沒開過的 session | 200 | 記錄下來但不動作;重試永遠找不到 |
| goen 不處理的事件類型 | 200 | 記錄下來,歷史留完整 |
| 資料庫失敗 / 金額不符 | 500 | Stripe **應該**再試,或需要人介入 |

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

3. **不要傳 `payment_method_types`。** goen 刻意省略它,這樣才會啟用 dynamic
   payment methods —— Stripe 依幣別、國別、金額決定顯示哪些付款方式,而且改動
   在 Dashboard 完成、不用改程式。寫死 `["card"]` 會讓之後啟用的每一種付款方式
   都靜靜失效。要限制範圍請用 `payment_method_configurations`。

4. **`checkout.session.completed` 不會展開 `payment_intent.latest_charge`**,
   所以一般事件沒有卡別和末四碼。空字串是一個「值」,會撞上
   `payments_last4_format`(要求四位數字)—— 未知要寫 NULL。

5. **`success_url` 不是事實。** 任何人都能請求那個網址。只有簽章驗證過的
   webhook 能讓訂單變成已付款。

6. **一筆訂單只能有一個開著的 session。** 每次 POST 都建一個新的 session,就會
   有一個新的 `requires_payment` row —— `open_payment` 只在
   `(order_id, provider_ref)` 上去重,而每個 session 都有自己的 id。兩個分頁就是
   兩筆真的扣款,而 `payments_one_capture_per_order` 是 `status = 'succeeded'` 的
   partial unique index,所以第二筆 capture 是在**錢已經到 Stripe 之後**才被擋:
   webhook 回 500、Stripe 無限重送、沒有任何東西會退款。
   第一道防線是先查資料庫有沒有開著的 session;`Idempotency-Key` 是第二道,不是
   第一道 —— 金額變了(退回store credit、套用折扣碼)key 就會變,單靠 key 擋不住。

7. **session 的 `expires_at` 綁在庫存保留上,不是 `now() + 30 分鐘`。** 保留是從
   `PlaceOrder` 起算、session 是從按下付款起算,兩段一樣長但起點不同,所以 session
   永遠比它背後的貨活得久 —— 顧客可以付一筆已經被 sweeper 放回架上、賣給別人的
   庫存。剩下的保留不足 Stripe 的 30 分鐘下限時,goen 拒絕開 session。

## 上線前要換的東西

依照 Stripe 官方安全指引,以下三項在正式環境是必要的,現在刻意先不做:

1. **改用受限金鑰(Restricted API Key,`rk_` 開頭)而不是 `sk_`。**
   goen 只需要兩種權限:建立 Checkout Session、讀取 PaymentIntent(退款時)。
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
