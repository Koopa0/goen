//go:build integration

package db_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestNoRoleHoldsAWriteItsQueriesNeverMake is the MIRROR of
// [TestEveryRoleCanReadWhatItsQueriesRead]: that one asks whether a role can do
// its job, and this one asks whether it can do anything else.
//
// # What the missing direction costs
//
// Without it, the customer-facing role escalates to back-office admin in three
// statements, as `store`:
//
//	UPDATE users SET role = 'admin' WHERE id = <attacker>;   -- UPDATE 1
//	DELETE FROM staff_totp_credentials WHERE user_id = <target>;
//	-- sign in
//
// and `admin` impersonates any customer, leaving no audit row:
//
//	UPDATE users SET password_hash = 'x' WHERE …;            -- UPDATE 1
//	INSERT INTO sessions (token_hash, user_id, expires_at) …;-- INSERT 1
//
// Both are pure capability. No handler makes either write and no feature wants
// them, so nothing is missing from the application and nothing looks wrong. A
// privilege test that is a HAND-WRITTEN LIST of money, stock and ledger tables
// cannot see either one: those tables are locked down properly, and the
// authentication tables are simply not on the list. A list cannot refuse a table
// nobody thought to put on it.
//
// That is the same argument this project accepts for
// TestEveryDefinerWrittenTableIsRevoked, where a hand-written list names eight
// tables and the catalog finds eleven. The rule there is "a table a SECURITY
// DEFINER function writes is a table the app writes only through it", derived
// rather than listed. The rule here is the general form:
//
//	a role may hold INSERT, UPDATE or DELETE on a table only if some query
//	that role actually runs writes it.
//
// # How the question is derived
//
// The privileges come from the catalog — `information_schema.role_table_grants`
// via has_table_privilege, so column-level grants and views are all visible.
//
// The EXPECTATION comes from the SQL this repository ships. Each role is mapped
// to the feature packages whose stores run on that role's pool, and a package's
// writes are the INSERT/UPDATE/DELETE targets in its own query.sql. That mapping
// is the one hand-maintained thing left, and it is deliberately small: four
// entries mirroring cmd/goen's wiring, rather than a per-table list that grows
// with the schema. [TestThePoolMapIsComplete] refuses a package that exists and
// is named by neither map, so adding a feature cannot silently skip the guard.
//
// # Why the pool map is the honest boundary
//
// Which role performs a write is decided by which pool a store is constructed
// on, and that fact lives in Go, not in SQL — there is nothing in the schema to
// derive it from. internal/newsletter is the proof it matters: its two halves
// run on two different pools on purpose, because subscribing is a storefront
// write and sending is a back-office one. Moving internal/twofactor from the
// storefront pool to the admin pool is what let `store` give up the second
// factor at all.
func TestNoRoleHoldsAWriteItsQueriesNeverMake(t *testing.T) {
	ctx := t.Context()

	for _, role := range []string{"store", "admin", "reporting", "maintenance"} {
		t.Run(role, func(t *testing.T) {
			allowed := writableTables(t, role)

			rows, err := pool.Query(ctx, `
				SELECT c.relname, p.priv
				FROM pg_class c
				JOIN pg_namespace n ON n.oid = c.relnamespace
				CROSS JOIN unnest(ARRAY['INSERT', 'UPDATE', 'DELETE']) AS p(priv)
				WHERE n.nspname = 'public'
				  AND c.relkind IN ('r', 'v', 'm', 'p')
				  AND has_table_privilege($1, c.oid, p.priv)
				ORDER BY c.relname, p.priv`, role)
			if err != nil {
				t.Fatalf("read the privilege catalog: %v", err)
			}
			defer rows.Close()

			held := map[string][]string{}
			for rows.Next() {
				var table, priv string
				if scanErr := rows.Scan(&table, &priv); scanErr != nil {
					t.Fatalf("scan a privilege: %v", scanErr)
				}
				held[table] = append(held[table], priv)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("walk the privilege catalog: %v", err)
			}

			for _, table := range sortedKeys(held) {
				if allowed[table] {
					continue
				}
				if why, ok := writeExemptions[role+"."+table]; ok {
					t.Logf("%s may write %s: %s", role, table, why)
					continue
				}
				t.Errorf("%s holds %s on %s, and no query it runs writes that table.\n"+
					"  A privilege with no caller is capability waiting for a bug to find "+
					"it — `store` reaching users.role and staff_totp_credentials was how a "+
					"storefront request became an admin, and `admin` reaching "+
					"users.password_hash and sessions was silent impersonation with no "+
					"audit row.\n"+
					"  Either a query should write it — in which case the pool map below "+
					"is wrong — or the grant should be revoked. If it must be held "+
					"unused, name it in writeExemptions with the reason.",
					role, strings.Join(held[table], "/"), table)
			}
		})
	}
}

