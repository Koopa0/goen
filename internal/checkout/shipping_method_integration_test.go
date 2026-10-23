//go:build integration

package checkout_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/shipping"
)

// TestBuyerStore_OrderShippingName: buyer.Store.Order names an order's shipping
// method in the reader's language, from the version the order was priced from:
// English for an English reader, the Chinese the shop typed for a Chinese one.
// The method's name is read from the version the order was priced from, so an
// English reader is not shown the Chinese name the shop typed, and a Chinese
// reader still is.
func TestBuyerStore_OrderShippingName(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	var methodID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_methods (code, destination_kind)
		VALUES ($1, 'pickup_point') RETURNING id`, "named"+uuid.NewString()[:6]).Scan(&methodID); err != nil {
		t.Fatalf("create method: %v", err)
	}
	var versionID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_method_versions (method_id, name, name_en, fee_cents)
		VALUES ($1, '測試超取', 'Test pickup', 6000) RETURNING id`, methodID).Scan(&versionID); err != nil {
		t.Fatalf("create version: %v", err)
	}

	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, variantOf(t, "pixelight-9-pro"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	addr := &delivery.Details{
		To:    shipping.DestinationPickupPoint,
		Email: "named@example.com", RecipientName: "林小美", Phone: "0955666777",
		PickupChain: "seven_eleven",
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{}, versionID, addr, "",
		"named-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{{i18n.En, "Test pickup"}, {i18n.ZhHant, "測試超取"}} {
		view, err := buyer.NewStore(pool).Order(i18n.WithLocale(ctx, tt.locale), number)
		if err != nil {
			t.Fatalf("read the order in %s: %v", tt.locale.Tag(), err)
		}
		if view.ShippingName != tt.want {
			t.Errorf("shipping name in %s = %q, want %q", tt.locale.Tag(), view.ShippingName, tt.want)
		}
	}
}
