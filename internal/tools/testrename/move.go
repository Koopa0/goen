package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/tools/imports"
)

// importsMu guards imports.LocalPrefix, which is a package variable.
var importsMu sync.Mutex

// move is one test function that changes file.
type move struct{ from, to, name string }

// moveDecls moves each function, with its doc comment, from one file to
// another within files. A destination that does not exist is created with the
// source's package clause, build constraint and imports. Imports are then
// added, removed and grouped by goimports with the module's own packages in
// the last group. Both files must be in one package with one build constraint.
func moveDecls(files map[string][]byte, moves []move, modulePath string) error {
	touched := map[string]bool{}
	for _, m := range moves {
		if err := moveOne(files, m); err != nil {
			return fmt.Errorf("move %s from %s to %s: %w", m.name, m.from, m.to, err)
		}
		touched[m.from], touched[m.to] = true, true
	}
	for _, name := range sortedKeys(touched) {
		out, err := processImports(name, files[name], modulePath)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		files[name] = out
	}
	return nil
}

func processImports(name string, src []byte, modulePath string) ([]byte, error) {
	importsMu.Lock()
	defer importsMu.Unlock()
	imports.LocalPrefix = modulePath
	return imports.Process(name, src, &imports.Options{Comments: true, TabIndent: true, TabWidth: 8})
}

func moveOne(files map[string][]byte, m move) error {
	src, ok := files[m.from]
	if !ok {
		return fmt.Errorf("%s does not exist", m.from)
	}
	decls, err := declsIn(m.from, src)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(decls, func(d decl) bool { return d.Name == m.name })
	if i < 0 {
		return fmt.Errorf("%s declares no %s", m.from, m.name)
	}
	dst, err := destination(files, m.to, src)
	if err != nil {
		return err
	}
	text, rest := cutDecl(src, decls[i])
	dst = append(bytes.TrimRight(dst, "\n"), '\n', '\n')
	files[m.to] = append(dst, append(text, '\n')...)
	files[m.from] = rest
	return nil
}

// cutDecl returns the declaration with its doc comment, and src without them.
func cutDecl(src []byte, d decl) (decl, rest []byte) {
	text := slices.Clone(src[d.Doc:d.End])
	start, end := d.Doc, d.End
	if end < len(src) && src[end] == '\n' {
		end++
	}
	// A blank line on both sides becomes one.
	if start >= 2 && src[start-1] == '\n' && src[start-2] == '\n' && end < len(src) && src[end] == '\n' {
		end++
	}
	return text, append(slices.Clone(src[:start]), src[end:]...)
}

// destination returns the content of the file the function moves to: the file
// as it is, with the imports of the source added, or a new file beside the
// source.
func destination(files map[string][]byte, to string, src []byte) ([]byte, error) {
	dst, exists := files[to]
	if !exists {
		return newFileFor(src)
	}
	if err := sameBuild(src, dst, to); err != nil {
		return nil, err
	}
	return withImportsOf(dst, src)
}

// newFileFor returns the header of a new file beside src: the text up to and
// including the package clause (which carries the build constraint) and src's
// import declarations.
func newFileFor(src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(src[:fset.Position(file.Name.End()).Offset])
	out = append(out, '\n', '\n')
	for _, d := range file.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			out = append(out, src[fset.Position(g.Pos()).Offset:fset.Position(g.End()).Offset]...)
			out = append(out, '\n')
		}
	}
	return out, nil
}

// sameBuild refuses a move between files that differ in package or build constraint.
func sameBuild(from, to []byte, name string) error {
	a, err := headerOf(from)
	if err != nil {
		return err
	}
	b, err := headerOf(to)
	if err != nil {
		return err
	}
	if a != b {
		return fmt.Errorf("%s has header %q, the source has %q: a move may not change the package or the build tags", name, b, a)
	}
	return nil
}

// headerOf is the build constraint and package name of a file.
func headerOf(src []byte) (string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.ParseComments|parser.SkipObjectResolution|parser.PackageClauseOnly)
	if err != nil {
		return "", err
	}
	tag := ""
	if expr := buildConstraint(file); expr != nil {
		tag = expr.String()
	}
	return tag + " package " + file.Name.Name, nil
}

// withImportsOf adds to dst an import declaration for every import of src that
// dst lacks; goimports drops the ones the moved function does not use.
func withImportsOf(dst, src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	have, err := parser.ParseFile(fset, "", dst, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	want, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution|parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, spec := range have.Imports {
		present[importKey(spec)] = true
	}
	var add []string
	for _, spec := range want.Imports {
		if !present[importKey(spec)] {
			add = append(add, "\t"+importLine(spec))
		}
	}
	if len(add) == 0 {
		return dst, nil
	}
	at := fset.Position(have.Name.End()).Offset
	for _, d := range have.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			at = fset.Position(g.End()).Offset
		}
	}
	block := "\n\nimport (\n" + strings.Join(add, "\n") + "\n)"
	out := append(slices.Clone(dst[:at]), block...)
	return append(out, dst[at:]...), nil
}

func importKey(spec *ast.ImportSpec) string {
	p, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		p = spec.Path.Value
	}
	if spec.Name != nil {
		return spec.Name.Name + " " + p
	}
	return p
}

func importLine(spec *ast.ImportSpec) string {
	if spec.Name != nil {
		return spec.Name.Name + " " + spec.Path.Value
	}
	return spec.Path.Value
}

// formatStable re-formats edited, but only a file that gofmt leaves alone to
// begin with, so a rename never reformats code it did not touch.
func formatStable(orig, edited []byte) []byte {
	if out, err := format.Source(orig); err != nil || !bytes.Equal(out, orig) {
		return edited
	}
	if out, err := format.Source(edited); err == nil {
		return out
	}
	return edited
}
