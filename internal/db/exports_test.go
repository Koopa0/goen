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

// TestEveryExportedFunctionHasACaller holds every export to being named
// somewhere other than its own declaration.
//
// goen is entirely under internal/, so nothing here is public API: an exported
// function with no caller is not "available to consumers", it is a feature
// nobody finished. Seven were found the day this was written, including two
// written an hour earlier in the same session —
//
//   - twofactor.Store.Remove, documenting a 2FA recovery path that could not be
//     reached, so a lost authenticator was a permanent lockout;
//   - media.Store.Recent, "the back office's picker", for a picker that did not
//     exist — so a shot belonging on three products was uploaded three times;
//   - AdminStockRisk.SoldText, the units figure that makes "14 days of cover"
//     mean something;
//   - ProductReview.ReviewStars, a rating for somebody scanning rather than
//     reading.
//
// It lives in internal/db beside TestEveryGeneratedQueryHasACaller because the
// two ask the same question at different layers, and a reader looking for one
// should find the other.
func TestEveryExportedFunctionHasACaller(t *testing.T) {
	t.Parallel()

	decls := exportedFunctions(t)
	if len(decls) < 300 {
		t.Fatalf("found %d exported functions, want far more — the parser stopped matching",
			len(decls))
	}
	uses := referenced(t)
	templates := templateText(t)

	var orphans []string
	for name, declaredIn := range decls {
		if uses[name] > 0 || strings.Contains(templates, name) {
			continue
		}
		orphans = append(orphans, name+"  ("+declaredIn+")")
	}
	if len(orphans) > 0 {
		t.Errorf("%d exported functions are named nowhere but their own declaration:"+
			"\n  %s\n\nWire each one or delete it. Nothing here is public API, so an "+
			"unused export is a feature nobody finished — and it reads in review as one "+
			"that shipped.", len(orphans), strings.Join(orphans, "\n  "))
	}
}

// exportedFunctions maps each exported function or method name to the file that
// declares it.
func exportedFunctions(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	forEachSource(t, false, func(path string, file *ast.File) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			if _, seen := out[fn.Name.Name]; !seen {
				out[fn.Name.Name] = path
			}
		}
	})
	return out
}

// referenced is every name USED as an identifier anywhere in the module,
// tests included.
//
// Identifiers and not text: a doc comment naming the function it documents is
// exactly what made the first version of this test report nothing at all, and
// a function called only by a sibling in its own file is legitimately used —
// which made the second version report fifteen false alarms.
//
// Tests count. `package foo_test` is the idiomatic choice here and it can only
// reach exported names, so treating a test caller as no caller would push the
// codebase towards in-package tests to satisfy a lint. What this is for is the
// function nobody calls at ALL.
func referenced(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	forEachSource(t, true, func(_ string, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.FuncDecl:
				// The declaration's own name is not a use of it. Walk the body
				// and signature, and skip the name node.
				if v.Recv != nil {
					ast.Inspect(v.Recv, countIdents(out))
				}
				ast.Inspect(v.Type, countIdents(out))
				if v.Body != nil {
					ast.Inspect(v.Body, countIdents(out))
				}
				return false
			case *ast.Ident:
				out[v.Name]++
			}
			return true
		})
	})
	return out
}

func countIdents(into map[string]int) func(ast.Node) bool {
	return func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			into[id.Name]++
		}
		return true
	}
}

// forEachSource parses every Go file in the module, optionally including tests.
//
// .templ files are read as TEXT and their identifiers counted crudely, because
// they are not Go until templ generates them — and the generated output is
// skipped so a stale *_templ.go cannot keep a name alive.
func forEachSource(t *testing.T, withTests bool, fn func(string, *ast.File)) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join("..", ".."), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// internal/db is generated. cmd/ is skipped only when collecting
			// DECLARATIONS — main and its wiring are called by the runtime, not
			// by goen — but it must be WALKED for references, because
			// server.go is where every handler is named.
			if d.Name() == ".git" || d.Name() == "db" || (!withTests && d.Name() == "cmd") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_templ.go") {
			return nil
		}
		if !withTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		fn(path, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
}

// templateText is every .templ file, which the generated Go is compiled from.
func templateText(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(filepath.Join("..", ".."), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".templ") {
			return err
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		b.Write(src)
		return nil
	})
	if err != nil {
		t.Fatalf("walk for templates: %v", err)
	}
	return b.String()
}
