# parse-helpers-collapse-states

**Verdict** CONFIRMED · **Severity** high · **Origin** found in this round's own sweep (already established)

**Files** internal/admin/handler.go, internal/admin/admin.go, internal/admin/product.go, internal/admin/shipping.go, internal/i18n/chrome_admin_forms.go, internal/ui/pages/adminproduct.go, internal/ui/pages/adminproduct.templ, internal/ui/pages/adminshipping.go, internal/ui/pages/adminshipping.templ, internal/admin/admin_test.go, internal/admin/integration_test.go


## Root cause

A parser whose return type cannot express the distinction its callers depend on. `parseSafetyStock` maps three different inputs — absent, an intentional zero, and an unreadable or out-of-range string — onto one value, and the caller then reads that value as a decision ("no term stated", "unmeasured", "no limit"). Every validator downstream is asking about the parsed number, so by the time `Validate` runs the evidence that a person typed something has already been thrown away.

The repository already has the right shape three functions away — `admin.ParseAdjustment`, `admin.ParseReceipt` and `admin.ParsePrice` (`internal/admin/admin.go:123-153`) all return `(T, bool)`, and `ParsePrice` even distinguishes blank from invalid explicitly. The defect is that the number-with-a-meaningful-zero fields were given the older single-return helper and nobody re-read it when the parcel and warranty columns were added on top of it.

Second cause, same helper: one hard-coded ceiling (1,000,000) standing in for four different CHECKs, so the parser is simultaneously too permissive (a 500 on a value the schema refuses) and too silent (a zero on a value it does not).


## Reproduction — the evidence this rests on

EXECUTED (temporary Go test in package `admin`, run, then deleted; `git status --porcelain` empty at finish).

Cited lines still say what the summary claims. `internal/admin/handler.go:914-922`:

    // parseSafetyStock reads the safety-stock field, returning int32 directly
    // because gosec cannot see that a caller-side ceiling makes the conversion safe.
    func parseSafetyStock(s string) int32 {
        n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
        if err != nil || n < 0 || n > 1_000_000 {
            return 0
        }
        return int32(n)
    }

Nine call sites confirmed: `handler.go:840` (`safety`), `:843-845` (`parcel_longest`, `parcel_sum`, `parcel_weight`, under the comment "Zero is UNMEASURED and stores NULL, so a blank field leaves the variant refused by no shipping method"), `:874` (`warranty_months`, in `productFormOf`), `:1235-1237` (`max_parcel_longest`, `max_parcel_sum`, `max_parcel_weight`, under "Zero is 'no stated limit', the honest default for home delivery").

Probe output (file `internal/admin/zz_probe_test.go`, since deleted):

    parseSafetyStock("")        = 0 ; warranty_months refused = false
    parseSafetyStock("12o")     = 0 ; warranty_months refused = false
    parseSafetyStock("-3")      = 0 ; warranty_months refused = false
    parseSafetyStock("2 4")     = 0 ; warranty_months refused = false
    parseSafetyStock("١٢")      = 0 ; warranty_months refused = false
    parseSafetyStock("24.0")    = 0 ; warranty_months refused = false
    parseSafetyStock("1000001") = 0 ; warranty_months refused = false
    variant parcel = 0/0/0 errs=map[]          (inputs "45o", "-1", "10,000")
    over-ceiling longest 6000 -> 6000, errs=map[]

So a typo stores the UNSTATED state and no validator objects: `ProductForm.Validate` (`internal/admin/product.go:84-86`) only refuses `<0 || >120`, and `VariantForm.Validate` (`:123-125`) checks only `SafetyStock < 0` — the three parcel fields are never validated at all. The queries then turn 0 into NULL: `internal/admin/query.sql:444` / `:458` (`nullif(@warranty_months::integer, 0)`), `:485-487`, `:1328-1330`.

Consequences as stated hold. `products.warranty_months IS NULL` → `internal/warranty/store.go:54` `HasTerm: r.WarrantyMonths.Valid` false and `internal/ui/pages/product.go:172` `HasWarranty()` false, so registration is refused. `product_variants.parcel_*_mm IS NULL` → `internal/cart/query.sql:103-108` (`ShippingChoices`) excludes a method only when BOTH sides are non-NULL, so an unmeasured oversized item keeps 超商取貨.

TWO THINGS THE SUMMARY DOES NOT SAY, both in scope for the fix:

