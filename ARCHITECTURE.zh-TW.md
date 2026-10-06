# 系統架構

[English](ARCHITECTURE.md) · [圖檔來源](docs/architecture/README.md)

goen 是為台灣商店打造的 Go 電商應用。前台、後台與背景工作在同一個程序中執行，訂單、庫存與帳本都存在 PostgreSQL；Stripe 處理付款與退款，綠界處理電子發票。

## 1. 商務模型

購買意圖、庫存、金流、出貨與發票的生命週期各不相同，goen 因此分開記錄。一筆訂單可以已付款但未出貨，也可以已退款、發票卻還待更正。

| 紀錄 | 職責 |
| --- | --- |
| 購物車 | 可變的購買意圖。加入購物車不占庫存；結帳時重新確認當下條件。 |
| 訂單與明細 | 買方確認的內容：商品、價格、保固、運送、收件、發票偏好與語系。 |
| 庫存保留 | 為訂單保留的數量。下單減少可售量，出貨消耗保留，符合條件的取消或逾期才釋放。 |
| 付款與退款 | 持久的嘗試識別碼與經核實的金流結果。購物金與點數各有帳本。 |
| 出貨與退貨 | 備貨、包裹、評估、驗收與入庫，與金流各自進行。 |
| 發票操作 | 等待送出、對帳或人員處理的外部工作。 |
| 發票文件 | 經核實的開立、作廢或折讓結果。 |
| 投影與縮圖 | 共購推薦與縮圖，可由權威紀錄重建。 |

下單時複製成交條件，日後改價不影響歷史。訂單進入 `settled_orders`（已承諾或已取消）後，資料庫凍結受保護欄位，範圍比「已付款」更廣；另外，`order_is_committed` 決定保留只能消耗、不能再釋放。見[結帳][checkout]與 [schema][schema]（`settled_orders`、`order_is_committed`）。

## 2. 系統邊界

![顧客與員工使用同一個 Go 程序；其 handler 與背景工作共用 PostgreSQL，並呼叫外部服務。](docs/architecture/01-system-context.png)

[SVG](docs/architecture/01-system-context.svg)

### 應用與呈現

`net/http` handler 輸出具型別的 `templ` 頁面。每個寫入都能以不需 JavaScript 的一般 `POST` 表單完成：成功後重新導向，驗證失敗則保留輸入。htmx 增強回傳的 HTML。CSS、字型與 script 嵌入執行檔，網址帶內容版本。

套件依功能劃分，各自包含 handler、store、SQL 與測試。store 呼叫 sqlc 產生的 `db.Queries`；`cmd/goen` 組合路由、資料存取、外部客戶端與背景工作。見[組合][server]、[資產][assets]與 [sqlc 設定][sqlc]。

### 外部服務

| 服務 | 職責 |
| --- | --- |
| Stripe Checkout | 代管的刷卡頁、Session、簽章付款事件與退款。 |
| 綠界電子發票 | 開立、查詢、作廢、折讓與手機條碼檢查。 |
| 綠界門市地圖 | 超商門市選擇，與電子發票分開設定。 |
| SMTP | 由持久佇列寄出郵件。 |
| Google 登入 | 選用，與密碼登入並存。 |

未設定 Stripe、電子發票、門市地圖或 Google 登入時，對應功能關閉。未設定 SMTP 時，開發模式把郵件寫入 log 並視為已寄出；secure mode 缺少 SMTP、TOTP 或 `GOEN_BASE_URL` 則拒絕啟動。使用 Stripe 正式金鑰時，還必須啟用 secure cookie、正式的電子發票與門市地圖端點、真實寄件地址，且不得有示範帳號。見[外部服務設定][provider-posture]。

PostgreSQL 也存放 session、存取授權、稽核紀錄與上傳的圖片。單一儲存讓交易留在本地、備份保持完整，代價是 I/O 與可用性集中在同一個資料庫。session 存在資料庫，多個實例可以服務同一個登入。

## 3. 結帳與付款

下單、Session 綁定與 webhook 套用分別提交，沒有任何資料庫交易跨越 Stripe 呼叫。

![下單交易 A、Session 綁定 B 與 webhook 交易 C 之間各隔著一次 Stripe 請求。](docs/architecture/02-checkout-payment.png)

[SVG](docs/architecture/02-checkout-payment.svg)

### A. 建立訂單

