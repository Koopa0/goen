package audit_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
)

// TestEveryAuditActionHasALabel reads the vocabulary out of the const block
// that declares it, so an action added next week is covered the day it is
// written.
//
// Entry.Label falls back to the raw action when the map has no entry, and
// the fallback is right — audit_events is append-only, so a row naming a
// retired action must still render rather than crash the page. The cost is that
// a MISSING label looks like working software: "invoice.issue" appears where a
// phrase belongs, on a page only staff read. Seventeen actions were in that
// state.
//
// The corpus is both writers. Most actions are Go constants in this package;
// a few are string literals inside a schema function and have no Go constant at
// all, because declaring one so a parser could see it would be a dead
// identifier. Reading the migration is what covers those without inventing one.
func TestEveryAuditActionHasALabel(t *testing.T) {
	t.Parallel()

	literals, _ := schemaActions(t)
	actions := append(declaredActions(t), literals...)
	// Well under the 63 declared, so a parser that silently stops matching
	// fails here rather than passing over an empty corpus.
	if len(actions) < 50 {
		t.Fatalf("found %d actions in action.go; the parser is not reading the const block", len(actions))
	}

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	var missing []string
	for _, action := range actions {
		entry := audit.Entry{Action: action}
		if entry.Label(ctx) == action {
			missing = append(missing, action)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d audit action(s) render as their own identifier on /admin/audit:\n  %s\n\n"+
			"Add a key to internal/i18n/audit.go and an entry to pages.actionLabels.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// schemaWrittenAction matches an action a schema function inserts directly,
// e.g. `'invoice.allowance_provider_invalid', 'invoice_documents',`.
var schemaWrittenAction = regexp.MustCompile(`'([a-z_]+\.[a-z0-9_.]+)',\s*'[a-z_]+',`)

// schemaAssignedAction matches an action a schema function keeps in a variable
// before inserting it, e.g. `v_action := 'refund.provider_rejected';`.
var schemaAssignedAction = regexp.MustCompile(`v_action\s*:=\s*'([a-z_]+\.[a-z0-9_.]+)'\s*;`)

// schemaConcatenatedAction matches the head of an action the schema builds from
// a value, e.g. `'refund.provider_' || p_status`. Its tail is not a literal, so
// only the prefix can be read.
var schemaConcatenatedAction = regexp.MustCompile(`'([a-z_]+\.[a-z0-9_]*)'\s*\|\|`)

// schemaActions is every action written by a stored function rather than by Go:
// the literal ones, and the prefixes of the ones built from a value.
func schemaActions(t *testing.T) (literals, prefixes []string) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	// Only the prefixes audit_events actually uses: the pattern is loose enough
	// to match an unrelated pair of quoted literals otherwise.
	domains := map[string]bool{
		"invoice": true, "order": true, "payment": true, "return": true,
		"credit": true, "stock": true, "shipping": true, "tier": true,
		"review": true, "customer": true, "newsletter": true, "refund": true,
	}
	for _, pattern := range []*regexp.Regexp{schemaWrittenAction, schemaAssignedAction} {
		for _, m := range pattern.FindAllStringSubmatch(string(body), -1) {
			if domains[strings.SplitN(m[1], ".", 2)[0]] {
				literals = append(literals, m[1])
			}
		}
	}
	for _, m := range schemaConcatenatedAction.FindAllStringSubmatch(string(body), -1) {
		if domains[strings.SplitN(m[1], ".", 2)[0]] {
			prefixes = append(prefixes, m[1])
		}
	}
	if len(literals) == 0 {
		t.Fatal("found no schema-written actions; the pattern has stopped matching")
	}
	return literals, prefixes
}

// declaredActions is every string constant of type Action in action.go.
func declaredActions(t *testing.T) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "action.go", nil, 0)
	if err != nil {
		t.Fatalf("parse action.go: %v", err)
	}

	var out []string
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if name, ok := value.Type.(*ast.Ident); !ok || name.Name != "Action" {
				continue
			}
			for _, v := range value.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				action, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", lit.Value, err)
				}
				out = append(out, action)
			}
		}
	}
	return out
}

// TestEveryAuditedTableHasAnEntityLabel reads the tables the application writes
// into the trail, so one added next week is covered the day it is written.
// Without a label the subject line shows the table name.
func TestEveryAuditedTableHasAnEntityLabel(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(`(?:Table|EntityTable):\s*"([a-z_]+)"`)
	tables := map[string]bool{"refunds": true} // written by a schema function
	err := filepath.WalkDir(filepath.Join("..", "..", "..", "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") || strings.Contains(path, string(filepath.Separator)+"db"+string(filepath.Separator)) {
			return err
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: this repository's own files
		if readErr != nil {
			return readErr
		}
		for _, m := range pattern.FindAllStringSubmatch(string(src), -1) {
			tables[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) < 20 {
		t.Fatalf("found %d audited tables; the scan stopped matching", len(tables))
	}

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	var missing []string
	for table := range tables {
		if (audit.Entry{Entity: table}).EntityLabel(ctx) == table {
			missing = append(missing, table)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("table(s) shown by their database name on /admin/audit:\n  %s\nAdd a key to internal/i18n/audit_entity.go and an entry to pages.entityLabels.",
			strings.Join(missing, "\n  "))
	}
}
