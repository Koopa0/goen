# setzoneprefixes-appends

**Verdict** CONFIRMED · **Severity** high · **Origin** external report (verified this round)

**Files** internal/admin/shipping.go, internal/admin/query.sql, internal/db/query.sql.go, internal/admin/integration_test.go, internal/ui/pages/adminshipping.templ, internal/admin/handler.go, cmd/goen/server.go, internal/cart/query.sql


## Root cause

A comment stating the rationale for the half that IS implemented, standing in for the half that is not — CLAUDE.md's mistake #31 shape. `shipping.go:338` reads "upserts each prefix; delete-then-insert leaves a gap", which is a true and good argument about ORDERING for a prefix present in the submission. A reader (and the author) takes it as the whole design of the write and stops, and the question the sentence never asks — what happens to a prefix the operator took OUT of the field — is never answered.

Underneath that, the write's semantics and the form's semantics were decided in different places and never reconciled. `AdminShippingZones` renders the complete current set into one input labelled "Postal codes for <zone>" with a "Set" button, which is a replace control; the store implements union-with-what-is-already-there. Two correct-looking halves that disagree — the class CLAUDE.md says every guard here is blind to, because they all ask what is ABSENT and here nothing is absent.


## Reproduction — the evidence this rests on

EXECUTED twice — once as raw SQL against the live dev database in a rolled-back transaction, once through the Go store via a temporary integration test (since deleted).

1) The code. `internal/admin/shipping.go:338-366`:

```go
// SetZonePrefixes upserts each prefix; delete-then-insert leaves a gap.
func (s *Store) SetZonePrefixes(ctx context.Context, id, list string) (map[string]string, error) {
	...
	}, func(ctx context.Context, q *db.Queries) error {
		for _, prefix := range prefixes {
			if assignErr := q.AssignZonePrefix(ctx, db.AssignZonePrefixParams{
				Prefix: prefix, ZoneID: zoneID,
			}); assignErr != nil {
```
The closure contains ONLY the upsert loop. No DELETE anywhere in it.

`internal/admin/query.sql:1344-1347`:
```sql
-- name: AssignZonePrefix :exec
INSERT INTO shipping_zone_prefixes (prefix, zone_id)
VALUES (@prefix::text, @zone_id)
ON CONFLICT (prefix) DO UPDATE SET zone_id = @zone_id;
```

2) The UI does present the field as the complete set. `internal/admin/query.sql:965-973` (`AdminShippingZones`) fills it with `string_agg(zp.prefix, ' ' ORDER BY zp.prefix)` — every prefix the zone holds. `internal/ui/pages/adminshipping.templ:262-267` renders that whole set into one editable input and a submit button:
```
<input class="ui-input ui-input--sm" id={ "pre-" + z.ID } name="prefixes" value={ z.Prefixes }/>
<button ...>{ i18n.T(ctx, i18n.KeyAdminShipSet) }</button>
```
The sr-only label is `KeyAdminShipZonePrefixes` = 「%s 的郵遞區號」 / "Postal codes for %s" (`internal/i18n/chrome_admin_shipping.go:131`) and the button is `KeyAdminShipSet` = 「設定」 / "Set" (`:64`). A prefilled full set plus "Set" is a replace field, and it is the ONLY prefix control on the page — there is no per-prefix remove button and no route for one (`cmd/goen/server.go:274-276` has exactly three zone routes: create, prefixes, delete).

3) SQL reproduction (`psql "$GOEN_DATABASE_URL"`, rolled back):
```
BEGIN
INSERT 0 1   -- zone
INSERT 0 1   -- '100'  (CreateZone's AssignZonePrefix)
INSERT 0 1   -- '200'
     step     | prefixes
--------------+----------
 after create | 100 200
INSERT 0 1   -- SetZonePrefixes with the field edited down to "100"
         step          | prefixes
-----------------------+----------
 after set to 100 only | 100 200
ROLLBACK
```

4) Go reproduction (temporary `internal/admin/zzprobe_integration_test.go`, `go test -tags integration -run TestProbeSetZonePrefixesDropsNothing ./internal/admin/`), calling `s.CreateZone(..., Prefixes: "100 200")` then `s.SetZonePrefixes(ctx, zoneID, "100")`:
```
--- FAIL: TestProbeSetZonePrefixesDropsNothing (0.01s)
    after setting the field to "100" the zone holds "100 200"
    zone holds "100 200", want "100"
```
`SetZonePrefixes` returned no error and no field errors. File deleted afterwards.

5) The operator is told it worked. `internal/admin/handler.go:1318-1319` redirects to `/admin/shipping?ok=1`, and `adminNotices["ok"]` (`handler.go:461`) → `KeyAdminNoticeOK` = 「已更新。」 / "Saved." (`internal/i18n/chrome_admin_pages.go:65`).

