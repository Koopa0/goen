# goen 的完整度路線

目標:一個可以拿來展示的完整電商 —— 功能豐富度對得起主流電商,軟體工程與系統
設計本身也是展示的一部分。視覺**不參考**任何既有電商,goen 有自己的設計語言
(`docs/decisions/002-storefront-design-approach.md`)。

**這份文件本身是可以過期的,而且過期過。** 上一版把商品管理、圖片上傳、報表、
員工管理、商品問答、商品比較、保固登錄、Outbox、速率限制、SEO 和政策頁全部列為
「無」,那時它們都已經蓋好了 —— 而一次第三方審查因此把一條**早就關掉的**發現
重新提報了一次(見 `07-codex-round6-dispositions.md`:被引用的段落在同一個檔案
往下 160 行就寫著「已關閉」)。所以這裡只寫兩種東西:**沒蓋的**,和**決定不蓋
的**。蓋好的功能寫在 CLAUDE.md,不在這裡重複一份會走鐘的清單。

## 排序的原則

1. **看得到但用不了的**,優先於還沒有的 —— 那是缺陷,不是缺功能。
2. **購買主線上的**,優先於周邊。
3. **schema 已經設計好的**,優先於要新設計的 —— 那代表當初想過,只是沒蓋。

---

## A. 還沒蓋,而且要蓋

### A1. ~~退貨後段工作流~~ — 已完成

`/admin/returns` 會收件了:逐行填實收與可再販售的數量,可售的透過
`record_inventory_movement` 以 `return` 為 reason 回架,然後結案。
`return_requests_completed_is_inspected` 讓「已完成」有意義 —— 還沒驗的貨關不了案。
一條線只驗一次,更正走 `/admin/stock/{sku}` 的調整門。

不做三態處置(可售 / 拆封品 / 瑕疵):「拆封品」只有在它變成一個以不同價格上架的
variant 時才有意義,建模一個沒東西能處理的狀態就是這個 repo 一直在抓的「有表沒門」。

### A2. ~~分批出貨~~ — 已完成

一張訂單可以分成任意多個包裹了。`CanShip` 看的是「還欠什麼」而不是狀態,所以出過
一箱、還欠東西的訂單可以再出;保留會拆分,`consume_reservation_partial` 結算掉出
去的那部分,剩下的繼續held。每個包裹各發一封通知。

### A3. ~~統一發票開立~~ — 已完成

`internal/invoice` 走綠界 B2C 電子發票 API 的**測試環境**,用他們公開的測試憑證
(MerchantID 2000132)。程式是真的:真的 AES 封包、真的錯誤碼、真的作廢與折讓。
上正式只是換 `GOEN_ECPAY_*` 四個值。

姿態跟 Stripe 一樣:沒設定就不開發票並說明白,設一半則拒絕啟動。

真實 API 教了四件文件上沒寫的事,每一件都變成測試 —— 見 CLAUDE.md。

### A4. ~~Google OAuth~~ — 已完成

`/auth/google` 是授權碼流程,帶 state(CSRF)和 PKCE S256。沒有解析 ID token:
token 端點是直連 TLS 回來的,連線本身就是驗證,所以不需要 JWKS 快取和 JWT 函式庫,
也沒有新的相依(模組數還是 103)。

綁定規則是這一項唯一真正的決定,寫在 `SignInWithGoogle` 的註解裡:**只有兩邊都
驗證過同一個信箱才綁定**。goen 註冊時不驗證信箱,所以照地址自動綁定會讓攻擊者
先註冊受害者的地址、等對方第一次用 Google 登入時接收整個帳號(pre-hijacking)。
拒絕之後的出路是 /forgot —— 信寄到對方剛證明自己讀得到的信箱,而重設會結束所有
session,把冒用者踢出去。

### A5. ~~保固的店家那一面~~ — 已完成

`/admin/warranty` 用序號或訂單編號查得到客人登錄的保固。在這之前
`warranty_registrations` 是客人寫、客人讀,店裡沒有人看得到 —— 而 `/warranty` 上
寫著「登錄過的機器我們來收、運費我們付」。**客人打電話來報修,唯一有紀錄的人是
報修的那個人。**

