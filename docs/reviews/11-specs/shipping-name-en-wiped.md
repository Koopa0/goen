# shipping-name-en-wiped

**Verdict** CONFIRMED · **Severity** high · **Origin** external report (verified this round)

**Files** internal/admin/shipping.go, internal/ui/pages/fields_test.go, internal/admin/integration_test.go, internal/ui/pages/adminshipping.templ, internal/ui/pages/adminshipping.go, internal/admin/query.sql, internal/admin/handler.go, internal/cart/query.sql, internal/site/query.sql, internal/db/query.sql.go


## Root cause

A form that is the ONLY door to an append-only table must render the current row in full, and this one renders it in part.

`PublishShippingVersion` is an INSERT of a whole new version, never an UPDATE — so every column the form omits is not "left alone", it is set to whatever the blank field says. The template was written correctly for that model: it pre-fills `name`, `carrier`, `fee` and `free_over` from the version in force so an untouched submission republishes the same facts. `name_en` and `carrier_en` were added to the schema, the query, the params struct, the view-model struct and the template — and the one line in internal/admin/shipping.go:40-46 that copies the row into the view model was never extended. Six of seven places learned; the seventh did not, and a Go struct literal with a field left out compiles.

The repository already knows this exact failure mode on this exact table and wrote the counter-measure one query away, in internal/admin/query.sql:951-953: "Without this, publishing a new base fee silently drops every surcharge: the rows key on the VERSION, and the new version has none — so a shop raising its home-delivery fee would start shipping to the outlying islands at that fee." `CarryZoneSurcharges` carries the zone surcharges forward for that reason. The English name and carrier key on the version in exactly the same way and nothing carries them.

The second, worse half is the guard. `TestEveryViewModelFieldIsAssigned` exists because "a zero value is valid, so neither the compiler nor go vet can see it" — it is the lock built after layouts.Page.CartCount and Page.Nav. It caught this defect. It was then taught to pass on it by an allowlist entry that describes the defect ("the store never fills it, so the edit form shows an empty English name") and labels it "REPORTED", a state .claude/rules/review-process.md does not define and no tracker records. That converts a red build into a permanent, self-documenting exemption: the guard now certifies the bug rather than reporting it, and the next reviewer reads a green suite. It is also an under-statement of its own subject — the entry says "the edit form shows an empty English name", which is a cosmetic claim, when the consequence is a destructive write on submit.


## Reproduction — the evidence this rests on

All three parts EXECUTED, not merely reasoned about.

(1) The query DOES select them. internal/admin/query.sql:919-923 (`-- name: AdminShippingMethods :many`):
    "v.id AS version_id, v.name, v.carrier,
     coalesce(v.name_en, '') AS name_en, coalesce(v.carrier_en, '') AS carrier_en,"
sqlc emits them: internal/db/query.sql.go:1644-1645 `NameEn string` / `CarrierEn string` on `AdminShippingMethodsRow`. The view-model fields also exist: internal/ui/pages/adminshipping.go:52-53 `NameEn string` / `CarrierEn string` on `AdminShippingMethod`.

(2) The round-trip DROPS them. internal/admin/shipping.go:40-46 builds the view model and never assigns NameEn/CarrierEn:
    method := pages.AdminShippingMethod{
        MethodID: m.MethodID.String(), VersionID: m.VersionID.String(),
        Code: m.Code, Destination: m.DestinationKind, Name: m.Name,
        Carrier: m.Carrier.String, FeeCents: m.FeeCents,
        FreeOverCents: m.FreeOverCents.Int64,
        EffectiveAt:   m.EffectiveAt.Format("2006-01-02"),
        VersionCount:  m.VersionCount, Active: m.IsActive,
    }
(contrast line 65, where the ZONE loop does assign `NameEn: z.NameEn`.)
The template reads them: internal/ui/pages/adminshipping.templ:50 `name="name_en" value={ m.NameEn }` and :54 `name="carrier_en" value={ m.CarrierEn }`.

LIVE SERVER, staff session minted the way the Makefile's check-layout does (GOEN_TOTP_KEY is empty, so no step-up), GET http://127.0.0.1:9700/admin/shipping → 200. Rendered per-method publish form:
  <input ... name="name"      value="宅配到府" required maxlength="60">
  <input ... name="carrier"   value="黑貓宅急便" maxlength="60">
  <input ... id="name-en-ffff0001-...-001" name="name_en"    value="" ...>
  <input ... id="carrier-en-ffff0001-...-001" name="carrier_en" value="" ...>
