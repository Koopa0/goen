//go:build integration

package buyer_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestHandler_CancelOrderMayTakeMoney: CancelOrder refuses with 422 and the
// existing refusal message an order whose payment may be taking money
// (requires_action, processing, requires_reconciliation or an unreconciled
// webhook event), and leaves the order, its hold, events, notices and stock
// unchanged. An open Checkout Session is not among them: it is expired first.
func TestHandler_CancelOrderMayTakeMoney(t *testing.T) {
	for _, status := range []string{"requires_action", "processing", "requires_reconciliation", "unreconciled_event"} {
		t.Run(status, func(t *testing.T) {
			ctx := t.Context()
			variant := ordertest.FreshVariant(t, pool, "cancel-payment-guard")
			orderID := ordertest.HeldOrder(t, pool, variant, -10*time.Minute, false)
			number := ordertest.NumberOf(t, pool, orderID)
			ref := "cs_cancel_guard_" + uuid.NewString()
			if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
				t.Fatal(err)
			}
			switch status {
			case "unreconciled_event":
				if _, err := pool.Exec(ctx, `SELECT cancel_payment($1)`, ref); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `
					INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, processed_at, unreconciled)
					VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}', now(), 'refused_capture: cancellation fixture')`,
					"evt_cancel_guard_"+uuid.NewString(), ref); err != nil {
					t.Fatal(err)
				}
			case "requires_reconciliation":
				if _, err := pool.Exec(ctx, `SELECT record_complete_payment($1, $2, 100000)`, orderID, ref); err != nil {
					t.Fatal(err)
				}
			default:
				if _, err := pool.Exec(ctx, `UPDATE payments SET status = $2 WHERE provider_ref = $1`, ref, status); err != nil {
					t.Fatal(err)
				}
			}
			stripeAPI, gateway := newCheckoutStripe(t)
			h := handlerWithCheckouts(t, gateway)
			before := ordertest.StockOf(t, pool, variant)
			for _, locale := range i18n.Locales() {
				localized := i18n.WithLocale(ctx, locale)
				req := httptest.NewRequestWithContext(localized, http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
				req.SetPathValue("number", number)
				placedBy(t, req, number)
				res := httptest.NewRecorder()
				h.CancelOrder(res, req)
				if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" || !strings.Contains(res.Body.String(), i18n.T(localized, i18n.KeyCancelRefusedBody)) {
					t.Errorf("%s cancellation = %d %s, want 422 with existing refusal", locale.Tag(), res.Code, res.Header().Get("Location"))
				}
			}
			if n := stripeAPI.requested(); n != 0 {
				t.Errorf("Stripe received %d requests, want none: no Checkout Session is open", n)
			}
			if f := factsOf(t, number); f.status != "pending" || f.heldHolds != 1 || f.events != 0 || f.notices != 0 || f.reversals != 0 {
				t.Errorf("refused cancellation changed order or effects: %+v", f)
			}
			if got := ordertest.StockOf(t, pool, variant); got != before {
				t.Errorf("refused cancellation changed stock: %d, want %d", got, before)
			}
		})
	}
}

// TestStore_CancelOrderBehindPaymentOpen: a cancellation waiting on the order
// lock rereads the payment once a concurrent open_payment commits, answers
// that a checkout is open and leaves the order pending with its hold.
func TestStore_CancelOrderBehindPaymentOpen(t *testing.T) {
	ctx := t.Context()
	orderID := ordertest.HeldOrder(t, pool, ordertest.FreshVariant(t, pool, "cancel-payment-lock"), -10*time.Minute, false)
	number := ordertest.NumberOf(t, pool, orderID)
	opener, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opener.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := opener.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, "cs_cancel_lock_"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	customer := buyer.NewStore(storeApplicationPool(t, "customer-cancel-behind-open"))
	done := make(chan error, 1)
	go func() {
		cancelErr := customer.CancelOrder(ctx, number)
		done <- cancelErr
	}()
	waitForApplicationLock(t, "customer-cancel-behind-open", done)
	if err := opener.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := errors.AsType[*buyer.CheckoutOpenError](<-done); !ok {
		t.Error("cancellation behind payment open did not answer that a checkout is open")
	}
	if f := factsOf(t, number); f.status != "pending" || f.heldHolds != 1 || f.events != 0 || f.notices != 0 {
		t.Errorf("payment committed during wait did not protect order: %+v", f)
	}
}

// TestStore_CancelOrderSessionExpired: CancelOrder cancels an order whose
// payment session was cancelled, releasing its held stock and writing one event
// and one notice.
func TestStore_CancelOrderSessionExpired(t *testing.T) {
	ctx := t.Context()
	variant := ordertest.FreshVariant(t, pool, "cancel-expired-session")
	orderID := ordertest.HeldOrder(t, pool, variant, -10*time.Minute, false)
	ref := "cs_cancel_expired_" + uuid.NewString()
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT cancel_payment($1)`, ref); err != nil {
		t.Fatal(err)
	}
	otherID := ordertest.HeldOrder(t, pool, ordertest.FreshVariant(t, pool, "cancel-expired-session-other"), -10*time.Minute, false)
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, otherID, "cs_cancel_other_"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	before := ordertest.StockOf(t, pool, variant)
	number := ordertest.NumberOf(t, pool, orderID)
	if err := buyer.NewStore(storeRolePool(t)).CancelOrder(ctx, number); err != nil {
		t.Fatalf("cancel after session expiry: %v", err)
	}
	if f := factsOf(t, number); f.status != "cancelled" || f.heldHolds != 0 || f.events != 1 || f.notices != 1 {
		t.Errorf("expired-session cancellation effects: %+v", f)
	}
	if got := ordertest.StockOf(t, pool, variant); got != before+1 {
		t.Errorf("stock after cancellation = %d, want %d", got, before+1)
	}
}

// TestStore_CancelOrderBehindCapture: a cancellation that waits behind a
// capture is refused with ErrNotCancellable instead of failing in the
// transition trigger, and the paid order is not cancelled.
//
// capture_payment holds the order lock without updating the row. The customer's
// cancel waits for it, and must then see the payment and answer
// ErrNotCancellable rather than reach the transition trigger, whose helper the
// store role may not execute.
func TestStore_CancelOrderBehindCapture(t *testing.T) {
	ctx := t.Context()
	orderID := ordertest.HeldOrder(t, pool, ordertest.FreshVariant(t, pool, "cancel-capture"), time.Hour, false)
	number := ordertest.NumberOf(t, pool, orderID)
	session := "cs_cancel_capture_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, session); err != nil {
		t.Fatalf("open payment: %v", err)
	}

	capturer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin capture: %v", err)
	}
	defer func() { _ = capturer.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := capturer.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, session); err != nil {
		t.Fatalf("capture: %v", err)
	}

	customer := buyer.NewStore(storeApplicationPool(t, "cancel-behind-capture"))
	done := make(chan error, 1)
	go func() {
		cancelErr := customer.CancelOrder(ctx, number)
		done <- cancelErr
	}()
	waitForApplicationLock(t, "cancel-behind-capture", done)
	if err := capturer.Commit(ctx); err != nil {
		t.Fatalf("commit capture: %v", err)
	}
	if err := <-done; !errors.Is(err, buyer.ErrNotCancellable) {
		t.Errorf("cancel behind a capture = %v; want ErrNotCancellable", err)
	}
	if f := factsOf(t, number); f.status == "cancelled" {
		t.Error("a paid order was cancelled")
	}
}