`placeOrder` 先鎖結帳重試鍵，再鎖使用者與購物車，接著依 UUID 順序鎖規格與商品。它重新讀取價格、運送、庫存、購物金與折扣碼資格，重算的報價必須與買方確認的一致。

同一筆交易寫入訂單快照、庫存保留、帳本、事件與 outbox，並清空購物車。以相同的鍵重試會取回原訂單，涵蓋「已提交但回應遺失」的情況。無須付款的訂單跳過 Stripe；金額為正但全額以購物金支付的訂單，在同一筆交易中扣除購物金並排入 `invoice.due`。見[下單][checkout]與 [schema][schema]（`lock_cart_catalogue`）。

### B. 綁定 Stripe Session

之後的請求以冪等鍵建立 Checkout Session，鍵由訂單編號、應付金額與嘗試次數組成；`OpenPayment` 把它綁定到訂單。重新導向前，goen 會再查一次 Session，因為 Stripe 可能重播先前的建立回應，而那個 Session 已經過期。

庫存保留 60 分鐘，新 Session 必須在 29 分鐘內建立，讓 Stripe 的最短 Session 期限落在保留期間內。若 Stripe 已建立 Session 但本地綁定失敗，goen 會讓它過期。見[付款處理][payment-handler]與 [Stripe adapter][stripe]。

### C. 套用核實的付款事實

webhook 先驗證原始 body 的簽章，再於同一筆交易中領取事件、透過 goen 的付款紀錄找到訂單、核對金額與幣別，然後套用扣款、回饋、通知與 `invoice.due`。webhook 可能比綁定先到；兩條路徑鎖同一個 provider reference。瀏覽器返回頁只負責帶顧客回來。

資料庫失敗時，領取與其效果一起回滾並回應 `500`，讓 Stripe 重試。已驗證卻無法安全套用的事件會提交一則警示並回應 `200`，留下持久的例外交由人員處理。見 [webhook 套用][payment-store]。

### 完成的 Session 如何恢復

完成的 Session 可能讓付款停在 `requires_reconciliation`，而該 reference 沒有 webhook 警示。人員到 Stripe 查證後使用 `ReconcileCompletePayment`：

- **已付款：** 受保護的扣款連同金流效果與稽核一起套用。若庫存已釋放，訂單需要退款。
- **未付款或已全額退款：** 釋放這次嘗試，讓買方重新付款。

結帳鍵、provider 請求鍵與事件 ID 各自保護自己的操作。逾時後應沿用原識別碼恢復，因為外部服務可能已經成功。見[人員恢復][health-reconcile]與 [Stripe 錯誤處理][stripe-errors]。

## 4. 庫存與付款競爭

![扣款與逾期共用訂單鎖；扣款先提交則保留庫存，釋放先提交則晚到的付款留待對帳與退款。](docs/architecture/03-stock-payment-race.png)

[SVG](docs/architecture/03-stock-payment-race.svg)

保留會減少可售量；出貨消耗保留，不會重複扣庫存，部分出貨時其餘數量繼續保留。符合條件的逾期或取消會釋放保留；付款結果未定時，保留持續到對帳完成。

扣款與釋放都會鎖訂單。扣款先提交，保留就留著；釋放先提交，扣款守門會拒絕晚到的付款，goen 記錄一則例外以供對帳與退款。見 [schema][schema]（扣款與釋放的守門）與[逾期處理][cart-sweeper]。

保留中、被放棄與釋放延遲的庫存，都會減少可售量。保留多久，既是營運問題，也是商業決策。

### 競爭熱點

結帳可能在同一個規格、其所屬商品、折扣碼、購物金帳戶，或訂單與付款列上互相等待。同一商品的不同規格共用商品鎖，因此每條路徑都必須以相同順序取鎖。

下單也會更新當日的 `order_number_counters` 列，並持有到提交為止，所以互不相關的購買會在此交會。六位數計數器讓每個營業日最多 999,999 筆訂單，當日再下單會被拒絕。修改臨界區或編號方式之前，先量測鎖的持有與等待時間。見 [schema][schema]（`next_order_number`）。

## 5. 出貨、退貨與退款

人員記錄包裹與出貨數量。核准退貨時確定退款分配、寫入稽核並開始退款；驗收與入庫是之後的步驟。退貨完成不會增加庫存。

