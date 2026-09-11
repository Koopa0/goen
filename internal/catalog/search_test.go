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

func searchPage(t *testing.T, q string) string {
	t.Helper()
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	target := "/search?q=" + url.QueryEscape(q)
	req := httptest.NewRequestWithContext(
		i18n.WithLocale(t.Context(), i18n.En),
		http.MethodGet, target, http.NoBody)
	res := httptest.NewRecorder()
	h.Search(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", res.Code, res.Body.String())
	}
	return res.Body.String()
}
