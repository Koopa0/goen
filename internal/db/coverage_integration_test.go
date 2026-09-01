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

	"github.com/koopa0/goen/internal/pickup"
)

// Every test below derives what must be covered from the LIVE CATALOG, so a constraint added
// to the migration without a case here fails the build.

// TestEveryCheckConstraintIsExercised requires a case for every CHECK PostgreSQL created.
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

// TestCheckConstraintNamesAreUnique is the precondition the coverage gate relies on: checkCases
// keys on the bare constraint name, and PostgreSQL permits one to repeat across tables.
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

// TestCheckConstraintsReject requires the NAMED constraint to be what refused each case: one
// happy with any error counts a statement that tripped an unrelated unique index as proof.
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

// TestCheckConstraintsAccept runs the neighbouring legal value: a constraint refusing
// everything is otherwise indistinguishable from a correct one.
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

// TestPickupBrandChoicesMatchDatabaseContract binds the customer-facing closed
// set to the schema allowlist. A new form choice is not usable unless the
// database admits it, and widening the CHECK must not make arbitrary carrier
// routing codes valid at the write boundary.
func TestPickupBrandChoicesMatchDatabaseContract(t *testing.T) {
	brands := pickup.Offered()
	if len(brands) == 0 {
		t.Fatal("pickup.Offered() is empty; this test would prove nothing")
	}

	for _, brand := range brands {
		t.Run(string(brand), func(t *testing.T) {
			ctx := t.Context()
			tx, err := schemaPool(t).Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()

			if _, err := tx.Exec(ctx, fixtures); err != nil {
				t.Fatalf("load fixtures: %v", err)
			}
			if _, err := tx.Exec(ctx, `
				UPDATE order_private_data
				SET postal_code = NULL, city = NULL, district = NULL, street = NULL,
				    pickup_brand = $1, pickup_store_code = 'TEST01',
				    pickup_store_name = '契約測試門市'
				WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'`, string(brand)); err != nil {
				t.Fatalf("schema refused offered pickup brand %q: %v", brand, err)
			}

			var stored string
			if err := tx.QueryRow(ctx, `
				SELECT pickup_brand
				FROM order_private_data
				WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'`).Scan(&stored); err != nil {
				t.Fatalf("read stored pickup brand: %v", err)
			}
			if stored != string(brand) {
				t.Fatalf("stored pickup brand = %q, want offered value %q", stored, brand)
			}
		})
	}

	err := run(t, `
		UPDATE order_private_data
		SET postal_code = NULL, city = NULL, district = NULL, street = NULL,
		    pickup_brand = 'other_chain', pickup_store_code = 'TEST01',
		    pickup_store_name = '契約測試門市'
		WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'`)
	if err == nil {
		t.Fatal("database accepted an unknown pickup brand")
	}
	code, name := constraintViolation(err)
	if code != "23514" || name != "order_private_data_pickup_brand_known" {
		t.Fatalf("unknown pickup brand refused by SQLSTATE %s constraint %q, want 23514/order_private_data_pickup_brand_known: %v",
			code, name, err)
	}
}

// TestEveryUniqueConstraintIsExercised applies the completeness rule to the unique indexes.
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

// TestUniqueConstraintsReject requires each unique index to refuse its own duplicate, by name.
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