刷卡退款在呼叫 Stripe 前先保存嘗試識別碼與操作者，只依核實的結果結算；購物金經本地帳本退回。人員按 `Resume` 會補退尚欠的來源；款項結清後，也會補記缺少的退款事件與點數扣回。見[退貨][returns]與[退款恢復][refund-payout]。

出貨前由人員退款時，依序保存核准、嘗試更正發票、退回尚欠款項，最後在守門下取消訂單並釋放庫存。發票更正失敗不會阻止退款，但可能讓取消停下來；再按一次會從第一個未完成的步驟續作。可作廢的發票直接作廢，否則可能需要買方線上同意折讓；已送出的折讓與已結案的文件分開記錄。

全額以購物金支付、尚未備貨的訂單，由顧客或人員（透過同一個出貨前退款動作）在一筆交易中取消：退回購物金、釋放庫存與排入 `invoice.void_due` 一起提交，不建立退貨單。已無法作廢的發票交由人員處理；兩條路徑都不會啟動折讓流程。見[出貨前退款][before-shipment]、[顧客取消][cart-cancel]與[發票更正][invoice-cancellation]。

## 6. 持久背景工作

![業務變更與 outbox 一起提交；背景工作寄送郵件，或把發票工作交給持久操作，再與綠界對帳。](docs/architecture/04-durable-work.png)

[SVG](docs/architecture/04-durable-work.svg)

### Outbox

後續工作與引發它的業務變更一起提交。背景工作以 `FOR UPDATE SKIP LOCKED` 領取到期的列，把 `available_at` 往後推作為持久租約，並寫入 `lease_owner`，阻擋過期的確認。

每批最多六筆，逐筆執行。背景工作每 5 秒輪詢一次，批次滿時 `DrainAll` 立刻再領。每個 handler 有 30 秒、每次確認有 5 秒，5 分鐘的租約涵蓋整批並留有餘裕。優先序影響領取，但重試與多個背景工作讓全域 FIFO 無法成立。見[背景工作][outbox]與[領取查詢][outbox-sql]。

重試會退避；從第八次起改為每日重試，並出現在健康頁。資料列保留 30 天，已寄出的從寄出時起算，未寄出的從建立時起算，因此未完成的工作也可能被清掉；`(topic, dedupe_key)` 只在該列存在時防止重複。必須超過保留期限的工作，需要自己的領域紀錄。

SMTP 收下郵件後、確認前若程序中止，郵件可能寄出兩次：寄送屬於「至少一次」，租約歸屬無法撤回外部效果。

### 發票對帳

`ClaimDue`（取消的交易則是 `ClaimVoidDue`）在任何網路呼叫之前，把應辦的請求凍結到 `invoice_operations`；義務已完成或已撤回時直接結束，人員也可以直接領取操作。outbox 列寄出只代表交接完成，發票仍有自己的狀態。

對帳程序讀取凍結的請求，查詢綠界，必要時送出，並在結案前核對識別資料、金額與品項。開立的回應不含完整品項，所以要再查詢確認。到期的 `pending` 工作自動續作，暫時失敗稍後重試，`attention` 則停止自動領取、交由人員判斷。折讓可能在等待買方同意；查無結果時，先確認外部狀態再決定是否重送。見[領取][invoice-store]與[對帳][invoice-recovery]。

### 其他迴圈

背景工作清除逾期的保留、結帳嘗試與草稿、session 與重設密碼 token、存取授權、outbox 列與孤立的上傳。共購推薦在啟動時與排程中，以 maintenance pool 在 advisory lock 下重建。排程由[背景工作啟動][main]設定；該觀察的是延遲與追趕速度。

## 7. 資料庫權限與安全

應用程式角色只能透過授權的 `SECURITY DEFINER` 函式變更受保護的付款、退款、發票、稽核、庫存、購物金與計數器紀錄；其他資料使用明確的表或欄位授權。約束、唯一鍵、狀態轉換 trigger 與共用列上的鎖共同守住規則，跨列的不變條件依賴這種序列化。[由 catalog 推導的測試][conformance]檢查 [schema][schema] 的授權與約束。

稽核證據與它描述的變更一起提交，透過 `audit.Run`、`audit.In` 或特權函式本身寫入；紀錄只能新增，帳號刪除另有明確處理。`reporting` 角色沒有執行期連線池，讀不到憑證、session、個人與收件資料、自由文字、webhook 證據，以及 outbox 與發票操作表；訂單與退貨表只開放列出的業務欄位。migration 由 schema owner 執行。