// TestNoRoleHoldsAColumnWriteItsQueriesNeverMake is the same question one level
// down, and it is the level the escalation lives at.
//
// The table-level guard above cannot see the statement that matters:
//
//	UPDATE users SET role = 'admin' WHERE id = <attacker>;
//
// because `store` DOES write `users` — it registers accounts and changes
// passwords — so a whole-table grant reads as legitimate. The dangerous privilege
// is on ONE COLUMN of a table the role is entitled to write, and only a
// column-level question finds it. That is the mutation which separates the two
// guards: restore `GRANT INSERT, UPDATE ON users TO store` and the table-level
// test stays GREEN while this one goes red.
//
// PostgreSQL has the machinery — has_column_privilege — and this schema uses
// column grants for exactly this purpose on product_variants. What nothing else
// asks is whether they are complete.
//
// Tables whose expected column set is UNKNOWN are skipped here and covered by
// the table-level guard instead; see [columnWrites] for when that happens.
func TestNoRoleHoldsAColumnWriteItsQueriesNeverMake(t *testing.T) {
	ctx := t.Context()

	for _, role := range []string{"store", "admin"} {
		t.Run(role, func(t *testing.T) {
			expected := writableColumns(t, role)

			for _, table := range sortedColumnKeys(expected) {
				cols := expected[table]
				if cols == nil {
					continue // unknown; the table-level guard has it
				}
				if !columnNarrowed[table] {
					continue
				}
				// Columns the database GENERATES are excluded, and the exact
				// shape of that filter is load-bearing.
				//
				// Excluding every column that HAS a default is the obvious way
				// to silence the id/created_at/updated_at noise repeating on
				// every table, and it also silences `users.role`, whose default
				// is the literal 'customer' — so the mutation that gives `store`
				// back the single privilege this whole guard exists to refuse
				// stays GREEN. A filter written for readability removes the
				// subject.
				//
				// So the test is on the default EXPRESSION, not on its presence:
				// a default that manufactures a value per row (uuidv7(), now(),
				// nextval) is bookkeeping no statement names and no rule reads,
				// while a default that is a business CONSTANT — 'customer', 0,
				// false — is a real column somebody could set to something else.
				// Forging a created_at changes nothing; setting role does.
				rows, err := pool.Query(ctx, `
					SELECT c.column_name, p.priv
					FROM information_schema.columns c
					CROSS JOIN unnest(ARRAY['INSERT', 'UPDATE']) AS p(priv)
					WHERE c.table_schema = 'public' AND c.table_name = $2
					  AND c.is_identity = 'NO'
					  AND c.is_generated = 'NEVER'
					  AND (c.column_default IS NULL
					       OR c.column_default !~* '(uuidv7|gen_random_uuid|now|current_timestamp|nextval)')
					  AND has_column_privilege($1, c.table_name::regclass, c.column_name, p.priv)
					ORDER BY c.column_name, p.priv`, role, table)
				if err != nil {
					t.Fatalf("read column privileges for %s: %v", table, err)
				}
				for rows.Next() {
					var col, priv string
					if scanErr := rows.Scan(&col, &priv); scanErr != nil {
						t.Fatalf("scan a column privilege: %v", scanErr)
					}
					if cols[col] {
						continue
					}
					if why, ok := columnExemptions[role+"."+table+"."+col]; ok {
						t.Logf("%s may write %s.%s: %s", role, table, col, why)
						continue
					}
					t.Errorf("%s holds %s on %s.%s, and no query it runs writes that "+
						"column.\n  This is the level the privilege escalation lived at: "+
						"`store` writes `users` legitimately, so the whole-table guard "+
						"cannot see that it also reached users.role — one UPDATE from a "+
						"storefront request to a back-office account.\n  Either narrow "+
						"the column grant, or name it in columnExemptions with the reason.",
						role, priv, table, col)
				}
				if err := rows.Err(); err != nil {
					t.Fatalf("walk column privileges for %s: %v", table, err)
				}
				rows.Close()
			}
		})
	}
}

