//go:build integration

package stock_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/web"
)

// The days cover rows sit on the desk's first page only: the cursor pages after
// it are the list.
func TestStockDeskListsDaysCoverOnTheFirstPageOnly(t *testing.T) {
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
	if later := get("/admin/stock?" + web.KeysetParam + "=x"); strings.Contains(later, "goen-admin__cover") {
		t.Error("a later page lists the days cover rows again")
	}
}

func TestDaysCoverReadsAtLeastThirtyShopDays(t *testing.T) {
	ctx := t.Context()
	s := stock.NewStore(pool)
	now := time.Now()
	a, _, err := s.DaysCover(ctx, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.DaysCover(ctx, 30, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Errorf("DaysCover over 7 days lists %d rows and over 30 lists %d; 7 must be read as 30", len(a), len(b))
	}
}
