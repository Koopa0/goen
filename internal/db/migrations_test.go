package db_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	dbtestImportPath = "github.com/koopa0/goen/internal/db/dbtest"
	minimumTestMains = 15
)

type testMainSource struct {
	path                string
	usesMigrationRunner bool
}

// TestEveryIntegrationSuiteUsesTheMigrationRunner makes the integration-test
// harness part of the verified corpus. dbtest is the one runner that globs all
// *.up.sql files; a copied TestMain that applies one filename goes silently
// stale the day the next migration is cut.
func TestEveryIntegrationSuiteUsesTheMigrationRunner(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	testMains := findTestMains(t, root)
	if len(testMains) < minimumTestMains {
		t.Fatalf("only %d TestMains found, want at least %d; the source walk is not reading the repository",
			len(testMains), minimumTestMains)
	}

	for _, source := range testMains {
		if source.usesMigrationRunner {
			continue
		}
		rel, err := filepath.Rel(root, source.path)
		if err != nil {
			rel = source.path
		}
		t.Errorf("%s declares TestMain without dbtest.Start or dbtest.Pool; "+
			"the shared runner globs every *.up.sql, while a suite reading one migration by name "+
			"goes stale when 002 is cut", filepath.ToSlash(rel))
	}
}

func findTestMains(t *testing.T, root string) []testMainSource {
	t.Helper()
	var found []testMainSource
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Name.Name != "TestMain" {
				continue
			}
			found = append(found, testMainSource{
				path:                path,
				usesMigrationRunner: callsMigrationRunner(file, function),
			})
			break
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk for TestMain: %v", err)
	}
	slices.SortFunc(found, func(a, b testMainSource) int {
		return strings.Compare(a.path, b.path)
	})
	return found
}

func callsMigrationRunner(file *ast.File, testMain *ast.FuncDecl) bool {
	importName := ""
	for _, spec := range file.Imports {
		if spec.Path.Value != `"`+dbtestImportPath+`"` {
			continue
		}
		if spec.Name == nil {
			importName = "dbtest"
		} else if spec.Name.Name != "." && spec.Name.Name != "_" {
			importName = spec.Name.Name
		}
		break
	}
	if importName == "" || testMain.Body == nil {
		return false
	}

	found := false
	ast.Inspect(testMain.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "Start" && selector.Sel.Name != "Pool") {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if ok && packageName.Name == importName {
			found = true
		}
		return !found
	})
	return found
}
