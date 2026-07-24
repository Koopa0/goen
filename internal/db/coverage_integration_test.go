//go:build integration

package db_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// This file exists because the previous suite lied.
//
// It reported 48 green subtests and was read as "every constraint is
// exercised". Deleting constraints one at a time showed that 48 of the 65
// named CHECKs could be removed without a single test turning red: the suite
// asserted the rules it happened to think of, and its silence about the rest
// looked exactly like coverage.
//
// The fix is to stop trusting a hand-written list. Every test below derives
// its expectations from the live catalog, so a constraint added to the
// migration without a case here fails the build rather than quietly joining
// the untested majority.

// TestEveryCheckConstraintIsExercised is the completeness gate. It reads the
// constraint names PostgreSQL actually created and requires a case for each.
func TestEveryCheckConstraintIsExercised(t *testing.T) {
	live := liveCheckConstraints(t)

	covered := make(map[string]bool, len(checkCases))
	for _, c := range checkCases {
		covered[c.constraint] = true
	}

	var missing []string
	for _, name := range live {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d CHECK constraints have no case in checkCases:\n  %s\n\n"+
			"Add one per constraint. A constraint with no case is a constraint\n"+
			"nobody has watched reject anything.",
			len(missing), strings.Join(missing, "\n  "))
	}

	// The reverse direction: a case naming a constraint that no longer exists
	// is a test that silently stopped testing when the schema moved on.
	inLive := make(map[string]bool, len(live))
	for _, name := range live {
		inLive[name] = true
	}
	var stale []string
	for _, c := range checkCases {
		if !inLive[c.constraint] {
			stale = append(stale, c.constraint)
		}
	}
	if len(stale) > 0 {
		t.Errorf("%d cases name a constraint the database does not have:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// TestCheckConstraintNamesAreUnique is the precondition the coverage gate
// relies on. checkCases is keyed by bare constraint name, which stands in for
// exactly one constraint only while no name repeats across tables. PostgreSQL
// permits a repeat, so if one ever appears this fails and says to make the
// coverage query and the case key table-qualified — before a duplicate lets
// one case silently cover for another.
func TestCheckConstraintNamesAreUnique(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT c.conname, count(*)
		FROM pg_constraint c
		JOIN pg_class tbl ON tbl.oid = c.conrelid
		WHERE c.contype = 'c'
		  AND c.connamespace = 'public'::regnamespace
		  AND tbl.relname <> 'schema_migrations'
		GROUP BY c.conname
		HAVING count(*) > 1
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("query constraint names: %v", err)
	}
	defer rows.Close()

	var dupes []string
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		dupes = append(dupes, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(dupes) > 0 {
		t.Errorf("constraint names repeat across tables: %s\n"+
			"The coverage gate keys on the bare name; make liveCheckConstraints\n"+
			"and the case key (table, name) before this can be trusted.",
			strings.Join(dupes, ", "))
	}
}

// TestCheckConstraintsReject runs every case's rejecting statement and requires
// that the named constraint — not merely some constraint — is what refused it.
//
// Binding the assertion to the constraint name is the point. The old suite
// accepted any error, so a statement that tripped an unrelated unique index
// counted as proof that the CHECK worked.
func TestCheckConstraintsReject(t *testing.T) {
	for _, c := range checkCases {
		t.Run(c.constraint, func(t *testing.T) {
			err := run(t, c.reject)
			if err == nil {
				t.Fatalf("the database accepted the row; %s does not enforce this", c.constraint)
			}

			code, name := constraintViolation(err)
			if code != "23514" {
				t.Fatalf("refused with SQLSTATE %s (constraint %q), want a 23514 check violation: %v",
					code, name, err)
			}
			if name != c.constraint {
				t.Fatalf("refused by %q, want %q — this case is proving the wrong rule",
					name, c.constraint)
			}
		})
	}
}

// TestCheckConstraintsAccept runs the neighbouring legal value. Without it a
// constraint that refuses everything is indistinguishable from a correct one.
func TestCheckConstraintsAccept(t *testing.T) {
	for _, c := range checkCases {
		if c.accept == "" {
			t.Run(c.constraint, func(t *testing.T) {
				t.Skipf("no legal neighbour: %s", c.acceptNote)
			})
			continue
		}
		t.Run(c.constraint, func(t *testing.T) {
			if err := run(t, c.accept); err != nil {
				t.Fatalf("the database refused a legal row: %v", err)
			}
		})
	}
}

// TestEveryUniqueConstraintIsExercised applies the same completeness rule to
// the unique indexes that carry a business rule.
func TestEveryUniqueConstraintIsExercised(t *testing.T) {
	live := liveUniqueIndexes(t)

	covered := make(map[string]bool, len(uniqueCases))
	for _, c := range uniqueCases {
		covered[c.index] = true
	}

	var missing []string
	for _, name := range live {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d unique indexes have no case in uniqueCases:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestUniqueConstraintsReject requires each unique index to refuse its own
// duplicate, identified by name.
func TestUniqueConstraintsReject(t *testing.T) {
	for _, c := range uniqueCases {
		t.Run(c.index, func(t *testing.T) {
			err := run(t, c.reject)
			if err == nil {
				t.Fatalf("the database accepted the duplicate; %s does not enforce this", c.index)
			}
			code, name := constraintViolation(err)
			if code != "23505" {
				t.Fatalf("refused with SQLSTATE %s (constraint %q), want a 23505 unique violation: %v",
					code, name, err)
			}
			if name != c.index {
				t.Fatalf("refused by %q, want %q", name, c.index)
			}
		})
	}
}

// TestUniqueConstraintsAdmitTheNeighbour proves each index is scoped as
// intended: the near-duplicate that differs in the one dimension the index
// does not cover must be accepted.
func TestUniqueConstraintsAdmitTheNeighbour(t *testing.T) {
	for _, c := range uniqueCases {
		if c.accept == "" {
			t.Run(c.index, func(t *testing.T) {
				t.Skipf("no meaningful neighbour: %s", c.acceptNote)
			})
			continue
		}
		t.Run(c.index, func(t *testing.T) {
			if err := run(t, c.accept); err != nil {
				t.Fatalf("the database refused a legal near-duplicate: %v", err)
			}
		})
	}
}

// liveCheckConstraints returns every named CHECK in the public schema.
//
// NOT NULL is excluded: PostgreSQL 18 records those as CHECK constraints with
// generated names, and they are covered by the column definitions themselves.
func liveCheckConstraints(t *testing.T) []string {
	t.Helper()

	rows, err := schemaPool(t).Query(t.Context(), `
		-- contype = 'c' already excludes NOT NULL, which PostgreSQL 18 records
		-- as contype = 'n'. An earlier version also filtered constraint names
		-- ending _not_null, which would have silently excused a real CHECK a
		-- developer happened to name that way — the name is no longer consulted.
		--
		-- Keyed on the bare constraint name, which is safe only while names are
		-- globally unique; TestCheckConstraintNamesAreUnique enforces that
		-- precondition, and turns red with instructions if it ever stops holding.
		SELECT c.conname
		FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		WHERE c.contype = 'c'
		  AND c.connamespace = 'public'::regnamespace
		  AND t.relname <> 'schema_migrations'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read check constraints: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no check constraints found; the migration did not apply")
	}
	sort.Strings(names)
	return names
}

// liveUniqueIndexes returns the unique indexes that encode a business rule.
// Primary keys are excluded: uniqueness of a generated surrogate key is a
// property of uuidv7, not a rule about goen.
func liveUniqueIndexes(t *testing.T) []string {
	t.Helper()

	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT i.relname
		FROM pg_index x
		JOIN pg_class i ON i.oid = x.indexrelid
		JOIN pg_class t ON t.oid = x.indrelid
		WHERE x.indisunique
		  AND NOT x.indisprimary
		  AND i.relnamespace = 'public'::regnamespace
		  AND t.relname <> 'schema_migrations'
		  -- Exclude FK-support indexes: a unique index whose columns include the
		  -- table's primary key (e.g. (product_id, id)) is unique by virtue of
		  -- the PK and cannot be violated in isolation — the PK fires first. It
		  -- exists only so a composite foreign key can reference those columns,
		  -- not to enforce a business rule, so there is nothing to write a
		  -- reject case for.
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_index pk
		      WHERE pk.indrelid = x.indrelid AND pk.indisprimary
		        AND (string_to_array(pk.indkey::text, ' ')::smallint[])
		            <@ (string_to_array(x.indkey::text, ' ')::smallint[])
		        AND x.indnatts > pk.indnatts
		  )
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read unique indexes: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	sort.Strings(names)
	return names
}

// checkCase pairs one CHECK constraint with a statement it must refuse and,
// where one exists, the neighbouring statement it must accept.
type checkCase struct {
	constraint string
	reject     string
	accept     string
	// acceptNote explains why no accepting statement exists, for the few
	// constraints whose legal side is already covered by the fixtures.
	acceptNote string
}

// uniqueCase pairs one unique index with a duplicate it must refuse and a
// near-duplicate it must admit.
type uniqueCase struct {
	index      string
	reject     string
	accept     string
	acceptNote string
}

// constraintViolation extracts the SQLSTATE and constraint name PostgreSQL
// reported, so an assertion can name the rule it is proving instead of
// accepting any failure at all. That distinction is what the previous suite
// lacked: a statement tripping an unrelated unique index read as proof that
// the CHECK under test worked.
func constraintViolation(err error) (code, constraint string) {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return "", ""
	}
	return pgErr.Code, pgErr.ConstraintName
}

// ============================================================================
// Privilege conformance.
//
// This block exists because the schema's central claim — that goen_app cannot
// write stock, money or a ledger except through a function, and cannot switch
// the guards off — had no test at all. A round-3 review found the claim false
// in several ways (INSERT was never revoked on payments/refunds/variants; the
// SECURITY DEFINER functions were PUBLIC EXECUTE; unpinned trigger functions
// could be shadowed via pg_temp) precisely because nothing here ever assumed
// goen_app and tried the forbidden write. These tests do, and each is proven by
// mutation: re-grant the privilege, or unpin a function, and the matching test
// goes red.
// ============================================================================

// moneyStockLedgerTables are written only through a SECURITY DEFINER function
// or the owner-run payment path; goen_app must hold no direct DML on them. This
// list is explicit because "reached only through a function" is intent the
// catalog cannot express — but every row is mutation-proven: re-GRANT the
// privilege in the migration and TestGoenAppHasNoDirectWriteToMoney fails.
var moneyStockLedgerTables = []string{
	"payments", "refunds", "product_variants", "inventory_movements",
	"inventory_reservations", "store_credit_entries", "audit_events",
	"order_number_counters",
}

func goenAppHasTablePriv(t *testing.T, table, priv string) bool {
	t.Helper()
	var ok bool
	if err := schemaPool(t).QueryRow(t.Context(),
		"SELECT has_table_privilege('goen_app', $1, $2)", table, priv).Scan(&ok); err != nil {
		t.Fatalf("has_table_privilege(goen_app, %q, %q): %v", table, priv, err)
	}
	return ok
}

// TestGoenAppHasNoDirectWriteToMoney is the core of the privilege model: the
// application role holds no INSERT/UPDATE/DELETE on any money, stock or ledger
// table. The born-succeeded payment and phantom-stock variant the review wrote
// as goen_app both landed because INSERT was granted here.
func TestGoenAppHasNoDirectWriteToMoney(t *testing.T) {
	for _, table := range moneyStockLedgerTables {
		for _, priv := range []string{"INSERT", "UPDATE", "DELETE"} {
			if goenAppHasTablePriv(t, table, priv) {
				t.Errorf("goen_app has %s on %s; it must reach this table only through a function",
					priv, table)
			}
		}
	}
}

// TestAppendOnlyTablesDenyUpdateDelete is catalog-driven: every table whose
// forbid_change trigger declares it history must deny goen_app UPDATE and
// DELETE, so the trigger is not the only thing standing between the app and a
// rewritten ledger. A new append-only table without the matching revoke fails
// here. INSERT is deliberately not asserted — some (invoice_document_lines) are
// appended by the app.
func TestAppendOnlyTablesDenyUpdateDelete(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT DISTINCT c.relname
		FROM pg_trigger tg
		JOIN pg_class c ON c.oid = tg.tgrelid
		JOIN pg_proc p ON p.oid = tg.tgfoid
		WHERE NOT tg.tgisinternal
		  AND p.proname = 'forbid_change'
		  AND c.relnamespace = 'public'::regnamespace
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read append-only tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("no append-only tables found; the forbid_change triggers are missing")
	}
	for _, table := range tables {
		for _, priv := range []string{"UPDATE", "DELETE"} {
			if goenAppHasTablePriv(t, table, priv) {
				t.Errorf("append-only %s grants goen_app %s; the trigger is its only guard", table, priv)
			}
		}
	}
}

// TestGoenAppCannotDisableTriggers proves the guards cannot be switched off from
// the application role: session_replication_role is superuser-only, so the SET
// must be refused. Without this, every trigger-based rule is optional.
func TestGoenAppCannotDisableTriggers(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL ROLE goen_app"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL session_replication_role = 'replica'"); err == nil {
		t.Fatal("goen_app was allowed to set session_replication_role; it can disable every trigger guard")
	}
}

// TestGoenAppIsNotSuperuser proves SET ROLE goen_app actually drops superuser —
// the invariant the binary's startup guard also checks. A superuser session
// ignores every REVOKE above.
func TestGoenAppIsNotSuperuser(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL ROLE goen_app"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	var isSuper bool
	if err := tx.QueryRow(ctx, "SELECT current_setting('is_superuser')::boolean").Scan(&isSuper); err != nil {
		t.Fatalf("read is_superuser: %v", err)
	}
	if isSuper {
		t.Fatal("session is still a superuser after SET ROLE goen_app; the REVOKEs do not bind it")
	}
}

// TestGoenAppHasNoTempPrivilege locks the defense-in-depth half of the pg_temp
// fix: with TEMP revoked, goen_app cannot create the shadowing temp table at
// all, independent of search_path pinning.
func TestGoenAppHasNoTempPrivilege(t *testing.T) {
	var ok bool
	if err := schemaPool(t).QueryRow(t.Context(),
		"SELECT has_database_privilege('goen_app', current_database(), 'TEMP')").Scan(&ok); err != nil {
		t.Fatalf("has_database_privilege: %v", err)
	}
	if ok {
		t.Fatal("goen_app holds TEMP; it can plant a pg_temp table that shadows a guard's tables")
	}
}

// TestEveryStoredFunctionEndsSearchPathWithPgTemp is the completeness gate for
// the pg_temp class-break. It is not enough that a function pins search_path:
// pg_temp is searched FIRST for relations unless it is listed explicitly, so a
// pin of "pg_catalog, public" leaves the shadow open. The only safe shape is
// pg_temp listed LAST, after public, so a real table always wins. This asserts
// exactly that — every goen (non-extension) plpgsql function's search_path ends
// in pg_temp — and would fail the earlier "pg_catalog, public" pin that read as
// fixed but was not.
func TestEveryStoredFunctionEndsSearchPathWithPgTemp(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT p.proname,
		       (SELECT cfg FROM unnest(coalesce(p.proconfig, '{}')) cfg
		        WHERE cfg LIKE 'search_path=%')
		FROM pg_proc p
		JOIN pg_language l ON l.oid = p.prolang
		WHERE p.pronamespace = 'public'::regnamespace
		  AND l.lanname = 'plpgsql'
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e'
		  )
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read functions: %v", err)
	}
	defer rows.Close()

	var bad []string
	for rows.Next() {
		var name string
		var cfg *string
		if err := rows.Scan(&name, &cfg); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if cfg == nil {
			bad = append(bad, name+" (no search_path — pg_temp is then searched first)")
			continue
		}
		value := strings.TrimPrefix(*cfg, "search_path=")
		parts := strings.Split(value, ",")
		last := strings.TrimSpace(parts[len(parts)-1])
		if last != "pg_temp" {
			bad = append(bad, name+" (search_path is "+value+"; pg_temp must be last)")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(bad) > 0 {
		t.Errorf("%d functions do not end search_path with pg_temp (a temp table can shadow their relations):\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// TestSearchPathPinDefeatsTempShadowing is the behavioral proof behind the
// catalog gate above: the earlier suite only checked that search_path was set,
// which a review showed proved nothing. Here goen_app is handed TEMP for the
// transaction, plants an empty pg_temp.categories, and tries to write a cycle
// the categories_reject_cycle guard must refuse. If the pin were wrong the guard
// would read the decoy and the UPDATE would land; the guard must reject it.
func TestSearchPathPinDefeatsTempShadowing(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	setup := []string{
		"GRANT TEMPORARY ON DATABASE " + currentDatabase(t) + " TO goen_app",
		"INSERT INTO categories (id, slug, name) VALUES ('aaaa9999-0000-4000-8000-000000000001','shadow-a','A')",
		"INSERT INTO categories (id, parent_id, slug, name) VALUES ('bbbb9999-0000-4000-8000-000000000002','aaaa9999-0000-4000-8000-000000000001','shadow-b','B')",
		"SET LOCAL ROLE goen_app",
		"CREATE TEMP TABLE categories (id uuid, parent_id uuid, slug text, name text)",
	}
	for _, stmt := range setup {
		if _, serr := tx.Exec(ctx, stmt); serr != nil {
			t.Fatalf("setup %q: %v", stmt, serr)
		}
	}

	_, err = tx.Exec(ctx,
		"UPDATE public.categories SET parent_id='bbbb9999-0000-4000-8000-000000000002' WHERE id='aaaa9999-0000-4000-8000-000000000001'")
	if err == nil {
		t.Fatal("the cycle write landed: the guard read a pg_temp decoy, so search_path is not pinned to defeat shadowing")
	}
	if _, name := constraintViolation(err); name != "categories_acyclic" {
		t.Fatalf("write refused by %q, want categories_acyclic — the guard fired for the wrong reason: %v", name, err)
	}
}

func currentDatabase(t *testing.T) string {
	t.Helper()
	var name string
	if err := schemaPool(t).QueryRow(t.Context(), "SELECT current_database()").Scan(&name); err != nil {
		t.Fatalf("current_database: %v", err)
	}
	return name
}

// TestGoenAppCannotDeleteUsers locks the erasure path: a direct DELETE of a user
// leaves delivery PII on their orders (order_private_data keys on the order) and
// a plaintext restock email behind, so DELETE is revoked and erase_user is the
// only door. UPDATE stays for profile edits.
func TestGoenAppCannotDeleteUsers(t *testing.T) {
	if goenAppHasTablePriv(t, "users", "DELETE") {
		t.Error("goen_app can DELETE users directly, bypassing erase_user and leaving PII behind")
	}
	if !goenAppHasTablePriv(t, "users", "UPDATE") {
		t.Error("goen_app cannot UPDATE users; profile edits are ordinary writes and should be allowed")
	}
}

// TestNoStoredFunctionIsPublicExecute is the completeness gate for the EXECUTE
// revoke: no goen-authored plpgsql function may carry a PUBLIC EXECUTE grant, or
// a role with no other privilege (goen_readonly) could call a SECURITY DEFINER
// posting function and write stock through it.
func TestNoStoredFunctionIsPublicExecute(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT p.proname
		FROM pg_proc p
		JOIN pg_language l ON l.oid = p.prolang
		WHERE p.pronamespace = 'public'::regnamespace
		  AND l.lanname = 'plpgsql'
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e'
		  )
		  AND p.proacl IS NOT NULL
		  AND EXISTS (
		      SELECT 1 FROM aclexplode(p.proacl) a
		      WHERE a.grantee = 0 AND a.privilege_type = 'EXECUTE'
		  )
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read function acls: %v", err)
	}
	defer rows.Close()

	var public []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		public = append(public, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(public) > 0 {
		t.Errorf("%d functions are PUBLIC EXECUTE (any role can call them):\n  %s",
			len(public), strings.Join(public, "\n  "))
	}
}
