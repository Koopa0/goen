package outbox_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
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

// Add only. A topic is never renamed or removed while a row may carry it; add a
// new topic and keep the old handler for at least outbox.Retain.
var frozenTopics = []string{
	"order.placed",
	"account.password_reset",
	"order.paid",
	"order.shipped",
	"order.terminal",
	"catalogue.restocked",
	"newsletter.confirm",
	"newsletter.welcome",
	"newsletter.issue",
	"account.email_verify",
	"staff.invitation",
	"staff.enrolment",
	"staff.factor_failure",
	"account.password_reset_request",
	"account.registration",
	"invoice.due",
	"invoice.void_due",
	"order.part_refunded",
}

// A queued row carries its topic name as text, so the names are stored data:
// renaming one strands every row already written under it.
func TestTopicNamesAreFrozen(t *testing.T) {
	t.Parallel()

	declared := topicVars(t)
	got := make([]string, 0, len(declared))
	for _, name := range declared {
		got = append(got, name)
	}
	sort.Strings(got)
	want := slices.Clone(frozenTopics)
	sort.Strings(want)

	for _, name := range want {
		if !slices.Contains(got, name) {
			t.Errorf("topic %q is no longer declared in outbox.go; rows queued under it would be stranded. "+
				"Add a new topic and keep the old handler instead of renaming or removing", name)
		}
	}
	for _, name := range got {
		if !slices.Contains(want, name) {
			t.Errorf("outbox.go declares topic %q that is not in the frozen list; "+
				"if it is new, add it to frozenTopics (add only)", name)
		}
	}
	if len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Errorf("two variables declare the same topic name: %v", got)
	}
}

var (
	sqlTopicEquals = regexp.MustCompile(`(?i)\btopic\s*=\s*'([^']+)'`)
	sqlTopicIn     = regexp.MustCompile(`(?i)\btopic\s+IN\s*\(([^)]*)\)`)
	sqlQuoted      = regexp.MustCompile(`'([^']+)'`)
)

// sqlFiles is every migration and every sqlc query file under internal/.
func sqlFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find migrations: paths=%d, error=%v", len(paths), err)
	}
	err = filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "query.sql" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	return paths
}

// SQL cannot import a Go constant, so a topic renamed in Go leaves its literal
// behind and the erase or cleanup that matches it silently matches nothing.
func TestSQLTopicLiteralsAreRegistered(t *testing.T) {
	t.Parallel()

	registered := map[string]bool{}
	for _, name := range topicVars(t) {
		registered[name] = true
	}

	type use struct{ path, topic string }
	var found []use
	for _, path := range sqlFiles(t) {
		src := readFile(t, path)
		for _, m := range sqlTopicEquals.FindAllStringSubmatch(src, -1) {
			found = append(found, use{path, m[1]})
		}
		for _, m := range sqlTopicIn.FindAllStringSubmatch(src, -1) {
			for _, q := range sqlQuoted.FindAllStringSubmatch(m[1], -1) {
				found = append(found, use{path, q[1]})
			}
		}
	}
	for _, name := range sqlProducerTopics(t) {
		found = append(found, use{"migrations (INSERT INTO outbox_messages)", name})
	}

	// Today: one producer and six literals in 001 (an IN list holds three), two
	// in account/query.sql. The floor stops a scan that stopped matching from
	// passing with nothing to check.
	const minimum = 9
	if len(found) < minimum {
		t.Fatalf("found %d topic literals in SQL, want at least %d: the scan stopped matching", len(found), minimum)
	}
	for _, u := range found {
		if !registered[u.topic] {
			t.Errorf("%s names topic %q, which outbox.go does not declare", u.path, u.topic)
		}
	}
}

// A payload is the stored JSON of its topic, so it is declared here, beside the
// queue that stores it, and not in a producer or in the letter composer: its
// tags are a contract with every row already queued, which this package's wire
// tests hold.
func TestEveryPayloadIsDeclaredInOutbox(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "outbox.go", nil, 0)
	if err != nil {
		t.Fatalf("parse outbox.go: %v", err)
	}
	topics := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		index, ok := call.Fun.(*ast.IndexExpr)
		if !ok {
			return true
		}
		if fn, ok := index.X.(*ast.Ident); !ok || fn.Name != "topic" {
			return true
		}
		topics++
		if _, local := index.Index.(*ast.Ident); !local {
			t.Errorf("topic %s carries %s, a type declared outside package outbox; declare the payload in messages.go",
				types.ExprString(call.Args[0]), types.ExprString(index.Index))
		}
		return true
	})
	if topics < len(frozenTopics) {
		t.Fatalf("found %d topic[T](name) declarations, want at least %d: the parser stopped matching", topics, len(frozenTopics))
	}
}