同一批修掉保固的時鐘:到期日本來從 `shipped_at` 起算,現在從 `delivered_at`。
每個客人被少算一到三天,而 CLAUDE.md、套件註解和查詢註解三個地方都寫著「保固從
送達起算」。原因不是誰不小心 —— 功能上線時 `delivered_at` 根本沒有人寫,是分批
出貨那批補上 `applyStatusEffects` 之後才存在的。詳見 CLAUDE.md 第 34 條。

### A6. ~~進貨~~ — 已完成

`/admin/stock/{sku}` 可以登記進貨了,帳本上記 `receipt` 而不是 `adjustment`。
那個 reason 從 schema 寫下來的那天就有 CHECK、有方向規則、有後台標籤 —— 而唯一
寫過它的是開發用的 seed。所以店家自己買進來的每一件,在自己的帳本裡都跟「數錯了
改一下」長得一模一樣,而那一頁存在的理由就是回答「這個為什麼是四」。

不做採購單。goen 沒有供應商、沒有成本、沒有單據可以指,而建一個沒東西能處理的
狀態就是這個 repo 一直在抓的「有表沒門」。

### A7. ~~後台多國語系~~ — 已完成

`/admin` 本來被 i18n 守衛以「類別」排除,理由寫的是「這是一家台灣店的員工」——
那是關於**讀者**的主張,不是關於程式的。店家改了答案,所以排除刪掉而不是縮小:
排除條目自己就寫著觸發條件(「如果 goen 請了不讀中文的人,這一行是第一個要改的」)。

1,058 個字面量、52 個檔案,變成 402 個新 key 加 113 次重用。`pendingTranslation`
是空的,而 `TestThePendingListOnlyHoldsRealDebt` 拒絕一個檔案已經乾淨的條目,
所以它不會偷偷長回去。

最後一批抓到一個真缺陷:五組配對欄位(圖片說明、規格項目名、規格值、規格表項目、
規格表內容)各自示範「這格要填什麼」,旁邊是 `_en` 雙胞胎。翻譯之後兩個 input
在英文頁上顯示**完全一樣**的字,那一對就不再說明哪半邊要哪個語言了。這些欄位的
語言是 schema 定的、不是讀者定的,所以它們的範例不跟著讀者走。

### A8. 短出貨:放棄出不完的尾數

**還沒蓋,而且是這份清單裡唯一一件購買主線上的。**

`orders_finished_when_shipped` 拒絕一張還欠著包裹的訂單結案 —— 因為沒出去的東西
要嘛還會出去(`CanShip` 依「還欠什麼」而不是依狀態,第二個包裹本來就允許),
要嘛是一次**放棄**,而放棄是人做的決定,不是下拉選單的副作用。

所以缺的是那道門:店家明確說「這一行不出了」,把庫存放回架上、把錢退掉、
在訂單留下寫著為什麼的一列,然後訂單才結得掉。在那之前,一張出了一半又不打算
出完的訂單只能停在 `shipped` 或 `delivered`。

CLAUDE.md 說這件事「已具名排程」,而排程的地方是**這裡** —— 它同時寫著
roadmap.md 是「唯一一份沒蓋的清單」。上一輪把它記在 `docs/reviews/` 的處置檔裡,
那是處置紀錄,不是清單;一份第二清單就是有人會讀到的那一份。

最後對帳:2026-08-21。

### A9. 收窄後台 `ErrRefused` 的資料庫錯誤分類

`internal/admin` 還有約 50 個 `%w: %w` 把同一呼叫點的所有資料庫錯誤都分類成
`ErrRefused`:分布在 `product.go`、`shipping.go`、`store.go`、`campaign.go`、
`taxonomy.go`、`coupon.go`、`image.go`、`tiers.go`、`banner.go`、`faq.go`、
`hero.go`、`delivery.go` 和 `question.go`。目前這些路徑都會把完整原因寫進 WARN,
所以本輪只修沒有紀錄就吞掉錯誤的商品讀取;後續要逐點只把具名 constraint 映射成
業務拒絕,讓 timeout、斷線等基礎設施錯誤維持原類別。完成時也要改掉
`ErrRefused`「資料庫拒絕的 write」這句過度寬鬆的註解。