// columnNarrowed is the tables whose column grants are DELIBERATE, and which
// this guard therefore holds to being complete and no wider.
//
// It is a scope, not an allowlist: a table absent from here is still covered by
// the table-level guard, which asks whether the role should reach the table at
// all. What this list adds is the second question — given that it may write the
// table, may it write THIS column — and that question is only meaningful where
// somebody has already decided the answer is "not all of them".
//
// # Why it is not simply every table
//
// It nearly is. What holds a table back is the cost of getting the grant wrong
// in the other direction: narrowing means hand-authoring a column list, and a
// list one column too NARROW breaks a write path a customer is standing in.
// Every suite here connects as the OWNER, who is subject to no missing grant, so
// that class of mistake is invisible to almost everything in this package.
//
// [TestEveryRoleCanRunItsOwnQueries] is the exception, and it is what makes
// narrowing cheap enough to do everywhere: it plans every generated query under
// SET ROLE with EXPLAIN (GENERIC_PLAN), which resolves COLUMN privileges as well
// as table ones, so a grant one column too narrow is a red test in about a
// second, over 425 pairs.
//
// The lists in the migration are DERIVED from this test rather than authored:
// every column, minus the ones reported here. That is the difference between
// fourteen grants and fourteen guesses.
//
// Two pairs stay out and they are named in the migration too — `admin` on
// order_private_data and on stock_notifications. [writableColumns] returns nil
// for those, meaning the parser could not resolve the set, and a nil set is
// skipped as UNKNOWN a few lines below. Narrowing on a set nobody derived is
// the hand-authored grant this whole approach exists to avoid.
var columnNarrowed = map[string]bool{
	// The authentication surface, and the reason this guard exists. Whole-table
	// UPDATE on users lets `store` set role = 'admin', and lets `admin` set
	// password_hash and take any customer's account.
	"users": true,
	// Creating a session is signing somebody in. admin may stamp
	// totp_verified_at and nothing else.
	"sessions": true,
	// Stock is the column an admin may not set by hand — record_inventory_movement
	// is the only door, and this is the narrowing that makes that true.
	"product_variants": true,
	// The app stamps processed_at and may not rewrite the provider's evidence.
	"payment_webhook_events": true,
	// The eight tables both roles legitimately write, where the column decides
	// WHOSE statement a row records. store writes what a customer said; admin
	// writes what the shop decided about it. Neither may write the other's.
	//
	// hidden_at is the sharpest of them: hiding a review takes it out of the
	// SCORE as well as the list, so a storefront-reachable write to that column
	// moves a product's public rating.
	"product_reviews":   true,
	"product_questions": true,
	"product_answers":   true,
	// resolution, decided_at and status are the shop deciding a return; reason is
	// the customer stating one.
	"return_requests": true,
	// staff_note and completed_at are the back office's; every money figure is
	// the checkout's and admin may not restate it.
	"orders": true,
	// Everything except handled_at is a customer's own words, in the one table
	// /admin/messages exists to read.
	"contact_messages": true,
	// notified_at means "the outbox has this". Written by the claim, which runs
	// on the back office's stock adjustment.
	"stock_notifications": true,
	// erased_at is erase_user's, and erase_user is SECURITY DEFINER — so the
	// storefront reaches it through the function or not at all.
	"order_private_data": true,
}

// columnExemptions is a column privilege a role holds that its queries never
// exercise, with the reason. Keyed role.table.column.
var columnExemptions = map[string]string{
	// A row's own identity and its timestamps. The default supplies each, the
	// set_updated_at trigger maintains updated_at, and no statement names them —
	// but a column grant that omitted them would refuse the INSERT outright on
	// any statement that did, which is a trap for the next writer rather than a
	// boundary. They carry no authority: forging an id or a created_at changes
	// nothing a guard reads.
	"store.users.id":         "supplied by the default; naming no column grant for it would trap the next INSERT",
	"store.users.created_at": "supplied by the default",
	"store.users.updated_at": "maintained by set_updated_at",
	"admin.users.id":         "supplied by the default",
	"admin.users.created_at": "supplied by the default",
	"admin.users.updated_at": "maintained by set_updated_at",
	// A pre-order date the back office may set on a variant. It is in the column
	// grant and no query sets it yet, which makes it a FEATURE gap rather than a
	// privilege one — the column is read by the storefront and the form to fill
	// it has not been built. Named here so the guard keeps reporting it as a
	// decision rather than going quiet, and so building that form deletes an
	// entry instead of discovering an unexplained grant.
	"admin.product_variants.preorder_release_on": "the variant form does not collect it yet; the column is read and not yet written",
}