密碼使用 Argon2id；session token 不透明，並以雜湊保存。secure mode 下，session、購物車、訂單存取與門市選擇的 cookie 使用 `__Host-`；所有 cookie 都是 `HttpOnly`，`Secure` 跟隨 secure mode。員工需要授權與 TOTP 二次驗證，管理員動作另有一道守門；TOTP secret 加密保存，重播的驗證碼會被拒絕。見[帳號][account]、[存取][access]與[兩步驟驗證][twofactor]。

[Middleware][middleware] 提供 origin 保護、CSP 與受限的表單目標，只有門市地圖的返回有一個精確的例外。Stripe webhook 以簽章驗證。只有設定過的受信任 proxy 能提供用戶端位址；敏感 handler 在密碼檢查等昂貴操作前先限流。資料庫角色補強而非取代逐一的使用者歸屬檢查。

金額是有界的整數 cents，以新台幣在同一處解析。訂單保存語系；應用程式文案屬於 `i18n`，商品翻譯則是商店內容。`Asia/Taipei`、內嵌的時區資料與 SQL `shop_day` 讓營業日不受主機時區影響。見[金額][money]、[i18n][i18n] 與[商店時間][shoptime]。

## 8. 資源與工作負載

![store、admin 與 maintenance 連線池各有額度，但共用程序資源與 PostgreSQL。](docs/architecture/05-resource-boundaries.png)

[SVG](docs/architecture/05-resource-boundaries.svg)

連線池保留連線數並固定資料庫角色，但仍共用 CPU、記憶體，以及 PostgreSQL 的 I/O、WAL 與鎖。

| 連線池 | 每程序連線上限 | Statement timeout | 主要使用者 |
| --- | ---: | ---: | --- |
| `store` | 25 | 15 秒 | 前台、登入、outbox、顧客資料清理。 |
| `admin` | 10 | 30 秒 | 後台、發票、媒體清理。 |
| `maintenance` | 2 | 5 分鐘 | 共購推薦重建。 |

角色在連線建立時設定；後台登入仍使用 `store`。N 個程序最多 `37 × N` 條連線，還要為滾動部署與其他客戶端預留空間。見[連線池建立][main]。

動態請求（包含後台）有 25 秒的 context。靜態資產、探針、webhook 與 favicon 不受此限；媒體讀取原圖以 25 秒為限、產生縮圖以 30 秒為限。statement timeout 從資料庫開始計時，等待連線則跟隨呼叫端的 context，所以連線池滿了，等待中的請求仍會累積。

### 讀取成本

分類頁一般會發出 12 個查詢（列表 8、導覽 3、橫幅 1）；已登入且有購物車時約再多 3 個。圖片請求與 SQL 函式內部的查詢不計入。見[列表][catalog-store]與[導覽][home-banner]。

搜尋以跳脫且限長的 `ILIKE` 比對商品、SKU、品牌、規格與分類；資料成長時要觀察掃描、排序與篩選的成本。橫幅即時反映變更，導覽推薦也帶價格，所以快取之前要先定義新鮮度：匿名 HTML 仍可能因語系或購物車而不同，帶內容版本的資產則可以共用。見[商品查詢][catalog-query]。

報表由多個查詢組成。可重現的財務匯出需要明確的快照與截止點，不能只共用日期區間。見[報表][reports]。

### 圖片

上傳會正規化為最多 8 MiB、最長邊 2400 像素。每個 renderer 有 64 MiB 的編碼快取、依鍵 `singleflight`、固定寬度與有限的並行數，上傳也有上限。解碼後的像素與處理中的工作會占用快取以外的記憶體，縮圖本身也不檢查取消。圖片存在 PostgreSQL，讓小型商品目錄的備份保持完整，代價是資料庫大小、I/O 與還原時間。見[產生縮圖][media-render]與[處理][media-handler]。

## 9. 容量與恢復測試

負載測試要使用有代表性的硬體、資料與流量，同時檢查延遲、正確性與恢復。

```text
SQL 速率     ≈ Σ(路由請求率 × 每請求查詢數) + 背景查詢
持有連線數   ≈ Σ(到達率 × 連線持有時間) + 背景持有
積壓追趕時間 ≈ 積壓量 / (完成速率 − 到達速率)
```

持有時間從取得連線到歸還，包含鎖等待；看平均值也要看尾延遲。追趕公式假設完成速率持續高於到達速率，重試也算工作量。