最後對帳:2026-08-24。

### A10. 讓 migration 的靜態分析讀取完整歷史

`internal/db/writers_test.go` 和 `internal/db/coverage_integration_test.go` 目前把
`001_initial_schema.up.sql` 當文字解析,分別盤點 stored function body 與 role / grant
順序。它們不是建立測試資料庫的入口,所以不應併進 `dbtest` 的修正;但 `002` 出現後,
只讀 `001` 仍會漏掉後續 migration 裡的新 function、role 或 grant。兩個 parser 已有
最小結果數的自我檢查,因此比靜默套用舊 schema 的 suite 低優先,但仍要改成依 migration
順序讀完所有 `*.up.sql`。

最後對帳:2026-08-24。

### A11. 商品問答的顧客回覆入口

補上 `POST /p/{slug}/questions/{id}/answers` 的 storefront route 和表單。這道門
必須由登入顧客送出、成功回 `303`,驗證失敗回 `422` 並保留原值,同時帶完整的
`aria-invalid` / `aria-describedby` 關聯。`product.Store.Answer` 已依
`answer-staff-flag-is-caller-supplied.md` 收窄為顧客專用:簽章不再接收 `staff` 旗標,
`is_staff=false` 由 package 決定。現在保留的方法供 integration fixtures 建立顧客
回覆,其中包括後台 queue 的「顧客已回覆、店家尚未回覆」案例;雙向 deadcode
allowlist 會在這道 route 接上時要求刪除例外。

最後對帳:2026-08-25。

### A12. 讓分拆退貨的可付款半邊不被卡片終態一起擋住

`payApprovedReturn` 先退卡片、再補購物金。卡片端若被付款服務明確拒絕,目前整個
流程立刻停止,連本來可以獨立補回的購物金也一起擱置。`/admin/returns` 現在會如實
標成必須人工處理,但仍沒有一道門能先付可付的 credit half。實作時要把兩個來源各自
結算並各自呈現,不能讓一邊的終態吞掉另一邊。

最後對帳:2026-08-25。

### A13. Back-office timestamps render in the process zone, not the shop's

後台約 25 個 `time.Time.Format("2006-01-02 15:04")` 直接用 process 的
local zone。Container 若跑 UTC，台北店家看到的時間會整體差八小時。這些目前
只是顯示，沒有決策從它們衍生，所以不跟法定鑑賞期的 calendar fix 混成一批；
要補的是一個共用的店舖時區顯示門，不是再對每個 call site 各自換算。

最後對帳:2026-08-25。

## B. 蓋了會更好,但要等一個數字

這兩條**不是缺陷,是門檻沒到**。決策文件裡有量測,不是猜的。

| 項目 | 觸發條件 | 現在的數字 | 依據 |
|---|---|---|---|
| **中文搜尋的 bigram tsvector 投影** | 約 5,000 個上架商品,或量到中文搜尋超過 50ms | 種子 15 個商品;10,000 個商品時量到 8.8ms(trigram 索引服務拉丁文查詢,短 CJK 查詢選擇性太低,planner 不用它) | `docs/decisions/003-listing-read-model.md` |
| **首頁 / 列表 tile 的讀模型投影** | 約 1,000–2,000 個上架商品,或量到 tile 延遲進入數十毫秒 | 種子 15 個商品時 0.26ms;10,000 個商品時 60ms(照計算分數排序會掃過每個上架商品) | `docs/decisions/001-home-read-model.md` |

**這兩個門檻現在沒有感測器。** 觀測性是刻意延後的,代價就寫在這裡:上面每一個
數字都要有人手動去量才會知道到了沒。真的要蓋的時候,形狀是 otel → Prometheus/
Grafana,而 `/admin/health` 的規則照套:量**工作**,不要量心跳。