// TestUniqueConstraintsAdmitTheNeighbour proves each index is scoped as intended: the
// near-duplicate differing in the one dimension it does not cover must be accepted.
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
func liveCheckConstraints(t *testing.T) []string {
	t.Helper()

	rows, err := schemaPool(t).Query(t.Context(), `
		-- contype = 'c' already excludes NOT NULL, which PostgreSQL 18 records as contype = 'n'.
		-- Keyed on the bare name, which TestCheckConstraintNamesAreUnique holds globally unique.
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

// liveUniqueIndexes returns the unique indexes that encode a business rule, primary keys apart.
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
		  -- Exclude FK-support indexes: a unique index containing the table's primary key is
		  -- unique by virtue of the PK and cannot be violated in isolation — the PK fires first.
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

// checkCase pairs one CHECK with a statement it must refuse and a neighbour it must accept.
type checkCase struct {
	constraint string
	reject     string
	accept     string
	// acceptNote explains why no accepting statement exists.
	acceptNote string
}

// uniqueCase pairs one unique index with a duplicate it must refuse and a neighbour it admits.
type uniqueCase struct {
	index      string
	reject     string
	accept     string
	acceptNote string
}

// constraintViolation extracts the SQLSTATE and constraint name PostgreSQL reported.
func constraintViolation(err error) (code, constraint string) {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return "", ""
	}
	return pgErr.Code, pgErr.ConstraintName
}

// Privilege conformance. Every other case in this package connects as the OWNER, who is subject
// to no missing grant, so this block is the only thing that can see the model is porous.

// appWritableThroughDefiner names the tables a SECURITY DEFINER function writes that `store`
// may STILL write directly, with the reason.
var appWritableThroughDefiner = map[string]string{
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

// TestEveryDefinerWrittenTableIsRevoked holds the rule that a table a SECURITY DEFINER function
// writes is one the app writes only through it, derived from the catalog rather than a list.
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
				// Scoped to INSERT: the exception is "created on first use", not "unguarded".
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

// definerWrittenTables is every table a SECURITY DEFINER function inserts into, read from
// prosrc — a text search, so it would miss a dynamic INSERT; goen has none.
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

// TestReportingCannotReadCredentialsOrPII is the only counterweight to GRANT SELECT ON ALL
// TABLES TO reporting: every other privilege guard asks about a write, or asserts positively
// that a role CAN read, which cannot fail on a grant that is too wide.
func TestReportingCannotReadCredentialsOrPII(t *testing.T) {
	// An entry here is a claim somebody read the table and meant it.
	allowed := map[string]string{
		"order_events":  "a status timeline: no contact detail, and the note is the shop's own words",
		"audit_events":  "the back-office trail, which records WHO acted and never what a customer wrote",
		"media_objects": "image bytes and their digests; 'digest' matches the pattern and is content addressing",
	}

	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT DISTINCT c.table_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public'
		  AND t.table_type = 'BASE TABLE'
		  AND (c.column_name ~ '(token|secret|password|digest|payload)'
		       OR c.column_name LIKE '%\_hash'
		       OR c.column_name IN ('email', 'phone', 'street', 'full_name',
		                            'recipient_name', 'pickup_store_code', 'tax_id'))
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read the catalog: %v", err)
	}
	defer rows.Close()

	var sensitive []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		sensitive = append(sensitive, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(sensitive) == 0 {
		t.Fatal("the catalog reported no table holding a credential or a contact " +
			"detail, which cannot be true of this schema — the query has stopped " +
			"matching and this guard is asserting nothing")
	}

	for _, table := range sensitive {
		if why, ok := allowed[table]; ok {
			t.Logf("%s is allowed: %s", table, why)
			continue
		}
		if hasTablePriv(t, "reporting", table, "SELECT") {
			t.Errorf("reporting may SELECT %s, which holds a credential or a "+
				"customer's contact details. A read-only dashboard role is the one "+
				"most likely to be pointed at a BI tool, a notebook or a contractor; "+
				"it reads aggregates, not secrets. Add it to the REVOKE SELECT list "+
				"in migrations/001, or to this test's allowlist with a reason.", table)
		}
	}

	// The control: a blanket revoke would satisfy every assertion above.
	if !hasTablePriv(t, "reporting", "orders", "SELECT") {
		t.Error("reporting cannot read orders; there is no dashboard left to build")
	}
	if !hasTablePriv(t, "reporting", "committed_orders", "SELECT") {
		t.Error("reporting cannot read committed_orders, which every report joins")
	}
}

// TestAppendOnlyTablesDenyUpdateDelete requires every table a forbid_change trigger declares
// history to also deny store UPDATE and DELETE. INSERT is not asserted: some are app-appended.
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

// TestStoreCannotDisableTriggers proves the guards cannot be switched off from the application
// role: session_replication_role is superuser-only, so the SET must be refused.
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

// TestStoreCannotBadgeAProductAnswerAsTheShop binds the column grant itself.
// The storefront query omits is_staff, but a future raw writer must not be able
// to turn a customer's words into an official answer by naming the column.
func TestStoreCannotBadgeAProductAnswerAsTheShop(t *testing.T) {
	ctx := t.Context()
	tx, beginErr := schemaPool(t).Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, fixtureErr := tx.Exec(ctx, fixtures); fixtureErr != nil {
		t.Fatalf("load fixtures: %v", fixtureErr)
	}
	const questionID = "ab000001-0000-4000-8000-000000000001"
	if _, seedErr := tx.Exec(ctx, `
		INSERT INTO product_questions (id, product_id, user_id, body)
		VALUES ($1, '33333333-3333-4333-8333-333333333333',
		        '55555555-5555-4555-8555-555555555555', '這是顧客的問題')`, questionID); seedErr != nil {
		t.Fatalf("seed question: %v", seedErr)
	}
	if _, roleErr := tx.Exec(ctx, "SET LOCAL ROLE store"); roleErr != nil {
		t.Fatalf("set role: %v", roleErr)
	}
	_, insertErr := tx.Exec(ctx, `
		INSERT INTO product_answers (question_id, user_id, body, is_staff)
		VALUES ($1, '55555555-5555-4555-8555-555555555555', '冒充店家', true)`, questionID)
	pgErr, ok := errors.AsType[*pgconn.PgError](insertErr)
	if !ok || pgErr.Code != "42501" {
		t.Fatalf("store explicit is_staff INSERT failed with %v, want PgError 42501", insertErr)
	}
}

// TestStoreIsNotSuperuser proves SET ROLE store drops superuser: a superuser session ignores
// every REVOKE above.
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

// TestStoreHasNoTempPrivilege locks the other half of the pg_temp fix: with TEMP revoked,
// store cannot create the shadowing table at all, independent of search_path pinning.
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

// TestEveryStoredFunctionEndsSearchPathWithPgTemp holds pg_temp LAST in every function's
// search_path: it is searched FIRST for relations unless listed explicitly, so a pin of
// "pg_catalog, public" reads as fixed and leaves the shadow open. Every language, not plpgsql.
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

// TestSearchPathPinDefeatsTempShadowing is the behavioural proof behind the catalog gate above:
// a decoy pg_temp.categories is planted and a cycle written, which the guard misses if it reads
// the decoy. It runs as the OWNER because `store` holds no write on categories to reach it with.
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

// TestStoreCannotDeleteUsers locks the erasure path: a direct DELETE leaves delivery PII on
// the orders behind, so DELETE is revoked and erase_user is the only door.
func TestStoreCannotDeleteUsers(t *testing.T) {
	if goenAppHasTablePriv(t, "users", "DELETE") {
		t.Error("store can DELETE users directly, bypassing erase_user and leaving PII behind")
	}
	// UPDATE is a COLUMN grant, so only the per-column question can see that a whole-table
	// grant answering "yes" also admits `UPDATE users SET role='admin'`.
	for _, col := range []string{"password_hash", "full_name", "phone", "email"} {
		if !goenAppHasColumnPriv(t, "users", col, "UPDATE") {
			t.Errorf("store cannot UPDATE users.%s; the storefront writes it", col)
		}
	}
	if goenAppHasColumnPriv(t, "users", "role", "UPDATE") {
		t.Error("store can UPDATE users.role — a storefront request is one statement " +
			"from making itself an admin")
	}
	// The same hole from the INSERT side, which is the easier one to forget.
	if goenAppHasColumnPriv(t, "users", "role", "INSERT") {
		t.Error("store can INSERT users.role — a registration could name its own role")
	}
}

// TestAdminStaffWritesUseNarrowFunctions keeps roster mutation behind the two
// SECURITY DEFINER doors that own lock order, credential neutralisation and
// session cleanup. A column grant would let a future query split those acts.
func TestAdminStaffWritesUseNarrowFunctions(t *testing.T) {
	for _, column := range []string{"email", "full_name", "role"} {
		for _, privilege := range []string{"INSERT", "UPDATE"} {
			if roleHasColumnPriv(t, "admin", "users", column, privilege) {
				t.Errorf("admin can %s users.%s directly; staff changes must use their narrow functions",
					privilege, column)
			}
		}
	}

	for _, function := range []string{"upsert_staff(text,text,text)", "revoke_staff(uuid)"} {
		var allowed bool
		if err := schemaPool(t).QueryRow(t.Context(),
			`SELECT has_function_privilege('admin', $1, 'EXECUTE')`, function).Scan(&allowed); err != nil {
			t.Fatalf("read admin EXECUTE on %s: %v", function, err)
		}
		if !allowed {
			t.Errorf("admin cannot execute %s", function)
		}
	}
	for _, internalFunction := range []string{"lock_admin_roster()", "secure_promoted_account(uuid)"} {
		var allowed bool
		if err := schemaPool(t).QueryRow(t.Context(),
			`SELECT has_function_privilege('admin', $1, 'EXECUTE')`, internalFunction).Scan(&allowed); err != nil {
			t.Fatalf("read admin EXECUTE on %s: %v", internalFunction, err)
		}
		if allowed {
			t.Errorf("admin can execute internal roster primitive %s directly", internalFunction)
		}
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

// TestNoStoredFunctionIsPublicExecute refuses a PUBLIC EXECUTE grant: reporting could otherwise
// call a SECURITY DEFINER posting function and write stock through it.
func TestNoStoredFunctionIsPublicExecute(t *testing.T) {
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT p.proname
		FROM pg_proc p
		WHERE p.pronamespace = 'public'::regnamespace
		  AND p.prokind IN ('f', 'p')
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e'
		  )
		  AND EXISTS (
		      -- NULL is not "no privileges": for a new function it means the
		      -- default ACL, which grants EXECUTE to PUBLIC. Include that default
		      -- so a function appended below the migration's final sweep is seen.
		      SELECT 1 FROM aclexplode(coalesce(p.proacl, acldefault('f', p.proowner))) a
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

// adminForbiddenTables is what the back office may NOT write directly. product_variants is
// absent because admin maintains the catalogue; stock_quantity is a column rule asserted below.
var adminForbiddenTables = []string{
	"payments", "refunds", "inventory_movements",
	"inventory_reservations", "store_credit_entries", "audit_events",
	"order_number_counters",
}

// TestAdminCannotWriteMoneyOrStockDirectly is the back office's half: `admin` is a wider set
// than store, not an unbounded one.
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

// TestAdminCannotSetStockQuantity is the narrower rule a table-level grant silently defeats:
// PostgreSQL reads table-level UPDATE as permission on every column, so a column-level REVOKE
// written against one does nothing while reading as a rule.
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

// TestAdminIsNotASuperuser is the same guard the connecting role gets.
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

// TestNoRoleCanWriteStockDirectly proves stock has one door, per role and per VERB: with UPDATE
// revoked and INSERT left alone, admin conjures a variant carrying stock at birth.
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

	// The control: a blanket revoke would pass every assertion above and break the back office.
	if !roleHasColumnPriv(t, "admin", "product_variants", "sku", "INSERT") {
		t.Error("admin cannot insert a variant's sku; the back office cannot add a product")
	}
	if !roleHasColumnPriv(t, "admin", "product_variants", "price_cents", "UPDATE") {
		t.Error("admin cannot reprice a variant; the back office cannot run the shop")
	}
}

// TestAdminHasNoDirectWriteToMoney is the back office's mirror of the store money revokes.
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

// TestNoGrantNamesARoleThatDoesNotExistYet reads the ordering out of the file. PostgreSQL is
// the real lock — the migration fails outright — but its error names the role, not the line.
func TestNoGrantNamesARoleThatDoesNotExistYet(t *testing.T) {
	schema, err := os.ReadFile("../../migrations/001_initial_schema.up.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	src := string(schema)

	created := map[string]int{}
	for _, m := range regexp.MustCompile(`(?m)CREATE ROLE ([a-z_]+)`).FindAllStringSubmatchIndex(src, -1) {
		created[src[m[2]:m[3]]] = m[0]
	}
	if len(created) < 4 {
		t.Fatalf("only %d roles found; the parser is not reading the schema", len(created))
	}

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
				// goen is the owner, created by the deployment rather than by this file.
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
