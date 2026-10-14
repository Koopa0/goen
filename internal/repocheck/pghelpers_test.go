package repocheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestPostgresHelpersLiveOnlyInDb: LIKE syntax and the name a PostgreSQL error
// carries for its constraint are the database's, and package db owns both
// (db.EscapeLike, db.ConstraintName). A second copy elsewhere can drift from
// the first: a LIKE escaper that forgets the backslash makes a typed \ eat the
// next character, and a ConstraintName with another fallback changes what a
// caller's switch decides.
//
// Two shapes are refused in production code outside internal/db: a
// strings.NewReplacer that escapes %, and a function returning a value's
// ConstraintName field. Reading pgErr.ConstraintName in a switch or an if,
// often beside pgErr.Code, is not a copy and stays allowed.
func TestPostgresHelpersLiveOnlyInDb(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	dbDir := filepath.Join(root, "internal", "db")
	fset := token.NewFileSet()
	var visited int
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			filepath.Dir(path) == dbDir {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		visited++
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if isLikeEscaper(n) {
					offenders = append(offenders, rel+":"+strconv.Itoa(fset.Position(n.Pos()).Line)+
						" builds a LIKE escaper: use db.EscapeLike")
				}
			case *ast.FuncDecl:
				if line := returnsConstraintName(fset, n); line > 0 {
					offenders = append(offenders, rel+":"+strconv.Itoa(line)+
						" returns a ConstraintName field from "+n.Name.Name+": use db.ConstraintName")
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}

	if visited < 300 {
		t.Fatalf("walked %d non-test files, want at least 300: the walk lost its subject", visited)
	}
	for _, o := range offenders {
		t.Error(o)
	}
}

// isLikeEscaper reports whether call is strings.NewReplacer with a string
// argument that escapes %, the one character a LIKE pattern is built around.
func isLikeEscaper(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "NewReplacer" {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "strings" {
		return false
	}
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(s, `\%`) {
			return true
		}
	}
	return false
}

// returnsConstraintName returns the line of a return statement in fn that
// returns a selector ending in .ConstraintName, or 0.
func returnsConstraintName(fset *token.FileSet, fn *ast.FuncDecl) int {
	if fn.Body == nil {
		return 0
	}
	var line int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || line > 0 {
			return line == 0
		}
		for _, r := range ret.Results {
			if sel, ok := r.(*ast.SelectorExpr); ok && sel.Sel.Name == "ConstraintName" {
				line = fset.Position(ret.Pos()).Line
			}
		}
		return true
	})
	return line
}
