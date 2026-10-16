package repocheck_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// importGraph maps an importing package to the goen packages it imports, and each
// edge to the production files that create it. Every path is relative to the
// module ("internal/ui/pages", "cmd/goen", "assets").
type importGraph struct {
	edges   map[string]map[string][]string
	scanned map[string]bool // every directory that held a production .go file
}

// importRule is one row of the policy table. forbids reports whether the edge
// from -> to breaks the rule. A hard rule admits no exception; any other rule's
// current violations are listed in exceptions.
type importRule struct {
	name   string
	hard   bool
	forbid func(from, to string) bool
	reason string
}

// exceptions are the edges the rules forbid and the plan has not removed yet:
// key "from -> to", value the task that removes the edge. An entry may only
// disappear; one whose edge is gone fails, so each task proves its own work.
var exceptions = map[string]string{
	"internal/ui/pages -> internal/loyalty":               "S11",
	"internal/admin/admintest -> internal/admin/orders":   "T48",
	"internal/admin/admintest -> internal/admin/products": "T48",
	"internal/admin/admintest -> internal/admin/refunds":  "T48",
	"internal/admin/admintest -> internal/admin/returns":  "T48",
}

// under reports whether pkg is dir or lies below it.
func under(pkg, dir string) bool {
	return pkg == dir || strings.HasPrefix(pkg, dir+"/")
}

// deskRoot returns "internal/admin/<x>" for a desk package and "" for anything
// else. internal/admin/access, audit and admintest are not desks: access and audit
// are the foundation every desk may import, admintest is the shared test fixture.
func deskRoot(pkg string) string {
	rest, ok := strings.CutPrefix(pkg, "internal/admin/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	switch name {
	case "", "access", "audit", "admintest":
		return ""
	}
	return "internal/admin/" + name
}

func isDesk(pkg string) bool { return deskRoot(pkg) != "" }

var (
	// uiMayImport is what the view layer may import besides itself.
	uiMayImport = []string{
		"assets", "internal/fieldrule", "internal/i18n", "internal/money", "internal/shoptime",
		"internal/coupon", "internal/inventory", "internal/invoice", "internal/keyset", "internal/order",
		"internal/postcode", "internal/product/labelling", "internal/refund",
		"internal/returns", "internal/shipping", "internal/user",
	}
	// storefrontPages are the packages that serve storefront pages. The rule
	// that uses them leaves out, on purpose: internal/newsletter (its issue
	// half moves to admin/content in T41), internal/contact (admin/feedback
	// uses its Subject vocabulary), internal/loyalty (programme rules
	// admin/loyalty configures), and internal/media and internal/payment
	// (shared infrastructure). It matches exactly, not by prefix:
	// internal/product/labelling is a vocabulary leaf that admin/products imports.
	storefrontPages = []string{
		"internal/account", "internal/cart", "internal/catalog", "internal/home",
		"internal/product", "internal/site",
	}
	// importFreeLeaves import no goen package at all.
	importFreeLeaves = []string{
		"internal/refund",
		"internal/coupon", "internal/inventory", "internal/postcode", "internal/user",
		"internal/money", "internal/db", "internal/keyset", "internal/bearer", "internal/sales",
	}
	// i18nOnlyLeaves import nothing from goen except the message catalogue.
	i18nOnlyLeaves = []string{
		"internal/invoice", "internal/order", "internal/product/labelling", "internal/returns", "internal/shoptime", "internal/fieldrule",
		"internal/worklist", "internal/shipping",
	}
)

// depsRules is the repository's whole package-graph policy, in one place, in the
// shape of the standard library's go/build deps_test.go.
var depsRules = []importRule{
	{
		name: "ui",
		forbid: func(from, to string) bool {
			return under(from, "internal/ui") && !under(to, "internal/ui") && !slices.Contains(uiMayImport, to)
		},
		reason: "the view layer never reaches request plumbing, the database, a feature or a desk",
	},
	{
		name: "web",
		forbid: func(from, to string) bool {
			return from == "internal/web" && to != "internal/i18n"
		},
		reason: "request plumbing does not depend on the view layer",
	},
	{
		name: "i18n",
		forbid: func(from, _ string) bool {
			return from == "internal/i18n"
		},
		reason: "a closed set names its own labels with the catalogue's keys, not the other way round",
	},
	{
		name: "leaves",
		hard: true,
		forbid: func(from, to string) bool {
			return slices.Contains(importFreeLeaves, from) ||
				(slices.Contains(i18nOnlyLeaves, from) && to != "internal/i18n")
		},
		reason: "a vocabulary package can be imported by anything without a cycle",
	},
	{
		name: "desks",
		forbid: func(from, to string) bool {
			root := deskRoot(to)
			return root != "" && from != "cmd/goen" && !under(from, root)
		},
		reason: "a desk is imported in production only by cmd/goen or from inside its own directory; " +
			"a desk reaches another desk through an interface it declares",
	},
	{
		name: "desks and storefront pages",
		forbid: func(from, to string) bool {
			return isDesk(from) && slices.Contains(storefrontPages, to)
		},
		reason: "the back office does not depend on a storefront page package",
	},
	{
		name: "cart and account",
		forbid: func(from, to string) bool {
			return under(from, "internal/cart") && under(to, "internal/account")
		},
		reason: "the cart does not depend on the account feature",
	},
	{
		name: "back office and account",
		hard: true,
		forbid: func(from, to string) bool {
			return under(from, "internal/admin") && under(to, "internal/account")
		},
		reason: "the back office does not depend on the storefront's account feature",
	},
	{
		name: "foundation",
		hard: true,
		forbid: func(from, to string) bool {
			return (under(from, "internal/admin/access") || under(from, "internal/admin/audit")) && isDesk(to)
		},
		reason: "access and audit are the foundation every desk imports, so they import no desk",
	},
	{
		name: "one way",
		hard: true,
		forbid: func(from, to string) bool {
			return under(to, "internal/admin") && !under(from, "internal/admin") && from != "cmd/goen"
		},
		reason: "only the back office and cmd/goen import a back-office package",
	},
}

