//go:build integration

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/restore"
)

type restoreStripeRefund struct {
	mu             sync.Mutex
	calls, creates int
	key            string
	amount         int64
}

func restoreStripeBackend(t *testing.T) *restoreStripeRefund {
	t.Helper()
	provider := &restoreStripeRefund{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if parseErr := r.ParseForm(); parseErr != nil {
			t.Errorf("parse local provider request: %v", parseErr)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		provider.mu.Lock()
		defer provider.mu.Unlock()
		provider.calls++
		w.Header().Set("Content-Type", "application/json")
		var response any
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/cs_restore_refund":
			response = map[string]any{"id": "cs_restore_refund", "object": "checkout.session", "payment_intent": map[string]any{"id": "pi_restore_refund"}}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/refunds":
			if r.Form.Get("payment_intent") != "pi_restore_refund" {
				t.Errorf("refund lookup intent = %q", r.Form.Get("payment_intent"))
			}
			data := []any{}
			if provider.creates > 0 {
				data = append(data, map[string]any{"id": "re_restore_recovered", "object": "refund", "status": "succeeded", "currency": "twd", "amount": provider.amount,
					"payment_intent": "pi_restore_refund", "metadata": map[string]string{"goen_request_key": provider.key}})
			}
			response = map[string]any{"object": "list", "data": data, "has_more": false, "url": "/v1/refunds"}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/refunds":
			provider.creates++
			provider.key = r.Header.Get("Idempotency-Key")
			amount, amountErr := strconv.ParseInt(r.Form.Get("amount"), 10, 64)
			if amountErr != nil || amount != 100000 || provider.key == "" || r.Form.Get("metadata[goen_request_key]") != provider.key || r.Form.Get("payment_intent") != "pi_restore_refund" {
				t.Errorf("refund creation lost its frozen identity: amount=%d key=%q form=%v", amount, provider.key, r.Form)
			}
			provider.amount = amount
			// The provider committed its operation; only its reply was lost.
			w.WriteHeader(http.StatusInternalServerError)
			response = map[string]any{"error": map[string]string{"type": "api_error", "message": "response unavailable after commit"}}
		default:
			t.Errorf("unexpected local provider request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotImplemented)
			response = map[string]any{"error": map[string]string{"type": "api_error", "message": "unsupported local request"}}
		}
		if encodeErr := json.NewEncoder(w).Encode(response); encodeErr != nil {
			t.Errorf("encode provider response: %v", encodeErr)
		}
	}))
	t.Cleanup(server.Close)
	original := stripe.GetBackend(stripe.APIBackend)
	noRetries := int64(0)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(server.URL), MaxNetworkRetries: &noRetries,
	}))
	t.Cleanup(func() { stripe.SetBackend(stripe.APIBackend, original) })
	return provider
}

func (p *restoreStripeRefund) snapshot() (calls, creates int, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.creates, p.key
}

