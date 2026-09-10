package pages_test

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
// Both sides are keyed Type.Field: on the bare name a dead field passes as soon
// as any other struct has one of the same name.
func TestEveryViewModelFieldIsAssigned(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"AdminCreditView.Email":  "REPORTED: GrantCredit redirects instead of re-rendering, so a refused form comes back blank",
		"AdminCreditView.Reason": "REPORTED: GrantCredit redirects instead of re-rendering, so a refused form comes back blank",
		"AdminCreditView.Amount": "REPORTED: GrantCredit redirects instead of re-rendering, so a refused form comes back blank",

		"AdminCampaignView.Title":   "REPORTED: neither assigned nor rendered; the edit page heads itself with the slug",
		"AdminCampaignView.EndsAt":  "REPORTED: neither assigned nor rendered; the edit page never states the window",
		"AdminCampaignView.Running": "REPORTED: neither assigned nor rendered; the edit page never says whether it is live",

		"AdminCustomersView.Notice": "REPORTED: neither assigned nor rendered, unlike every other back-office Notice",
	}

	declared, models, holds := declaredFields(t)
	if len(declared) < 700 {
		t.Fatalf("found %d view-model fields, want far more — the parse is wrong",
			len(declared))
	}
	assigned := assignedFields(t, models, holds)
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
}

// viewModelDirs are the two packages whose exported structs are view models.
var viewModelDirs = []string{".", filepath.Join("..", "layouts")}

// declaredFields is every exported field on every exported view model, as
// Type.Field, with the view model each of those fields holds — which is what
// resolves a write through a field, `view.Questions[at].Answers = …`.
func declaredFields(t *testing.T) (names []string, models map[string]bool, holds map[string]string) {
	t.Helper()

	files := parseDirs(t, viewModelDirs...)

	// The type names first: everything downstream is gated on them, or a
	// `func (v PointsView) Credit() string` contributes `string` as a rival
	// result type and the ambiguity un-resolves every `view.Notice = …`.
	models = map[string]bool{}
	forEachStruct(files, func(spec *ast.TypeSpec, _ *ast.StructType) {
		models[spec.Name.Name] = true
	})

	holds = map[string]string{}
	w := writes{names: map[string]bool{"": true}, models: models}
	forEachStruct(files, func(spec *ast.TypeSpec, structType *ast.StructType) {
		for _, field := range structType.Fields.List {
			for _, name := range field.Names {
				if !name.IsExported() {
					continue
				}
				key := spec.Name.Name + "." + name.Name
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
func forEachStruct(files []*ast.File, visit func(*ast.TypeSpec, *ast.StructType)) {
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok || !spec.Name.IsExported() {
				return true
			}
			if structType, isStruct := spec.Type.(*ast.StructType); isStruct {
				visit(spec, structType)
			}
			return true
		})
	}
}

// parseDirs parses the hand-written Go in each directory.
func parseDirs(t *testing.T, dirs ...string) []*ast.File {
	t.Helper()

	var out []*ast.File
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		out = slices.Grow(out, len(entries))
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			if !isHandWrittenGo(path) {
				continue
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				t.Fatalf("parse %s: %v", path, parseErr)
			}
			out = append(out, file)
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
func assignedFields(t *testing.T, models map[string]bool, holds map[string]string) map[string]bool {
	t.Helper()

	files := repositoryGo(t)
	returns := resultTypes(files, models)
	out := map[string]bool{}
	for _, file := range files {
		w := writes{
			names: viewModelPackages(file), models: models, pkg: file.Name.Name,
			returns: returns, holds: holds, out: out,
		}
		w.file(file)
	}
	return out
}

// repositoryGo parses every hand-written Go file in the repository.
func repositoryGo(t *testing.T) []*ast.File {
	t.Helper()

	var out []*ast.File
	err := filepath.WalkDir(repoRoot(t), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
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
		out = append(out, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}
	return out
}

// resultTypes maps a function or method NAME to the view model each of its
// results carries. The key is a bare name because typing `h.store` needs the type
// checker; a name bound to two different view models is dropped, not guessed.
func resultTypes(files []*ast.File, models map[string]bool) map[string][]string {
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
	for _, file := range files {
		w := writes{names: viewModelPackages(file), models: models}
		for _, decl := range file.Decls {
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
				note(file.Name.Name+"."+fn.Name.Name, i, name)
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

// viewModelPackages is the set of identifiers one file uses to name the two
// view-model packages — the import alias where there is one, and "" inside them.
func viewModelPackages(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file.Name.Name == "pages" || file.Name.Name == "layouts" {
		out[""] = true
	}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if path != "github.com/koopa0/goen/internal/ui/pages" &&
			path != "github.com/koopa0/goen/internal/ui/layouts" {
			continue
		}
		if spec.Name != nil {
			out[spec.Name.Name] = true
			continue
		}
		out[filepath.Base(path)] = true
	}
	return out
}

// writes collects the Type.Field pairs one file assigns.
type writes struct {
	names   map[string]bool
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
	if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "new" && len(call.Args) == 1 {
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
		if w.names[""] && w.models[t.Name] {
			return t.Name
		}
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if ok && w.names[pkg.Name] && w.models[t.Sel.Name] {
			return t.Sel.Name
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
