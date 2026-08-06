package db_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryGeneratedQueryHasACaller holds every generated query to having one.
//
// An audit on 2026-07-28 found 14 of 231 generated methods with no caller
// outside internal/db. None was dead code: every one carried a comment
// describing what it was for, and several named the UI they were written for.
// They were half-built features that read as finished ones —
//
//   - the customer could not change their default address, and checkout
//     prefills from it;
//   - the product page never knew whether something was already saved;
//   - checkout.session.expired was unhandled, so abandoned payments never
//     resolved;
//   - nothing pruned expired sessions or reclaimed abandoned uploads;
//   - the back office decided returns without seeing what was in them, granted
//     credit without seeing the balance, and had no timeline on an order;
//   - nobody could ask who had two-factor turned on.
//
// The rule this locks: a query with no caller is either wired or deleted.
// "Write it now, use it later" is where all fourteen came from.
//
// No allowlist, deliberately. A list of accepted exceptions is a list that
// grows, and the whole failure mode here is code that looks finished — the
// only honest exception is the one somebody deletes.
func TestEveryGeneratedQueryHasACaller(t *testing.T) {
	t.Parallel()

	methods := generatedMethods(t)
	if len(methods) < 100 {
		t.Fatalf("found %d generated methods, want far more — the parser stopped matching",
			len(methods))
	}
	callers := callerSources(t)

	var orphans []string
	for _, name := range methods {
		// ".Name(" rather than the bare name: a comment mentioning the query is
		// not a caller, and this is what a call through q, s.q or a WithTx copy
		// all look like.
		if !strings.Contains(callers, "."+name+"(") {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) > 0 {
		t.Errorf("%d generated queries have no caller outside internal/db:\n  %s\n\n"+
			"Wire each one or delete it. A query nobody calls is a feature nobody "+
			"finished, and it reads in review as one that shipped.",
			len(orphans), strings.Join(orphans, "\n  "))
	}
}

// generatedMethods is every method sqlc put on *Queries.
func generatedMethods(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "query.sql.go", nil, 0)
	if err != nil {
		t.Fatalf("parse query.sql.go: %v", err)
	}

	var out []string
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
	return out
}

// callerSources is every Go file in the module outside internal/db, as one
// blob. Test files count: an integration test IS a caller, and a query reached
// only by a test is a different smell that testing.md already covers.
func callerSources(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "db" && strings.HasSuffix(path, filepath.Join("internal", "db")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		b.Write(src)
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
	return b.String()
}
