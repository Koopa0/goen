# 審查請求 ② — go-spec 匯入後的程式碼與工具鏈

> 整段複製給 Codex。這是第二輪:第一輪審 schema 的處置紀錄在
> `docs/reviews/01-schema-response.md`,請一併複審那份的處置是否真的成立。

---

## 背景

goen 是台灣 3C 電商:Go 1.26、net/http 無框架、server-rendered templ、htmx、
PostgreSQL(pgx/v5 + sqlc)、Stripe。尚未上線,沒有正式資料。

`~/go-spec` 是這個專案宣稱遵循的治理發行版。**先前只是照它的想法做,從未真的匯入它的
設定** —— 這一輪把該匯入的匯入了,並依它的規則整理了現有程式碼。

## 這一輪做了什麼

### 從 go-spec 匯入

| 項目 | 位置 | 改動 |
|---|---|---|
| Linter 設定 | `.golangci.yml`(40 行 → 320 行) | 加 `run.build-tags: [integration]`;sqlc 排除規則收窄成三個真正產生的檔案;`hugeParam` 門檻 80 → 192 並寫明理由 |
| Go/web 規則 | `.claude/rules/`(22 選 15) | 原封。沒帶的 7 個是 Genkit / NATS / Ristretto / gRPC 材料 |
| 寫入時守衛 | `.claude/hooks/`(19 選 6) | `check-generated-code.sh` 收窄(原本會擋掉 goen 自己手寫的測試),並加上 `*_templ.go` 與 vendored CSS |
| 權限設定 | `.claude/settings.json` | goen 的工具鏈;`.env` 與金鑰檔拒讀 |

### 依規則修正的程式碼

1. **移除兩個為測試而生的介面**(`Recorder`、`Subscriber`)。`interfaces.md` 明說
   「測試永遠不是引入介面的理由」。它們背後是手寫的資料庫假物件 —— 而該假物件不強制
   任何一條真實寫入會遇到的約束。handler 測試已改為 testcontainers 整合測試。
2. **Store 契約**:建構子改收 `db.DBTX`(非 `*pgxpool.Pool`),加 `WithTx`,
   把 SQLSTATE 對應成 feature 層的哨兵錯誤。
3. **sqlc overrides**:`uuid` → `uuid.UUID`、`timestamptz` → `time.Time`,
   並修正 initialism(`Sku` → `SKU`、`Ip` → `IP`)。
4. **HTTP 安全**:`http.MaxBytesReader`(64 KiB)、`MaxHeaderBytes`、
   `/healthz` 與 `/readyz`(分開:liveness 不檢查資料庫)、request ID middleware。
5. **輸入驗證**:拒絕控制字元(換行走私到姓名/主旨/訂單編號會偽造支援佇列與通知信的第二行)。
6. **Schema**:補上三個缺失的外鍵(`inventory_reservations.order_id`、
   `checkout_attempts.order_id`、`store_credit_entries.order_id`)。實測確認修正前
   可以寫入指向不存在訂單的孤兒保留。

### 修掉三個「會說謊的閘門」

這三個是我自己寫的,而且每一個都通過了它宣稱要防的情境:

1. `sqlc-check` **就地重生成後才 diff** —— 它做的正是自己註解說要避免的事:報告一次
   陳舊,然後把樹修好,第二次就綠了。現已改成唯讀,並實測「連續兩次都失敗」。
2. `integration-build-check` 用 `go test -run='^$'` 宣稱「只編譯不執行」——
   `TestMain` 照跑,會啟動容器。改成 `go vet -tags=integration`。
3. `templ-check` 與 `gen` 用了**不同的呼叫方式**(根目錄 vs `-path internal/ui`),
   兩者產出不同的 `FileName` 字串,所以 `make verify` 會被自己上一次的輸出弄紅。
   實測:連跑三次都 exit 0 才算修好。

### 其他

- CI(`.github/workflows/verify.yml`)三個 job:verify / schema(Docker)/ govulncheck,
  actions 全部釘 SHA
- Dependabot
- `make vuln`、`make verify-all`、`make templ-check`
- 所有外部工具改用 `go run pkg@version` 釘版本(sqlc / migrate / govulncheck / ko),
  不進 module graph
- ko 基底映像改用 digest 釘死(原本 `:latest` 與「可重現建置」的宣稱矛盾);映像加上 tag
  (原本每次都覆寫 `:latest`,沒有東西可以回滾)
- `git init` + 首次提交

---

## 請審查

**請以「這裡哪個宣稱是假的」的角度審。** 上一輪最有價值的發現就是「機器驗證的前提不成立」。