| 實驗 | 變因與觀測 | 通過條件 |
| --- | --- | --- |
| 瀏覽與搜尋 | 登入與否、商品數、選擇性、分頁；查詢計畫、連線等待、p95/p99。 | 掌握資料成長與重複查詢的成本。 |
| 結帳競爭 | 不同商品、同商品的規格、單一 SKU、共用折扣碼、購物金、每日計數器。 | 庫存、購物金與折扣碼不重複消耗；預期中的拒絕另外統計。 |
| 重試與競爭 | 重複的鍵與 webhook、亂序事件、扣款對上逾期或取消。 | 狀態合法、效果只發生一次、必要的警示存在。 |
| 昂貴工作 | 冷啟動與頻繁更換的圖片、上傳、登入尖峰、報表、清理。 | CPU、RSS、GC、等待中的請求與結帳延遲維持在範圍內。 |
| 積壓與中斷 | 大量郵件中夾帶緊急郵件、外部服務變慢或失敗，再恢復。 | 工作年齡、緊急郵件期限與排空速度達標。 |
| 飽和與重啟 | 連線池全滿、持續的 webhook、實例重疊。 | 確認、readiness、取消，以及負載下降後的恢復。 |
| 外部結果遺失 | 外部服務已接受，goen 卻在記錄前中止。 | 以原操作識別碼恢復。 |

飽和時優先測 webhook 與 readiness。webhook 以呼叫端的 context 取得連線，不受請求預算限制；readiness 在同一個 2 秒內依序檢查 `store` 與 `admin`，admin 連線池飽和時可能讓整個實例被移出服務。見[付款處理][payment-store]與[探針][probe]。

競爭與故障實驗在隔離的資料上進行，搭配可控制的外部服務與排定的連線。同時使用固定使用者數（closed）與固定到達率（open）模型，並記錄預定、送出、完成、拒絕與丟棄的工作，避免系統變慢時測試器也跟著放慢。見 [k6 負載模型][k6-models]與[丟棄的迭代][k6-dropped]。

記錄版本、硬體、資料庫與連線池設定、資料分布、快取狀態與外部延遲。資料、促銷、查詢或部署方式改變時，重跑受影響的情境。

## 10. 可觀測性

goen 有 request ID、結構化的 `slog` 日誌、500 ms 以上的慢查詢紀錄（標示 sqlc 名稱或 `unnamed`、連線池與 request ID，不含參數），以及顯示連線池、待辦與對帳的健康頁。見[慢查詢紀錄][slowquery]與[健康查詢][health-sql]。OpenTelemetry 已在規劃中，用來回答：

| 問題 | 要補上的訊號 |
| --- | --- |
| 結帳慢在哪裡？ | 請求、取得連線、交易、查詢、外部服務與輸出頁面的耗時。 |
| 資料庫在執行還是在等？ | 連線持有、鎖等待、CPU、I/O、WAL、暫存溢出。 |
| 哪些讀取值得快取？ | 累積查詢成本、重複讀取、變更頻率、命中率。 |
| 背景工作跟得上嗎？ | 各 topic 的到達、完成、重試、到期等待與端到端年齡。 |
| 什麼需要人處理？ | 付款結果不明、退款未完成、發票待處理，以及原因與時長。 |

取得連線與查詢時間要分開量測：`pgxpool` 累計的 `AcquireDuration` 算不出 p99。`pg_stat_statements.track = all` 可以看到 SQL 函式內部的成本，但外層與內層的耗時會重疊。見 [pgxpool][pgxpool] 與 [pg_stat_statements][pg-statements]。

每次背景嘗試都有自己的 span，透過 trace context 連回來源；持久的時間戳則量測整件工作跨越多次重試的總時長。`outbox_oldest_seconds` 從 `available_at` 起算，而租約與退避會移動它，所以還需要從排入到完成的年齡。見 [messaging span][otel-messaging]。

指標使用有限的維度：路由樣板、連線池、topic、外部服務、結果。訂單與操作 ID 放在 trace 與受控查詢中。遙測不含個資、token、SQL 參數與原始的外部 payload，exporter 以有界的非同步方式送出。稽核與財務紀錄不受 trace 抽樣影響，始終持久保存。見 [OpenTelemetry for Go][otel-go]。

告警針對可能違約的承諾與需要處理的工作。等待買方同意、重試中與需要人員判斷，應該有不同的門檻與負責人。

