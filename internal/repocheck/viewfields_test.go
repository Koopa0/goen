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
// A view model is a struct a template takes: the struct type of a parameter (or
// the receiver) of a templ component, and every struct reachable from it through
// fields, pointers, slices, maps and named types (see templateTakes). A feature's
// other exported types — a form's choice, a store row — are the feature's own
// business and are not checked.
//
// Both sides are keyed <import path>.Type.Field: on the bare name a dead field
// passes as soon as any other struct has one of the same name, and the storefront
// and the back office each declare a CampaignView or an OrderView.
func TestEveryViewModelFieldIsAssigned(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		goenModule + "/internal/admin/customers.CustomersView.Notice": "REPORTED: neither assigned nor rendered, unlike every other back-office Notice",
	}

	templates := templSources(t)
	packages := viewModelPackages(templates)
	files := repositoryGo(t)
	generated := generatedTemplGo(t)
	components := templComponents(generated)
	if len(components) < minComponents {
		t.Fatalf("found %d templ components in the generated *_templ.go files, want at least %d — "+
			"the walk stopped reaching them", len(components), minComponents)
	}
	// A struct a .templ file declares is copied verbatim into its generated
	// file, so the declarations are read from both; the assignments stay
	// hand-written (see isHandWrittenGo).
	taken := templateTakes(slices.Concat(files, generated), components, packages)
	declared, models, holds := declaredFields(taken)
	if len(declared) < minViewModelFields {
		t.Fatalf("found %d view-model fields, want at least %d — the parse is wrong",
			len(declared), minViewModelFields)
	}
	assigned := assignedFields(files, packages, models, holds)
	if len(assigned) < minAssignedFields {
		t.Fatalf("found %d assignments, want at least %d — the walk found nothing",
			len(assigned), minAssignedFields)
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
		model := field[:strings.LastIndex(field, ".")]
		t.Errorf("%s is declared and never assigned.\n"+
			"  %s is a view model because the templ component %s reaches it: as a parameter, a "+
			"field of one, or the result of a method or function it uses. A page that "+
			"reads the field shows the zero value to everybody — which is what "+
			"layouts.Page.CartCount did to the cart badge and Page.Nav did to the "+
			"navigation. Fill it where the view is BUILT, not in each handler, or "+
			"delete it.", field, model, taken[model].component)
	}

	// By identity, never by count.
	for field, why := range allowed {
		if !exempted[field] {
			t.Errorf("the allowlist exempts %s (%s), and either something assigns it now "+
				"or no such field exists — the entry is stale", field, why)
		}
	}

	t.Run("every template has its generated code", func(t *testing.T) {
		t.Parallel()
		generated := map[string]bool{}
		for _, comp := range components {
			generated[comp.src.path] = true
		}
		for name := range templates {
			if !generated[strings.TrimSuffix(name, ".templ")+"_templ.go"] {
				t.Errorf("%s has no generated %s beside it that declares a component: the "+
					"view-model walk reads the generated signatures. Run `make gen`.",
					name, strings.TrimSuffix(filepath.Base(name), ".templ")+"_templ.go")
			}
		}
	})

	t.Run("no package is listed without a template", func(t *testing.T) {
		t.Parallel()
		holding := templatePackages(templates)
		for _, pkg := range templatePackagesWithoutViewModels {
			if !slices.Contains(holding, pkg) {
				t.Errorf("templatePackagesWithoutViewModels lists %s, which holds no template: "+
					"delete the entry", pkg)
			}
		}
	})
}

// goenModule is the repository's module, the prefix of every import path below.
const goenModule = "github.com/koopa0/goen"

// The floors under the walks. Measured on 900683a9: 252 components in 89
// generated files, 236 view models with 1703 fields, 1723 assignments. A walk that stopped
// reaching its input drops far below them; moving a template or a view between
// packages keeps the totals.
const (
	minComponents      = 200
	minViewModelFields = 700
	minAssignedFields  = 700
)

// templatePackagesWithoutViewModels hold templates and no view models: shared
// components, charts, icons and the mail templates take their data as
// parameters, not as structs a handler fills.
//
// Every other package that holds a template is searched for the structs its
// templates take (viewModelPackages), so a template moved into a new package is
// covered without anybody editing a list.
var templatePackagesWithoutViewModels = []string{
	goenModule + "/internal/ui/components",
	goenModule + "/internal/ui/chart",
	goenModule + "/internal/ui/icons",
	goenModule + "/internal/letter",
}

