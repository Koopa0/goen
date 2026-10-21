package repocheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// assetReaders lists every function in a _test.go file that reads goen's own
// stylesheets or scripts as text, keyed "<file relative to the root>#<func>".
// The value is the reason the bytes may be read:
//
//   - "contract: ..." is permanent: the bytes themselves are what the test
//     locks (serving, caching, compression, the fonts licence, the CSP
//     origin, which sheet a shell links).
//   - "T14b" ... "T14e" name the task that replaces the text match with a
//     check of behaviour in a real browser, and "#380" the goilerplate
//     rebuild that re-keys the check onto semantic tokens. Each of those
//     rows is deleted by the task that names it; the list only shrinks.
var assetReaders = map[string]string{
	"assets/assets_test.go#TestACartQuantityAppliesItselfWhereScriptRuns":                               "T14d",
	"assets/assets_test.go#TestAHeadRequestCarriesTheSelectedRepresentationHeaders":                     "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestAWriteFailureUsesTheConfiguredRequestLogger":                             "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestAnAssetETagDistinguishesEncodings":                                       "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestAnAssetIsServedPrecompressed":                                            "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestAnAssetMatchesAValidatorOnALaterHeaderLine":                              "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestAnAssetWithoutAcceptEncodingIsIdentity":                                  "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestAssetGzipNegotiationHonoursQualityAndWildcard":                           "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestHandlerRefusesLongCacheWithoutMatchingVersion":                           "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestHandlerServesRequestedAsset":                                             "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestRequiredAssetsAreVersioned":                                              "contract: serving, versioning, caching and compression act on the embedded files themselves",
	"assets/assets_test.go#TestTheCarouselMovesOnlyOnTheVisitorsInput":                                  "T14d",
	"assets/assets_test.go#TestTheDemoAccountButtonSignsInWhereScriptRuns":                              "T14d",
	"assets/assets_test.go#TestTheStyleSheetCarriesThePageTransition":                                   "T14d",
	"assets/band_test.go#TestTheBandWithoutAPhotographKeepsThePageColumn":                               "T14b",
	"assets/contrast_test.go#TestControlBoundariesReadOnTheirGrounds":                                   "#380",
	"assets/contrast_test.go#TestEveryCustomPropertyUsedIsDeclared":                                     "#380",
	"assets/contrast_test.go#TestEveryDeclaredToneColourHasAConsumer":                                   "#380",
	"assets/contrast_test.go#TestEveryFocusOutlineColourIsTheRing":                                      "#380",
	"assets/contrast_test.go#TestEveryToneGroundHoldsItsText":                                           "#380",
	"assets/contrast_test.go#TestInvalidAndReadOnlyFieldBoundariesHoldContrast":                         "#380",
	"assets/contrast_test.go#TestPromoIsDeclaredOnlyForThePromotionStrip":                               "#380",
	"assets/contrast_test.go#TestShelfPricesReadOnTheirGrounds":                                         "#380",
	"assets/contrast_test.go#TestTextTokensReadOnTheGroundsTheyAreUsedOn":                               "#380",
	"assets/contrast_test.go#TestTheBandReadsItsMutedTextFromTheTone":                                   "#380",
	"assets/contrast_test.go#TestTheFocusRingReadsOnEveryGround":                                        "#380",
	"assets/contrast_test.go#TestTheStarPickerIsVisibleOnTheReviewForm":                                 "#380",
	"assets/contrast_test.go#aliasTokens":                                                               "#380",
	"assets/contrast_test.go#baseTokens":                                                                "#380",
	"assets/contrast_test.go#toneBlocks":                                                                "#380",
	"assets/duplicate_rules_test.go#TestSharedLayoutRulesAreDeclaredOnce":                               "#380",
	"assets/fonts_test.go#TestFontStylesheetMatchesTheEmbeddedFonts":                                    "contract: the font licence and the @font-face list name the embedded font files",
	"assets/fonts_test.go#TestNoStylesheetFetchesFromAnotherOrigin":                                     "contract: the CSP is style-src and font-src self, so no sheet may name another origin",
	"assets/hero_test.go#TestAHeroSlideWithoutAPhotographIsAPosterBelowDesktop":                         "T14b",
	"assets/period_test.go#checkRules":                                                                  "T14c",
	"assets/precompress_test.go#TestEveryPrecompressibleExtensionIsExplicitAndWorks":                    "contract: the precompressed bytes must decode to the embedded file",
	"assets/tiles_test.go#TestTheRelatedProductsFollowTheGridsColumnGap":                                "T14b",
	"cmd/goen/main_integration_test.go#TestTheRouterKeepsAssetsStatelessAndCompressesPages":             "contract: the served asset bytes and their headers are the contract",
	"cmd/goen/server_test.go#FuzzAssetAndDynamicGzipNegotiationAgree":                                   "contract: the served asset bytes, their headers and the middleware around them are the contract",
	"cmd/goen/server_test.go#TestAnAssetIsNotCompressedTwiceByTheChain":                                 "contract: the served asset bytes, their headers and the middleware around them are the contract",
	"cmd/goen/server_test.go#TestAnAssetRequestNeverReachesPerVisitorMiddleware":                        "contract: the served asset bytes, their headers and the middleware around them are the contract",
	"cmd/goen/server_test.go#TestOnlyAStaffMemberGetsTheBackOfficeEntrance":                             "contract: the served asset bytes, their headers and the middleware around them are the contract",
	"cmd/goen/server_test.go#TestSpeculationRulesAreOfferedOnlyWhereTheyAreSafe":                        "contract: the served asset bytes, their headers and the middleware around them are the contract",
	"internal/ui/chart/columns_test.go#TestColumnsDropStripNamesFromAFigureNarrowerThanTheyAreFittedTo": "T14c",
	"internal/ui/layouts/chrome_test.go#TestOnlyTheBackOfficeLinksItsStylesheet":                        "contract: which sheet each shell links is the page contract",
	"internal/checkout/page_test.go#TestTheCheckoutFormIsCheckedByTheServerAndFocusesTheFirstRefusal":   "T14e",
	"internal/catalog/product_page_test.go#TestTheBottomBarIsDrivenByAnIntersectionObserver":            "T14e",
	"internal/catalog/stepper_test.go#TestTheStepperNeverDisablesTheButtonThatHasFocus":                 "T14e",
}

