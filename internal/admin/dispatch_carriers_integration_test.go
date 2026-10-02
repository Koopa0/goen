//go:build integration

package admin_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
)

// The dispatch form lists what the order can use and selects what it implies:
// a store order's chain carries it, and a home delivery names no carrier code.
func TestTheOrderPageCarriesTheCarriersTheDispatchFormOffers(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)

	store, err := s.Order(ctx, pickupOrderForCorrection(t))
	if err != nil {
		t.Fatalf("read store order: %v", err)
	}
	if want := []carrier.Carrier{carrier.FamilyMart}; !slices.Equal(store.ShipCarriers, want) ||
		store.ShipCarrier != string(carrier.FamilyMart) {
		t.Errorf("a family_mart order lists %v with %q chosen, want only %v chosen", store.ShipCarriers, store.ShipCarrier, want)
	}

	home, err := s.Order(ctx, placeUnpaidOrder(t))
	if err != nil {
		t.Fatalf("read home order: %v", err)
	}
	want, _ := carrier.ForDelivery("", false)
	if !slices.Equal(home.ShipCarriers, want) || home.ShipCarrier != "" {
		t.Errorf("a home order lists %v with %q chosen, want %v and none chosen", home.ShipCarriers, home.ShipCarrier, want)
	}
}

func shipmentCount(t *testing.T, number string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM order_shipments s JOIN orders o ON o.id = s.order_id
		WHERE o.order_number = $1`, number).Scan(&n); err != nil {
		t.Fatalf("count shipments: %v", err)
	}
	return n
}

// The server holds what the form narrows: a home delivery cannot be dispatched
// with a store chain's carrier, nor a store order with a home carrier, whatever
// a crafted post names.
func TestADispatchWithACarrierTheOrderCannotUseIsRefused(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	actor := uuid.NullUUID{UUID: staff, Valid: true}

	home := shippableOrder(t, "zh-Hant")
	err := s.Ship(ctx, home, admin.Dispatch{Carrier: "seven_eleven", Tracking: "WRONG-" + home}, actor)
	if !errors.Is(err, admin.ErrCarrier) {
		t.Fatalf("a home delivery sent by a store carrier answered %v, want ErrCarrier", err)
	}
	if n := shipmentCount(t, home); n != 0 {
		t.Fatalf("the refused dispatch left %d shipments", n)
	}

	h := adminHandlerOver(pool, s)
	form := url.Values{"carrier": {"seven_eleven"}, "tracking": {"WRONG-" + home}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+home+"/ship", strings.NewReader(form.Encode()))
	req.SetPathValue("number", home)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.RequireStaff(h.Ship)(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the refused dispatch answered %d, want 422", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, `id="ship-carrier-error"`) ||
		!strings.Contains(body, i18n.T(i18n.WithLocale(ctx, i18n.ZhHant), i18n.KeyAdminCarrierNotForOrder)) {
		t.Error("the refusal is not shown under the carrier list")
	}

	if err := s.Ship(ctx, home, admin.Dispatch{Carrier: "black_cat", Tracking: "RIGHT-" + home}, actor); err != nil {
		t.Fatalf("a home delivery sent by a home carrier: %v", err)
	}

	store := shippableOrder(t, "zh-Hant")
	if _, execErr := pool.Exec(ctx, `
		UPDATE orders SET shipping_version_id = v.id, shipping_method_code = sm.code, shipping_method_name = v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE orders.order_number = $1 AND sm.destination_kind = 'pickup_point'`, store); execErr != nil {
		t.Fatalf("make it a store order: %v", execErr)
	}
	if shipErr := s.Ship(ctx, store, admin.Dispatch{Carrier: "black_cat", Tracking: "WRONG-" + store}, actor); !errors.Is(shipErr, admin.ErrCarrier) {
		t.Fatalf("a store order sent by a home carrier answered %v, want ErrCarrier", shipErr)
	}
	if shipErr := s.Ship(ctx, store, admin.Dispatch{Carrier: "family_mart", Tracking: "RIGHT-" + store}, actor); shipErr != nil {
		t.Fatalf("a store order sent by a store carrier: %v", shipErr)
	}
}
