//go:build integration

package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestHandler_Suggest holds what the search field lists as the shopper types:
// the search page's own first products, at most six, each a link to its page,
// and the way to all of them with their number; blank words list nothing, and
// words that find nothing say so.
func TestHandler_Suggest(t *testing.T) {
	h := catalog.NewHandler(catalog.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search/suggest", h.Suggest)
	suggest := func(q string) string {
		t.Helper()
		req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), i18n.ZhHant), http.MethodGet, "/search/suggest?q="+q, http.NoBody)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET /search/suggest?q=%s = %d, want 200", q, res.Code)
		}
		return res.Body.String()
	}
	option := regexp.MustCompile(`<li class="goen-suggest__item" id="site-search-option-\d+" role="option" aria-selected="false"><a class="goen-suggest__link" href="/p/([a-z0-9-]+)"`)
	slugs := func(body string) []string {
		matches := option.FindAllStringSubmatch(body, -1)
		out := make([]string, 0, len(matches))
		for _, m := range matches {
			out = append(out, m[1])
		}
		return out
	}

	// Products of the test's own, named with a token nothing else in the
	// package's database carries: other tests add products of their own, and a
	// word the seed shares with them would count theirs too.
	named := func(token string, n int) []string {
		t.Helper()
		for range n {
			ordertest.VariantsOf(t, pool, token, 1)
		}
		rows, err := pool.Query(t.Context(), `SELECT slug FROM products WHERE name LIKE $1 || '-%' ORDER BY slug`, token)
		if err != nil {
			t.Fatalf("read the %s products: %v", token, err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil || len(got) != n {
			t.Fatalf("the %s products are %v (%v), want %d", token, got, err, n)
		}
		return got
	}
	few, many := "sgstfew"+strings.ToLower(uuid.NewString()[:6]), "sgstmany"+strings.ToLower(uuid.NewString()[:6])
	want := named(few, 3)
	named(many, 7)

	body := suggest(few)
	got := slugs(body)
	slices.Sort(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("three products list %v, want %v", got, want)
	}
	if all := `<a class="goen-suggest__all" href="/search?q=` + few + `">查看全部 3 件商品</a>`; !strings.Contains(body, all) {
		t.Errorf("three products: no way to every result %q in %s", all, body)
	}
	body = suggest(many)
	if n := len(slugs(body)); n != 6 {
		t.Errorf("seven products list %d, want the first 6", n)
	}
	if all := `<a class="goen-suggest__all" href="/search?q=` + many + `">查看全部 7 件商品</a>`; !strings.Contains(body, all) {
		t.Errorf("seven products: no way to every result %q in %s", all, body)
	}

	blank := suggest("%20%E3%80%80")
	if got := slugs(blank); len(got) != 0 || strings.Contains(blank, "goen-suggest__all") || strings.Contains(blank, "goen-suggest__none") {
		t.Errorf("blank words list or say something: %s", blank)
	}
	if !strings.Contains(blank, `<ul id="site-search-list" class="goen-suggest__list" role="listbox"`) {
		t.Errorf("blank words drop the list the field controls: %s", blank)
	}

	none := suggest("zzqqxx")
	if len(slugs(none)) != 0 || !strings.Contains(none, `<p class="goen-suggest__none">找不到「zzqqxx」</p>`) {
		t.Errorf("words that find nothing do not say so: %s", none)
	}
}
