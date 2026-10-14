package repocheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestEveryViewModelFieldIsAssigned refuses a field the pages read and nothing
// fills — a zero value is valid, so neither the compiler nor go vet can see it.
//
// Both sides are keyed <import path>.Type.Field: on the bare name a dead field
// passes as soon as any other struct has one of the same name, and the storefront
// and the back office each declare a CampaignView or an OrderView.
func TestEveryViewModelFieldIsAssigned(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		goenModule + "/internal/ui/pages/admin.CustomersView.Notice": "REPORTED: neither assigned nor rendered, unlike every other back-office Notice",
	}

	files := repositoryGo(t)
	declared, models, holds := declaredFields(t, files)
	if len(declared) < 700 {
		t.Fatalf("found %d view-model fields, want far more — the parse is wrong",
			len(declared))
	}
	assigned := assignedFields(files, models, holds)
	if len(assigned) < 700 {
		t.Fatalf("found %d assignments, want far more — the walk found nothing",
			len(assigned))
	}

	exempted := make(map[string]bool, len(allowed))
	for _, field := range declared {
		if assigned[field] {
			continue
		}
		if _, ok := allowed[field]; ok {
			exempted[field] = true
			continue
		}
		t.Errorf("%s is declared and never assigned.\n"+
			"  A page that reads it shows the zero value to everybody — which is what "+
			"layouts.Page.CartCount did to the cart badge and Page.Nav did to the "+
			"navigation. Fill it where the view is BUILT, not in each handler, or "+
			"delete it.", field)
	}

	// By identity, never by count.
	for field, why := range allowed {
		if !exempted[field] {
			t.Errorf("the allowlist exempts %s (%s), and either something assigns it now "+
				"or no such field exists — the entry is stale", field, why)
		}
	}

	t.Run("every directory holding a template is classified", func(t *testing.T) {
		t.Parallel()
		reported := map[string]bool{}
		for name := range templSources(t) {
			dir := filepath.ToSlash(filepath.Dir(name))
			pkg := importPath(dir)
			if reported[dir] || slices.Contains(viewModelPackages, pkg) ||
				slices.Contains(templatePackagesWithoutViewModels, pkg) {
				continue
			}
			reported[dir] = true
			t.Errorf("%s holds a template (%s) and %s is in neither viewModelPackages nor "+
				"templatePackagesWithoutViewModels. Decide whether its view structs are view "+
				"models; until then their fields are not checked for being assigned.",
				dir, name, pkg)
		}
	})
}

// goenModule is the repository's module, the prefix of every import path below.
const goenModule = "github.com/koopa0/goen"

// viewModelPackages are the packages, by import path, whose exported structs are
// view models. Types are keyed by the import path of the package that declares
// them: pages and pages/admin each declare a CampaignView or an OrderView, and on
// the bare name a field assigned in one would excuse the same name unassigned in
// the other.
//
// This list and templatePackagesWithoutViewModels together must name every
// directory that holds a template (TestEveryViewModelFieldIsAssigned checks it),
// so a template moved into a new package cannot leave that package's view
// structs unchecked without somebody deciding so.
var viewModelPackages = []string{
	goenModule + "/internal/ui/pages",
	goenModule + "/internal/ui/pages/admin",
	goenModule + "/internal/ui/layouts",
}

// templatePackagesWithoutViewModels hold templates and no view models: shared
// components, charts, icons and the mail templates take their data as
// parameters, not as structs a handler fills.
var templatePackagesWithoutViewModels = []string{
	goenModule + "/internal/ui/components",
	goenModule + "/internal/ui/chart",
	goenModule + "/internal/ui/icons",
	goenModule + "/internal/email",
}

// source is one parsed file and the import path of the package it belongs to.
type source struct {
	file *ast.File
	pkg  string
}

