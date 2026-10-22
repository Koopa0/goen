package repocheck_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// maxTestNameLen is the most characters a top-level test name may have after
// "Test". It is goen's own limit, measured rather than cited: the standard
// library's test names run to 42 at the 99th percentile and 14 at the median,
// and no style guide states a number.
const maxTestNameLen = 40

// migratedTestPackages lists, by directory relative to the repository root,
// the packages whose test names have been migrated to the convention in
// CONTRIBUTING.md. Only these packages are checked: a package joins the list in
// the pull request that renames its tests, so a name cannot drift back, and
// nothing else in the repository is held to the rule until then.
//
// TestMigratedTestNames reads every listed package and fails if one has no
// tests to read; TestCheckTestNames runs the same checks over fixtures that
// fail them, so the guard cannot pass without looking.
var migratedTestPackages = []string{
	"assets",
	"internal/admin/banners",
	"internal/admin/coupons",
	"internal/admin/credits",
	"internal/admin/faq",
	"internal/admin/newsletters",
	"internal/admin/questions",
	"internal/admin/refunds",
	"internal/admin/reports",
	"internal/admin/stock",
	"internal/bearer",
	"internal/credit",
	"internal/email",
	"internal/i18n",
	"internal/invoice",
	"internal/keyset",
	"internal/media",
	"internal/money",
	"internal/order",
	"internal/order/access",
	"internal/order/cancellation",
	"internal/order/delivery",
	"internal/order/returns",
	"internal/product",
	"internal/product/copurchase",
	"internal/product/inventory",
	"internal/product/labelling",
	"internal/shipping/postcode",
	"internal/shoptime",
	"internal/user",
	"internal/web/clientip",
	"internal/web/compress",
	"internal/web/formtext",
	"internal/web/probe",
	"internal/web/ratelimit",
	"internal/web/requestid",
	"internal/web/respond",
	"internal/web/siteurl",
}

// testNameExceptions is the shrink-only allow list: for a listed package, each
// test that breaks the rule and why. A row is stale, and fails, once its test is
// renamed or deleted or no longer breaks the rule. Do not add a row to let a
// new name pass; rename the test. A row belongs here only for a test whose unit
// is not a Go identifier, such as a SQL object or a repository-wide rule.
var testNameExceptions = map[string]map[string]string{
	"assets": {
		"TestEveryCustomPropertyUsedIsDeclared":    "a rule of the whole stylesheet set: it walks every file under css/, so no one declared identifier is its unit",
		"TestEveryFocusOutlineColourIsTheRing":     "a rule of two stylesheets at once: it reads AppCSS and AdminCSS alike, so naming it after either would misstate its unit",
		"TestEveryFontFamilyCarriesItsLicence":     "a rule about the embedded font directories and their OFL.txt files, not about a function or type; no declared identifier names fonts/",
		"TestFieldBoundariesHoldContrast":          "a rule of three stylesheets at once: it reads one boundary rule each from AppCSS, AdminCSS and BaseCSS, so naming it after one would misstate its unit",
		"TestNoStylesheetFetchesFromAnotherOrigin": "a rule of the whole stylesheet set (the style-src 'self' promise): it walks every file under css/, so no one declared identifier is its unit",
		"TestSharedLayoutRulesAreDeclaredOnce":     "a rule of two stylesheets at once: it counts one selector in AppCSS and one in AdminCSS, so naming it after either would misstate its unit",
	},
	"internal/admin/refunds": {
		"TestClaimReturnRefundExecutionConcurrent": "the unit is the SQL function claim_return_refund_execution, called straight through the pool; no Go identifier of this package is what the test drives",
		"TestOrderPageIssuedInvoiceNoProvider":     "the unit is Store.Order of package orders, which this package does not declare; moving the test there is a reorganisation this pass does not do",
		"TestRecordRefundOutcomes":                 "the unit is the SQL functions record_refund_pending, _requires_action, _succeeded, _failed, _cancelled and _api_rejection, called straight through the pool, not a Go identifier",
		"TestRecordRefundSucceededLockOrder":       "the unit is the SQL function record_refund_succeeded, raced against claim_return_refund_execution through the pool, not a Go identifier",
		"TestRecordRefundSucceededReplay":          "the unit is the SQL function record_refund_succeeded, called straight through the pool, not a Go identifier",
		"TestReverseReturnPointsErasureLock":       "the unit is the SQL function reverse_return_points, raced against an erasure through the pool, not a Go identifier",
	},
	"internal/admin/reports": {
		"TestStockDaysCoverRefundedBeforeShipment": "the unit is Store.DaysCover of package stock, which this package does not declare (it declares only the Cover interface it calls through); moving the test there is a reorganisation this pass does not do",
	},
	"internal/admin/stock": {
		"TestProductVariantsRestockNoticesRollback": "the unit is the product_variants_restock_notices trigger, a SQL object, driven by plain UPDATE statements on the admin role's pool, not a Go identifier",
	},
	"internal/i18n": {
		"TestNoHardCodedGenericChrome": "a repository rule: no customer-facing .templ source hard-codes a generic English word; it reads every source file, and no function of the package is its unit",
		"TestNoHardCodedHan":           "a repository rule: no customer-facing source file hard-codes a Han string; it reads every source file, and no function of the package is its unit",
		"TestPendingTranslationDebt":   "a rule about pendingTranslation, a list declared in the test file beside the two rules above, so there is no package identifier to start from",
	},
	"internal/media": {
		"TestImage_SrcsetOfUpload":    "the unit is assets.Image.Srcset, which package assets declares; this package declares no identifier for it, and moving the test to assets is a reorganisation this pass does not do",
		"TestMediaObjectsConstraints": "the unit is the CHECK constraints of the media_objects table, a SQL object, not a Go identifier",
		"TestMediaObjectsImmutable":   "the unit is the media_objects_immutable trigger, a SQL object, not a Go identifier",
	},
	"internal/product/copurchase": {
		"TestRefreshCopurchasesOverlap": "its unit is the SQL function refresh_copurchases, called directly through the pool; Store.Refresh is not what it exercises",
	},
	"internal/shoptime": {
		"TestAmbientFormat":   "its unit is ambientFormat, a helper declared in the guard's test file, not part of the package",
		"TestNoAmbientFormat": "a repository rule: no .Format( call outside this package renders a time in the process's zone; it reads every source file, and no function of the package is its unit",
	},
}

