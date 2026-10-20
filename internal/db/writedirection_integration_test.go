//go:build integration

package db_test

import (
	"fmt"
	"go/ast"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"
)

func writableVerbs(t *testing.T, role string) map[string]map[string]bool {
	t.Helper()
	byQuery := queryWriteVerbs(t)
	out := map[string]map[string]bool{}
	for _, pkg := range packagesOn(role) {
		for _, method := range calledQueries(t, pkg) {
			for table, verbs := range byQuery[method] {
				if out[table] == nil {
					out[table] = map[string]bool{}
				}
				for verb := range verbs {
					out[table][verb] = true
				}
			}
		}
	}
	return out
}

func queryWriteVerbs(t *testing.T) map[string]map[string]map[string]bool {
	t.Helper()
	src, err := os.ReadFile("query.sql.go")
	if err != nil {
		t.Fatalf("read the generated queries: %v", err)
	}
	out := map[string]map[string]map[string]bool{}
	for _, m := range generatedQuery.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = writeVerbTargets(m[2])
	}
	return out
}

var insertVerb = regexp.MustCompile(
	`(?is)\bINSERT\s+INTO\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)
var upsertVerb = regexp.MustCompile(`(?is)\bON\s+CONFLICT\b.*?\bDO\s+UPDATE\b`)
var updateVerb = regexp.MustCompile(
	`(?is)\bUPDATE\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)(?:\s+[a-z_][a-z0-9_]*)?\s+SET\b`)
var deleteVerb = regexp.MustCompile(
	`(?is)\bDELETE\s+FROM\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)

// rowLock matches a locking clause and its optional OF list. PostgreSQL checks
// UPDATE on every table each of the four locks, so a lock is an UPDATE use.
var rowLock = regexp.MustCompile(
	`(?is)\bFOR\s+(?:NO\s+KEY\s+UPDATE|UPDATE|KEY\s+SHARE|SHARE)\b` +
		`(?:\s+OF\s+([a-z_][a-z0-9_]*(?:\s*,\s*[a-z_][a-z0-9_]*)*))?`)