while the database holds the translations:
  psql → home_delivery | 宅配到府 | Home delivery | 黑貓宅急便 | T-Cat
So the Chinese fields come back pre-filled and the English ones come back blank.

Handler takes the blanks verbatim: internal/admin/handler.go:1989-1990 `NameEn: r.PostFormValue("name_en"), CarrierEn: r.PostFormValue("carrier_en")`, into `ShippingVersion` → internal/admin/shipping.go:112 `NameEn: nameEn, CarrierEn: carrierEn`.

WRITE reproduced in a rolled-back transaction, running exactly the generated statement (internal/db/query.sql.go:7838-7845) with the parameters an untouched form submits:
  BEGIN;
  INSERT INTO shipping_method_versions (method_id, name, carrier, name_en, carrier_en, fee_cents, free_over_cents)
  VALUES ('ffff0001-...-001','宅配到府', nullif('黑貓宅急便'::text,''), nullif(''::text,''), nullif(''::text,''), 8000, nullif(300000,0))
  RETURNING id, name, name_en, carrier_en;
  → 01a031bc-... | 宅配到府 | (null) | (null)
Then the current-version read (DISTINCT ON (sm.id) ORDER BY effective_at DESC — the same shape AdminShippingMethods and cart.ShippingChoices use):
  home_delivery | 宅配到府 | <NULL> | <NULL>
  store_pickup  | 超商取貨 | Convenience store pickup | <NULL>
  ROLLBACK;
Customer-facing effect confirmed in SQL: `localized_name(v.name, v.name_en, 'en')` on the live row = "Home delivery"; `localized_name('宅配到府', NULL, 'en')` = "宅配到府". internal/cart/query.sql:91-92 (`ShippingChoices`) and internal/site/query.sql:16 read exactly that expression, so the checkout chooser and /shipping go Chinese for English visitors.

(3) The guard with the allowlist is TestEveryViewModelFieldIsAssigned, internal/ui/pages/fields_test.go:19, allowlist at lines 22-35. The two entries, verbatim (lines 27-28):
    "AdminShippingMethod.NameEn":    "REPORTED: the store never fills it, so the edit form shows an empty English name",
    "AdminShippingMethod.CarrierEn": "REPORTED: the store never fills it, so the edit form shows an empty English carrier",
(TestEveryLocaleParamIsAssigned, internal/db/locale_param_test.go:12, is a different guard — it asks whether a sqlc Params struct carrying a `Locale` field is filled; it has no allowlist and is blind to this, because AdminShippingMethods takes no locale param.)
`go test ./internal/ui/pages/ -run TestEveryViewModelFieldIsAssigned -count=1` → ok. Mutation, proven to apply (perl -i deleting exactly those two lines, then reverted with git checkout):
    --- FAIL: TestEveryViewModelFieldIsAssigned (0.03s)
        fields_test.go:55: AdminShippingMethod.CarrierEn is declared and never assigned.
        fields_test.go:55: AdminShippingMethod.NameEn is declared and never assigned.
So the allowlist is the only thing holding the guard green — the test institutionalises the defect exactly as claimed.

"REPORTED" is not a disposition this repository recognises: grep for the string finds it only inside fields_test.go, and neither docs/roadmap.md nor docs/reviews/ names this item. .claude/rules/review-process.md requires "Fixed in this PR", "Queued by name — a named work item in the project's plan or tracker — never a mental note", or "Refused in writing". This is none of the three.

Working tree: my one edit was reverted (`git diff --stat` empty). The `zz*_probe*_test.go` files listed by `git status --porcelain` are not mine — they appeared at 11:06-11:07 during this run from concurrent sibling review agents and are still being added; I left them alone rather than deleting another agent's in-flight probe. The probe admin user/session I inserted into the dev database was deleted (`select count(*) ... = 0`).


## Blast radius

Every English-reading customer, on the buying mainline, from the first time the shop touches its own delivery fees — and silently.

The wipe happens on the ordinary path, not an edge case: a staff member opens /admin/shipping, changes the fee (or the free-over threshold), and presses 發布新版本. The form they submit carries the Chinese name and carrier pre-filled and the English ones blank, so the new version — which is immediately the current one, `DISTINCT ON (sm.id) ORDER BY effective_at DESC` — has name_en = NULL and carrier_en = NULL. Nothing in the UI says a translation was there; the fields simply looked empty and stayed empty.

