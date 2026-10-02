# goen

[English](README.md) | 繁體中文

[![verify](https://github.com/Koopa0/goen/actions/workflows/verify.yml/badge.svg)](https://github.com/Koopa0/goen/actions/workflows/verify.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/Koopa0/goen)](go.mod)
[![Apache-2.0](https://img.shields.io/badge/Apache--2.0-blue)](LICENSE)

goen 是用 Go 寫的網路商店：前台給買東西的人，後台給經營商店的人。這是參考專案，不是可以真的購物的商店。

示範站 [goen.koopa0.dev](https://goen.koopa0.dev) 以 Stripe 測試模式收款：卡號請用 **4242 4242 4242 4242**，到期日填任何未來的日期，安全碼隨意。不會有真的款項進出。

![繁體中文店面：商品分類與推薦商品](assets/readme/storefront.zh-TW.png)

## 做法

- **伺服器產生 HTML**：頁面是 templ 元件，每個寫入動作都是一般的表單，關掉 JavaScript 也能送出。htmx（專案內附的單一檔案）只改變回傳的內容，不影響寫入是否發生。不需要建置 JavaScript 或 CSS。
- **標準函式庫優先**：路由用 `net/http`。查詢是手寫的 SQL，由 sqlc 編譯成 Go，透過 pgx 執行。沒有 web 框架，也沒有 ORM。靜態檔案嵌入執行檔，網址附上檔案內容的雜湊值。
- **資料庫守住金流與庫存**：前台與後台的請求各用自己的連線池，各以自己的 PostgreSQL 角色連線。兩個角色都不能直接寫入付款、退款、庫存或帳本；這些資料只能在 `SECURITY DEFINER` 函式裡變動，由函式自己檢查規則。
- **付款由 Stripe 說了算**：結帳用 Stripe 代管的付款頁。訂單要等簽章驗證過的 webhook 才算付款完成，瀏覽器跳轉回來不算，因為任何人都能開那個網址。付款頁的有效期限和庫存保留同時結束。
- **對外動作先寫進資料庫**：信件和它通知的事實在同一個交易裡寫進 outbox，保證至少送出一次。電子發票的請求在送出前就先存下來。
- **金額與時間各由一個套件負責**：金額一律是整數（以分為單位），由 `internal/money` 輸出，網頁和信件不會各說各話。時間一律透過 `internal/shoptime` 以店家時區 Asia/Taipei 顯示，因為正式環境的映像檔沒有設定時區；在別處格式化時間，測試會擋下來。
- **兩種語言，集中一處**：goen 自己說的每一句話都在 `internal/i18n` 宣告一次，繁體中文與英文寫在一起；缺了任何一種語言，程式啟動時就會停下。商品文案照店家寫的原文顯示。
- **依台灣的法規與習慣**：依消保法第 19 條，七天鑑賞期從收到商品的隔天起算，退貨照此處理。統一發票透過綠界開立。超商取貨在綠界的電子地圖上選門市。
- **schema 只有一個檔案**：`migrations/001` 就是完整的 schema。還沒有任何真實店家資料前，直接修改這個檔；第一次正式上線後即凍結，之後的每項變更都寫成新的 migration。

## 在本機執行

需要 Go 1.27、Docker 與 `psql`。

```sh
cp .env.example .env
make db-up        # PostgreSQL 跑在 127.0.0.1:5433
make migrate-up
make db-seed      # 四十件商品的示範型錄
make run          # http://127.0.0.1:9700
```

直接使用複製來的 `.env` 時，信件只寫進 log、不會寄出；Stripe、綠界與 Google 登入都不會啟用。各自需要的設定列在 `.env.example`。測試與參與開發的方式見 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 限制

示範站不是真的商店：型錄是示範資料，只收測試卡，也不會出貨。goen 的設計是單一程序連一個 PostgreSQL，請求頻率限制只存在記憶體裡。

## 授權

[Apache License 2.0](LICENSE)。安全性問題請依 [SECURITY.md](.github/SECURITY.md) 回報。