// templatePackages is the import path of every directory that holds a template.
func templatePackages(templates map[string]string) []string {
	var out []string
	for name := range templates {
		pkg := importPath(filepath.ToSlash(filepath.Dir(name)))
		if !slices.Contains(out, pkg) {
			out = append(out, pkg)
		}
	}
	slices.Sort(out)
	return out
}

// viewModelPackages are the packages, by import path, whose structs can be view
// models: those that hold a template, except templatePackagesWithoutViewModels.
// Types are keyed by the import path of the package that declares them: pages and
// pages/admin each declare a CampaignView or an OrderView, and on the bare name a
// field assigned in one would excuse the same name unassigned in the other.
func viewModelPackages(templates map[string]string) []string {
	var out []string
	for _, pkg := range templatePackages(templates) {
		if !slices.Contains(templatePackagesWithoutViewModels, pkg) {
			out = append(out, pkg)
		}
	}
	return out
}

// source is one parsed file and the import path of the package it belongs to.
type source struct {
	file *ast.File
	pkg  string
	path string // from the repository root, slash-separated
}

// component is one templ component: the function templ generated for it, with
// the file it is in.
type component struct {
	name string
	src  source
	// takes is the receiver and every parameter's type expression.
	takes []ast.Expr
	// calls is every function the component's body calls by name: `bannerFrom(ctx)`
	// or `layouts.Foo(…)`. A method call is not here; it is followed from the type
	// it is called on.
	calls []*ast.CallExpr
}

// generatedTemplGo parses every *_templ.go file under internal/.
func generatedTemplGo(t *testing.T) []source {
	t.Helper()
	return goFiles(t, "internal", isGeneratedTempl)
}

// templComponents is every function the generated *_templ.go files declare that
// returns a templ.Component, read from the generated signatures rather than from
// the .templ sources: templ's own parser has already decided what a receiver, a
// generic parameter or a multi-line parameter list is, and `make templ-check`
// keeps the generated files equal to the sources.
func templComponents(generated []source) []component {
	out := make([]component, 0, len(generated))
	for _, src := range generated {
		out = append(out, componentsOf(src)...)
	}
	return out
}

// componentsOf is the templ components one generated file declares.
func componentsOf(src source) []component {
	templName := ""
	for _, spec := range src.file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != "github.com/a-h/templ" {
			continue
		}
		templName = "templ"
		if spec.Name != nil {
			templName = spec.Name.Name
		}
	}
	if templName == "" {
		return nil
	}

	var out []component
	for _, decl := range src.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !returnsComponent(fn, templName) {
			continue
		}
		var takes []ast.Expr
		for _, field := range fields(fn.Recv, fn.Type.Params) {
			takes = append(takes, field.Type)
		}
		comp := component{name: fn.Name.Name, src: src, takes: takes}
		if fn.Body != nil {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					comp.calls = append(comp.calls, call)
				}
				return true
			})
		}
		out = append(out, comp)
	}
	return out
}

// returnsComponent reports whether fn's only result is templ.Component, with
// templ as the name the file imports it by.
func returnsComponent(fn *ast.FuncDecl, templName string) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	sel, ok := fn.Type.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Component" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == templName
}

// declaration is a top-level type or function and the file it is declared in,
// whose imports say what the names in it mean.
type declaration struct {
	src  source
	spec *ast.TypeSpec // a type, or
	fn   *ast.FuncDecl // a function
}

// viewModel is a struct a template takes and the component that takes it, directly
// or through a field of what it takes.
type viewModel struct {
	fields    *ast.StructType
	src       source // the file that declares it
	component string
}

