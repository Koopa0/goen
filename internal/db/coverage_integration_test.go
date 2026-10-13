//go:build integration

package db_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/icons"
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
		LEFT JOIN pg_class tbl ON tbl.oid = c.conrelid
		WHERE c.contype = 'c'
		  AND c.connamespace = 'public'::regnamespace
		  AND (c.conrelid <> 0 OR c.contypid <> 0)
		  AND tbl.relname IS DISTINCT FROM 'schema_migrations'
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

// TestPickupChainChoicesMatchDatabaseContract binds the customer-facing closed
// set to the schema allowlist. A new form choice is not usable unless the
// database admits it, and widening the CHECK must not make arbitrary carrier
// routing codes valid at the write boundary.
func TestPickupChainChoicesMatchDatabaseContract(t *testing.T) {
	chains := pickup.Offered()
	if len(chains) == 0 {
		t.Fatal("pickup.Offered() is empty; this test would prove nothing")
	}

	for _, chain := range chains {
		t.Run(string(chain), func(t *testing.T) {
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
				    pickup_chain = $1, pickup_store_code = 'TEST01',
				    pickup_store_name = '契約測試門市'
				WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'`, string(chain)); err != nil {
				t.Fatalf("schema refused offered pickup chain %q: %v", chain, err)
			}

			var stored string
			if err := tx.QueryRow(ctx, `
				SELECT pickup_chain
				FROM order_private_data
				WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'`).Scan(&stored); err != nil {
				t.Fatalf("read stored pickup chain: %v", err)
			}
			if stored != string(chain) {
				t.Fatalf("stored pickup chain = %q, want offered value %q", stored, chain)
			}
		})
	}

	err := run(t, `
		UPDATE order_private_data
		SET postal_code = NULL, city = NULL, district = NULL, street = NULL,
		    pickup_chain = 'other_chain', pickup_store_code = 'TEST01',
		    pickup_store_name = '契約測試門市'
		WHERE order_id = '6666aaaa-6666-4666-8666-666666666666'`)
	if err == nil {
		t.Fatal("database accepted an unknown pickup chain")
	}
	code, name := constraintViolation(err)
	if code != "23514" || name != "order_private_data_pickup_chain_known" {
		t.Fatalf("unknown pickup chain refused by SQLSTATE %s constraint %q, want 23514/order_private_data_pickup_chain_known: %v",
			code, name, err)
	}
}

