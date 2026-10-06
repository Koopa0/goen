//go:build integration

package stock_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/web"
)

// The days cover rows sit where the desk opens: not on a search, the sold out
// filter or the pages after the first.
func TestStockDeskListsDaysCoverOnlyWhereItOpens(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := handlerOver(stock.NewStore(pool))
	get := func(target string) string {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		admintest.BackOffice.RequireStaff(h.Variants)(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s answered %d, want 200", target, w.Code)
		}
		return w.Body.String()
	}
	if first := get("/admin/stock"); !strings.Contains(first, "goen-admin__cover") {
		t.Error("the first page has no days cover section")
	}
	for _, target := range []string{"/admin/stock?q=x", "/admin/stock?low=1", "/admin/stock?" + web.KeysetParam + "=x"} {
		if page := get(target); strings.Contains(page, "goen-admin__cover") {
			t.Errorf("%s lists the days cover rows", target)
		}
	}
}
