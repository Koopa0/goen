//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/lapse"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/user"
)

// TestReconciliationRolesHaveOneDoor keeps the webhook alarm monotone. The
// store may raise it but cannot clear or rewrite it; admin may resolve it only
// through the function that atomically closes the linked live payment.
func TestReconciliationRolesHaveOneDoor(t *testing.T) {
	ctx := t.Context()
	number, _ := order(t, 105000)
	session := "cs_role_reconcile_" + uuid.NewString()[:12]
	if err := payment.NewStore(pool).OpenPayment(ctx, number, session, 105000); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	eventID := "evt_role_reconcile_" + uuid.NewString()[:12]
	processedEventID := "evt_role_processed_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events
			(provider, event_id, type, object_ref, payload, processed_at)
		VALUES
			('stripe', $1, 'checkout.session.completed', $2, '{}'::jsonb, NULL),
			('stripe', $3, 'checkout.session.completed', $2, '{}'::jsonb, now())`,
		eventID, session, processedEventID); err != nil {
		t.Fatalf("record webhook event: %v", err)
	}

	conn, acquireErr := pool.Acquire(ctx)
	if acquireErr != nil {
		t.Fatalf("acquire role connection: %v", acquireErr)
	}
	defer conn.Release()
	defer func() {
		if _, resetErr := conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`); resetErr != nil {
			t.Errorf("reset role: %v", resetErr)
		}
	}()
	requireDenied := func(t *testing.T, err error) {
		t.Helper()
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.Code != "42501" {
			t.Fatalf("operation returned %v, want SQLSTATE 42501 insufficient_privilege", err)
		}
	}

	if _, err := conn.Exec(ctx, `SET ROLE store`); err != nil {
		t.Fatalf("set store role: %v", err)
	}
	var marked bool
	if err := conn.QueryRow(ctx, `SELECT mark_payment_event_unreconciled($1, $2)`,
		eventID, "refused_capture: role boundary test").Scan(&marked); err != nil || !marked {
		t.Fatalf("store mark_payment_event_unreconciled = %v, %v; want true", marked, err)
	}
	if err := conn.QueryRow(ctx, `SELECT mark_payment_event_unreconciled($1, $2)`,
		processedEventID, "refused_capture: must not rewrite a completed outcome").Scan(&marked); err != nil {
		t.Fatalf("mark an already processed event: %v", err)
	} else if marked {
		t.Fatal("mark_payment_event_unreconciled rewrote an already processed clean event")
	}
	var processedReason *string
	if err := conn.QueryRow(ctx, `SELECT unreconciled FROM payment_webhook_events
		WHERE provider = 'stripe' AND event_id = $1`, processedEventID).Scan(&processedReason); err != nil {
		t.Fatalf("read already processed event: %v", err)
	}
	if processedReason != nil {
		t.Errorf("already processed event gained alarm %q", *processedReason)
	}
	if _, err := conn.Exec(ctx, `UPDATE payment_webhook_events SET processed_at = now()
		WHERE provider = 'stripe' AND event_id = $1`, eventID); err != nil {
		t.Fatalf("store cannot stamp processed_at: %v", err)
	}
	_, deniedErr := conn.Exec(ctx, `UPDATE payment_webhook_events SET unreconciled = NULL
		WHERE provider = 'stripe' AND event_id = $1`, eventID)
	requireDenied(t, deniedErr)
	_, deniedErr = conn.Exec(ctx, `UPDATE payment_webhook_events SET reconciled_at = now()
		WHERE provider = 'stripe' AND event_id = $1`, eventID)
	requireDenied(t, deniedErr)
	var reconciled bool
	deniedErr = conn.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).Scan(&reconciled)
	requireDenied(t, deniedErr)

	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatalf("reset store role: %v", err)
	}
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("switch to admin role: %v", err)
	}
	_, deniedErr = conn.Exec(ctx, `UPDATE payment_webhook_events SET reconciled_at = now()
		WHERE provider = 'stripe' AND event_id = $1`, eventID)
	requireDenied(t, deniedErr)
	deniedErr = conn.QueryRow(ctx, `SELECT mark_payment_event_unreconciled($1, $2)`,
		eventID, "admin must not rewrite the alarm").Scan(&marked)
	requireDenied(t, deniedErr)
	if err := conn.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).Scan(&reconciled); err != nil || !reconciled {
		t.Fatalf("admin release_payment_event = %v, %v; want true", reconciled, err)
	}
	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatalf("reset admin role: %v", err)
	}

	var status, reason string
	var wasReconciled bool
	if err := conn.QueryRow(ctx, `
		SELECT p.status, e.unreconciled, e.reconciled_at IS NOT NULL
		FROM payments p
		JOIN payment_webhook_events e
		  ON e.provider = p.provider AND e.object_ref = p.provider_ref
		WHERE e.provider = 'stripe' AND e.event_id = $1`, eventID).
		Scan(&status, &reason, &wasReconciled); err != nil {
		t.Fatalf("read reconciled state: %v", err)
	}
	if status != "cancelled" || reason != "refused_capture: role boundary test" || !wasReconciled {
		t.Errorf("reconciled state = %q/%q/%v, want cancelled/original reason/true",
			status, reason, wasReconciled)
	}
}

