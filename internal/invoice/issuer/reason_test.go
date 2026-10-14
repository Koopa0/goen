package issuer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestOperationReasonsAreNeverBareStrings keeps every reason the state machine
// stores a constant of invoice.Reason. The parameter's type alone does not: an
// untyped string literal converts to it silently, and then the health page has
// no label for it and no test lists it. So the call sites are read as syntax.
func TestOperationReasonsAreNeverBareStrings(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	calls := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || len(call.Args) != 5 {
				return true
			}
			switch sel.Sel.Name {
			case "retry", "alarm", "reject":
			default:
				return true
			}
			calls++
			ast.Inspect(call.Args[3], func(arg ast.Node) bool {
				if lit, bare := arg.(*ast.BasicLit); bare {
					t.Errorf("%s: %s stores text built from the string %s; use an invoice.Reason constant",
						fset.Position(lit.Pos()), sel.Sel.Name, lit.Value)
				}
				return true
			})
			return true
		})
	}
	if calls == 0 {
		t.Fatal("no retry, alarm or reject call found; the test no longer reads the state machine")
	}
}