What then reads NULL:
- internal/cart/query.sql:91-92 `ShippingChoices` — the checkout's delivery chooser. An English customer reads 宅配到府 / 超商取貨 at the moment of paying. This is precisely the failure CLAUDE.md added `name_en`/`carrier_en` to fix: "Delivery method names were the last shop-typed chrome on the buying mainline: the checkout's chooser labels come from `shipping_method_versions.name`, so an English customer read 宅配到府 and 超商取貨 at the moment of paying."
- internal/site/query.sql:16 — the /shipping policy page.
- internal/cart/query.sql:135 — the order/summary surface that names the chosen method.

Irreversible without a new row: `shipping_method_versions_append_only` (migrations/001, line ~1364, BEFORE UPDATE OR DELETE → forbid_change) means the wiped version cannot be repaired in place; the shop must publish yet another version, and can only do so if somebody notices — which requires reading the site in English. Orders priced against the wiped version keep pointing at a row whose English name is gone.

Silent in both directions: no error, no constraint, no log. `shipping_method_versions` has no NOT NULL on name_en (it is optional by design, and correctly so — a shop must be able to have no translation), so the database has nothing to refuse. And the one guard that could see it, TestEveryViewModelFieldIsAssigned, is held green by its own allowlist.

Severity is high rather than critical because no money, stock or history is corrupted — it is chrome on the payment page, and CLAUDE.md's own judgement of that class ("the half-translated failure the whole locale feature exists to stop") is what sets it above cosmetic.


## Fix

Three edits. Fix the assignment, delete the exemption, and complete the audit record. No schema change, no sqlc regeneration (the query and the generated row already carry both columns), no templ regeneration (the template already reads both fields).

1. internal/admin/shipping.go, in `func (s *Store) Shipping`, the composite literal at lines 40-46. Add the two fields from the row that is already in hand (`m` is `*db.AdminShippingMethodsRow`, whose `NameEn` and `CarrierEn` are plain `string` because the query coalesces them):

        method := pages.AdminShippingMethod{
            MethodID: m.MethodID.String(), VersionID: m.VersionID.String(),
            Code: m.Code, Destination: m.DestinationKind, Name: m.Name,
            Carrier: m.Carrier.String, FeeCents: m.FeeCents,
            NameEn: m.NameEn, CarrierEn: m.CarrierEn,
            FreeOverCents: m.FreeOverCents.Int64,
            EffectiveAt:   m.EffectiveAt.Format("2006-01-02"),
            VersionCount:  m.VersionCount, Active: m.IsActive,
        }

   Put a comment on those two fields saying why they must be here: PublishShippingVersion is an INSERT, so a field the form does not carry is a field the next version loses, and shipping_method_versions_append_only means it cannot be repaired in place.

2. internal/ui/pages/fields_test.go, delete lines 27-28 — the "AdminShippingMethod.NameEn" and "AdminShippingMethod.CarrierEn" allowlist entries. Do NOT leave them with a reworded reason; the guard checks the allowlist by identity (fields_test.go:64-70, "By identity, never by count"), so a stale entry fails the test on its own and the deletion is required for the suite to pass once the fix lands.

3. internal/admin/shipping.go:100-106, the `audited` Event's `After` map, which records `"name_en": nameEn` and omits `carrier_en`. Add `"carrier_en": carrierEn` so the audit row names every shop-typed string the version was published with.

DO NOT fix this in SQL by coalescing the new value onto the previous version's. The write must stay `nullif(@name_en::text, '')` (internal/admin/query.sql:945). CLAUDE.md fixes that rule for the twin case on categories: "a shop that added a translation must be able to take it back, which is why the write is `nullif(@name_en, '')` and not a coalesce onto the old value." A server-side carry-forward would make clearing an English name impossible through the only door that exists. The form rendering the current value IS the carry-forward, exactly as it already is for `name`, `carrier`, `fee` and `free_over`.

DO NOT add a NOT NULL or a "must be present" CHECK on name_en/carrier_en. They are optional by design and every localized read falls back through `localized_name`.

Leave the other allowlist entries (AdminCreditView.*, AdminCampaignView.*, AdminCustomersView.Notice) alone — they are separate findings and each needs its own disposition; touching them here would make one fix look like five.