1. The parser's ceiling (1,000,000) is ABOVE the schema's for four of the columns, so the same helper also produces the opposite failure — a 500 instead of a 422. `migrations/001_initial_schema.up.sql:347/349/351` bound `parcel_longest_mm` at 5000, `parcel_sum_mm` at 15000, `parcel_weight_g` at 200000, and `:353-354` adds `parcel_sum_mm >= parcel_longest_mm`. None is checked in Go, so `6000` in the longest-side box reaches `CreateVariant`, trips `product_variants_parcel_longest_sane`, and `AddVariant` (`internal/admin/product.go:367-377`) maps any PgError other than `product_variants_sku_key` to `ErrRefused` → `handler.go:854` → `h.serverError` → 500, with the whole form discarded.
2. The variant form and the method form cannot show a field refusal even if one were produced. `internal/ui/pages/adminproduct.templ:517-534` renders `safety`/`parcel_*` with no `value=`, no `aria-invalid`, no error paragraph (only `options` at :511-513 has one), and `editProductWithErrors` (`handler.go:1545-1557`) rebuilds the view from the store, so everything typed is lost. `pages.AdminMethodDraft` (`internal/ui/pages/adminshipping.go:20-26`) carries `Fee`/`FreeOver` but not the three ceilings, and `adminshipping.templ:156-172` renders them with no value and no error block. That is a pre-existing write-face-rule violation on the same controls; the fix is not visible without closing it.

Not a recorded decision: CLAUDE.md and docs/roadmap.md contain nothing exempting these parsers. CLAUDE.md states the opposite requirement — "NULL is UNMEASURED and not unlimited… A method may only be refused on a figure that EXISTS", and "A rejected form re-renders at `422` with the submitted values intact and `aria-invalid` on each control it refused."


## Blast radius

Nine form fields on two back-office pages, silently. Every failure mode is invisible to the person who caused it, because the form answers 303 and the page reloads looking correct.

- warranty_months (`/admin/products/{slug}`): a typo (`12o`, `24.0`, a full-width digit, a stray space) stores NULL. Every PDP for that product drops the warranty line, `/account/warranty/{number}` refuses registration, and `/compare` prints the no-cover cell. The shop believes it stated 24 months. Frequency: once per mistyped product, permanent until somebody notices the PDP.
- parcel_longest/sum/weight (`/admin/products/{slug}` variant form): a typo stores NULL = unmeasured, and `ShippingChoices` then offers every method. A 27-inch monitor becomes selectable for 超商取貨: the customer pays, the parcel is packed, and the refusal happens at the counter with the customer waiting — exactly the event those columns were added to prevent. Also the reverse: `6000` is a 500 that discards the form.
- max_parcel_longest/sum/weight (`/admin/shipping`): a typo stores NULL = no stated limit, so a newly created 超商取貨 method accepts every cart forever. This is worse than the variant case because it is one row governing the whole channel, and `shipping_method_versions` is append-only — the ceiling lives on `shipping_methods`, so it can be corrected, but nothing surfaces that it is wrong.
- safety_stock: a typo stores 0, so `products_in_stock_idx` / the "in stock" predicate (`stock_quantity > safety_stock`) sells into the buffer the shop meant to keep.

None of it raises, logs, or shows a badge. The only signal is a customer or a counter clerk.


## Fix

GOAL: absent, zero and invalid become three distinguishable outcomes for all nine fields, an invalid entry surfaces as a 422 with the typed text still on the control, and a value the CHECK would refuse becomes a field message rather than a 500.

ONE GENERIC PAIR, NOT NINE NAMED PARSERS — but the BOUND is a named constant at the call site, next to the CHECK it mirrors. Nine named parsers would be nine copies of one arithmetic, which is what `store_credit_balances` and `localized_name` were consolidated to stop; a bare literal at the call site is what let 1,000,000 stand in for four different ceilings. Two functions and not one with a flag, because blank means two different things and a boolean parameter is the configuration-flag shape mistake #30 says to close.

1. `internal/admin/admin.go`, beside `ParseAdjustment`/`ParseReceipt`/`ParsePrice`:

    // ParseOptionalCount reads a whole number a form may leave blank. Blank —
    // and a typed zero, which the queries nullif to NULL — is UNSTATED and
    // returns (0, true). Anything the column's CHECK would refuse returns
    // (0, false), so the form says so in the shop's own words instead of
    // storing "unmeasured" or reaching PostgreSQL as a 500.
    func ParseOptionalCount(s, max) (int32, bool)   // "" or "0" -> (0,true); 1..max -> (n,true); else (0,false)

    // ParseCount reads a whole number whose zero is a real value —
    // product_variants.safety_stock is NOT NULL DEFAULT 0.
    func ParseCount(s, max) (int32, bool)           // "" -> (0,true); 0..max -> (n,true); else (0,false)

   Both trim, both parse with `strconv.ParseInt(_, 10, 32)`. Keep `parseSafetyStock` DELETED, not wrapped: a wrapper leaves the collapsing call available to the next caller.

