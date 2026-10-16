package repocheck_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The layering ratchet. Until internal/ui/pages and internal/ui/pages/admin are
// dissolved into the features that render them, code outside internal/ui uses view
// types as data contracts. Each list below names every current occurrence of one
// measure, by identity:
//
//   - viewPackageImporters: a package directory outside internal/ui with a non-test
//     file that imports internal/ui/pages or internal/ui/pages/admin.
//   - dataFilesNamingViews: a non-test file outside internal/ui that imports
//     internal/db or pgx and also selects an identifier from a view import.
//   - viewContracts: an interface method outside internal/ui whose signature selects
//     from a view import, keyed dir.Interface.Method.
//   - storeMethodsNamingViews: a method on a type named *Store whose signature
//     selects from a view import, counted per file.
//   - viewValueReads: a selector x.Name where x is a view import and Name is an
//     exported const or var declared in that view package, counted per file.
//
// The consumer is task series T42-T53, which lowers these lists until
// internal/ui/pages is deleted, at which point the lists become hard rules.
//
// Each list may only shrink. A new entry is a regression: return a domain value
// and map it to the view in the package that renders it. A missing entry is
// progress: delete it (or lower its count) in the same PR.
var viewPackageImporters = []string{
	"cmd/goen",
	"internal/account",
	"internal/admin/dashboard",
	"internal/admin/orders",
	"internal/admin/products",
	"internal/admin/reports",
	"internal/admin/stock",
	"internal/auth",
	"internal/cart",
	"internal/catalog",
	"internal/home",
	"internal/order/buyer",
}

var dataFilesNamingViews = []string{
	"cmd/goen/server.go",
	"internal/account/store.go",
	"internal/admin/dashboard/store.go",
	"internal/admin/orders/picking.go",
	"internal/admin/orders/store.go",
	"internal/admin/orders/timeline.go",
	"internal/admin/products/image.go",
	"internal/admin/products/invoice_line.go",
	"internal/admin/products/product.go",
	"internal/admin/products/product_label.go",
	"internal/admin/products/standing.go",
	"internal/admin/reports/store.go",
	"internal/cart/outbox.go",
	"internal/cart/store.go",
	"internal/catalog/campaign.go",
	"internal/catalog/compare.go",
	"internal/catalog/department.go",
	"internal/catalog/product_store.go",
	"internal/catalog/question.go",
	"internal/catalog/review.go",
	"internal/catalog/store.go",
	"internal/home/hero.go",
	"internal/home/store.go",
	"internal/order/buyer/order.go",
}

var viewContracts = []string{
	"internal/auth.Carts.RecoveryContents",
}

var storeMethodsNamingViews = map[string]int{
	"internal/account/store.go":                3,
	"internal/admin/products/image.go":         1,
	"internal/admin/products/product.go":       4,
	"internal/admin/products/product_label.go": 1,
	"internal/admin/products/standing.go":      2,
	"internal/cart/store.go":                   3,
	"internal/catalog/campaign.go":             2,
	"internal/catalog/compare.go":              3,
	"internal/catalog/department.go":           5,
	"internal/catalog/product_store.go":        8,
	"internal/catalog/question.go":             1,
	"internal/catalog/review.go":               1,
	"internal/catalog/store.go":                7,
	"internal/home/hero.go":                    1,
	"internal/home/store.go":                   5,
	"internal/order/buyer/order.go":            4,
}

var viewValueReads = map[string]int{
	"cmd/goen/server.go":                  2,
	"internal/auth/handler.go":            2,
	"internal/cart/handler.go":            11,
	"internal/cart/store.go":              3,
	"internal/catalog/compare.go":         3,
	"internal/catalog/department.go":      2,
	"internal/catalog/product_handler.go": 8,
	"internal/catalog/review.go":          12,
	"internal/catalog/store.go":           2,
}

const (
	layeringModule = "github.com/koopa0/goen/"
	// minLayeringFiles is the floor for the walk: main parses 493 non-test files, so
	// a walk that read far fewer has not looked at the repository.
	minLayeringFiles = 400
)

