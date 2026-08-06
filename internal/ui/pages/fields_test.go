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

// TestEveryViewModelFieldIsAssigned refuses a field the pages read and nothing fills.
//
// # The defect, twice
//
// layouts.Page.CartCount existed from the day the header was built and no handler
// ever set it, so the cart badge read 0 for every visitor with anything in their
// cart. layouts.Page.Nav was the same field three lines down: the header reads it to
// mark the current category and to emit aria-current="page", and nothing assigned it
// — so no navigation item had ever been highlighted and a screen reader was never
// told where the visitor was.
//
// Neither is a compile error. `go vet` cannot see them either, because a zero value
// is valid: an int that is 0 and a string that is "" are exactly what an unfilled
// field looks like, and exactly what a page with nothing to show looks like.
//
// # What this asks
//
// Every exported field declared on a view model in this package or in
// internal/ui/layouts must be WRITTEN somewhere in the repository — in a struct
// literal, by an assignment, or by a compound operator. Reading it is not enough;
// being read and never written is precisely the defect.
//
// # The namesake, which is what this guard was actually passing on
//
// The first cut keyed on the BARE FIELD NAME on both sides: it collected `Number`
// rather than `Page.Number`, and asked whether anything in the repository matched
// `Number:` or `.Number =`. So a field was covered by ANY other struct's field of
// the same name. A third-party review proved it: a dead `Number` added to
// layouts.Page passed, because internal/payment/handler.go fills `PayView.Number`;
// renaming it `Numberz` went red naming Page correctly; reverting went green.
// CartCount and Nav were caught only because those two names are unique in this
// repository — a third field called Total, Status, Count, Name or ID would not have
// been, and those are the names a view model is most likely to grow.
//
// # What ties an assignment to a type
//
// go/ast rather than a regular expression, because the question is about types and
// a regular expression cannot see one. A composite literal names its type at the
// literal — `layouts.Page{Nav: ...}` — and the import that qualifies it is resolved
// per FILE, so an aliased import still resolves and an unrelated package called
// layouts does not.
//
// A bare `x.Field = v` names no type, so x is resolved against the bindings its own
// function makes: a parameter, a `var x T`, an `x := T{}`, an `x := &T{}`, a
// `new(T)`, and — the shape every handler here actually uses — the declared result
// of the call it came from. From there the whole left-hand side is followed, so
// `opts[i].Selected` and `view.Questions[at].Answers` each land on the type that
// declares the field rather than on nothing.
//
// This does NOT run the type checker. Doing it properly means loading packages,
// which means golang.org/x/tools as a direct dependency of a repository that
// measures its module graph before adding one. What is here is stdlib and reaches
// 937 of the 946 fields; the nine it does not reach are in the allowlist with their
// reasons, and every one of them turned out to be a real defect rather than a limit.
//
// # Known limits
//
//   - A call is resolved BY NAME, because knowing what `h.store` is needs the type
//     checker. One name declared twice with two DIFFERENT view models is dropped
//     rather than guessed — crediting both would be the namesake defect again, one
//     level up — and a declaration in the caller's own package is preferred first,
//     which is what tells product.Store.Load from home.Store.Load. Results that are
//     not view models are ignored rather than counted as rivals: a `Credit() string`
//     cannot be the thing somebody sets `.Reason` on, because that would not
//     compile.
//   - A variable typed through something this cannot follow — an interface, a type
//     assertion, a channel receive — reads as unassigned. That is the SAFE
//     direction: it fails loudly and gets an allowlist entry naming what fills it,
//     never a silent pass.
//   - .templ files are not parsed, because they are not Go. Every view model in
//     this repository is BUILT in a .go file and only RENDERED in a .templ one,
//     which is the split the write-face rule already draws; a field first assigned
//     inside a template would be reported here, and that is the right conversation
//     to have.
//   - Only EXPORTED structs in these two packages are asked about, and only their
//     exported fields. An unexported view model is not part of the contract a
//     handler in another package has to fill.
func TestEveryViewModelFieldIsAssigned(t *testing.T) {
	t.Parallel()

	// A field a page legitimately never fills: the zero value IS the answer, and
	// naming it here is the claim that somebody checked. Keyed Type.Field, so an
	// exemption cannot spread to another view model's field of the same name.
	//
	// Every entry below is REPORTED rather than accepted. The bare-name match this
	// guard used to run found none of them — each was covered by some other view
	// model's Title, Email, Reason, Amount, Notice or NameEn — and the fix for each
	// is in a handler or a store, not in this file. They are here so the build is
	// not red while they wait, and each retires itself the moment it is filled.
	allowed := map[string]string{
		// The credit grant form loses what an admin typed. These three carry a
		// refused form's values back into it — their own comment says so — and
		// GrantCredit answers 303 to /admin/credit?needs=1 instead of re-rendering,
		// so admincredit.templ renders `value={ v.Email }` empty on every refusal.
		// That is the write-face rule's "a rejected form re-renders at 422 with the
		// submitted values intact", not implemented on this one form.
		"AdminCreditView.Email":  "REPORTED: GrantCredit redirects instead of re-rendering, so a refused form comes back blank",
		"AdminCreditView.Reason": "REPORTED: GrantCredit redirects instead of re-rendering, so a refused form comes back blank",
		"AdminCreditView.Amount": "REPORTED: GrantCredit redirects instead of re-rendering, so a refused form comes back blank",

		// The English name of a delivery method is edited on a form that never
		// shows the current one. adminshipping.templ renders `value={ m.NameEn }`
		// and admin/shipping.go builds AdminShippingMethod without it, so the box
		// is blank whether or not a translation exists — and saving the form as
		// presented is how an existing one gets blanked.
		"AdminShippingMethod.NameEn":    "REPORTED: the store never fills it, so the edit form shows an empty English name",
		"AdminShippingMethod.CarrierEn": "REPORTED: the store never fills it, so the edit form shows an empty English carrier",

		// Declared and read by nothing: the campaign edit page is built from the
		// slug and its products alone, so it heads the page with the slug and never
		// says what the campaign is called, when it ends, or whether it is running.
		// Either the page should show them or the three fields should go.
		"AdminCampaignView.Title":   "REPORTED: neither assigned nor rendered; the edit page heads itself with the slug",
		"AdminCampaignView.EndsAt":  "REPORTED: neither assigned nor rendered; the edit page never states the window",
		"AdminCampaignView.Running": "REPORTED: neither assigned nor rendered; the edit page never says whether it is live",

		// Neither assigned nor rendered. Every other back-office view carries a
		// Notice and shows it; this one has the field and no template line, so it
		// was copied in rather than decided on.
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

	// By IDENTITY, never by count: an entry that names a field something assigns
	// now would otherwise go on reading as a decision somebody made.
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
// Type.Field — together with, for each of those fields, the view model it holds.
//
// The second half is what lets `view.Questions[at].Answers = …` be resolved: the
// base variable's type gives ProductQuestionsView, its Questions field is declared
// []Question, and the write lands on Question.Answers. Without it that assignment
// names no type this can see, and Question.Answers reads as dead.
func declaredFields(t *testing.T) (names []string, models map[string]bool, holds map[string]string) {
	t.Helper()

	files := parseDirs(t, viewModelDirs...)

	// The type names first, because everything downstream is gated on them: a
	// bare identifier inside package pages is only a view model if it is one of
	// these. Without the gate `func (v PointsView) Credit() string` contributes
	// `string` as a rival result type for the name Credit, and the ambiguity
	// silently un-resolves every `view.Notice = …` in the back office.
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

// isHandWrittenGo reports whether path is Go this repository wrote.
//
// TESTS DO NOT COUNT, and that is the whole point rather than a convenience: a
// field only a test fills is exactly the defect — the page reads it, the suite
// constructs it, and no handler ever supplies it.
//
// It also stopped this guard being satisfied by its own proof. The first version
// matched `.Nav = ` inside a t.Errorf message in the test below, so deleting the
// real assignment left it green.
func isHandWrittenGo(path string) bool {
	return strings.HasSuffix(path, ".go") &&
		!strings.HasSuffix(path, "_templ.go") && // generated from the .templ beside it
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
// results carries.
//
// This is what makes the common shape resolvable at all. A handler almost never
// writes the type down:
//
//	view, err := h.store.Orders(…)
//	view.Notice = noticeFor(r)
//
// so without the store method's own signature there is nothing to tie Notice to
// AdminOrdersView, and 78 of this repository's fields read as unassigned.
//
// A NAME is the key rather than a receiver-plus-name, because knowing what type
// `h.store` is needs the type checker this deliberately does not run. Results
// that are NOT view models are ignored rather than counted as a rival candidate:
// `AccountView.Credit() string` and `(*admin.Store).Credit() AdminCreditView`
// share a name, and a string has no field to assign, so the code that compiles
// settles it. Two different VIEW MODELS under one name is the ambiguity that
// cannot be settled that way, and there the entry is dropped — a binding that
// credited both would be the namesake defect again, one level up.
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

// viewModelPackages is the set of identifiers this file uses to name the two
// view-model packages — the import alias where there is one, and "" for a file
// inside one of them, whose types need no qualifier.
//
// Resolved per file rather than assumed, so `import l "…/internal/ui/layouts"`
// still resolves and an unrelated package that happens to be called layouts does
// not.
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

// literal records the keys of a composite literal, and carries an element type
// into the untyped literals nested in it — the `{{Name: …}}` inside a
// `[]pages.Crumb{…}` names no type of its own.
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
// on its right — one call returning several values included, which is the shape
// every handler in this repository uses.
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

// valueType is the view model an expression evaluates to, following the chain a
// left-hand side can be: a variable, an element of one, a field of one, a literal,
// a call.
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

// typeName is the view model a type expression names, or "" for anything else.
//
// A slice, map, array or pointer of a view model answers with the view model
// itself. That is not a confusion: it is what lets `opts[i].Selected = …` resolve,
// and a field selector on the container itself would not compile, so there is
// nothing the conflation can wrongly credit.
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

// calleeName is the bare name a call names, `Orders` for both `Orders(…)` and
// `h.store.Orders(…)`.
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