var fromTable = regexp.MustCompile(
	`(?is)\b(?:FROM|JOIN)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)

func writeVerbTargets(src string) map[string]map[string]bool {
	clean := stripSQLComments(src)
	out := map[string]map[string]bool{}
	note := func(table, verb string) {
		if table == "set" {
			return
		}
		if out[table] == nil {
			out[table] = map[string]bool{}
		}
		out[table][verb] = true
	}
	inserts := insertVerb.FindAllStringSubmatchIndex(clean, -1)
	for i, match := range inserts {
		table := clean[match[2]:match[3]]
		note(table, "INSERT")
		end := len(clean)
		if i+1 < len(inserts) {
			end = inserts[i+1][0]
		}
		statement := clean[match[1]:end]
		if stop := strings.IndexByte(statement, ';'); stop >= 0 {
			statement = statement[:stop]
		}
		if upsertVerb.MatchString(statement) {
			note(table, "UPDATE")
		}
	}
	for _, m := range updateVerb.FindAllStringSubmatch(clean, -1) {
		note(m[1], "UPDATE")
	}
	for _, m := range deleteVerb.FindAllStringSubmatch(clean, -1) {
		note(m[1], "DELETE")
	}
	for statement := range strings.SplitSeq(clean, ";") {
		for _, table := range lockedTables(statement) {
			note(table, "UPDATE")
		}
	}
	return out
}

// lockedTables is the tables one statement's locking clauses cover: those an OF
// list names, by table or by alias, and without OF every table after FROM or JOIN.
func lockedTables(statement string) []string {
	locks := rowLock.FindAllStringSubmatch(statement, -1)
	if len(locks) == 0 {
		return nil
	}
	var named []string
	for _, m := range fromTable.FindAllStringSubmatch(statement, -1) {
		named = append(named, m[1])
	}
	var out []string
	for _, lock := range locks {
		if lock[1] == "" {
			out = append(out, named...)
			continue
		}
		for ref := range strings.SplitSeq(lock[1], ",") {
			ref = strings.TrimSpace(ref)
			if slices.Contains(named, ref) {
				out = append(out, ref)
				continue
			}
			aliased := regexp.MustCompile(`(?is)\b(?:FROM|JOIN)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)\s+(?:AS\s+)?` +
				regexp.QuoteMeta(ref) + `\b`)
			if m := aliased.FindStringSubmatch(statement); m != nil {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// TestNoRoleHoldsAWriteItsQueriesNeverMake checks each held verb against the
// production calls on that role, so an INSERT cannot justify UPDATE or DELETE.
func TestNoRoleHoldsAWriteItsQueriesNeverMake(t *testing.T) {
	ctx := t.Context()

	for _, role := range []string{"store", "admin", "reporting", "maintenance"} {
		t.Run(role, func(t *testing.T) {
			allowed := writableVerbs(t, role)

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
				for _, verb := range held[table] {
					if allowed[table][verb] {
						continue
					}
					if why, ok := writeExemptions[role+"."+table]; ok && verb == "INSERT" {
						t.Logf("%s may INSERT %s: %s", role, table, why)
						continue
					}
					t.Errorf("%s holds %s on %s, and no production query it runs uses that verb.\n"+
						"  Either a query should use it — in which case the pool map below is "+
						"wrong — or the verb should be revoked. If an INSERT must be held "+
						"unused, name it in writeExemptions with the reason.",
						role, verb, table)
				}
			}
		})
	}
}

// TestNoRoleHoldsAColumnWriteItsQueriesNeverMake is the same question one level down, a verb
// at a time: every INSERT or UPDATE privilege a role holds on a column must be one of the
// columns its production statements INSERT or UPDATE. `store` writes `users` legitimately,
// so only has_column_privilege can see it reaching users.role.
//
// The verbs are kept apart because a column a role inserts is not one it may rewrite: the
// order a checkout writes once is not the order the storefront may later re-price or
// backdate. Every table is in scope, and a column's default excuses nothing: orders.placed_at
// is defaulted to now(), and UPDATE on it rewrites a committed order's date.
func TestNoRoleHoldsAColumnWriteItsQueriesNeverMake(t *testing.T) {
	ctx := t.Context()

	for _, role := range []string{"store", "admin", "maintenance", "reporting"} {
		t.Run(role, func(t *testing.T) {
			expected := writableColumns(t, role)

			rows, err := pool.Query(ctx, `
				SELECT c.relname, a.attname, p.priv
				FROM pg_class c
				JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
				CROSS JOIN unnest(ARRAY['INSERT', 'UPDATE']) AS p(priv)
				WHERE c.relnamespace = 'public'::regnamespace
				  AND c.relkind IN ('r', 'p')
				  AND has_column_privilege($1, c.oid, a.attnum, p.priv)
				ORDER BY c.relname, a.attnum, p.priv`, role)
			if err != nil {
				t.Fatalf("read the column privileges of %s: %v", role, err)
			}
			defer rows.Close()

			held := 0
			for rows.Next() {
				var table, col, priv string
				if scanErr := rows.Scan(&table, &col, &priv); scanErr != nil {
					t.Fatalf("scan a column privilege: %v", scanErr)
				}
				held++
				cols, known := expected[priv][table]
				if known && cols == nil {
					continue // some statement names no columns; the table-level guard has it
				}
				if cols[col] {
					continue
				}
				if why, ok := columnExemptions[role+"."+priv+"."+table+"."+col]; ok {
					t.Logf("%s may %s %s.%s: %s", role, priv, table, col, why)
					continue
				}
				t.Errorf("%s holds %s on %s.%s, and no statement it runs %ss that column.\n"+
					"  An INSERT does not justify an UPDATE, and a default does not justify "+
					"either. Narrow the column grant in migrations/001, or name it in "+
					"columnExemptions with the reason.", role, priv, table, col, strings.ToLower(priv))
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("walk the column privileges of %s: %v", role, err)
			}
			t.Logf("%s holds %d column write privileges", role, held)
		})
	}
}

// TestRolesCannotWriteTheColumnsTheirStatementsLeaveAlone runs, as each role, a write of
// columns no statement of that role writes and that other rules read as fixed. PostgreSQL
// checks column privileges when it plans a statement, so each case is refused for
// privilege whether or not a row matches.
func TestRolesCannotWriteTheColumnsTheirStatementsLeaveAlone(t *testing.T) {
	const order = "'66666666-6666-4666-8666-666666666666'"
	for _, tc := range []struct{ name, role, stmt string }{
		{"store UPDATE orders.placed_at", "store",
			`UPDATE orders SET placed_at = placed_at - interval '800 days' WHERE id = ` + order},
		{"admin UPDATE orders.placed_at", "admin",
			`UPDATE orders SET placed_at = placed_at - interval '800 days' WHERE id = ` + order},
		{"store INSERT orders.placed_at", "store",
			`INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
			                     shipping_method_name, placed_at)
			 VALUES ('GO-260721-000999', 'ffff0002-0000-4000-8000-000000000000', 'home_delivery',
			         '宅配到府', now() - interval '800 days')`},
		{"store UPDATE orders.shipping_cents, discount_cents", "store",
			`UPDATE orders SET shipping_cents = 0, discount_cents = 0 WHERE id = ` + order},
		{"store UPDATE orders.order_number", "store",
			`UPDATE orders SET order_number = 'GO-260721-000999' WHERE id = ` + order},
		{"store UPDATE orders.locale", "store",
			`UPDATE orders SET locale = 'en' WHERE id = ` + order},
		{"store UPDATE product_reviews.product_id, user_id", "store",
			`UPDATE product_reviews SET product_id = product_id, user_id = user_id`},
		{"store UPDATE product_reviews.is_verified_purchase", "store",
			`UPDATE product_reviews SET is_verified_purchase = true`},
		{"admin UPDATE order_shipments.shipped_at", "admin",
			`UPDATE order_shipments SET shipped_at = shipped_at - interval '30 days'
			 WHERE id = '66660002-0000-4000-8000-000000000000'`},
		{"admin INSERT order_shipments.delivered_at", "admin",
			`INSERT INTO order_shipments (order_id, carrier, tracking_number, delivered_at)
			 VALUES (` + order + `, 'black_cat', '903-2214-0000', now() - interval '30 days')`},
		{"admin UPDATE newsletter_issues.subject, body", "admin",
			`UPDATE newsletter_issues SET subject = '改寫', body = '改寫'`},
		{"admin INSERT newsletter_issues.sent_at, recipients", "admin",
			`INSERT INTO newsletter_issues (subject, body, sent_at, recipients)
			 VALUES ('主旨', '內容', now(), 1)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRoleStatementDenied(t, tc.role, tc.stmt)
		})
	}
}

