//go:build integration

package checkout_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/shipping"
)

// TestStore_PlaceOrderPickupFromMethod: PlaceOrder takes the destination from
// the shipping method, not the form: a pickup method keeps the pickup point and
// no street though the form claims an address.
func TestStore_PlaceOrderPickupFromMethod(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, variantOf(t, "pixelight-9-pro"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	addr := &delivery.Details{
		// The destination the CALLER claims is deliberately wrong: PlaceOrder re-reads
		// the method and overrides it.
		To:    shipping.DestinationAddress,
		Email: "pickup@example.com", RecipientName: "陳小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
		PickupChain: "family_mart", PickupStoreCode: "012345", PickupStoreName: "台北車站門市",
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{},
		shipVersionFor(t, "store_pickup"), addr, "", "dest-pickup-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place pickup order: %v", err)
	}

	street, brand, code, name := destinationOf(t, number)
	if street != "" {
		t.Errorf("a pickup order kept a street address: %q", street)
	}
	if brand != "family_mart" || code != "012345" || name != "台北車站門市" {
		t.Errorf("pickup destination is %q/%q/%q, want family_mart/012345/台北車站門市",
			brand, code, name)
	}
}

// TestStore_PlaceOrderPickupChainAlone: PlaceOrder places a convenience-store
// order that carries only the chain, with no store number, store name or
// street, and the confirmation names the chain. It holds what checkout now
// submits: a convenience-store order carrying no store number and no store
// name. The columns stay for the day the carrier's picker fills them.
func TestStore_PlaceOrderPickupChainAlone(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, variantOf(t, "pixelight-9-pro"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	addr := &delivery.Details{
		To:    shipping.DestinationPickupPoint,
		Email: "chain@example.com", RecipientName: "林小美", Phone: "0955666777",
		PickupChain: "seven_eleven",
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{},
		shipVersionFor(t, "store_pickup"), addr, "", "dest-chain-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place a pickup order with the chain alone: %v", err)
	}

	street, brand, code, name := destinationOf(t, number)
	if brand != "seven_eleven" {
		t.Errorf("the chain is %q, want seven_eleven", brand)
	}
	if street != "" || code != "" || name != "" {
		t.Errorf("the order carries more than the chain: %q/%q/%q", street, code, name)
	}

	view, err := buyer.NewStore(pool).Order(ctx, number)
	if err != nil {
		t.Fatalf("read the order back: %v", err)
	}
	if !strings.Contains(view.DeliveryTo, "7-ELEVEN") {
		t.Errorf("the confirmation does not name the chain: %q", view.DeliveryTo)
	}
}

// TestStore_PlaceOrderAddressNoPickup: PlaceOrder keeps the street address of
// an address order and no pickup point, though the form carried one.
func TestStore_PlaceOrderAddressNoPickup(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id := checkouttest.NewCart(t, s)
	if err := s.Carts().Add(ctx, id, variantOf(t, "pixelight-9-pro"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	addr := &delivery.Details{
		To:    shipping.DestinationPickupPoint,
		Email: "home@example.com", RecipientName: "王大明", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: "復興南路一段 1 號",
		PickupChain: "seven_eleven", PickupStoreCode: "987654", PickupStoreName: "光復門市",
	}
	number, err := checkouttest.PlaceOrder(t, s, ctx, id, uuid.NullUUID{},
		shipVersionFor(t, "home_delivery"), addr, "", "dest-home-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place address order: %v", err)
	}

	street, brand, code, name := destinationOf(t, number)
	if street != "復興南路一段 1 號" {
		t.Errorf("the street address did not survive: %q", street)
	}
	if brand != "" || code != "" || name != "" {
		t.Errorf("an address order kept a pickup point: %q/%q/%q", brand, code, name)
	}
}