// skippedDir mirrors the go command: it ignores directories whose name starts
// with "." or "_", and testdata. node_modules is skipped as well.
func skippedDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
		name == "testdata" || name == "node_modules"
}

// modulePath reads the module path from the first module line of go.mod.
func modulePath(t *testing.T, root string) string {
	t.Helper()
	//nolint:gosec // G304: the path is go.mod of this repository.
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}

// ignoredByBuildLine reports whether the file opts out of every build with
// //go:build ignore.
func ignoredByBuildLine(comments [][]string) bool {
	for _, group := range comments {
		if slices.Contains(group, "//go:build ignore") {
			return true
		}
	}
	return false
}

// loadImportGraph parses the imports of every production .go file in the module,
// including files behind //go:build integration, and returns the goen-to-goen edges.
func loadImportGraph(t *testing.T) importGraph {
	t.Helper()

	root := repoRoot(t)
	module := modulePath(t, root)
	prefix := module + "/"
	g := importGraph{edges: map[string]map[string][]string{}, scanned: map[string]bool{}}
	fset := token.NewFileSet()

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
		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		var header [][]string
		for _, group := range f.Comments {
			if group.Pos() >= f.Package {
				break
			}
			texts := make([]string, 0, len(group.List))
			for _, c := range group.List {
				texts = append(texts, c.Text)
			}
			header = append(header, texts)
		}
		if ignoredByBuildLine(header) {
			return nil
		}

		dir, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		from := filepath.ToSlash(dir)
		g.scanned[from] = true
		for _, spec := range f.Imports {
			imported, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			to, ok := strings.CutPrefix(imported, prefix)
			if !ok {
				continue
			}
			if g.edges[from] == nil {
				g.edges[from] = map[string][]string{}
			}
			g.edges[from][to] = append(g.edges[from][to], filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return g
}

// TestDependencies holds the repository's import policy: one table of rules over
// the production import graph, plus the edges the plan has not removed yet.
func TestDependencies(t *testing.T) {
	t.Parallel()

	g := loadImportGraph(t)

	// The walker must not pass by reading nothing.
	if len(g.edges) < 50 {
		t.Errorf("the graph has %d importing packages, want at least 50: the walker reads too little", len(g.edges))
	}
	if _, ok := g.edges["cmd/goen"]; !ok {
		t.Error("cmd/goen is not among the importing packages: the walker reads too little")
	}
	for pkg := range g.scanned {
		for seg := range strings.SplitSeq(pkg, "/") {
			if strings.HasPrefix(seg, ".") && seg != "." {
				t.Errorf("package %q has a dot segment: the walk must skip hidden directories", pkg)
			}
		}
	}

	var problems []string
	waived := map[string]bool{} // exception keys that a soft rule still needs
	for from, tos := range g.edges {
		for to, files := range tos {
			slices.Sort(files)
			key := from + " -> " + to
			for _, rule := range depsRules {
				if !rule.forbid(from, to) {
					continue
				}
				if _, ok := exceptions[key]; ok && !rule.hard {
					waived[key] = true
					continue
				}
				problems = append(problems, fmt.Sprintf("%s (%s): %s [rule %q]",
					key, strings.Join(slices.Compact(files), ", "), rule.reason, rule.name))
			}
		}
	}
	for key, task := range exceptions {
		if waived[key] {
			continue
		}
		from, to, _ := strings.Cut(key, " -> ")
		if _, exists := g.edges[from][to]; exists {
			problems = append(problems, fmt.Sprintf("%s: delete this exception, no rule that admits exceptions forbids this edge (%s)", key, task))
			continue
		}
		problems = append(problems, fmt.Sprintf("%s: delete this exception, its edge is gone (%s)", key, task))
	}

	slices.Sort(problems)
	for _, p := range problems {
		t.Error(p)
	}
}