// columnExemptions is a column privilege a role holds that its statements never exercise,
// keyed role.VERB.table.column, with the reason.
var columnExemptions = map[string]string{
	"admin.UPDATE.shipping_zones.position": "LockShippingZone takes FOR UPDATE on the zone, " +
		"which PostgreSQL allows only with UPDATE on some column; the back office updates no " +
		"zone column, and position only orders the zone list",
}

// TestNoStaleColumnExemption refuses a columnExemptions entry whose privilege is no longer held.
func TestNoStaleColumnExemption(t *testing.T) {
	ctx := t.Context()
	for key, why := range columnExemptions {
		parts := strings.Split(key, ".")
		if len(parts) != 4 {
			t.Errorf("columnExemptions key %q is not role.VERB.table.column", key)
			continue
		}
		var held bool
		if err := pool.QueryRow(ctx, `SELECT has_column_privilege($1, $2::regclass, $3, $4)`,
			parts[0], parts[2], parts[3], parts[1]).Scan(&held); err != nil {
			t.Fatalf("ask whether %s holds %s: %v", parts[0], key, err)
		}
		if !held {
			t.Errorf("columnExemptions has %q (%s), but the privilege is not held. "+
				"The entry claims a gap that is closed.", key, why)
		}
	}
}

// writableColumns is, per verb, the columns of each table the role's production statements
// write. A nil column set means UNKNOWN: some statement writes the table without naming them.
func writableColumns(t *testing.T, role string) map[string]map[string]map[string]bool {
	t.Helper()
	byQuery := queryColumnWrites(t)
	out := map[string]map[string]map[string]bool{"INSERT": {}, "UPDATE": {}}
	for _, pkg := range packagesOn(role) {
		for _, method := range calledQueries(t, pkg) {
			for verb, tables := range byQuery[method] {
				for table, cols := range tables {
					mergeColumns(out[verb], table, cols)
				}
			}
		}
	}
	return out
}