// TestCarriersMatchDatabaseContract binds the carriers a dispatch can name to
// the allowlist order_shipments_carrier_known: a carrier the form offers and the
// CHECK refuses fails the dispatch that chose it.
func TestCarriersMatchDatabaseContract(t *testing.T) {
	home, _ := carrier.ForDelivery("", false)
	stores, _ := carrier.ForDelivery("", true)
	every := slices.Concat(home, stores)
	if len(every) == 0 {
		t.Fatal("no carrier on offer; this test would prove nothing")
	}
	for _, c := range every {
		t.Run(string(c), func(t *testing.T) {
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
				INSERT INTO order_shipments (order_id, carrier, tracking_number)
				VALUES ('66666666-6666-4666-8666-666666666666', $1, 'CONTRACT-1')`, string(c)); err != nil {
				t.Fatalf("schema refused carrier %q: %v", c, err)
			}
		})
	}

	err := run(t, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ('66666666-6666-4666-8666-666666666666', 'T-cat', 'CONTRACT-2')`)
	if err == nil {
		t.Fatal("database accepted a carrier outside the closed set")
	}
	if code, name := constraintViolation(err); code != "23514" || name != "order_shipments_carrier_known" {
		t.Fatalf("unknown carrier refused by SQLSTATE %s constraint %q, want 23514/order_shipments_carrier_known: %v",
			code, name, err)
	}
}

// TestCategoryIconKeysMatchDatabaseContract binds the glyphs the renderer can
// draw to the keys categories_icon_key_known admits. A key the picker offers
// and the CHECK refuses fails the back-office form that offered it; a key the
// CHECK admits and the renderer cannot draw is an empty tile.
func TestCategoryIconKeysMatchDatabaseContract(t *testing.T) {
	keys := icons.CategoryKeys()
	if len(keys) == 0 {
		t.Fatal("icons.CategoryKeys() is empty; this test would prove nothing")
	}

	var def string
	if err := schemaPool(t).QueryRow(t.Context(), `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conname = 'categories_icon_key_known'`).Scan(&def); err != nil {
		t.Fatalf("read categories_icon_key_known: %v", err)
	}
	literals := regexp.MustCompile(`'([^']*)'::text`).FindAllStringSubmatch(def, -1)
	admitted := make([]string, 0, len(literals))
	for _, m := range literals {
		admitted = append(admitted, m[1])
	}
	slices.Sort(admitted)
	drawn := slices.Sorted(slices.Values(keys))
	if !slices.Equal(admitted, drawn) {
		t.Fatalf("categories_icon_key_known admits %v; icons.CategoryKeys() draws %v\n%s",
			admitted, drawn, def)
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
		LEFT JOIN pg_class t ON t.oid = c.conrelid
		WHERE c.contype = 'c'
		  AND c.connamespace = 'public'::regnamespace
		  AND (c.conrelid <> 0 OR c.contypid <> 0)
		  AND t.relname IS DISTINCT FROM 'schema_migrations'
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

// Every other case in this package connects as the OWNER, who is subject to no missing
// grant, so nothing but the cases below can see whether the privilege model is porous.

// appWritableThroughDefiner names the tables a SECURITY DEFINER function writes that `store`
// may STILL write directly, with the reason.
var appWritableThroughDefiner = map[string]string{
	"store_credit_accounts": "created on first use; UPDATE is what is revoked",
}

// sharedCleanup is a verb a cleanup-only SECURITY DEFINER function performs that
// the role's own production queries perform too, keyed role.table.VERB, with the
// caller. The definer is not the only door here: revoking the verb would break
// the caller, and an entry the scan never reaches fails as stale.
var sharedCleanup = map[string]string{
	"store.sessions.DELETE": "sign-out, a password change or reset, and the expiry sweep end sessions directly (DeleteSession, " +
		"DeleteUserSessions, DeleteExpiredSessions); revoke_staff and secure_promoted_account end them too",
	"admin.sessions.DELETE": "removing a staff member's TOTP ends their sessions (RemoveTOTPAndSessions); " +
		"revoke_staff and secure_promoted_account end them too",
	"store.outbox_messages.DELETE": "the relay sweeps delivered rows and expired undelivered ones (SweepDeliveredMessages, SweepUndeliveredMessages) and account " +
		"flows drop superseded letters; erase_user drops a customer's messages",
	"store.order_access_grants.DELETE": "the retention sweep deletes grants nobody can present " +
		"(DeleteOldOrderAccessGrants); erase_user deletes a customer's",
	"store.newsletter_confirmations.DELETE": "confirming spends the token (SpendNewsletterConfirmation); " +
		"erase_user deletes a customer's",
}

type dynamicDefinerContract struct {
	reason string
	insert []string
	update []string
	delete []string
}

// Dynamic targets cannot be derived from prosrc. Contracts use regprocedure
// signatures so an overload cannot inherit another function's exception.
var dynamicDefinerContracts = map[string]dynamicDefinerContract{}

func goenAppHasTablePriv(t *testing.T, table, priv string) bool {
	t.Helper()
	var ok bool
	if err := schemaPool(t).QueryRow(t.Context(),
		"SELECT has_table_privilege('store', $1, $2)", table, priv).Scan(&ok); err != nil {
		t.Fatalf("has_table_privilege(store, %q, %q): %v", table, priv, err)
	}
	return ok
}

// TestEveryDefinerWrittenTableIsRevoked enforces exclusive ledger writes and
// requires an ownership decision for newly discovered cleanup overlaps.
func TestEveryDefinerWrittenTableIsRevoked(t *testing.T) {
	tables := definerWrittenTables(t)
	byVerb := map[string][]string{
		"INSERT": definerTargetTables(t, `INSERT\s+INTO\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`),
		"UPDATE": definerTargetTables(t, `UPDATE\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`),
		"DELETE": definerTargetTables(t, `DELETE\s+FROM\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`),
	}
	for verb, targets := range dynamicDefinerWriteTargets(t) {
		byVerb[verb] = append(byVerb[verb], targets...)
		tables = append(tables, targets...)
	}
	slices.Sort(tables)
	tables = slices.Compact(tables)
	if len(tables) < 8 {
		t.Fatalf("only %d definer-written tables found; the catalog query is not "+
			"finding them and this test would pass on nothing", len(tables))
	}

	consulted := map[string]bool{}
	for _, table := range tables {
		for _, role := range []string{"store", "admin"} {
			for _, priv := range []string{"INSERT", "UPDATE", "DELETE"} {
				if !hasTablePriv(t, role, table, priv) {
					continue
				}
				// Inserting ledger functions have an exclusive-door contract.
				// A cleanup-only definer does not establish that same contract;
				// its overlap needs a decision before treating callers as forbidden.
				if !slices.Contains(byVerb["INSERT"], table) {
					if !slices.Contains(byVerb[priv], table) {
						continue
					}
					key := role + "." + table + "." + priv
					if why, shared := sharedCleanup[key]; shared {
						consulted[key] = true
						t.Logf("%s may %s %s: %s", role, priv, table, why)
						continue
					}
					t.Errorf("unclassified definer/direct-write overlap: %s holds %s on %s.\n"+
						"  Find the production caller. If the role needs the verb, name it in "+
						"sharedCleanup with that caller; if not, revoke it and let the "+
						"definer be the only door.", role, priv, table)
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
	for key, why := range sharedCleanup {
		if !consulted[key] {
			t.Errorf("sharedCleanup has %q (%s), but no cleanup-only definer and that "+
				"role share the verb any more. Remove the entry.", key, why)
		}
	}
}

func definerWrittenTables(t *testing.T) []string {
	t.Helper()
	return definerTargetTables(t, `(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)
}

func dynamicDefinerWriteTargets(t *testing.T) map[string][]string {
	t.Helper()
	targets := map[string][]string{}
	consulted := map[string]bool{}
	for _, signature := range dynamicDefiners(t) {
		contract, ok := dynamicDefinerContracts[signature]
		if !ok {
			t.Errorf("SECURITY DEFINER %s uses EXECUTE with no dynamicDefinerContracts entry; "+
				"declare its reason and INSERT/UPDATE/DELETE targets before trusting the write-target scan", signature)
			continue
		}
		consulted[signature] = true
		if strings.TrimSpace(contract.reason) == "" {
			t.Errorf("dynamicDefinerContracts entry for %s has no reason", signature)
		}
		for verb, tables := range map[string][]string{
			"INSERT": contract.insert,
			"UPDATE": contract.update,
			"DELETE": contract.delete,
		} {
			for _, table := range tables {
				var exists bool
				if err := schemaPool(t).QueryRow(t.Context(), `
					SELECT EXISTS (SELECT 1 FROM pg_tables
					WHERE schemaname = 'public' AND tablename = $1)`, table).Scan(&exists); err != nil {
					t.Fatalf("read dynamic write target %q for %s: %v", table, signature, err)
				}
				if !exists {
					t.Errorf("dynamicDefinerContracts entry for %s names absent table %q", signature, table)
					continue
				}
				targets[verb] = append(targets[verb], table)
			}
		}
	}
	for signature := range dynamicDefinerContracts {
		if !consulted[signature] {
			t.Errorf("dynamicDefinerContracts has %q, but no public SECURITY DEFINER uses EXECUTE "+
				"under that signature any more. Remove the entry.", signature)
		}
	}
	return targets
}

func dynamicDefiners(t *testing.T) []string {
	t.Helper()
	// Even a literal EXECUTE needs a contract: concatenation can append an unseen target.
	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT p.oid::regprocedure::text
		FROM pg_proc p
		WHERE p.prosecdef AND p.pronamespace = 'public'::regnamespace
		  AND regexp_replace(regexp_replace(p.prosrc, '/\*.*?\*/', ' ', 'gs'), '--[^\n]*', ' ', 'g')
		      ~* '\mEXECUTE\M(?!\s+(?:FUNCTION|PROCEDURE)\M)'
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("read dynamic SECURITY DEFINER functions: %v", err)
	}
	defer rows.Close()
	var signatures []string
	for rows.Next() {
		var signature string
		if err := rows.Scan(&signature); err != nil {
			t.Fatalf("scan dynamic SECURITY DEFINER function: %v", err)
		}
		signatures = append(signatures, signature)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate dynamic SECURITY DEFINER functions: %v", err)
	}
	return signatures
}

func definerTargetTables(t *testing.T, pattern string) []string {
	t.Helper()
	rows, err := schemaPool(t).Query(t.Context(), `
		WITH written AS (
			SELECT DISTINCT lower(m[1]) AS tbl
			FROM pg_proc p,
			     LATERAL regexp_matches(
				 regexp_replace(regexp_replace(p.prosrc, '/\*.*?\*/', '', 'gs'), '--[^\n]*', '', 'g'),
				 $1, 'gi') m
			WHERE p.prosecdef AND p.pronamespace = 'public'::regnamespace
		)
		SELECT w.tbl FROM written w
		JOIN pg_tables t ON t.tablename = w.tbl AND t.schemaname = 'public'
		ORDER BY 1`, pattern)
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
//
// It asks of every COLUMN whose type can hold words, not of column names: what a customer
// typed is called customer_note on an order and reason on a return, and no pattern over names
// refuses either. A column readable here and absent from the list fails until somebody has
// read it and decided it is business data.
func TestReportingCannotReadCredentialsOrPII(t *testing.T) {
	const (
		published   = "copy the shop publishes on the storefront"
		shopWords   = "bookkeeping in the shop's own words and closed vocabularies"
		customerPub = "written by a customer to be published on the product page"
	)
	// An entry here is a claim somebody read the column and meant it.
	readable := map[string]reportingText{
		"audit_events": {[]string{"action", "actor_kind", "after", "before", "entity_table", "request_id"},
			"the back-office trail, which records WHO acted and never what a customer wrote"},
		"brands":     {[]string{"name", "slug"}, published},
		"categories": {[]string{"icon_key", "image_alt", "image_alt_en", "image_key", "name", "name_en", "slug", "tone"}, published},
		"checkout_attempts": {[]string{"idempotency_key"},
			"a server-issued replay key, which opens nothing without the cart cookie it is bound to"},
		"coupons": {[]string{"code", "description", "kind"}, published},
		"faq_entries": {[]string{"answer", "answer_en", "category", "category_en",
			"question", "question_en"}, published},
		"hero_slides": {[]string{"body", "body_en", "eyebrow", "eyebrow_en", "headline",
			"headline_en", "image_alt", "image_alt_en", "image_key", "primary_cta_href",
			"primary_cta_label", "primary_cta_label_en", "secondary_cta_href",
			"secondary_cta_label", "secondary_cta_label_en"}, published},
		"inventory_movements":    {[]string{"idempotency_key", "reason", "source_type"}, shopWords},
		"inventory_reservations": {[]string{"state"}, shopWords},
		"invoice_document_lines": {[]string{"description", "tax_type", "unit"},
			"what a 統一發票 itemised: the catalogue snapshot and a closed vocabulary"},
		"invoice_documents": {[]string{"kind", "number", "provider_ref", "request_key", "status"},
			"the filed document's numbers and state; the buyer's identity is in invoice_preferences"},
		"loyalty_entries": {[]string{"idempotency_key", "kind", "reason"}, shopWords},
		"media_objects": {[]string{"bytes", "content_type", "digest"},
			"catalogue images, content-addressed by their digest"},
		"membership_tiers":  {[]string{"code", "name", "name_en"}, published},
		"newsletter_issues": {[]string{"body", "subject"}, "what the shop mailed to its list"},
		"order_events": {[]string{"kind"},
			"a status timeline in a closed vocabulary; the note staff type is withheld"},
		"order_lines": {[]string{"product_name", "sku", "variant_label", "warranty_note", "tax_type", "invoice_unit"},
			"the catalogue snapshot taken at purchase"},
		"order_refunds":   {[]string{"order_number"}, "the number a customer quotes to support"},
		"order_shipments": {[]string{"carrier", "tracking_number"}, "the carrier's parcel reference"},
		"orders": {[]string{"currency", "fulfillment_status", "locale", "order_number",
			"shipping_method_code", "shipping_method_name"},
			"closed vocabularies, the order number and the shipping snapshot; the staff note is withheld"},
		"payments": {[]string{"card_brand", "card_last4", "currency", "provider", "provider_ref", "status"},
			"the provider's references, and a card brand and last four, which identify nobody"},
		"product_answers":       {[]string{"body"}, "published under the question it answers"},
		"product_images":        {[]string{"alt_text", "alt_text_en", "storage_key"}, published},
		"product_option_values": {[]string{"swatch_hex", "value", "value_en"}, published},
		"product_options":       {[]string{"name", "name_en"}, published},
		"product_questions":     {[]string{"body"}, customerPub},
		"product_reviews":       {[]string{"body", "title"}, customerPub},
		"product_specs":         {[]string{"label", "label_en", "value", "value_en"}, published},
		"product_variants":      {[]string{"sku"}, published},
		// Responsible-party contacts are shop-authored public label facts, not customer contact records.
		"products": {[]string{"description", "description_en", "name", "name_en", "slug",
			"status", "summary", "summary_en", "warranty_note", "origin", "origin_en",
			"domestic_party_name", "domestic_party_phone", "domestic_party_address", "net_unit", "tax_type", "invoice_unit"},
			"shop catalogue copy and public label facts, plus product tax vocabulary and invoice units; no buyer identity"},
		"promo_banners": {[]string{"code", "cta_href", "cta_label", "cta_label_en", "message",
			"message_en", "message_short", "message_short_en"}, published},
		"refunds": {[]string{"provider_ref", "reason", "request_key", "status"},
			"the provider's references; the reason is the shop's resolution, not the customer's"},
		"return_eligibility_assessments": {[]string{"basis"}, "the staff member's own assessment"},
		"return_eligibility_facts": {[]string{"accessories_complete", "packaging_complete",
			"policy_window", "unused"}, shopWords},
		"return_request_lines": {[]string{"inspection_note"}, "what the shop found in the parcel"},
		"return_requests": {[]string{"resolution", "status"},
			"the shop's decision; the customer's own reason is withheld"},
		"sale_campaigns":           {[]string{"image_alt", "image_alt_en", "image_key", "slug", "title", "title_en", "tone"}, published},
		"shipping_method_versions": {[]string{"carrier", "carrier_en", "name", "name_en"}, published},
		"shipping_methods":         {[]string{"code", "destination_kind"}, published},
		"shipping_zone_prefixes":   {[]string{"prefix"}, published},
		"shipping_zones":           {[]string{"code", "name", "name_en"}, published},
		"store_credit_entries":     {[]string{"idempotency_key", "reason"}, shopWords},
		"visible_reviews":          {[]string{"body", "title"}, customerPub},
		"warranty_registrations": {[]string{"serial_number"},
			"the manufacturer's number on the unit, which the privacy policy retains"},
	}

	rows, err := schemaPool(t).Query(t.Context(), `
		SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_type ty ON ty.oid = a.atttypid
		WHERE c.relnamespace = 'public'::regnamespace
		  AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
		  AND a.attnum > 0 AND NOT a.attisdropped
		  AND has_column_privilege('reporting', c.oid, a.attnum, 'SELECT')
		  -- A number, a truth value, a moment, an enum label or a uuid cannot carry a
		  -- sentence; text, JSON, bytes, arrays and every other type can.
		  AND ty.typcategory NOT IN ('N', 'B', 'D', 'T', 'E')
		  AND a.atttypid <> 'uuid'::regtype
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("read the catalog: %v", err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var table, column, typ string
		if err := rows.Scan(&table, &column, &typ); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[table+"."+column] = true
		if entry, ok := readable[table]; ok && slices.Contains(entry.columns, column) {
			continue
		}
		t.Errorf("reporting may SELECT %s.%s (%s), which can hold whatever a customer "+
			"typed or a credential, and nobody has said it is business data. A read-only "+
			"dashboard role is the one most likely to be pointed at a BI tool, a notebook "+
			"or a contractor; it reads aggregates, not people. Revoke it from reporting in "+
			"migrations/001, granting the table's other columns by name as orders does, "+
			"or name it in this test with the reason.", table, column, typ)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(seen) < 100 {
		t.Fatalf("the catalog reported %d columns reporting can read that hold words, "+
			"which cannot be true of this schema — the query has stopped matching and "+
			"this guard is asserting nothing", len(seen))
	}

	for _, table := range slices.Sorted(maps.Keys(readable)) {
		entry := readable[table]
		for _, column := range entry.columns {
			if !seen[table+"."+column] {
				t.Errorf("this test names %s.%s (%s), which reporting cannot read or which "+
					"no longer holds words. Remove the entry: a list that stops describing "+
					"the grants hides the next column it should have refused.",
					table, column, entry.why)
			}
		}
	}

	// The control: a blanket revoke would satisfy every assertion above.
	for _, c := range []struct{ table, column string }{
		{"orders", "placed_at"},
		{"orders", "discount_cents"},
		{"return_requests", "goods_refund_cents"},
		{"committed_orders", "id"},
	} {
		if !roleHasColumnPriv(t, "reporting", c.table, c.column, "SELECT") {
			t.Errorf("reporting cannot read %s.%s; there is no dashboard left to build",
				c.table, c.column)
		}
	}
}

// reportingText is the columns of one relation that reporting may read and that can hold
// words, with the reason none of them is a person.
type reportingText struct {
	columns []string
	why     string
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

// TestStoreCannotBadgeAProductAnswerAsTheShop binds the grant itself: no
// storefront code writes an answer, so a raw writer must not be able to turn a
// customer's words into an official answer by naming is_staff.
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

// TestStoreCannotWriteOrDeleteProductAnswers holds the revoke itself. Column
// grants are invisible to the table-level guard, so a leftover INSERT (question_id,
// user_id, body) would keep a customer write path alive with no query behind it.
func TestStoreCannotWriteOrDeleteProductAnswers(t *testing.T) {
	const questionID = "ab000002-0000-4000-8000-000000000001"
	for name, statement := range map[string]string{
		"insert": `INSERT INTO product_answers (question_id, user_id, body)
			VALUES ('` + questionID + `', '55555555-5555-4555-8555-555555555555', '顧客的回覆')`,
		"delete": `DELETE FROM product_answers WHERE question_id = '` + questionID + `'`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			tx, beginErr := schemaPool(t).Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin: %v", beginErr)
			}
			defer func() { _ = tx.Rollback(ctx) }()

			if _, fixtureErr := tx.Exec(ctx, fixtures); fixtureErr != nil {
				t.Fatalf("load fixtures: %v", fixtureErr)
			}
			if _, seedErr := tx.Exec(ctx, `
				INSERT INTO product_questions (id, product_id, user_id, body)
				VALUES ($1, '33333333-3333-4333-8333-333333333333',
				        '55555555-5555-4555-8555-555555555555', '這是顧客的問題')`, questionID); seedErr != nil {
				t.Fatalf("seed question: %v", seedErr)
			}
			if _, seedErr := tx.Exec(ctx, `
				INSERT INTO product_answers (question_id, user_id, body)
				VALUES ($1, '55555555-5555-4555-8555-555555555555', '既有的回答')`, questionID); seedErr != nil {
				t.Fatalf("seed answer: %v", seedErr)
			}
			if _, roleErr := tx.Exec(ctx, "SET LOCAL ROLE store"); roleErr != nil {
				t.Fatalf("set role: %v", roleErr)
			}
			_, writeErr := tx.Exec(ctx, statement)
			pgErr, ok := errors.AsType[*pgconn.PgError](writeErr)
			if !ok || pgErr.Code != "42501" {
				t.Fatalf("store %s on product_answers failed with %v, want PgError 42501", name, writeErr)
			}
		})
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

// TestSearchPathPinDefeatsTempShadowing plants a decoy pg_temp.categories and writes a cycle,
// which the guard misses if it reads the decoy. It runs as the OWNER because `store` holds no
// write on categories to reach it with.
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

func TestStoreCannotRewriteTheInvoiceFilingSnapshot(t *testing.T) {
	if goenAppHasTablePriv(t, "invoice_preferences", "UPDATE") {
		t.Error("store can UPDATE invoice_preferences; a storefront request could rewrite the tax filing identity after checkout")
	}
	if !goenAppHasTablePriv(t, "invoice_preferences", "INSERT") {
		t.Error("store cannot INSERT invoice_preferences; checkout cannot create the filing snapshot")
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
	"loyalty_redemption_operations", "order_number_counters",
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
		"inventory_reservations", "store_credit_entries",
		"loyalty_redemption_operations", "order_number_counters",
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

func TestDefinerCorpusIncludesUpdateAndDelete(t *testing.T) {
	ctx := t.Context()
	_, err := pool.Exec(ctx, `
		CREATE TABLE public.guard_update_only (value integer);
		CREATE TABLE public.guard_delete_only (value integer);
		CREATE TABLE public.guard_commented_write (value integer);
		CREATE FUNCTION public.guard_update_only() RETURNS void
		LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp
		AS $$
            -- DELETE FROM guard_commented_write;
            /* INSERT INTO guard_commented_write (value) VALUES (1); */
            UPDATE guard_update_only SET value = 1 $$;
		CREATE FUNCTION public.guard_delete_only() RETURNS void
		LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp
		AS $$ DELETE FROM guard_delete_only $$;
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := pool.Exec(context.WithoutCancel(ctx), `
			DROP FUNCTION public.guard_update_only();
			DROP FUNCTION public.guard_delete_only();
			DROP TABLE public.guard_update_only;
			DROP TABLE public.guard_delete_only;
			DROP TABLE public.guard_commented_write;
		`)
		if err != nil {
			t.Error(err)
		}
	})
	tables := definerWrittenTables(t)
	if slices.Contains(tables, "guard_commented_write") {
		t.Error("definer corpus counted a commented-out write")
	}
	for _, want := range []string{"guard_update_only", "guard_delete_only"} {
		if !slices.Contains(tables, want) {
			t.Errorf("definer corpus omitted %s", want)
		}
	}
}

func TestDefinerCorpusIncludesDynamicSQL(t *testing.T) {
	ctx := t.Context()
	before := dynamicDefiners(t)
	_, err := pool.Exec(ctx, `
		CREATE TABLE public.guard_dynamic_target (value integer);
		CREATE FUNCTION public.guard_dynamic_format() RETURNS void
		LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN
			EXECUTE format('DELETE FROM %I', 'guard_dynamic_target');
		END $$;
		CREATE FUNCTION public.guard_dynamic_concat() RETURNS void
		LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN
			EXECUTE'DELETE FROM ' || quote_ident('guard_dynamic_target');
		END $$;
		CREATE FUNCTION public.guard_dynamic_altered() RETURNS void
		LANGUAGE plpgsql AS $$ BEGIN
			execute /* target chosen at runtime */ format('UPDATE %I SET value = 1', 'guard_dynamic_target');
		END $$;
		ALTER FUNCTION public.guard_dynamic_altered() SECURITY DEFINER;
		CREATE FUNCTION public.guard_dynamic_query() RETURNS SETOF integer
		LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN
			RETURN QUERY EXECUTE format('SELECT value FROM %I', 'guard_dynamic_target');
		END $$;
		CREATE FUNCTION public.guard_dynamic_literal() RETURNS void
		LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN
			EXECUTE 'DELETE FROM guard_dynamic_target';
		END $$;
		CREATE FUNCTION public.guard_dynamic_invoker() RETURNS void
		LANGUAGE plpgsql AS $$ BEGIN
			EXECUTE format('DELETE FROM %I', 'guard_dynamic_target');
		END $$;
		CREATE FUNCTION public.guard_dynamic_commented() RETURNS void
		LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN
			-- EXECUTE format('DELETE FROM %I', 'guard_dynamic_target');
			/* EXECUTE 'DELETE FROM ' || quote_ident('guard_dynamic_target'); */
			NULL;
		END $$;
		CREATE FUNCTION public.guard_dynamic_trigger() RETURNS void
		LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN
			CREATE TRIGGER guard_function AFTER DELETE ON guard_dynamic_target
				FOR EACH ROW EXECUTE  FUNCTION forbid_change('guard_dynamic_target');
			CREATE TRIGGER guard_procedure AFTER DELETE ON guard_dynamic_target
				FOR EACH ROW EXECUTE
				PROCEDURE forbid_change('guard_dynamic_target');
		END $$;
	`)
	if err != nil {
		t.Fatal(err)
	}
	drop := func() {
		_, err := pool.Exec(context.WithoutCancel(ctx), `
			DROP FUNCTION IF EXISTS public.guard_dynamic_format();
			DROP FUNCTION IF EXISTS public.guard_dynamic_concat();
			DROP FUNCTION IF EXISTS public.guard_dynamic_altered();
			DROP FUNCTION IF EXISTS public.guard_dynamic_query();
			DROP FUNCTION IF EXISTS public.guard_dynamic_literal();
			DROP FUNCTION IF EXISTS public.guard_dynamic_invoker();
			DROP FUNCTION IF EXISTS public.guard_dynamic_commented();
			DROP FUNCTION IF EXISTS public.guard_dynamic_trigger();
			DROP TABLE IF EXISTS public.guard_dynamic_target;
		`)
		if err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(drop)
	want := append(slices.Clone(before),
		"guard_dynamic_format()", "guard_dynamic_concat()", "guard_dynamic_altered()",
		"guard_dynamic_query()", "guard_dynamic_literal()")
	slices.Sort(want)
	if got := dynamicDefiners(t); !slices.Equal(got, want) {
		t.Errorf("dynamic SECURITY DEFINER functions = %v, want %v", got, want)
	}
	drop()
	if got := dynamicDefiners(t); !slices.Equal(got, before) {
		t.Errorf("dynamic SECURITY DEFINER functions after removing fixtures = %v, want %v", got, before)
	}
}

// The invoice unit is one domain; each column that holds one and the function that
// compares a provider payload to it must refuse what the domain refuses.
func TestInvoiceUnitDomainRefusesBadUnits(t *testing.T) {
	statements := map[string]string{
		"products":               `INSERT INTO products (brand_id,category_id,slug,name,status,invoice_unit) VALUES ('11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','invoice-facts-case','Invoice fixture','draft',%s);`,
		"order_lines":            `INSERT INTO order_lines (order_id,sku,product_name,unit_price_cents,quantity,position,invoice_unit) VALUES ('6666aaaa-6666-4666-8666-666666666666','SNAPSHOT','Snapshot',100,1,5,%s);`,
		"invoice_document_lines": `INSERT INTO invoice_document_lines (document_id,description,quantity,unit_price_cents,amount_cents,tax_type,unit,position) VALUES ('99990001-0000-4000-8000-000000000000','Unit fixture',1,100,100,'exempt',%s,0);`,
	}
	units := []struct {
		name, literal string
		valid         bool
	}{
		{"six characters", `repeat('箱',6)`, true},
		{"seven characters", `repeat('箱',7)`, false},
		{"trailing newline", `E'個\n'`, false},
		{"blank", `'  '`, false},
	}
	for table, statement := range statements {
		for _, unit := range units {
			t.Run(table+"/"+unit.name, func(t *testing.T) {
				err := run(t, fmt.Sprintf(statement, unit.literal))
				if unit.valid {
					if err != nil {
						t.Fatalf("%s refused %s: %v", table, unit.literal, err)
					}
					return
				}
				code, name := constraintViolation(err)
				if code != "23514" || name != "invoice_unit_valid" {
					t.Fatalf("%s with %s: SQLSTATE %q constraint %q, want 23514 invoice_unit_valid (err %v)", table, unit.literal, code, name, err)
				}
			})
		}
	}
}

func TestInvoiceOperationLinesMatchRefusesBadUnits(t *testing.T) {
	for _, unit := range []struct {
		name string
		json string
		want bool
	}{
		{"six characters", `"箱箱箱箱箱箱"`, true},
		{"seven characters", `"箱箱箱箱箱箱箱"`, false},
		{"control character", `"個\n"`, false},
		{"blank", `" "`, false},
		{"absent", `null`, false},
	} {
		t.Run(unit.name, func(t *testing.T) {
			payload := `{"lines":[{"tax_type":"taxable","unit":` + unit.json + `,"description":"d","quantity":1,"unit_price_cents":100,"amount_cents":100}]}`
			var got bool
			if err := schemaPool(t).QueryRow(t.Context(),
				`SELECT invoice_operation_lines_match($1::jsonb, ARRAY['d'], ARRAY[1], ARRAY[100::bigint], ARRAY[100::bigint])`, payload).Scan(&got); err != nil {
				t.Fatalf("invoice_operation_lines_match: %v", err)
			}
			if got != unit.want {
				t.Fatalf("invoice_operation_lines_match with unit %s = %v, want %v", unit.json, got, unit.want)
			}
		})
	}
}
