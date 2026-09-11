package admin_test

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

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// TestEveryAuditActionHasALabel reads the vocabulary out of the const block
// that declares it, so an action added next week is covered the day it is
// written.
//
// AuditEntry.Label falls back to the raw action when the map has no entry, and
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

	actions := append(declaredActions(t), schemaActions(t)...)
	// Well under the 63 declared, so a parser that silently stops matching
	// fails here rather than passing over an empty corpus.
	if len(actions) < 50 {
		t.Fatalf("found %d actions in audit.go; the parser is not reading the const block", len(actions))
	}

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	var missing []string
	for _, action := range actions {
		entry := pages.AuditEntry{Action: action}
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

// TestNewsletterComposeRendersAPhrase locks the compose action that lives in
// newsletter rather than admin's Action const block, so a missing label cannot
// hide behind the raw identifier on /admin/audit.
func TestNewsletterComposeRendersAPhrase(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	entry := pages.AuditEntry{Action: "newsletter.compose"}
	if got := entry.Label(ctx); got == "newsletter.compose" {
		t.Errorf("Label() = %q; the compose action has no staff-facing phrase", got)
	}
}

// schemaWrittenAction matches an action a schema function inserts directly,
// e.g. `'invoice.allowance_provider_invalid', 'invoice_documents',`.
var schemaWrittenAction = regexp.MustCompile(`'([a-z_]+\.[a-z0-9_.]+)',\s*'[a-z_]+',`)

// schemaActions is every action written by a stored function rather than by Go.
func schemaActions(t *testing.T) []string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_initial_schema.up.sql"))
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}
	// Only the prefixes audit_events actually uses: the pattern is loose enough
	// to match an unrelated pair of quoted literals otherwise.
	domains := map[string]bool{
		"invoice": true, "order": true, "payment": true, "return": true,
		"credit": true, "stock": true, "shipping": true, "tier": true,
		"review": true, "customer": true, "newsletter": true,
	}
	var out []string
	for _, m := range schemaWrittenAction.FindAllStringSubmatch(string(body), -1) {
		if domains[strings.SplitN(m[1], ".", 2)[0]] {
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Fatal("found no schema-written actions; the pattern has stopped matching")
	}
	return out
}

// declaredActions is every string constant of type Action in audit.go.
func declaredActions(t *testing.T) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "audit.go", nil, 0)
	if err != nil {
		t.Fatalf("parse audit.go: %v", err)
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
