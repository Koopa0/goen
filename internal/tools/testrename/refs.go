package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strings"
)

// nameRE finds a test name; a key finds the name with the file that declares it,
// the form of internal/repocheck/assetreaders_test.go's rows.
var (
	nameRE = regexp.MustCompile(`\bTest[A-Z0-9_][A-Za-z0-9_]*`)
	fileRE = regexp.MustCompile(`[A-Za-z0-9_./-]*_test\.go`)
	keyRE  = regexp.MustCompile(`([A-Za-z0-9_./-]+_test\.go)#(Test[A-Z0-9_][A-Za-z0-9_]*)`)
)

// Mention categories, by where the mention sits.
const (
	catOwnDoc     = "own doc comment"
	catGoComment  = "go comment"
	catTestString = "string in a _test.go"
	catSrcString  = "string in a non-test .go"
	catTestIdent  = "identifier in a _test.go"
	catWorkflow   = "workflow"
	catMakefile   = "makefile"
	catMarkdown   = "markdown"
	catSQL        = "sql"
	catOther      = "other file"
	whatTest      = "test"
	whatFile      = "file"
)

// mention is one place that names a test or a test file.
type mention struct {
	What   string // whatTest or whatFile
	Name   string
	File   string
	Line   int
	Cat    string
	Decl   string // the top-level declaration that holds a Go string
	IsKey  bool   // a "file#Test" key
	Exists bool   // the test is declared, or a test file of that name exists
}

// mentionScan collects the mentions of one snapshot.
type mentionScan struct {
	s         *snapshot
	names     map[string]bool           // declared Test names
	ownDocs   map[string]map[int]bool   // file -> lines of a test's own doc comment
	selfDecl  map[string]map[int]string // file -> line of a func keyword -> the name it declares
	baseNames map[string]bool           // base names of the _test.go files
	out       []mention
}

// mentions lists every mention of a declared-looking test name and of a test
// file path in s. Go files are tokenised with go/scanner, so a mention is known
// to sit in a comment, a string literal or code. TestMain is not a test.
func mentions(s *snapshot, d *declared) []mention {
	m := &mentionScan{
		s: s, names: map[string]bool{}, ownDocs: map[string]map[int]bool{},
		selfDecl: map[string]map[int]string{}, baseNames: map[string]bool{},
	}
	for _, x := range d.all {
		if !x.Main {
			m.noteDecl(x)
		}
	}
	for _, name := range s.paths {
		if strings.HasSuffix(name, "_test.go") {
			m.baseNames[path.Base(name)] = true
		}
	}
	for _, name := range s.paths {
		if strings.HasSuffix(name, ".go") {
			m.goFile(name, s.data[name])
			continue
		}
		m.text(name, string(s.data[name]), 1, otherCategory(name), "")
	}
	return m.out
}

func (m *mentionScan) noteDecl(x decl) {
	m.names[x.Name] = true
	src := m.s.data[x.File]
	if m.ownDocs[x.File] == nil {
		m.ownDocs[x.File] = map[int]bool{}
		m.selfDecl[x.File] = map[int]string{}
	}
	for l := lineOf(src, x.Doc); l <= lineOf(src, x.Start); l++ {
		m.ownDocs[x.File][l] = true
	}
	m.selfDecl[x.File][lineOf(src, x.Start)] = x.Name
}

func (m *mentionScan) emit(mt mention) {
	if mt.What == whatTest {
		mt.Exists = m.names[mt.Name]
	} else {
		mt.Exists = m.s.has(mt.Name) || m.baseNames[path.Base(mt.Name)]
	}
	m.out = append(m.out, mt)
}

// text scans the lines of body, which starts on line line, for mentions.
func (m *mentionScan) text(file, body string, line int, cat, decl string) {
	for i, l := range strings.Split(body, "\n") {
		var keys [][2]int
		for _, k := range keyRE.FindAllStringSubmatchIndex(l, -1) {
			keys = append(keys, [2]int{k[0], k[1]})
			m.emit(mention{What: whatTest, Name: l[k[4]:k[5]], File: file, Line: line + i, Cat: cat, Decl: decl, IsKey: true})
		}
		for _, at := range nameRE.FindAllStringIndex(l, -1) {
			name := l[at[0]:at[1]]
			if name == "TestMain" || insideAny(keys, at[0]) {
				continue
			}
			c := cat
			if c == catGoComment && m.ownDocs[file][line+i] {
				c = catOwnDoc
			}
			m.emit(mention{What: whatTest, Name: name, File: file, Line: line + i, Cat: c, Decl: decl})
		}
		for _, name := range fileRE.FindAllString(l, -1) {
			if name != "_test.go" && !strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "*") {
				m.emit(mention{What: whatFile, Name: name, File: file, Line: line + i, Cat: cat, Decl: decl})
			}
		}
	}
}

func otherCategory(name string) string {
	switch {
	case strings.HasPrefix(name, ".github/"):
		return catWorkflow
	case name == "Makefile":
		return catMakefile
	case strings.HasSuffix(name, ".md"):
		return catMarkdown
	case strings.HasSuffix(name, ".sql"):
		return catSQL
	}
	return catOther
}

// goFile tokenises one Go file.
func (m *mentionScan) goFile(name string, body []byte) {
	isTest := strings.HasSuffix(name, "_test.go")
	spans := declSpans(name, body)
	fset := token.NewFileSet()
	f := fset.AddFile(name, -1, len(body))
	var sc scanner.Scanner
	sc.Init(f, body, nil, scanner.ScanComments)
	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			return
		}
		line := f.Line(pos)
		switch tok {
		case token.COMMENT:
			m.text(name, lit, line, catGoComment, "")
		case token.STRING, token.CHAR:
			cat := catSrcString
			if isTest {
				cat = catTestString
			}
			m.text(name, lit, line, cat, spans.at(f.Offset(pos)))
		case token.IDENT:
			if m.names[lit] && m.selfDecl[name][line] != lit {
				m.emit(mention{What: whatTest, Name: lit, File: name, Line: line, Cat: catTestIdent})
			}
		default:
			// Operators, keywords and numbers name nothing.
		}
	}
}

// span is a top-level declaration's byte range and name.
type span struct {
	from, to int
	name     string
}

type spans []span

func (s spans) at(off int) string {
	for _, sp := range s {
		if off >= sp.from && off < sp.to {
			return sp.name
		}
	}
	return ""
}

// declSpans names the top-level declarations of a Go file: a func by its name,
// a var, const or type by its first name.
func declSpans(name string, body []byte) spans {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, body, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var out spans
	for _, d := range file.Decls {
		sp := span{from: fset.Position(d.Pos()).Offset, to: fset.Position(d.End()).Offset}
		switch d := d.(type) {
		case *ast.FuncDecl:
			sp.name = d.Name.Name
		case *ast.GenDecl:
			sp.name = firstName(d)
		}
		out = append(out, sp)
	}
	return out
}

func firstName(d *ast.GenDecl) string {
	for _, spec := range d.Specs {
		switch spec := spec.(type) {
		case *ast.ValueSpec:
			return spec.Names[0].Name
		case *ast.TypeSpec:
			return spec.Name.Name
		}
	}
	return ""
}

// lineOf returns the 1-based line of byte offset off.
func lineOf(src []byte, off int) int {
	return 1 + bytes.Count(src[:min(off, len(src))], []byte{'\n'})
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func insideAny(ranges [][2]int, off int) bool {
	return slices.ContainsFunc(ranges, func(r [2]int) bool { return off >= r[0] && off < r[1] })
}