// layering is one measurement of the repository.
type layering struct {
	files        int             // non-test .go files parsed, including internal/ui
	viewNames    int             // exported consts and vars declared in view packages
	importers    map[string]bool // package directories
	dataFiles    map[string]bool // files
	contracts    map[string]bool // dir.Interface.Method
	storeMethods map[string]int  // file -> methods
	valueReads   map[string]int  // file -> selectors
}

func isLayeringView(importPath string) bool {
	pages := layeringModule + "internal/ui/pages"
	return importPath == pages || strings.HasPrefix(importPath, pages+"/")
}

func isLayeringData(importPath string) bool {
	return importPath == layeringModule+"internal/db" || strings.HasPrefix(importPath, "github.com/jackc/pgx/")
}

// measureLayering parses every non-test .go file under root, ignoring build tags,
// and counts the five measures. Files under internal/ui are read only for the
// exported const and var names of the view packages.
func measureLayering(t *testing.T, root string) layering {
	t.Helper()

	type parsed struct {
		rel string
		f   *ast.File
	}
	var files []parsed
	viewValues := map[string]map[string]bool{} // view import path -> exported const/var names
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return perr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		files = append(files, parsed{rel, f})

		importPath := layeringModule + filepath.ToSlash(filepath.Dir(rel))
		if !isLayeringView(importPath) {
			return nil
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if !name.IsExported() {
						continue
					}
					if viewValues[importPath] == nil {
						viewValues[importPath] = map[string]bool{}
					}
					viewValues[importPath][name.Name] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	m := layering{
		files:        len(files),
		importers:    map[string]bool{},
		dataFiles:    map[string]bool{},
		contracts:    map[string]bool{},
		storeMethods: map[string]int{},
		valueReads:   map[string]int{},
	}
	for _, names := range viewValues {
		m.viewNames += len(names)
	}

	for _, fl := range files {
		if strings.HasPrefix(fl.rel, "internal/ui/") {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(fl.rel))
		views := map[string]string{} // local name -> view import path
		data := false
		for _, im := range fl.f.Imports {
			p, uerr := strconv.Unquote(im.Path.Value)
			if uerr != nil {
				t.Fatalf("%s: import path %s: %v", fl.rel, im.Path.Value, uerr)
			}
			data = data || isLayeringData(p)
			if !isLayeringView(p) {
				continue
			}
			name := filepath.Base(p)
			if im.Name != nil {
				name = im.Name.Name
			}
			views[name] = p
			m.importers[dir] = true
		}
		if len(views) == 0 {
			continue
		}

		// selectsView reports whether n selects an identifier from a view import.
		selectsView := func(n ast.Node) bool {
			found := false
			ast.Inspect(n, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && views[id.Name] != "" {
						found = true
					}
				}
				return !found
			})
			return found
		}
		contractMethods := func(it *ast.InterfaceType, owner string) {
			for _, field := range it.Methods.List {
				if _, ok := field.Type.(*ast.FuncType); !ok || !selectsView(field.Type) {
					continue
				}
				for _, name := range field.Names {
					m.contracts[owner+"."+name.Name] = true
				}
			}
		}

		if data && selectsView(fl.f) {
			m.dataFiles[fl.rel] = true
		}

		named := map[*ast.InterfaceType]bool{}
		ast.Inspect(fl.f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.TypeSpec:
				if it, ok := n.Type.(*ast.InterfaceType); ok {
					named[it] = true
					contractMethods(it, dir+"."+n.Name.Name)
				}
			case *ast.InterfaceType:
				if !named[n] {
					owner := fmt.Sprintf("%s:%d", fl.rel, fset.Position(n.Pos()).Line)
					contractMethods(n, owner)
				}
			case *ast.SelectorExpr:
				if id, ok := n.X.(*ast.Ident); ok && viewValues[views[id.Name]][n.Sel.Name] {
					m.valueReads[fl.rel]++
				}
			case *ast.FuncDecl:
				if n.Recv == nil || len(n.Recv.List) != 1 {
					break
				}
				recv := n.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if id, ok := recv.(*ast.Ident); ok && strings.HasSuffix(id.Name, "Store") && selectsView(n.Type) {
					m.storeMethods[fl.rel]++
				}
			}
			return true
		})
	}
	return m
}

