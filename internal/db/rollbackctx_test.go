package db_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// anyRollback matches every rollback call, whatever the receiver is named. A
// savepoint is the same pgx Tx as the outer transaction; a cancelled context
// still kills the pooled connection.
var anyRollback = regexp.MustCompile(`\.Rollback\(`)

// boundedRollback is the one call that outlives the request and still ends.
var boundedRollback = regexp.MustCompile(`\bdb\.Rollback\(`)

// rollbackHelper is the one file that calls a transaction's Rollback itself.
var rollbackHelper = filepath.Join("internal", "db", "tx.go")

// unboundedRollback reports a rollback on this line that does not go through
// db.Rollback: the request's own context under any name, r.Context(), a
// stored context, a helper's result, or context.WithoutCancel with no deadline.
func unboundedRollback(line string) bool {
	return len(anyRollback.FindAllStringIndex(line, -1)) >
		len(boundedRollback.FindAllStringIndex(line, -1))
}

func TestTheRollbackRuleSeesWhatTheOldPatternMissed(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]bool{
		`defer func() { _ = tx.Rollback(ctx) }()`:                              true,
		`defer func() { _ = tx.Rollback(r.Context()) }()`:                      true,
		`defer func() { _ = tx.Rollback(cleanupCtx) }()`:                       true,
		`_ = sp.Rollback(c)`:                                                   true,
		`defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()`:       true,
		`defer db.Rollback(ctx, tx)`:                                           false,
		`db.Rollback(ctx, sp)`:                                                 false,
		`db.Rollback(ctx, a); _ = b.Rollback(context.WithoutCancel(ctx))`:      true,
		`rollbackAfter := context.WithoutCancel(ctx) // no rollback call here`: false,
	} {
		if got := unboundedRollback(line); got != want {
			t.Errorf("unboundedRollback(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestEveryRollbackOutlivesItsRequest derives its corpus from the tree rather
// than a list, so a store written next week is covered the day it is written.
//
// pgx's Rollback runs `Exec(ctx, "rollback")`, and on failure calls conn.die()
// under the comment "a rollback failure leaves the connection in an undefined
// state". A cancelled context fails that Exec before it reaches the server, so
// a rollback deferred on the request's own context rolls nothing back when the
// client disconnects: the transaction is left for the server to reap and the
// pooled connection is destroyed. Detached with no deadline instead, a ROLLBACK
// to a server that stopped answering holds its goroutine and connection until
// TCP gives up. db.Rollback is detached and bounded.
func TestEveryRollbackOutlivesItsRequest(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
			return nil
		}
		//nolint:gosec // G304: path comes from WalkDir over this repository
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == rollbackHelper {
			return nil
		}
		for i, line := range strings.Split(string(src), "\n") {
			if unboundedRollback(line) {
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+"  "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("%d rollback(s) do not go through db.Rollback:\n  %s\n\n"+
			"A request-bound context makes pgx fail the ROLLBACK before it reaches the "+
			"server and destroy the pooled connection; a detached one with no deadline "+
			"waits on a silent server for as long as TCP does. Use "+
			"defer db.Rollback(ctx, tx).",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// handsOffItsTransaction lists the functions allowed to open a transaction
// without ending it, each keyed "path/to/file.go:Func" with the reason. It
// is shrink-only: an entry that stops being needed fails
// TestEveryTransactionIsRolledBack, so the list cannot go stale. It is empty
// because every transaction in the tree is ended by the function that opened
// it. Add an entry only for a helper that returns the transaction to a caller
// that owns the rollback, and name that caller in the reason.
var handsOffItsTransaction = map[string]string{}

// minTransactions is a floor on how many Begin sites the walk must find, so a
// change to how the tree opens a transaction cannot leave the guard looking at
// nothing and passing. The walk found 60 on 2026-10-14 (58 on a pool, 2
// savepoints; 13 of them in admintest). The floor leaves room for a deleted
// store, not for a guard that has stopped seeing the tree.
const minTransactions = 55

// openedTx is one Begin or BeginTx call and the variable that receives the
// transaction.
type openedTx struct {
	fn   string // the enclosing function: Name, Recv.Name, or Name.func@line for a closure
	line int
	name string // "" when the transaction is not assigned to a variable
}

// beginCall reports whether call opens a transaction: pool.Begin(ctx) or
// pool.BeginTx(ctx, opts). The argument counts keep out an unrelated method
// that happens to be called Begin, such as twofactor's
// Store.Begin(ctx, userID, address). A savepoint is tx.Begin(ctx), the same
// shape, and needs its own rollback.
func beginCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "Begin" && len(call.Args) == 1 ||
		sel.Sel.Name == "BeginTx" && len(call.Args) == 2
}

// rollsBack reports whether call ends the transaction held in name:
// db.Rollback(ctx, name) or name.Rollback(ctx).
func rollsBack(call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Rollback" {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if recv.Name == name {
		return true
	}
	if recv.Name == "db" && len(call.Args) == 2 {
		arg, isArg := call.Args[1].(*ast.Ident)
		return isArg && arg.Name == name
	}
	return false
}

// transactionsIn parses one file and returns every transaction it opens that
// has no rollback on the same variable in the same function. A rollback inside
// a closure the function defines counts (a deferred func literal, a
// t.Cleanup); a rollback in some other function does not. opened is how many
// Begin sites the file has.
func transactionsIn(path string, src any) (opened int, bare []openedTx, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return 0, nil, err
	}

	var check func(name string, body *ast.BlockStmt)
	check = func(name string, body *ast.BlockStmt) {
		var begins []openedTx
		seen := map[*ast.CallExpr]bool{}
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				// Its own function: judged on its own body.
				line := fset.Position(n.Pos()).Line
				check(name+".func@"+strconv.Itoa(line), n.Body)
				return false
			case *ast.AssignStmt:
				if len(n.Lhs) == 2 && len(n.Rhs) == 1 {
					if call, ok := n.Rhs[0].(*ast.CallExpr); ok && beginCall(call) {
						seen[call] = true
						tx := ""
						if id, ok := n.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
							tx = id.Name
						}
						begins = append(begins, openedTx{name, fset.Position(call.Pos()).Line, tx})
					}
				}
			case *ast.CallExpr:
				if beginCall(n) && !seen[n] {
					begins = append(begins, openedTx{name, fset.Position(n.Pos()).Line, ""})
				}
			}
			return true
		})
		opened += len(begins)

		for _, b := range begins {
			ended := false
			if b.name != "" {
				ast.Inspect(body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok && rollsBack(call, b.name) {
						ended = true
					}
					return !ended
				})
			}
			if !ended {
				bare = append(bare, b)
			}
		}
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) == 1 {
			recv := fn.Recv.List[0].Type
			if star, isStar := recv.(*ast.StarExpr); isStar {
				recv = star.X
			}
			if id, isIdent := recv.(*ast.Ident); isIdent {
				name = id.Name + "." + name
			}
		}
		check(name, fn.Body)
	}
	return opened, bare, nil
}