// declaredFields is every exported field on every exported view model, as
// Type.Field, with the view model each of those fields holds — which is what
// resolves a write through a field, `view.Questions[at].Answers = …`.
func declaredFields(t *testing.T, repository []source) (names []string, models map[string]bool, holds map[string]string) {
	t.Helper()

	files := viewModelSources(t, repository)

	// The type names first: everything downstream is gated on them, or a
	// `func (v PointsView) Credit() string` contributes `string` as a rival
	// result type and the ambiguity un-resolves every `view.Notice = …`.
	models = map[string]bool{}
	forEachStruct(files, func(src source, spec *ast.TypeSpec, _ *ast.StructType) {
		models[src.pkg+"."+spec.Name.Name] = true
	})

	holds = map[string]string{}
	forEachStruct(files, func(src source, spec *ast.TypeSpec, structType *ast.StructType) {
		w := writes{names: map[string]string{"": src.pkg}, models: models}
		for _, field := range structType.Fields.List {
			for _, name := range field.Names {
				if !name.IsExported() {
					continue
				}
				key := src.pkg + "." + spec.Name.Name + "." + name.Name
				names = append(names, key)
				if held := w.typeName(field.Type); held != "" {
					holds[key] = held
				}
			}
		}
	})
	slices.Sort(names)
	return names, models, holds
}

// forEachStruct visits every exported struct type declared in files.
func forEachStruct(files []source, visit func(source, *ast.TypeSpec, *ast.StructType)) {
	for _, src := range files {
		ast.Inspect(src.file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok || !spec.Name.IsExported() {
				return true
			}
			if structType, isStruct := spec.Type.(*ast.StructType); isStruct {
				visit(src, spec, structType)
			}
			return true
		})
	}
}

// viewModelSources is the part of the repository's Go that declares view models:
// the files of the packages named in viewModelPackages. Every entry must hold a
// file, or it names a package that is not there and the guard would read
// nothing from it.
func viewModelSources(t *testing.T, repository []source) []source {
	t.Helper()

	var out []source
	for _, pkg := range viewModelPackages {
		before := len(out)
		for _, src := range repository {
			if src.pkg == pkg {
				out = append(out, src)
			}
		}
		if len(out) == before {
			t.Errorf("viewModelPackages names %s, which holds no hand-written Go", pkg)
		}
	}
	return out
}

// isHandWrittenGo reports whether path is Go this repository wrote. Tests do not
// count: a field only a test fills is exactly the defect, and a guard that read
// test files could be satisfied by its own error message.
func isHandWrittenGo(path string) bool {
	return strings.HasSuffix(path, ".go") &&
		!strings.HasSuffix(path, "_templ.go") &&
		!strings.HasSuffix(path, "_test.go")
}

// assignedFields is every Type.Field the repository writes.
func assignedFields(files []source, models map[string]bool, holds map[string]string) map[string]bool {
	declared := declaredPackageNames(files)
	returns := resultTypes(files, models, declared)
	out := map[string]bool{}
	for _, src := range files {
		w := writes{
			names: packageNames(src, declared), models: models, pkg: src.pkg,
			returns: returns, holds: holds, out: out,
		}
		w.file(src.file)
	}
	return out
}

// repositoryGo parses every hand-written Go file in the repository.
func repositoryGo(t *testing.T) []source {
	t.Helper()

	root := repoRoot(t)
	var out []source
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isHandWrittenGo(path) {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		dir, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		out = append(out, source{file: file, pkg: importPath(dir)})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	return out
}

// importPath is the import path of the package in a directory given relative to
// the repository root.
func importPath(dir string) string {
	if dir == "." {
		return goenModule
	}
	return goenModule + "/" + filepath.ToSlash(dir)
}

// resultTypes maps a function or method NAME to the view model each of its
// results carries. The key is a bare name because typing `h.store` needs the type
// checker; a name bound to two different view models is dropped, not guessed.
func resultTypes(files []source, models map[string]bool, declared map[string]string) map[string][]string {
	candidates := map[string]map[int]map[string]bool{}
	note := func(key string, i int, name string) {
		if candidates[key] == nil {
			candidates[key] = map[int]map[string]bool{}
		}
		if candidates[key][i] == nil {
			candidates[key][i] = map[string]bool{}
		}
		candidates[key][i][name] = true
	}
	for _, src := range files {
		w := writes{names: packageNames(src, declared), models: models}
		for _, decl := range src.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Type.Results == nil {
				continue
			}
			for i, result := range flatten(fn.Type.Results) {
				name := w.typeName(result)
				if name == "" {
					continue
				}
				note(fn.Name.Name, i, name)
				note(src.pkg+"."+fn.Name.Name, i, name)
			}
		}
	}

	out := map[string][]string{}
	for name, positions := range candidates {
		results := make([]string, widest(positions)+1)
		for i, found := range positions {
			if len(found) != 1 {
				continue
			}
			for only := range found {
				results[i] = only
			}
		}
		out[name] = results
	}
	return out
}

