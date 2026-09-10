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

func TestEveryKeyIsTranslatedInEveryLocale(t *testing.T) {
	if len(messages) == 0 {
		t.Fatal("the catalogue is empty; this test would pass on nothing")
	}

	for k, m := range messages {
		for _, l := range Locales() {
			if strings.TrimSpace(m.in(l)) == "" {
				t.Errorf("%s has no translation for %q", l, k)
			}
		}
	}
}

func TestADuplicateKeyIsRefused(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("declaring an id twice did not panic; the later entry would " +
				"silently replace the earlier one")
		}
	}()
	key("nav.cart", Message{ZhHant: "第二個", En: "the second one"})
}

// exhaustruct catches the literal that omits a field; this catches "".
func TestAMissingTranslationIsRefused(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a blank translation did not panic")
		}
	}()
	key("test.blank", Message{ZhHant: "有字", En: ""})
}

// englishQuotesCJK is every key whose English text carries CJK on purpose, and why.
var englishQuotesCJK = map[Key]string{
	"about.name": "explains ご縁 and 五円, so it has to print them",
}

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
		// English sits SECOND on purpose: with "zh-CN,zh" the case passes whether
		// zh-CN matched or fell through, because the default is Chinese too.
		{"simplified Chinese, English behind it", "", "zh-CN,en;q=0.8", ZhHant},
		{"simplified Chinese alone", "", "zh-CN", ZhHant},
		{"English then Chinese", "", "en-US,en;q=0.9,zh;q=0.8", En},
		{"Chinese then English", "", "zh-TW;q=0.9,en;q=0.8", ZhHant},
		{"quality outweighs order", "", "zh-TW;q=0.2,en;q=0.9", En},
		{"zero quality is excluded", "", "en;q=0,zh-TW;q=0.5", ZhHant},
		{"wildcard cannot revive an explicit exclusion", "", "zh;q=0,*;q=1", En},
		{"equal quality keeps header order", "", "en;q=0.8,zh-TW;q=0.8", En},
		{"wildcard uses the default", "", "*;q=0.9,en;q=0.8", Default},
		{"language name is not a range", "", "english,zh-TW;q=0.8", ZhHant},
		{"invalid quality is ignored", "", "en;q=wat,zh-TW;q=0.8", ZhHant},
		{"non-finite quality is ignored", "", "en;q=NaN,zh-TW;q=0.8", ZhHant},
		{"a language goen does not speak", "", "fr-FR,fr;q=0.9", Default},
		{"a header that does not parse", "", ";;;q=", Default},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tt.cookie != "" {
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

func TestAMissingKeyIsVisible(t *testing.T) {
	ctx := WithLocale(t.Context(), En)
	if got := T(ctx, Key("does.not.exist")); got != "does.not.exist" {
		t.Errorf("a missing key rendered as %q, want the key itself", got)
	}
}

func TestTheCookieIsHostScopedWhenSecure(t *testing.T) {
	w := httptest.NewRecorder()
	SetCookie(w, En, true)
	got := w.Header().Get("Set-Cookie")

	// An independent literal, not CookieName: asserting the constant against
	// itself passes whatever the constant becomes.
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

	w = httptest.NewRecorder()
	SetCookie(w, En, false)
	if strings.HasPrefix(w.Header().Get("Set-Cookie"), "__Host-") {
		t.Error("the insecure cookie carries a __Host- prefix; it will never come back")
	}
}

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

// sourcesOutsideThisPackage is every Go and templ file that could render a key;
// generated *_templ.go is skipped so a key cannot survive on stale output.
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