After the fix, per .claude/rules/review-process.md, re-run /self-review on it: the newly reachable state is a staff member who deliberately blanks an English name to clear it, which must still work (nullif → NULL) and is what test (c) below pins.


## The lock, and how to see it fail first

Two locks, because the meta-guard alone cannot see the destructive write — it only asks whether a field is assigned, and a field could be assigned from the wrong source and still pass.

LOCK A — the meta-guard, restored. In internal/ui/pages/fields_test.go, deleting the two allowlist rows is itself the lock. Proof of red is already recorded and re-runnable: with the fix NOT applied, delete lines 27-28 and run
    go test ./internal/ui/pages/ -run TestEveryViewModelFieldIsAssigned -count=1
Observed:
    --- FAIL: TestEveryViewModelFieldIsAssigned (0.03s)
        fields_test.go:55: AdminShippingMethod.CarrierEn is declared and never assigned.
        fields_test.go:55: AdminShippingMethod.NameEn is declared and never assigned.
Then apply the shipping.go fix and it goes green. The mutation provably applies (`git diff` shows the two lines gone before the run), and it compiles — it is an allowlist row, not a symbol.

LOCK B — the behavioural round-trip, which is the one that pins the consequence. Add to internal/admin/integration_test.go (the feature's single //go:build integration file, package admin_test, testcontainers against real PostgreSQL — no new file, no fake, no interface):

    TestPublishingAVersionKeepsItsEnglishName
    (a) Fixture: insert a shipping_methods row and a first shipping_method_versions row with name='宅配到府', name_en='Home delivery', carrier='黑貓宅急便', carrier_en='T-Cat', fee_cents=8000, effective_at=now()-interval '1 day'. Do NOT reuse the dev seed's rows — CLAUDE.md #23/#26: a fixture must create what it needs.
    (b) Call Store.Shipping(ctx). Find the method by Code and assert with cmp.Diff that NameEn=="Home delivery" and CarrierEn=="T-Cat". This alone goes RED today (both are "").
    (c) Feed the view straight back into the publish path, exactly as an untouched form does — Store.PublishShippingVersion(ctx, ShippingVersion{MethodID: m.MethodID, Name: m.Name, Carrier: m.Carrier, NameEn: m.NameEn, CarrierEn: m.CarrierEn, FeeDollars: 100, FreeOverDollars: 3000}) — with an actor in ctx, since `audited` returns ErrNoActor without one. Then read the row back with raw SQL, not through the view:
        SELECT name, name_en, carrier, carrier_en FROM shipping_method_versions
        WHERE method_id = $1 ORDER BY effective_at DESC, id DESC LIMIT 1
    and assert name_en='Home delivery', carrier_en='T-Cat', fee_cents=10000. Assert on the RAW COLUMN, never on `localized_name(...)` or on the view — CLAUDE.md #34 records what an assertion coarser than the error costs, and a view-level assertion would pass on the same defective source the view was built from.
    (d) A clearing case in the same test, because the fix must not become a coalesce: publish once more with NameEn:"" and CarrierEn:"" and assert both columns come back SQL NULL. Without this, someone "fixing" it in the query with a coalesce onto the previous version would go green while removing the shop's ability to take a translation back.

MUTATION, to be watched red and recorded in the PR:
  - For (b)/(c): delete `NameEn: m.NameEn, CarrierEn: m.CarrierEn` from the literal in internal/admin/shipping.go:40-46 and re-run. It must fail on (b) with `NameEn = "", want "Home delivery"` and on (c) with `name_en = <nil>, want "Home delivery"`. Confirm the deletion applied by reading the file back before the run — CLAUDE.md records false-green mode #3, an edit whose pattern matched nothing while a grep claimed it had.
  - For (d): change the query's `nullif(@name_en::text, '')` to `coalesce(nullif(@name_en::text, ''), <previous>)` (or simply `@name_en::text`) and re-run; (d) must go red. Restore.
  - Deleting the whole fixture must NOT leave the test green: if (a) is removed the test cannot find its method and must fail loudly rather than skip — CLAUDE.md #26's rule for a fixture that stops working.

Gate: `make verify` (which runs the unit suite, so Lock A) and `make test-integration` (Lock B; needs Docker). Do not report either from a piped command — CLAUDE.md #5.