## 11. 架構演進

快取命中、交易讀取、訂單提交與熱門庫存寫入的成本各不相同，總 QPS 本身無法決定 goen 是否需要 Redis 或 broker。

| 何時 | 可評估的方向 | 代價或必須保留的性質 |
| --- | --- | --- |
| 高成本的讀取反覆出現，且可在明確範圍內接受過時資料 | 先改善查詢、讀取模型、程序或 HTTP 快取，再考慮共享快取。 | 新鮮度、失效、stampede、授權範圍、回源負載。 |
| 多個實例必須共用一個頻率額度 | 共享 limiter 或在入口統一執行；Redis 是選項之一。 | 計數與到期的原子性、中斷時的行為。 |
| 需要獨立的消費者、路由、重播或保留 | 符合需求的 broker 或事件日誌。 | 可靠發布、重送、順序範圍、payload 相容性。 |
| 背景或媒體工作影響 HTTP 目標 | 獨立的 worker 程序或有限並行。 | 工作歸屬、資源額度、外部服務的速率限制。 |
| 可容忍延遲的讀取與交易互相競爭 | 投影或唯讀副本。 | 庫存與付款判斷仍以權威資料為準。 |
| `ILIKE` 搜尋的品質或成本不足 | 搜尋索引或服務。 | 索引延遲、重建、結帳時重新驗證。 |
| 圖片主導 I/O、備份或還原 | 物件儲存、CDN、預先產生縮圖。 | 引用、刪除、孤立檔案、備份一致性。 |
| 庫存或計數器的鎖限制吞吐量 | 縮短臨界區，或調整分配模型。 | 數量與金額的不變條件仍由單一權威維持。 |
| 可用性需求超過單一程序或資料庫 | 多個實例與 PostgreSQL 高可用。 | 故障切換、連線額度、全域限制、RPO/RTO。 |

採用 broker 時，要保留可靠的交接：業務交易與 outbox 一起提交，再經 relay、broker，交給具冪等性的消費者。見 [transactional outbox][outbox-pattern]。

## 12. 部署、恢復與變更安全

[啟動][main]時檢查設定與外部服務、建立並檢查連線池、組合路由，再啟動背景工作。關閉時取消程序 context，在期限內排空 HTTP，等待背景工作結束後關閉連線池。migration 另外以 schema owner 執行。

每多一個實例，記憶體內的限流額度就多一份，所有背景迴圈也都會執行。outbox 租約協調領取；共購推薦的 advisory lock 防止重疊，但不阻止另一個實例之後再重建。session 經 PostgreSQL 共用；快取、`singleflight` 與限流則各程序獨立。背景迴圈的歸屬要與 §8 的連線額度一起檢視。

在真正的商店資料出現前，migration `001` 可以修改；第一次正式部署後凍結，之後以編號 migration 變更。滾動部署需要新舊執行檔、SQL 函式與持久 payload 彼此相容。見 [migration 政策][contributing]。

先訂出可接受的資料遺失與恢復時間，再以演練驗證，媒體與其引用也要一併檢查。PostgreSQL 還原後，Stripe 與綠界的狀態可能比資料庫新，恢復時要依操作識別碼與外部結果對帳。見 [point-in-time recovery][pg-pitr]。

### 驗證

CI 執行格式、vet、lint、無用程式碼與生成碼檢查、migration lint、建置、隨機順序的 race 測試、弱點掃描、CodeQL、workflow 政策、commit 署名檢查、真實 PostgreSQL 的 schema 與並行測試、migration 來回測試，以及瀏覽器版面與無障礙檢查。專門的測試競爭庫存與退款；一般表單測試確保每個寫入不需 JavaScript 也能完成。

`ko` 建置容器映像，`templ` 與 sqlc 從頁面和 SQL 產生 Go 程式，testcontainers 在測試中執行 PostgreSQL。實際指令在 [Makefile][makefile]，檢查關卡在 [workflows][workflows]，流程在 [CONTRIBUTING.md][contributing]。

## 程式導覽

