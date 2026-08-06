package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryKeyIsTranslatedInEveryLocale proves no locale ships a fallback.
//
// The catalogue's shape now makes the ordinary way this broke impossible: a key
// and both its translations are ONE declaration, and key() panics on a blank
// one before the server binds a port. This is the belt to that brace — it
// asserts the panic is reachable from the outside rather than trusting it, and
// it is what would catch a future locale added to Locales() and to nothing else.
func TestEveryKeyIsTranslatedInEveryLocale(t *testing.T) {
	keys := Keys()
	if len(keys) == 0 {
		t.Fatal("the catalogue is empty; this test would pass on nothing")
	}

	for _, k := range keys {
		m, ok := MessageFor(k)
		if !ok {
			t.Errorf("%q is in Keys() and not in the catalogue", k)
			continue
		}
		for _, l := range Locales() {
			if strings.TrimSpace(m.in(l)) == "" {
				t.Errorf("%s has no translation for %q", l, k)
			}
		}
	}
}

// TestADuplicateKeyIsRefused proves the registry will not let two areas of the
// catalogue quietly claim one id.
//
// Before this shape, "messages" was a map literal and a repeated key was legal
// Go: the later entry won, and which one that was depended on the order somebody
// happened to write the file in.
func TestADuplicateKeyIsRefused(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("declaring an id twice did not panic; the later entry would " +
				"silently replace the earlier one")
		}
	}()
	key("nav.cart", Message{ZhHant: "第二個", En: "the second one"})
}

// TestAMissingTranslationIsRefused proves the same for a half-written entry.
//
// exhaustruct catches the literal that omits a field; this catches the one that
// spells it out as "". Both have to fail, because the second is what somebody
// writes to get past the first.
func TestAMissingTranslationIsRefused(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a blank translation did not panic")
		}
	}()
	key("test.blank", Message{ZhHant: "有字", En: ""})
}

// englishQuotesCJK is every key whose English text carries CJK characters ON
// PURPOSE, and why.
//
// The test below exists to catch a copy-paste that left Chinese in the English
// catalogue — an entry that looks filled in and is not. Text that QUOTES a
// Japanese or Chinese word in order to explain it is the opposite case, and the
// list is tiny and named for that reason: an entry here is a claim somebody has
// read the English and meant it.
var englishQuotesCJK = map[Key]string{
	"about.name": "explains ご縁 and 五円, so it has to print them",
}

// TestEnglishIsActuallyEnglish proves no Chinese was left in the English
// catalogue.
//
// A copy-paste that leaves a Chinese string in the English catalogue is the
// most likely way this breaks, and it is invisible in review — the entry looks
// filled in. Han characters in an English value are the signal.
func TestEnglishIsActuallyEnglish(t *testing.T) {
	for k, m := range messages {
		if why, allowed := englishQuotesCJK[k]; allowed {
			t.Logf("%s quotes CJK on purpose: %s", k, why)
			continue
		}
		v := m.En
		for _, r := range v {
			if r >= 0x4E00 && r <= 0x9FFF {
				t.Errorf("the English %q contains Han characters: %q", k, v)
				break
			}
		}
	}
}

