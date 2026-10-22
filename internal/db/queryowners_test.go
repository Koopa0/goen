package db_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// minOwnerFilesRead is the floor under the number of production files the walk
// reads. The walk reads 488 on the tree this was written against; a walk that
// stops matching reads a handful.
const minOwnerFilesRead = 450

var (
	// sqlcQueryFile is one quoted query file under queries: in sqlc.yaml.
	sqlcQueryFile = regexp.MustCompile(`(?m)^\s*-\s*"([^"]+\.sql)"\s*$`)
	// sqlcQueryName is one declaration in a query file.
	sqlcQueryName = regexp.MustCompile(`(?m)^-- name: (\w+) :`)
)

const (
	refundEngine = "unassigned: the refund engine settles an order, its return and its reservations in one transaction across desks"
	oneOrderLock = "unassigned: one order lock, shared by every path that cancels an order"
	sharedRead   = "unassigned: a read several packages share"
)

// queryOwnerExceptions is every cross-package query call that exists today, keyed
// "<caller directory> -> <Query>". The value is the task that removes it, or
// "unassigned: <reason>" while nobody owns it yet. The list may only shrink: a
// violation not named here fails, and an entry whose call is gone fails.
var queryOwnerExceptions = map[string]string{
	// The customer's cancel, the lapse sweeper and the refund engine each take
	// the order lock before the status UPDATE that cancellation.Settle must run
	// behind; the lock is declared beside Settle's queries.
	"internal/order/buyer -> LockOrderByNumber": oneOrderLock,
	"internal/order/lapse -> LockOrderByNumber": oneOrderLock,
	// The sweeper returns an expired hold through the one release every path uses.
	"internal/order/lapse -> ReleaseReservation": "unassigned: one stock release, shared by every path that returns a hold",
	// The order-placed email is written from the order page's own reads, in the
	// transaction that places the order.
	"internal/checkout -> OrderPageLines":       "unassigned: the order-placed email reads the order page's lines",
	"internal/checkout -> OrderSummaryByNumber": "unassigned: the order-placed email reads the order page's figures",

	// The order page shows what was refunded; the refund engine reads the same sum.
	"internal/admin/orders -> SettledRefundsForOrder": sharedRead,

	"internal/admin/refunds -> AdvanceOrder":             refundEngine,
	"internal/admin/refunds -> CompleteReturn":           refundEngine,
	"internal/admin/refunds -> HeldReservationsForOrder": refundEngine,
	"internal/admin/refunds -> LockOrderByNumber":        refundEngine,
	"internal/admin/refunds -> LockOrderForAdvance":      refundEngine,
	"internal/admin/refunds -> OpenSessionsForOrder":     refundEngine,
	"internal/admin/refunds -> PaidByCreditAlone":        refundEngine,
	"internal/admin/refunds -> RecordOrderEvent":         refundEngine,
	"internal/admin/refunds -> ReleaseReservation":       refundEngine,
	"internal/admin/refunds -> ReturnForDecision":        refundEngine,
	"internal/admin/orders -> OpenSessionsForOrder":      refundEngine,

	// The account page shows the profile and whether a password is set from
	// the users row sign-in reads.
	"internal/account -> UserByID":               sharedRead,
	"internal/account -> ReturnedOrders":         sharedRead,
	"internal/admin/customers -> ReturnedOrders": sharedRead,
	"internal/admin/orders -> ReturnedOrders":    sharedRead,
	"internal/order/warranty -> ReturnedOrders":  sharedRead,
	"internal/admin/customers -> MemberStanding": sharedRead,
	"internal/loyalty -> MemberStanding":         sharedRead,
	"internal/admin/staff -> UserByEmail":        sharedRead,
	"internal/admin/staff -> UserByID":           sharedRead,
	"internal/admin/products -> ProductRating":   sharedRead,
	"internal/checkout -> OrderHoldSpan":         sharedRead,
	"internal/order/buyer -> OrderHoldSpan":      sharedRead,
	"internal/home -> ListedCampaigns":           sharedRead,
	"internal/payment -> OrderSummaryByNumber":   sharedRead,
	"internal/catalog -> WishlistHas":            sharedRead,

	// The warranty search marks a registration's order refunded by the read
	// the customer's page and the storefront's warranty page use.
	"internal/admin/warranties -> ReturnedOrders": sharedRead,
}

// ownedQuery is a query and the place that declares it.
type ownedQuery struct {
	dir  string // the declaring file's directory, relative to the repository root
	file string // the declaring file, relative to the repository root
	line int
}

// queryCall is one call of a generated query.
type queryCall struct {
	caller string // the calling file's directory, relative to the repository root
	query  string
}

