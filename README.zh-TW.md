# goen

[English](README.md)

goen 是給台灣 3C 選品店用的一支 Go 程式：店面、會員與後台同一支 binary，資料放在
PostgreSQL，刷卡走 Stripe，統一發票走綠界。這是示範與參考專案，不是代管服務。

![goen 店面首頁（繁體中文）：導覽、主視覺、分類與推薦商品](assets/readme/storefront.png)

## 跑起來

需要 Go 1.27、Docker 與 `psql`。

```sh
cp .env.example .env
make db-up        # Docker 裡的 PostgreSQL，127.0.0.1:5433
make migrate-up
make db-seed      # 沒有目錄時，店面是空的，而且是對的
make run          # http://127.0.0.1:9700
```

Stripe 是選用的。沒有金鑰時店面仍可跑；結帳會到付款頁，頁上會說明金流尚未啟用。

## 守住的事

四條界線寫在樹上，不是靠約定：

- **每一次寫入都是普通表單。** 寫入是 `<form method="post">`，以 `303 See Other`
  回答，重新整理不會重送。驗證拒絕時以 `422` 把送出的值原樣畫回去；已登入的問與答
  送出仍會重導並丟掉草稿
  （[#154](https://github.com/Koopa0/goen/issues/154)）。htmx 只改回來的
  片段，不改寫入是否發生。
- **能交給資料庫的，就交給資料庫。** 316 個 `CHECK` 約束、90 條外鍵、75 個唯一
  索引與 46 個規則觸發器；金錢與庫存只能經由應用角色可執行、不能繞過的
  `SECURITY DEFINER` 函式寫入。一致性套件從系統目錄推導該覆蓋什麼，少測一條
  約束就過不了建置。
- **介面跟著讀者；內容跟著店。** 導覽、標籤與驗證有譯文。商品文案只在店家真的
  寫了譯文時才翻譯。
- **設計系統是上游的。** `assets/css/ds/` 從上游帶入；頁面組合只寫在
  `assets/css/app/app.css`。

頁面是伺服器渲染的 HTML。[htmx](https://htmx.org) 與一小段原生
`assets/js/goen.js` 負責加強。沒有前端框架、沒有 Node 執行環境，也沒有
JavaScript 建置流程。

## 現況

本機可以瀏覽、比較、購物車、以訪客或會員結帳、打開付款頁、申請退貨、登錄保固，
以及在第二因素之後使用 `/admin`。

刷卡請款、統一發票開立與外寄信件已對接對應的服務商，但尚未在真實的 Stripe、
綠界或 SMTP 端點上驗收
（[#40](https://github.com/Koopa0/goen/issues/40)）。

政策文案寫了 7 天與 14 天的退貨期限；退貨審核路徑目前並未強制執行
（[#50](https://github.com/Koopa0/goen/issues/50)）。

可觀測性、延後付款方式，以及短 CJK 查詢的搜尋投影都還沒做。那是選擇，不是
遺漏。

## 架構

依功能分包。`cmd/goen` 負責接線；`internal/<feature>` 把該功能的型別、處理器、
查詢與測試放在一起。沒有 `services`、`repositories` 或 `models` 目錄。

[CONTRIBUTING.md](CONTRIBUTING.md) 寫了四條界線、閘門，以及如何改產生出來的
`internal/db` 與 `*_templ.go`。

## 驗證

```sh
make verify       # 格式、產生、vet、lint、建置與 race 測試
make verify-all   # verify，加上資料庫套件與 govulncheck
make test-integration
make check-layout # 真實瀏覽器裡的版面與無障礙
```

`make verify` 需要 `PATH` 上有 `golangci-lint` 與 `squawk`；其餘工具由
`Makefile` 釘版本，用 `go run` 取用。整合套件需要 Docker。`make check-layout`
需要 Chrome 與已啟動的伺服器。

## 設定

設定只來自環境變數；[`.env.example`](.env.example) 列了每一個。
`GOEN_DATABASE_URL` 必填，沒有預設值。

四種組合會拒絕啟動，因為另一個選擇比不跑更糟：

| 設定 | 拒絕時機 |
| --- | --- |
| `GOEN_STRIPE_API_KEY` | 設了卻沒有 `GOEN_STRIPE_WEBHOOK_SECRET` — 錢走過一個沒人驗證的端點 |
| `GOEN_TOTP_KEY` | 不是剛好 32 位元組的 hex 或 base64 — 這是金鑰，不是口令 |
| `GOEN_SMTP_ADDR` | cookie 已是 Secure 卻是空的 — 重設信會被標成已寄、卻走不出去 |
| `GOEN_BASE_URL` | 正式姿態下不是根路徑的 HTTPS 來源 — 明文重設連結會漏出 token |

## 貢獻

歡迎 issue 與 pull request。貢獻說明在 [CONTRIBUTING.md](CONTRIBUTING.md)。
安全性問題走
[私下諮詢表單](https://github.com/Koopa0/goen/security/advisories/new)，
不要開公開 issue。見 [SECURITY.md](.github/SECURITY.md)。

## 授權

Apache License 2.0。見 [LICENSE](LICENSE)。

## 免責

這是示範與參考專案。與任何公司無關，也不是正式支援的產品。
