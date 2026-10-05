//go:build integration

package products_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/i18n"
)

func TestAMalformedVariantSKUIsRefusedOnItsFieldAndNothingIsWritten(t *testing.T) {
	staffCtx, _ := admintest.StaffContext(t, pool)
	ctx := i18n.WithLocale(staffCtx, i18n.En)
	s := products.NewStore(pool)
	slug := admintest.DraftProduct(t, ctx, pool, s)
	mux := http.NewServeMux()
	admintest.ProductDesk(pool, s).Routes(mux, admintest.BackOffice)

	add := func(sku string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"sku": {sku}, "price": {"1000"}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/products/"+slug+"/variants", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		return res
	}
	variantCount := func() int {
		t.Helper()
		view, err := s.Product(ctx, slug)
		if err != nil {
			t.Fatal(err)
		}
		return len(view.Variants)
	}

	for _, sku := range []string{"TEE_RED", "TEE RED M"} {
		res := add(sku)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("SKU %q answered %d, want 422", sku, res.Code)
		}
		body := res.Body.String()
		admintest.AssertRefusedInput(t, body, "v-sku", sku)
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyFormSKUFormat)) {
			t.Errorf("SKU %q: the format sentence is absent", sku)
		}
		if n := variantCount(); n != 0 {
			t.Fatalf("SKU %q left %d variants, want 0", sku, n)
		}
	}

	if res := add("TEE-RED"); res.Code != http.StatusSeeOther {
		t.Fatalf("valid SKU answered %d, want 303", res.Code)
	}
	res := add("TEE-RED")
	if res.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyFormSKUTaken)) {
		t.Errorf("taken SKU answered %d, want 422 with the taken sentence", res.Code)
	}
	if n := variantCount(); n != 1 {
		t.Errorf("variants = %d, want 1", n)
	}
}
