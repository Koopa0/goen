//go:build integration

package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
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

	for _, tc := range []struct {
		name, q string
		want    []string
		all     string
	}{
		{"three", "pixel", []string{"pixelight-9-pro-case", "pixelight-9", "pixelight-9-pro"}, `<a class="goen-suggest__all" href="/search?q=pixel">查看全部 3 件商品</a>`},
		{"capped", "%E8%8C%B6", nil, `<a class="goen-suggest__all" href="/search?q=%E8%8C%B6">查看全部 7 件商品</a>`},
	} {
		body := suggest(tc.q)
		got := slugs(body)
		if tc.want != nil && strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: lists %v, want %v", tc.name, got, tc.want)
		}
		if tc.want == nil && len(got) != 6 {
			t.Errorf("%s: lists %d products, want the first 6", tc.name, len(got))
		}
		if !strings.Contains(body, tc.all) {
			t.Errorf("%s: no way to every result %q in %s", tc.name, tc.all, body)
		}
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
