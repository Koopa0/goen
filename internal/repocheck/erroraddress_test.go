package repocheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// personalNames are the identifiers production code gives a person's address,
// phone or recipient. The spellings are exact: SMTPSender.Addr and cfg.Addr are
// a server's host:port, not a person, and stay out of the set.
var personalNames = map[string]bool{
	"email": true, "Email": true,
	"address": true, "Address": true, "addr": true,
	"recipient": true, "Recipient": true,
	"To":    true,
	"phone": true, "Phone": true,
}

// TestErrorsDoNotFormatPersonalAddresses: handlers log the errors that stores
// return at ERROR, and a failing database is exactly when every request would
// write another customer's address into that log (#1927). Errors carry the user
// id, the order number or the step that failed; a person's address, when it
// must be shown at all, goes through email.Mask.
//
// The shape refused in production code is an fmt.Errorf whose arguments name
// one of personalNames. The check reads names and not types, so an address held
// in a variable called something else gets through; it is the cheap guard on the
// spelling this repository actually uses, not a taint analysis. email.Mask(x)
// is the sanctioned form and its argument is not read. There is no allow list
// because no production fmt.Errorf needs one; add an entry here, with the
// reason, before adding an exception in the code.
func TestErrorsDoNotFormatPersonalAddresses(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	fset := token.NewFileSet()
	var visited int
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		visited++
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		for _, o := range errorfPersonalNames(fset, f) {
			offenders = append(offenders, rel+":"+o)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
	if visited < 300 {
		t.Fatalf("walked %d non-test files, want at least 300: the walk lost its subject", visited)
	}
	for _, o := range offenders {
		t.Error(o)
	}
}

// TestErrorAddressGuardSeesTheShapeItRefuses keeps the guard from going quiet:
// it must flag the spellings the fixed code used, and pass the masked form and
// the identifiers that only look alike.
func TestErrorAddressGuardSeesTheShapeItRefuses(t *testing.T) {
	t.Parallel()

	const src = `package p

import (
	"fmt"

	"example.test/email"
)

func f(id, addr, name string, user struct{ Email string }, s struct{ Addr string }, m struct{ To string }) {
	_ = fmt.Errorf("a %s", addr)                // flagged: addr
	_ = fmt.Errorf("b %s", user.Email)          // flagged: Email
	_ = fmt.Errorf("c %q", m.To)                // flagged: To
	_ = fmt.Errorf("d %s %s", id, user.Email)   // flagged: Email beside an id
	_ = fmt.Errorf("e %s", email.Mask(addr))    // masked
	_ = fmt.Errorf("f %s", id)                  // an id
	_ = fmt.Errorf("g %q", s.Addr)              // a server, not a person
	_ = fmt.Errorf("h %s", name)                // a name is not in the set
	_ = fmt.Sprintf("i %s", addr)               // not an error
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}
	got := errorfPersonalNames(fset, f)
	want := []string{"10", "11", "12", "13"}
	if len(got) != len(want) {
		t.Fatalf("flagged %d calls, want %d: %v", len(got), len(want), got)
	}
	for i, line := range want {
		if !strings.HasPrefix(got[i], line+" ") {
			t.Errorf("finding %d is %q, want line %s", i, got[i], line)
		}
	}
}

// errorfPersonalNames lists each fmt.Errorf in f whose arguments name a
// personal identifier outside email.Mask, as "line message".
func errorfPersonalNames(fset *token.FileSet, f *ast.File) []string {
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isPackageCall(call, "fmt", "Errorf") {
			return true
		}
		for _, arg := range call.Args {
			if name := personalName(arg); name != "" {
				found = append(found, strconv.Itoa(fset.Position(call.Pos()).Line)+
					" fmt.Errorf formats "+name+": carry the user id or email.Mask, never the address")
				break
			}
		}
		return true
	})
	return found
}

// personalName returns the first personal identifier in e, or "". It does not
// read inside email.Mask. For a selector it reads the field and not a bare
// qualifier on the left, so the package in email.Valid(id) is not mistaken for
// a variable.
func personalName(e ast.Expr) string {
	var name string
	ast.Inspect(e, func(n ast.Node) bool {
		if name != "" {
			return false
		}
		switch n := n.(type) {
		case *ast.CallExpr:
			if isPackageCall(n, "email", "Mask") {
				return false
			}
		case *ast.SelectorExpr:
			if personalNames[n.Sel.Name] {
				name = n.Sel.Name
				return false
			}
			_, bare := n.X.(*ast.Ident)
			return !bare
		case *ast.Ident:
			if personalNames[n.Name] {
				name = n.Name
			}
		}
		return name == ""
	})
	return name
}

// isPackageCall reports whether call is pkg.name(...), by the identifier
// spelling of the package.
func isPackageCall(call *ast.CallExpr, pkg, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}
