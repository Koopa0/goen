//go:build integration

package cart_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/shoptime"
)

func TestCheckoutQueuesTheCompletePlacedConfirmation(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, kind := range []destination.Kind{destination.Address, destination.PickupPoint} {
			for _, funding := range []struct {
				name                                             string
				balance, credit, owed, shipping, discount, total int64
				en, zh                                           string
			}{
				{"card", 0, 0, 201000, 6000, 5000, 201000, "Credit card: NT$2,010 remains payable.", "信用卡：尚須支付 NT$2,010。"},
				{"mixed", 50000, 50000, 151000, 6000, 5000, 201000, "Store credit applied: NT$500. Credit card: NT$1,510 remains payable.", "購物金已折抵 NT$500；信用卡尚須支付 NT$1,510。"},
				{"credit", 1000000, 201000, 0, 6000, 5000, 201000, "Paid with store credit: NT$2,010.", "已使用購物金支付 NT$2,010。"},
				{"zero with unused credit", 50000, 0, 0, 0, 200000, 0, "No payment required.", "無須付款。"},
			} {
				t.Run(locale.Tag()+"/"+string(kind)+"/"+funding.name, func(t *testing.T) {
					ctx := i18n.WithLocale(t.Context(), locale)
					s := cart.NewStore(pool)
					variant, zhName, enName := translatedVariant(t)
					name := enName
					if locale == i18n.ZhHant {
						name = zhName
					}
					var sku string
					if err := pool.QueryRow(ctx, `SELECT sku FROM product_variants WHERE id = $1`, variant).Scan(&sku); err != nil {
						t.Fatalf("read fixture SKU: %v", err)
					}
					var methodID, shipID uuid.UUID
					if err := pool.QueryRow(ctx, `INSERT INTO shipping_methods (code, destination_kind) VALUES ($1, $2) RETURNING id`, "mail"+uuid.NewString()[:8], string(kind)).Scan(&methodID); err != nil {
						t.Fatalf("create delivery method: %v", err)
					}
					t.Cleanup(func() {
						clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
						defer cancel()
						if _, err := pool.Exec(clean, `UPDATE shipping_methods SET is_active = false WHERE id = $1`, methodID); err != nil {
							t.Errorf("withdraw delivery fixture: %v", err)
						}
					})
					if err := pool.QueryRow(ctx, `INSERT INTO shipping_method_versions (method_id, name, name_en, fee_cents) VALUES ($1, '確認信配送', 'Confirmation delivery', $2) RETURNING id`, methodID, funding.shipping).Scan(&shipID); err != nil {
						t.Fatalf("create delivery version: %v", err)
					}
					shippingName := "Confirmation delivery"
					if locale == i18n.ZhHant {
						shippingName = "確認信配送"
					}
					couponCode := coupon(t, "MAIL"+strings.ToUpper(uuid.NewString()[:8]), "amount", funding.discount, 0, 0, 0, 0)
					owner := uuid.NullUUID{}
					if funding.balance > 0 {
						owner = uuid.NullUUID{UUID: creditedCustomer(t, funding.balance), Valid: true}
					}
					addr := &order.Delivery{To: kind, Email: "placed-snapshot@example.com", RecipientName: "Alex", Phone: "0912345678", PostalCode: "110", City: "Taipei", District: "Xinyi", Street: "Recorded Road 1", Note: "Recorded delivery note"}
					deliveryTo := "110 TaipeiXinyiRecorded Road 1"
					if kind == destination.PickupPoint {
						addr.PickupChain, addr.PickupStoreCode, addr.PickupStoreName = pickup.SevenEleven, "012345", "Recorded store"
						addr.DropOtherDestination()
						deliveryTo = "7-ELEVEN Recorded store(012345)"
					}
					id := newCart(t, s)
					if err := s.Add(ctx, id, variant, 2); err != nil {
						t.Fatalf("add: %v", err)
					}
					quote := checkoutQuote(t, s, id, owner, shipID, addr, couponCode)
					attempt := checkoutAttemptKey("placed-contract-" + uuid.NewString())
					number, placeErr := s.PlaceOrder(ctx, id, owner, shipID, addr, nil, couponCode, quote, attempt)
					if placeErr != nil {
						t.Fatalf("PlaceOrder(): %v", placeErr)
					}
					payload, total, owed := placedOrderPayload(t, number)
					if total != funding.total || owed != funding.owed {
						t.Fatalf("fixture total/owed = %d/%d, want %d/%d", total, owed, funding.total, funding.owed)
					}
					if payload.Snapshot == nil {
						t.Fatal("PlaceOrder() queued no commercial snapshot")
					}
					var placed, heldUntil time.Time
					if err := pool.QueryRow(ctx, `SELECT o.placed_at, min(ir.expires_at) FROM orders o JOIN inventory_reservations ir ON ir.order_id=o.id WHERE o.order_number=$1 GROUP BY o.placed_at`, number).Scan(&placed, &heldUntil); err != nil {
						t.Fatalf("read actual reservation: %v", err)
					}
					want := email.PlacedSnapshot{Lines: []email.PlacedLine{{SKU: sku, Name: name, UnitCents: 100000, Quantity: 2}}, SubtotalCents: 200000, ShippingCents: funding.shipping, DiscountCents: funding.discount, DiscountReason: couponCode + " · 測試折扣", TaxCents: 0, CreditCents: funding.credit, ShippingName: shippingName, DeliveryTo: deliveryTo, Phone: "0912345678", DeliveryNote: "Recorded delivery note", HoldUntil: heldUntil, StartBy: heldUntil.Add(-31 * time.Minute)}
					got := *payload.Snapshot
					if !got.HoldUntil.Equal(want.HoldUntil) || !got.StartBy.Equal(want.StartBy) {
						t.Errorf("queued deadlines = %s/%s, want actual hold %s and existing start %s", got.HoldUntil, got.StartBy, want.HoldUntil, want.StartBy)
					}
					got.HoldUntil, got.StartBy = want.HoldUntil, want.StartBy
					if !reflect.DeepEqual(got, want) {
						t.Errorf("PlaceOrder() queued snapshot = %#v, want %#v", got, want)
					}
					if payload.Name != "Alex" || payload.Email != addr.Email || payload.Locale != locale.Tag() || payload.OwedCents == nil || *payload.OwedCents != funding.owed {
						t.Errorf("PlaceOrder() recipient/locale/funding = %#v", payload)
					}
					if !heldUntil.After(placed) {
						t.Fatalf("fixture hold %s does not follow placement %s", heldUntil, placed)
					}
					if _, err := pool.Exec(ctx, `UPDATE products SET name='Changed live name',name_en='Changed live name' WHERE id=(SELECT product_id FROM product_variants WHERE id=$1)`, variant); err != nil {
						t.Fatalf("change live catalogue: %v", err)
					}
					if _, err := pool.Exec(ctx, `UPDATE product_variants SET price_cents=900000 WHERE id=$1`, variant); err != nil {
						t.Fatalf("change live price: %v", err)
					}
					again, err := s.PlaceOrder(ctx, id, owner, shipID, addr, nil, couponCode, quote, attempt)
					if err != nil || again != number {
						t.Fatalf("PlaceOrder() replay = %q/%v, want %q", again, err, number)
					}
					queued, _, _ := placedOrderPayload(t, number)
					if !reflect.DeepEqual(payload, queued) {
						t.Errorf("PlaceOrder() replay changed its original confirmation")
					}
					var count int
					if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE topic='order.placed' AND dedupe_key=$1`, number).Scan(&count); err != nil {
						t.Fatalf("count confirmation: %v", err)
					}
					if count != 1 {
						t.Errorf("PlaceOrder() replay queued %d confirmations, want 1", count)
					}
					if owner.Valid {
						balance, err := s.AvailableCredit(ctx, owner)
						if err != nil {
							t.Fatalf("read remaining credit: %v", err)
						}
						if balance != funding.balance-funding.credit {
							t.Errorf("PlaceOrder() replay credit balance = %d, want %d", balance, funding.balance-funding.credit)
						}
					}
					sink := &placedConfirmationSink{}
					notifier := email.New(sink, "https://goen.test", "", "")
					if err := notifier.SendOrderPlaced(t.Context(), &queued); err != nil {
						t.Fatalf("send queued confirmation: %v", err)
					}
					fundingText := funding.en
					if locale == i18n.ZhHant {
						fundingText = funding.zh
					}
					for _, text := range []string{name, sku, shippingName, deliveryTo, "0912345678", "Recorded delivery note", fundingText} {
						if !strings.Contains(sink.message.Body, text) || !strings.Contains(sink.message.HTML, text) {
							t.Errorf("queued confirmation missing frozen %q in text/HTML", text)
						}
					}
					if strings.Contains(sink.message.Body, "Changed live name") || strings.Contains(sink.message.Body, "NT$9,000") {
						t.Errorf("queued confirmation read changed catalogue: %s", sink.message.Body)
					}
					if funding.owed > 0 {
						for _, deadline := range []string{shoptime.Minute(want.StartBy), shoptime.Minute(heldUntil)} {
							if !strings.Contains(sink.message.Body, deadline) {
								t.Errorf("queued confirmation missing actual deadline %q: %s", deadline, sink.message.Body)
							}
						}
					} else if strings.Contains(sink.message.Body, "開始付款") || strings.Contains(sink.message.Body, "Start paying") {
						t.Errorf("funded confirmation asks to begin payment: %s", sink.message.Body)
					}
				})
			}
		}
	}
}

type placedConfirmationSink struct{ message *email.Message }

func (s *placedConfirmationSink) Send(_ context.Context, m *email.Message) error {
	s.message = m
	return nil
}

// TestCheckoutWritesWhatThePlacedLetterStillOwes is the producer half of the
// placed-letter owed figure. The renderer already quotes owed_cents; without
// this, a checkout that omits the field or writes the order total still mails
// the pay CTA on a funded order. Both credit cases have to land in the
// committed payload; one alone lets the other drift.
func TestCheckoutWritesWhatThePlacedLetterStillOwes(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	shipID := shipVersionFor(t, "home_delivery")
	addr := &order.Delivery{
		Email: "placed-mail@example.com", RecipientName: "王小明", Phone: "0912345678",
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

func placedOrderPayload(t *testing.T, number string) (payload email.OrderPlaced, total, owed int64) {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(t.Context(), `
		SELECT payload FROM outbox_messages
		WHERE topic = 'order.placed' AND dedupe_key = $1`, number).Scan(&raw); err != nil {
		t.Fatalf("read order.placed payload for %s: %v", number, err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode order.placed payload: %v", err)
	}

	if err := pool.QueryRow(t.Context(), `
		SELECT (SELECT coalesce(sum(ol.unit_price_cents * ol.quantity), 0)
		          FROM order_lines ol WHERE ol.order_id = o.id)
		       - o.discount_cents + o.shipping_cents + o.tax_cents,
		       order_amount_after_credit(o.id)
		FROM orders o WHERE o.order_number = $1`, number).Scan(&total, &owed); err != nil {
		t.Fatalf("read order totals for %s: %v", number, err)
	}
	return payload, total, owed
}
