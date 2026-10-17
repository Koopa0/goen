package repocheck_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// randReaders lists every crypto/rand.Read call outside internal/bearer, keyed
// "<slash path>#<enclosing func>", with why it is not a bearer token.
var randReaders = map[string]string{ //nolint:gosec // G101: the key names the function NewSecret, no credential is held
	"internal/auth/auth.go#HashPassword":           "argon2 salt",
	"internal/admin/twofactor/totp.go#NewSecret":   "TOTP shared secret",
	"internal/admin/twofactor/crypt.go#seal":       "AES-GCM nonce",
	"internal/admin/stock/handler.go#newKey":       "form idempotency key, never digested",
	"internal/cart/cart.go#newCheckoutAttemptID":   "checkout attempt id echoed in the form",
	"internal/cart/ecpaymap.go#NewMerchantTradeNo": "ECPay map trade number",
	"internal/cart/ecpaymap.go#newPickupNonce":     "map return nonce",
}

// sha256Users lists every crypto/sha256 Sum256 or New call outside
// internal/bearer, keyed like randReaders, with why it does not digest a bearer
// token.
var sha256Users = map[string]string{
	"internal/auth/google.go#AuthorizeURL":             "PKCE S256 code challenge (RFC 7636 section 4.2)",
	"internal/admin/twofactor/crypt.go#newKeyedAEAD":   "key identifier written into a stored TOTP secret's header",
	"internal/admin/twofactor/store.go#mailedCodeHash": "digest of a mailed one-time code",
	"internal/cart/cart.go#ID":                         "checkout quote identity",
	"internal/site/chrome.go#dismissDigest":            "promo-dismiss cookie value",
	"internal/media/decode.go#normaliseDecoded":        "content address",
	"internal/ratelimit/ratelimit.go#clampKey":         "bounded limiter key",
	"internal/ui/pages/facets.go#CountID":              "HTML id",
	"internal/ui/pages/facets.go#SelectedID":           "HTML id",
	"assets/assets.go#index":                           "asset fingerprint",
	"migrations/migrations.go#Digest":                  "digest of migrations/001 a database records",
}

// TestNoBearerTokenIsMadeOrDigestedOutsideTheBearerPackage refuses a second
// token generator or token digest. Seven tables store digests of tokens made by
// internal/bearer, so a copy that drifts in encoding silently orphans every
// session, cart and mailed link.
//
// A new crypto/rand.Read, crypto/sha256.Sum256 or crypto/sha256.New outside
// internal/bearer is the defect this catches. Add an entry to randReaders or
// sha256Users only for randomness or hashing that is not a bearer token, and
// say why. An entry that matches no call fails too, so the lists only shrink.
func TestNoBearerTokenIsMadeOrDigestedOutsideTheBearerPackage(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	reads, sums := cryptoCalls(t, root)
	if len(reads) == 0 || len(sums) == 0 {
		t.Fatalf("found %d rand.Read and %d sha256 calls; the source walk is not reading the repository",
			len(reads), len(sums))
	}

	check := func(what string, found []string, allowed map[string]string, advice string) {
		t.Helper()
		for _, key := range found {
			if _, ok := allowed[key]; !ok {
				t.Errorf("%s calls %s outside internal/bearer: %s", key, what, advice)
			}
		}
		for key := range allowed {
			if !slices.Contains(found, key) {
				t.Errorf("stale entry %q: it matches no %s call, delete it", key, what)
			}
		}
	}
	check("rand.Read", reads, randReaders,
		"a bearer token comes from bearer.New; randomness for anything else goes in randReaders with a reason")
	check("sha256.Sum256 or sha256.New", sums, sha256Users,
		"a bearer token's digest is bearer.Digest; hashing for anything else goes in sha256Users with a reason")
}

// cryptoCalls returns the "<slash path>#<func>" of every call of
// crypto/rand.Read, and of crypto/sha256.Sum256 or crypto/sha256.New, in the
// module's non-test Go files outside internal/bearer.
func cryptoCalls(t *testing.T, root string) (reads, sums []string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case rel != "." && strings.HasPrefix(d.Name(), "."), d.Name() == "testdata", rel == "internal/bearer":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") ||
			filepath.ToSlash(filepath.Dir(rel)) == "internal/db" {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", rel, parseErr)
		}
		randName, sha256Name := importedAs(file, "crypto/rand"), importedAs(file, "crypto/sha256")
		if randName == "" && sha256Name == "" {
			return nil
		}
		for _, decl := range file.Decls {
			// A call in a package-level initialiser has no function to name.
			key := rel + "#(package level)"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				key = rel + "#" + fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				pkg, name, ok := qualified(call.Fun)
				switch {
				case !ok:
				case randName != "" && pkg == randName && name == "Read":
					reads = append(reads, key)
				case sha256Name != "" && pkg == sha256Name && (name == "Sum256" || name == "New"):
					sums = append(sums, key)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk for crypto calls: %v", err)
	}
	slices.Sort(reads)
	slices.Sort(sums)
	return slices.Compact(reads), slices.Compact(sums)
}

// importedAs is the name file gives the package at path, or "" when it does not
// import it (or imports it only for side effects).
func importedAs(file *ast.File, path string) string {
	for _, spec := range file.Imports {
		if spec.Path.Value != `"`+path+`"` {
			continue
		}
		if spec.Name == nil {
			return path[strings.LastIndex(path, "/")+1:]
		}
		if spec.Name.Name == "_" || spec.Name.Name == "." {
			return ""
		}
		return spec.Name.Name
	}
	return ""
}

// qualified splits pkg.Name.
func qualified(fun ast.Expr) (pkg, name string, ok bool) {
	sel, isSel := fun.(*ast.SelectorExpr)
	if !isSel {
		return "", "", false
	}
	id, isIdent := sel.X.(*ast.Ident)
	if !isIdent {
		return "", "", false
	}
	return id.Name, sel.Sel.Name, true
}