// TestDetectPrefersAnExplicitChoice proves a chosen language beats a guessed
// one.
//
// The cookie is a choice the visitor made; Accept-Language is a guess a browser
// makes. A visitor who switched to English must not be switched back by a
// header they never configured.
func TestDetectPrefersAnExplicitChoice(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		header string
		want   Locale
	}{
		{"nothing at all", "", "", Default},
		{"cookie only", "en", "", En},
		{"header only", "", "en-GB,en;q=0.9", En},
		{"cookie beats the header", "en", "zh-TW,zh;q=0.9", En},
		{"cookie beats the header the other way", "zh-Hant", "en-US,en;q=0.9", ZhHant},
		{"an unknown cookie falls through to the header", "kling-on", "en;q=0.8", En},
		{"an unknown cookie and no header is the default", "kling-on", "", Default},
		// Simplified readers get Traditional, not English: closer by a long way.
		//
		// The header names English SECOND on purpose. With "zh-CN,zh" the case
		// passes whether zh-CN matched or fell through, because the default is
		// also Chinese — it proved nothing. English behind it is what makes the
		// two outcomes different.
		{"simplified Chinese, English behind it", "", "zh-CN,en;q=0.8", ZhHant},
		{"simplified Chinese alone", "", "zh-CN", ZhHant},
		{"English then Chinese", "", "en-US,en;q=0.9,zh;q=0.8", En},
		{"Chinese then English", "", "zh-TW;q=0.9,en;q=0.8", ZhHant},
		{"a language goen does not speak", "", "fr-FR,fr;q=0.9", Default},
		{"a header that does not parse", "", ";;;q=", Default},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tt.cookie != "" {
				// A request-side cookie: the attributes gosec looks for are
				// response-side, and a client never sends them.
				r.AddCookie(&http.Cookie{ //nolint:gosec // G124: request cookie, not a Set-Cookie
					Name: insecureCookieName, Value: tt.cookie,
				})
			}
			if tt.header != "" {
				r.Header.Set("Accept-Language", tt.header)
			}
			if got := Detect(r, false); got != tt.want {
				t.Errorf("Detect = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAMissingKeyIsVisible proves a gap in the catalogue shows on the page.
//
// It renders as the key itself — "nav.cart" on the page, greppable and
// impossible to mistake for copy. Returning "" would render as a blank button
// nobody notices until a customer cannot find the checkout.
func TestAMissingKeyIsVisible(t *testing.T) {
	ctx := WithLocale(t.Context(), En)
	if got := T(ctx, Key("does.not.exist")); got != "does.not.exist" {
		t.Errorf("a missing key rendered as %q, want the key itself", got)
	}
}

// TestTheCookieIsHostScopedWhenSecure proves the cookie carries the prefix its
// siblings do.
//
// __Host- forbids a Domain attribute and requires Secure and Path=/, which is
// what stops a sibling subdomain setting a visitor's language — a small thing,
// but the same prefix protects the cart and session cookies and a locale cookie
// that broke the pattern would be the one somebody copies next.
func TestTheCookieIsHostScopedWhenSecure(t *testing.T) {
	w := httptest.NewRecorder()
	SetCookie(w, En, true)
	got := w.Header().Get("Set-Cookie")

	// An independent literal, not CookieName: asserting the constant against
	// itself is a tautology that passes whatever the constant becomes. The
	// prefix is a PROTOCOL requirement, so it is spelled out here.
	if !strings.HasPrefix(got, "__Host-") {
		t.Errorf("cookie is %q, want a name beginning __Host-", got)
	}
	if !strings.HasPrefix(got, "__Host-goen_locale=") {
		t.Errorf("cookie is %q, want __Host-goen_locale", got)
	}
	for _, want := range []string{"Secure", "Path=/", "HttpOnly"} {
		if !strings.Contains(got, want) {
			t.Errorf("cookie %q is missing %s", got, want)
		}
	}
	if strings.Contains(got, "Domain=") {
		t.Errorf("cookie %q carries a Domain, which __Host- forbids", got)
	}

	// And the development name has no __Host- prefix, or a dev machine on
	// plain http:// would forget the choice on every request.
	w = httptest.NewRecorder()
	SetCookie(w, En, false)
	if strings.HasPrefix(w.Header().Get("Set-Cookie"), "__Host-") {
		t.Error("the insecure cookie carries a __Host- prefix; it will never come back")
	}
}

// TestEveryKeyIsRendered proves the catalogue has nothing in it that no page
// uses.
//
// Fourteen of thirty-eight keys were translated into English and rendered
// nowhere: the cart, the checkout and the product page's buy panel were still
// hard-coded Chinese, so an English visitor got a half-English site on the
// buying mainline — which is the exact failure the locale work was for.
//
// The reverse direction cannot fail: keys are typed identifiers, so a template
// naming one that does not exist will not compile. This is the direction that
// can rot quietly.
func TestEveryKeyIsRendered(t *testing.T) {
	t.Parallel()

	used := sourcesOutsideThisPackage(t)
	for name := range declaredKeys(t) {
		if !strings.Contains(used, name) {
			t.Errorf("i18n.%s is translated in every locale and rendered by nothing — "+
				"use it or delete it", name)
		}
	}
}

// declaredKeys is every Key this package declares.
//
// It reads VAR declarations, because a key and its translations are now one
// declaration and a var is what that has to be. The parser matched CONST until
// the catalogue changed shape, and it FAILED rather than passing on an empty
// set — which is the only reason this comment is being written by somebody who
// noticed.
func declaredKeys(t *testing.T) map[string]struct{} {
	t.Helper()
	fset := token.NewFileSet()
	pkgFiles, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list this package: %v", err)
	}

	out := map[string]struct{}{}
	for _, path := range pkgFiles {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
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
				for _, id := range vs.Names {
					if strings.HasPrefix(id.Name, "Key") {
						out[id.Name] = struct{}{}
					}
				}
			}
			return true
		})
	}
	if len(out) < 20 {
		t.Fatalf("found %d keys, want far more — the parser stopped matching", len(out))
	}
	return out
}

// sourcesOutsideThisPackage is every Go and templ file that could render a key.
//
// Generated *_templ.go files are skipped: they are the .templ files compiled,
// so counting both would let a key survive on the strength of stale output.
func sourcesOutsideThisPackage(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "i18n" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_templ.go") ||
			(!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".templ")) {
			return nil
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // G304: paths come from walking this repository
		if readErr != nil {
			return readErr
		}
		b.Write(src)
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}
	return b.String()
}