// TestMigratedTestNames holds every package in migratedTestPackages to the rule
// that a top-level test name is at most 40 characters after "Test" and starts
// with the identifier of a function or type the package declares. The second
// part is checked only for a package that has non-test Go files, because only
// there is there an identifier to compare with; a package of guards has none
// and is held to length alone. Whether the identifier is the unit the test
// really exercises is left to review.
//
// It is not a count rule: it asks for no number of tests and no coverage.
func TestMigratedTestNames(t *testing.T) {
	t.Parallel()
	if len(migratedTestPackages) == 0 {
		t.Log("no package is listed in migratedTestPackages yet, so nothing is checked")
	}
	for _, problem := range checkTestNames(repoRoot(t), migratedTestPackages, testNameExceptions) {
		t.Error(problem)
	}
}

// checkTestNames returns what is wrong with the test names of each package in
// pkgs, and with the exceptions that do not hold.
func checkTestNames(root string, pkgs []string, exceptions map[string]map[string]string) []string {
	var problems []string
	listed := map[string]bool{}
	for _, pkg := range pkgs {
		if listed[pkg] {
			problems = append(problems, pkg+" is listed twice in migratedTestPackages")
			continue
		}
		listed[pkg] = true
		problems = append(problems, checkPackageTestNames(root, pkg, exceptions[pkg])...)
	}
	for _, pkg := range slices.Sorted(maps.Keys(exceptions)) {
		if !listed[pkg] {
			problems = append(problems, pkg+" has rows in testNameExceptions but is not in migratedTestPackages: delete them")
		}
	}
	return problems
}

func checkPackageTestNames(root, pkg string, exceptions map[string]string) []string {
	tests, idents, err := scanTestPackage(filepath.Join(root, filepath.FromSlash(pkg)))
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", pkg, err)}
	}
	if len(tests) == 0 {
		// A scan that finds nothing is asserting nothing: the walk has stopped
		// matching, or the package was listed before it had tests.
		return []string{pkg + " is listed in migratedTestPackages but no top-level test was found in it"}
	}
	var problems []string
	for _, name := range slices.Sorted(maps.Keys(exceptions)) {
		switch {
		case strings.TrimSpace(exceptions[name]) == "":
			problems = append(problems, fmt.Sprintf("%s: the exception for %s gives no reason", pkg, name))
		case !slices.Contains(tests, name):
			problems = append(problems, fmt.Sprintf("%s: the exception for %s is stale: no such test any more; delete the row", pkg, name))
		case testNameFault(name, idents) == "":
			problems = append(problems, fmt.Sprintf("%s: the exception for %s is stale: the name now follows the rule; delete the row", pkg, name))
		}
	}
	for _, name := range tests {
		if fault := testNameFault(name, idents); fault != "" {
			if _, excepted := exceptions[name]; !excepted {
				problems = append(problems, fmt.Sprintf("%s: %s %s.\nName the test after the unit it exercises and move the sentence into its doc comment (CONTRIBUTING.md, Tests).", pkg, name, fault))
			}
		}
	}
	return problems
}

