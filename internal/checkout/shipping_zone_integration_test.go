//go:build integration

package checkout_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/order/delivery"
)

func TestAnOffshoreAddressCostsMoreThanATaipeiOne(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	version := shipVersionFor(t, "home_delivery")

	taipei, err := s.QuoteShipping(ctx, version, 100000, "110")
	if err != nil {
		t.Fatalf("quote for Taipei: %v", err)
	}
	kinmen, err := s.QuoteShipping(ctx, version, 100000, "890")
	if err != nil {
		t.Fatalf("quote for Kinmen: %v", err)
	}
	kinmenTotal, taipeiTotal := quoteTotal(t, kinmen), quoteTotal(t, taipei)
	if kinmenTotal <= taipeiTotal {
		t.Errorf("金門 costs %d and 台北 costs %d — the surcharge is not applied",
			kinmenTotal, taipeiTotal)
	}
	if kinmen.Surcharge == 0 || kinmen.ZoneName == "" {
		t.Errorf("the quote does not name the surcharge: %+v", kinmen)
	}

	// A FIVE-digit code, which is what a customer types: Taiwan writes 3+2, and the
	// zone is found from the first three.
	full, err := s.QuoteShipping(ctx, version, 100000, "89052")
	if err != nil {
		t.Fatalf("quote for a five-digit Kinmen code: %v", err)
	}
	fullTotal := quoteTotal(t, full)
	if fullTotal != kinmenTotal {
		t.Errorf("89052 costs %d and 890 costs %d — the prefix is not being taken",
			fullTotal, kinmenTotal)
	}

	freeTaipei, err := s.QuoteShipping(ctx, version, 500000, "110")
	if err != nil {
		t.Fatalf("quote for a large Taipei order: %v", err)
	}
	freeKinmen, err := s.QuoteShipping(ctx, version, 500000, "890")
	if err != nil {
		t.Fatalf("quote for a large Kinmen order: %v", err)
	}
	freeTaipeiTotal, freeKinmenTotal := quoteTotal(t, freeTaipei), quoteTotal(t, freeKinmen)
	if freeTaipeiTotal != 0 {
		t.Errorf("a large 台北 order pays %d, want free", freeTaipeiTotal)
	}
	if freeKinmenTotal != kinmen.Surcharge {
		t.Errorf("a large 金門 order pays %d, want the surcharge %d — 免運 must "+
			"cover the base rate and not the crossing",
			freeKinmenTotal, kinmen.Surcharge)
	}
}

func TestAPickupOrderIsNeverInAZone(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	quote, err := s.QuoteShipping(ctx, shipVersionFor(t, "store_pickup"), 100000, "")
	if err != nil {
		t.Fatalf("quote for a pickup order: %v", err)
	}
	if quote.Surcharge != 0 || quote.ZoneName != "" {
		t.Errorf("a pickup order was priced into a zone: %+v", quote)
	}
}

func TestAnOrderIsChargedTheZoneItShipsTo(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, freshVariant(t, "stockfix-16"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	addr := &delivery.Details{
		Email: "kinmen@example.com", RecipientName: "金門", Phone: "0912345678",
		PostalCode: "890", City: "金門縣", District: "金城鎮", Street: "民生路 1 號",
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{},
		shipVersionFor(t, "home_delivery"), addr, "", "zone-order-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var charged, subtotal int64
	if readErr := pool.QueryRow(ctx, `
		SELECT o.shipping_cents,
		       coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
		                 FROM order_lines ol WHERE ol.order_id = o.id), 0)
		FROM orders o WHERE o.order_number = $1`, number).Scan(&charged, &subtotal); readErr != nil {
		t.Fatalf("read order: %v", readErr)
	}

	want, err := s.QuoteShipping(ctx, shipVersionFor(t, "home_delivery"), subtotal, "890")
	if err != nil {
		t.Fatalf("re-quote: %v", err)
	}
	wantTotal := quoteTotal(t, want)
	if charged != wantTotal {
		t.Errorf("the order was charged %d, want %d", charged, wantTotal)
	}
	if want.Surcharge == 0 {
		t.Fatal("the fixture priced no surcharge — this test proved nothing")
	}
}

func TestAFreeShippingCouponDoesNotPayForTheCrossing(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, min_subtotal_cents)
		VALUES ('FREESHIPZONE', '測試免運', 'free_shipping', 0)`); err != nil {
		t.Fatalf("create the coupon: %v", err)
	}

	place := func(postal, city, district, key string) int64 {
		t.Helper()
		id := newCart(t, s)
		if err := s.Carts().Add(ctx, id, freshVariant(t, "stockfix-17"), 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		_, err := s.CouponByCode(ctx, "FREESHIPZONE")
		if err != nil {
			t.Fatalf("find the coupon: %v", err)
		}
		number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{},
			shipVersionFor(t, "home_delivery"), &delivery.Details{
				Email: "ship@example.com", RecipientName: "測試", Phone: "0912345678",
				PostalCode: postal, City: city, District: district, Street: "路 1 號",
			}, "FREESHIPZONE", key)
		if err != nil {
			t.Fatalf("place: %v", err)
		}
		var cents int64
		if err := pool.QueryRow(ctx,
			`SELECT shipping_cents FROM orders WHERE order_number = $1`, number).Scan(&cents); err != nil {
			t.Fatalf("read shipping: %v", err)
		}
		return cents
	}

	if got := place("110", "台北市", "信義區", "freeship-main"); got != 0 {
		t.Errorf("a mainland order with a 免運 coupon pays %d, want 0", got)
	}
	offshore := place("890", "金門縣", "金城鎮", "freeship-offshore")
	if offshore == 0 {
		t.Error("a 免運 coupon paid for the 離島 crossing")
	}
}
