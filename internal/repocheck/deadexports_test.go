package repocheck_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// minExportedExamined is the floor under the exported identifiers the analysis
// examines (package-level functions, variables, constants and types, plus the
// exported methods of exported types). Deleting a dead identifier lowers the
// count by one; an analysis that loaded too little drops it far below. The
// module examines about 5600 (2600 variables, mostly message keys, 1500
// methods, 560 types, 550 constants and 430 functions).
const minExportedExamined = 5000

// unreferencedExports lists the exported identifiers that nothing in the module
// refers to and that stay anyway, key "import/path.Name" or
// "import/path.Type.Method", value the reason. An entry may only disappear: one
// that is now referenced, or that no longer exists, fails, so this table cannot
// outlive its subject. A new dead identifier is deleted, not listed.
var unreferencedExports = map[string]string{
	"github.com/koopa0/goen/internal/payment.StatusRequiresPayment":        paymentStatusMember,
	"github.com/koopa0/goen/internal/payment.StatusRequiresAction":         paymentStatusMember,
	"github.com/koopa0/goen/internal/payment.StatusProcessing":             paymentStatusMember,
	"github.com/koopa0/goen/internal/payment.StatusRequiresReconciliation": paymentStatusMember,
	"github.com/koopa0/goen/internal/payment.StatusCancelled":              paymentStatusMember,
	"github.com/koopa0/goen/internal/payment.StatusReconciled":             paymentStatusMember,

	"github.com/koopa0/goen/internal/admin/audit.ActionIssueInvoice": invoiceActionMember,
	"github.com/koopa0/goen/internal/admin/audit.ActionVoidInvoice":  invoiceActionMember,
	"github.com/koopa0/goen/internal/admin/audit.ActionAllowInvoice": invoiceActionMember,

	"github.com/koopa0/goen/internal/ui/components.SizeMedium": "the default step: class() has no case for it and renders the empty class, " +
		"so a caller that leaves Size empty and one that names SizeMedium get the same button; the name is the " +
		"third member of the Size set beside SizeSmall and SizeLarge",
	"github.com/koopa0/goen/internal/cart.FreeDeliveryUnstated": "the zero value of FreeDeliveryKind, named because silence is a " +
		"decision (the methods on offer disagree); every switch on Kind falls through to it",
	"github.com/koopa0/goen/internal/loyalty.ReversedForReturn": "the 'return' reason that reverse_return_points writes into " +
		"loyalty_entries.reason; pointsHistoryEntry converts the stored text with " +
		"PointsReversalReason(r.Reason), so the value arrives from the database and only its sibling " +
		"ReversedForCancellation is compared in Go",
}

// paymentStatusMember explains the six payment.Status constants nothing reads.
const paymentStatusMember = "a member of the Status set that payments_status_known closes; Go reads only " +
	"StatusSucceeded, the other values arrive from stored functions and are scanned as text, and a set " +
	"listed in part would read as the whole of the column's vocabulary"

// invoiceActionMember explains the three invoice Action constants nothing reads.
const invoiceActionMember = "the schema writes this action itself, as 'invoice.' || kind in the invoice " +
	"audit function, so no Go code passes it; the constant keeps the action in action.go, which " +
	"TestEveryActionConstantIsInTheLedger reads and pages/admin's actionLabels phrases"

// exportedObject is one declared, exported, hand-written identifier.
type exportedObject struct {
	key  string // "import/path.Name" or "import/path.Type.Method"
	kind string
	pos  token.Position
}

// TestEveryExportedIdentifierIsReferenced refuses an exported identifier that
// nothing in the module refers to. The unused linter cannot see it: exported
// code is reachable by a caller it cannot know about, but this module has none
// outside itself, so a zero count is dead code. Two such names were dead copies
// of SQL policy numbers, a second answer that a reader would trust.
//
// What it reads: every package of the module, built with the integration tag,
// with each package's test variants (internal and external test packages), so a
// reference from a test counts. A reference is a use recorded by go/types, which
// includes the generated *_templ.go files and sqlc code, and never a comment, a
// string or a declaration. Identity is the declaring source position of the
// go/types object, so a same-named identifier in another package is a different
// identifier, and one package's variants are the same.
//
// What it does not count as a reference: the declaration, the doc comment, the
// receiver of a method declaration, and a function's use of itself.
//
// What it examines: package-level functions, variables, constants and types, and
// the exported methods of exported types, in hand-written files. It skips
// generated files (their generator decides what exists) and struct fields (a
// JSON tag, a database scan or a template reaches them by name). A method that
// implements an interface also counts as used, because the call goes through the
// interface's method: it is exempt when some interface in the build has a method
// of that name and the method's type implements the interface.
func TestEveryExportedIdentifierIsReferenced(t *testing.T) {
	t.Parallel()

	examined, referenced := loadExportReferences(t)

	var dead []string
	root := repoRoot(t)
	for _, o := range examined {
		if referenced[o.pos.String()] {
			continue
		}
		if _, listed := unreferencedExports[o.key]; listed {
			continue
		}
		rel, relErr := filepath.Rel(root, o.pos.Filename)
		if relErr != nil {
			rel = o.pos.Filename
		}
		dead = append(dead, fmt.Sprintf("%s:%d  %s %s", filepath.ToSlash(rel), o.pos.Line, o.kind, o.key))
	}
	slices.Sort(dead)

	if len(examined) < minExportedExamined {
		t.Errorf("examined %d exported identifiers, want at least %d: the analysis loaded too little",
			len(examined), minExportedExamined)
	}
	if len(dead) > 0 {
		t.Errorf("%d exported identifiers are referenced nowhere in the module, tests included. "+
			"The unused linter cannot see exported code, and nothing outside the module can "+
			"call it. Delete them; list one in unreferencedExports only with the reason it "+
			"must stay:\n%s", len(dead), strings.Join(dead, "\n"))
	}

	byKey := map[string]exportedObject{}
	for _, o := range examined {
		byKey[o.key] = o
	}
	for _, key := range slices.Sorted(maps.Keys(unreferencedExports)) {
		if strings.TrimSpace(unreferencedExports[key]) == "" {
			t.Errorf("unreferencedExports[%q] has no reason", key)
		}
		o, ok := byKey[key]
		switch {
		case !ok:
			t.Errorf("unreferencedExports[%q] names no exported identifier: delete the entry", key)
		case referenced[o.pos.String()]:
			t.Errorf("unreferencedExports[%q] is referenced now: delete the entry", key)
		}
	}
}