| 領域 | 入口 |
| --- | --- |
| 組合與生命週期 | [`cmd/goen`][main]、[路由][server]、[middleware][middleware]、[外部服務設定][provider-posture] |
| 結帳、保留與訂單存取 | [`internal/cart`][checkout]、[逾期處理][cart-sweeper]、[`internal/orderaccess`][orderaccess] |
| 付款與恢復 | [`internal/payment`][payment-store]、[Stripe adapter][stripe]、[`internal/admin/health`][health] |
| 出貨、退貨與退款 | [`internal/order`][order]、[`internal/admin/orders`][orders]、[`internal/returns`][returns-rules]、[`internal/admin/returns`][returns]、[`internal/admin/refunds`][refund-payout] |
| 發票與延後寄送 | [`internal/invoice`][invoice-store]、[`internal/admin/invoicing`][invoicing]、[`internal/outbox`][outbox]、[`internal/email`][email] |
| 資料庫權限 | [schema][schema]、[sqlc 設定][sqlc]、[一致性測試][conformance]、[`internal/admin/audit`][audit]、[`internal/db/dbtest`][dbtest] |
| 瀏覽與衍生資料 | [`internal/catalog`][catalog-store]、[`internal/home`][home-banner]、[`internal/recommend`][recommend]、[報表][reports] |
| 語言、金額與時間 | [`internal/i18n`][i18n]、[`internal/money`][money]、[`internal/shoptime`][shoptime] |
| 資產與媒體 | [`assets`][assets]、[`internal/media`][media-render] |
| 身分與瀏覽器安全 | [`internal/account`][account]、[員工存取][access]、[`internal/twofactor`][twofactor]、[`internal/ratelimit`][ratelimit] |
| 維運 | [健康查詢][health-sql]、[探針][probe]、[慢查詢紀錄][slowquery]、[Makefile][makefile]、[CI][workflows] |

[contributing]: CONTRIBUTING.md
[main]: cmd/goen/main.go
[server]: cmd/goen/server.go
[middleware]: cmd/goen/middleware.go
[provider-posture]: cmd/goen/provider_posture.go
[slowquery]: cmd/goen/slowquery.go
[checkout]: internal/cart/store.go
[cart-sweeper]: internal/cart/sweeper.go
[cart-cancel]: internal/cart/cancel.go
[orderaccess]: internal/orderaccess
[payment-store]: internal/payment/store.go
[payment-handler]: internal/payment/handler.go
[stripe]: internal/payment/stripe.go
[order]: internal/order
[orders]: internal/admin/orders
[returns-rules]: internal/returns
[returns]: internal/admin/returns/store.go
[refund-payout]: internal/admin/refunds/payout.go
[before-shipment]: internal/admin/refunds/beforeshipment.go
[invoice-store]: internal/invoice/store.go
[invoice-recovery]: internal/invoice/recovery.go
[invoice-cancellation]: internal/invoice/cancellation.go
[invoicing]: internal/admin/invoicing
[outbox]: internal/outbox/outbox.go
[outbox-sql]: internal/outbox/query.sql
[email]: internal/email
[schema]: migrations/001_initial_schema.up.sql
[sqlc]: sqlc.yaml
[conformance]: internal/db/coverage_integration_test.go
[dbtest]: internal/db/dbtest
[audit]: internal/admin/audit/audit.go
[account]: internal/account
[access]: internal/admin/access/access.go
[twofactor]: internal/twofactor
[ratelimit]: internal/ratelimit
[i18n]: internal/i18n
[money]: internal/money
[shoptime]: internal/shoptime
[catalog-store]: internal/catalog/store.go
[catalog-query]: internal/catalog/query.sql
[home-banner]: internal/home/banner.go
[recommend]: internal/recommend/store.go
[reports]: internal/admin/reports/store.go
[assets]: assets/assets.go
[media-render]: internal/media/render.go
[media-handler]: internal/media/handler.go
[health]: internal/admin/health
[health-reconcile]: internal/admin/health/reconcile.go
[health-sql]: internal/admin/health/query.sql
[probe]: internal/probe/probe.go
[makefile]: Makefile
[workflows]: .github/workflows
[stripe-errors]: https://docs.stripe.com/error-low-level
[k6-models]: https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/open-vs-closed/
[k6-dropped]: https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/dropped-iterations/
[pgxpool]: https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool
[pg-statements]: https://www.postgresql.org/docs/current/pgstatstatements.html
[otel-messaging]: https://opentelemetry.io/docs/specs/semconv/messaging/messaging-spans/
[otel-go]: https://opentelemetry.io/docs/languages/go/instrumentation/
[outbox-pattern]: https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html
[pg-pitr]: https://www.postgresql.org/docs/current/continuous-archiving.html
