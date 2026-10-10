package reports

import (
	"bytes"
	"encoding/csv"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/access/accesstest"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

func TestOrdersExportMonthUsesTheShopCalendar(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, month, from, to string }{
		{"leap February", "2024-02", "2024-01-31T16:00:00Z", "2024-02-29T16:00:00Z"},
		{"year boundary", "2024-12", "2024-11-30T16:00:00Z", "2024-12-31T16:00:00Z"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, valid := exportMonth(tt.month)
			if !valid {
				t.Fatal("exportMonth() rejected a valid month")
			}
			if got := p.from.UTC().Format(time.RFC3339); got != tt.from {
				t.Errorf("exportMonth(%q) from = %q, want %q", tt.month, got, tt.from)
			}
			if got := p.to.UTC().Format(time.RFC3339); got != tt.to {
				t.Errorf("exportMonth(%q) to = %q, want %q", tt.month, got, tt.to)
			}
		})
	}
}

func TestOrdersExportRejectsInvalidMonthsBeforeReading(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"", "month=", "month=2024-2", "month=0000-01", "month=2024-13", "month=2024-00", "month=2024-02-01", "month=2024-02&month=2024-03", "month=2024-02%0A"} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			(&Handler{}).ordersCSV(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/reports/orders.csv?"+query, nil))
			if w.Code != http.StatusBadRequest {
				t.Errorf("ordersCSV(%q) status = %d, want 400", query, w.Code)
			}
			if strings.HasPrefix(w.Body.String(), "\xEF\xBB\xBF") {
				t.Error("invalid month received a CSV attachment")
			}
		})
	}
}

func TestOrdersExportUsesTheStaffAndSecondFactorGuards(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *access.Control) {
		NewHandler(&Store{}, log).Routes(mux, ac)
	}, "GET /admin/reports/orders.csv?month=2024-02")
	mux := http.NewServeMux()
	NewHandler(&Store{}, log).Routes(mux, access.New(log, func(*http.Request) (bool, error) { return false, nil }))
	ctx := user.NewContext(t.Context(), user.User{ID: "staff", Role: user.RoleStaff})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/reports/orders.csv?month=2024-02", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/verify" {
		t.Errorf("unverified staff export = %d %q, want 303 /admin/verify", w.Code, w.Header().Get("Location"))
	}
}

func TestOrdersCSVKeepsChineseHeadersBOMAndIntegerCents(t *testing.T) {
	t.Parallel()
	placed := time.Date(2024, 2, 29, 15, 59, 59, 0, time.UTC)
	paid := time.Date(2024, 2, 29, 16, 0, 1, 0, time.UTC)
	header := []string{"訂單編號", "下單時間（台灣時間）", "付款時間（台灣時間）", "訂單總額_cents", "折扣_cents", "運費_cents", "購物金折抵_cents", "信用卡金額_cents", "發票號碼"}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, empty := range []bool{false, true} {
			t.Run(locale.Tag()+"/"+map[bool]string{false: "orders", true: "empty"}[empty], func(t *testing.T) {
				t.Parallel()
				rows := []db.OrdersExportBetweenRow{{OrderNumber: "GO-240229-000001", PlacedAt: placed, PaidAt: paid, TotalCents: 195001, DiscountCents: 10000, ShippingCents: 5001, CreditCents: 50000, CardCents: 145001, InvoiceNumber: "AB12345678"}}
				want := [][]string{header, {"GO-240229-000001", "2024-02-29 23:59:59", "2024-03-01 00:00:01", "195001", "10000", "5001", "50000", "145001", "AB12345678"}}
				if empty {
					rows = nil
					want = want[:1]
				}
				var body bytes.Buffer
				if err := writeOrdersCSV(&body, i18n.WithLocale(t.Context(), locale), rows); err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(body.String(), "\xEF\xBB\xBF") {
					t.Fatal("orders CSV is missing its UTF-8 BOM")
				}
				if !strings.HasSuffix(body.String(), "\r\n") {
					t.Error("orders CSV does not end with CRLF")
				}
				got, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body.String(), "\xEF\xBB\xBF"))).ReadAll()
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("orders CSV mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}

var errCSVWrite = errors.New("CSV write refused")

type refusedCSVWriter struct{}

func (refusedCSVWriter) Write([]byte) (int, error) { return 0, errCSVWrite }

func TestOrdersCSVReturnsWriterFailure(t *testing.T) {
	t.Parallel()
	if err := writeOrdersCSV(refusedCSVWriter{}, t.Context(), nil); !errors.Is(err, errCSVWrite) {
		t.Errorf("writeOrdersCSV() = %v, want writer failure", err)
	}
}

func FuzzOrdersExportMonth(f *testing.F) {
	for _, value := range []string{"2024-02", "2024-12", "", "0000-01", "2024-13", "2024-02\x00"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		p, valid := exportMonth(value)
		if valid && (p.from.Day() != 1 || !p.to.After(p.from) || p.from.Format("2006-01") != value) {
			t.Fatalf("exportMonth(%q) accepted invalid bounds %+v", value, p)
		}
	})
}