// typeCheck parses and type-checks every package of the graph, dependencies
// first, one at a time, and stores the result in the fields packages.Load
// fills when it is asked for syntax and types: Fset, Syntax, Types, TypesInfo
// and, for a type error, Errors. A package outside the module is checked
// without its function bodies and without a TypesInfo, because only the module's
// own references are counted; its declarations are all a dependent needs.
func typeCheck(pkgs []*packages.Package, inModule func(path string) bool) {
	fset := token.NewFileSet()
	parsed := map[string]*ast.File{} // the variants of one package share their files
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		p.Fset = fset
		if p.PkgPath == "unsafe" {
			p.Types = types.Unsafe
			return
		}
		for _, name := range p.CompiledGoFiles {
			f, ok := parsed[name]
			if !ok {
				var err error
				f, err = parser.ParseFile(fset, name, nil, parser.AllErrors|parser.ParseComments)
				if err != nil {
					p.Errors = append(p.Errors, packages.Error{Pos: name, Msg: err.Error(), Kind: packages.ParseError})
				}
				parsed[name] = f
			}
			if f != nil {
				p.Syntax = append(p.Syntax, f)
			}
		}
		conf := types.Config{
			Importer: importerFunc(func(path string) (*types.Package, error) {
				if path == "unsafe" {
					return types.Unsafe, nil
				}
				if imp := p.Imports[path]; imp != nil && imp.Types != nil {
					return imp.Types, nil
				}
				return nil, fmt.Errorf("no package %q loaded for %s", path, p.ID)
			}),
			IgnoreFuncBodies: !inModule(p.PkgPath),
			Sizes:            p.TypesSizes,
			Error: func(err error) {
				p.Errors = append(p.Errors, packages.Error{Pos: "-", Msg: err.Error(), Kind: packages.TypeError})
			},
		}
		if p.Module != nil && p.Module.GoVersion != "" {
			conf.GoVersion = "go" + p.Module.GoVersion
		}
		if inModule(p.PkgPath) {
			p.TypesInfo = &types.Info{
				Types: map[ast.Expr]types.TypeAndValue{},
				Defs:  map[*ast.Ident]types.Object{},
				Uses:  map[*ast.Ident]types.Object{},
			}
		}
		p.Types = types.NewPackage(p.PkgPath, p.Name)
		_ = types.NewChecker(&conf, fset, p.Types, p.TypesInfo).Files(p.Syntax) // reported through conf.Error
	})
}

// importerFunc is a types.Importer written as a function.
type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