// templateTakes is the set of view models, keyed <import path>.Type: every
// exported struct declared in one of the view-model packages — in a .go file or
// in a .templ file, whose declarations the generated *_templ.go repeats — that a
// templ component takes or reads.
//
// It starts at what a component is handed — the receiver and parameter types —
// and at what it fetches — the results of the package-level functions its body
// calls (`bannerFrom(ctx)` reads a layouts.Banner out of the context). From
// there it follows the type expressions to their declarations: through pointers,
// slices, arrays, maps (key and value), channels, func signatures, generic
// arguments and parentheses; through a named type that is not a struct (`type
// Rows []Row` leads to Row) and through an unexported struct; into every field of
// a struct it reaches, named or embedded, to any depth; and into the results of
// every method declared on a type it reaches (`func (v ListingView)
// AppliedChips() []AppliedChip` is how the template gets its chips).
//
// It stops at a type declared outside the view-model packages: a domain struct
// such as order.Order is a template's input, not a view model a handler fills.
// A struct that no path of this kind leads to is not a view model, even when it is
// exported next to one.
func templateTakes(repository []source, components []component, packages []string) map[string]viewModel {
	decls := map[string]declaration{}
	methods := map[string][]*ast.FuncDecl{} // <import path>.Type -> its methods
	funcs := map[string]declaration{}       // <import path>.Func -> the function
	for _, src := range repository {
		if !slices.Contains(packages, src.pkg) {
			continue
		}
		for _, decl := range src.file.Decls {
			switch decl := decl.(type) {
			case *ast.GenDecl:
				if decl.Tok != token.TYPE {
					continue
				}
				for _, spec := range decl.Specs {
					if spec, ok := spec.(*ast.TypeSpec); ok {
						decls[src.pkg+"."+spec.Name.Name] = declaration{src: src, spec: spec}
					}
				}
			case *ast.FuncDecl:
				if decl.Recv == nil {
					funcs[src.pkg+"."+decl.Name.Name] = declaration{src: src, fn: decl}
				} else if recv := receiverName(decl.Recv); recv != "" {
					methods[src.pkg+"."+recv] = append(methods[src.pkg+"."+recv], decl)
				}
			}
		}
	}
	declared := declaredPackageNames(repository, packages)

	taken := map[string]viewModel{}
	seen := map[string]bool{}
	var visit func(typ ast.Expr, src source, via string)
	reach := func(key string, via string) {
		decl, ok := decls[key]
		if !ok || seen[key] {
			return
		}
		seen[key] = true
		if structType, isStruct := decl.spec.Type.(*ast.StructType); isStruct {
			if decl.spec.Name.IsExported() {
				taken[key] = viewModel{fields: structType, src: decl.src, component: via}
			}
			for _, field := range structType.Fields.List {
				visit(field.Type, decl.src, via)
			}
		} else {
			visit(decl.spec.Type, decl.src, via)
		}
		for _, method := range methods[key] {
			for _, result := range fields(method.Type.Results) {
				visit(result.Type, decl.src, via)
			}
		}
	}
	visit = func(typ ast.Expr, src source, via string) {
		switch t := typ.(type) {
		case *ast.Ident:
			reach(src.pkg+"."+t.Name, via)
		case *ast.SelectorExpr:
			if pkg, ok := t.X.(*ast.Ident); ok {
				if imported, named := packageNames(src, declared)[pkg.Name]; named {
					reach(imported+"."+t.Sel.Name, via)
				}
			}
		case *ast.StarExpr:
			visit(t.X, src, via)
		case *ast.ParenExpr:
			visit(t.X, src, via)
		case *ast.ArrayType:
			visit(t.Elt, src, via)
		case *ast.Ellipsis:
			visit(t.Elt, src, via)
		case *ast.ChanType:
			visit(t.Value, src, via)
		case *ast.MapType:
			visit(t.Key, src, via)
			visit(t.Value, src, via)
		case *ast.IndexExpr:
			visit(t.X, src, via)
			visit(t.Index, src, via)
		case *ast.IndexListExpr:
			visit(t.X, src, via)
			for _, index := range t.Indices {
				visit(index, src, via)
			}
		case *ast.FuncType:
			for _, field := range fields(t.Params, t.Results) {
				visit(field.Type, src, via)
			}
		case *ast.StructType:
			for _, field := range t.Fields.List {
				visit(field.Type, src, via)
			}
		}
	}
	for _, comp := range components {
		via := comp.src.pkg + "." + comp.name
		for _, typ := range comp.takes {
			visit(typ, comp.src, via)
		}
		for _, call := range comp.calls {
			key := ""
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				key = comp.src.pkg + "." + fun.Name
			case *ast.SelectorExpr:
				if pkg, ok := fun.X.(*ast.Ident); ok {
					if imported, named := packageNames(comp.src, declared)[pkg.Name]; named {
						key = imported + "." + fun.Sel.Name
					}
				}
			}
			if fn, ok := funcs[key]; ok {
				for _, result := range fields(fn.fn.Type.Results) {
					visit(result.Type, fn.src, via)
				}
			}
		}
	}
	return taken
}