// testNameFault says how name breaks the rule, or returns "" when it does not.
// idents holds the package's own top-level identifiers with their first letter
// upper-cased, or is empty when the package has no non-test file.
func testNameFault(name string, idents map[string]bool) string {
	rest := strings.TrimPrefix(name, "Test")
	var faults []string
	if len(rest) > maxTestNameLen {
		faults = append(faults, fmt.Sprintf("has %d characters after Test, over the limit of %d", len(rest), maxTestNameLen))
	}
	if len(idents) > 0 && !startsWithIdent(rest, idents) {
		faults = append(faults, "does not start with the name of a function or type the package declares")
	}
	return strings.Join(faults, ", and ")
}

// startsWithIdent reports whether rest, up to its first underscore, begins with
// one of idents ending at a word boundary: TestParseQuantity begins with Parse,
// TestParsed does not.
func startsWithIdent(rest string, idents map[string]bool) bool {
	head, _, _ := strings.Cut(rest, "_")
	for i := 1; i <= len(head); i++ {
		if !idents[head[:i]] {
			continue
		}
		if i == len(head) {
			return true
		}
		if next, _ := utf8.DecodeRuneInString(head[i:]); !unicode.IsLower(next) {
			return true
		}
	}
	return false
}

// scanTestPackage parses every Go file in dir whatever its build tags and
// returns the names of its top-level tests (TestMain is not one) and the
// package's own top-level identifiers.
func scanTestPackage(dir string) (tests []string, idents map[string]bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read the package directory: %w", err)
	}
	idents = map[string]bool{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", entry.Name(), parseErr)
		}
		isTest := strings.HasSuffix(entry.Name(), "_test.go")
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil {
					continue
				}
				if isTest {
					if isTestName(d.Name.Name) {
						tests = append(tests, d.Name.Name)
					}
				} else {
					addIdent(idents, d.Name.Name)
				}
			case *ast.GenDecl:
				if isTest {
					continue
				}
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						addIdent(idents, s.Name.Name)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							addIdent(idents, n.Name)
						}
					}
				}
			}
		}
	}
	slices.Sort(tests)
	return slices.Compact(tests), idents, nil
}

// isTestName reports whether a func name is a test: Test followed by something
// that does not start with a lower-case letter, as go test requires. TestMain
// runs the package and is not a test.
func isTestName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Test")
	if !ok || rest == "" || name == "TestMain" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLower(first)
}