6) `RemoveZonePrefix` (`internal/admin/shipping.go:369`, query at `query.sql:1351`) has no production caller: `grep -rn RemoveZonePrefix` finds only the store method, sqlc's generated wrapper, and `internal/admin/integration_test.go:4882` — a test using it to empty a zone so `DeleteZone` will accept it, a capability no route has.

7) The documented reasoning does NOT cover omission. CLAUDE.md: "Assigning a prefix is an UPSERT, since `prefix` is the primary key of `shipping_zone_prefixes` — a postal code belongs to exactly one zone by construction, so moving one between zones is the ordinary edit, and delete-then-insert would leave a postal code in no zone for a moment in which a checkout would undercharge." Every clause is about a prefix that IS in the submitted list (its move from zone A to zone B), and about the ordering of statements for that prefix. It says nothing about a prefix the operator removed from the field. `docs/roadmap.md` mentions neither zones nor prefixes. So this is a gap in the decision, not the decision.

The no-gap argument is also already satisfied by something else: `Store.audited` (`internal/admin/audit.go:110-127`) opens a `pgx` transaction and runs the whole closure inside it, so a delete and an insert in that closure are never separately visible to a checkout.


## Blast radius

Everyone who checks out to a postal code the shop meant to stop surcharging, on every order, until someone re-reads the field.

`internal/cart/query.sql:123-132` (`ShippingZoneFor`) prices a delivery by `zp.prefix = left(@postal_code::text, 3)`, so a prefix stranded in a surcharge zone keeps adding that zone's `surcharge_cents` at checkout. Two concrete ways it fires:

- Un-surcharging an area. An operator editing 離島's field from "100 200" to "100" is told "Saved."; every subsequent order to a 200* postal code is still charged the 離島 surcharge (NT$200 in the shop's own documented example). The customer is overcharged and has no way to know; the shop believes it stopped.
- Correcting a typo. A zone created with "890" when "880" was meant, then corrected to "880", ends up holding BOTH — 890* customers are surcharged for a zone they were never meant to be in.

Not fully silent to the operator: the redirect re-renders the field from the database, so the dropped prefix reappears in the box. But the page's own one-shot banner says "Saved." above it, and nothing marks the difference between what was submitted and what was kept.

Two secondary consequences, both real:

- The audit trail records a number that is false. `shipping.go:352` writes `After: map[string]any{"prefixes": len(prefixes)}` — the count of the SUBMITTED list. After the reproduction above the row says `prefixes: 1` while the zone holds 2. `audit_events` is append-only, so that is a permanent wrong figure in the one record of what staff did.
- A zone with prefixes has no direct door out of `DeleteZone`. `query.sql:1355-1359` refuses the delete while `shipping_zone_prefixes` holds a row for the zone, `parsePrefixes` (`shipping.go:418-420`) refuses an empty list, and `RemoveZonePrefix` has no route — so emptying a zone from the back office is only possible by re-assigning each of its prefixes to some other zone one submission at a time.

No test locks the current behaviour: `TestAShopCanSayWhichPostalCodesCostMore` (`integration_test.go:4800`) covers creation and movement between zones only, so the fix does not have to change a passing assertion.


## Fix

Make `SetZonePrefixes` mean what its form says: the submitted list IS the zone's whole set.

1. `internal/admin/query.sql`, beside `AssignZonePrefix` (currently line 1344) — add:

```sql
-- The field on /admin/shipping carries the zone's WHOLE set, so a prefix the
-- operator removed from it is one they took out of this zone. Scoped to the
-- zone in the DELETE's own WHERE clause, like RemoveZonePrefix: a prefix that
-- has since moved to another zone is not swept by a stale form.
-- name: RemoveZonePrefixesExcept :execrows
DELETE FROM shipping_zone_prefixes
WHERE zone_id = @zone_id AND NOT (prefix = ANY(@keep::text[]));
```

The `zone_id = @zone_id` predicate is load-bearing and must not be dropped: without it one zone's form empties every other zone.

2. Regenerate with `make sqlc`. Do not hand-edit `internal/db` (CLAUDE.md: sqlc output "is never hand-edited"), and use `make sqlc-check` afterwards, remembering mistake #12 — the check regenerates before diffing.

3. `internal/admin/shipping.go:353-361` — inside the existing `s.audited(...)` closure, after the upsert loop and before `return nil`, add:

```go
		if _, delErr := q.RemoveZonePrefixesExcept(ctx, db.RemoveZonePrefixesExceptParams{
			ZoneID: zoneID, Keep: prefixes,
		}); delErr != nil {
			return delErr
		}
```

