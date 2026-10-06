//go:build integration

package catalog_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
)

// Checkout drops store pickup where the store map is not configured, so the
// department page's free-delivery note must describe the home delivery it still offers.
func TestTheDepartmentPageStatesOnlyTheDeliveryCheckoutOffers(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	// New versions, because shipping_method_versions is append-only. The fixture names its own fees.
	if _, err := pool.Exec(ctx, `
		INSERT INTO shipping_method_versions
		    (method_id, name, carrier, fee_cents, free_over_cents, effective_at)
		SELECT DISTINCT ON (v.method_id) v.method_id, v.name, v.carrier,
		       CASE sm.destination_kind WHEN 'pickup_point' THEN 6000 ELSE 8000 END,
		       300000, now()
		FROM shipping_method_versions v
		JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE sm.is_active
		ORDER BY v.method_id, v.effective_at DESC`); err != nil {
		t.Fatalf("publish the fees: %v", err)
	}

	render := func(store *catalog.Store, htmx bool) string {
		h := catalog.NewHandler(store, slog.New(slog.DiscardHandler))
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/c/phones", http.NoBody)
		req.SetPathValue("slug", "phones")
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		res := httptest.NewRecorder()
		h.Listing(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200", res.Code)
		}
		return res.Body.String()
	}

	with := render(catalog.NewStore(pool), false)
	if !strings.Contains(with, `class="ui-statline ui-statline--wide"`) {
		t.Fatal("the department page does not end with the shop rules")
	}
	if !strings.Contains(with, "宅配與超商取貨") || !strings.Contains(with, "未達門檻運費 NT$60 起") {
		t.Error("a shop that offers pickup does not state pickup and its NT$60 floor")
	}
	without := render(catalog.NewStore(pool).WithoutPickup(), false)
	if strings.Contains(without, "宅配與超商取貨") {
		t.Error("the department page promises pickup where checkout does not offer it")
	}
	if !strings.Contains(without, "未達門檻運費 NT$80 起") {
		t.Error("the floor is not the home delivery fee")
	}
	if strings.Contains(render(catalog.NewStore(pool), true), "ui-statline") {
		t.Error("a filter swap draws the shop rules")
	}
}