func addIdent(idents map[string]bool, name string) {
	if name == "_" || name == "init" || name == "main" {
		return
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	idents[string(r)] = true
}

// TestCheckTestNames runs the guard's checks over fixture packages: each fault
// the guard exists to catch has a case that must report it, and each way the
// check could pass without looking has one that must fail.
func TestCheckTestNames(t *testing.T) {
	t.Parallel()
	const source = "package shop\n\nfunc Parse() {}\n\ntype Store struct{}\n\nfunc (Store) Decide() {}\n"
	const long = "TestParseEveryRowOfTheCustomersCartAndKeepsGoing"
	body := func(names ...string) string {
		var b strings.Builder
		b.WriteString("package shop_test\n\nimport \"testing\"\n")
		for _, n := range names {
			fmt.Fprintf(&b, "\nfunc %s(t *testing.T) {}\n", n)
		}
		return b.String()
	}
	for _, tc := range []struct {
		name       string
		files      map[string]string
		exceptions map[string]string
		want       []string // substrings, one per problem; nil means none
	}{
		{
			name:  "FollowsTheRule",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParse", "TestParseQuantity", "TestStore_Decide", "TestMain")},
		},
		{
			name:  "BoundaryIsAWord",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParsed")},
			want:  []string{"TestParsed does not start with the name"},
		},
		{
			name:  "TooLong",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParse", long)},
			want:  []string{long + " has 44 characters after Test"},
		},
		{
			name:  "FortyIsAllowed",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParse" + strings.Repeat("X", 35))},
		},
		{
			name:  "FortyOneIsNot",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestParse" + strings.Repeat("X", 36))},
			want:  []string{"has 41 characters after Test"},
		},
		{
			name:  "NoUnit",
			files: map[string]string{"shop.go": source, "shop_test.go": body("TestEveryRowIsKept")},
			want:  []string{"TestEveryRowIsKept does not start with the name"},
		},
		{
			name:  "UnexportedUnit",
			files: map[string]string{"shop.go": "package shop\n\nfunc roundUp() {}\n", "shop_test.go": body("TestRoundUp")},
		},
		{
			name:  "TaggedFileIsRead",
			files: map[string]string{"shop.go": source, "shop_integration_test.go": "//go:build integration\n\n" + body("TestParse", long)},
			want:  []string{long},
		},
		{
			name:  "OnlyTestsLengthOnly",
			files: map[string]string{"guard_test.go": body("TestDependencies", "TestEveryRowIsKept")},
		},
		{
			name:       "ExceptionHolds",
			files:      map[string]string{"shop.go": source, "shop_test.go": body("TestParse", long)},
			exceptions: map[string]string{long: "a rule of the whole repository"},
		},
		{
			name:       "ExceptionWithoutReason",
			files:      map[string]string{"shop.go": source, "shop_test.go": body("TestParse", long)},
			exceptions: map[string]string{long: " "},
			want:       []string{"the exception for " + long + " gives no reason"},
		},
		{
			name:       "ExceptionForARenamedTest",
			files:      map[string]string{"shop.go": source, "shop_test.go": body("TestParse")},
			exceptions: map[string]string{long: "a rule of the whole repository"},
			want:       []string{"the exception for " + long + " is stale: no such test"},
		},
		{
			name:       "ExceptionForANameThatNowFits",
			files:      map[string]string{"shop.go": source, "shop_test.go": body("TestParse", "TestParseQuantity")},
			exceptions: map[string]string{"TestParseQuantity": "was long once"},
			want:       []string{"the exception for TestParseQuantity is stale: the name now follows the rule"},
		},
		{
			name:  "NoTests",
			files: map[string]string{"shop.go": source, "shop_test.go": "package shop_test\n"},
			want:  []string{"no top-level test was found"},
		},
		{
			name:  "OnlyTestMain",
			files: map[string]string{"shop.go": source, "main_integration_test.go": body("TestMain")},
			want:  []string{"no top-level test was found"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "internal", "shop")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var exceptions map[string]map[string]string
			if tc.exceptions != nil {
				exceptions = map[string]map[string]string{"internal/shop": tc.exceptions}
			}
			got := checkTestNames(root, []string{"internal/shop"}, exceptions)
			if len(got) != len(tc.want) {
				t.Fatalf("checkTestNames(%q) = %q, want %d problems containing %q", tc.name, got, len(tc.want), tc.want)
			}
			for i, want := range tc.want {
				if !strings.Contains(got[i], want) {
					t.Errorf("checkTestNames(%q)[%d] = %q, want it to contain %q", tc.name, i, got[i], want)
				}
			}
		})
	}
}

// TestCheckTestNamesListing covers what a list can get wrong: a package that is
// not there, one listed twice, and exceptions for a package that is not listed.
func TestCheckTestNamesListing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "shop")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shop_test.go"), []byte("package shop_test\n\nimport \"testing\"\n\nfunc TestParse(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := checkTestNames(root, nil, nil); len(got) != 0 {
		t.Errorf("checkTestNames(root, nil, nil) = %q, want none: the empty list is allowed", got)
	}
	for _, tc := range []struct {
		name       string
		pkgs       []string
		exceptions map[string]map[string]string
		want       string
	}{
		{"Missing", []string{"gone"}, nil, "gone: read the package directory"},
		{"Twice", []string{"shop", "shop"}, nil, "shop is listed twice"},
		{"Unlisted", []string{"shop"}, map[string]map[string]string{"other": {"TestX": "why"}}, "other has rows in testNameExceptions but is not in migratedTestPackages"},
	} {
		got := checkTestNames(root, tc.pkgs, tc.exceptions)
		if len(got) != 1 || !strings.Contains(got[0], tc.want) {
			t.Errorf("checkTestNames(%q) = %q, want one problem containing %q", tc.name, got, tc.want)
		}
	}
}