// mergeColumns adds cols to the table's set; nil (unknown) absorbs everything after it.
func mergeColumns(into map[string]map[string]bool, table string, cols map[string]bool) {
	if existing, seen := into[table]; seen && existing == nil {
		return
	}
	if cols == nil {
		into[table] = nil
		return
	}
	if into[table] == nil {
		into[table] = map[string]bool{}
	}
	for c := range cols {
		into[table][c] = true
	}
}

func queryColumnWrites(t *testing.T) map[string]map[string]map[string]map[string]bool {
	t.Helper()
	src, err := os.ReadFile("query.sql.go")
	if err != nil {
		t.Fatalf("read the generated queries: %v", err)
	}
	out := map[string]map[string]map[string]map[string]bool{}
	for _, m := range generatedQuery.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = columnWrites(m[2])
	}
	return out
}

// calledQueries is the generated queries a pool package's code can run: the calls in its
// whole tree (a feature package below it runs on the same pool), and those of every module
// function that code calls, followed through the static call graph. A helper in another
// package runs on whichever pool's querier it is handed: the back office's picking
// transition records the paid event through payment.CompleteFunding, so payment's
// RecordPaidEvent runs on the admin pool too.
func calledQueries(t *testing.T, pkg string) []string {
	t.Helper()
	graph, err := loadCallGraph()
	if err != nil {
		t.Fatalf("load the module's call graph: %v", err)
	}
	root := "internal/" + pkg
	seen := map[*types.Func]bool{}
	found := map[string]bool{}
	var visit func(fn *types.Func)
	visit = func(fn *types.Func) {
		if seen[fn] {
			return
		}
		seen[fn] = true
		for _, q := range graph.queries[fn] {
			found[q] = true
		}
		for _, callee := range graph.callees[fn] {
			visit(callee)
		}
	}
	for fn, path := range graph.pkgOf {
		rel := strings.TrimPrefix(path, modulePath+"/")
		if rel != root && !strings.HasPrefix(rel, root+"/") {
			continue
		}
		// A test-helper package (admintest, accesstest) is test code, as *_test.go is.
		if rel != root && strings.HasSuffix(rel[strings.LastIndexByte(rel, '/')+1:], "test") {
			continue
		}
		visit(fn)
	}
	return slices.Sorted(maps.Keys(found))
}

const modulePath = "github.com/koopa0/goen"

// callGraph is the module's static call graph over production code: for each declared
// function, the module functions it names and the *db.Queries methods it names.
type callGraph struct {
	pkgOf   map[*types.Func]string
	queries map[*types.Func][]string
	callees map[*types.Func][]*types.Func
}

