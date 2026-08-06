//go:build integration

package db_test

import (
	"errors"
	"os"
	"regexp"
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
// This block exists because the schema's central claim — that store cannot
// write stock, money or a ledger except through a function, and cannot switch
// the guards off — had no test at all. A round-3 review found the claim false
// in several ways (INSERT was never revoked on payments/refunds/variants; the
// SECURITY DEFINER functions were PUBLIC EXECUTE; unpinned trigger functions
// could be shadowed via pg_temp) precisely because nothing here ever assumed
// store and tried the forbidden write. These tests do, and each is proven by
// mutation: re-grant the privilege, or unpin a function, and the matching test
// goes red.
// ============================================================================

// appWritableThroughDefiner names the tables a SECURITY DEFINER function writes
// that `store` may STILL write directly, and says why for each.
//
// This is the exception list, and it is short on purpose. The rule —
// "a table a definer function writes is a table the app writes only through
// it" — is derived from the catalog by TestEveryDefinerWrittenTableIsRevoked,
// so a new ledger is covered the day it exists rather than the day somebody
// remembers to add it here.
//
// The hand-written list this replaced had eight entries and the catalog finds
// eleven: loyalty_entries, coupon_redemptions and product_copurchases were all
// added after it and none of them was noticed.
var appWritableThroughDefiner = map[string]string{
	// The account is created on first use — a customer who has never held
	// credit or points has no row, and refusing to make one would refuse their
	// first award. UPDATE is revoked, which is the half that matters: a direct
	// UPDATE could repoint a whole balance at another user.
	"store_credit_accounts": "created on first use; UPDATE is what is revoked",
}

func goenAppHasTablePriv(t *testing.T, table, priv string) bool {
	t.Helper()
	var ok bool
	if err := schemaPool(t).QueryRow(t.Context(),
		"SELECT has_table_privilege('store', $1, $2)", table, priv).Scan(&ok); err != nil {
		t.Fatalf("has_table_privilege(store, %q, %q): %v", table, priv, err)
	}
	return ok
}

// TestStoreHasNoDirectWriteToMoney is the core of the privilege model: the
// application role holds no INSERT/UPDATE/DELETE on any money, stock or ledger
// table. The born-succeeded payment and phantom-stock variant the review wrote
// as store both landed because INSERT was granted here.
// TestEveryDefinerWrittenTableIsRevoked derives the rule from the catalog.
//
// A SECURITY DEFINER function exists to be the ONE door into a table. If the
// app can also write that table directly, the function is a convention rather
// than a control — and a convention is what the next query forgets.
//
// Derived rather than listed, because a hand-written list only covers what
// somebody remembered: the list this replaced was written when goen had eight
// such tables and never grew, while the schema grew to eleven.
func TestEveryDefinerWrittenTableIsRevoked(t *testing.T) {
	tables := definerWrittenTables(t)
	if len(tables) < 8 {
		t.Fatalf("only %d definer-written tables found; the catalog query is not "+
			"finding them and this test would pass on nothing", len(tables))
	}

	for _, table := range tables {
		for _, role := range []string{"store", "admin"} {
			for _, priv := range []string{"INSERT", "UPDATE", "DELETE"} {
				if !hasTablePriv(t, role, table, priv) {
					continue
				}
				// Scoped to INSERT: the exception is "the row is created on
				// first use", never "this table is unguarded". Widening it to
				// every verb currently changes nothing — the one entry has
				// UPDATE and DELETE revoked anyway — and that is recorded
				// rather than dressed up as a lock. The scope is here for the
				// second entry, whichever table earns one.
				if why, allowed := appWritableThroughDefiner[table]; allowed && priv == "INSERT" {
					t.Logf("%s may INSERT %s: %s", role, table, why)
					continue
				}
				t.Errorf("%s has %s on %s, which a SECURITY DEFINER function writes — "+
					"the function is meant to be the only door, and a second one "+
					"makes it a convention rather than a control",
					role, priv, table)
			}
		}
	}
}

// definerWrittenTables is every table a SECURITY DEFINER function inserts into.
//
// Read from prosrc. That is a text search over function bodies and it would
// miss a dynamic INSERT — but goen has none, and the alternative is the
// hand-written list whose failure mode is silence.
func definerWrittenTables(t *testing.T) []string {
	t.Helper()
	rows, err := schemaPool(t).Query(t.Context(), `
		WITH written AS (
			SELECT DISTINCT lower(m[1]) AS tbl
			FROM pg_proc p,
			     LATERAL regexp_matches(p.prosrc, 'INSERT\s+INTO\s+([a-z_]+)', 'gi') m
			WHERE p.prosecdef AND p.pronamespace = 'public'::regnamespace
		)
		SELECT w.tbl FROM written w
		JOIN pg_tables t ON t.tablename = w.tbl AND t.schemaname = 'public'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read definer-written tables: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// hasTablePriv asks the database, for any role.
func hasTablePriv(t *testing.T, role, table, priv string) bool {
	t.Helper()
	var ok bool
	if err := schemaPool(t).QueryRow(t.Context(),
		"SELECT has_table_privilege($1, $2, $3)", role, table, priv).Scan(&ok); err != nil {
		t.Fatalf("read privilege: %v", err)
	}
	return ok
}

// TestAppendOnlyTablesDenyUpdateDelete is catalog-driven: every table whose
// forbid_change trigger declares it history must deny store UPDATE and
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
				t.Errorf("append-only %s grants store %s; the trigger is its only guard", table, priv)
			}
		}
	}
}

// TestStoreCannotDisableTriggers proves the guards cannot be switched off from
// the application role: session_replication_role is superuser-only, so the SET
// must be refused. Without this, every trigger-based rule is optional.
func TestStoreCannotDisableTriggers(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL ROLE store"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL session_replication_role = 'replica'"); err == nil {
		t.Fatal("store was allowed to set session_replication_role; it can disable every trigger guard")
	}
}

// TestStoreIsNotSuperuser proves SET ROLE store actually drops superuser —
// the invariant the binary's startup guard also checks. A superuser session
// ignores every REVOKE above.
func TestStoreIsNotSuperuser(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL ROLE store"); err != nil {
		t.Fatalf("set role: %v", err)
	}
	var isSuper bool
	if err := tx.QueryRow(ctx, "SELECT current_setting('is_superuser')::boolean").Scan(&isSuper); err != nil {
		t.Fatalf("read is_superuser: %v", err)
	}
	if isSuper {
		t.Fatal("session is still a superuser after SET ROLE store; the REVOKEs do not bind it")
	}
}

// TestStoreHasNoTempPrivilege locks the defense-in-depth half of the pg_temp
// fix: with TEMP revoked, store cannot create the shadowing temp table at
// all, independent of search_path pinning.
func TestStoreHasNoTempPrivilege(t *testing.T) {
	var ok bool
	if err := schemaPool(t).QueryRow(t.Context(),
		"SELECT has_database_privilege('store', current_database(), 'TEMP')").Scan(&ok); err != nil {
		t.Fatalf("has_database_privilege: %v", err)
	}
	if ok {
		t.Fatal("store holds TEMP; it can plant a pg_temp table that shadows a guard's tables")
	}
}

// TestEveryStoredFunctionEndsSearchPathWithPgTemp is the completeness gate for
// the pg_temp class-break. It is not enough that a function pins search_path:
// pg_temp is searched FIRST for relations unless it is listed explicitly, so a
// pin of "pg_catalog, public" leaves the shadow open. The only safe shape is
// pg_temp listed LAST, after public, so a real table always wins. This asserts
// exactly that — every goen (non-extension) function's search_path ends in
// pg_temp — and would fail the earlier "pg_catalog, public" pin that read as
// fixed but was not.
//
// Every language, not only plpgsql: a LANGUAGE sql function resolves unqualified
// relations the same way, so a plpgsql filter here (and in the migration's own
// ALTER loop) was a blind spot waiting for the first SQL helper someone writes.
func TestEveryStoredFunctionEndsSearchPathWithPgTemp(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT p.proname,
		       (SELECT cfg FROM unnest(coalesce(p.proconfig, '{}')) cfg
		        WHERE cfg LIKE 'search_path=%')
		FROM pg_proc p
		WHERE p.pronamespace = 'public'::regnamespace
		  AND p.prokind IN ('f', 'p')
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
// which a review showed proved nothing. A decoy pg_temp.categories is planted
// and a cycle is written that categories_reject_cycle must refuse. If the pin
// were wrong the guard would read the empty decoy, find no cycle, and the UPDATE
// would land.
//
// It no longer does this AS `store`, and the reason is that the attack it models
// has since been closed twice over from the other side. `store` now holds no
// write on categories at all — merchandising is not something a storefront
// request does — so the setup could not even reach the trigger, and the test
// failed with "permission denied" rather than proving anything about
// search_path.
//
// The property is role-independent: pg_temp is searched ahead of public for ANY
// role, so listing it LAST is what makes a real table win, and demonstrating
// that with the owner demonstrates it for everybody. What `store` specifically
// can no longer do is covered by TestStoreHasNoTempPrivilege and by the
// categories revoke — two independent layers, each with its own test, which is
// the arrangement rather than a weakening.
func TestSearchPathPinDefeatsTempShadowing(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	setup := []string{
		"INSERT INTO categories (id, slug, name) VALUES ('aaaa9999-0000-4000-8000-000000000001','shadow-a','A')",
		"INSERT INTO categories (id, parent_id, slug, name) VALUES ('bbbb9999-0000-4000-8000-000000000002','aaaa9999-0000-4000-8000-000000000001','shadow-b','B')",
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

// TestStoreCannotDeleteUsers locks the erasure path: a direct DELETE of a user
// leaves delivery PII on their orders (order_private_data keys on the order) and
// a plaintext restock email behind, so DELETE is revoked and erase_user is the
// only door. UPDATE stays for profile edits.
func TestStoreCannotDeleteUsers(t *testing.T) {
	if goenAppHasTablePriv(t, "users", "DELETE") {
		t.Error("store can DELETE users directly, bypassing erase_user and leaving PII behind")
	}
	// UPDATE is now a COLUMN grant, so the table-level question answers "no" and
	// the useful one is per column: the storefront changes a password and a
	// profile, and never a ROLE.
	//
	// It held whole-table UPDATE until a third-party probe ran
	// `UPDATE users SET role='admin'` as store and got `UPDATE 1` — a storefront
	// request one statement from the back office. Asserting the table-level
	// privilege is what MISSED that, because the answer was "yes" and the answer
	// was right for the wrong column.
	for _, col := range []string{"password_hash", "full_name", "phone", "email"} {
		if !goenAppHasColumnPriv(t, "users", col, "UPDATE") {
			t.Errorf("store cannot UPDATE users.%s; the storefront writes it", col)
		}
	}
	if goenAppHasColumnPriv(t, "users", "role", "UPDATE") {
		t.Error("store can UPDATE users.role — a storefront request is one statement " +
			"from making itself an admin")
	}
	// The same hole from the INSERT side, which is the easier one to forget:
	// without a column list, store cannot UPDATE a role and can simply INSERT a
	// row that already carries one.
	if goenAppHasColumnPriv(t, "users", "role", "INSERT") {
		t.Error("store can INSERT users.role — a registration could name its own role")
	}
}

// goenAppHasColumnPriv asks whether store holds priv on one COLUMN.
func goenAppHasColumnPriv(t *testing.T, table, column, priv string) bool {
	t.Helper()
	var ok bool
	if err := schemaPool(t).QueryRow(t.Context(),
		`SELECT has_column_privilege('store', $1::regclass, $2, $3)`,
		table, column, priv).Scan(&ok); err != nil {
		t.Fatalf("has_column_privilege(store, %s.%s, %s): %v", table, column, priv, err)
	}
	return ok
}

// TestNoStoredFunctionIsPublicExecute is the completeness gate for the EXECUTE
// revoke: no goen-authored plpgsql function may carry a PUBLIC EXECUTE grant, or
// a role with no other privilege (reporting) could call a SECURITY DEFINER
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

// adminForbiddenTables is what the back office may NOT write directly.
//
// It is moneyStockLedgerTables minus product_variants: maintaining the
// catalogue IS the back office's job, so admin creates and retires variants
// where store cannot. What it still may not do is set stock_quantity — that is
// a column-level rule, asserted separately below, because the table-level
// permission here would otherwise imply it.
var adminForbiddenTables = []string{
	"payments", "refunds", "inventory_movements",
	"inventory_reservations", "store_credit_entries", "audit_events",
	"order_number_counters",
}

// TestAdminCannotWriteMoneyOrStockDirectly is the back office's half of the
// privilege model. `admin` is a WIDER set than store, not an unbounded one: the
// point of the model is that no connecting role writes money or a ledger by
// hand, and the back office is where that temptation is strongest.
func TestAdminCannotWriteMoneyOrStockDirectly(t *testing.T) {
	for _, table := range adminForbiddenTables {
		for _, priv := range []string{"INSERT", "UPDATE", "DELETE"} {
			var ok bool
			if err := schemaPool(t).QueryRow(t.Context(),
				"SELECT has_table_privilege('admin', $1, $2)", table, priv).Scan(&ok); err != nil {
				t.Fatalf("has_table_privilege(admin, %q, %q): %v", table, priv, err)
			}
			if ok {
				t.Errorf("admin has %s on %s; the back office must reach it through a function",
					priv, table)
			}
		}
	}
}

// TestAdminCannotSetStockQuantity is the narrower rule, and the one a
// table-level grant silently defeats.
//
// PostgreSQL reads table-level UPDATE as permission on every column, so a
// column-level REVOKE against a table-level grant does nothing at all — the
// first version of this rule looked right and left stock fully writable. The
// grant is therefore column-by-column, and this asserts both halves: stock is
// refused, and the columns the back office actually needs are not.
func TestAdminCannotSetStockQuantity(t *testing.T) {
	for _, tc := range []struct {
		column string
		want   bool
	}{
		{"stock_quantity", false}, // only record_inventory_movement may move it
		{"price_cents", true},
		{"safety_stock", true},
		{"is_active", true},
	} {
		var ok bool
		if err := schemaPool(t).QueryRow(t.Context(),
			"SELECT has_column_privilege('admin', 'product_variants', $1, 'UPDATE')",
			tc.column).Scan(&ok); err != nil {
			t.Fatalf("has_column_privilege(admin, product_variants, %q): %v", tc.column, err)
		}
		if ok != tc.want {
			if tc.want {
				t.Errorf("admin cannot UPDATE product_variants.%s, which the back office needs",
					tc.column)
			} else {
				t.Errorf("admin can UPDATE product_variants.%s directly; stock would move with "+
					"no movement row behind it and the ledger would disagree with the shelf",
					tc.column)
			}
		}
	}
}

// TestAdminIsNotASuperuser is the same guard the connecting role gets: a
// superuser ignores every REVOKE above.
func TestAdminIsNotASuperuser(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL ROLE admin"); err != nil {
		t.Fatalf("set role admin: %v", err)
	}
	var super bool
	if err := tx.QueryRow(ctx, "SELECT current_setting('is_superuser')::boolean").Scan(&super); err != nil {
		t.Fatalf("read is_superuser: %v", err)
	}
	if super {
		t.Fatal("the session is a superuser after SET ROLE admin; every REVOKE above is decorative")
	}
}

// roleHasColumnPriv answers whether a role may write one column.
func roleHasColumnPriv(t *testing.T, role, table, column, priv string) bool {
	t.Helper()
	var ok bool
	if err := schemaPool(t).QueryRow(t.Context(),
		"SELECT has_column_privilege($1, $2, $3, $4)", role, table, column, priv).Scan(&ok); err != nil {
		t.Fatalf("has_column_privilege(%s, %s.%s, %s): %v", role, table, column, priv, err)
	}
	return ok
}

// TestNoRoleCanWriteStockDirectly proves stock has exactly one door, for every
// role and for both write verbs.
//
// stock_quantity has exactly one door — record_inventory_movement — because
// that is what keeps the shelf and inventory_movements agreeing. A role that
// can set the column writes stock with no ledger row behind it.
//
// This is asserted per COLUMN and per VERB, which is the part that was missed.
// `admin` legitimately creates variants, so the table-level test written for
// `store` does not transfer: admin had UPDATE revoked and INSERT left alone,
// and could conjure a variant carrying 999 units at birth. Both verbs need the
// column list; the INSERT one is the easier to forget.
func TestNoRoleCanWriteStockDirectly(t *testing.T) {
	for _, role := range []string{"store", "admin", "reporting"} {
		for _, priv := range []string{"INSERT", "UPDATE"} {
			t.Run(role+"/"+priv, func(t *testing.T) {
				if roleHasColumnPriv(t, role, "product_variants", "stock_quantity", priv) {
					t.Errorf("%s may %s product_variants.stock_quantity; stock must move "+
						"only through record_inventory_movement, or the shelf and the "+
						"ledger disagree with nothing to reconcile them", role, priv)
				}
			})
		}
	}

	// The control. admin DOES create variants, so a blanket revoke would pass
	// every assertion above and break the back office instead.
	if !roleHasColumnPriv(t, "admin", "product_variants", "sku", "INSERT") {
		t.Error("admin cannot insert a variant's sku; the back office cannot add a product")
	}
	if !roleHasColumnPriv(t, "admin", "product_variants", "price_cents", "UPDATE") {
		t.Error("admin cannot reprice a variant; the back office cannot run the shop")
	}
}

// TestAdminHasNoDirectWriteToMoney mirrors TestStoreHasNoDirectWriteToMoney for
// the back office.
//
// The back office is staff, not a superuser: it decides returns and grants
// credit through SECURITY DEFINER functions for the same reason the storefront
// captures payments through one. product_variants is absent from this list on
// purpose — admin creates variants, and TestNoRoleCanWriteStockDirectly is what
// bounds that to the columns it may set.
func TestAdminHasNoDirectWriteToMoney(t *testing.T) {
	adminForbidden := []string{
		"payments", "refunds", "inventory_movements",
		"inventory_reservations", "store_credit_entries", "order_number_counters",
	}
	for _, table := range adminForbidden {
		for _, priv := range []string{"INSERT", "UPDATE", "DELETE"} {
			var ok bool
			if err := schemaPool(t).QueryRow(t.Context(),
				"SELECT has_table_privilege('admin', $1, $2)", table, priv).Scan(&ok); err != nil {
				t.Fatalf("has_table_privilege(admin, %q, %q): %v", table, priv, err)
			}
			if ok {
				t.Errorf("admin has %s on %s; the back office must reach it through a function",
					priv, table)
			}
		}
	}
}

// TestNoGrantNamesARoleThatDoesNotExistYet reads the ordering out of the file
// rather than discovering it from a container that will not start.
//
// It is REDUNDANT and says so. A grant naming a role created further down makes
// the migration fail outright, so TestMain never gets a database and every case
// in this package errors before it runs — which is loud, and is what happened
// to me an hour ago.
//
// It is kept for what that failure does NOT say. "role admin does not exist"
// names the role and not the line, so the next person reads 4,000 lines of SQL
// to find which of forty grants moved. This names the line and the line it must
// come after. A mutation of it therefore comes back green, and that is recorded
// rather than dressed up: the lock is PostgreSQL, and this is the error
// message.
func TestNoGrantNamesARoleThatDoesNotExistYet(t *testing.T) {
	schema, err := os.ReadFile("../../migrations/001_initial_schema.up.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	src := string(schema)

	// Every role the file creates, with where.
	created := map[string]int{}
	for _, m := range regexp.MustCompile(`(?m)CREATE ROLE ([a-z_]+)`).FindAllStringSubmatchIndex(src, -1) {
		created[src[m[2]:m[3]]] = m[0]
	}
	if len(created) < 4 {
		t.Fatalf("only %d roles found; the parser is not reading the schema", len(created))
	}

	// Every GRANT/REVOKE naming a role, with where.
	stmt := regexp.MustCompile(`(?m)^\s*(?:GRANT|REVOKE)\b[^;]*?\b(?:TO|FROM)\s+([a-z_, ]+);`)
	for _, m := range stmt.FindAllStringSubmatchIndex(src, -1) {
		at := m[0]
		for _, role := range strings.Split(src[m[2]:m[3]], ",") {
			role = strings.TrimSpace(role)
			if role == "" || role == "PUBLIC" || role == "public" {
				continue
			}
			createdAt, exists := created[role]
			if !exists {
				// goen is the owner, created by the deployment rather than by
				// this file.
				if role == "goen" {
					continue
				}
				t.Errorf("line %d grants to %q, which this file never creates",
					lineOf(src, at), role)
				continue
			}
			if at < createdAt {
				t.Errorf("line %d grants to %q, which is not created until line %d — "+
					"the migration fails outright on a fresh database",
					lineOf(src, at), role, lineOf(src, createdAt))
			}
		}
	}
}

// lineOf is the 1-indexed line an offset falls on.
func lineOf(src string, offset int) int {
	return strings.Count(src[:offset], "\n") + 1
}
