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

// TestEveryExportedFunctionHasACaller refuses an export named nowhere but its
// own declaration. Nothing here is public API, so an unused export is a feature
// nobody finished.
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

// referenced counts every name used as an identifier anywhere in the module,
// tests included. Identifiers rather than text, or every function matches its
// own doc comment; a use anywhere counts, or a helper called by a sibling in
// its own file reads as an orphan.
func referenced(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	forEachSource(t, true, func(_ string, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.FuncDecl:
				// The declaration's own name is not a use of it.
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
// Generated *_templ.go is skipped so a stale one cannot keep a name alive.
func forEachSource(t *testing.T, withTests bool, fn func(string, *ast.File)) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join("..", ".."), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// cmd/ is skipped for DECLARATIONS (the runtime calls main) but walked
			// for references: server.go is where every handler is named.
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