`product_copurchases` 是唯一已經蓋好的投影,而且它的過時是**有論證的**:「跟這個
一起買」的一小時前答案還是同一個答案,而一小時前的庫存數字是超賣。那個不對稱就是
全部的理由。

### B1. In-memory cache(ristretto / badger)—— 量過了,結論是**不要**

不是「以後再說」,是**量完之後的決定**。`EXPLAIN (ANALYZE, BUFFERS)` 加
`pgbench -M extended -c 4`,對照 2026-08-17 的 PostgreSQL 18.4。

**能安全快取的東西只值一頁的 7%。** 首頁 p50 是 5.2ms;快取能服務的五個
shop-wide 讀取合計 1.13ms,而其中三個**不能**快取,理由是正確性不是成本:

- `FreeDeliveryThreshold` —— CLAUDE.md 才剛關掉這條漂移:「一個頁面說出的運費是
  一個承諾,而唯一守著那個承諾的地方是結帳收費的那張表」。加 TTL 等於**把漂移
  裝上計時器再打開一次**,而且每個 replica 一份。
- `CurrentPromoBanner` —— `cmd/goen/server.go` 已經用文字拒絕過:「不快取,因為
  店家把促銷關掉時預期它就是不見了」。
- `CurrentHeroSlide` —— 視窗是拿資料庫的 `now()` 比的;快取會讓排程的檔期**晚
  開始**最多一個 TTL。

剩下的 `NavCategories` + `HomeCategories` 是 0.378ms —— 5.2ms 的 7.3%,而且
`sync.Map` + `time.Ticker` 就收得到,不需要相依。**那兩個查詢現在已經合併成
`RootCategories` 一個**,所以那 0.378ms 有一半是永久省下的,過時為零。

**大數字的兩條路,答案是投影不是快取。** 9,999 個上架商品時
`HomeRecommendedTiles` 是 275.7ms 冷 / 69.5ms 熱(130,912 buffers 換 8 列),
`CategoryListing` 是 72.1ms —— 這**確認**了 `001` 已經寫下的門檻(它記的是 60ms /
221ms),沒有推翻它。而這兩個查詢都帶 `in_stock` 和 `min_price_cents`:**快取它們
就是快取庫存和價格**,而這個 repo 自己的判準是「一小時前的『跟這個一起買』還是同一個
答案,一小時前的庫存數字是超賣」。

決定性的論證是**副本數**:一個投影是 PostgreSQL 裡**一份共用的複本**,
`TestEveryTableIsRead`、`TestEveryTableHasAWriter`、`TestEveryColumnIsReadOrWritten`
都看得到它,`/admin/health` 已經在報它的年齡。**N 個 process-local 快取是 N 份會
互相不一致、而且這個 repo 每一個守衛都看不見的複本** —— 每個守衛問的都是「有沒有
缺」,而這正是 #13、#30、#31 記下的盲點。兩個 replica 對同一個 SKU 回兩個庫存數字,
是 #13 的形狀而且沒有一行程式碼可以指。

模組圖的代價量過了:goen 現在 103,加 ristretto 是 106(+3),加 badger 是 114
(+11)。ristretto 以 `ko` 的標準(104→528)算便宜 —— 但它仍然是錯的工具,因為
**goen 可快取的鍵空間是 2 個查詢 × 2 個語系 = 4 個鍵**。TinyLFU 的准入策略和
成本式淘汰是為了大而雜、有記憶體壓力的鍵空間;蓋在 4 個鍵上純粹是額外開銷。「它在
核准清單上」不是在 map 才是誠實資料結構的地方使用它的理由。

**badger 是無條件不要**,而且不是效能理由:它是持久化的 LSM 儲存,不是快取。它會
把耐久狀態放到磁碟,而 goen 的架構是一個 binary 加一個 PostgreSQL;它需要 `ko` 不
提供的 volume(這正是 `internal/media` 的圖片放在 PostgreSQL 裡的同一個限制),並
且替一個沒有 compaction 也沒有 crash recovery 的行程加上這兩件事。

