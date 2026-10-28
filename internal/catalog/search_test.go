package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// readCountDB fails every statement and counts them, so a test sees how many
// reads a handler issued.
type readCountDB struct{ reads atomic.Int32 }

var errReadCountDB = errors.New("read refused")

func (d *readCountDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	d.reads.Add(1)
	return pgconn.CommandTag{}, errReadCountDB
}

func (d *readCountDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	d.reads.Add(1)
	return nil, errReadCountDB
}

func (d *readCountDB) QueryRow(context.Context, string, ...any) pgx.Row {
	d.reads.Add(1)
	return errRow{errReadCountDB}
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// TestHandler_SearchWithoutQueryReadsNewest holds that a search with nothing
// typed offers the departments and one read of the newest products.
//
// The page used to end after a single sentence. It reads the newest products
// once, as the page for a search that matched nothing does, and still answers
// when that read fails.
func TestHandler_SearchWithoutQueryReadsNewest(t *testing.T) {
	t.Parallel()
	for _, q := range []string{"", "   ", "\u3000"} {
		t.Run(fmt.Sprintf("%q", q), func(t *testing.T) {
			t.Parallel()
			conn := &readCountDB{}
			log := slog.New(slog.DiscardHandler)
			h := NewHandler(NewStore(conn, log), log, "https://goen.example")
			ctx := frame.WithTopNav(i18n.WithLocale(t.Context(), i18n.En), []frame.NavItem{{Slug: "tech", Name: "3C", Href: "/c/tech"}})
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/search?q="+url.QueryEscape(q), http.NoBody)
			res := httptest.NewRecorder()

			h.Search(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", res.Code)
			}
			if got := conn.reads.Load(); got != 1 {
				t.Errorf("reads = %d, want 1: the newest products", got)
			}
			if !strings.Contains(res.Body.String(), `<a class="goen-pagehead__chip" href="/c/tech">3C</a>`) {
				t.Error("the search page with nothing typed offers no department")
			}
		})
	}
}

// TestSearchPromptNext holds that the page asking what to look for also shows
// what to look at.
func TestSearchPromptNext(t *testing.T) {
	t.Parallel()
	ctx := frame.WithTopNav(i18n.WithLocale(t.Context(), i18n.En), []frame.NavItem{{Slug: "tech", Name: "3C", Href: "/c/tech"}})
	newest := shelf(2)
	html := renderComponent(t, ctx, Search(frame.Page{}, SearchView{Newest: newest}))
	for _, want := range []string{
		`<a class="goen-pagehead__chip" href="/c/tech">3C</a>`,
		`<h2 class="goen-listing__subhead">Newest products</h2>`,
		newest[0].Name,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the prompt page lacks %q:\n%s", want, html)
		}
	}
}

// TestHandler_SearchHeaderBlank holds that the search page leaves the header
// input blank without a query.
func TestHandler_SearchHeaderBlank(t *testing.T) {
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
	h := &Handler{store: &Store{q: db.New(&readCountDB{})}, log: slog.New(slog.DiscardHandler)}
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

// TestSearchPatternUnicodeSpace holds that input of Unicode whitespace only is
// empty for both the pattern and the display.
func TestSearchPatternUnicodeSpace(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	prompt := i18n.T(ctx, i18n.KeySearchPrompt)

	for _, q := range []string{"\u00a0", "\u2003", "\u3000", "\u00a0\u2003\u3000"} {
		t.Run(fmt.Sprintf("%q", q), func(t *testing.T) {
			if got := SearchPattern(q); got != "" {
				t.Errorf("SearchPattern(%q) = %q, want empty", q, got)
			}
			view := SearchView{Query: trimForDisplay(q)}
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

// TestSearchPatternEdgeSpace holds that a term wrapped in Unicode space is kept
// and then capped in runes.
func TestSearchPatternEdgeSpace(t *testing.T) {
	q := "\u00a0\u3000pixel\u2003"
	view := SearchView{Query: trimForDisplay(q)}
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
	capped := SearchView{Query: trimForDisplay(over)}
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

// TestSearchPatternEscapesTypedWildcards: a typed _ or % reaches the pattern
// escaped, between goen's own wildcards.
func TestSearchPatternEscapesTypedWildcards(t *testing.T) {
	if got := SearchPattern("_"); got != `%\_%` {
		t.Errorf("SearchPattern(%q) = %q, want the escaped term between goen's own wildcards", "_", got)
	}
}

// TestSearchPatternTerms holds that SearchPattern splits on whitespace and
// bounds the terms.
func TestSearchPatternTerms(t *testing.T) {
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

// TestSearchPatternFullWidth holds that SearchPattern folds full-width letters
// and digits.
func TestSearchPatternFullWidth(t *testing.T) {
	if got := SearchPattern("Ｐｉｘｅｌ６５Ｗ"); got != "%Pixel65W%" {
		t.Errorf("SearchPattern = %q, want the folded term", got)
	}
	if got := trimForDisplay("　Ｐｉｘｅｌ　"); got != "Pixel" {
		t.Errorf("trimForDisplay = %q, want the folded query the page echoes", got)
	}
}
