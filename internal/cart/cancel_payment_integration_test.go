//go:build integration

package cart_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/ratelimit"
)

func TestCustomerCancellationKeepsOrdersThatMayStillTakeMoney(t *testing.T) {
	for _, status := range []string{"requires_payment", "requires_action", "processing", "requires_reconciliation", "unreconciled_event"} {
		t.Run(status, func(t *testing.T) {
			ctx := t.Context()
			variant := freshVariant(t, "cancel-payment-guard")
			orderID := heldOrder(t, variant, -10*time.Minute, false)
			number := numberOf(t, orderID)
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
			appPool := storeRolePool(t)
			h := cart.NewHandler(cart.NewStore(appPool), orderaccess.NewStore(appPool, false), slog.New(slog.DiscardHandler), false,
				ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)
			before := stockOf(t, variant)
			for _, locale := range i18n.Locales() {
				localized := i18n.WithLocale(ctx, locale)
				req := httptest.NewRequestWithContext(localized, http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
				req.SetPathValue("number", number)
				req.AddCookie(placedCookie(t, number))
				res := httptest.NewRecorder()
				h.CancelOrder(res, req)
				if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" || !strings.Contains(res.Body.String(), i18n.T(localized, i18n.KeyCancelRefusedBody)) {
					t.Errorf("%s cancellation = %d %s, want 422 with existing refusal", locale.Tag(), res.Code, res.Header().Get("Location"))
				}
			}
			if f := factsOf(t, number, uuid.Nil); f.status != "pending" || f.heldHolds != 1 || f.events != 0 || f.notices != 0 || f.reversals != 0 {
				t.Errorf("refused cancellation changed order or effects: %+v", f)
			}
			if got := stockOf(t, variant); got != before {
				t.Errorf("refused cancellation changed stock: %d, want %d", got, before)
			}
		})
	}
}

func TestCustomerCancellationRereadsPaymentAfterTakingOrderLock(t *testing.T) {
	ctx := t.Context()
	orderID := heldOrder(t, freshVariant(t, "cancel-payment-lock"), -10*time.Minute, false)
	number := numberOf(t, orderID)
	opener, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opener.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := opener.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, "cs_cancel_lock_"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	customer := cart.NewStore(storeApplicationPool(t, "customer-cancel-behind-open"))
	done := make(chan error, 1)
	go func() {
		cancelErr := customer.CancelOrder(ctx, number)
		done <- cancelErr
	}()
	waitForApplicationLock(t, "customer-cancel-behind-open", done)
	if err := opener.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, cart.ErrNotCancellable) {
		t.Errorf("cancellation behind payment open = %v, want ErrNotCancellable", err)
	}
	if f := factsOf(t, number, uuid.Nil); f.status != "pending" || f.heldHolds != 1 || f.events != 0 || f.notices != 0 {
		t.Errorf("payment committed during wait did not protect order: %+v", f)
	}
}

func TestCustomerCancellationAfterSessionExpiryReleasesStock(t *testing.T) {
	ctx := t.Context()
	variant := freshVariant(t, "cancel-expired-session")
	orderID := heldOrder(t, variant, -10*time.Minute, false)
	ref := "cs_cancel_expired_" + uuid.NewString()
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT cancel_payment($1)`, ref); err != nil {
		t.Fatal(err)
	}
	before := stockOf(t, variant)
	number := numberOf(t, orderID)
	if err := cart.NewStore(storeRolePool(t)).CancelOrder(ctx, number); err != nil {
		t.Fatalf("cancel after session expiry: %v", err)
	}
	if f := factsOf(t, number, uuid.Nil); f.status != "cancelled" || f.heldHolds != 0 || f.events != 1 || f.notices != 1 {
		t.Errorf("expired-session cancellation effects: %+v", f)
	}
	if got := stockOf(t, variant); got != before+1 {
		t.Errorf("stock after cancellation = %d, want %d", got, before+1)
	}
}