| 項目 | 觸發條件 | 現在的數字 |
|---|---|---|
| **chrome 的 in-memory 快取** | chrome middleware 的來回佔 storefront p50 超過 20%,或連線池出現非零 `EmptyAcquireCount` | 合併後 1 次來回,5.2ms 的頁面上約 3.6% |
| **ristretto 本身** | 快取鍵空間超過約 10,000 個鍵且有記憶體壓力 | 4 個鍵 |

跟上面兩條一樣,**這兩個門檻也沒有感測器**。

---

## C. 決定不蓋

- **多商家 / marketplace**:goen 是選品店,那是另一個產品。
- **抄任何既有電商的視覺**:功能可以參考,畫面不行。
- **行為式個人化推薦**(看了又看):需要瀏覽紀錄,而這個專案刻意不收集。
  `product_copurchases` 沒有 user 維度,是設計而不是省略 —— 見
  `docs/decisions/004-recommendation-read-model.md`。這一條是從「還沒做」移到
  「不做」的:上一版把它列在想要的功能裡,那跟不收瀏覽紀錄的決定互相矛盾。

---

## D. 已知的取捨,不是待辦

寫在這裡是因為它們會被誤認成缺陷。

- **超商取貨的離島加價收不到**。加價從郵遞區號找分區,而超商取貨的目的地是門市,
  沒有郵遞區號。實測綠界店家清單:7-ELEVEN 有 76 家離島門市、全家 23 家,所以包裹
  真的會過海,而 goen 一毛都加不到。要關掉它需要一份能回答「這個店號在哪」的門市
  目錄,那跟綠界物流一起來。**店家自己吸收**,這是記錄在案的決定。
- **延遲付款方式是關掉的**,而且是 fail-closed。session 釘死 card,因為庫存保留只
  活 60 分鐘。要打開需要一個寫下來的 in-flight 付款狀態、一個以付款期限為壽命的
  保留、一個放過在途訂單的到期謂詞,以及扣款時的庫存檢查。底下那個問題是店家的:
  **一筆沒付的轉帳可以壓住幾天的庫存?** Stripe 在台灣沒有任何本地延遲方式,所以
  這個釘子目前不花任何成本。
- **`ok_mart` 留在取貨白名單裡**,綠界物流來的那天它就走 —— ECPay 的 `CvsType` 不
  收 OK,但它的 API 問起來又真的回 688 家 OK 門市。「在目錄裡」和「寄得到」是兩個
  問題。
- **字型從 Google Fonts 載入**。上線前要自架並做子集化,`cmd/goen/server.go` 的 CSP
  屆時可以少兩個第三方來源。
- **`.ui-btn` 在 vendored 設計系統裡是 content-box**,所以 `.ui-btn--block` 永遠是
  容器寬度 +30px。goen 在 `app.css` 逐個介面補償;正確的修法在上游。
- **`002` 還沒切**,而 `make schema-drift` 是讓這件事安全的東西。什麼都還沒部署,
  所以 `001` 繼續原地修改;schema 一旦到達任何不可丟棄的環境,這件事就停止,`002`
  開始。
- **`govulncheck` 會報一條 `golang.org/x/crypto` 的 GO-2026-5932,而它沒有修法**。
  被點名的是 `openpgp` 套件(unmaintained, unsafe by design),`Fixed in: N/A` ——
  升級到不了。goen 用 `x/crypto` 的是 argon2,呼叫不到那條路徑,所以掃描結果是
  「your code is affected by 0 vulnerabilities」。寫在這裡是因為下一個看掃描輸出的
  人會再問一次:**一條沒有修法又呼叫不到的建議,重複調查的成本比記下來高**。

---

## E. 這個 repo 學到的東西,寫在別處

每一條缺陷的機制、每一個守衛為什麼長那樣、以及 37 條具名的錯誤,都在 CLAUDE.md。
那裡是讀的地方;這裡只回答「接下來蓋什麼」。
