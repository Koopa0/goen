package db_test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestEveryGeneratedQueryHasAProductionCaller covers the generated layer that
// x/tools/deadcode treats as live through reflection: does each sqlc method have
// a direct selector CallExpr in hand-written, non-test production Go?
//
// It is not type-aware — a same-named selector on another receiver satisfies it,
// and a call inside a dead wrapper still counts. make deadcode owns wrapper
// reachability; this test owns generated methods with zero production calls.
func TestEveryGeneratedQueryHasAProductionCaller(t *testing.T) {
	t.Parallel()

	methods := generatedMethods(t)
	if len(methods) < 100 {
		t.Fatalf("found %d generated methods, want far more — the parser stopped matching",
			len(methods))
	}
	calls := productionCallNames(t)

	var orphans []string
	for _, name := range methods {
		if _, called := calls[name]; !called {
			orphans = append(orphans, name)
		}
	}
	slices.Sort(orphans)
	if len(orphans) > 0 {
		t.Errorf("%d generated queries have no call expression in hand-written production Go:\n  %s\n\n"+
			"Wire each one or delete it. A query nobody calls is a feature nobody finished.",
			len(orphans), strings.Join(orphans, "\n  "))
	}
}

// generatedMethods is every method sqlc put on *Queries.
func generatedMethods(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "query.sql.go", nil, 0)
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

// productionCallNames returns selector names used as direct calls in files the
// current production build includes. Tests, integration-tagged files and
// generated code are excluded: none of them may make the guard greener.
func productionCallNames(t *testing.T) map[string]struct{} {
	t.Helper()

	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	calls := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			name := entry.Name()
			if name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, ".go") {
			return nil
		}
		matched, matchErr := build.Default.MatchFile(filepath.Dir(path), name)
		if matchErr != nil {
			return matchErr
		}
		if !matched {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		if ast.IsGenerated(file) {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok {
				calls[selector.Sel.Name] = struct{}{}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk production Go: %v", err)
	}
	return calls
}