func TestTheTransactionRuleSeesAMissingRollback(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, body string
		wantOpened int
		wantBare   []string
	}{
		{"deferred", `func f(ctx context.Context) { tx, err := pool.Begin(ctx); _ = err; defer db.Rollback(ctx, tx) }`, 1, nil},
		{"deferred with options", `func f(ctx context.Context) { tx, err := pool.BeginTx(ctx, opts); _ = err; defer db.Rollback(ctx, tx) }`, 1, nil},
		{"no rollback", `func f(ctx context.Context) { tx, err := pool.Begin(ctx); _, _ = tx, err; _ = tx.Commit(ctx) }`, 1, []string{"f"}},
		{"rollback on another variable", `func f(ctx context.Context) { tx, err := pool.Begin(ctx); _, _ = tx, err; defer db.Rollback(ctx, other) }`, 1, []string{"f"}},
		{"explicit rollback on the error path", `func f(ctx context.Context) { sp, err := tx.Begin(ctx); _ = err; if bad { db.Rollback(ctx, sp) } }`, 1, nil},
		{"raw rollback still counts as one", `func f(ctx context.Context) { tx, err := pool.Begin(ctx); _ = err; defer func() { _ = tx.Rollback(ctx) }() }`, 1, nil},
		{"rollback in a cleanup closure", `func f(t *testing.T) { tx, err := pool.Begin(ctx); _ = err; t.Cleanup(func() { db.Rollback(ctx, tx) }) }`, 1, nil},
		{"closure opens, outer rolls back", `func f(ctx context.Context) { tx, err := pool.Begin(ctx); _ = err; defer db.Rollback(ctx, tx); go func() { inner, _ := pool.Begin(ctx); _ = inner }() }`, 2, []string{"f.func@2"}},
		{"result thrown away", `func f(ctx context.Context) { _, err := pool.Begin(ctx); _ = err }`, 1, []string{"f"}},
		{"result returned to the caller", `func f(ctx context.Context) (pgx.Tx, error) { return pool.Begin(ctx) }`, 1, []string{"f"}},
		{"method", `func (s *Store) f(ctx context.Context) { tx, err := s.pool.Begin(ctx); _, _ = tx, err }`, 1, []string{"Store.f"}},
		{"unrelated Begin", `func f(ctx context.Context) { secret, uri, err := store.Begin(ctx, id, email); _, _, _ = secret, uri, err }`, 0, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opened, bare, err := transactionsIn("x.go", "package p\n"+tt.body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var got []string
			for _, b := range bare {
				got = append(got, b.fn)
			}
			if opened != tt.wantOpened || !slices.Equal(got, tt.wantBare) {
				t.Errorf("opened, bare = %d, %v; want %d, %v", opened, got, tt.wantOpened, tt.wantBare)
			}
		})
	}
}

