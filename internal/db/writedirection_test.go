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

// TestNoRoleHoldsAWriteItsQueriesNeverMake holds the rule that a role may have
// INSERT, UPDATE or DELETE on a table only if some query that role runs writes it.
// Which role performs a write is decided by the pool a store is constructed on,
// which lives in Go and not in SQL — hence the pool maps below.
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
// down. `store` writes `users` legitimately, so a whole-table guard cannot see
// that it also reaches users.role; only has_column_privilege can.
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
				// The filter is on the default EXPRESSION, never on its presence:
				// excluding every column that HAS a default would also excuse
				// users.role, whose default is the constant 'customer'.
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

// columnNarrowed is the tables whose column grants are deliberate, and which this
// guard therefore holds to being complete and no wider. It is a scope, not an
// allowlist: a table absent from it is still covered by the table-level guard.
var columnNarrowed = map[string]bool{
	"users":                  true,
	"sessions":               true,
	"product_variants":       true,
	"payment_webhook_events": true,
	"product_reviews":        true,
	"product_questions":      true,
	"product_answers":        true,
	"return_requests":        true,
	"orders":                 true,
	"contact_messages":       true,
	"stock_notifications":    true,
	"order_private_data":     true,
}

// columnExemptions is a column privilege a role holds that its queries never
// exercise, with the reason. Keyed role.table.column.
var columnExemptions = map[string]string{
	"store.users.id":                             "supplied by the default; naming no column grant for it would trap the next INSERT",
	"store.users.created_at":                     "supplied by the default",
	"store.users.updated_at":                     "maintained by set_updated_at",
	"admin.users.id":                             "supplied by the default",
	"admin.users.created_at":                     "supplied by the default",
	"admin.users.updated_at":                     "maintained by set_updated_at",
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
// exercise, with the reason. Keyed role.table so an exemption cannot spread.
var writeExemptions = map[string]string{
	"store.store_credit_accounts": "created on first use; every other verb is revoked and the ledger goes through post_store_credit",
	"admin.store_credit_accounts": "created on first use when the back office grants credit to a customer with no account yet",
}

// generatedQuery matches one sqlc-generated query constant and its SQL body. The
// generated file is the source rather than each feature's query.sql: sqlc emits one
// db package, so any store may call any query whichever .sql file declared it.
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

// packageWrites is every table the given feature package writes, derived from the
// generated queries it calls. Test files are excluded: counting one would let a
// fixture justify a production privilege.
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

// writeTarget matches the table an INSERT, UPDATE or DELETE names. Coarse in the
// safe direction: over-reporting a write only makes the guard more permissive.
var writeTarget = regexp.MustCompile(
	`(?is)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)

// writeTargets is every table the given SQL writes.
func writeTargets(src string) map[string]bool {
	out := map[string]bool{}
	for _, m := range writeTarget.FindAllStringSubmatch(stripSQLComments(src), -1) {
		out[m[1]] = true
	}
	// `UPDATE SET` means the pattern caught a fragment rather than a statement.
	delete(out, "set")
	return out
}

// insertColumns matches an INSERT that names its columns.
var insertColumns = regexp.MustCompile(
	`(?is)\bINSERT\s+INTO\s+([a-z_][a-z0-9_]*)\s*\(([^)]*)\)`)

// bareInsert matches an INSERT that does NOT name its columns, which means every
// column and therefore no useful expectation.
var bareInsert = regexp.MustCompile(
	`(?is)\bINSERT\s+INTO\s+([a-z_][a-z0-9_]*)\s+(?:VALUES|SELECT|DEFAULT)\b`)

// updateSet matches an UPDATE's target and its SET clause up to the first
// clause that ends it.
var updateSet = regexp.MustCompile(
	`(?is)\bUPDATE\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)\s+SET\s+(.*?)(?:\bWHERE\b|\bRETURNING\b|\bFROM\b|;|$)`)

// assignedColumn matches the identifier on the left of one SET assignment.
var assignedColumn = regexp.MustCompile(`(?i)(?:^|,)\s*([a-z_][a-z0-9_]*)\s*=`)

// columnWrites is the columns each table's writes name. A table maps to nil when
// some statement writes it without naming columns: nil means UNKNOWN, and unknown
// degrades that table to the table-level check rather than inventing expectations.
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
// read as a write: sqlc's doc comment sits above each query and names the table.
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

// storefrontPackages run on the pool that does SET ROLE store. Mirrors cmd/goen's
// wiring; a package in both maps has halves on two pools.
var storefrontPackages = []string{
	"account", "cart", "catalog", "contact", "home", "loyalty", "media",
	"newsletter", "outbox", "payment", "product", "returns", "site", "warranty",
}

// backOfficePackages run on the pool that does SET ROLE admin.
var backOfficePackages = []string{
	"admin", "media", "newsletter", "outbox", "twofactor", "invoice",
}

// maintenancePackages run on the pool that does SET ROLE maintenance. The role
// holds no table write at all: refresh_copurchases is SECURITY DEFINER, so who
// may call it is the entire control.
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
		// A dashboard reads: the empty set is the expectation, not an oversight.
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

// TestThePoolMapIsComplete refuses a feature package named by neither map, which
// would otherwise opt that feature's tables out of the guard entirely.
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
// longer held, by identity rather than by count.
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

// sortedKeys is the map's keys in a stable order.
func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
