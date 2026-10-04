# Architecture

goen is one Go binary serving server-rendered [templ](https://templ.guide) pages over PostgreSQL. htmx makes some interactions smoother; every form is still a plain HTML post. Stripe Checkout takes card payments, and ECPay issues 統一發票 and provides convenience-store pickup.

## Database roles

The storefront and the back office connect as different PostgreSQL roles, `store` and `admin`. A third, `maintenance`, only refreshes the co-purchase table.

Neither role may write `payments`, `refunds` or `invoice_operations` directly. They change them only through `SECURITY DEFINER` functions, each granted to a single role: `capture_payment` to `store`; `record_refund_*`, `claim_invoice_*` and `settle_invoice_*` to `admin`. See the grants in [migrations/001_initial_schema.up.sql](migrations/001_initial_schema.up.sql).

## What the database refuses

- An unknown status: closed sets are `CHECK` constraints, e.g. `orders_fulfillment_status_known`.
- An illegal order transition: the `orders_legal_transition` trigger. `FulfillmentStatus.Next` in [internal/order](internal/order) is the Go copy, and `TestNextIsTheTransitionsTheTriggerAllows` keeps the two equal.
- Negative stock or store credit, and a refund larger than its capture (`product_variants_stock_non_negative`, `store_credit_never_negative`, `refunds_within_capture`).

Concurrent requests are tested against these rules, e.g. `TestTwoOrdersCannotTakeTheSameLastUnit`, `TestStockCannotOversell` and `TestRefundsCannotRacePastCapture`.

## Payments

- A Checkout Session expires with the stock hold behind it ([internal/payment](internal/payment)).
- A webhook is accepted only with a valid signature. Its order comes from the payment row goen wrote when it opened the session, never from the event, and an amount or currency that differs from that row is refused.

## Work that leaves the database

- Mail goes through an outbox ([internal/outbox](internal/outbox)): it is queued in the same transaction as the change it reports, and workers lease and retry it. A message that keeps failing is listed on the back office's health page.
- An e-invoice is handed off through the same outbox, then tracked as an `invoice_operations` row that [internal/invoice](internal/invoice) leases and retries until ECPay's answer is settled.
- A paid order cancelled before shipment corrects its 統一發票: inside ECPay's window the invoice is voided; after it, an allowance is filed for the buyer to confirm online.

## Back-office writes

Back-office writes go through `audit.Run` ([internal/admin/audit](internal/admin/audit)), which records the change and its audit event in one transaction. `audit_events` is append-only: neither role may write it directly, and a trigger refuses updates and deletes, except clearing the actor when that account is erased.

## Without JavaScript, without providers

- `TestEveryFormWorksWithScriptingOff` checks that every form in the templates names an action and a get or post method.
- Each provider may be missing. Without Stripe the payment page says card payment is unavailable; without ECPay the checkout takes no mobile barcode and the back office shows no invoice controls; without the store map the checkout asks for a store chain only.

## Checks

Every pull request and every push to `main` runs [verify.yml](.github/workflows/verify.yml):

- **verify**: formatting, generated-code drift, `squawk` on the migration, `go vet`, `deadcode`, `golangci-lint`, production and integration builds, and unit tests with `-race -shuffle=on`.
- **schema**: integration tests against PostgreSQL with `-race -shuffle=on`, then the migration up, down and up again.
- **layout**: the storefront's and back office's key pages in Chrome at 375, 768, 1024 and 1440 px, with axe-core (WCAG 2.0 A and AA) failing on any new serious or critical violation.
- **vulnerabilities**: `govulncheck`.

CodeQL runs on Go and the workflows in [codeql.yml](.github/workflows/codeql.yml).
