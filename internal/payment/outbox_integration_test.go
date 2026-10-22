//go:build integration

package payment_test

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
)

// TestWebhookTx_CaptureEnqueuesReceipt: A capture enqueues the receipt. It
// holds that money arriving produces a receipt, written in the capture's own
// transaction.
func TestWebhookTx_CaptureEnqueuesReceipt(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := order(t, 149900)
	session := "cs_receipt_" + number

	// A capture runs from a webhook, where nobody is reading, so a receipt that
	// is not English read its locale from somewhere it never may.
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET locale = 'en' WHERE order_number = $1`, number); err != nil {
		t.Fatalf("set the order locale: %v", err)
	}

	if err := s.OpenPayment(ctx, number, session, 149900); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{
		SessionID: session, AmountRecv: 149900, CardBrand: "visa", CardLast4: "4242",
	}); err != nil {
		t.Fatalf("capture: %v", err)
	}

	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $1`,
		number).Scan(&payload); err != nil {
		t.Fatalf("no receipt was enqueued for %s: %v", number, err)
	}
	var got outbox.OrderPaid
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	// Carried in the payload rather than read at delivery: erase_user blanks
	// order_private_data.
	want := outbox.OrderPaid{
		OrderNumber: number, Email: "pay@example.com", Name: "收件",
		AmountCents: 149900, Card: "Visa •••• 4242",
		Locale: "en",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("receipt (-want +got):\n%s", diff)
	}
}

// TestWebhookTx_CaptureRedelivered: A redelivered webhook sends one receipt. It
// holds the dedupe key that turns at-least-once delivery into one email.
func TestWebhookTx_CaptureRedelivered(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := order(t, 99900)
	session := "cs_dupe_" + number

	if err := s.OpenPayment(ctx, number, session, 99900); err != nil {
		t.Fatalf("open: %v", err)
	}
	capture := func() error {
		_, err := captureThroughWebhook(t, s, payment.Capture{SessionID: session, AmountRecv: 99900})
		return err
	}
	// Twice, deliberately: the dedupe key is what is left if the claim fails.
	if err := capture(); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := capture(); err != nil {
		t.Fatalf("second capture: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $1`,
		number).Scan(&n); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if n != 1 {
		t.Errorf("%d receipts enqueued for one order, want 1", n)
	}
}
