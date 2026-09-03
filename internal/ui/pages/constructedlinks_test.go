package pages_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestEveryConstructedLinkResolvesToARoute covers the URLs a view model
// assembles in Go — `"/admin/returns/" + r.ID + "/decide"` — which
// [TestEveryHardCodedLinkResolvesToARoute] cannot see because it reads
// `href="/literal"` out of the .templ files.
//
// The dynamic parts are replaced by a single segment, which is what a
// {wildcard} in the route pattern matches, so the assertion is about the SHAPE
// of the path and not about any particular id.
func TestEveryConstructedLinkResolvesToARoute(t *testing.T) {
	root := repoRoot(t)
	server := filepath.Join(root, "cmd", "goen", "server.go")

	// Both methods: these are hrefs AND form actions, and a form posts.
	routes := append(routesFor(t, server, "GET"), routesFor(t, server, "POST")...)
	if len(routes) < 40 {
		t.Fatalf("only %d routes found; the parser is not reading server.go", len(routes))
	}

	built := constructedPaths(t, filepath.Join(root, "internal", "ui"))
	// The count is asserted so that a parser which silently stops finding
	// anything fails instead of passing over an empty corpus, which is how a
	// guard comes to be satisfied by nothing.
	if len(built) < 30 {
		t.Fatalf("only %d constructed paths found; the AST walk is not reading the view models", len(built))
	}

	paths := make([]string, 0, len(built))
	for p := range built {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		path := p
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		if path == "" || strings.Contains(path, "//") {
			continue
		}
		if !resolves(path, routes) {
			t.Errorf("%s (%s) is built by a view model and matches no route",
				p, built[p])
		}
	}
}

// constructedPaths finds every path a view model assembles by concatenation,
// mapped to where it is written. A non-literal operand becomes one path
// segment, because that is what it always is: an id, a slug or a number.
func constructedPaths(t *testing.T, uiDir string) map[string]string {
	t.Helper()

	found := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(uiDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "_templ.go") {
			return err
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			join, ok := n.(*ast.BinaryExpr)
			if !ok || join.Op != token.ADD {
				return true
			}
			built, ok := flattenPath(join)
			if !ok || !strings.HasPrefix(built, "/") {
				return true
			}
			found[built] = filepath.Base(path) + ":" +
				strconv.Itoa(fset.Position(join.Pos()).Line)
			// Do not descend. `"/a/" + id + "/b"` parses as `("/a/" + id) + "/b"`,
			// so walking into it would also record the prefix `/a/x` as a path of
			// its own — four routes that nothing links, reported as broken.
			return false
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk view models: %v", err)
	}
	return found
}

// flattenPath renders a concatenation with its variables standing in as one
// segment each. It reports false when nothing in the expression is a literal,
// so an ordinary string sum is not mistaken for a URL.
func flattenPath(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		left, leftOK := flattenPath(v.X)
		right, rightOK := flattenPath(v.Y)
		if !leftOK && !rightOK {
			return "", false
		}
		if !leftOK {
			left = "x"
		}
		if !rightOK {
			right = "x"
		}
		return left + right, true
	default:
		return "", false
	}
}
