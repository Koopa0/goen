package catalog

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/siteurl"
)

// TestParseCompareSet holds that a cookie reads as the slugs it can, in order,
// each once and at most MaxCompare, and never as an error: the browser sends
// whatever it holds, a hand-edited value included.
func TestParseCompareSet(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, value string
		want        []string
	}{
		{"empty", "", nil},
		{"two", "pixelight-9.koto-over-ear", []string{"pixelight-9", "koto-over-ear"}},
		{"repeated", "a.b.a", []string{"a", "b"}},
		{"not slugs", "A.b..c/d.-e.f", []string{"b", "f"}},
		{"past the cap", "a.b.c.d.e.f.g", []string{"a", "b", "c", "d", "e"}},
	} {
		if got := parseCompareSet(tt.value); !slices.Equal(got, tt.want) {
			t.Errorf("%s: parseCompareSet(%q) = %q, want %q", tt.name, tt.value, got, tt.want)
		}
	}
}

// FuzzParseCompareSet holds what any cookie value reads as: valid slugs only,
// none twice, at most MaxCompare, and the same set again once written back.
func FuzzParseCompareSet(f *testing.F) {
	for _, seed := range []string{"", "a.b", "a..b", "A.b", "a.b.c.d.e.f", "a.a", "\x00.a", strings.Repeat("a", 200)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		got := parseCompareSet(value)
		if len(got) > MaxCompare {
			t.Fatalf("parseCompareSet(%q) holds %d slugs, more than %d", value, len(got), MaxCompare)
		}
		for i, s := range got {
			if !siteurl.ValidSlug(s) || len(s) > 120 {
				t.Fatalf("parseCompareSet(%q) kept %q, which is not a slug", value, s)
			}
			if slices.Contains(got[:i], s) {
				t.Fatalf("parseCompareSet(%q) holds %q twice", value, s)
			}
		}
		if again := parseCompareSet(strings.Join(got, compareSetJoin)); !slices.Equal(again, got) {
			t.Fatalf("written back, %v reads as %v", got, again)
		}
	})
}

// TestChangeCompareSet holds the one press: add appends an offered product that
// is not there while there is room, remove takes it out, and a full set is
// never made room in by dropping a product the shopper chose.
func TestChangeCompareSet(t *testing.T) {
	t.Parallel()
	full := []string{"a", "b", "c", "d", "e"}
	for _, tt := range []struct {
		name            string
		set             []string
		slug            string
		remove, offered bool
		want            []string
	}{
		{"add to none", nil, "a", false, true, []string{"a"}},
		{"add at the end", []string{"a", "b"}, "c", false, true, []string{"a", "b", "c"}},
		{"add what is there", []string{"a", "b"}, "a", false, true, []string{"a", "b"}},
		{"add what is not offered", []string{"a"}, "x", false, false, []string{"a"}},
		{"add to a full set", full, "f", false, true, full},
		{"remove", []string{"a", "b", "c"}, "b", true, false, []string{"a", "c"}},
		{"remove what is not there", []string{"a"}, "b", true, false, []string{"a"}},
		{"remove from a full set", full, "e", true, false, []string{"a", "b", "c", "d"}},
	} {
		before := slices.Clone(tt.set)
		got := changeCompareSet(tt.set, tt.slug, tt.remove, tt.offered)
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: changeCompareSet(%v, %q) = %v, want %v", tt.name, tt.set, tt.slug, got, tt.want)
		}
		if !slices.Equal(tt.set, before) {
			t.Errorf("%s: the set passed in was changed to %v", tt.name, tt.set)
		}
	}
}

// postCompare sends the set's form as a browser would, carrying the cookie.
func postCompare(t *testing.T, secure bool, cookie string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), i18n.ZhHant),
		http.MethodPost, "/compare/items", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: compareCookieName(secure), Value: cookie, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	}
	rec := httptest.NewRecorder()
	// Removing never reads the catalogue, so these paths run without a store.
	(&CompareSet{log: slog.New(slog.DiscardHandler), secure: secure}).Update(rec, req)
	return rec
}

