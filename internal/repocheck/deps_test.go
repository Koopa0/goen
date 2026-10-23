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
// module ("internal/ui/tile", "cmd/goen", "assets").
type importGraph struct {
	edges   map[string]map[string][]string
	scanned map[string]bool // every directory that held a production .go file
}

// holds reports whether a scanned package is dir or lies below it.
func (g importGraph) holds(dir string) bool {
	for pkg := range g.scanned {
		if under(pkg, dir) {
			return true
		}
	}
	return false
}

// importRule is one row of the policy table. forbids reports whether the edge
// from -> to breaks the rule. A hard rule admits no exception; any other rule's
// current violations are listed in exceptions. A predicate names a package only
// through the constants below, never a string literal, and the row lists each
// one in pkgs (a package) or dirs (a directory it matches with under, which may
// hold no .go file itself): TestDependencies fails a row whose entry names no
// scanned package, as it does for an entry of packageLists.
type importRule struct {
	name   string
	hard   bool
	pkgs   []string
	dirs   []string
	forbid func(from, to string) bool
	reason string
}

// The packages and directories the rule predicates name. dirAdmin and pkgAdmin
// are one path in two roles: the back office's directory, matched with under,
// and the package at its root, matched exactly.
const (
	cmdGoen       = "cmd/goen"
	dirUI         = "internal/ui"
	dirWeb        = "internal/web"
	dirAdmin      = "internal/admin"
	pkgAdmin      = "internal/admin"
	pkgI18n       = "internal/i18n"
	pkgMoney      = "internal/money"
	pkgFormtext   = "internal/web/formtext"
	pkgSite       = "internal/site"
	pkgPolicy     = "internal/policy"
	pkgCampaign   = "internal/discount/campaign"
	pkgLetter     = "internal/letter"
	pkgCart       = "internal/cart"
	pkgCheckout   = "internal/checkout"
	pkgAccount    = "internal/account"
	pkgAuth       = "internal/auth"
	pkgWishlist   = "internal/wishlist"
	pkgGoogle     = "internal/auth/google"
	pkgStoremap   = "internal/shipping/storemap"
	pkgAccesstest = "internal/admin/accesstest"
	pkgAudit      = "internal/admin/audit"
	pkgWorklist   = "internal/admin/worklist"
	pkgAdmintest  = "internal/admin/admintest"
)

// exceptions are the edges the rules forbid and the plan has not removed yet:
// key "from -> to", value the task that removes the edge. An entry may only
// disappear; one whose edge is gone fails, so each task proves its own work.
var exceptions = map[string]string{
	"internal/admin/admintest -> internal/admin/orders":  "T48",
	"internal/admin/admintest -> internal/admin/refunds": "T48",
	"internal/admin/admintest -> internal/admin/returns": "T48",
}

// under reports whether pkg is dir or lies below it.
func under(pkg, dir string) bool {
	return pkg == dir || strings.HasPrefix(pkg, dir+"/")
}

