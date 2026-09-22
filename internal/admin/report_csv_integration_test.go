//go:build integration

package admin_test

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin"
)

func TestReportCSVMatchesTheSelectedQueryWindow(t *testing.T) {
	ctx, _ := staffContext(t)
	store := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	for _, age := range []int{0, 10} {
		id := reportOrder(t, 100, false)
		_, err := pool.Exec(ctx, `
            UPDATE order_lines SET variant_id = (SELECT id FROM product_variants ORDER BY id LIMIT 1),
                quantity = 999 WHERE order_id = $1;
        `, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE orders SET placed_at = now() - make_interval(days => $2) WHERE id = $1`, id, age); err != nil {
			t.Fatal(err)
		}
		ref := "csv_" + id.String()
		if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 99900)`, id, ref); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `SELECT capture_payment($1, 99900, NULL, NULL)`, ref); err != nil {
			t.Fatal(err)
		}
	}
	handler := adminHandlerOver(pool, store)
	for _, raw := range []string{"7", "30", "90", "invalid", "31"} {
		days, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			days = 0
		}
		view, err := store.Report(ctx, int32(days))
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Sellers) == 0 {
			t.Fatal("CSV fixture produced no best sellers")
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/reports/export.csv?days="+raw, nil)
		rr := httptest.NewRecorder()
		handler.RequireStaff(handler.ReportCSV)(rr, req)
		if rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("CSV response %d %v", rr.Code, rr.Header())
		}
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(rr.Body.String(), "\xef\xbb\xbf"))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != len(view.Sellers)+1 || len(rows) > admin.MaxReportRows+1 {
			t.Fatalf("window %s row count %d, HTML rows %d", raw, len(rows), len(view.Sellers))
		}
		for i, seller := range view.Sellers {
			row := rows[i+1]
			if row[0] != strconv.Itoa(view.Days) || row[1] != seller.Slug || row[4] != strconv.FormatInt(seller.Units, 10) || row[5] != strconv.FormatInt(seller.RevenueCents, 10) {
				t.Fatalf("window %s row %d differs from HTML: %q", raw, i, row)
			}
		}
	}
}
