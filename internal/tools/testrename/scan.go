package main

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/scanner"
	"go/token"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// decl is one test-like function (Test, Benchmark, Fuzz or Example) that
// `go test -list` would print, or a TestMain.
type decl struct {
	Dir, File, Name, Pkg string
	Main                 bool // TestMain
	Constraint           constraint.Expr
	// Doc is the offset of the doc comment (Start when there is none), Start
	// the offset of the func keyword, End the offset after the closing brace.
	Doc, Start, End int
	BodyStart       int // offset of the opening brace
}

// tagSets are the two build configurations the repository's tests live in.
var tagSets = [][]string{nil, {"integration"}}

func tagSetName(set []string) string {
	if len(set) == 0 {
		return "no tags"
	}
	return "-tags " + strings.Join(set, ",")
}

// active reports whether the declaration is compiled under the tag set.
func (d decl) active(set []string) bool {
	if d.Constraint == nil {
		return true
	}
	return d.Constraint.Eval(func(tag string) bool { return slices.Contains(set, tag) })
}

// testKinds maps a test-like prefix to the testing type its parameter has.
var testKinds = map[string]string{"Test": "T", "Benchmark": "B", "Fuzz": "F", "Example": ""}

// declsIn parses one _test.go file.
func declsIn(name string, src []byte) ([]decl, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	expr := buildConstraint(file)
	var out []decl
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil {
			continue
		}
		isMain := fn.Name.Name == "TestMain" && paramType(fn) == "M"
		if !isMain && !isTestFunc(fn) {
			continue
		}
		start := fset.Position(fn.Pos()).Offset
		doc := start
		if fn.Doc != nil {
			doc = fset.Position(fn.Doc.Pos()).Offset
		}
		out = append(out, decl{
			Dir: dirOf(name), File: name, Name: fn.Name.Name, Pkg: file.Name.Name, Main: isMain,
			Constraint: expr, Doc: doc, Start: start,
			End:       fset.Position(fn.End()).Offset,
			BodyStart: fset.Position(fn.Body.Lbrace).Offset,
		})
	}
	return out, nil
}

// buildConstraint returns the //go:build line before the package clause.
func buildConstraint(file *ast.File) constraint.Expr {
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		for _, c := range group.List {
			if constraint.IsGoBuild(c.Text) {
				if expr, err := constraint.Parse(c.Text); err == nil {
					return expr
				}
			}
		}
	}
	return nil
}

// paramType returns X for a sole parameter of type *testing.X, "" when there
// is none and "?" for anything else.
func paramType(fn *ast.FuncDecl) string {
	params := fn.Type.Params.List
	if len(params) == 0 {
		return ""
	}
	if len(params) != 1 || len(params[0].Names) > 1 {
		return "?"
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return "?"
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return "?"
	}
	return sel.Sel.Name
}

// isTestFunc follows cmd/go: the name starts with Test, Benchmark, Fuzz or
// Example followed by anything but a lower-case letter, and the signature
// matches.
func isTestFunc(fn *ast.FuncDecl) bool {
	if fn.Type.TypeParams != nil {
		return false
	}
	for prefix, param := range testKinds {
		rest, ok := strings.CutPrefix(fn.Name.Name, prefix)
		if !ok {
			continue
		}
		if r, _ := utf8.DecodeRuneInString(rest); rest != "" && unicode.IsLower(r) {
			continue
		}
		if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
			continue
		}
		if paramType(fn) == param {
			return true
		}
	}
	return false
}

// declared is every test declaration of a snapshot.
type declared struct {
	all []decl
}

// scanDecls lists the declarations of every _test.go file of s, skipping
// testdata directories as the go command does.
func scanDecls(s *snapshot) (*declared, error) {
	var d declared
	for _, name := range s.paths {
		if !strings.HasSuffix(name, "_test.go") || slices.Contains(strings.Split(name, "/"), "testdata") {
			continue
		}
		ds, err := declsIn(name, s.data[name])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		d.all = append(d.all, ds...)
	}
	return &d, nil
}

// key identifies a test within a build configuration: go test runs a
// directory as one unit, so the directory and the name are what must be unique.
type key struct{ Dir, Name string }

// list returns the declarations that `go test -list` prints for a tag set.
func (d *declared) list(set []string) map[key][]decl {
	out := map[key][]decl{}
	for _, x := range d.all {
		if !x.Main && x.active(set) {
			k := key{x.Dir, x.Name}
			out[k] = append(out[k], x)
		}
	}
	return out
}

// dirsByName maps each Test name to the directories that declare it under any
// tag set.
func (d *declared) dirsByName() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, x := range d.all {
		if x.Main || !strings.HasPrefix(x.Name, "Test") {
			continue
		}
		if out[x.Name] == nil {
			out[x.Name] = map[string]bool{}
		}
		out[x.Name][x.Dir] = true
	}
	return out
}

// tokens returns the Go tokens of src[from:to] as strings, with every
// identifier, string, character and comment passed through fix. Whitespace
// and layout do not appear, so a re-formatted body compares equal.
func tokens(src []byte, from, to int, fix func(string) string) []string {
	var s scanner.Scanner
	fset := token.NewFileSet()
	chunk := src[from:to]
	s.Init(fset.AddFile("", -1, len(chunk)), chunk, nil, scanner.ScanComments)
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		switch tok {
		case token.SEMICOLON:
			out = append(out, ";")
		case token.IDENT, token.STRING, token.CHAR, token.COMMENT:
			out = append(out, fix(lit))
		default:
			out = append(out, tok.String()+lit)
		}
	}
}