2. Ceilings, mirroring the CHECKs by name. In `internal/admin/product.go`, in the const block that already holds `MaxWarrantyMonths = 120 // mirrors products_warranty_months_sane`:

    ParcelLongestCeilingMM = 5000    // product_variants_parcel_longest_sane
    ParcelSumCeilingMM     = 15000   // product_variants_parcel_sum_sane
    ParcelWeightCeilingG   = 200000  // product_variants_parcel_weight_sane
    SafetyStockCeiling     = 1_000_000

   Constant names deliberately differ from the `NewMethod.MaxParcel*` FIELD names. `internal/admin/shipping.go` reuses the same three constants for the method ceilings: the schema bounds those only at `> 0` (`migrations/001:1326-1330`), but a method ceiling is compared against a variant measurement bounded at 5000/15000/200000, so a larger ceiling is unreachable and is always a typo — say that in a comment beside the call.

3. Per-field wiring. Every parse refusal keys the errs map on the input's own `name`, matching the convention already on both pages.

   a. `handler.go:836-861` (`AddVariant`): parse into locals, build `parseErrs map[string]string` first, and if it is non-empty re-render at 422 WITHOUT touching the store.
      - `safety` → `ParseCount(v, SafetyStockCeiling)` → key `safety`, message `i18n.KeyFormSafetyStock` (widen that message, step 5).
      - `parcel_longest` → `ParseOptionalCount(v, ParcelLongestCeilingMM)` → key `parcel_longest`
      - `parcel_sum` → `ParseOptionalCount(v, ParcelSumCeilingMM)` → key `parcel_sum`
      - `parcel_weight` → `ParseOptionalCount(v, ParcelWeightCeilingG)` → key `parcel_weight`
      Add to `VariantForm.Validate` (`product.go:107-126`) the cross-field CHECK the schema carries and Go does not: if both `ParcelSumMM` and `ParcelLongestMM` are non-zero and `ParcelSumMM < ParcelLongestMM`, `errs["parcel_sum"] = KeyFormParcelSumShort`. Otherwise `product_variants_parcel_sum_at_least_longest` is still a 500.

   b. `handler.go:869-877` (`productFormOf`): change the signature to `productFormOf(r *http.Request) (*ProductForm, map[string]string)`; `warranty_months` → `ParseOptionalCount(v, MaxWarrantyMonths)` → key `warranty_months`. Both callers (CreateProduct, UpdateProduct) merge the returned map into the store's `errs` before the `switch`. Add `WarrantyMonthsRaw string` to `ProductForm`, always set to the raw `r.PostFormValue("warranty_months")`.

   c. `handler.go:1225-1259` (`CreateShippingMethod`): `max_parcel_longest`/`max_parcel_sum`/`max_parcel_weight` → `ParseOptionalCount` with the same three ceilings → keys `max_parcel_longest`, `max_parcel_sum`, `max_parcel_weight`, merged into `errs` before `CreateMethod` is called.

4. Where the refusal surfaces. The controls cannot show one today, so this half is required, not cosmetic.
   - `internal/ui/pages/adminproduct.go`: add `WarrantyMonthsRaw string` to `AdminProductView`, and make `WarrantyMonthsText()` (`:152-157`) return it when non-empty, else the existing formatted number. Add `VariantDraft AdminVariantDraft` with `SKU, Price, Compare, Safety, ParcelLongest, ParcelSum, ParcelWeight string`, mirroring `AdminMethodDraft` (`adminshipping.go:20-26`).
   - `handler.go:1545-1557` (`editProductWithErrors`): take the draft as a parameter and assign `view.VariantDraft`; `rejectProduct` (`:884-903`) assigns `view.WarrantyMonthsRaw = f.WarrantyMonthsRaw`. Every new field must be assigned somewhere non-test or `TestEveryViewModelFieldIsAssigned` (`internal/ui/pages/fields_test.go:19`) fails — that guard is the reason the draft must be filled where the view is BUILT.
   - `internal/ui/pages/adminproduct.templ:481-540`: give each of `v-sku`, `v-price`, `v-compare`, `v-safety`, `v-longest`, `v-sum`, `v-weight` a `value={ v.VariantDraft.X }` and the exact three-part pattern the warranty field already uses at `:221-243` — `if v.HasErr("<key>") { aria-invalid="true" aria-describedby="<id>-error" }` plus `<p class="ui-error-text" id="<id>-error">{ v.Err("<key>") }</p>`. Error ids: `v-safety-error`, `v-longest-error`, `v-sum-error`, `v-weight-error` (and `v-sku-error`, `v-price-error`, `v-compare-error`, which the store already keys and nothing renders).
   - `internal/ui/pages/adminshipping.go`: add `MaxLongest, MaxSum, MaxWeight string` to `AdminMethodDraft`; fill them in `rejectShippingForm`'s caller (`handler.go:1250-1256`) from the raw `PostFormValue`s. `adminshipping.templ:156-172`: same three-part pattern, ids `m-max-longest-error`, `m-max-sum-error`, `m-max-weight-error`.
   - Run `templ generate` (never hand-edit `*_templ.go`).