func widest(positions map[int]map[string]bool) int {
	out := -1
	for i := range positions {
		out = max(out, i)
	}
	return out
}

// flatten expands a result list into one entry per value, since `(a, b Thing)`
// declares two.
func flatten(list *ast.FieldList) []ast.Expr {
	var out []ast.Expr
	for _, field := range list.List {
		for range max(len(field.Names), 1) {
			out = append(out, field.Type)
		}
	}
	return out
}

// declaredPackageNames maps the import path of each view-model package to the
// name its package clause declares, which is what an import without an alias is
// called in the importing file.
func declaredPackageNames(files []source) map[string]string {
	out := map[string]string{}
	for _, src := range files {
		if slices.Contains(viewModelPackages, src.pkg) {
			out[src.pkg] = src.file.Name.Name
		}
	}
	return out
}

// packageNames maps each identifier one file uses to name a view-model package
// to that package's import path: "" is the file's own package, an alias is the
// alias, and an import without one is the name the package declares. Types are
// keyed by import path, so two packages sharing a name or a last path element
// cannot be confused.
func packageNames(src source, declared map[string]string) map[string]string {
	out := map[string]string{"": src.pkg}
	for _, spec := range src.file.Imports {
		imported := strings.Trim(spec.Path.Value, `"`)
		name, ok := declared[imported]
		if !ok {
			continue
		}
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		out[name] = imported
	}
	return out
}

// writes collects the Type.Field pairs one file assigns.
type writes struct {
	names   map[string]string // identifier -> import path; "" is the file's own package
	models  map[string]bool
	pkg     string
	returns map[string][]string
	holds   map[string]string
	out     map[string]bool
}

func (w *writes) file(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			w.literal(node, node.Type)
		case *ast.FuncDecl:
			w.function(node)
		}
		return true
	})
}

// literal records the keys of a composite literal, carrying the element type into
// the untyped literals nested in it.
func (w *writes) literal(lit *ast.CompositeLit, typ ast.Expr) {
	name := w.typeName(typ)
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if key, isIdent := kv.Key.(*ast.Ident); isIdent && name != "" {
				w.out[name+"."+key.Name] = true
			}
			elt = kv.Value
		}
		if inner, ok := elt.(*ast.CompositeLit); ok {
			w.literal(inner, orElse(inner.Type, elementType(typ)))
		}
	}
}

// function records what a function body assigns through a variable it can type.
func (w *writes) function(fn *ast.FuncDecl) {
	local := map[string]string{}
	bind := func(name *ast.Ident, typeName string) {
		if name != nil && typeName != "" {
			local[name.Name] = typeName
		}
	}
	for _, param := range fields(fn.Type.Params, fn.Type.Results, fn.Recv) {
		for _, name := range param.Names {
			bind(name, w.typeName(param.Type))
		}
	}

	ast.Inspect(fn, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			// A closure's parameter is how a refusal reaches the view it fills.
			for _, param := range fields(node.Type.Params) {
				for _, name := range param.Names {
					bind(name, w.typeName(param.Type))
				}
			}
		case *ast.ValueSpec:
			for _, name := range node.Names {
				bind(name, w.typeName(node.Type))
			}
			w.declare(node.Names, node.Values, local, bind)
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				w.record(lhs, local)
			}
			w.declare(identsOf(node.Lhs), node.Rhs, local, bind)
		case *ast.IncDecStmt:
			w.record(node.X, local)
		}
		return true
	})
}