### 1. 匯入是否忠實

- `.golangci.yml` 我做了三處偏離 go-spec,每處都寫了理由。這些理由站得住嗎?
  特別是 `hugeParam` 從 80 拉到 192 —— 這是誠實的調整,還是為了讓它變綠?
- `.claude/hooks/check-generated-code.sh` 我收窄了 sqlc 的封鎖範圍。收窄後有沒有
  漏掉真的該擋的路徑?
- 沒帶進來的 7 個 rules 與 13 個 hooks,有沒有哪個其實適用而我判斷錯了?

### 2. 那三個「會說謊的閘門」是否真的修好

不要相信我說修好了。請具體檢查:

- `sqlc-check`:它現在真的是唯讀嗎?失敗後樹有沒有被動過?
- `integration-build-check`:`go vet -tags=integration` 真的會 type-check 測試檔嗎?
  它抓得到只在整合測試裡才出現的型別錯誤嗎?
- `templ-check`:除了 `-path` 之外,還有沒有別的方式會讓產生檔漂移?
- **還有沒有其他 Makefile target 有同類問題**(宣稱做 A 實際做 B)?

### 3. 移除介面之後

handler 現在直接持有 `*Store`。請檢查:

- 這是否讓某些測試變得無法撰寫,而我因此少測了什麼?
- `contact` 的 handler 整合測試現在會啟動容器 —— 對 `newsletter` 和 `site` 我**沒有**
  寫等價的測試。這是不是一個缺口?
- `health.Pinger` 是我新增的介面。它符合 `interfaces.md` 的「跨套件消費者」例外嗎,
  還是我又犯了同一個錯?

### 4. HTTP 與安全

- `MaxFormBytes = 64 KiB` 這個數字合理嗎?
- request ID 的 `validRequestID` 接受 `[0-9A-Za-z-]{1,64}`。有沒有繞過方式?
  回顯客戶端提供的值到 response header 有沒有風險?
- `/readyz` 檢查資料庫、`/healthz` 不檢查。這個切分在滾動部署時真的正確嗎?
- 控制字元的檢查:`hasControlChars` 放行 `\t`,body 額外放行 `\n\r`。有沒有漏掉的
  危險字元(例如 Unicode 方向控制字元 U+202E、零寬字元)?

### 5. 資料層

- Store 的錯誤對應:`23505` → `ErrDuplicate`、`23514`/`23503` → 包裝後回傳。
  這個分類對嗎?有沒有該對應而沒對應的 SQLSTATE?
- `WithTx` 目前沒有任何呼叫者。`interfaces.md` 說不要為「未來彈性」而設計 ——
  這條規則(`database.md` 要求 WithTx)和那條規則是不是衝突了?哪一條該贏?
- sqlc 的 `uuid.UUID` override 引入了 `github.com/google/uuid` 相依。
  有沒有更好的做法?

### 6. 上一輪處置的複審

`docs/reviews/01-schema-response.md` 逐條記錄了第一輪的處置。請抽查:

- 「CHECK 刪除實驗:100 個中 0 個能不被察覺地刪除」—— 這個宣稱可信嗎?驗證方式有沒有漏洞?
- 「並行守衛 mutation:4/4 種紅」—— 其中一種(只拿掉庫存的條件式)我標記為
  「預期逃逸,因為 CHECK 是第二道防線」。這個解釋成立嗎?
- 有沒有哪一項我宣稱處置了但其實沒有?

## 不需要評論

- 命名風格、註解長度、繁體中文用詞
- 尚未實作的功能(首頁、商品、購物車、結帳、後台 —— 這些是後續批次)
- 「應該用 X 框架」這類偏好,除非能說出具體會出事的場景

## 輸出格式

```
[嚴重度: 會壞資料 / 會出錯 / 會變慢 / 宣稱不實 / 可改進]
問題:一句話
證據:檔案與行號 + 一個具體失敗情境(什麼輸入 → 什麼結果)
建議:具體改法
```

每一節(1–6)都請明確交代結論,即使是「檢查過,沒有發現問題」。

## 現況數據

| | |
|---|---|
| golangci-lint | 320 行設定,`./...` 含 integration tag **0 findings** |
| squawk | 0 issues |
| `make verify` | PASS,連跑三次都 exit 0 |
| `make test-integration` | PASS,5 個套件 |
| `make vuln` | No vulnerabilities found |
| 資料表 / CHECK / 外鍵 / 唯一索引 / 規則 trigger | 52 / 130 / 58 / 43 / 17 |
| 整合測試子測試 | 378 |