// receiverName is the type a method is declared on, `Row` for `(r *Row)` and
// `(r Row[T])`.
func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) != 1 {
		return ""
	}
	typ := recv.List[0].Type
	for {
		switch t := typ.(type) {
		case *ast.StarExpr:
			typ = t.X
		case *ast.ParenExpr:
			typ = t.X
		case *ast.IndexExpr:
			typ = t.X
		case *ast.IndexListExpr:
			typ = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// declaredFields is every exported field on every view model, as Type.Field;
// models maps each view model to those of its fields, which a conversion to it
// writes; holds maps each field to the view model it holds — which is what
// resolves a write through a field, `view.Questions[at].Answers = …`.
func declaredFields(taken map[string]viewModel) (names []string, models map[string][]string, holds map[string]string) {
	// The type names first: everything downstream is gated on them, or a
	// `func (v PointsView) Credit() string` contributes `string` as a rival
	// result type and the ambiguity un-resolves every `view.Notice = …`.
	models = map[string][]string{}
	for key := range taken {
		models[key] = nil
	}

	holds = map[string]string{}
	for key, model := range taken {
		w := writes{names: map[string]string{"": model.src.pkg}, models: models}
		for _, field := range model.fields.Fields.List {
			for _, name := range field.Names {
				if !name.IsExported() {
					continue
				}
				fieldKey := key + "." + name.Name
				names = append(names, fieldKey)
				models[key] = append(models[key], fieldKey)
				if held := w.typeName(field.Type); held != "" {
					holds[fieldKey] = held
				}
			}
		}
	}
	slices.Sort(names)
	return names, models, holds
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
func assignedFields(files []source, packages []string, models map[string][]string, holds map[string]string) map[string]bool {
	declared := declaredPackageNames(files, packages)
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
	return goFiles(t, ".", isHandWrittenGo)
}

// isGeneratedTempl reports whether path is Go that templ generated from a .templ.
func isGeneratedTempl(path string) bool {
	return strings.HasSuffix(path, "_templ.go")
}

// goFiles parses every file under dir (relative to the repository root) that keep
// accepts, skipping the directories the go command skips.
func goFiles(t *testing.T, dir string, keep func(path string) bool) []source {
	t.Helper()

	root := repoRoot(t)
	var out []source
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !keep(path) {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		pkgDir, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, source{file: file, pkg: importPath(pkgDir), path: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
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
func resultTypes(files []source, models map[string][]string, declared map[string]string) map[string][]string {
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

// declaredPackageNames maps the import path of each of packages to the
// name its package clause declares, which is what an import without an alias is
// called in the importing file.
func declaredPackageNames(files []source, packages []string) map[string]string {
	out := map[string]string{}
	for _, src := range files {
		if slices.Contains(packages, src.pkg) {
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
	names   map[string]string   // identifier -> import path; "" is the file's own package
	models  map[string][]string // view model -> its exported fields
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
		case *ast.CallExpr:
			w.conversion(node, local)
		}
		return true
	})
}

// conversion records `T(x)`, T a view model, as a write of every field of T.
//
// A call whose function is a type name is a conversion (the Go spec,
// "Conversions"), and the name cannot also be a function's: a package block
// declares an identifier once ("Declarations and scope"). A struct converts to
// T only from a struct whose underlying type is identical to T's, tags aside
// ("Conversions"), and identical struct types have the same fields — the same
// names and types in the same order ("Type identity"). So x carries a value for
// every field of T, adding a field to T without adding it to x's type does not
// compile, and no field can be left out the way a key is left out of a literal.
//
// Converting a T to T writes nothing a T did not already hold, so it does not
// count where the walk can type x; where it cannot, the unconvert linter
// refuses the conversion. A pointer, slice or map type such as `(*T)(p)` does
// not count either: its elements are written wherever they were built.
func (w *writes) conversion(call *ast.CallExpr, local map[string]string) {
	if len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return
	}
	switch call.Fun.(type) {
	case *ast.Ident, *ast.SelectorExpr:
	default:
		return
	}
	model := w.typeName(call.Fun)
	if model == "" || w.valueType(call.Args[0], local) == model {
		return
	}
	for _, field := range w.models[model] {
		w.out[field] = true
	}
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
		if own, ok := w.names[""]; ok && w.isModel(own+"."+t.Name) {
			return own + "." + t.Name
		}
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return ""
		}
		if imported, named := w.names[pkg.Name]; named && w.isModel(imported+"."+t.Sel.Name) {
			return imported + "." + t.Sel.Name
		}
	}
	return ""
}

func (w *writes) isModel(key string) bool {
	_, ok := w.models[key]
	return ok
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