5. i18n — a key and both translations are ONE `key()` declaration, in `internal/i18n/chrome_admin_forms.go`.
   - REUSE `KeyFormWarrantyMonths` for the parse refusal: its text already reads 「保固月數請填 1 到 120,或留空表示未提供保固。」 / "A warranty term is 1 to 120 months, or blank for no stated cover," which is right for unreadable and out-of-range alike.
   - WIDEN `KeyFormSafetyStock` (`:119`) from "cannot be negative" to 「安全庫存請填 0 以上的整數。」 / "Safety stock is a whole number, zero or more." Same id, new Message.
   - NEW `KeyFormParcelMeasurement = key("form.parcel.measurement", Message{ZhHant: "請填 1 到 %d 的整數,或留空表示尚未量測。", En: "Type a whole number from 1 to %d, or leave it blank for unmeasured."})`, used as `fmt.Sprintf(i18n.T(ctx, ...), ceiling)` so one sentence serves three fields.
   - NEW `KeyFormParcelSumShort` — 「三邊和不能小於最長邊。」 / "The sum of the three sides cannot be less than the longest side."
   - NEW `KeyFormMethodParcelLimit = key("form.method.parcel.limit", …)` — 「上限請填 1 以上的整數,或留空表示不設限。」 / "A limit is a whole number above zero, or blank for no stated limit."
   `exhaustruct` is on for `i18n.Message`, so a missing locale is refused at the line; `TestEveryKeyIsRendered` is satisfied by a Go reference outside `internal/i18n`.

6. Client-side hints only, never the authority: leave `min="0"` on all seven optional number inputs (a typed 0 is defined to mean unstated, exactly as `nullif` already treats it) and add `max=` mirroring each ceiling.

DO NOT: add a `//nolint` for the gosec conversion the old comment mentions — the new bound parameter is already an `int32`-safe ceiling. DO NOT change `internal/db` (sqlc output) or the queries; the `nullif(…, 0)` line is correct and stays. DO NOT touch the schema — the CHECKs stay the authority, the Go check exists only so the refusal reaches the form.

SAME SHAPE, ADJACENT, NOT IN THIS FIX — fix or queue by name (review-process.md: a finding reaches exactly one of fixed/queued/refused):
- `handler.go:906-912 dollarsToCents` — a mistyped `compare` becomes 0, which `VariantForm.Validate:117` reads as "not on sale".
- `handler.go:1010-1016 whole` — coupon `cap`/`min`: a typo silently means no cap / no minimum spend.
- `handler.go:1019-1025 small` — coupon `max`/`days`: a typo silently means UNLIMITED redemptions and NEVER EXPIRES. This is money and is the worst of the three.
- `handler.go:1359-1365 dollars` — shipping `fee`/`free_over`: a typo silently means free delivery. Its own comment ("a blank or unparseable box is zero, which NewMethod.Validate reads per field") states the collapse as if `Validate` could tell them apart; it cannot.
ALREADY CORRECT, for contrast: `admin.ParseAdjustment`/`ParseReceipt`/`ParsePrice` (`admin.go:123-153`), `handler.go:284-296` and `internal/returns/handler.go:65-73` (both skip blank and refuse invalid), `cart.ParseQuantityAllowingZero`. OUT OF SCOPE and defensible: `product.parseRating` (0 is not a legitimate rating and `Validate` refuses it), `cart.ParseQuantity` (documented "the button means add this"), `catalog.ParsePage`/`ParsePrice` (GET filters, nothing is stored), `media/handler.go:47-52` (a 404 by allowlist). BORDERLINE, name it: `internal/warranty/handler.go:88-91` swallows a bad `unit` into 0.


## The lock, and how to see it fail first

