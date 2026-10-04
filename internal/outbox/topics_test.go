package outbox_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestEveryTopicHasAProducerAndAHandler holds each topic to both ends of the
// wire. A topic with no handler is worse than absent: it retries until Stuck()
// surfaces it.
func TestEveryTopicHasAProducerAndAHandler(t *testing.T) {
	t.Parallel()

	declared := topicVars(t)
	if len(declared) < 3 {
		t.Fatalf("found %d topic variables, want at least 3 — the parser stopped matching", len(declared))
	}

	main := readFile(t, filepath.Join("..", "..", "cmd", "goen", "main.go"))
	producers := producerSources(t)
	sqlProducers := sqlProducerTopics(t)

	for name, topic := range declared {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(main, "outbox."+name) {
				t.Errorf("%s has no handler in cmd/goen/main.go — every message on it "+
					"will retry until it is stuck", name)
			}
			if !slices.ContainsFunc(producers, func(src string) bool {
				return strings.Contains(src, "outbox."+name)
			}) && !slices.Contains(sqlProducers, topic) {
				t.Errorf("%s has no producer in internal/ or migrations/ — nothing ever enqueues it", name)
			}
		})
	}
}

func topicVars(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "outbox.go", nil, 0)
	if err != nil {
		t.Fatalf("parse outbox.go: %v", err)
	}

	out := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR {
			return true
		}
		for _, spec := range decl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, id := range vs.Names {
				if !strings.HasPrefix(id.Name, "Topic") {
					continue
				}
				if i >= len(vs.Values) {
					t.Fatalf("%s must declare its topic name", id.Name)
				}
				call, ok := vs.Values[i].(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					t.Fatalf("%s must declare its topic name", id.Name)
				}
				literal, ok := call.Args[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatalf("%s topic name must be a string literal", id.Name)
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("%s topic name: %v", id.Name, err)
				}
				out[id.Name] = value
			}
		}
		return true
	})
	return out
}

// Database triggers enqueue in the stock writer's transaction without a Go producer.
func sqlProducerTopics(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find migrations: paths=%d, error=%v", len(paths), err)
	}
	insert := regexp.MustCompile(`(?is)\bINSERT\s+INTO\s+(?:public\.)?outbox_messages\s*\(\s*topic\s*,[^)]*\)\s*(?:SELECT|VALUES\s*\()\s*'([^']+)'`)
	var topics []string
	for _, path := range paths {
		for _, match := range insert.FindAllStringSubmatch(readFile(t, path), -1) {
			topics = append(topics, match[1])
		}
	}
	return topics
}

// producerSources is every non-test Go file under internal/ except this
// package's own: declaring a topic is not producing it.
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