// declare binds the names on the left of a declaration or assignment to the types
// on its right, one call returning several values included.
func (w *writes) declare(names []*ast.Ident, values []ast.Expr, local map[string]string, bind func(*ast.Ident, string)) {
	if call, ok := singleCall(values); ok && len(names) > 1 {
		for i, name := range names {
			bind(name, w.callResult(call, i))
		}
		return
	}
	for i, name := range names {
		if i < len(values) {
			bind(name, w.valueType(values[i], local))
		}
	}
}

// record notes an assignment whose left-hand side reaches a view model's field.
func (w *writes) record(lhs ast.Expr, local map[string]string) {
	sel, ok := lhs.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if base := w.valueType(sel.X, local); base != "" {
		w.out[base+"."+sel.Sel.Name] = true
	}
}

// valueType is the view model an expression evaluates to: a variable, an element
// of one, a field of one, a literal, a call.
func (w *writes) valueType(expr ast.Expr, local map[string]string) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return local[e.Name]
	case *ast.ParenExpr:
		return w.valueType(e.X, local)
	case *ast.StarExpr:
		return w.valueType(e.X, local)
	case *ast.UnaryExpr:
		return w.valueType(e.X, local)
	case *ast.IndexExpr:
		return w.valueType(e.X, local)
	case *ast.CompositeLit:
		return w.typeName(e.Type)
	case *ast.CallExpr:
		return w.callResult(e, 0)
	case *ast.SelectorExpr:
		if base := w.valueType(e.X, local); base != "" {
			return w.holds[base+"."+e.Sel.Name]
		}
	}
	return ""
}

// callResult is the view model a call's i-th result carries.
func (w *writes) callResult(call *ast.CallExpr, i int) string {
	if fn, ok := call.Fun.(*ast.Ident); ok && (fn.Name == "new" || fn.Name == "make") && len(call.Args) >= 1 {
		return w.typeName(call.Args[0])
	}
	name := calleeName(call.Fun)
	if own := at(w.returns[w.pkg+"."+name], i); own != "" {
		return own
	}
	return at(w.returns[name], i)
}

// at is the i-th result, or "" past the end.
func at(results []string, i int) string {
	if i < 0 || i >= len(results) {
		return ""
	}
	return results[i]
}

// typeName is the view model a type expression names, or "" for anything else. A
// slice, map, array or pointer of one answers with the view model itself, which
// is what resolves `opts[i].Selected = …`.
func (w *writes) typeName(typ ast.Expr) string {
	switch t := typ.(type) {
	case *ast.StarExpr:
		return w.typeName(t.X)
	case *ast.ArrayType:
		return w.typeName(t.Elt)
	case *ast.MapType:
		return w.typeName(t.Value)
	case *ast.Ident:
		if own, ok := w.names[""]; ok && w.models[own+"."+t.Name] {
			return own + "." + t.Name
		}
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return ""
		}
		if imported, named := w.names[pkg.Name]; named && w.models[imported+"."+t.Sel.Name] {
			return imported + "." + t.Sel.Name
		}
	}
	return ""
}

// calleeName is `Orders` for both `Orders(…)` and `h.store.Orders(…)`.
func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// singleCall reports the one call on the right of `a, b := f()`.
func singleCall(values []ast.Expr) (*ast.CallExpr, bool) {
	if len(values) != 1 {
		return nil, false
	}
	call, ok := values[0].(*ast.CallExpr)
	return call, ok
}

// identsOf keeps the plain names out of an assignment's left-hand side.
func identsOf(exprs []ast.Expr) []*ast.Ident {
	out := make([]*ast.Ident, len(exprs))
	for i, expr := range exprs {
		if name, ok := expr.(*ast.Ident); ok {
			out[i] = name
		}
	}
	return out
}

// elementType is what a slice, array, map or pointer type holds.
func elementType(typ ast.Expr) ast.Expr {
	switch t := typ.(type) {
	case *ast.ArrayType:
		return t.Elt
	case *ast.MapType:
		return t.Value
	case *ast.StarExpr:
		return elementType(t.X)
	}
	return nil
}

func fields(lists ...*ast.FieldList) []*ast.Field {
	var out []*ast.Field
	for _, list := range lists {
		if list != nil {
			out = append(out, list.List...)
		}
	}
	return out
}

func orElse(first, second ast.Expr) ast.Expr {
	if first != nil {
		return first
	}
	return second
}
