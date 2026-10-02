package catalog

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestSearchPageLeavesTheHeaderInputBlankWithoutAQuery(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		t.Run(locale.Tag(), func(t *testing.T) {
			body := searchPageInLocale(t, locale, "")
			if got := headerSearchValue(body); got != "" {
				t.Errorf("header search value = %q, want empty without a query", got)
			}
		})
	}
}

func headerSearchValue(html string) string {
	const marker = `id="site-search"`
	i := strings.Index(html, marker)
	if i < 0 {
		return ""
	}
	rest := html[i:]
	const attr = `value="`
	j := strings.Index(rest, attr)
	if j < 0 {
		return ""
	}
	rest = rest[j+len(attr):]
	k := strings.Index(rest, `"`)
	if k < 0 {
		return ""
	}
	return rest[:k]
}

func searchPage(t *testing.T, q string) string {
	t.Helper()
	return searchPageInLocale(t, i18n.En, q)
}

func searchPageInLocale(t *testing.T, locale i18n.Locale, q string) string {
	t.Helper()
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	target := "/search?q=" + url.QueryEscape(q)
	req := httptest.NewRequestWithContext(
		i18n.WithLocale(t.Context(), locale),
		http.MethodGet, target, http.NoBody)
	res := httptest.NewRecorder()
	h.Search(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", res.Code, res.Body.String())
	}
	return res.Body.String()
}

func TestSearchUnicodeWhitespaceIsEmptyForPatternAndDisplay(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	prompt := i18n.T(ctx, i18n.KeySearchPrompt)

	for _, q := range []string{"\u00a0", "\u2003", "\u3000", "\u00a0\u2003\u3000"} {
		t.Run(fmt.Sprintf("%q", q), func(t *testing.T) {
			if got := SearchPattern(q); got != "" {
				t.Errorf("SearchPattern(%q) = %q, want empty", q, got)
			}
			view := pages.SearchView{Query: trimForDisplay(q)}
			if view.Searched() {
				t.Errorf("SearchView.Query = %q; Searched() is true for Unicode-space-only input", view.Query)
			}

			body := searchPage(t, q)
			if !strings.Contains(body, prompt) {
				t.Errorf("GET /search for %q did not render the empty prompt %q", q, prompt)
			}
			if strings.Contains(body, "Nothing found") {
				t.Errorf("GET /search for %q rendered a no-results heading; the query is whitespace-only", q)
			}
		})
	}
}

func TestSearchKeepsATermWrappedInUnicodeSpaceThenCapsRunes(t *testing.T) {
	q := "\u00a0\u3000pixel\u2003"
	view := pages.SearchView{Query: trimForDisplay(q)}
	if view.Query != "pixel" {
		t.Fatalf("trimForDisplay(%q) = %q, want %q", q, view.Query, "pixel")
	}
	if !view.Searched() {
		t.Fatal("SearchView.Searched() is false after edge Unicode spaces around a real term")
	}
	if got := SearchPattern(q); got != "%pixel%" {
		t.Fatalf("SearchPattern(%q) = %q, want %%pixel%%", q, got)
	}

	over := "\u00a0" + strings.Repeat("字", MaxQueryRunes+8) + "\u3000"
	want := strings.Repeat("字", MaxQueryRunes)
	capped := pages.SearchView{Query: trimForDisplay(over)}
	if capped.Query != want {
		t.Fatalf("display after edge trim kept %d runes, want %d", len([]rune(capped.Query)), MaxQueryRunes)
	}
	if !capped.Searched() {
		t.Fatal("a capped real term must still count as searched")
	}
	if got := SearchPattern(over); got != "%"+want+"%" {
		t.Fatalf("SearchPattern after edge trim+cap = %q, want the same %d-rune term", got, MaxQueryRunes)
	}
}

// TestEscapeLikeLeavesNoWildcard: every search pattern goen builds from typed
// words, storefront and back office, escapes LIKE's syntax through this.
func TestEscapeLikeLeavesNoWildcard(t *testing.T) {
	for q, want := range map[string]string{
		"%%":           `\%\%`,
		"a_b@goen.dev": `a\_b@goen.dev`,
		`50\%`:         `50\\\%`,
		"pixel":        "pixel",
	} {
		if got := EscapeLike(q); got != want {
			t.Errorf("EscapeLike(%q) = %q, want %q", q, got, want)
		}
	}
	if got := SearchPattern("_"); got != `%\_%` {
		t.Errorf("SearchPattern(%q) = %q, want the escaped term between goen's own wildcards", "_", got)
	}
}

func TestSearchPatternSplitsOnWhitespaceAndBoundsTheTerms(t *testing.T) {
	tests := []struct {
		name, q, want string
	}{
		{"one term", "aurora", "%aurora%"},
		{"two terms", "aurora 65W", "%aurora% %65W%"},
		{"full-width space", "藍牙　耳機", "%藍牙% %耳機%"},
		{"runs of space", " a \t b\n", "%a% %b%"},
		{"each term escaped", "50% a_b", `%50\%% %a\_b%`},
		{"more terms than the bound", "a b c d e f g", "%a% %b% %c% %d% %e%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SearchPattern(tt.q); got != tt.want {
				t.Errorf("SearchPattern(%q) = %q, want %q", tt.q, got, tt.want)
			}
		})
	}

	terms, exact := SearchTerms(SearchPattern("Aurora 50%"))
	if len(terms) != 2 || terms[0] != "%Aurora%" || terms[1] != `%50\%%` {
		t.Errorf("SearchTerms terms = %q, want one pattern per term", terms)
	}
	if exact != `Aurora 50\%` {
		t.Errorf("SearchTerms exact = %q, want the whole query without goen's wildcards", exact)
	}
}

func TestSearchFoldsFullWidthLettersAndDigits(t *testing.T) {
	if got := SearchPattern("Ｐｉｘｅｌ６５Ｗ"); got != "%Pixel65W%" {
		t.Errorf("SearchPattern = %q, want the folded term", got)
	}
	if got := trimForDisplay("　Ｐｉｘｅｌ　"); got != "Pixel" {
		t.Errorf("trimForDisplay = %q, want the folded query the page echoes", got)
	}
}
