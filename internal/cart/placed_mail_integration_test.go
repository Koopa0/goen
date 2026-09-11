//go:build integration

package cart_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
)

// TestCheckoutWritesWhatThePlacedLetterStillOwes is the producer half of the
// placed-letter owed figure. The renderer already quotes owed_cents; without
// this, a checkout that omits the field or writes the order total still mails
// the pay CTA on a funded order. Both credit cases have to land in the
// committed payload; one alone lets the other drift.
func TestCheckoutWritesWhatThePlacedLetterStillOwes(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	shipID := shipVersionFor(t, "home_delivery")
	addr := &cart.Address{
		Email: "placed-mail@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	t.Run("fully funded writes zero", func(t *testing.T) {
		userID := creditedCustomer(t, 10_000_000)
		id := newCart(t, s)
		if err := s.Add(ctx, id, freshVariant(t, "placed-mail-full"), 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		number, err := placeOrder(t, s, ctx, id,
			uuid.NullUUID{UUID: userID, Valid: true}, shipID, addr, "",
			"placed-mail-full-"+uuid.NewString())
		if err != nil {
			t.Fatalf("place: %v", err)
		}

		payload, total, owed := placedOrderPayload(t, number)
		if owed != 0 {
			t.Fatalf("a fully funded order still owes %d against total %d; the fixture did not cover it",
				owed, total)
		}
		if payload.OwedCents == nil {
			t.Fatal("a fully funded checkout omitted owed_cents; the letter falls back to the total and asks the customer to pay")
		}
		if *payload.OwedCents != 0 {
			t.Errorf("owed_cents is %d, want 0 — a fully funded letter would still quote an amount due",
				*payload.OwedCents)
		}
	})

	t.Run("partly funded writes the remainder", func(t *testing.T) {
		// NT$1,000 against a NT$1,999 line, so shipping cannot make this a
		// full cover and the remainder cannot be the order total.
		const credit = int64(100000)
		userID := creditedCustomer(t, credit)
		id := newCart(t, s)
		if err := s.Add(ctx, id, freshVariant(t, "placed-mail-part"), 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		number, err := placeOrder(t, s, ctx, id,
			uuid.NullUUID{UUID: userID, Valid: true}, shipID, addr, "",
			"placed-mail-part-"+uuid.NewString())
		if err != nil {
			t.Fatalf("place: %v", err)
		}

		payload, total, owed := placedOrderPayload(t, number)
		if owed <= 0 || owed >= total {
			t.Fatalf("partly funded remainder is %d against total %d; the fixture is not a partial spend",
				owed, total)
		}
		if payload.OwedCents == nil {
			t.Fatal("a partly funded checkout omitted owed_cents; the letter falls back to the total")
		}
		if *payload.OwedCents != owed {
			t.Errorf("owed_cents is %d, want the remainder %d — a partly funded letter would quote the wrong amount due",
				*payload.OwedCents, owed)
		}
	})
}

func placedOrderPayload(t *testing.T, number string) (cart.OrderPlaced, int64, int64) {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(t.Context(), `
		SELECT payload FROM outbox_messages
		WHERE topic = 'order.placed' AND dedupe_key = $1`, number).Scan(&raw); err != nil {
		t.Fatalf("read order.placed payload for %s: %v", number, err)
	}
	var payload cart.OrderPlaced
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode order.placed payload: %v", err)
	}

	var total, owed int64
	if err := pool.QueryRow(t.Context(), `
		SELECT (SELECT coalesce(sum(ol.unit_price_cents * ol.quantity), 0)
		          FROM order_lines ol WHERE ol.order_id = o.id)
		       - o.discount_cents + o.shipping_cents + o.tax_cents,
		       order_amount_owed(o.id)
		FROM orders o WHERE o.order_number = $1`, number).Scan(&total, &owed); err != nil {
		t.Fatalf("read order totals for %s: %v", number, err)
	}
	return payload, total, owed
}