// TestOnlyListedTestsReadTheStyleSheetsAndScripts keeps tests from matching
// the text of assets/css and assets/js.
//
// A regular expression over a stylesheet fails when two declarations swap
// places and stays green when a later rule overrides the one it checked; a
// check of computed style, box geometry or what the script does fails only
// when the behaviour changes. A new test therefore measures behaviour in a
// browser (internal/ui/uitest) or, for a static fact such as a contrast ratio
// between two tokens, parses the value; a test that reads the bytes because
// the bytes are the contract is listed in assetReaders with "contract: ...".
//
// Scope: _test.go files only. Production code reads the sheets by design
// (assets/assets.go embeds them, assets/precompress.go compresses them).
// The walk parses every _test.go file under the root whatever its build tag,
// and skips directories whose name starts with "." or "_" (the checkout may
// hold untracked .worktrees and .claude copies of the repository), testdata
// and node_modules.
//
// A function reads CSS or JS when its declaration contains any of:
//
//	(a) the selector assets.X, or inside package assets the identifier X, for
//	    X one of the asset-name constants in assets/assets.go;
//	(b) a string literal whose base name is a file under assets/css or
//	    assets/js (listed at run time, not hard-coded);
//	(c) a string literal that is exactly ".css", ".js", "css" or "js", the
//	    spelling of a walk over the sheets.
func TestOnlyListedTestsReadTheStyleSheetsAndScripts(t *testing.T) {
	root := repoRoot(t)
	embedded := embeddedSheetNames(t, root)
	readers, parsed := assetReadersIn(t, root, embedded)

	// The repository holds 793 _test.go files under these skip rules; fail if
	// the walk reads fewer, which would mean it stopped looking.
	const minTestFiles = 770
	if parsed < minTestFiles {
		t.Fatalf("the walk parsed %d _test.go files, want at least %d", parsed, minTestFiles)
	}
	if len(readers) == 0 {
		t.Fatal("no test function reads the style sheets or scripts: the detector is blind")
	}

	for _, key := range sortedKeys(readers) {
		if _, ok := assetReaders[key]; !ok {
			t.Errorf("%s reads goen's CSS/JS text; check the behaviour in a browser test (internal/ui/uitest) or, for a static fact, parse it; if the bytes themselves are the contract, list it here with \"contract: …\"", key)
		}
	}
	for _, key := range sortedKeys(assetReaders) {
		if _, ok := readers[key]; !ok {
			t.Errorf("%s: stale, delete this entry", key)
		}
	}
	for key, reason := range assetReaders {
		if reason != "#380" && !strings.HasPrefix(reason, "contract: ") && !strings.HasPrefix(reason, "T14") {
			t.Errorf("%s: reason %q is neither \"contract: <why>\", a T14 task, nor \"#380\"", key, reason)
		}
	}
}

// assetNameConstants are the names in assets/assets.go that hold the path of a
// stylesheet or script.
var assetNameConstants = map[string]bool{
	"AppCSS": true, "AdminCSS": true, "BaseCSS": true, "FontsCSS": true,
	"AppJS": true, "TransitionsJS": true, "HTMXJS": true,
}

// assetDirs are the directories under assets/ that hold the sheets and
// scripts, and sheetSpellings the bare strings a test uses to walk them. They
// are package-level so that the detector's own functions do not match the
// rules they implement.
var (
	assetDirs      = []string{"css", "js"}
	sheetSpellings = []string{".css", ".js", "css", "js"}
)

// embeddedSheetNames returns the base names of the files under assets/css and
// assets/js.
func embeddedSheetNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	for _, dir := range assetDirs {
		err := filepath.WalkDir(filepath.Join(root, "assets", dir), func(_ string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				names[entry.Name()] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("list assets/%s: %v", dir, err)
		}
	}
	if len(names) == 0 {
		t.Fatal("assets/css and assets/js hold no files")
	}
	return names
}

// assetReadersIn returns the key of every function declaration in a _test.go
// file under root that reads CSS or JS, and the number of files parsed.
func assetReadersIn(t *testing.T, root string, embedded map[string]bool) (readers map[string]bool, parsed int) {
	t.Helper()
	readers = make(map[string]bool)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		parsed++
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		inAssets := file.Name.Name == "assets"
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && readsSheet(fn, inAssets, embedded) {
				readers[filepath.ToSlash(rel)+"#"+fn.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return readers, parsed
}

// readsSheet reports whether fn mentions a stylesheet or script by rule (a),
// (b) or (c) of the test's doc comment.
func readsSheet(fn *ast.FuncDecl, inAssets bool, embedded map[string]bool) bool {
	found := false
	ast.Inspect(fn, func(node ast.Node) bool {
		if found {
			return false
		}
		switch n := node.(type) {
		case *ast.SelectorExpr:
			pkg, ok := n.X.(*ast.Ident)
			found = ok && pkg.Name == "assets" && assetNameConstants[n.Sel.Name]
		case *ast.Ident:
			found = inAssets && assetNameConstants[n.Name]
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(n.Value)
			if err != nil {
				return true
			}
			found = embedded[filepath.Base(value)] || slices.Contains(sheetSpellings, value)
		}
		return !found
	})
	return found
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