// loadExportReferences type-checks the module and returns the exported
// identifiers it examines and the set of declaring positions that something
// refers to.
func loadExportReferences(t *testing.T) (examined []exportedObject, referenced map[string]bool) {
	t.Helper()

	root := repoRoot(t)
	module := modulePath(t, root)
	// The loader supplies the package graph and the file lists only. It could
	// also type-check, but it does so on one goroutine per package, and under
	// the race detector go/types reports a race when two of them reach the same
	// instantiated generic type of a shared dependency (Checker.isComplete reads
	// Named.fromRHS while Named.unpack writes it). typeCheck below does the
	// checking on this goroutine alone.
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedModule | packages.NeedTypesSizes,
		Dir:        root,
		Tests:      true,
		BuildFlags: []string{"-tags=integration"},
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatalf("load the module: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no package loaded")
	}
	inModule := func(path string) bool { return path == module || strings.HasPrefix(path, module+"/") }
	typeCheck(pkgs, inModule)
	var broken []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			broken = append(broken, e.Error())
		}
	})
	if len(broken) > 0 {
		t.Fatalf("the module does not type-check, so a reference could be missed:\n%s",
			strings.Join(broken[:min(len(broken), 10)], "\n"))
	}
	fset := pkgs[0].Fset
	origin := func(o types.Object) types.Object {
		switch o := o.(type) {
		case *types.Func:
			return o.Origin()
		case *types.Var:
			return o.Origin()
		}
		return o
	}
	posKey := func(o types.Object) string { return fset.Position(origin(o).Pos()).String() }

	referenced = map[string]bool{}
	for _, p := range pkgs {
		if p.TypesInfo == nil || !inModule(p.PkgPath) {
			continue
		}
		// A use inside a method's receiver, or a function's use of itself, is
		// not a caller.
		notACaller := map[*ast.Ident]bool{}
		for _, f := range p.Syntax {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if fd.Recv != nil {
					ast.Inspect(fd.Recv, func(n ast.Node) bool {
						if id, isIdent := n.(*ast.Ident); isIdent {
							notACaller[id] = true
						}
						return true
					})
				}
				fn, ok := p.TypesInfo.Defs[fd.Name].(*types.Func)
				if !ok || fd.Body == nil {
					continue
				}
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					if id, isIdent := n.(*ast.Ident); isIdent && p.TypesInfo.Uses[id] != nil &&
						origin(p.TypesInfo.Uses[id]) == types.Object(fn.Origin()) {
						notACaller[id] = true
					}
					return true
				})
			}
		}
		for id, obj := range p.TypesInfo.Uses {
			if obj.Pkg() != nil && inModule(obj.Pkg().Path()) && !notACaller[id] {
				referenced[posKey(obj)] = true
			}
		}
	}

	// Interfaces in the whole build, by method name, for the implements rule.
	byMethod := map[string][]*types.Interface{}
	addInterface := func(it *types.Interface) {
		for m := range it.Methods() {
			byMethod[m.Name()] = append(byMethod[m.Name()], it)
		}
	}
	addInterface(types.Universe.Lookup("error").Type().Underlying().(*types.Interface))
	seen := map[*types.Package]bool{}
	var collect func(p *types.Package)
	collect = func(p *types.Package) {
		if seen[p] {
			return
		}
		seen[p] = true
		for _, name := range p.Scope().Names() {
			if tn, ok := p.Scope().Lookup(name).(*types.TypeName); ok && !tn.IsAlias() {
				if it, ok := tn.Type().Underlying().(*types.Interface); ok {
					addInterface(it)
				}
			}
		}
		for _, imp := range p.Imports() {
			collect(imp)
		}
	}
	for _, p := range pkgs {
		if p.Types != nil {
			collect(p.Types)
		}
		if p.TypesInfo == nil || !inModule(p.PkgPath) {
			continue
		}
		for _, tv := range p.TypesInfo.Types {
			if it, ok := tv.Type.(*types.Interface); ok { // an interface written in place
				addInterface(it)
			}
		}
	}
	satisfies := func(recv *types.Named, m *types.Func) bool {
		candidates := byMethod[m.Name()]
		if recv.TypeParams().Len() > 0 { // cannot be tested uninstantiated
			return len(candidates) > 0
		}
		for _, it := range candidates {
			if types.Implements(recv, it) || types.Implements(types.NewPointer(recv), it) {
				return true
			}
		}
		return false
	}

	generated := map[string]bool{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, f := range p.Syntax {
			if ast.IsGenerated(f) {
				generated[fset.Position(f.Pos()).Filename] = true
			}
		}
	})
	add := func(p *packages.Package, o types.Object, name, kind string) {
		pos := fset.Position(o.Pos())
		if generated[pos.Filename] || strings.HasSuffix(pos.Filename, "_test.go") {
			return
		}
		examined = append(examined, exportedObject{key: p.PkgPath + "." + name, kind: kind, pos: pos})
	}
	for _, p := range pkgs {
		// The declaring variant is the plain one: no test files mixed in, and
		// neither a test binary nor an external test package.
		if !inModule(p.PkgPath) || p.Types == nil || strings.Contains(p.ID, "[") ||
			strings.HasSuffix(p.ID, ".test") || strings.HasSuffix(p.PkgPath, "_test") {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			o := scope.Lookup(name)
			if !o.Exported() {
				continue
			}
			switch o := o.(type) {
			case *types.Func:
				add(p, o, o.Name(), "func")
			case *types.Var:
				add(p, o, o.Name(), "var")
			case *types.Const:
				add(p, o, o.Name(), "const")
			case *types.TypeName:
				add(p, o, o.Name(), "type")
				named, ok := o.Type().(*types.Named)
				if !ok || o.IsAlias() {
					continue
				}
				for m := range named.Methods() {
					if m.Exported() && !satisfies(named, m) {
						add(p, m, o.Name()+"."+m.Name(), "method")
					}
				}
			}
		}
	}
	return examined, referenced
}
