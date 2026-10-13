//go:build integration

package buyer_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/returns/buyer"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestReturnFormReadsTheDeliveredParcelsDatabaseDeadlineAndOnlyTheOpenNotice(t *testing.T) {
	ctx := t.Context()
	number, lineID := shippedOrder(t, 2, 2)
	delivered := time.Date(2026, time.October, 1, 16, 30, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE order_shipments SET shipped_at=$2::timestamptz-interval '1 day', delivered_at=$2 WHERE order_id=(SELECT id FROM orders WHERE order_number=$1)`, number, delivered); err != nil {
		t.Fatal(err)
	}
	var expected time.Time
	if err := pool.QueryRow(ctx, `SELECT return_window_ends($1::timestamptz)::date`, delivered).Scan(&expected); err != nil {
		t.Fatal(err)
	}
	appPool := returnsApplicationPool(t, "return-next-step")
	store := buyer.NewStore(appPool)
	order, err := store.Order(ctx, number)
	if err != nil {
		t.Fatal(err)
	}
	if len(order.Shipments) != 1 || !order.Shipments[0].RescissionEnds.Equal(expected) {
		t.Fatalf("return form parcels = %+v, want the existing database deadline %v", order.Shipments, expected)
	}
	handler := buyer.NewHandler(store, access.NewStore(appPool, false), slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{number}/return", handler.Page)
	mux.HandleFunc("POST /orders/{number}/return", handler.Submit)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		local := i18n.WithLocale(ctx, locale)
		request := httptest.NewRequestWithContext(local, http.MethodGet, "/orders/"+number+"/return", http.NoBody)
		placedBy(t, request, number)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		orderPage := pages.OrderView{Now: order.Now}
		date := orderPage.RescissionLastDay(local, order.Shipments[0])
		body := html.UnescapeString(response.Body.String())
		if response.Code != http.StatusOK || !strings.Contains(body, date) || !strings.Contains(body, i18n.T(local, i18n.KeyReturnSub)) {
			t.Errorf("return lead locale %s status = %d, missing deadline or next step", locale, response.Code)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE order_shipments SET delivered_at=now() WHERE order_id=(SELECT id FROM orders WHERE order_number=$1)`, number); err != nil {
		t.Fatal(err)
	}
	if err := store.Open(ctx, number, uuid.NullUUID{}, &buyer.Request{Lines: map[string]int32{lineID.String(): 2}}); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		local := i18n.WithLocale(ctx, locale)
		form := url.Values{"qty_" + lineID.String(): {"2"}, "reason": {"Keep this reason"}}
		request := httptest.NewRequestWithContext(local, http.MethodPost, "/orders/"+number+"/return", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		placedBy(t, request, number)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		body := html.UnescapeString(response.Body.String())
		if response.Code != http.StatusUnprocessableEntity || strings.Count(body, i18n.T(local, i18n.KeyReturnInFlight)) != 1 || strings.Contains(body, i18n.T(local, i18n.KeyReturnTooMany)) || strings.Contains(body, `id="return-form-error"`) {
			t.Errorf("open request locale %s status = %d, wrong refusal", locale, response.Code)
		}
		if !strings.Contains(body, `value="2"`) || !strings.Contains(body, "Keep this reason") {
			t.Error("open-request422 lost submitted input")
		}
	}
}

func TestReturnFormTellsAPickupOrderToCountFromCollection(t *testing.T) {
	ctx := t.Context()
	home, _ := shippedOrder(t, 1, 1)
	pickup, _ := shippedOrder(t, 1, 1)
	if _, err := pool.Exec(ctx, `
		UPDATE order_private_data
		SET postal_code = NULL, city = NULL, district = NULL, street = NULL, pickup_chain = 'seven_eleven'
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, pickup); err != nil {
		t.Fatal(err)
	}
	appPool := returnsApplicationPool(t, "return-pickup")
	store := buyer.NewStore(appPool)
	handler := buyer.NewHandler(store, access.NewStore(appPool, false), slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{number}/return", handler.Page)
	for _, tc := range []struct {
		number string
		key    i18n.Key
	}{
		{home, i18n.KeyOrderRescissionAwaits},
		{pickup, i18n.KeyOrderRescissionAwaitsPickup},
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			local := i18n.WithLocale(ctx, locale)
			request := httptest.NewRequestWithContext(local, http.MethodGet, "/orders/"+tc.number+"/return", http.NoBody)
			placedBy(t, request, tc.number)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			sentence := i18n.Count(local, tc.key, pages.RescissionDays, pages.RescissionDays)
			if response.Code != http.StatusOK || !strings.Contains(html.UnescapeString(response.Body.String()), sentence) {
				t.Errorf("order %s locale %s status = %d, missing %q", tc.number, locale, response.Code, sentence)
			}
		}
	}
}