// writableColumns is every column a role's queries write, per table.
func writableColumns(t *testing.T, role string) map[string]map[string]bool {
	t.Helper()
	var pkgs []string
	switch role {
	case "store":
		pkgs = storefrontPackages
	case "admin":
		pkgs = backOfficePackages
	default:
		panic("db: the column guard covers only the two writing roles: " + role)
	}

	byQuery := queryColumnWrites(t)
	out := map[string]map[string]bool{}
	for _, pkg := range pkgs {
		for _, method := range calledQueries(t, pkg) {
			for table, cols := range byQuery[method] {
				if existing, seen := out[table]; seen && existing == nil {
					continue
				}
				if cols == nil {
					out[table] = nil
					continue
				}
				if out[table] == nil {
					out[table] = map[string]bool{}
				}
				for c := range cols {
					out[table][c] = true
				}
			}
		}
	}
	return out
}

// queryColumnWrites maps each generated query to the columns it writes.
func queryColumnWrites(t *testing.T) map[string]map[string]map[string]bool {
	t.Helper()
	src, err := os.ReadFile("query.sql.go")
	if err != nil {
		t.Fatalf("read the generated queries: %v", err)
	}
	out := map[string]map[string]map[string]bool{}
	for _, m := range generatedQuery.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = columnWrites(m[2])
	}
	return out
}