func TestCompletePaymentResolutionIsAdminOnly(t *testing.T) {
	ctx := t.Context()
	_, orderID := order(t, 105500)
	providerRef := "cs_complete_role_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx,
		`SELECT record_complete_payment($1, $2, 105500)`, orderID, providerRef); err != nil {
		t.Fatalf("record complete payment: %v", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire role connection: %v", err)
	}
	defer conn.Release()
	defer func() {
		if _, resetErr := conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`); resetErr != nil {
			t.Errorf("reset role: %v", resetErr)
		}
	}()

	if _, err := conn.Exec(ctx, `SET ROLE store`); err != nil {
		t.Fatalf("set store role: %v", err)
	}
	for _, statement := range []string{
		`SELECT release_complete_payment($1)`,
		`SELECT * FROM attribute_complete_payment_paid($1)`,
	} {
		_, roleErr := conn.Exec(ctx, statement, providerRef)
		pgErr, ok := errors.AsType[*pgconn.PgError](roleErr)
		if !ok || pgErr.Code != "42501" {
			t.Errorf("store %q = %v, want SQLSTATE 42501", statement, roleErr)
		}
	}

	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatalf("reset store role: %v", err)
	}
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set admin role: %v", err)
	}
	var released bool
	if err := conn.QueryRow(ctx, `SELECT release_complete_payment($1)`, providerRef).
		Scan(&released); err != nil || !released {
		t.Fatalf("admin release_complete_payment = %v, %v; want true", released, err)
	}

	if _, err := conn.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatalf("reset admin role: %v", err)
	}
	var status string
	if err := conn.QueryRow(ctx,
		`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); err != nil {
		t.Fatalf("read complete payment: %v", err)
	}
	if status != "reconciled" {
		t.Errorf("admin resolution left status %q, want reconciled", status)
	}
}

// TestPaidCompleteResolutionCannotOpenSecondSession covers the irreversible
// branch of the operator door. A complete Session confirmed paid is captured
// through the same database invariants and side effects as a signed webhook;
// Start must then stop before making any second Stripe Session.
func TestPaidCompleteResolutionCannotOpenSecondSession(t *testing.T) {
	ctx := t.Context()
	const amount = int64(125000)

	var customerID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role)
		VALUES ('complete-paid-' || gen_random_uuid() || '@goen.invalid', 'customer')
		RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	number, orderID := ownedOrderHolding(t, customerID, amount, 1)
	hold(t, orderID, 0, 60*time.Minute, "complete-paid:"+number)
	providerRef := "cs_complete_paid_" + uuid.NewString()[:12]
	if _, err := pool.Exec(ctx,
		`SELECT record_complete_payment($1, $2, $3)`, orderID, providerRef, amount); err != nil {
		t.Fatalf("record complete payment: %v", err)
	}
	backOffice := health.NewStore(pool)
	if err := backOffice.ReconcileCompletePayment(
		ctx, providerRef, health.CompletePaymentPaid,
	); !errors.Is(err, audit.ErrNoActor) {
		t.Fatalf("paid attribution without an auditable actor = %v, want ErrNoActor", err)
	}
	var rolledBackStatus string
	var rolledBackEffects int64
	if err := pool.QueryRow(ctx, `
		SELECT p.status,
		       (SELECT count(*) FROM order_events e
		        WHERE e.order_id = p.order_id AND e.kind = 'paid')
		FROM payments p WHERE p.provider_ref = $1`, providerRef).
		Scan(&rolledBackStatus, &rolledBackEffects); err != nil {
		t.Fatalf("read failed attribution transaction: %v", err)
	}
	if rolledBackStatus != "requires_reconciliation" || rolledBackEffects != 0 {
		t.Fatalf("failed audit left capture/effects = %q/%d, want requires_reconciliation/0",
			rolledBackStatus, rolledBackEffects)
	}

	var actorID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role)
		VALUES ('complete-operator-' || gen_random_uuid() || '@goen.invalid', 'admin')
		RETURNING id`).Scan(&actorID); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	adminCtx := user.NewContext(ctx, user.User{ID: actorID.String(), Role: user.RoleAdmin})
	if err := backOffice.ReconcileCompletePayment(
		adminCtx, providerRef, health.CompletePaymentPaid,
	); err != nil {
		t.Fatalf("attribute complete payment as paid: %v", err)
	}

	var status string
	var captured int64
	if err := pool.QueryRow(ctx, `
		SELECT status, captured_amount_cents
		FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status, &captured); err != nil {
		t.Fatalf("read attributed payment: %v", err)
	}
	if status != "succeeded" || captured != amount {
		t.Fatalf("attributed payment = %q/%d, want succeeded/%d", status, captured, amount)
	}

	assertSideEffects := func() {
		t.Helper()
		for _, assertion := range []struct {
			name string
			sql  string
			args []any
			want int64
		}{
			{"paid timeline event", `SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'paid'`, []any{orderID}, 1},
			{"paid receipt", `SELECT count(*) FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $1`, []any{number}, 1},
			{"loyalty points", `SELECT coalesce(sum(points), 0) FROM loyalty_entries WHERE order_id = $1`, []any{orderID}, amount / 10000},
		} {
			var got int64
			if err := pool.QueryRow(ctx, assertion.sql, assertion.args...).Scan(&got); err != nil {
				t.Fatalf("read %s: %v", assertion.name, err)
			}
			if got != assertion.want {
				t.Errorf("%s = %d, want %d", assertion.name, got, assertion.want)
			}
		}
	}
	assertSideEffects()

	var auditResolution string
	if err := pool.QueryRow(ctx, `
		SELECT after->>'resolution' FROM audit_events
		WHERE action = 'payment.reconciled' AND after->>'provider_ref' = $1
		ORDER BY occurred_at DESC LIMIT 1`, providerRef).Scan(&auditResolution); err != nil {
		t.Fatalf("read paid attribution audit: %v", err)
	}
	if auditResolution != "paid_attributed" {
		t.Errorf("audit resolution = %q, want paid_attributed", auditResolution)
	}

	gateway, createCalls := gatewayRecordingCalls(t, "cs_must_not_open")
	h := payment.NewHandler(payment.NewStore(pool), gateway, access.NewStore(pool, false),
		slog.New(slog.DiscardHandler))
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/pay", http.NoBody)
	req.SetPathValue("number", number)
	placedBy(t, req, number)
	res := httptest.NewRecorder()
	h.Start(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number {
		t.Fatalf("Start after paid attribution = %d %q, want 303 to order",
			res.Code, res.Header().Get("Location"))
	}
	if *createCalls != 0 {
		t.Fatalf("Start created %d Stripe Sessions after paid attribution, want 0", *createCalls)
	}

	// A later signed paid event is still processed, but manual attribution and
	// its effects are idempotent under the shared provider-reference lock.
	if _, err := captureThroughWebhook(t, payment.NewStore(pool), payment.Capture{
		SessionID: providerRef, AmountRecv: amount,
	}); err != nil {
		t.Fatalf("late paid webhook: %v", err)
	}
	assertSideEffects()
}