Three locks, each proven RED by a mutation that is SEEN to apply (mistake #6, and false-green mode #3 — grep the file after the edit and paste the changed line before running).

LOCK 1 — unit, `internal/admin/admin_test.go` (package `admin`, no Docker, runs in `make verify`).
`TestAnUnreadableNumberIsNotAnUnstatedOne`: table over the two new parsers with the four call-site ceilings. Rows and expected `(value, ok)`:
  ParseOptionalCount(_, MaxWarrantyMonths): ""→(0,true) · "0"→(0,true) · "24"→(24,true) · "12o"→(0,false) · "24.0"→(0,false) · "-3"→(0,false) · "2 4"→(0,false) · "١٢"→(0,false) · "121"→(0,false)
  ParseOptionalCount(_, ParcelLongestCeilingMM): "450"→(450,true) · "6000"→(0,false) · "10,000"→(0,false)
  ParseCount(_, SafetyStockCeiling): ""→(0,true) · "0"→(0,true) · "5"→(5,true) · "-1"→(0,false) · "5o"→(0,false)
The assertion must name BOTH halves — a row asserting only `ok` cannot tell a refusal from a zero, which is the defect itself.
MUTATION: in `ParseOptionalCount`, change the error branch to `return 0, true`. Rows "12o" and "-3" must go RED. Record the failure text.
SECOND MUTATION: raise the ceiling check to `n > 1_000_000`. The "6000" and "121" rows must go RED — this is the half that proves the ceiling is per-field and not one number for four columns.

LOCK 2 — integration, `internal/admin/integration_test.go` (`//go:build integration`).
`TestAMistypedWarrantyTermIsRefusedNotDropped`: create a product with `warranty_months = 24`. POST the edit form through the real handler with `warranty_months=12o` and everything else unchanged. Assert, in this order:
  (a) `w.Code == http.StatusUnprocessableEntity` — a 303 is the defect;
  (b) the body contains `id="p-warranty-months"` carrying `aria-invalid="true"` AND `aria-describedby="p-warranty-months-error"`, and a `<p … id="p-warranty-months-error">` with non-empty text — the write-face rule's own two halves, and mistake #36 is why `aria-describedby` is asserted rather than assumed;
  (c) the body still shows `value="12o"` — the typed text survived;
  (d) `SELECT warranty_months FROM products WHERE slug = $1` is still 24 and NOT NULL.
Assertion (d) is the one that cannot be satisfied by a cosmetic fix, and (a) alone is not enough: a handler that 422s while having already written NULL passes (a).
MUTATION: revert `productFormOf` to ignore the `ok` and pass the parsed 0. (a) and (d) must both go RED. Prove the edit applied by pasting the changed line.

LOCK 3 — integration, `internal/admin/integration_test.go`.
`TestAMistypedParcelDimensionDoesNotBecomeUnmeasured`: this one must assert the CONSEQUENCE, not only the refusal, or it is `TestADeliveredOrderLinksToItsWarrantyForm`'s mistake — an assertion written from the implementation.
  Part 1 (the refusal): POST the variant form with a valid SKU and price and `parcel_weight=10,000`. Assert 422; assert `id="v-weight"` carries `aria-invalid="true"` and `aria-describedby="v-weight-error"` with a rendered message; assert `value="10,000"` is still in the box; assert `SELECT count(*) FROM product_variants WHERE sku = $1` is 0.
  Part 2 (why it matters): create a 超商取貨 method with `max_parcel_weight_g = 10000`, put ONE unit of the SKU from part 1 into a cart, and call `cart.ShippingChoices` (`internal/cart/query.sql:86-109`). With the fix, part 1 created no variant, so instead seed the variant through the fixed handler with `parcel_weight=15000` and assert the pickup method is ABSENT from the choices; then seed a second variant with a blank weight and assert it is PRESENT (NULL is unmeasured, not too big — CLAUDE.md's stated rule, and the half a fix that refuses blank would break).
MUTATION for part 1: make `AddVariant`'s handler ignore the parse `ok` for `parcel_weight`. The count assertion goes RED (a variant appears) and so does the 422.
MUTATION for part 2: with the mutation above still applied, the pickup method reappears in `ShippingChoices` for the oversized item — that is the counter refusal, reproduced in a test. Record both.

A fourth lock is NOT required for the shipping-method ceilings: they run through the same two parsers, and Lock 1's ceiling mutation covers the arithmetic. Do add the rendering half to `check-layout`'s expectations only if a row there already POSTs a refused form — per mistake #26 and #36, a browser check over a state no row reaches measures nothing, so the coverage claim stays in the Go tests.

Gate: `make verify` (twice — mistake #22) and `make test-integration`, and `git status --porcelain` empty.