// loadCallGraph type-checks the module once per test binary. A call through an interface
// is not followed; no *db.Queries method is reached that way, and the regexp this replaced
// (any `.Name(` in the package's files) found the same queries per package.
var loadCallGraph = sync.OnceValues(func() (*callGraph, error) {
	cfg := &packages.Config{
		Dir:  filepath.Join("..", ".."),
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
	}
	pkgs, err := packages.Load(cfg, "./internal/...", "./cmd/...")
	if err != nil {
		return nil, err
	}
	g := &callGraph{pkgOf: map[*types.Func]string{}, queries: map[*types.Func][]string{},
		callees: map[*types.Func][]*types.Func{}}
	dbPath := modulePath + "/internal/db"
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("load %s: %w", p.PkgPath, p.Errors[0])
		}
		for _, f := range p.Syntax {
			if strings.HasSuffix(p.Fset.File(f.Pos()).Name(), "_test.go") {
				continue
			}
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				fn, ok := p.TypesInfo.Defs[fd.Name].(*types.Func)
				if !ok {
					continue
				}
				g.pkgOf[fn] = p.PkgPath
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					id, ok := n.(*ast.Ident)
					if !ok {
						return true
					}
					used, ok := p.TypesInfo.Uses[id].(*types.Func)
					if !ok || used.Pkg() == nil || !strings.HasPrefix(used.Pkg().Path(), modulePath+"/") {
						return true
					}
					used = used.Origin()
					if used.Pkg().Path() == dbPath && used.Signature().Recv() != nil {
						g.queries[fn] = append(g.queries[fn], used.Name())
						return true
					}
					g.callees[fn] = append(g.callees[fn], used)
					return true
				})
			}
		}
	}
	return g, nil
})

// writeExemptions is a privilege a role holds that its own queries never exercise, keyed
// role.table, with the reason.
var writeExemptions = map[string]string{}

// generatedQuery matches one sqlc-generated query constant and its SQL body. sqlc emits one db
// package, so the compiler lets any store call any query; queryowners_test.go holds each query to
// the package whose query.sql declares it.
var generatedQuery = regexp.MustCompile(
	"(?s)const \\w+ = `-- name: (\\w+) :\\w+\n(.*?)\n`")

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

