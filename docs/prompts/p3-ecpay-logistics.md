# P3 — 綠界 (ECPay) and Taiwan convenience-store logistics: decide, do not assume

> Self-contained brief. Assume no context beyond this file and the repository.
> **For:** Codex · **Order:** any time.

`goen` is a Traditional-Chinese storefront — one Go binary, `net/http`, `templ`
server-rendered HTML, PostgreSQL 18 via pgx + sqlc — that today takes money through
**Stripe hosted Checkout** and collects a 超商取貨 destination as **typed text** (brand,
店號, 店名 — digits-only store code, because that is what all four chains have in
common). It does not integrate any Taiwanese provider. Read `CLAUDE.md` at the
repository root for why: the 電子地圖 picker each chain offers needs credentials the
project does not have, and the decision recorded is to collect the destination honestly
rather than fake the picker. The same reasoning left 統一發票 issuing unbuilt, because a
real one goes through a 加值中心.

**Your job is a recommendation with evidence, not an implementation.**

## Research and answer

1. **What 綠界 (ECPay) actually offers** that Stripe does not, for a Taiwanese shop:
   payment methods (ATM 虛擬帳號, 超商代碼繳費, 超商條碼, 分期), 電子發票 issuing, and
   物流 (超商取貨 with and without 代收貨款). Which of these are separate products,
   separate contracts, separate credentials?
2. **The 超商取貨 integration specifically.** What does the real 電子地圖 flow look like
   — the redirect, the callback, what the shop stores, what the customer sees? How do
   the four chains (7-11, 全家, 萊爾富, OK) differ? What does it require of goen's
   checkout, which is deliberately a set of LINKS carrying state in the URL and works
   with scripting off?
3. **代收貨款 (COD)** — what it changes about an order's money model. goen's schema
   posts money through `SECURITY DEFINER` functions and has a strict
   `order_amount_owed` definition; a COD order is funded on delivery by a third party.
   Sketch what that does to `orders_funded_to_leave_pending` and the payment guards.
4. **電子發票 through 綠界 vs a 加值中心 directly.** goen already collects the 發票
   preference (會員載具 / 手機條碼載具 / 公司統編) into `invoice_preferences` and has
   `invoice_documents` waiting. What is the smallest honest path to actually issuing one?
5. **Sandbox reality.** For each of the above: can it be exercised in a test environment
   without a Taiwanese company registration and a signed contract? Be specific — this
   is a portfolio project, and an integration nobody can run is worth less than one
   they can.

Then recommend one of: (a) integrate ECPay payments, (b) integrate ECPay logistics
only, (c) integrate 電子發票 only, (d) keep Stripe and the typed 門市, and say what the
project loses. Give the module-graph cost of each (`go list -m all | wc -l` before and
after is this project's standard), and the amount of new state each puts in the schema.

Cite official documentation with URLs. Where the documentation is Chinese-only, say so.

## Report

Recommendation first, evidence after.

Then end with the same three lists every brief in this set ends with:

1. **Verified by running** — executed, result observed.
2. **Read but could not verify** — a view formed from the code or the documentation,
   without running it.
3. **Did not look at** — in scope on paper, not examined.
