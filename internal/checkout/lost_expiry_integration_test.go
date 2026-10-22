//go:build integration

package checkout_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
)

// endpointSigning signs the stand-in's webhooks; it authenticates nothing real.
const endpointSigning = "whsec_lostexpiryfixtureonly"

// stripeSessions is a Stripe stand-in that knows the Checkout Sessions a test
// gives it and answers 404 for any other, as Stripe does for a session it has
// never seen. Other tests leave lapsed checkouts in this database; they are
// asked about, refused, and left alone.
type stripeSessions struct {
	mu        sync.Mutex
	status    map[string]string
	retrieved map[string]int
}

func newStripeSessions(t *testing.T) (*stripeSessions, *payment.Gateway) {
	t.Helper()
	s := &stripeSessions{status: map[string]string{}, retrieved: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	gateway, err := payment.NewGatewayAt("sk_test_notreal", endpointSigning, "https://goen.example", srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	return s, gateway
}

func (s *stripeSessions) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path, expire := strings.CutSuffix(r.URL.Path, "/expire")
	s.mu.Lock()
	defer s.mu.Unlock()
	// The id written back is the stand-in's own, never the request's.
	var id string
	for known := range s.status {
		if path == "/v1/checkout/sessions/"+known {
			id = known
		}
	}
	status := s.status[id]
	switch {
	case id == "":
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","code":"resource_missing",`+
			`"message":"No such checkout.session"}}`)
		return
	case expire && r.Method == http.MethodPost:
		if status != "open" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error",`+
				`"message":"only an open Checkout Session can be expired"}}`)
			return
		}
		status = "expired"
		s.status[id] = status
	case r.Method == http.MethodGet:
		s.retrieved[id]++
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":%q,`+
		`"url":"https://checkout.stripe.com/c/pay/%s"}`, id, status, id)
}

func (s *stripeSessions) set(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[id] = status
}

func (s *stripeSessions) state(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status[id]
}

func (s *stripeSessions) asked(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retrieved[id]
}

