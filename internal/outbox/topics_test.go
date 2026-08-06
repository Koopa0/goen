package outbox_test

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

// TestEveryTopicHasAProducerAndAHandler holds each declared topic to both ends
// of the wire it is supposed to be.
//
// Three topics shipped as constants with neither. order.paid and order.shipped
// were declared when the outbox was built and nothing ever wrote or read them,
// so a customer was told their order was placed and then never heard again —
// not when the money arrived, not when the parcel left. The constants read as
// working features.
//
// A topic with no handler is worse than absent: Store.reschedule sends it round
// again with "no handler registered", so it retries until Stuck() surfaces it.
//
// Derived from the SOURCE rather than from a list, for the reason the schema
// suite derives its coverage from the catalog: a hand-written list is a list
// somebody adds a constant without touching.
func TestEveryTopicHasAProducerAndAHandler(t *testing.T) {
	t.Parallel()

	declared := topicConstants(t)
	if len(declared) < 3 {
		t.Fatalf("found %d topic constants, want at least 3 — the parser stopped matching", len(declared))
	}

	main := readFile(t, filepath.Join("..", "..", "cmd", "goen", "main.go"))
	producers := producerSources(t)

	for name := range declared {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(main, "outbox."+name) {
				t.Errorf("%s has no handler in cmd/goen/main.go — every message on it "+
					"will retry until it is stuck", name)
			}
			if !slices.ContainsFunc(producers, func(src string) bool {
				return strings.Contains(src, "outbox."+name)
			}) {
				t.Errorf("%s has no producer in internal/ — nothing ever enqueues it", name)
			}
		})
	}
}

// topicConstants reads the Topic* names out of this package's own const block.
func topicConstants(t *testing.T) map[string]struct{} {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "outbox.go", nil, 0)
	if err != nil {
		t.Fatalf("parse outbox.go: %v", err)
	}

	out := map[string]struct{}{}
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		for _, spec := range decl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, id := range vs.Names {
				if strings.HasPrefix(id.Name, "Topic") {
					out[id.Name] = struct{}{}
				}
			}
		}
		return true
	})
	return out
}

// producerSources is every non-test Go file under internal/ except this
// package's own — the outbox declaring its topics is not a producer of them.
func producerSources(t *testing.T) []string {
	t.Helper()
	var out []string
	const root = ".." // internal/
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") ||
			strings.Contains(path, filepath.Join("internal", "outbox")) ||
			strings.Contains(path, string(filepath.Separator)+"outbox"+string(filepath.Separator)) {
			return nil
		}
		b, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		out = append(out, string(b))
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: the path is a constant in this test
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
