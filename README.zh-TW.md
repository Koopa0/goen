# goen

[English](README.md) | 繁體中文

[![verify](https://github.com/Koopa0/goen/actions/workflows/verify.yml/badge.svg)](https://github.com/Koopa0/goen/actions/workflows/verify.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/Koopa0/goen)](go.mod)
[![Apache-2.0](https://img.shields.io/badge/Apache--2.0-blue)](LICENSE)

goen 是用 Go 打造的完整電商專案，包含前台與後台。

示範站：[goen.koopa0.dev](https://goen.koopa0.dev)

![繁體中文店面：商品分類與推薦商品](assets/readme/storefront.zh-TW.png)

## 特色

- 以 Go 標準函式庫為核心，搭配 templ 與 htmx 完成整個介面。
- 金流串接 Stripe，電子發票與超商取貨串接綠界。
- 繁體中文與英文雙語。
- 內建台灣的七天鑑賞期與電子發票規範。

## 試用示範站

用登入頁上的示範帳號登入，付款時使用 Stripe 測試卡 4242 4242 4242 4242。

## 在本機執行

需要 Go 1.27、Docker 與 `psql`。

```sh
cp .env.example .env
make db-up
make migrate-up
make db-seed
make run          # http://127.0.0.1:9700
```

測試與設定請見 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 授權

[Apache License 2.0](LICENSE)。安全性問題請依 [SECURITY.md](.github/SECURITY.md) 回報。
