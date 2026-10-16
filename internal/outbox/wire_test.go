package outbox

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

// decodesTo feeds raw, as a stored row carries it, through the handler path of
// tp and holds the result to want. The literals below are the JSON each topic
// has always carried, so a renamed tag or field turns a row already queued into
// a zero value and fails here.
func decodesTo[T any](t *testing.T, tp Topic[T], raw string, want T) {
	t.Helper()
	store := &Store{handlers: map[string]Handler{}}
	var got T
	store.HandleJSON(tp, func(_ context.Context, p *T) error {
		got = *p
		return nil
	})
	if err := store.handlers[tp.Name()](t.Context(), []byte(raw)); err != nil {
		t.Fatalf("%s: decode %s: %v", tp.Name(), raw, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: decoded %+v from %s, want %+v", tp.Name(), got, raw, want)
	}
}

func TestStoredRowsDecodeUnderTheirTopicsType(t *testing.T) {
	t.Parallel()

	order := uuid.MustParse("6666aaaa-6666-4666-8666-666666666666")

	decodesTo(t, TopicOrderPlaced,
		`{"locale":"en","order_number":"GO-1","email":"a@example.com","name":"A","total_cents":1000,"owed_cents":900}`,
		OrderPlaced{Locale: "en", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", TotalCents: 1000, OwedCents: 900})
	decodesTo(t, TopicOrderPlaced,
		`{"locale":"zh-Hant","order_number":"GO-1","email":"a@example.com","name":"A","total_cents":1000,"owed_cents":0}`,
		OrderPlaced{Locale: "zh-Hant", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", TotalCents: 1000, OwedCents: 0})
	// This row was queued while snapshots still carried "tax_cents" (always 0
	// in practice, 43 here to prove the value is dropped, not read). The field
	// is gone from the Go type, and encoding/json ignores the unknown key, so a
	// row still waiting in outbox_messages decodes to the same value as before.
	decodesTo(t, TopicOrderPlaced,
		`{"locale":"en","order_number":"GO-1","email":"a@example.com","name":"A","total_cents":1000,"owed_cents":900,`+
			`"snapshot":{"lines":[{"sku":"MUG-1","name":"Mug","label":"Blue","unit_cents":450,"quantity":2}],`+
			`"subtotal_cents":900,"shipping_cents":60,"discount_cents":30,"discount_reason":"spring","tax_cents":43,`+
			`"credit_cents":100,"shipping_name":"A Buyer","delivery_to":"7-11 Taipei","phone":"0912345678",`+
			`"delivery_note":"leave at door","hold_until":"2026-08-10T12:00:00Z","start_by":"2026-08-07T15:22:00Z"}}`,
		OrderPlaced{Locale: "en", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", TotalCents: 1000, OwedCents: 900,
			Snapshot: &PlacedSnapshot{
				Lines:          []PlacedLine{{SKU: "MUG-1", Name: "Mug", Label: "Blue", UnitCents: 450, Quantity: 2}},
				SubtotalCents:  900,
				ShippingCents:  60,
				DiscountCents:  30,
				DiscountReason: "spring",
				CreditCents:    100,
				ShippingName:   "A Buyer",
				DeliveryTo:     "7-11 Taipei",
				Phone:          "0912345678",
				DeliveryNote:   "leave at door",
				HoldUntil:      time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
				StartBy:        time.Date(2026, 8, 7, 15, 22, 0, 0, time.UTC),
			}})
	decodesTo(t, TopicOrderPaid,
		`{"locale":"en","order_number":"GO-1","email":"a@example.com","name":"A","amount_cents":1000,"card":"Visa •••• 4242"}`,
		OrderPaid{Locale: "en", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", AmountCents: 1000, Card: "Visa •••• 4242"})
	decodesTo(t, TopicOrderShipped,
		`{"locale":"en","order_number":"GO-1","email":"a@example.com","name":"A","carrier":"black_cat","tracking":"T1","pickup":true}`,
		OrderShipped{Locale: "en", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", Carrier: "black_cat", Tracking: "T1", Pickup: true})
	decodesTo(t, TopicOrderTerminal,
		`{"order_id":"6666aaaa-6666-4666-8666-666666666666","kind":"cancelled_by_staff","refunded":true}`,
		OrderTerminal{OrderID: order, Kind: TerminalCancelledByStaff, Refunded: true})
	decodesTo(t, TopicOrderPartRefunded,
		`{"order_id":"6666aaaa-6666-4666-8666-666666666666","return_request_id":"6666aaaa-6666-4666-8666-666666666666"}`,
		OrderPartRefunded{OrderID: order, ReturnRequestID: order})
	decodesTo(t, TopicRestocked,
		`{"locale":"en","email":"a@example.com","product_name":"Mug","slug":"mug","sku":"MUG-1"}`,
		RestockNotice{Locale: "en", Email: "a@example.com", ProductName: "Mug", Slug: "mug", SKU: "MUG-1"})
	decodesTo(t, TopicPasswordReset,
		`{"locale":"en","email":"a@example.com","token":"tok"}`,
		PasswordReset{Locale: "en", Email: "a@example.com", Token: "tok"})
	decodesTo(t, TopicNewsletterConfirm,
		`{"locale":"en","email":"a@example.com","token":"tok"}`,
		NewsletterConfirm{Locale: "en", Email: "a@example.com", Token: "tok"})
	decodesTo(t, TopicNewsletterWelcome,
		`{"locale":"en","email":"a@example.com","unsubscribe_token":"leave"}`,
		NewsletterWelcome{Locale: "en", Email: "a@example.com", UnsubscribeToken: "leave"})
	decodesTo(t, TopicNewsletterIssue,
		`{"locale":"en","email":"a@example.com","subject":"S","body":"B","unsubscribe_token":"leave"}`,
		NewsletterIssue{Locale: "en", Email: "a@example.com", Subject: "S", Body: "B", UnsubscribeToken: "leave"})
	decodesTo(t, TopicEmailVerify,
		`{"locale":"en","email":"a@example.com","token":"tok","registration":true,"next":"/account"}`,
		AddressVerify{Locale: "en", Email: "a@example.com", Token: "tok", Registration: true, Next: "/account"})
	decodesTo(t, TopicStaffInvitation,
		`{"user_id":"u1","locale":"en"}`,
		StaffInvitation{UserID: "u1", Locale: "en"})
	decodesTo(t, TopicStaffEnrolment,
		`{"locale":"en","email":"a@example.com","code":"01234567"}`,
		StaffEnrolment{Locale: "en", Email: "a@example.com", Code: "01234567"})
	decodesTo(t, TopicStaffFactorFailure,
		`{"locale":"en","email":"a@example.com","locked":true,"limit":10}`,
		StaffFactorFailure{Locale: "en", Email: "a@example.com", Locked: true, Limit: 10})
	decodesTo(t, TopicPasswordResetRequest,
		`{"user_id":"u1","locale":"en"}`,
		PasswordResetRequest{UserID: "u1", Locale: "en"})
	decodesTo(t, TopicRegistration,
		`{"user_id":"u1","created":true,"locale":"en","next":"/account"}`,
		AccountRegistration{UserID: "u1", Created: true, Locale: "en", Next: "/account"})
	decodesTo(t, TopicInvoiceDue,
		`{"order_number":"GO-1","trigger":"evt_1"}`,
		InvoiceDue{OrderNumber: "GO-1", Trigger: "evt_1"})
	decodesTo(t, TopicInvoiceVoidDue,
		`{"order_number":"GO-1","trigger":"cancel:GO-1"}`,
		InvoiceVoidDue{OrderNumber: "GO-1", Trigger: "cancel:GO-1"})
}

// TestAFundedOrderWritesZeroOwed holds the producer half of the wire: Enqueue
// stores what json.Marshal writes, and a fully funded order owes 0, which the
// letter reads as "nothing payable". A zero left out of the JSON would be
// indistinguishable from a field nobody wrote.
func TestAFundedOrderWritesZeroOwed(t *testing.T) {
	t.Parallel()

	got, err := json.Marshal(OrderPlaced{
		Locale: "en", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", TotalCents: 1000, OwedCents: 0,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"locale":"en","order_number":"GO-1","email":"a@example.com","name":"A","total_cents":1000,"owed_cents":0}`
	if string(got) != want {
		t.Errorf("a funded order is stored as\n%s\nwant\n%s", got, want)
	}
}

// TestAPlacedSnapshotIsStoredWithoutTax holds the encoded half of the snapshot:
// the JSON the order page's confirmation row is written as. Prices include tax,
// so no tax figure is stored; every other key keeps its name and its place.
func TestAPlacedSnapshotIsStoredWithoutTax(t *testing.T) {
	t.Parallel()

	got, err := json.Marshal(PlacedSnapshot{
		Lines:          []PlacedLine{{SKU: "MUG-1", Name: "Mug", Label: "Blue", UnitCents: 450, Quantity: 2}},
		SubtotalCents:  900,
		ShippingCents:  60,
		DiscountCents:  30,
		DiscountReason: "spring",
		CreditCents:    100,
		ShippingName:   "A Buyer",
		DeliveryTo:     "7-11 Taipei",
		Phone:          "0912345678",
		DeliveryNote:   "leave at door",
		HoldUntil:      time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		StartBy:        time.Date(2026, 8, 7, 15, 22, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"lines":[{"sku":"MUG-1","name":"Mug","label":"Blue","unit_cents":450,"quantity":2}],` +
		`"subtotal_cents":900,"shipping_cents":60,"discount_cents":30,"discount_reason":"spring",` +
		`"credit_cents":100,"shipping_name":"A Buyer","delivery_to":"7-11 Taipei","phone":"0912345678",` +
		`"delivery_note":"leave at door","hold_until":"2026-08-10T12:00:00Z","start_by":"2026-08-07T15:22:00Z"}`
	if string(got) != want {
		t.Errorf("a snapshot is stored as\n%s\nwant\n%s", got, want)
	}
}