// TestEveryQueryIsCalledByItsOwner holds each generated query to the package whose
// query.sql declares it. sqlc emits every query.sql into this one package, so the
// compiler lets any store call any query; a call from another directory is
// coupling that the import policy in internal/repocheck cannot see.
//
// The walk reads every non-test .go file under the repository root regardless of
// build tags (integration-only production files such as admintest are included),
// except the files directly in internal/db (generated code and hand-written
// helpers) and directories that start with "." or "_" or are named testdata or
// node_modules (the main checkout holds untracked .worktrees/ and .claude/ copies
// of the repository). Test files are out of scope on purpose: fixtures
// legitimately call any query.
//
// A call is syntactic: a selector call naming a generated query on the identifier
// q, on a selector ending in q (s.q, w.q), or on db.New(...). That is the
// repository's convention and it avoids same-named methods on other types. A call
// the rule cannot see would make some query look uncalled, so every query must be
// reached by at least one recognised call.
func TestEveryQueryIsCalledByItsOwner(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	owners := queryOwners(t, root)

	generated := make(map[string]bool)
	for _, name := range generatedMethodsIn(t, "query.sql.go", "orders_export.sql.go") {
		generated[name] = true
	}
	var missing, extra []string
	for name := range generated {
		if _, ok := owners[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name := range owners {
		if !generated[name] {
			extra = append(extra, name)
		}
	}
	slices.Sort(missing)
	slices.Sort(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("the query files named in sqlc.yaml and the generated methods disagree\n"+
			"generated but declared in no listed query file: %v\n"+
			"declared in a listed query file but not generated (run make sqlc): %v",
			missing, extra)
	}

	calls, filesRead := queryCalls(t, root, generated)
	if filesRead < minOwnerFilesRead {
		t.Fatalf("read %d production files, want at least %d: the walk stopped matching",
			filesRead, minOwnerFilesRead)
	}

	reached := make(map[string]bool)
	for _, c := range calls {
		reached[c.query] = true
	}
	var unreached []string
	for name := range owners {
		if !reached[name] {
			unreached = append(unreached, name)
		}
	}
	slices.Sort(unreached)
	for _, name := range unreached {
		t.Errorf("query %s has no call through a recognised receiver (q, *.q, db.New(…)): "+
			"name the *db.Queries field q, or extend the rule; a call the rule cannot see "+
			"is a call it cannot police", name)
	}

	var problems []string
	seen := make(map[string]bool)
	for _, c := range calls {
		own := owners[c.query]
		if c.caller == own.dir {
			continue
		}
		key := c.caller + " -> " + c.query
		seen[key] = true
		if _, ok := queryOwnerExceptions[key]; ok {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s calls %s, which %s/query.sql declares (%s:%d): call it through the owner, "+
				"or move the declaration to the package that owns the rows",
			c.caller, c.query, own.dir, own.file, own.line))
	}
	for key := range queryOwnerExceptions {
		if !seen[key] {
			problems = append(problems, fmt.Sprintf("%q: delete this exception, its call is gone", key))
		}
	}
	slices.Sort(problems)
	problems = slices.Compact(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// queryOwners maps every query named in sqlc.yaml's query files to the file that
// declares it. A name declared twice fails.
func queryOwners(t *testing.T, root string) map[string]ownedQuery {
	t.Helper()

	repo, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("open the repository root: %v", err)
	}
	defer repo.Close()

	yaml, err := repo.ReadFile("sqlc.yaml")
	if err != nil {
		t.Fatalf("read sqlc.yaml: %v", err)
	}
	files := sqlcQueryFile.FindAllStringSubmatch(string(yaml), -1)
	if len(files) == 0 {
		t.Fatal("sqlc.yaml names no query file: the pattern stopped matching")
	}

	owners := make(map[string]ownedQuery)
	for _, m := range files {
		rel := m[1]
		src, readErr := repo.ReadFile(rel)
		if readErr != nil {
			t.Fatalf("read %s: %v", rel, readErr)
		}
		for _, loc := range sqlcQueryName.FindAllSubmatchIndex(src, -1) {
			name := string(src[loc[2]:loc[3]])
			line := 1 + strings.Count(string(src[:loc[0]]), "\n")
			if prev, dup := owners[name]; dup {
				t.Errorf("query %s is declared twice: %s:%d and %s:%d",
					name, prev.file, prev.line, rel, line)
				continue
			}
			owners[name] = ownedQuery{dir: filepath.ToSlash(filepath.Dir(rel)), file: rel, line: line}
		}
	}
	return owners
}

// generatedMethodsIn is every method sqlc put on *Queries in the named files of
// this directory.
func generatedMethodsIn(t *testing.T, names ...string) []string {
	t.Helper()

	var out []string
	for _, name := range names {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if id, ok := star.X.(*ast.Ident); ok && id.Name == "Queries" {
				out = append(out, fn.Name.Name)
			}
		}
	}
	return out
}

// queryCalls walks the repository for calls of a generated query and reports them
// with the number of files read.
func queryCalls(t *testing.T, root string, generated map[string]bool) (calls []queryCall, filesRead int) {
	t.Helper()

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
				name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "internal/db" {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		filesRead++
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !generated[sel.Sel.Name] || !isQueriesReceiver(sel.X) {
				return true
			}
			calls = append(calls, queryCall{caller: dir, query: sel.Sel.Name})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	return calls, filesRead
}

// isQueriesReceiver reports whether x is spelled the way the repository spells a
// *db.Queries: q, a selector ending in q (s.q), or a db.New(...) call.
func isQueriesReceiver(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.Ident:
		return x.Name == "q"
	case *ast.SelectorExpr:
		return x.Sel.Name == "q"
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "New" {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == "db"
	}
	return false
}
