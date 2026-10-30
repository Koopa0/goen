//go:build integration

package payment_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/payment"
)

// TestSessionExpirer_ExpireOpen: ExpireOpen expires the order's open Checkout
// Session at Stripe and records the payment cancelled, as checkout.session.expired
// would. A session Stripe has already expired, which it refuses to expire again,
// is recorded the same way, and an order with no open session asks Stripe
// nothing. A client that has gone away does not stop the record being written
// once Stripe has expired the session.
func TestSessionExpirer_ExpireOpen(t *testing.T) {
	for _, tc := range []struct {
		name      string
		atStripe  string
		gone      bool
		wantState string
	}{
		{name: "open", atStripe: "open", wantState: "expired"},
		{name: "already expired", atStripe: "expired", wantState: "expired"},
		{name: "client gone", atStripe: "open", gone: true, wantState: "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stripeAPI, gateway := newStripeSessions(t)
			store := payment.NewStore(storeRolePool(t))
			number, _, ref := openOrder(t, store, 100000, "expire-open")
			stripeAPI.set(ref, tc.atStripe)

			ctx := t.Context()
			if tc.gone {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := payment.NewSessionExpirer(store, gateway).ExpireOpen(ctx, number); err != nil {
				t.Fatalf("ExpireOpen = %v, want nil", err)
			}
			if got := paymentStatus(t, ref); got != "cancelled" {
				t.Errorf("payment is %s, want cancelled", got)
			}
			if got := stripeAPI.state(ref); got != tc.wantState {
				t.Errorf("the session is %s at Stripe, want %s", got, tc.wantState)
			}
		})
	}

	t.Run("nothing open", func(t *testing.T) {
		stripeAPI, gateway := newStripeSessions(t)
		store := payment.NewStore(storeRolePool(t))
		number, _, ref := openOrder(t, store, 100000, "expire-none")
		stripeAPI.set(ref, "expired")
		if _, err := pool.Exec(t.Context(), `SELECT cancel_payment($1)`, ref); err != nil {
			t.Fatal(err)
		}
		unpaid, _ := order(t, 100000)
		for _, n := range []string{number, unpaid} {
			if err := payment.NewSessionExpirer(store, gateway).ExpireOpen(t.Context(), n); err != nil {
				t.Errorf("ExpireOpen(%s) = %v, want nil", n, err)
			}
		}
		if got := stripeAPI.asked(ref); got != 0 {
			t.Errorf("Stripe was asked about a session that had ended %d times, want none", got)
		}
	})
}

// TestSessionExpirer_ExpireOpenRefused: ExpireOpen answers ErrStillPayable and
// records nothing when Stripe says the session completed, will not expire it,
// or has no record of it. In each the customer may have paid, so the payment
// stays open and the session is left as Stripe holds it.
func TestSessionExpirer_ExpireOpenRefused(t *testing.T) {
	for _, tc := range []struct {
		name     string
		atStripe string
	}{
		{name: "completed", atStripe: "complete"},
		{name: "unknown to Stripe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stripeAPI, gateway := newStripeSessions(t)
			store := payment.NewStore(storeRolePool(t))
			number, _, ref := openOrder(t, store, 100000, "expire-refused")
			if tc.atStripe != "" {
				stripeAPI.set(ref, tc.atStripe)
			}

			err := payment.NewSessionExpirer(store, gateway).ExpireOpen(t.Context(), number)
			if !errors.Is(err, payment.ErrStillPayable) {
				t.Errorf("ExpireOpen = %v, want ErrStillPayable", err)
			}
			if got := paymentStatus(t, ref); got != "requires_payment" {
				t.Errorf("payment is %s, want requires_payment", got)
			}
			if got := stripeAPI.state(ref); got != tc.atStripe {
				t.Errorf("the session is %q at Stripe, want it left %q", got, tc.atStripe)
			}
		})
	}

	t.Run("expiry fails", func(t *testing.T) {
		store := payment.NewStore(storeRolePool(t))
		number, _, ref := openOrder(t, store, 100000, "expire-fails")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/expire") {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = fmt.Fprint(w, `{"error":{"type":"api_error","message":"Stripe is unavailable"}}`)
				return
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":"open",`+
				`"url":"https://checkout.stripe.com/c/pay/%s"}`, ref, ref)
		}))
		t.Cleanup(srv.Close)

		err := payment.NewSessionExpirer(store, gatewayAt(t, srv.URL)).ExpireOpen(t.Context(), number)
		if !errors.Is(err, payment.ErrStillPayable) {
			t.Errorf("ExpireOpen = %v, want ErrStillPayable", err)
		}
		if got := paymentStatus(t, ref); got != "requires_payment" {
			t.Errorf("payment is %s, want requires_payment", got)
		}
	})

	t.Run("no Stripe key", func(t *testing.T) {
		store := payment.NewStore(storeRolePool(t))
		number, _, ref := openOrder(t, store, 100000, "expire-disabled")
		err := payment.NewSessionExpirer(store, disabledGateway(t)).ExpireOpen(t.Context(), number)
		if !errors.Is(err, payment.ErrStillPayable) {
			t.Errorf("ExpireOpen = %v, want ErrStillPayable", err)
		}
		if got := paymentStatus(t, ref); got != "requires_payment" {
			t.Errorf("payment is %s, want requires_payment", got)
		}
	})
}