// writeTarget matches the table an INSERT, UPDATE or DELETE names.
var writeTarget = regexp.MustCompile(
	`(?is)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)

func writeTargets(src string) map[string]bool {
	out := map[string]bool{}
	for _, m := range writeTarget.FindAllStringSubmatch(stripSQLComments(src), -1) {
		out[m[1]] = true
	}
	// `UPDATE SET` means the pattern caught a fragment rather than a statement.
	delete(out, "set")
	return out
}

var insertColumns = regexp.MustCompile(
	`(?is)\bINSERT\s+INTO\s+([a-z_][a-z0-9_]*)\s*\(([^)]*)\)`)

// bareInsert matches an INSERT that does NOT name its columns, which means every column.
var bareInsert = regexp.MustCompile(
	`(?is)\bINSERT\s+INTO\s+([a-z_][a-z0-9_]*)\s+(?:VALUES|SELECT|DEFAULT)\b`)

// updateTarget matches an UPDATE's target, with or without an alias, up to its SET.
var updateTarget = regexp.MustCompile(
	`(?is)\bUPDATE\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)\s+(?:(?:AS\s+)?[a-z_][a-z0-9_]*\s+)??SET\s+`)

// upsertTarget matches the SET of an INSERT's ON CONFLICT DO UPDATE, which PostgreSQL checks
// as UPDATE on the columns it assigns.
var upsertTarget = regexp.MustCompile(`(?is)\bON\s+CONFLICT\b.*?\bDO\s+UPDATE\s+SET\s+`)

// setEnd matches the clause that ends a flattened SET list.
var setEnd = regexp.MustCompile(`(?is)\b(?:WHERE|RETURNING|FROM)\b`)

var assignedColumn = regexp.MustCompile(`(?i)(?:^|,)\s*([a-z_][a-z0-9_]*)\s*=`)

// assignedColumns is the columns a SET list starting at s assigns. Everything inside
// parentheses is blanked first, so a subquery's own FROM or WHERE cannot end the list and a
// function's commas cannot split it; an UPDATE inside WITH ends at the parenthesis that
// closes it. A multi-column assignment names nothing here and degrades to unknown.
func assignedColumns(s string) []string {
	b := []byte(s)
	depth, end := 0, len(b)
	for i, c := range b {
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
		}
		if depth < 0 || (depth == 0 && c == ';') {
			end = i
			break
		}
		if depth > 0 || c == '(' || c == ')' {
			b[i] = ' '
		}
	}
	flat := string(b[:end])
	if loc := setEnd.FindStringIndex(flat); loc != nil {
		flat = flat[:loc[0]]
	}
	matches := assignedColumn.FindAllStringSubmatch(flat, -1)
	cols := make([]string, 0, len(matches))
	for _, a := range matches {
		cols = append(cols, a[1])
	}
	return cols
}

// columnWrites is, per verb, the columns each table's writes name. nil means UNKNOWN — some
// statement writes the table without naming columns — which degrades it to the table-level
// check.
func columnWrites(src string) map[string]map[string]map[string]bool {
	clean := stripSQLComments(src)
	out := map[string]map[string]map[string]bool{"INSERT": {}, "UPDATE": {}}

	note := func(verb, table string, cols []string) {
		if len(cols) == 0 {
			mergeColumns(out[verb], table, nil)
			return
		}
		set := map[string]bool{}
		for _, c := range cols {
			set[strings.ToLower(strings.TrimSpace(c))] = true
		}
		mergeColumns(out[verb], table, set)
	}

	for _, m := range insertColumns.FindAllStringSubmatch(clean, -1) {
		note("INSERT", m[1], strings.Split(m[2], ","))
	}
	for _, m := range bareInsert.FindAllStringSubmatch(clean, -1) {
		note("INSERT", m[1], nil)
	}
	inserts := insertVerb.FindAllStringSubmatchIndex(clean, -1)
	for i, match := range inserts {
		end := len(clean)
		if i+1 < len(inserts) {
			end = inserts[i+1][0]
		}
		statement := clean[match[1]:end]
		if stop := strings.IndexByte(statement, ';'); stop >= 0 {
			statement = statement[:stop]
		}
		if loc := upsertTarget.FindStringIndex(statement); loc != nil {
			note("UPDATE", clean[match[2]:match[3]], assignedColumns(statement[loc[1]:]))
		}
	}
	for _, m := range updateTarget.FindAllStringSubmatchIndex(clean, -1) {
		note("UPDATE", clean[m[2]:m[3]], assignedColumns(clean[m[1]:]))
	}
	return out
}

// stripSQLComments removes -- comments: sqlc's doc comment sits above each query and names
// its table, which would otherwise read as a write.
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

// storefrontPackages run on the pool that does SET ROLE store, mirroring cmd/goen's wiring.
var storefrontPackages = []string{
	"account", "auth", "cart", "catalog", "contact", "home", "loyalty", "media",
	"newsletter", "order/access", "order/buyer", "order/cancellation", "order/lapse", "order/notice", "outbox", "payment", "site", "warranty",
}

// backOfficePackages run on the pool that does SET ROLE admin.
var backOfficePackages = []string{
	"admin", "media", "order/cancellation", "order/notice", "outbox", "invoice",
}

// maintenancePackages run on the pool that does SET ROLE maintenance, which holds no table write.
var maintenancePackages = []string{"recommend", "restock"}

// TestThePoolMapIsComplete refuses a directory that ships a query.sql and is on no pool map, which
// opts it out entirely. An entry covers its own directory and everything below it, so a nested
// package (order/access) is named by its path and a parent entry (admin) covers its desks.
func TestThePoolMapIsComplete(t *testing.T) {
	named := slices.Concat(storefrontPackages, backOfficePackages, maintenancePackages)

	root := filepath.Join("..", "..", "internal")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path == filepath.Join(root, "db") {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != "query.sql" {
			return nil
		}
		rel, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		for _, pkg := range named {
			if rel == pkg || strings.HasPrefix(rel, pkg+"/") {
				return nil
			}
		}
		t.Errorf("internal/%s ships a query.sql and is on no role's pool map, so "+
			"its writes are checked against nothing. Add it to the map that matches "+
			"the pool cmd/goen constructs its store on.", rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
}

// TestNoStaleWriteExemption refuses a writeExemptions entry whose privilege is no longer held.
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

func TestWriteVerbTargetsDistinguishUpserts(t *testing.T) {
	for _, tc := range []struct {
		sql    string
		update bool
	}{
		{"INSERT INTO products (id) VALUES (1) ON CONFLICT (id) DO UPDATE SET id = 1", true},
		{"INSERT INTO products (id) VALUES (1) ON CONFLICT (id) DO NOTHING", false},
		{"INSERT INTO products (id) VALUES (1); INSERT INTO coupons (id) VALUES (1) ON CONFLICT (id) DO UPDATE SET id = 1", false},
	} {
		verbs := writeVerbTargets(tc.sql)["products"]
		if !verbs["INSERT"] || verbs["UPDATE"] != tc.update || verbs["DELETE"] {
			t.Errorf("%s: got verbs %v", tc.sql, verbs)
		}
	}
}

func TestWriteVerbTargetsCountRowLocks(t *testing.T) {
	for _, tc := range []struct {
		sql      string
		locked   []string
		unlocked []string
	}{
		{"SELECT id FROM shipping_zones WHERE id = $1 FOR UPDATE", []string{"shipping_zones"}, nil},
		{"SELECT 1 FROM orders o JOIN order_lines l ON l.order_id = o.id FOR UPDATE OF o",
			[]string{"orders"}, []string{"order_lines"}},
		{"SELECT 1 FROM orders AS o JOIN order_lines l ON l.order_id = o.id FOR SHARE OF l NOWAIT",
			[]string{"order_lines"}, []string{"orders"}},
		{"SELECT id FROM t FOR KEY SHARE", []string{"t"}, nil},
		{"SELECT id FROM t FOR NO KEY UPDATE SKIP LOCKED", []string{"t"}, nil},
		{"SELECT id FROM carts FOR UPDATE; SELECT id FROM coupons", []string{"carts"}, []string{"coupons"}},
	} {
		got := writeVerbTargets(tc.sql)
		for _, table := range tc.locked {
			if !got[table]["UPDATE"] {
				t.Errorf("%s: %s not counted as UPDATE, got %v", tc.sql, table, got)
			}
		}
		for _, table := range tc.unlocked {
			if got[table]["UPDATE"] {
				t.Errorf("%s: %s counted as UPDATE, got %v", tc.sql, table, got)
			}
		}
	}
}

func TestColumnWritesKeepTheVerbsApart(t *testing.T) {
	for _, tc := range []struct {
		sql            string
		table          string
		insert, update []string
	}{
		{"INSERT INTO carts (user_id, digest) VALUES ($1, $2) ON CONFLICT (digest) DO UPDATE SET user_id = excluded.user_id",
			"carts", []string{"digest", "user_id"}, []string{"user_id"}},
		{"UPDATE hero_slides h SET position = coalesce((SELECT min(o.position) FROM hero_slides o WHERE o.id <> h.id), 0) - 1, " +
			"is_active = true WHERE h.id = $1",
			"hero_slides", nil, []string{"is_active", "position"}},
		{"UPDATE order_private_data pd SET email = $1, city = nullif($2::text, '') FROM orders o WHERE pd.order_id = o.id",
			"order_private_data", nil, []string{"city", "email"}},
		{"WITH claimed AS (UPDATE stock_notifications SET notified_at = now() WHERE id = $1 RETURNING id) " +
			"INSERT INTO outbox_messages (topic, dedupe_key, payload) SELECT 't', id::text, '{}' FROM claimed",
			"stock_notifications", nil, []string{"notified_at"}},
		{"WITH claimed AS (UPDATE stock_notifications SET notified_at = now() WHERE id = $1 RETURNING id) " +
			"INSERT INTO outbox_messages (topic, dedupe_key, payload) SELECT 't', id::text, '{}' FROM claimed",
			"outbox_messages", []string{"dedupe_key", "payload", "topic"}, nil},
	} {
		got := columnWrites(tc.sql)
		for verb, want := range map[string][]string{"INSERT": tc.insert, "UPDATE": tc.update} {
			cols, named := got[verb][tc.table]
			if want == nil {
				if named {
					t.Errorf("%s: %s counted on %s as %v", tc.sql, verb, tc.table, cols)
				}
				continue
			}
			if keys := slices.Sorted(maps.Keys(cols)); !slices.Equal(keys, want) {
				t.Errorf("%s: %s on %s = %v, want %v", tc.sql, verb, tc.table, keys, want)
			}
		}
	}
}
