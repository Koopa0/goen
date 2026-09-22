package admin

import (
	"encoding/csv"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestBestSellerCSVPreservesTextAndMoney(t *testing.T) {
	view := &pages.AdminReportView{Days: 7, Sellers: []pages.AdminSeller{
		{Slug: "phone", Name: "Phone, \"Pro\"\nmodel", Brand: "品牌", Units: 3, RevenueCents: 123456},
		{Slug: "formula", Name: " =HYPERLINK(\"example\")", Brand: "+sum", Units: 1, RevenueCents: 100},
	}}
	body, err := bestSellerCSV(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "\xef\xbb\xbf") {
		t.Fatal("CSV lacks its UTF-8 marker")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\xef\xbb\xbf"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[1][2] != view.Sellers[0].Name || rows[1][3] != "品牌" || rows[1][5] != "123456" {
		t.Fatalf("CSV lost text or integer cents: %q", rows)
	}
	if rows[2][2] != "'"+view.Sellers[1].Name || rows[2][3] != "'+sum" {
		t.Fatalf("CSV exposes a spreadsheet formula: %q", rows[2])
	}
	if rows[0][5] != "gross_merchandise_cents" || rows[1][0] != "7" {
		t.Fatalf("CSV mislabels scope: %q", rows)
	}
}

func TestBestSellerCSVEmptyWindowKeepsColumns(t *testing.T) {
	body, err := bestSellerCSV(&pages.AdminReportView{Days: 30})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\xef\xbb\xbf"))).ReadAll()
	if err != nil || len(rows) != 1 || len(rows[0]) != 6 {
		t.Fatalf("empty CSV rows=%q err=%v", rows, err)
	}
}

func TestReportCSVRequiresStaffAndStepUp(t *testing.T) {
	for _, role := range []string{"", "customer", "staff"} {
		t.Run(role, func(t *testing.T) {
			h := &Handler{log: slog.New(slog.DiscardHandler), stepUp: func(*http.Request) (bool, error) { return false, nil }}
			ctx := t.Context()
			if role != "" {
				ctx = account.WithUser(ctx, account.User{ID: "fixture", Role: role})
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/reports/export.csv?days=7", nil)
			rr := httptest.NewRecorder()
			h.RequireStaff(h.ReportCSV)(rr, req)
			want := http.StatusNotFound
			if role == "staff" {
				want = http.StatusSeeOther
			}
			if rr.Code != want || strings.HasPrefix(rr.Header().Get("Content-Type"), "text/csv") {
				t.Fatalf("role %q got %d %s", role, rr.Code, rr.Header().Get("Content-Type"))
			}
		})
	}
}
