package outbox

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/email"
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

	owed := int64(900)
	order := uuid.MustParse("6666aaaa-6666-4666-8666-666666666666")

	decodesTo(t, TopicOrderPlaced,
		`{"locale":"en","order_number":"GO-1","email":"a@example.com","name":"A","total_cents":1000,"owed_cents":900}`,
		email.OrderPlaced{Locale: "en", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", TotalCents: 1000, OwedCents: &owed})
	decodesTo(t, TopicOrderPlaced,
		`{"locale":"zh-Hant","order_number":"GO-1","email":"a@example.com","name":"A","total_cents":1000}`,
		email.OrderPlaced{Locale: "zh-Hant", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", TotalCents: 1000})
	decodesTo(t, TopicOrderPaid,
		`{"locale":"en","order_number":"GO-1","email":"a@example.com","name":"A","amount_cents":1000,"card":"Visa •••• 4242"}`,
		email.OrderPaid{Locale: "en", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", AmountCents: 1000, Card: "Visa •••• 4242"})
	decodesTo(t, TopicOrderShipped,
		`{"locale":"en","order_number":"GO-1","email":"a@example.com","name":"A","carrier":"black_cat","tracking":"T1","pickup":true}`,
		email.OrderShipped{Locale: "en", OrderNumber: "GO-1", Email: "a@example.com", Name: "A", Carrier: "black_cat", Tracking: "T1", Pickup: true})
	decodesTo(t, TopicOrderTerminal,
		`{"order_id":"6666aaaa-6666-4666-8666-666666666666","kind":"cancelled_by_staff","refunded":true}`,
		email.OrderTerminal{OrderID: order, Kind: email.TerminalCancelledByStaff, Refunded: true})
	decodesTo(t, TopicRestocked,
		`{"locale":"en","email":"a@example.com","product_name":"Mug","slug":"mug","sku":"MUG-1"}`,
		email.RestockNotice{Locale: "en", Email: "a@example.com", ProductName: "Mug", Slug: "mug", SKU: "MUG-1"})
	decodesTo(t, TopicPasswordReset,
		`{"locale":"en","email":"a@example.com","token":"tok"}`,
		email.PasswordReset{Locale: "en", Email: "a@example.com", Token: "tok"})
	decodesTo(t, TopicNewsletterConfirm,
		`{"locale":"en","email":"a@example.com","token":"tok"}`,
		email.NewsletterConfirm{Locale: "en", Email: "a@example.com", Token: "tok"})
	decodesTo(t, TopicNewsletterWelcome,
		`{"locale":"en","email":"a@example.com","unsubscribe_token":"leave"}`,
		email.NewsletterWelcome{Locale: "en", Email: "a@example.com", UnsubscribeToken: "leave"})
	decodesTo(t, TopicNewsletterIssue,
		`{"locale":"en","email":"a@example.com","subject":"S","body":"B","unsubscribe_token":"leave"}`,
		email.NewsletterIssue{Locale: "en", Email: "a@example.com", Subject: "S", Body: "B", UnsubscribeToken: "leave"})
	decodesTo(t, TopicEmailVerify,
		`{"locale":"en","email":"a@example.com","token":"tok","registration":true,"next":"/account"}`,
		email.AddressVerify{Locale: "en", Email: "a@example.com", Token: "tok", Registration: true, Next: "/account"})
	decodesTo(t, TopicStaffInvitation,
		`{"user_id":"u1","locale":"en"}`,
		email.StaffInvitation{UserID: "u1", Locale: "en"})
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
