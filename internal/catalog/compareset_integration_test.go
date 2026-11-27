//go:build integration

package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
)

// TestCompareSetAgainstCatalogue holds the compare set's writes and its one
// read against the dev catalogue: an active product is added and a slug that
// names none is not; htmx is answered with the control pressed and the
// header's count; and /compare without ?p= shows the kept products and sets no
// cookie of its own.
func TestCompareSetAgainstCatalogue(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	log := slog.New(slog.DiscardHandler)
	store := catalog.NewStore(pool, log)
	set := catalog.NewCompareSet(store, log, false)

	post := func(cookie string, htmx bool, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/compare/items", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "goen_compare", Value: cookie, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		}
		rec := httptest.NewRecorder()
		set.WithCount(http.HandlerFunc(set.Update)).ServeHTTP(rec, req)
		return rec
	}
	written := func(rec *httptest.ResponseRecorder) (string, bool) {
		for _, c := range rec.Result().Cookies() {
			if c.Name == "goen_compare" {
				return c.Value, true
			}
		}
		return "", false
	}

	added := post("pixelight-9", false, url.Values{"slug": {"koto-over-ear"}, "action": {"add"}, "return": {"/c/audio"}})
	if value, ok := written(added); !ok || value != "pixelight-9.koto-over-ear" || added.Code != http.StatusSeeOther || added.Header().Get("Location") != "/c/audio" {
		t.Fatalf("adding an active product answered %d to %q with the set %q (written %v), want 303 to /c/audio with both",
			added.Code, added.Header().Get("Location"), value, ok)
	}
	if _, ok := written(post("pixelight-9", false, url.Values{"slug": {"no-such-product"}, "action": {"add"}})); ok {
		t.Error("a slug that names no product was written to the set")
	}

	swapped := post("pixelight-9", true, url.Values{"slug": {"koto-over-ear"}, "action": {"add"}, "return": {"/c/audio"}})
	body := swapped.Body.String()
	for _, want := range []string{
		`<button class="goen-comparetoggle__button" type="submit" aria-pressed="true">`,
		`name="action" value="remove"`,
		`name="return" value="/c/audio"`,
		`id="compare-link"`,
		`<span class="goen-header__comparecount">2</span>`,
		`id="compare-link-menu"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("htmx's answer to an add lacks %s", want)
		}
	}
	if swapped.Code != http.StatusOK {
		t.Errorf("htmx's answer is %d, want 200", swapped.Code)
	}
	if strings.Contains(body, "比較已滿") {
		t.Error("htmx's answer to an add the set took says the set is full")
	}

	// A full set refuses the add, writes nothing, and its answer says why
	// beside a button still unpressed; the count stays.
	const full = "pixelight-9.pixelight-9-pro.aurora-edge-7.koto-over-ear.nimbus-buds-pro"
	refused := post(full, true, url.Values{"slug": {"meridian-book-14"}, "action": {"add"}, "return": {"/c/laptops"}})
	if _, ok := written(refused); ok {
		t.Error("a full set wrote the set")
	}
	for _, want := range []string{
		`<button class="goen-comparetoggle__button" type="submit" aria-pressed="false">`,
		`<span class="goen-comparetoggle__note" role="status">比較已滿 5 個，請先移除一個再加入。</span>`,
		`<span class="goen-header__comparecount">5</span>`,
	} {
		if !strings.Contains(refused.Body.String(), want) {
			t.Errorf("htmx's answer to an add a full set refused lacks %s", want)
		}
	}

	h := catalog.NewHandler(store, log, "https://goen.example")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/compare", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "goen_compare", Value: "pixelight-9.koto-over-ear", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	page := httptest.NewRecorder()
	set.WithCount(http.HandlerFunc(h.Compare)).ServeHTTP(page, req)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /compare with a set answered %d", page.Code)
	}
	if _, ok := written(page); ok {
		t.Error("GET /compare wrote the set")
	}
	kept := page.Body.String()
	for _, want := range []string{
		`class="goen-compare__table"`,
		`<input type="hidden" name="slug" value="koto-over-ear"> <input type="hidden" name="action" value="remove">`,
		`<span class="goen-header__comparecount">2</span>`,
	} {
		if !strings.Contains(kept, want) {
			t.Errorf("the kept set's page lacks %s", want)
		}
	}
	if strings.Contains(kept, `href="/compare?p=koto-over-ear"`) {
		t.Error("the kept set's page removes by a link to another address")
	}
}