func restoreFinancialRouter(t *testing.T, databaseURL string) http.Handler {
	t.Helper()
	storePool, err := openPool(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(storePool.Close)
	adminPool, err := openAdminPool(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(adminPool.Close)
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return newRouter(&RouterConfig{Pool: storePool, AdminPool: adminPool, Payments: gateway,
		Refunder: admin.NewRefunder("sk_test_restore_local_only"), BaseURL: "http://127.0.0.1"}, slog.New(slog.DiscardHandler))
}

func restoreOperator(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, string) {
	t.Helper()
	id := uuid.New()
	if _, insertErr := pool.Exec(t.Context(), `INSERT INTO users (id, email, role) VALUES ($1, $2, 'admin')`, id, id.String()+"@restore.invalid"); insertErr != nil {
		t.Fatal(insertErr)
	}
	token, err := account.NewStore(pool).StartSession(t.Context(), id.String(), "restore financial drill", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, updateErr := pool.Exec(t.Context(), `UPDATE sessions SET totp_verified_at = now() WHERE token_hash = $1`, account.HashToken(token)); updateErr != nil {
		t.Fatal(updateErr)
	}
	return id, token
}

func restoreOperatorPost(t *testing.T, handler http.Handler, token, path, requestID string, form url.Values, want string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Request-Id", requestID)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: "goen_session", Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != want {
		t.Fatalf("restored POST %s: status=%d location=%q, want 303 %q", path, res.Code, res.Header().Get("Location"), want)
	}
}

func restoreFinancialOrder(t *testing.T, pool *pgxpool.Pool, number, providerRef string, captured bool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var orderID, lineID uuid.UUID
	if queryErr := tx.QueryRow(ctx, `INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT $1, v.id, sm.code, v.name FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, number).Scan(&orderID); queryErr != nil {
		t.Fatal(queryErr)
	}
	if queryErr := tx.QueryRow(ctx, `INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'RESTORE-REFUND', 'Restore fixture', 100000, 2) RETURNING id`, orderID).Scan(&lineID); queryErr != nil {
		t.Fatal(queryErr)
	}
	if _, execErr := tx.Exec(ctx, `INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
		VALUES ($1, 'restore-financial@example.com', 'Restore', '0912345678', '110', 'Taipei', 'Xinyi', '1 Test Road')`, orderID); execErr != nil {
		t.Fatal(execErr)
	}
	if captured {
		if _, execErr := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`, orderID, providerRef); execErr != nil {
			t.Fatal(execErr)
		}
		if _, execErr := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`, providerRef); execErr != nil {
			t.Fatal(execErr)
		}
	} else if _, execErr := tx.Exec(ctx, `SELECT record_complete_payment($1, $2, 200000)`, orderID, providerRef); execErr != nil {
		t.Fatal(execErr)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}
	return orderID, lineID
}

func restoreRefundFixture(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	orderID, lineID := restoreFinancialOrder(t, pool, "GO-260914-000005", "cs_restore_refund", true)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, status := range []string{"picking", "shipped"} {
		if _, execErr := tx.Exec(ctx, `UPDATE orders SET fulfillment_status = $2 WHERE id = $1`, orderID, status); execErr != nil {
			t.Fatal(execErr)
		}
	}
	var shipment, request uuid.UUID
	if queryErr := tx.QueryRow(ctx, `INSERT INTO order_shipments (order_id, carrier, tracking_number) VALUES ($1, 'fixture', 'restore-refund') RETURNING id`, orderID).Scan(&shipment); queryErr != nil {
		t.Fatal(queryErr)
	}
	if _, execErr := tx.Exec(ctx, `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity) VALUES ($1, $2, $3, 2)`, orderID, shipment, lineID); execErr != nil {
		t.Fatal(execErr)
	}
	if queryErr := tx.QueryRow(ctx, `INSERT INTO return_requests (order_id, reason) VALUES ($1, 'restore recovery') RETURNING id`, orderID).Scan(&request); queryErr != nil {
		t.Fatal(queryErr)
	}
	if _, execErr := tx.Exec(ctx, `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity) VALUES ($1, $2, $3, 1)`, orderID, request, lineID); execErr != nil {
		t.Fatal(execErr)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}
	return request
}

func TestRestoredFinancialHandlersResumeWithoutDuplicateMoney(t *testing.T) {
	provider := restoreStripeBackend(t)
	source := dbtest.Pool(t)
	ctx := t.Context()
	if err := loadRestoreFixtureOn(ctx, source); err != nil {
		t.Fatal(err)
	}
	actor, token := restoreOperator(t, source)
	returnID := restoreRefundFixture(t, source)
	restoreFinancialOrder(t, source, "GO-260914-000006", "cs_restore_paid", false)
	restoreFinancialOrder(t, source, "GO-260914-000007", "cs_restore_unpaid", false)
	form := url.Values{"decision": {"exception"}, "resolution": {"owned restore recovery"}}
	path := "/admin/returns/" + returnID.String() + "/decide"
	restoreOperatorPost(t, restoreFinancialRouter(t, source.Config().ConnString()), token, path, "restore-source-refund", form, "/admin/returns?refundfailed=1")
	_, creates, key := provider.snapshot()
	if creates != 1 {
		t.Fatalf("source provider operations=%d, want one ambiguous operation", creates)
	}
	assertRestoreRefund(t, source, returnID, actor, "pending", key, 0, 1)

	copyName := "restore_money_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	copyURL, artifacts, cleanup, err := restore.DumpCopy(ctx, source.Config().ConnString(), copyName)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	copyPool, err := pgxpool.New(ctx, copyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer copyPool.Close()
	manifest, err := restore.Collect(ctx, copyPool)
	if err != nil || !restore.Equal(artifacts.Manifest, manifest) {
		t.Fatalf("restored financial snapshot differs: %v", err)
	}
	handler := restoreFinancialRouter(t, copyURL)
	for _, path := range []string{"/admin/returns", "/admin/health"} {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
		req.AddCookie(&http.Cookie{Name: "goen_session", Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("restored financial queue %s: status=%d", path, res.Code)
		}
		needles := []string{returnID.String()}
		if path == "/admin/health" {
			needles = []string{"cs_restore_paid", "cs_restore_unpaid"}
		}
		for _, needle := range needles {
			if !strings.Contains(res.Body.String(), needle) {
				t.Fatalf("restored financial queue %s lost pending work %q", path, needle)
			}
		}
	}
	assertRestoreRefund(t, copyPool, returnID, actor, "pending", key, 0, 1)
	restoreOperatorPost(t, handler, token, path, "restore-copy-refund", form, "/admin/returns?ok=1")
	assertRestoreRefund(t, copyPool, returnID, actor, "succeeded", key, 1, 2)
	calls, _, _ := provider.snapshot()
	restoreOperatorPost(t, handler, token, path, "restore-copy-replay", form, "/admin/returns?ok=1")
	assertRestoreRefund(t, copyPool, returnID, actor, "succeeded", key, 1, 2)
	if after, operations, afterKey := provider.snapshot(); after != calls || operations != 1 || afterKey != key {
		t.Fatalf("completed refund repeated provider work: calls=%d/%d creates=%d key=%q/%q", calls, after, operations, key, afterKey)
	}
	for _, resolution := range []string{"paid", "unpaid_or_refunded"} {
		ref := "cs_restore_paid"
		if resolution != "paid" {
			ref = "cs_restore_unpaid"
		}
		assertRestorePayment(t, copyPool, ref, actor, "requires_reconciliation", 0, 0, 0)
		paymentForm := url.Values{"payment": {ref}, "resolution": {resolution}}
		restoreOperatorPost(t, handler, token, "/admin/health/reconcile", "restore-"+strings.ReplaceAll(resolution, "_", "-"), paymentForm, "/admin/health?reconciled=1")
		status, amount, events := "cancelled", int64(0), 0
		if resolution == "paid" {
			status, amount, events = "succeeded", 200000, 1
		}
		assertRestorePayment(t, copyPool, ref, actor, status, amount, events, 1)
		restoreOperatorPost(t, handler, token, "/admin/health/reconcile", "restore-replay-"+strings.ReplaceAll(resolution, "_", "-"), paymentForm, "/admin/health?notflagged=1")
		assertRestorePayment(t, copyPool, ref, actor, status, amount, events, 1)
		assertRestorePayment(t, source, ref, actor, "requires_reconciliation", 0, 0, 0)
	}
	assertRestoreRefund(t, source, returnID, actor, "pending", key, 0, 1)
}

func assertRestoreRefund(t *testing.T, pool *pgxpool.Pool, id, actor uuid.UUID, wantStatus, wantKey string, events, attempts int) {
	t.Helper()
	var status, key string
	var amount int64
	var rows, gotEvents, gotAttempts, succeeded int
	queryErr := pool.QueryRow(t.Context(), `SELECT r.status, r.request_key, r.amount_cents,
		(SELECT count(*) FROM refunds x WHERE x.return_request_id = $1),
		(SELECT count(*) FROM order_events e WHERE e.return_request_id = $1 AND e.kind = 'refunded'),
		(SELECT count(*) FROM audit_events a WHERE a.entity_id = r.id AND a.action = 'refund.provider_attempt' AND a.actor_user_id = $2),
		(SELECT count(*) FROM audit_events a WHERE a.entity_id = r.id AND a.action = 'refund.provider_succeeded' AND a.actor_user_id = $2)
		FROM refunds r WHERE r.return_request_id = $1`, id, actor).Scan(&status, &key, &amount, &rows, &gotEvents, &gotAttempts, &succeeded)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if status != wantStatus || key != wantKey || amount != 100000 || rows != 1 || gotEvents != events || gotAttempts != attempts || succeeded != events {
		t.Fatalf("refund state=%s key=%q amount=%d rows=%d events=%d attempts=%d succeeded=%d; want %s/%q/100000/1/%d/%d/%d", status, key, amount, rows, gotEvents, gotAttempts, succeeded, wantStatus, wantKey, events, attempts, events)
	}
}

func assertRestorePayment(t *testing.T, pool *pgxpool.Pool, ref string, actor uuid.UUID, wantStatus string, amount int64, events, audits int) {
	t.Helper()
	var status string
	var captured int64
	var gotEvents, gotAudits int
	queryErr := pool.QueryRow(t.Context(), `SELECT p.status, p.captured_amount_cents,
		(SELECT count(*) FROM order_events e WHERE e.order_id = p.order_id AND e.kind = 'paid'),
		(SELECT count(*) FROM audit_events a WHERE a.action = 'payment.reconciled' AND a.after->>'provider_ref' = $1 AND a.actor_user_id = $2)
		FROM payments p WHERE p.provider_ref = $1`, ref, actor).Scan(&status, &captured, &gotEvents, &gotAudits)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if status != wantStatus || captured != amount || gotEvents != events || gotAudits != audits {
		t.Fatalf("payment %s state=%s captured=%d events=%d audits=%d; want %s/%d/%d/%d", ref, status, captured, gotEvents, gotAudits, wantStatus, amount, events, audits)
	}
}