// TestAdmittedCompleteSessionsBecomeVisibleAndResolvable covers both ways an
// already-admitted Checkout can be complete before its webhook arrives. The
// matching-amount resume and obsolete-amount retirement must both consume the
// original intent as requires_reconciliation, appear in admin health, and only
// advance a generation after the explicit unpaid/refunded outcome.
func TestAdmittedCompleteSessionsBecomeVisibleAndResolvable(t *testing.T) {
	for _, tt := range []struct {
		name            string
		shippingCents   int64
		firstHTTPStatus int
	}{
		{name: "matching amount", firstHTTPStatus: http.StatusSeeOther},
		{name: "obsolete amount", shippingCents: 30000, firstHTTPStatus: http.StatusConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			const originalAmount = int64(100000)
			number, orderID := holdableOrder(t, originalAmount)
			hold(t, orderID, 0, 60*time.Minute, "admitted-complete:"+number)
			oldSession := "cs_admitted_complete_" + uuid.NewString()[:12]
			s := payment.NewStore(pool)
			if err := s.OpenPayment(ctx, number, oldSession, originalAmount); err != nil {
				t.Fatalf("open admitted payment: %v", err)
			}
			if tt.shippingCents != 0 {
				if _, err := pool.Exec(ctx,
					`UPDATE orders SET shipping_cents = $2 WHERE id = $1`, orderID, tt.shippingCents); err != nil {
					t.Fatalf("change amount owed: %v", err)
				}
			}
			currentAmount := originalAmount + tt.shippingCents
			replacement := "cs_after_complete_" + uuid.NewString()[:12]

			var remote struct {
				mu         sync.Mutex
				createKeys []string
				oldReads   int
				newReads   int
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+oldSession:
					remote.mu.Lock()
					remote.oldReads++
					remote.mu.Unlock()
					_, _ = fmt.Fprintf(w,
						`{"id":%q,"object":"checkout.session","status":"complete",`+
							`"payment_status":"unpaid"}`, oldSession)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
					remote.mu.Lock()
					remote.createKeys = append(remote.createKeys, r.Header.Get("Idempotency-Key"))
					remote.mu.Unlock()
					_, _ = fmt.Fprintf(w,
						`{"id":%q,"object":"checkout.session","status":"open",`+
							`"url":"https://checkout.stripe.com/c/pay/%s"}`, replacement, replacement)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+replacement:
					remote.mu.Lock()
					remote.newReads++
					remote.mu.Unlock()
					_, _ = fmt.Fprintf(w,
						`{"id":%q,"object":"checkout.session","status":"open",`+
							`"url":"https://checkout.stripe.com/c/pay/%s"}`, replacement, replacement)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)

			gateway := gatewayAt(t, srv.URL)
			h := payment.NewHandler(s, gateway, access.NewStore(pool, false),
				slog.New(slog.DiscardHandler))
			post := func() *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequestWithContext(ctx, http.MethodPost,
					"/orders/"+number+"/pay", http.NoBody)
				req.SetPathValue("number", number)
				placedBy(t, req, number)
				res := httptest.NewRecorder()
				h.Start(res, req)
				return res
			}

			first := post()
			if first.Code != tt.firstHTTPStatus {
				t.Fatalf("first Start = %d, want %d", first.Code, tt.firstHTTPStatus)
			}
			remote.mu.Lock()
			firstCreates, oldReads := len(remote.createKeys), remote.oldReads
			remote.mu.Unlock()
			if firstCreates != 0 || oldReads != 1 {
				t.Fatalf("complete admission called create/read %d/%d, want 0/1", firstCreates, oldReads)
			}

			var oldStatus string
			var oldIntent int64
			if queryErr := pool.QueryRow(ctx, `
				SELECT status, intended_amount_cents FROM payments WHERE provider_ref = $1`,
				oldSession).Scan(&oldStatus, &oldIntent); queryErr != nil {
				t.Fatalf("read durable complete attempt: %v", queryErr)
			}
			if oldStatus != "requires_reconciliation" || oldIntent != originalAmount {
				t.Fatalf("durable complete attempt = %q/%d, want requires_reconciliation/%d",
					oldStatus, oldIntent, originalAmount)
			}

			backOffice := health.NewStore(pool)
			page, err := backOffice.WorkerHealth(
				ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
			)
			if err != nil {
				t.Fatalf("read health: %v", err)
			}
			visible := false
			for _, issue := range page.UnreconciledCompletePayments {
				if issue.ProviderRef == oldSession && issue.OrderNumber == number {
					visible = true
				}
			}
			if !visible {
				t.Fatalf("health did not expose complete attempt %s for %s", oldSession, number)
			}

			var actorID uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO users (email, role)
				VALUES ('complete-release-' || gen_random_uuid() || '@goen.invalid', 'admin')
				RETURNING id`).Scan(&actorID); err != nil {
				t.Fatalf("create operator: %v", err)
			}
			adminCtx := user.NewContext(ctx, user.User{ID: actorID.String(), Role: user.RoleAdmin})
			if err := backOffice.ReconcileCompletePayment(
				adminCtx, oldSession, health.CompletePaymentUnpaidOrRefunded,
			); err != nil {
				t.Fatalf("release complete attempt: %v", err)
			}

			second := post()
			wantLocation := "https://checkout.stripe.com/c/pay/" + replacement
			if second.Code != http.StatusSeeOther || second.Header().Get("Location") != wantLocation {
				t.Fatalf("Start after safe resolution = %d %q, want 303 %q",
					second.Code, second.Header().Get("Location"), wantLocation)
			}
			remote.mu.Lock()
			keys := append([]string(nil), remote.createKeys...)
			newReads := remote.newReads
			remote.mu.Unlock()
			if diff := cmp.Diff(
				[]string{payment.SessionKey(orderID, currentAmount, 1)}, keys,
			); diff != "" {
				t.Errorf("fresh generation key (-want +got):\n%s", diff)
			}
			if newReads != 1 {
				t.Errorf("replacement Session read %d times, want 1", newReads)
			}
		})
	}
}

// TestPaymentReconciliationPinsExpiredStock serializes the stock consequence
// with the money outcome. An expired hold cannot return to the shelf while a
// complete Session may be paid; paid attribution commits it with stock still
// held, while the explicit unpaid/refunded outcome makes release legal again.
func TestPaymentReconciliationPinsExpiredStock(t *testing.T) {
	assertNamedConstraint := func(t *testing.T, want string, err error) {
		t.Helper()
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.ConstraintName != want {
			t.Fatalf("error = %v, want constraint %s", err, want)
		}
	}
	for _, tt := range []struct {
		name             string
		resolution       health.CompletePaymentResolution
		wantPayment      string
		wantReservation  string
		secondConstraint string
	}{
		{
			name: "paid attribution keeps stock for fulfilment", resolution: health.CompletePaymentPaid,
			wantPayment: "succeeded", wantReservation: "held",
			secondConstraint: "inventory_reservation_committed_no_release",
		},
		{
			name: "unpaid or refunded releases stock", resolution: health.CompletePaymentUnpaidOrRefunded,
			wantPayment: "reconciled", wantReservation: "released",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			const amount = int64(110000)
			number, orderID := holdableOrder(t, amount)
			hold(t, orderID, 0, 60*time.Minute, "reconcile-stock:"+number)
			var reservationID uuid.UUID
			if err := pool.QueryRow(ctx, `
				UPDATE inventory_reservations
				SET created_at = now() - interval '2 hours',
				    expires_at = now() - interval '1 hour'
				WHERE order_id = $1 AND state = 'held'
				RETURNING id`, orderID).Scan(&reservationID); err != nil {
				t.Fatalf("expire reservation: %v", err)
			}
			providerRef := "cs_reconcile_stock_" + uuid.NewString()[:12]
			if _, err := pool.Exec(ctx,
				`SELECT record_complete_payment($1, $2, $3)`, orderID, providerRef, amount); err != nil {
				t.Fatalf("record complete payment: %v", err)
			}

			_, releaseErr := pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
			assertNamedConstraint(t, "inventory_reservation_payment_reconciliation_no_release", releaseErr)
			if _, _, err := lapse.NewSweeper(pool).Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
				t.Fatalf("sweep during reconciliation: %v", err)
			}
			var state string
			if err := pool.QueryRow(ctx,
				`SELECT state FROM inventory_reservations WHERE id = $1`, reservationID).Scan(&state); err != nil {
				t.Fatalf("read pinned reservation: %v", err)
			}
			if state != "held" {
				t.Fatalf("reconciliation sweep changed reservation to %q, want held", state)
			}

			var actorID uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO users (email, role)
				VALUES ('stock-resolution-' || gen_random_uuid() || '@goen.invalid', 'admin')
				RETURNING id`).Scan(&actorID); err != nil {
				t.Fatalf("create operator: %v", err)
			}
			adminCtx := user.NewContext(ctx, user.User{ID: actorID.String(), Role: user.RoleAdmin})
			if err := health.NewStore(pool).
				ReconcileCompletePayment(adminCtx, providerRef, tt.resolution); err != nil {
				t.Fatalf("resolve complete payment: %v", err)
			}

			var paymentStatus string
			if err := pool.QueryRow(ctx,
				`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&paymentStatus); err != nil {
				t.Fatalf("read resolved payment: %v", err)
			}
			if paymentStatus != tt.wantPayment {
				t.Errorf("resolved payment = %q, want %q", paymentStatus, tt.wantPayment)
			}

			_, releaseErr = pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
			if tt.secondConstraint != "" {
				assertNamedConstraint(t, tt.secondConstraint, releaseErr)
			} else if releaseErr != nil {
				t.Fatalf("release after safe resolution: %v", releaseErr)
			}
			if err := pool.QueryRow(ctx,
				`SELECT state FROM inventory_reservations WHERE id = $1`, reservationID).Scan(&state); err != nil {
				t.Fatalf("read resolved reservation: %v", err)
			}
			if state != tt.wantReservation {
				t.Errorf("resolved reservation = %q, want %q", state, tt.wantReservation)
			}
		})
	}
}

// TestReleasedStockMakesLateMoneyARefundCase fixes the other order-lock
// outcome from TestPaymentReconciliationPinsExpiredStock. A Session can become
// complete at Stripe before its deadline while the webhook/recovery write is
// delayed until after the sweeper. If release wins the order lock, neither the
// late webhook nor manual paid attribution may manufacture a paid order whose
// goods are already back on sale.
func TestReleasedStockMakesLateMoneyARefundCase(t *testing.T) {
	expireAndSweep := func(t *testing.T, orderID uuid.UUID) {
		t.Helper()
		ctx := t.Context()
		if _, err := pool.Exec(ctx, `
			UPDATE inventory_reservations
			SET created_at = now() - interval '2 hours',
			    expires_at = now() - interval '1 hour'
			WHERE order_id = $1 AND state = 'held'`, orderID); err != nil {
			t.Fatalf("expire reservation: %v", err)
		}
		if released, _, err := lapse.NewSweeper(pool).Sweep(
			ctx, slog.New(slog.DiscardHandler),
		); err != nil || released < 1 {
			t.Fatalf("sweep before payment outcome = released %d, error %v; want at least 1, nil",
				released, err)
		}
		var state string
		if err := pool.QueryRow(ctx, `
			SELECT state FROM inventory_reservations WHERE order_id = $1
			ORDER BY created_at DESC LIMIT 1`, orderID).Scan(&state); err != nil {
			t.Fatalf("read swept reservation: %v", err)
		}
		if state != "released" {
			t.Fatalf("swept reservation = %q, want released", state)
		}
	}

	t.Run("late signed capture is durable and cannot reopen payment", func(t *testing.T) {
		ctx := t.Context()
		const amount = int64(117000)
		number, orderID := holdableOrder(t, amount)
		hold(t, orderID, 0, 60*time.Minute, "late-stock-webhook:"+number)
		providerRef := "cs_late_stock_webhook_" + uuid.NewString()[:12]
		s := payment.NewStore(pool)
		if err := s.OpenPayment(ctx, number, providerRef, amount); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		expireAndSweep(t, orderID)

		eventID := "evt_late_stock_" + uuid.NewString()[:12]
		body, header := signed(t, typed(
			sessionEvent(eventID, providerRef, "paid", amount),
			"checkout.session.completed",
		))
		h := payment.NewHandler(s, enabledGateway(t), access.NewStore(pool, false),
			slog.New(slog.DiscardHandler))
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/webhooks/stripe", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", header)
		res := httptest.NewRecorder()
		h.Webhook(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("late Webhook status = %d, want 200 with a durable refund alarm", res.Code)
		}

		var status string
		var reason string
		var committed bool
		if err := pool.QueryRow(ctx, `
			SELECT p.status, e.unreconciled, order_is_committed(p.order_id)
			FROM payments p
			JOIN payment_webhook_events e
			  ON e.provider = p.provider AND e.object_ref = p.provider_ref
			WHERE p.provider_ref = $1 AND e.event_id = $2`, providerRef, eventID).
			Scan(&status, &reason, &committed); err != nil {
			t.Fatalf("read late capture outcome: %v", err)
		}
		if status != "requires_payment" || committed ||
			!strings.Contains(reason, "payments_capture_refuses_released_stock") {
			t.Fatalf("late capture = status %q, committed %v, reason %q; "+
				"want requires_payment/false/released-stock refusal", status, committed, reason)
		}

		gateway, calls := gatewayRecordingCalls(t, "cs_must_not_replace_"+uuid.NewString()[:12])
		start := payment.NewHandler(s, gateway, access.NewStore(pool, false),
			slog.New(slog.DiscardHandler))
		startReq := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/orders/"+number+"/pay", http.NoBody)
		startReq.SetPathValue("number", number)
		placedBy(t, startReq, number)
		startRes := httptest.NewRecorder()
		start.Start(startRes, startReq)
		if startRes.Code != http.StatusConflict || *calls != 0 {
			t.Fatalf("Start after late released-stock capture = %d, Stripe creates %d; want 409/0",
				startRes.Code, *calls)
		}
	})

	t.Run("manual paid attribution is refused until staff refund and release", func(t *testing.T) {
		ctx := t.Context()
		const amount = int64(118000)
		number, orderID := holdableOrder(t, amount)
		hold(t, orderID, 0, 60*time.Minute, "late-stock-admin:"+number)
		providerRef := "cs_late_stock_admin_" + uuid.NewString()[:12]
		s := payment.NewStore(pool)
		if err := s.OpenPayment(ctx, number, providerRef, amount); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		expireAndSweep(t, orderID)
		if _, err := pool.Exec(ctx,
			`SELECT record_complete_payment($1, $2, $3)`, orderID, providerRef, amount); err != nil {
			t.Fatalf("record delayed complete state: %v", err)
		}

		var actorID uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, role)
			VALUES ('late-stock-operator-' || gen_random_uuid() || '@goen.invalid', 'admin')
			RETURNING id`).Scan(&actorID); err != nil {
			t.Fatalf("create operator: %v", err)
		}
		adminCtx := user.NewContext(ctx, user.User{ID: actorID.String(), Role: user.RoleAdmin})
		backOffice := health.NewStore(pool)
		paidErr := backOffice.ReconcileCompletePayment(
			adminCtx, providerRef, health.CompletePaymentPaid,
		)
		pgErr, ok := errors.AsType[*pgconn.PgError](paidErr)
		if !errors.Is(paidErr, health.ErrPaymentRequiresRefund) || !ok ||
			pgErr.ConstraintName != "payments_capture_refuses_released_stock" {
			t.Fatalf("paid attribution after stock release = %v, want released-stock refusal", paidErr)
		}

		var status string
		var committed bool
		var effects int64
		if err := pool.QueryRow(ctx, `
			SELECT p.status, order_is_committed(p.order_id),
			       (SELECT count(*) FROM order_events e
			        WHERE e.order_id = p.order_id AND e.kind = 'paid')
			FROM payments p WHERE p.provider_ref = $1`, providerRef).
			Scan(&status, &committed, &effects); err != nil {
			t.Fatalf("read refused admin attribution: %v", err)
		}
		if status != "requires_reconciliation" || committed || effects != 0 {
			t.Fatalf("refused admin attribution = %q/committed %v/effects %d, "+
				"want requires_reconciliation/false/0", status, committed, effects)
		}
		page, err := backOffice.WorkerHealth(
			adminCtx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)),
		)
		if err != nil {
			t.Fatalf("read health after refused attribution: %v", err)
		}
		visibleAsRefundOnly := false
		for _, issue := range page.UnreconciledCompletePayments {
			if issue.ProviderRef == providerRef && !issue.PaidAttributionAllowed {
				visibleAsRefundOnly = true
			}
		}
		if !visibleAsRefundOnly {
			t.Fatal("released-stock payment was not exposed as refund-only in admin health")
		}

		// After an actual provider refund, this is the only permitted conclusion.
		if err := backOffice.ReconcileCompletePayment(
			adminCtx, providerRef, health.CompletePaymentUnpaidOrRefunded,
		); err != nil {
			t.Fatalf("release after confirmed refund: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT status FROM payments WHERE provider_ref = $1`, providerRef).Scan(&status); err != nil {
			t.Fatalf("read refunded resolution: %v", err)
		}
		if status != "reconciled" {
			t.Fatalf("refunded resolution = %q, want reconciled", status)
		}
	})
}