// checkLayeringList compares a measured set with its allowlist.
func checkLayeringList(t *testing.T, measure string, got map[string]bool, want []string) {
	t.Helper()
	if !slices.IsSorted(want) {
		t.Errorf("%s: the list must stay sorted", measure)
	}
	listed := map[string]bool{}
	for _, w := range want {
		if listed[w] {
			t.Errorf("%s: %q is listed twice", measure, w)
		}
		listed[w] = true
	}
	for _, g := range slices.Sorted(maps.Keys(got)) {
		if !listed[g] {
			t.Errorf("new entry %q: the view layer must not leak further into %s: return a domain value and map it in the rendering package", g, measure)
		}
	}
	for _, w := range slices.Sorted(maps.Keys(listed)) {
		if !got[w] {
			t.Errorf("stale entry %q: progress: delete or lower this entry of %s in the same PR", w, measure)
		}
	}
}

// checkLayeringCounts compares measured per-file counts with their allowlist.
func checkLayeringCounts(t *testing.T, measure string, got, want map[string]int) {
	t.Helper()
	for _, file := range slices.Sorted(maps.Keys(got)) {
		switch listed, ok := want[file]; {
		case !ok:
			t.Errorf("new entry %q (%d): the view layer must not leak further into %s: return a domain value and map it in the rendering package", file, got[file], measure)
		case got[file] > listed:
			t.Errorf("%q rose from %d to %d: the view layer must not leak further into %s: return a domain value and map it in the rendering package", file, listed, got[file], measure)
		}
	}
	for _, file := range slices.Sorted(maps.Keys(want)) {
		switch n := got[file]; {
		case n == 0:
			t.Errorf("stale entry %q (%d): progress: delete or lower this entry of %s in the same PR", file, want[file], measure)
		case n < want[file]:
			t.Errorf("%q fell from %d to %d: progress: delete or lower this entry of %s in the same PR", file, want[file], n, measure)
		}
	}
}

// TestLayeringRatchet holds the view layer where it is: every leak of internal/ui/pages
// into data code is listed by identity, and the lists may only shrink.
func TestLayeringRatchet(t *testing.T) {
	t.Parallel()

	m := measureLayering(t, repoRoot(t))
	// A walk that reads nothing would otherwise equal an empty list.
	if m.files < minLayeringFiles {
		t.Fatalf("the walk parsed %d non-test .go files, want at least %d: it is not looking at the repository", m.files, minLayeringFiles)
	}
	if m.viewNames == 0 {
		t.Fatal("no exported const or var found in internal/ui/pages: viewValueReads would count nothing")
	}

	t.Run("viewPackageImporters", func(t *testing.T) {
		t.Parallel()
		checkLayeringList(t, "viewPackageImporters", m.importers, viewPackageImporters)
	})
	t.Run("dataFilesNamingViews", func(t *testing.T) {
		t.Parallel()
		checkLayeringList(t, "dataFilesNamingViews", m.dataFiles, dataFilesNamingViews)
	})
	t.Run("viewContracts", func(t *testing.T) {
		t.Parallel()
		checkLayeringList(t, "viewContracts", m.contracts, viewContracts)
	})
	t.Run("storeMethodsNamingViews", func(t *testing.T) {
		t.Parallel()
		checkLayeringCounts(t, "storeMethodsNamingViews", m.storeMethods, storeMethodsNamingViews)
	})
	t.Run("viewValueReads", func(t *testing.T) {
		t.Parallel()
		checkLayeringCounts(t, "viewValueReads", m.valueReads, viewValueReads)
	})
}
