//go:build integration

package buyer_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// shipVersionFor is the current version of the named shipping method.
func shipVersionFor(t *testing.T, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT v.id FROM shipping_method_versions v
		 JOIN shipping_methods sm ON sm.id = v.method_id
		 WHERE sm.code = $1 ORDER BY v.effective_at DESC LIMIT 1`, code).Scan(&id); err != nil {
		t.Fatalf("shipping version for %s: %v", code, err)
	}
	return id
}

// TestStore_OrderPickupDeliveryTo: Store.Order names the chain, the
// store and its code in DeliveryTo for a pickup order placed with all three.
// The old name said both pages; the test reads the order view's DeliveryTo
// only.
func TestStore_OrderPickupDeliveryTo(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, ordertest.FreshVariant(t, pool, "pickup-delivery-to"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	addr := &delivery.Details{
		Email: "shown@example.com", RecipientName: "李小華", Phone: "0933444555",
		PickupChain: "hi_life", PickupStoreCode: "778899", PickupStoreName: "民生門市",
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{},
		shipVersionFor(t, "store_pickup"), addr, "", "dest-shown-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	view, err := buyer.NewStore(pool).Order(ctx, number)
	if err != nil {
		t.Fatalf("read order view: %v", err)
	}
	for _, want := range []string{"萊爾富", "民生門市", "778899"} {
		if !strings.Contains(view.DeliveryTo, want) {
			t.Errorf("the confirmation does not show %q: %q", want, view.DeliveryTo)
		}
	}
}