// lostExpiryOrder is lapsedOrder, its credit and coupon spent, with a checkout
// opened through the payment door and every hold run out ago.
func lostExpiryOrder(t *testing.T, label string, ago time.Duration) (number, ref string, accountID, couponID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	number, accountID, couponID = lapsedOrder(t, label)
	var owed int64
	if err := pool.QueryRow(ctx, `
		SELECT order_amount_after_credit(id) FROM orders WHERE order_number = $1`, number).Scan(&owed); err != nil {
		t.Fatalf("read what %s owes: %v", number, err)
	}
	if owed <= 0 {
		t.Fatalf("%s owes %d; a part-credit checkout must still owe something", number, owed)
	}
	ref = "cs_lost_expiry_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if err := payment.NewStore(pool).OpenPayment(ctx, number, ref, owed); err != nil {
		t.Fatalf("open the checkout of %s: %v", number, err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - $2::interval - interval '30 minutes', expires_at = now() - $2::interval
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`,
		number, pgtype.Interval{Microseconds: ago.Microseconds(), Valid: true}); err != nil {
		t.Fatalf("age the holds of %s: %v", number, err)
	}
	return number, ref, accountID, couponID
}

// sweepSessionsAs runs one pass of the payment sweeper on p. Sessions the
// stand-in does not know are deferred with a warning; an error is a failure.
func sweepSessionsAs(t *testing.T, p *pgxpool.Pool, gateway *payment.Gateway) payment.SessionSweep {
	t.Helper()
	var logged sweepLog
	done, err := payment.NewSessionSweeper(payment.NewStore(p), gateway).
		Sweep(t.Context(), slog.New(slog.NewTextHandler(&logged, nil)))
	if err != nil {
		t.Fatalf("session sweep: %v", err)
	}
	if failures := logged.errors(); failures != "" {
		t.Fatalf("session sweep logged failures:\n%s", failures)
	}
	return done
}

func paymentStatus(t *testing.T, ref string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(t.Context(),
		`SELECT status FROM payments WHERE provider_ref = $1`, ref).Scan(&status); err != nil {
		t.Fatalf("read payment %s: %v", ref, err)
	}
	return status
}

// lapsedCheckouts is the figure /admin/health shows for checkouts left
// unsettled past health.LapsedCheckoutAfter.
func lapsedCheckouts(t *testing.T) int64 {
	t.Helper()
	view, err := health.NewStore(pool).WorkerHealth(t.Context(),
		outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("read health: %v", err)
	}
	return view.LapsedCheckouts
}

// assertGivenBack is the state a delivered expiry leads to: the order cancelled
// at its deadline once, the credit and coupon back once, and no notice that
// says money was taken.
func assertGivenBack(t *testing.T, number, ref string, accountID, couponID uuid.UUID) {
	t.Helper()
	f := factsOf(t, number, couponID)
	if f.status != "cancelled" || f.events != 1 || f.deadlineNotices != 1 || f.notices != 1 {
		t.Fatalf("order is %s with %d cancelled events and %d notices (%d at the deadline); want cancelled with one of each",
			f.status, f.events, f.notices, f.deadlineNotices)
	}
	if f.reversals != 1 || f.couponSlotsHeld != 0 {
		t.Errorf("%d credit reversals and %d coupon slots held; want 1 and 0", f.reversals, f.couponSlotsHeld)
	}
	if got := creditBalance(t, accountID); got != 30000 {
		t.Errorf("balance = %d, want the 30000 spent on the order back", got)
	}
	if got := paymentStatus(t, ref); got != "cancelled" {
		t.Errorf("payment is %s, want cancelled", got)
	}
	if noticeRefunded(t, number) {
		t.Error("the notice of a checkout Stripe expired says money was taken")
	}
}

// TestSessionSweeper_SweepLostExpiry: Sweep settles a part-credit checkout
// whose Stripe expiry event never arrived, expiring a still-open session
// itself, so the order is cancelled at its deadline and its credit and coupon
// come back. It is #1966: Stripe never delivers checkout.session.expired for an
// abandoned part-credit checkout. The hold sweeper alone cannot tell the
// checkout ended, so it keeps the order; the payment sweeper asks Stripe, and
// the order then lapses as if the event had come. A session Stripe has not yet
// expired past its order's hold is expired by goen first.
func TestSessionSweeper_SweepLostExpiry(t *testing.T) {
	for _, atStripe := range []string{"expired", "open"} {
		t.Run(atStripe, func(t *testing.T) {
			stripeAPI, gateway := newStripeSessions(t)
			store := storeRolePool(t)
			before := lapsedCheckouts(t)

			number, ref, accountID, couponID := lostExpiryOrder(t, "lost-expiry", 2*time.Hour)
			stripeAPI.set(ref, atStripe)

			sweepAs(t, store)
			f := factsOf(t, number, couponID)
			if f.status != "pending" || f.couponSlotsHeld != 1 || creditBalance(t, accountID) != 0 {
				t.Fatalf("with the checkout still payable the hold sweeper left %s with %d coupon slots and balance %d; "+
					"want pending, 1 and 0", f.status, f.couponSlotsHeld, creditBalance(t, accountID))
			}
			if got := lapsedCheckouts(t); got != before+1 {
				t.Fatalf("health counts %d unsettled lapsed checkouts, want %d", got, before+1)
			}

			if done := sweepSessionsAs(t, store, gateway); done.Expired < 1 {
				t.Fatalf("session sweep = %+v, want this checkout expired", done)
			}
			sweepAs(t, store)

			assertGivenBack(t, number, ref, accountID, couponID)
			if got := stripeAPI.state(ref); got != "expired" {
				t.Errorf("the session is %s at Stripe, want expired", got)
			}
			if got := lapsedCheckouts(t); got != before {
				t.Errorf("health counts %d unsettled lapsed checkouts after the sweep, want %d", got, before)
			}
		})
	}
}

// expiryEvent is the checkout.session.expired Stripe would have sent.
func expiryEvent(t *testing.T, ref string) (eventID string, body []byte, header string) {
	t.Helper()
	eventID = "evt_lost_expiry_" + uuid.NewString()[:12]
	body = fmt.Appendf(nil, `{"id":%q,"object":"event","api_version":%q,"type":"checkout.session.expired",`+
		`"created":%d,"data":{"object":{"id":%q,"object":"checkout.session","status":"expired",`+
		`"payment_status":"unpaid"}}}`, eventID, stripe.APIVersion, time.Now().Unix(), ref)
	p := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{Payload: body, Secret: endpointSigning})
	return eventID, p.Payload, p.Header
}

// TestSessionSweeper_SweepTwice: A second pass of either sweeper, and the real
// expiry event arriving after all, find nothing left to do: Stripe is asked
// once and the order is given back once.
func TestSessionSweeper_SweepTwice(t *testing.T) {
	ctx := t.Context()
	stripeAPI, gateway := newStripeSessions(t)
	store := storeRolePool(t)
	number, ref, accountID, couponID := lostExpiryOrder(t, "lost-expiry-twice", time.Hour)
	stripeAPI.set(ref, "expired")

	// A second payment pass before the hold sweeper has cancelled the order,
	// then a second hold pass.
	for range 2 {
		sweepSessionsAs(t, store, gateway)
	}
	for range 2 {
		sweepAs(t, store)
	}

	eventID, body, header := expiryEvent(t, ref)
	h := payment.NewHandler(payment.NewStore(store), gateway, access.NewStore(store, false),
		slog.New(slog.DiscardHandler))
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)
	res := httptest.NewRecorder()
	h.Webhook(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("the late expiry webhook answered %d, want 200", res.Code)
	}
	var processed, unreconciled bool
	if err := pool.QueryRow(ctx, `
		SELECT processed_at IS NOT NULL, unreconciled IS NOT NULL
		FROM payment_webhook_events WHERE event_id = $1`, eventID).Scan(&processed, &unreconciled); err != nil {
		t.Fatalf("read the late event: %v", err)
	}
	if !processed || unreconciled {
		t.Errorf("late event processed %t, unreconciled %t; want processed and not flagged", processed, unreconciled)
	}
	sweepAs(t, store)
	sweepSessionsAs(t, store, gateway)

	if got := stripeAPI.asked(ref); got != 1 {
		t.Errorf("Stripe was asked about the session %d times, want once", got)
	}
	assertGivenBack(t, number, ref, accountID, couponID)
}

// TestSessionSweeper_SweepNotLostExpiry: Sweep leaves a payable checkout alone
// unless its expiry was lost: while the event may still come, once paid, with
// an unresolved provider event, and when complete at Stripe it neither cancels
// the order nor returns its credit and coupon. Each case is a payable checkout
// on a part-credit, coupon order that the sweeper must not settle, or whose
// credit and coupon must not come back because money may have arrived. Stripe
// would answer expired for every session it is asked about, so asking alone
// would be enough to change the order.
func TestSessionSweeper_SweepNotLostExpiry(t *testing.T) {
	for _, tc := range []struct {
		name string
		// arrange turns a lost-expiry checkout into the case.
		arrange   func(t *testing.T, stripeAPI *stripeSessions, number, ref string)
		ago       time.Duration
		wantAsked int
		wantPay   string
	}{
		{
			name: "the event may still come", ago: time.Minute,
			wantAsked: 0, wantPay: "requires_payment",
		},
		{
			name: "paid", ago: time.Hour,
			arrange: func(t *testing.T, _ *stripeSessions, number, ref string) {
				t.Helper()
				if _, err := pool.Exec(t.Context(), `
					SELECT capture_payment($1, order_amount_after_credit(o.id), NULL, NULL)
					FROM orders o WHERE o.order_number = $2`, ref, number); err != nil {
					t.Fatalf("capture: %v", err)
				}
			},
			wantAsked: 0, wantPay: "succeeded",
		},
		{
			name: "an unresolved provider event needs a person", ago: time.Hour,
			arrange: func(t *testing.T, _ *stripeSessions, _, ref string) {
				t.Helper()
				if _, err := pool.Exec(t.Context(), `
					INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload, processed_at, unreconciled)
					VALUES ('stripe', $1, 'checkout.session.completed', $2, '{}', now(), 'unsettled_session: lost expiry fixture')`,
					"evt_lost_expiry_"+uuid.NewString()[:12], ref); err != nil {
					t.Fatalf("record the unresolved event: %v", err)
				}
			},
			wantAsked: 0, wantPay: "requires_payment",
		},
		{
			name: "complete at Stripe", ago: time.Hour,
			arrange: func(t *testing.T, stripeAPI *stripeSessions, _, ref string) {
				t.Helper()
				stripeAPI.set(ref, "complete")
			},
			wantAsked: 1, wantPay: "requires_reconciliation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stripeAPI, gateway := newStripeSessions(t)
			store := storeRolePool(t)
			number, ref, accountID, couponID := lostExpiryOrder(t, "lost-expiry-keep", tc.ago)
			stripeAPI.set(ref, "expired")
			if tc.arrange != nil {
				tc.arrange(t, stripeAPI, number, ref)
			}

			sweepSessionsAs(t, store, gateway)
			sweepAs(t, store)

			if got := stripeAPI.asked(ref); got != tc.wantAsked {
				t.Errorf("Stripe was asked about the session %d times, want %d", got, tc.wantAsked)
			}
			if got := paymentStatus(t, ref); got != tc.wantPay {
				t.Errorf("payment is %s, want %s", got, tc.wantPay)
			}
			f := factsOf(t, number, couponID)
			if f.status == "cancelled" || f.events != 0 || f.reversals != 0 {
				t.Errorf("the order was cancelled: status %s, %d cancelled events, %d credit reversals",
					f.status, f.events, f.reversals)
			}
			if f.couponSlotsHeld != 1 || creditBalance(t, accountID) != 0 {
				t.Errorf("%d coupon slots held and balance %d; want the coupon and credit still on the order",
					f.couponSlotsHeld, creditBalance(t, accountID))
			}
		})
	}
}

// TestSessionSweeper_SweepLateExpiryRace: A late expiry webhook and Sweep settling
// the same session, in either order, both converge on cancelled without
// failing, and the credit and coupon come back once. The real event arrives
// while the sweeper is settling the same session. Both write through the
// provider reference lock and both converge on cancelled, so whichever holds
// the lock first, neither fails and the credit and coupon come back once.
func TestSessionSweeper_SweepLateExpiryRace(t *testing.T) {
	// webhook applies the expiry as the handler does, holding its transaction
	// open after the effect until release.
	webhook := func(ctx context.Context, p *pgxpool.Pool, ref, eventID string, body []byte,
		applied chan<- struct{}, release <-chan struct{},
	) error {
		_, err := payment.NewStore(p).ProcessWebhook(ctx, &payment.WebhookEvent{
			ID: eventID, Type: "checkout.session.expired", ObjectRef: ref, Payload: body,
		}, func(ctx context.Context, tx *payment.WebhookTx) error {
			if err := tx.CancelPaymentRow(ctx); err != nil {
				return err
			}
			close(applied)
			<-release
			return nil
		})
		return err
	}
	sweep := func(ctx context.Context, p *pgxpool.Pool, gateway *payment.Gateway) error {
		var logged sweepLog
		_, err := payment.NewSessionSweeper(payment.NewStore(p), gateway).
			Sweep(ctx, slog.New(slog.NewTextHandler(&logged, nil)))
		if err == nil && logged.errors() != "" {
			err = fmt.Errorf("logged failures:\n%s", logged.errors())
		}
		return err
	}

	t.Run("webhook first", func(t *testing.T) {
		ctx := t.Context()
		stripeAPI, gateway := newStripeSessions(t)
		number, ref, accountID, couponID := lostExpiryOrder(t, "lost-expiry-webhook-first", time.Hour)
		stripeAPI.set(ref, "expired")

		eventID, body, _ := expiryEvent(t, ref)
		hookPool := storeRolePool(t)
		applied, release := make(chan struct{}), make(chan struct{})
		webhookDone := make(chan error, 1)
		go func() { webhookDone <- webhook(ctx, hookPool, ref, eventID, body, applied, release) }()
		<-applied

		const name = "lost-expiry-sweep-behind-webhook"
		sweepPool := storeApplicationPool(t, name)
		sweepDone := make(chan error, 1)
		go func() { sweepDone <- sweep(ctx, sweepPool, gateway) }()
		waitForApplicationLock(t, name, sweepDone)
		close(release)

		if err := <-webhookDone; err != nil {
			t.Fatalf("webhook: %v", err)
		}
		if err := <-sweepDone; err != nil {
			t.Fatalf("session sweep: %v", err)
		}
		sweepAs(t, storeRolePool(t))
		assertGivenBack(t, number, ref, accountID, couponID)
	})

	t.Run("sweep first", func(t *testing.T) {
		ctx := t.Context()
		stripeAPI, gateway := newStripeSessions(t)
		number, ref, accountID, couponID := lostExpiryOrder(t, "lost-expiry-sweep-first", time.Hour)
		stripeAPI.set(ref, "expired")

		// The sweeper takes the provider lock and then waits here for the order.
		blocker, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin blocker: %v", err)
		}
		defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
		if _, err := blocker.Exec(ctx, `SELECT 1 FROM orders WHERE order_number = $1 FOR UPDATE`, number); err != nil {
			t.Fatalf("lock the order: %v", err)
		}

		const sweeper, hook = "lost-expiry-sweep-first", "lost-expiry-webhook-behind-sweep"
		sweepPool, hookPool := storeApplicationPool(t, sweeper), storeApplicationPool(t, hook)
		eventID, body, _ := expiryEvent(t, ref)
		sweepDone := make(chan error, 1)
		go func() { sweepDone <- sweep(ctx, sweepPool, gateway) }()
		waitForApplicationLock(t, sweeper, sweepDone)

		applied, release := make(chan struct{}), make(chan struct{})
		close(release)
		webhookDone := make(chan error, 1)
		go func() { webhookDone <- webhook(ctx, hookPool, ref, eventID, body, applied, release) }()
		waitForApplicationLock(t, hook, webhookDone)

		if err := blocker.Commit(ctx); err != nil {
			t.Fatalf("release the order: %v", err)
		}
		if err := <-sweepDone; err != nil {
			t.Fatalf("session sweep: %v", err)
		}
		if err := <-webhookDone; err != nil {
			t.Fatalf("webhook: %v", err)
		}
		sweepAs(t, storeRolePool(t))
		assertGivenBack(t, number, ref, accountID, couponID)
	})
}