// TestCompareSetRemove holds that removing rewrites the cookie without the
// product and goes back where the form said, a same-site path only, and that
// a set emptied is a cookie expired.
func TestCompareSetRemove(t *testing.T) {
	t.Parallel()

	rec := postCompare(t, false, "a.b.c", url.Values{"slug": {"b"}, "action": {"remove"}, "return": {"/c/phones?sort=price"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/c/phones?sort=price" {
		t.Fatalf("remove answered %d to %q, want 303 to /c/phones?sort=price", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "goen_compare" || cookies[0].Value != "a.c" || cookies[0].MaxAge <= 0 {
		t.Fatalf("remove set %v, want goen_compare=a.c that lasts", cookies)
	}

	last := postCompare(t, true, "a", url.Values{"slug": {"a"}, "action": {"remove"}, "return": {"https://evil.example/"}})
	if got := last.Header().Get("Location"); got != "/compare" {
		t.Errorf("a return off the site sent the shopper to %q, want /compare", got)
	}
	cookies = last.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-goen_compare" || cookies[0].MaxAge >= 0 || !cookies[0].Secure {
		t.Errorf("emptying the set sent %v, want __Host-goen_compare expired with Secure", cookies)
	}
}

// TestCompareSetRefusesNonSlug holds that a posted slug that is not a slug
// changes nothing and still goes back.
func TestCompareSetRefusesNonSlug(t *testing.T) {
	t.Parallel()
	for _, slug := range []string{"", "../admin", "A", "a.b"} {
		rec := postCompare(t, false, "a", url.Values{"slug": {slug}, "action": {"remove"}, "return": {"/compare"}})
		if rec.Code != http.StatusSeeOther || len(rec.Result().Cookies()) != 0 {
			t.Errorf("slug %q: answered %d with cookies %v, want a 303 that sets none", slug, rec.Code, rec.Result().Cookies())
		}
	}
}

// TestCompareSetWithCount holds that the middleware puts the set's size where
// the header reads it and the set where a toggle asks, and leaves a request
// without the cookie as it was.
func TestCompareSetWithCount(t *testing.T) {
	t.Parallel()
	c := &CompareSet{log: slog.New(slog.DiscardHandler)}
	for _, tt := range []struct {
		name, cookie string
		want         int
		in, out      string
	}{
		{"none", "", 0, "", "a"},
		{"two", "a.b", 2, "b", "c"},
		{"hand-edited", "a.a.B.c", 2, "c", "B"},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/c/phones", http.NoBody)
		if tt.cookie != "" {
			req.AddCookie(&http.Cookie{Name: "goen_compare", Value: tt.cookie, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		}
		var count int
		var in, out bool
		c.WithCount(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			count = frame.CompareCount(r.Context())
			in, out = inCompareSet(r.Context(), tt.in), inCompareSet(r.Context(), tt.out)
		})).ServeHTTP(httptest.NewRecorder(), req)
		if count != tt.want {
			t.Errorf("%s: CompareCount = %d, want %d", tt.name, count, tt.want)
		}
		if tt.in != "" && !in {
			t.Errorf("%s: inCompareSet(%q) = false for a product in the set", tt.name, tt.in)
		}
		if out {
			t.Errorf("%s: inCompareSet(%q) = true for a product not in the set", tt.name, tt.out)
		}
	}
}

// TestCompareToggle holds the control a tile or the product page carries: one
// form, posting to the set with and without script, that reads from the
// request whether the product is in the set, says which way it goes by
// aria-pressed under one name, comes back to the page it is on, and says a full
// set is full only where pressing would add.
func TestCompareToggle(t *testing.T) {
	t.Parallel()
	page := frame.WithReturnPath(i18n.WithLocale(t.Context(), i18n.ZhHant), "/c/phones?sort=price")
	note := "比較已滿 5 個"
	for _, tt := range []struct {
		name     string
		set      []string
		pressed  string
		action   string
		wantNote bool
	}{
		{"no set", nil, `aria-pressed="false"`, `name="action" value="add"`, false},
		{"out", []string{"a"}, `aria-pressed="false"`, `name="action" value="add"`, false},
		{"in", []string{"a", "pixelight-9"}, `aria-pressed="true"`, `name="action" value="remove"`, false},
		{"out of a full set", []string{"a", "b", "c", "d", "e"}, `aria-pressed="false"`, `name="action" value="add"`, true},
		{"in a full set", []string{"a", "b", "c", "d", "pixelight-9"}, `aria-pressed="true"`, `name="action" value="remove"`, false},
	} {
		ctx := frame.WithCompareCount(withCompareSet(page, tt.set), len(tt.set))
		got := renderComponent(t, ctx, CompareToggle("pixelight-9", "Pixelight 9"))
		for _, want := range []string{
			`method="post" action="/compare/items" hx-post="/compare/items"`,
			`hx-select-oob="#compare-link,#compare-link-menu"`,
			`name="slug" value="pixelight-9"`,
			`name="return" value="/c/phones?sort=price"`,
			tt.pressed, tt.action,
			`比較 <span class="goen-sr-only"> Pixelight 9</span>`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: the toggle lacks %s: %s", tt.name, want, got)
			}
		}
		if strings.Contains(got, note) != tt.wantNote {
			t.Errorf("%s: says the set is full = %v, want %v", tt.name, !tt.wantNote, tt.wantNote)
		}
		if !strings.Contains(got, `<span class="goen-comparetoggle__note" role="status">`) {
			t.Errorf("%s: no status region is in the page before it speaks", tt.name)
		}
	}
}