// TestACompleteSessionRejectedAfterItsWebhookAdvancesGeneration exercises the
// full distributed race: Stripe sends paid before Start can persist the Session,
// then returns that now-complete Session to Create. The seen-ref fence must not
// late-link it as payable, and complete must not be mislabeled expired. Its
// reconciliation row consumes attempt zero so the first post after operator
// resolution creates attempt one rather than replaying the same paid Session.
func TestACompleteSessionRejectedAfterItsWebhookAdvancesGeneration(t *testing.T) {
	ctx := t.Context()
	const amount = int64(125000)
	number, orderID := holdableOrder(t, amount)
	hold(t, orderID, 0, 60*time.Minute, "complete-race:"+number)

	completeSession := "cs_complete_race_" + uuid.NewString()[:12]
	replacementSession := "cs_complete_replacement_" + uuid.NewString()[:12]
	eventID := "evt_complete_race_" + uuid.NewString()[:12]
	webhookBody, webhookHeader := signed(t, typed(
		sessionEvent(eventID, completeSession, "paid", amount),
		"checkout.session.completed",
	))

	var state struct {
		mu           sync.Mutex
		keys         []string
		delivered    bool
		expireCalls  int
		retrieveOld  int
		retrieveNext int
	}
	var h *payment.Handler
	handlerReady := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			state.mu.Lock()
			state.keys = append(state.keys, r.Header.Get("Idempotency-Key"))
			first := len(state.keys) == 1
			deliver := first && !state.delivered
			state.delivered = state.delivered || deliver
			state.mu.Unlock()

			if deliver {
				<-handlerReady
				req := httptest.NewRequestWithContext(r.Context(), http.MethodPost,
					"/webhooks/stripe", bytes.NewReader(webhookBody))
				req.Header.Set("Stripe-Signature", webhookHeader)
				res := httptest.NewRecorder()
				h.Webhook(res, req)
				if res.Code != http.StatusOK {
					http.Error(w, "concurrent webhook failed", http.StatusInternalServerError)
					return
				}
			}

			id := replacementSession
			status := "open"
			if first {
				id = completeSession
				status = "complete"
			}
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":%q,`+
					`"payment_status":"paid","url":"https://checkout.stripe.com/c/pay/%s"}`,
				id, status, id)

		case r.Method == http.MethodPost &&
			strings.HasSuffix(r.URL.Path, "/"+completeSession+"/expire"):
			state.mu.Lock()
			state.expireCalls++
			state.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error",` +
				`"message":"only an open Checkout Session can be expired"}}`))

		case r.Method == http.MethodGet &&
			strings.HasSuffix(r.URL.Path, "/"+completeSession):
			state.mu.Lock()
			state.retrieveOld++
			state.mu.Unlock()
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"complete",`+
					`"payment_status":"paid"}`, completeSession)

		case r.Method == http.MethodGet &&
			strings.HasSuffix(r.URL.Path, "/"+replacementSession):
			state.mu.Lock()
			state.retrieveNext++
			state.mu.Unlock()
			_, _ = fmt.Fprintf(w,
				`{"id":%q,"object":"checkout.session","status":"open",`+
					`"url":"https://checkout.stripe.com/c/pay/%s"}`,
				replacementSession, replacementSession)

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	gateway := gatewayAt(t, srv.URL)
	h = payment.NewHandler(payment.NewStore(pool), gateway, access.NewStore(pool, false),
		slog.New(slog.DiscardHandler))
	close(handlerReady)

	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/orders/"+number+"/pay", http.NoBody)
		req.SetPathValue("number", number)
		placedBy(t, req, number)
		res := httptest.NewRecorder()
		h.Start(res, req)
		return res
	}

	if res := post(); res.Code != http.StatusConflict {
		t.Fatalf("Start after webhook won race = %d, want 409", res.Code)
	}

	var completeStatus string
	var unresolved bool
	if err := pool.QueryRow(ctx, `
		SELECT p.status,
		       e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
		FROM payments p
		JOIN payment_webhook_events e
		  ON e.provider = p.provider AND e.object_ref = p.provider_ref
		WHERE p.provider_ref = $1 AND e.event_id = $2`, completeSession, eventID).
		Scan(&completeStatus, &unresolved); err != nil {
		t.Fatalf("read complete-session recovery: %v", err)
	}
	if completeStatus != "requires_reconciliation" || !unresolved {
		t.Fatalf("complete-session recovery = %q/unresolved %v, want requires_reconciliation/true",
			completeStatus, unresolved)
	}

	state.mu.Lock()
	firstKeys := append([]string(nil), state.keys...)
	expireCalls, retrieveOld := state.expireCalls, state.retrieveOld
	state.mu.Unlock()
	if diff := cmp.Diff([]string{payment.SessionKey(orderID, amount, 0)}, firstKeys); diff != "" {
		t.Errorf("first create keys (-want +got):\n%s", diff)
	}
	if expireCalls != 1 || retrieveOld != 1 {
		t.Errorf("complete recovery called expire/retrieve %d/%d times, want 1/1",
			expireCalls, retrieveOld)
	}

	var reconciled bool
	if err := pool.QueryRow(ctx, `SELECT release_payment_event($1)`, eventID).
		Scan(&reconciled); err != nil || !reconciled {
		t.Fatalf("reconcile complete Session = %v, %v; want true", reconciled, err)
	}

	res := post()
	wantLocation := "https://checkout.stripe.com/c/pay/" + replacementSession
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != wantLocation {
		t.Fatalf("Start after reconciliation = %d %q, want 303 %q",
			res.Code, res.Header().Get("Location"), wantLocation)
	}

	state.mu.Lock()
	keys := append([]string(nil), state.keys...)
	retrieveNext := state.retrieveNext
	state.mu.Unlock()
	wantKeys := []string{
		payment.SessionKey(orderID, amount, 0),
		payment.SessionKey(orderID, amount, 1),
	}
	if diff := cmp.Diff(wantKeys, keys); diff != "" {
		t.Errorf("create generations (-want +got):\n%s", diff)
	}
	if retrieveNext != 1 {
		t.Errorf("replacement Session was retrieved %d times, want 1", retrieveNext)
	}

	var oldStatus, newStatus string
	if err := pool.QueryRow(ctx, `
		SELECT old.status, fresh.status
		FROM payments old, payments fresh
		WHERE old.provider_ref = $1 AND fresh.provider_ref = $2`,
		completeSession, replacementSession).Scan(&oldStatus, &newStatus); err != nil {
		t.Fatalf("read payment generations: %v", err)
	}
	if oldStatus != "reconciled" || newStatus != "requires_payment" {
		t.Errorf("payment generations = %q/%q, want reconciled/requires_payment",
			oldStatus, newStatus)
	}
}

// TestCompleteSessionShowsProcessingNotPayAgain locks #136: when Stripe reports
// a Checkout Session complete but the signed webhook has not posted capture,
// the customer sees a processing state rather than the ordinary unpaid order
// with a pay-again CTA. It covers eventual webhook success and the
// health/reconciliation failure path without implying success before signed capture.
func TestCompleteSessionShowsProcessingNotPayAgain(t *testing.T) {
	t.Run("complete session redirects to processing page and webhook resolves it", func(t *testing.T) {
		ctx := t.Context()
		const amount = int64(100000)
		number, orderID := holdableOrder(t, amount)
		hold(t, orderID, 0, 60*time.Minute, "complete-proc:"+number)
		sessionID := "cs_complete_proc_" + uuid.NewString()[:12]
		s := payment.NewStore(pool)
		if err := s.OpenPayment(ctx, number, sessionID, amount); err != nil {
			t.Fatalf("open payment: %v", err)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+sessionID {
				_, _ = fmt.Fprintf(w,
					`{"id":%q,"object":"checkout.session","status":"complete",`+
						`"payment_status":"unpaid"}`, sessionID)
				return
			}
			http.NotFound(w, r)
		}))
		t.Cleanup(srv.Close)

		gateway := gatewayAt(t, srv.URL)

		h := payment.NewHandler(s, gateway, access.NewStore(pool, false), slog.New(slog.DiscardHandler))

		// 1. Complete session without webhook: Start redirects to /orders/{number}/pay
		postReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/pay", http.NoBody)
		postReq.SetPathValue("number", number)
		placedBy(t, postReq, number)
		postRes := httptest.NewRecorder()
		h.Start(postRes, postReq)

		wantPayLocation := "/orders/" + number + "/pay"
		if postRes.Code != http.StatusSeeOther || postRes.Header().Get("Location") != wantPayLocation {
			t.Fatalf("Start with complete session = %d %q, want 303 %q",
				postRes.Code, postRes.Header().Get("Location"), wantPayLocation)
		}

		// 2. Following the redirect to Page (GET /orders/{number}/pay) shows the processing notice
		getReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/pay", http.NoBody)
		getReq.SetPathValue("number", number)
		placedBy(t, getReq, number)
		getRes := httptest.NewRecorder()
		h.Page(getRes, getReq)

		if getRes.Code != http.StatusOK {
			t.Fatalf("Page while processing = %d, want 200", getRes.Code)
		}
		body := getRes.Body.String()
		wantTitle := i18n.T(ctx, i18n.KeyPayProcessingTitle)
		wantBody := i18n.T(ctx, i18n.KeyPayProcessingBody)
		if !strings.Contains(body, wantTitle) {
			t.Errorf("processing page missing title %q", wantTitle)
		}
		if !strings.Contains(body, wantBody) {
			t.Errorf("processing page missing body %q", wantBody)
		}
		// Customer must not see a pay-again CTA while unreconciled payment is processing
		payCTA := `action="/orders/` + number + `/pay"`
		if strings.Contains(body, payCTA) || strings.Contains(body, i18n.T(ctx, i18n.KeyPay)) {
			t.Errorf("processing page must not offer pay CTA: %s", body)
		}
		// Page must not imply success before signed capture
		if strings.Contains(body, i18n.T(ctx, i18n.KeyStatusPaid)) {
			t.Errorf("processing page must not claim order is paid before capture")
		}

		// 3. Eventual webhook success: signed webhook captures the payment
		if _, err := captureThroughWebhook(t, s, payment.Capture{
			SessionID: sessionID, AmountRecv: amount,
		}); err != nil {
			t.Fatalf("late paid webhook: %v", err)
		}

		// Now /orders/{number}/pay sees the order is paid and redirects to /orders/{number}
		afterWebhookReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/pay", http.NoBody)
		afterWebhookReq.SetPathValue("number", number)
		placedBy(t, afterWebhookReq, number)
		afterWebhookRes := httptest.NewRecorder()
		h.Page(afterWebhookRes, afterWebhookReq)

		wantOrderLocation := "/orders/" + number
		if afterWebhookRes.Code != http.StatusSeeOther || afterWebhookRes.Header().Get("Location") != wantOrderLocation {
			t.Fatalf("Page after capture = %d %q, want 303 to %q",
				afterWebhookRes.Code, afterWebhookRes.Header().Get("Location"), wantOrderLocation)
		}
	})

	t.Run("health reconciliation failure path allows customer to pay again", func(t *testing.T) {
		ctx := t.Context()
		const amount = int64(100000)
		number, orderID := holdableOrder(t, amount)
		hold(t, orderID, 0, 60*time.Minute, "complete-proc-fail:"+number)
		sessionID := "cs_complete_proc_fail_" + uuid.NewString()[:12]
		s := payment.NewStore(pool)
		if err := s.OpenPayment(ctx, number, sessionID, amount); err != nil {
			t.Fatalf("open payment: %v", err)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/"+sessionID {
				_, _ = fmt.Fprintf(w,
					`{"id":%q,"object":"checkout.session","status":"complete",`+
						`"payment_status":"unpaid"}`, sessionID)
				return
			}
			http.NotFound(w, r)
		}))
		t.Cleanup(srv.Close)

		gateway := gatewayAt(t, srv.URL)

		h := payment.NewHandler(s, gateway, access.NewStore(pool, false), slog.New(slog.DiscardHandler))

		// Complete session without webhook records complete and redirects to pay page
		postReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/pay", http.NoBody)
		postReq.SetPathValue("number", number)
		placedBy(t, postReq, number)
		postRes := httptest.NewRecorder()
		h.Start(postRes, postReq)
		if postRes.Code != http.StatusSeeOther {
			t.Fatalf("Start = %d, want 303", postRes.Code)
		}

		// Shows processing state
		getReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/pay", http.NoBody)
		getReq.SetPathValue("number", number)
		placedBy(t, getReq, number)
		getRes := httptest.NewRecorder()
		h.Page(getRes, getReq)
		if !strings.Contains(getRes.Body.String(), i18n.T(ctx, i18n.KeyPayProcessingTitle)) {
			t.Fatalf("expected processing notice on page")
		}

		// Operator resolves complete payment as unpaid or refunded
		var actorID uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, role)
			VALUES ('operator-' || gen_random_uuid() || '@goen.invalid', 'admin')
			RETURNING id`).Scan(&actorID); err != nil {
			t.Fatalf("create operator: %v", err)
		}
		adminCtx := user.NewContext(ctx, user.User{ID: actorID.String(), Role: user.RoleAdmin})
		backOffice := health.NewStore(pool)
		if err := backOffice.ReconcileCompletePayment(
			adminCtx, sessionID, health.CompletePaymentUnpaidOrRefunded,
		); err != nil {
			t.Fatalf("reconcile complete payment: %v", err)
		}

		// Now page renders the pay form with pay CTA restored
		getReq2 := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/pay", http.NoBody)
		getReq2.SetPathValue("number", number)
		placedBy(t, getReq2, number)
		getRes2 := httptest.NewRecorder()
		h.Page(getRes2, getReq2)

		if getRes2.Code != http.StatusOK {
			t.Fatalf("Page after reconciliation = %d, want 200", getRes2.Code)
		}
		body2 := getRes2.Body.String()
		if strings.Contains(body2, i18n.T(ctx, i18n.KeyPayProcessingTitle)) {
			t.Errorf("processing notice should be gone after reconciliation")
		}
		if !strings.Contains(body2, i18n.T(ctx, i18n.KeyPay)) {
			t.Errorf("pay form CTA should be restored after reconciliation")
		}
	})
}
