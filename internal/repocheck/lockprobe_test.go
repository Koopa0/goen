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

// TestOnlyDbtestReadsTheActivityView holds one rule: a test that waits for a
// lock reads the server's activity view through dbtest.Backends or
// dbtest.Blocked, which filter by the current database. A probe that matches
// only an application name or a query pattern would, once several test
// databases share one server, see another test's waiting backend and return
// early, and the lock test would pass without the interleaving it exists to
// prove.
func TestOnlyDbtestReadsTheActivityView(t *testing.T) {
	// Built in pieces so that this file holds no literal with the view's name.
	view := "pg_stat" + "_activity"
	home := filepath.Join("internal", "db", "dbtest") + string(filepath.Separator)
	root := repoRoot(t)

	var parsed, inHome int
	var offenders []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		parsed++
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || !strings.Contains(lit.Value, view) {
				return true
			}
			if strings.HasPrefix(rel, home) {
				inHome++
				return true
			}
			offenders = append(offenders, filepath.ToSlash(rel)+":"+strconv.Itoa(fset.Position(lit.Pos()).Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	if parsed < 1000 {
		t.Fatalf("parsed %d .go files, want at least 1000: the walk is broken", parsed)
	}
	if inHome == 0 {
		t.Fatalf("found no literal inside %s: the needle or the walk is broken", filepath.ToSlash(home))
	}
	for _, where := range offenders {
		t.Errorf("%s: read %s through dbtest.Backends or dbtest.Blocked, which filter by the current database", where, view)
	}
}
