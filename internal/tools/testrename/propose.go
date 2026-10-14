package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// maxName is the longest a proposed name may be after "Test": the 99th
// percentile of the standard library's test names, which is what the guard
// will allow.
const maxName = 40

// maxScenario is how many words of the old name a proposal keeps as the
// scenario.
const maxScenario = 3

// ignoredCallees are called by nearly every test without being the test's
// subject.
var ignoredCallees = []string{"New", "Routes", "Handler"}

// stopWords are dropped from the ends of a scenario.
var stopWords = map[string]bool{
	"The": true, "A": true, "An": true, "Is": true, "Are": true, "Does": true, "Do": true, "Can": true,
	"Must": true, "Should": true, "Will": true, "And": true, "Or": true, "Of": true, "To": true,
	"In": true, "On": true, "For": true, "With": true, "By": true, "That": true, "It": true,
}

// unitsOf lists the units a directory's non-test files declare: functions and
// types by name, methods as Type_Method. The key is the form a test name uses:
// the identifiers joined, so Store.Decide is found in a name that starts with StoreDecide.
func unitsOf(s *snapshot, dir string) map[string]string {
	units := map[string]string{}
	for _, name := range s.paths {
		if dirOf(name) != dir || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, s.data[name], parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range file.Decls {
			collectUnits(d, units)
		}
	}
	return units
}

func collectUnits(d ast.Decl, units map[string]string) {
	switch d := d.(type) {
	case *ast.FuncDecl:
		if !d.Name.IsExported() {
			return
		}
		if d.Recv == nil {
			units[d.Name.Name] = d.Name.Name
			return
		}
		if recv := receiverName(d.Recv); ast.IsExported(recv) {
			units[recv+d.Name.Name] = recv + "_" + d.Name.Name
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
				units[ts.Name.Name] = ts.Name.Name
			}
		}
	}
}

func receiverName(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if idx, ok := t.(*ast.IndexExpr); ok {
		t = idx.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// words splits a MixedCaps identifier.
func words(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	for i := 1; i < len(rs); i++ {
		lowerBefore := unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])
		upperRun := unicode.IsUpper(rs[i-1]) && unicode.IsUpper(rs[i]) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
		if (unicode.IsUpper(rs[i]) && lowerBefore) || upperRun {
			out = append(out, string(rs[start:i]))
			start = i
		}
	}
	return append(out, string(rs[start:]))
}

// byPrefix finds the longest unit that the test name starts with, at a word
// boundary, and returns its label and the rest of the name.
func byPrefix(rest string, units map[string]string) (label, tail string, ok bool) {
	best := ""
	for u := range units {
		if !strings.HasPrefix(rest, u) || len(u) <= len(best) {
			continue
		}
		after := rest[len(u):]
		if after == "" || unicode.IsUpper(rune(after[0])) || unicode.IsDigit(rune(after[0])) || after[0] == '_' {
			best = u
		}
	}
	if best == "" {
		return "", "", false
	}
	return units[best], strings.TrimPrefix(rest[len(best):], "_"), true
}

// scenario keeps the first maxScenario words of tail that are not connecting
// words: "CountsToTheLastDay" gives "CountsLastDay".
func scenario(tail string) string {
	var keep []string
	for _, w := range words(tail) {
		if !stopWords[w] && len(keep) < maxScenario {
			keep = append(keep, w)
		}
	}
	return strings.Join(keep, "")
}

// callees finds, for each test of the directory, the units from the package
// under test that its body calls, with go/types.
func callees(root, dir string, names map[string]bool) (map[string][]string, error) {
	cfg := &packages.Config{
		Mode:       packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir:        root,
		Tests:      true,
		BuildFlags: []string{"-tags=integration"},
	}
	pkgs, err := packages.Load(cfg, "./"+dir)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		under := strings.TrimSuffix(pkg.PkgPath, "_test")
		for _, f := range pkg.Syntax {
			for _, d := range f.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && names[fn.Name.Name] && fn.Body != nil {
					out[fn.Name.Name] = calledUnits(fn.Body, pkg.TypesInfo, under)
				}
			}
		}
	}
	return out, nil
}