// TestEveryTransactionIsRolledBack closes the hole TestEveryRollbackOutlivesItsRequest
// leaves: that test judges the rollbacks that exist, so a Begin whose rollback
// was never written, or was deleted, has no line to judge. A transaction that is
// neither committed nor rolled back keeps its pooled connection and its row
// locks until the server's idle-in-transaction timeout, or for ever without one.
//
// It parses every non-test Go file under internal/ and cmd/, except generated
// templ output. That includes internal/admin/admintest, whose files are not
// _test.go but open transactions for tests. A function that opens a transaction
// must roll it back by name (db.Rollback(ctx, tx) or tx.Rollback(ctx)) in its
// own body or a closure inside it, deferred or explicit. Only the call is
// judged here; that it goes through db.Rollback is the neighbouring test's
// job, and what db.Rollback does is TestRollbackOutlivesTheRequestWithinItsBound's.
func TestEveryTransactionIsRolledBack(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	var opened int
	var offenders []string
	used := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
				return nil
			}
			n, bare, err := transactionsIn(path, nil)
			if err != nil {
				return err
			}
			opened += n
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, b := range bare {
				key := rel + ":" + b.fn
				if _, ok := handsOffItsTransaction[key]; ok {
					used[key] = true
					continue
				}
				offenders = append(offenders, fmt.Sprintf("%s:%d  %s opens a transaction and never rolls it back", rel, b.line, b.fn))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	if opened < minTransactions {
		t.Errorf("found %d transactions, want at least %d: the walk no longer sees how the tree opens one", opened, minTransactions)
	}
	if len(offenders) > 0 {
		t.Errorf("%d transaction(s) never rolled back:\n  %s\n\n"+
			"Add defer db.Rollback(ctx, tx) right after the Begin. After a Commit it is a no-op.",
			len(offenders), strings.Join(offenders, "\n  "))
	}
	for key, reason := range handsOffItsTransaction {
		if !used[key] {
			t.Errorf("handsOffItsTransaction[%q] (%s) no longer matches an unrolled transaction; delete the entry", key, reason)
		}
	}
}