// deskRoot returns "internal/admin/<x>" for a desk package and "" for anything
// else. The package internal/admin itself is not below dirAdmin, so it is no desk.
// accesstest, audit, worklist and admintest are not desks either: audit, like
// internal/admin, is the foundation every desk may import, accesstest is the route
// check every desk's tests run, worklist is the vocabulary the desks use to report
// work for one another, admintest is the shared test fixture.
func deskRoot(pkg string) string {
	rest, ok := strings.CutPrefix(pkg, dirAdmin+"/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	root := dirAdmin + "/" + name
	if name == "" || slices.Contains(notDesks, root) {
		return ""
	}
	return root
}

func isDesk(pkg string) bool { return deskRoot(pkg) != "" }

// importedFrom reports whether a package in dir, or below it, imports to.
func importedFrom(g importGraph, dir, to string) bool {
	for from, tos := range g.edges {
		if _, ok := tos[to]; ok && under(from, dir) {
			return true
		}
	}
	return false
}

// isLeaf reports whether pkg is on one of the leaf lists the rule "leaves" holds.
func isLeaf(pkg string) bool {
	return slices.Contains(importFreeLeaves, pkg) || slices.Contains(i18nOnlyLeaves, pkg) ||
		slices.Contains(formtextLeaves, pkg) || slices.Contains(moneyLeaves, pkg)
}

var (
	// notDesks are the packages under internal/admin that are not desks.
	notDesks = []string{pkgAccesstest, pkgAudit, pkgWorklist, pkgAdmintest}
	// uiMayImport is what the shared visual language (internal/ui/…) may import
	// besides itself.
	uiMayImport = []string{
		"assets", "internal/i18n", "internal/keyset", "internal/money", "internal/order",
		"internal/product/inventory", "internal/shoptime", "internal/user",
	}
	// storefrontPages are the packages that serve storefront pages. The rule
	// that uses them leaves out, on purpose: internal/contact (admin/messages
	// uses its Subject vocabulary), internal/loyalty (programme rules
	// admin/tiers configures), and internal/media and internal/payment
	// (shared infrastructure). It matches exactly, not by prefix:
	// internal/cart/cartitem is a vocabulary leaf, not a page.
	storefrontPages = []string{
		"internal/account", "internal/auth", "internal/cart", "internal/catalog", "internal/checkout", "internal/discount/campaign", "internal/home",
		"internal/newsletter", "internal/policy", "internal/site", "internal/wishlist",
	}
	// siteMayImport is what internal/site may import besides itself and the
	// leaves: the foundation every storefront page is built on.
	siteMayImport = []string{
		"assets", "internal/i18n", "internal/web/respond", "internal/ui/frame",
	}
	// policyMayImport is what internal/policy may import besides itself and the
	// leaves: the foundation its pages are built on, and the stat line the home,
	// department and product pages draw the stated rules in.
	policyMayImport = []string{
		"internal/i18n", "internal/web/respond", "internal/ui/widget", "internal/ui/frame",
	}
	// campaignMayImport is what internal/discount/campaign may import besides itself and
	// the leaves: the message catalogue, the stat its schedule is told in, and
	// the foundation its page and /deals are built on, the product tile included.
	campaignMayImport = []string{
		"assets", "internal/i18n", "internal/ui/widget", "internal/ui/frame", "internal/ui/tile", "internal/web/respond",
	}
	// importFreeLeaves import no goen package at all.
	importFreeLeaves = []string{
		"internal/web/clientip",
		"internal/shipping/postcode", "internal/user",
		"internal/money", "internal/db", "internal/keyset", "internal/bearer",
		"internal/email", "internal/cart/cartitem", "internal/product", "internal/web/compress",
		"internal/web/requestid", "internal/web/siteurl", "internal/web/probe",
	}
	// i18nOnlyLeaves import nothing from goen except the message catalogue.
	i18nOnlyLeaves = []string{
		"internal/invoice", "internal/product/labelling", "internal/order/returns", "internal/shoptime", "internal/ui/fieldrule",
		"internal/sales", "internal/admin/worklist", "internal/shipping", "internal/credit", "internal/web/formtext",
		"internal/ui/tone", "internal/product/inventory",
	}
	// formtextLeaves read a code a person typed: they import nothing from goen
	// except formtext, which folds its width, and the message catalogue.
	formtextLeaves = []string{"internal/discount/coupon", "internal/order"}
	// moneyLeaves name an amount in a sentence: they import nothing from goen
	// except money, which writes the amount, and the message catalogue.
	moneyLeaves = []string{"internal/payment/refund"}
	// googleMayImport is all of goen that Google's sign-in client imports:
	// bearer makes its state and PKCE verifier, siteurl reads the configured
	// origin its callback is built on.
	googleMayImport = []string{"internal/bearer", "internal/web/siteurl"}
	// storemapMayImport is all of goen that ECPay's store map imports: shipping
	// names the chains and the store-code shape, formtext refuses control
	// characters in a posted store, and i18n and assets build the two hop pages.
	storemapMayImport = []string{"assets", "internal/web/formtext", "internal/i18n", "internal/shipping"}
)

// packageLists are the package lists the rules above match against, by variable
// name, so TestDependencies can hold every entry to a package that exists.
var packageLists = map[string][]string{
	"uiMayImport": uiMayImport, "storefrontPages": storefrontPages, "siteMayImport": siteMayImport,
	"policyMayImport": policyMayImport, "campaignMayImport": campaignMayImport,
	"importFreeLeaves": importFreeLeaves, "i18nOnlyLeaves": i18nOnlyLeaves, "formtextLeaves": formtextLeaves,
	"moneyLeaves":     moneyLeaves,
	"googleMayImport": googleMayImport, "storemapMayImport": storemapMayImport, "notDesks": notDesks,
}

// mayImportFrom names, for each *MayImport list in packageLists, the directory
// whose packages it admits imports for. An entry no edge from there uses widens
// its rule silently, so TestDependencies fails it as it fails a stale exception.
var mayImportFrom = map[string]string{
	"uiMayImport": dirUI, "siteMayImport": pkgSite, "policyMayImport": pkgPolicy,
	"campaignMayImport": pkgCampaign, "googleMayImport": pkgGoogle, "storemapMayImport": pkgStoremap,
}

// depsRules is the repository's whole package-graph policy, in one place, in the
// shape of the standard library's go/build deps_test.go.
var depsRules = []importRule{
	{
		name: "ui",
		dirs: []string{dirUI},
		forbid: func(from, to string) bool {
			return under(from, dirUI) && !under(to, dirUI) && !slices.Contains(uiMayImport, to)
		},
		reason: "the shared visual language (internal/ui/…) never reaches request plumbing, the database, a feature or a desk",
	},
	{
		name: "web",
		pkgs: []string{pkgI18n},
		dirs: []string{dirWeb},
		forbid: func(from, to string) bool {
			return under(from, dirWeb) && !under(to, dirWeb) && to != pkgI18n
		},
		reason: "request plumbing (internal/web/…) imports nothing from goen but itself and the message catalogue: " +
			"no feature, desk or part of the shared visual language (internal/ui/…)",
	},
	{
		name: "i18n",
		pkgs: []string{pkgI18n},
		forbid: func(from, _ string) bool {
			return from == pkgI18n
		},
		reason: "a closed set names its own labels with the catalogue's keys, not the other way round",
	},
	{
		name: "leaves",
		hard: true,
		pkgs: []string{pkgI18n, pkgFormtext, pkgMoney},
		forbid: func(from, to string) bool {
			return slices.Contains(importFreeLeaves, from) ||
				(slices.Contains(i18nOnlyLeaves, from) && to != pkgI18n) ||
				(slices.Contains(formtextLeaves, from) && to != pkgI18n && to != pkgFormtext) ||
				(slices.Contains(moneyLeaves, from) && to != pkgI18n && to != pkgMoney)
		},
		reason: "a vocabulary package can be imported by anything without a cycle",
	},
	{
		name: "site",
		hard: true,
		pkgs: []string{pkgSite},
		forbid: func(from, to string) bool {
			return under(from, pkgSite) && !under(to, pkgSite) &&
				!slices.Contains(siteMayImport, to) && !isLeaf(to)
		},
		reason: "every storefront page wears site's chrome, so site imports no feature; " +
			"it reaches one through an interface it declares, as Catalogue reaches the catalogue",
	},
	{
		name: "policy",
		hard: true,
		pkgs: []string{pkgPolicy},
		forbid: func(from, to string) bool {
			return under(from, pkgPolicy) && !under(to, pkgPolicy) &&
				!slices.Contains(policyMayImport, to) && !isLeaf(to)
		},
		reason: "the home, department and product pages read the stated rules from policy, so policy imports no feature; " +
			"a number it states is read from the database or restated beside a test in the package that enforces it",
	},
	{
		name: "campaign",
		hard: true,
		pkgs: []string{pkgCampaign},
		forbid: func(from, to string) bool {
			return under(from, pkgCampaign) && !under(to, pkgCampaign) &&
				!slices.Contains(campaignMayImport, to) && !isLeaf(to)
		},
		reason: "the catalogue, home and wishlist pages strike a price with campaign.OnSale and tell a campaign's window with its Schedule and End: " +
			"campaign imports no feature, so catalog, home and wishlist can import it",
	},
	{
		name: "desks",
		pkgs: []string{cmdGoen},
		dirs: []string{dirAdmin},
		forbid: func(from, to string) bool {
			root := deskRoot(to)
			return root != "" && from != cmdGoen && !under(from, root)
		},
		reason: "a desk is imported in production only by cmd/goen or from inside its own directory; " +
			"a desk reaches another desk through an interface it declares",
	},
	{
		name: "desks and storefront pages",
		dirs: []string{dirAdmin},
		forbid: func(from, to string) bool {
			return isDesk(from) && slices.Contains(storefrontPages, to)
		},
		reason: "the back office does not depend on a storefront page package",
	},
	{
		name: "letter",
		hard: true,
		pkgs: []string{pkgLetter, cmdGoen},
		forbid: func(from, to string) bool {
			return to == pkgLetter && from != cmdGoen
		},
		reason: "only cmd/goen composes and sends mail; a feature enqueues an outbox payload instead",
	},
	{
		name: "cart, checkout and account",
		pkgs: []string{pkgCart, pkgCheckout, pkgAccount, pkgAuth},
		forbid: func(from, to string) bool {
			return (under(from, pkgCart) || under(from, pkgCheckout)) &&
				(under(to, pkgAccount) || under(to, pkgAuth))
		},
		reason: "the cart and the checkout do not depend on the account feature or on sign-in",
	},
	{
		name: "back office and account",
		hard: true,
		pkgs: []string{pkgAccount, pkgWishlist, pkgAuth},
		dirs: []string{dirAdmin},
		forbid: func(from, to string) bool {
			return under(from, dirAdmin) &&
				(under(to, pkgAccount) || under(to, pkgWishlist) || under(to, pkgAuth))
		},
		reason: "the back office does not depend on the storefront's account feature, its wishlist or its sign-in",
	},
	{
		name: "auth and account",
		hard: true,
		pkgs: []string{pkgAuth, pkgAccount},
		forbid: func(from, to string) bool {
			return under(from, pkgAuth) && under(to, pkgAccount)
		},
		reason: "how a customer proves who they are does not depend on the customer's pages; " +
			"account imports auth, never the other way round",
	},
	{
		name: "google",
		hard: true,
		pkgs: []string{pkgGoogle},
		forbid: func(from, to string) bool {
			return under(from, pkgGoogle) && !slices.Contains(googleMayImport, to)
		},
		reason: "Google's sign-in client holds no session, account or page, so it imports no goen feature; " +
			"auth imports it, never the other way round",
	},
	{
		name: "storemap",
		hard: true,
		pkgs: []string{pkgStoremap},
		forbid: func(from, to string) bool {
			return under(from, pkgStoremap) && !slices.Contains(storemapMayImport, to)
		},
		reason: "ECPay's store map holds no cart, order or session, so it imports no goen feature; " +
			"the cart and the checkout import it, never the other way round",
	},
	{
		name: "foundation",
		hard: true,
		pkgs: []string{pkgAdmin, pkgAccesstest, pkgAudit},
		dirs: []string{dirAdmin},
		forbid: func(from, to string) bool {
			return (from == pkgAdmin || under(from, pkgAccesstest) || under(from, pkgAudit)) && isDesk(to)
		},
		reason: "internal/admin and audit are the foundation every desk imports, and accesstest the check every desk's routes run, " +
			"so they import no desk",
	},
	{
		name: "one way",
		hard: true,
		pkgs: []string{cmdGoen},
		dirs: []string{dirAdmin},
		forbid: func(from, to string) bool {
			return under(to, dirAdmin) && !under(from, dirAdmin) && from != cmdGoen
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
	if _, ok := g.edges[cmdGoen]; !ok {
		t.Error(cmdGoen + " is not among the importing packages: the walker reads too little")
	}
	for pkg := range g.scanned {
		for seg := range strings.SplitSeq(pkg, "/") {
			if strings.HasPrefix(seg, ".") && seg != "." {
				t.Errorf("package %q has a dot segment: the walk must skip hidden directories", pkg)
			}
		}
	}

	var problems []string
	// A name in a rule's list that no scanned directory carries turns the rule off
	// for the package it meant, as a package move leaves behind.
	for list, names := range packageLists {
		for _, name := range names {
			if !g.scanned[name] {
				problems = append(problems, fmt.Sprintf("%s: %q names no package: rename or delete the entry", list, name))
			}
		}
		if !strings.HasSuffix(list, "MayImport") {
			continue
		}
		from, ok := mayImportFrom[list]
		if !ok {
			problems = append(problems, list+": name the directory it admits imports for in mayImportFrom")
			continue
		}
		for _, name := range names {
			if !importedFrom(g, from, name) {
				problems = append(problems, fmt.Sprintf("%s: %q is imported by nothing under %s: delete the allowance", list, name, from))
			}
		}
	}
	// A path a rule's predicate names is held to the same standard: a package that
	// moved leaves the rule matching nothing, and no edge says so.
	for _, rule := range depsRules {
		for _, pkg := range rule.pkgs {
			if !g.scanned[pkg] {
				problems = append(problems, fmt.Sprintf("rule %q: its predicate names %q, which is no package: rename the path or delete the rule", rule.name, pkg))
			}
		}
		for _, dir := range rule.dirs {
			if !g.holds(dir) {
				problems = append(problems, fmt.Sprintf("rule %q: its predicate names directory %q, which holds no package: rename the path or delete the rule", rule.name, dir))
			}
		}
	}
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