Order matters and is upserts-FIRST, delete-second, so that a prefix being moved INTO this zone by the same submission is written before anything is swept. `audited` (`internal/admin/audit.go:111-124`) already wraps the closure in one transaction, so no checkout ever sees an intermediate state — which is the concern the existing comment raises, now answered explicitly rather than by omission.

4. Replace the comment at `internal/admin/shipping.go:338`. It must state the replace semantics, not the upsert's rationale alone. Suggested:

```go
// SetZonePrefixes replaces the zone's set with the submitted one: the form shows
// every prefix the zone holds, so a prefix left out of it is one the operator
// removed. Assigning is an UPSERT and the sweep runs after it, in one
// transaction, so a prefix moving between zones is never in no zone.
```

5. Do NOT change `parsePrefixes`' refusal of an empty list as part of this fix — that is a separate decision (it is what leaves `DeleteZone` without a direct door). Either keep `RemoveZonePrefix` and give it a route, or let `SetZonePrefixes` accept an empty list for an existing zone (the create form's own hint `KeyAdminShipPrefixesHint`, `internal/i18n/chrome_admin_shipping.go:145`, already describes an empty zone as a valid state: "A zone with no prefixes is matched by no postal code, so it can never add a surcharge"). Whichever is chosen, `RemoveZonePrefix` must not be left with a test as its only caller — that is the repository's own "a fixture that reaches past the application is a fixture for a feature with no entrance". Per `.claude/rules/review-process.md`, if it is not fixed here it is queued by name.

6. `After: map[string]any{"prefixes": len(prefixes)}` at line 352 becomes true once the sweep lands, because the submitted count then IS the resulting count. No change needed, but the test below should assert it so it stays true.


## The lock, and how to see it fail first

Add `TestAZonesPostalCodesAreTheWholeSet` to `internal/admin/integration_test.go` (package `admin_test`, existing `//go:build integration` file), next to `TestAShopCanSayWhichPostalCodesCostMore`. It must have TWO zones or half of it is blind.

Fixture and assertions:
1. `s.CreateZone(ctx, &admin.NewZone{Code: a, Name: "甲區", Prefixes: "100 200 300"})`.
2. `s.CreateZone(ctx, &admin.NewZone{Code: b, Name: "乙區", Prefixes: "600 700"})` — the neighbour, present solely so an unscoped DELETE is visible.
3. `s.SetZonePrefixes(ctx, zoneA, "100 300 400")` — one prefix kept, one OMITTED (200), one added (400).
4. Assert zone A holds exactly `["100","300","400"]` via `SELECT prefix ... WHERE zone_id = $1 ORDER BY prefix` and `cmp.Diff`. This is the defect: today it returns `100 200 300 400`.
5. Assert zone B still holds exactly `["600","700"]`.
6. Assert movement still works: `s.SetZonePrefixes(ctx, zoneA, "100 300 400 600")` leaves 600 owned by zone A and zone B holding only `["700"]` — the documented UPSERT behaviour must survive the fix.
7. Assert the audit figure is now true: read the newest `audit_events` row for `action = 'shipping.zone.prefixes'` on that zone and check `(after->>'prefixes')::int` equals the count actually in the table.

Three mutations, each run and each recorded RED before the test is called a lock (`.claude/rules/testing.md`, "Locks Are Proven by Mutation"), and each must be SEEN to apply — CLAUDE.md's false-green mode #3, where an edit matched nothing and a grep claimed it had:

- Mutation A — delete the `RemoveZonePrefixesExcept` call from the closure. Step 4 must fail with `got ["100","200","300","400"], want ["100","300","400"]`. This is the exact defect reproduced above, so its RED transcript is the proof the lock is aimed at it.
- Mutation B — drop `zone_id = @zone_id` from the DELETE's WHERE clause and regenerate. Step 5 must fail (zone B emptied). Without the second zone in the fixture this mutation stays GREEN, which is why steps 2 and 5 exist.
- Mutation C — move the delete BEFORE the upsert loop. Step 6 must still pass (it will, inside one transaction); record it GREEN with the reason stated, rather than dressing it up as a lock — the ordering is a statement-level property the test cannot distinguish, the same call `internal/twofactor`'s constant-time compare and `/admin/messages`' single-clock fix already get.

`TestAZonePrefixMustBeThreeDigits` (`integration_test.go:4852`) calls `s.RemoveZonePrefix` at line 4882 to empty a zone before deleting it. If fix step 5 removes that method, that test must be rewritten to empty the zone through whatever door replaces it — and if it is rewritten to use `SetZonePrefixes` with an empty list, that path needs its own mutation proof too.

Run with `make test-integration` (shuffled, per CLAUDE.md mistake #23), then `make verify`, using `cmd && echo PASS || echo FAIL` and never a pipe (mistake #5).