// calledUnits returns the distinct exported functions and methods of package
// path under that body calls.
func calledUnits(body *ast.BlockStmt, info *types.Info, under string) []string {
	seen := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if label := unitOf(call.Fun, info, under); label != "" {
			seen[label] = true
		}
		return true
	})
	return sortedKeys(seen)
}

func unitOf(fun ast.Expr, info *types.Info, under string) string {
	var id *ast.Ident
	switch f := fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return ""
	}
	obj, ok := info.Uses[id].(*types.Func)
	if !ok || obj.Pkg() == nil || obj.Pkg().Path() != under || !obj.Exported() {
		return ""
	}
	for _, ignored := range ignoredCallees {
		if strings.HasPrefix(obj.Name(), ignored) {
			return ""
		}
	}
	if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil {
		t := sig.Recv().Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if named, ok := t.(*types.Named); ok {
			return named.Obj().Name() + "_" + obj.Name()
		}
	}
	return obj.Name()
}

// proposal builds the map rows of one directory.
func propose(root string, s *snapshot, dir string) ([]row, error) {
	d, err := scanDecls(s)
	if err != nil {
		return nil, err
	}
	var tests []decl
	names := map[string]bool{}
	for _, x := range d.all {
		if x.Dir == dir && !x.Main && strings.HasPrefix(x.Name, "Test") {
			tests = append(tests, x)
			names[x.Name] = true
		}
	}
	if len(tests) == 0 {
		return nil, fmt.Errorf("no tests in %s", dir)
	}
	called, err := callees(root, dir, names)
	if err != nil {
		return nil, fmt.Errorf("type-check %s: %w", dir, err)
	}
	units := unitsOf(s, dir)
	var rows []row
	for _, t := range tests {
		rows = append(rows, proposeOne(t, units, called[t.Name]))
	}
	return dedupe(rows), nil
}

// proposeOne chooses the unit by the name's prefix, then by the single unit the
// body calls, then gives up and leaves the row for a person.
func proposeOne(t decl, units map[string]string, called []string) row {
	rest := strings.TrimPrefix(t.Name, "Test")
	rw := row{File: t.File, Old: t.Name, New: t.Name, Source: "hand", Action: "todo"}
	if label, tail, ok := byPrefix(rest, units); ok {
		rw.Unit, rw.Source = label, "prefix"
		rw.New, rw.Action = fit("Test"+label, scenario(tail)), "rename"
		return rw
	}
	if len(called) == 1 {
		rw.Unit, rw.Source = called[0], "callee"
		rw.New, rw.Action = fit("Test"+called[0], scenario(rest)), "rename"
	}
	return rw
}

// fit joins unit and scenario, dropping scenario words from the end until the
// name is at most maxName characters after Test.
func fit(unit, scen string) string {
	ws := words(scen)
	for scen != "" && len(unit+scen)-len("Test") > maxName {
		ws = ws[:len(ws)-1]
		scen = strings.Join(ws, "")
	}
	return unit + scen
}

// dedupe gives two tests that would share a name back to a person: the later
// one keeps its old name and is marked todo.
func dedupe(rows []row) []row {
	taken := map[string]bool{}
	for i := range rows {
		rw := &rows[i]
		if rw.Action == "todo" {
			taken[rw.Old] = true
			continue
		}
		if taken[rw.New] && rw.New != rw.Old {
			rw.New, rw.Action = rw.Old, "todo"
			taken[rw.Old] = true
			continue
		}
		taken[rw.New] = true
	}
	// A proposed name must not equal a name another test keeps.
	kept := map[string]bool{}
	for _, rw := range rows {
		if rw.Action == "todo" {
			kept[rw.Old] = true
		}
	}
	for i := range rows {
		if rows[i].Action == "rename" && rows[i].New != rows[i].Old && kept[rows[i].New] {
			rows[i].New, rows[i].Action = rows[i].Old, "todo"
		}
	}
	slices.SortStableFunc(rows, func(a, b row) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		return strings.Compare(a.Old, b.Old)
	})
	return rows
}

// mapsDir is where the maps live: inside the tool's own directory, which git
// tracks (it ignores most of docs/) and no subcommand rewrites.
const mapsDir = ownDir + "maps"

// mapPath is where propose writes the map of directory dir.
func mapPath(dir string) string { return path.Join(mapsDir, dir+".tsv") }