// calledQueries is every generated query method a package calls.
func calledQueries(t *testing.T, pkg string) []string {
	t.Helper()
	known := queryWrites(t)
	dir := filepath.Join("..", "..", "internal", pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read internal/%s: %v", pkg, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: a path built from this repository's own tree
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		for _, m := range methodCall.FindAllStringSubmatch(string(src), -1) {
			if _, ok := known[m[1]]; ok {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// sortedColumnKeys is the map's keys in a stable order.
func sortedColumnKeys(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// writeExemptions is a privilege a role holds that its own queries never
// exercise, with the reason it is right anyway. Keyed role.table so an exemption
// cannot spread to another role.
//
// Every entry is a claim that somebody looked. [TestNoStaleWriteExemption]
// refuses one whose role no longer holds the privilege, so the list cannot
// quietly describe a world that has moved on.
var writeExemptions = map[string]string{
	// Store credit accounts are created by the POSTING FUNCTIONS, which run as
	// their owner — so no role's queries write the table, and the INSERT looks
	// unused from here. It is kept because the account must exist before the
	// first entry can reference it, and the schema documents the INSERT as the
	// one legitimate direct write on that table.
	"store.store_credit_accounts": "created on first use; every other verb is revoked and the ledger goes through post_store_credit",
	"admin.store_credit_accounts": "created on first use when the back office grants credit to a customer with no account yet",
}

// generatedQuery matches one sqlc-generated query constant and its SQL body.
//
// The GENERATED file is the source of truth rather than each feature's
// query.sql, and that distinction is the whole reason this derivation works.
// sqlc emits ONE `db` package, so any feature's store may call any query no
// matter which .sql file declared it — internal/admin answers a product question
// through db.AnswerQuestion, whose SQL lives in internal/product/query.sql. A
// guard that mapped a package to its own .sql file would have concluded that the
// back office never writes product_answers, and then cheerfully revoked the
// privilege its own /admin/questions page depends on.
var generatedQuery = regexp.MustCompile(
	"(?s)const \\w+ = `-- name: (\\w+) :\\w+\n(.*?)\n`")

// queryWrites maps each generated query method to the tables it writes.
func queryWrites(t *testing.T) map[string]map[string]bool {
	t.Helper()
	src, err := os.ReadFile("query.sql.go")
	if err != nil {
		t.Fatalf("read the generated queries: %v", err)
	}
	out := map[string]map[string]bool{}
	for _, m := range generatedQuery.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = writeTargets(m[2])
	}
	if len(out) < 100 {
		t.Fatalf("parsed %d generated queries, want far more — the pattern is wrong",
			len(out))
	}
	return out
}

// methodCall matches a call to a db.Queries method on any receiver.
var methodCall = regexp.MustCompile(`\.([A-Z]\w*)\(`)

// packageWrites is every table the given feature package writes, derived from
// the generated queries it CALLS.
//
// Test files are excluded: a query only a test runs is not something the
// application does, and counting one would let a fixture justify a production
// privilege. TestEveryViewModelFieldIsAssigned excludes them for the same
// reason — a field only a test fills is the defect, not the cure.
//
// The match is deliberately coarse — any `.Something(` whose name happens to be
// a generated query counts, so a package that calls an unrelated method of the
// same name over-reports. Over-reporting makes the guard MORE permissive, never
// less, so it can produce a missed over-grant but never a false accusation. That
// is the safe direction to be wrong in, and it is written here rather than left
// for a reader to work out.
func packageWrites(t *testing.T, pkg string, byQuery map[string]map[string]bool) map[string]bool {
	t.Helper()
	dir := filepath.Join("..", "..", "internal", pkg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read internal/%s: %v", pkg, err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: a path built from this repository's own tree
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		for _, m := range methodCall.FindAllStringSubmatch(string(src), -1) {
			for table := range byQuery[m[1]] {
				out[table] = true
			}
		}
	}
	return out
}

// writeTarget matches the table an INSERT, UPDATE or DELETE names.
//
// Deliberately coarse, and coarse in the SAFE direction: a false positive here
// makes the guard permit a privilege it should have refused, so the patterns are
// anchored on the statement keyword rather than on any mention of a table name.
// A CTE's inner write counts, which is correct — it is still a write.
var writeTarget = regexp.MustCompile(
	`(?is)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)

// writeTargets is every table the given SQL writes.
func writeTargets(src string) map[string]bool {
	out := map[string]bool{}
	for _, m := range writeTarget.FindAllStringSubmatch(stripSQLComments(src), -1) {
		out[m[1]] = true
	}
	// SET is a keyword UPDATE is always followed by; a match of `UPDATE SET`
	// would mean the regexp caught a fragment rather than a statement.
	delete(out, "set")
	return out
}

// insertColumns matches an INSERT that names its columns.
var insertColumns = regexp.MustCompile(
	`(?is)\bINSERT\s+INTO\s+([a-z_][a-z0-9_]*)\s*\(([^)]*)\)`)

// bareInsert matches an INSERT that does NOT name its columns, which means every
// column and therefore no useful column expectation.
var bareInsert = regexp.MustCompile(
	`(?is)\bINSERT\s+INTO\s+([a-z_][a-z0-9_]*)\s+(?:VALUES|SELECT|DEFAULT)\b`)

// updateSet matches an UPDATE's target and its SET clause up to the first
// clause that ends it.
var updateSet = regexp.MustCompile(
	`(?is)\bUPDATE\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)\s+SET\s+(.*?)(?:\bWHERE\b|\bRETURNING\b|\bFROM\b|;|$)`)

// assignedColumn matches the identifier on the left of one SET assignment.
var assignedColumn = regexp.MustCompile(`(?i)(?:^|,)\s*([a-z_][a-z0-9_]*)\s*=`)

// columnWrites is the columns each table's writes name, and whether that set can
// be trusted as complete.
//
// A table maps to nil when some statement writes it without naming columns —
// an `INSERT INTO t SELECT …` or a shape these patterns do not recognise. nil
// means UNKNOWN, and unknown degrades that table to the table-level check rather
// than inventing an expectation. Refusing a privilege on the strength of a
// regexp that did not understand the statement is how a guard starts being
// worked around.
func columnWrites(src string) map[string]map[string]bool {
	clean := stripSQLComments(src)
	out := map[string]map[string]bool{}

	note := func(table string, cols []string) {
		if existing, seen := out[table]; seen && existing == nil {
			return // already unknown; nothing narrows it back
		}
		if cols == nil {
			out[table] = nil
			return
		}
		if out[table] == nil {
			out[table] = map[string]bool{}
		}
		for _, c := range cols {
			out[table][strings.ToLower(strings.TrimSpace(c))] = true
		}
	}

	for _, m := range insertColumns.FindAllStringSubmatch(clean, -1) {
		note(m[1], strings.Split(m[2], ","))
	}
	for _, m := range bareInsert.FindAllStringSubmatch(clean, -1) {
		note(m[1], nil)
	}
	for _, m := range updateSet.FindAllStringSubmatch(clean, -1) {
		var cols []string
		for _, a := range assignedColumn.FindAllStringSubmatch(m[2], -1) {
			cols = append(cols, a[1])
		}
		if len(cols) == 0 {
			note(m[1], nil)
			continue
		}
		note(m[1], cols)
	}
	return out
}

// stripSQLComments removes -- comments so a table named only in prose does not
// read as a write. sqlc's own doc comments sit directly above each query and
// routinely name the table, which is exactly the false positive to avoid.
func stripSQLComments(src string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(src, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// storefrontPackages run on the pool that does SET ROLE store.
//
// Mirrors cmd/goen's wiring. internal/newsletter appears in BOTH maps on
// purpose: subscribing is a storefront write and sending is a back-office one,
// and the two halves are constructed on two pools for exactly that reason.
var storefrontPackages = []string{
	"account", "cart", "catalog", "contact", "home", "loyalty", "media",
	"newsletter", "outbox", "payment", "product", "returns", "site", "warranty",
}

// backOfficePackages run on the pool that does SET ROLE admin.
var backOfficePackages = []string{
	"admin", "media", "newsletter", "outbox", "twofactor",
	// invoice runs on the admin pool: issuing a 統一發票 is the back office's
	// act, and `store` holds no write on invoice_documents at all — a storefront
	// request that could file a tax document is a customer issuing their own.
	"invoice",
}

// maintenancePackages run on the pool that does SET ROLE maintenance.
//
// One package and one function. refresh_copurchases is SECURITY DEFINER, so WHO
// may call it is the entire control — this role holds no table write at all.
var maintenancePackages = []string{"recommend"}

// writableTables is every table a role's own queries write.
func writableTables(t *testing.T, role string) map[string]bool {
	t.Helper()
	var pkgs []string
	switch role {
	case "store":
		pkgs = storefrontPackages
	case "admin":
		pkgs = backOfficePackages
	case "maintenance":
		pkgs = maintenancePackages
	case "reporting":
		// A dashboard reads. It writes nothing, which is the whole point of the
		// role, and an empty set is the correct expectation rather than an
		// oversight — so it is written here explicitly.
		pkgs = nil
	default:
		panic("db: unknown role in the pool map: " + role)
	}

	byQuery := queryWrites(t)
	out := map[string]bool{}
	for _, pkg := range pkgs {
		for table := range packageWrites(t, pkg, byQuery) {
			out[table] = true
		}
	}
	return out
}

// TestThePoolMapIsComplete refuses a feature package named by neither map.
//
// Without it, adding a package with its own query.sql would silently opt that
// feature's tables out of the guard: its writes would not be in any role's
// expected set, so the guard would either refuse legitimate grants or — if
// somebody then added an exemption to quiet it — permanently stop asking.
func TestThePoolMapIsComplete(t *testing.T) {
	named := map[string]bool{}
	for _, pkg := range storefrontPackages {
		named[pkg] = true
	}
	for _, pkg := range backOfficePackages {
		named[pkg] = true
	}
	for _, pkg := range maintenancePackages {
		named[pkg] = true
	}

	entries, err := os.ReadDir(filepath.Join("..", "..", "internal"))
	if err != nil {
		t.Fatalf("read internal/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "db" {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(
			"..", "..", "internal", e.Name(), "query.sql")); statErr != nil {
			continue
		}
		if !named[e.Name()] {
			t.Errorf("internal/%s ships a query.sql and is on no role's pool map, so "+
				"its writes are checked against nothing. Add it to the map that matches "+
				"the pool cmd/goen constructs its store on.", e.Name())
		}
	}
}

// TestNoStaleWriteExemption refuses an entry describing a privilege that is no
// longer held.
//
// Checked by IDENTITY rather than by count. A comparison of totals cannot say
// WHICH entry has gone stale, and it passes outright when one entry goes stale as
// another is added — the same reason the allowlist checks in
// TestEveryCategoryNameIsLocalized name their entries.
func TestNoStaleWriteExemption(t *testing.T) {
	ctx := t.Context()
	for key, why := range writeExemptions {
		role, table, ok := strings.Cut(key, ".")
		if !ok {
			t.Errorf("writeExemptions key %q is not role.table", key)
			continue
		}
		var held bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_class c
				JOIN pg_namespace n ON n.oid = c.relnamespace
				CROSS JOIN unnest(ARRAY['INSERT', 'UPDATE', 'DELETE']) AS p(priv)
				WHERE n.nspname = 'public' AND c.relname = $2
				  AND has_table_privilege($1, c.oid, p.priv))`,
			role, table).Scan(&held); err != nil {
			t.Fatalf("ask whether %s writes %s: %v", role, table, err)
		}
		if !held {
			t.Errorf("writeExemptions has %q (%s), but %s holds no write on %s. "+
				"The entry claims a gap that is closed, and an exemption nobody "+
				"revisits is how a list stops describing the schema.", key, why, role, table)
		}
	}
}

// sortedKeys is the map's keys in a stable order, so a failing run names tables
// in the same sequence every time.
func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
