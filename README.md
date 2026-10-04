<h1><img src="assets/brand/favicon.svg" alt="" width="32" height="32"> goen</h1>

English | [繁體中文](README.zh-TW.md)

[![verify](https://github.com/Koopa0/goen/actions/workflows/verify.yml/badge.svg)](https://github.com/Koopa0/goen/actions/workflows/verify.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/Koopa0/goen)](go.mod)
[![Apache-2.0](https://img.shields.io/badge/Apache--2.0-blue)](LICENSE)

A complete e-commerce shop built with Go — from browsing to checkout, after-sales and the back office that runs it.

**Live demo: [goen.koopa0.dev](https://goen.koopa0.dev)**

![English storefront with product categories and recommended products](assets/readme/storefront.en.png)

## Features

**Shopping**
- Departments and categories with filters, sorting and search
- Product variants, reviews, questions and side-by-side comparison
- Wishlist, and a store in Traditional Chinese and English

**Checkout**
- Guest or member checkout, home delivery or convenience-store pickup
- Card payments through Stripe; coupons and store credit
- E-invoices issued automatically through ECPay

**After the order**
- Order tracking, returns within the seven-day period, warranty registration
- Reward points that turn into store credit

**Back office**
- Orders, fulfilment, returns and refunds
- Products, stock, pricing, campaigns and the home page
- Customers, reviews and questions; staff sign-in with two-factor codes and an audit log

## Built with

Go · templ · htmx · PostgreSQL · Stripe · ECPay

How it holds together — database roles, the rules the database enforces and the checks behind them: [ARCHITECTURE.md](ARCHITECTURE.md)

## Try the demo

Sign in with the demo account shown on the sign-in page, and pay with Stripe's test card 4242 4242 4242 4242.

## Run it locally

You need Go 1.27, Docker and `psql`.

```sh
cp .env.example .env
make db-up
make migrate-up
make db-seed
make run          # http://127.0.0.1:9700
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for tests and configuration.

## License

[Apache License 2.0](LICENSE). Security reports: [SECURITY.md](.github/SECURITY.md).
