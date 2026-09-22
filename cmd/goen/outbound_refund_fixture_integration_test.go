//go:build integration

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/payment"
)

func outboundRefundFinancialRouter(t *testing.T, databaseURL string) http.Handler {
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
		Refunder: admin.NewRefunder("sk_test_refund_local_only"), BaseURL: "http://127.0.0.1"}, slog.New(slog.DiscardHandler))
}

func outboundRefundOperator(t *testing.T, pool *pgxpool.Pool) (actorID uuid.UUID, sessionToken string) {
	t.Helper()
	id := uuid.New()
	if _, insertErr := pool.Exec(t.Context(), `INSERT INTO users (id, email, role) VALUES ($1, $2, 'admin')`, id, id.String()+"@refund.invalid"); insertErr != nil {
		t.Fatal(insertErr)
	}
	token, err := account.NewStore(pool).StartSession(t.Context(), id.String(), "outbound refund recovery", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, updateErr := pool.Exec(t.Context(), `UPDATE sessions SET totp_verified_at = now() WHERE token_hash = $1`, account.HashToken(token)); updateErr != nil {
		t.Fatal(updateErr)
	}
	return id, token
}

func outboundRefundOperatorPost(t *testing.T, handler http.Handler, token, path, requestID string, form url.Values, want string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Request-Id", requestID)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: "goen_session", Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != want {
		t.Fatalf("pending refund POST %s: status=%d location=%q, want 303 %q", path, res.Code, res.Header().Get("Location"), want)
	}
}

// The recovery action must come from the pending refund operator queue: a first
// exception decision and an approved payout retry are different form verbs.
func outboundRefundRetryForm(t *testing.T, body, action string) url.Values {
	t.Helper()
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{}
	matches := 0
	attribute := func(node *html.Node, key string) string {
		for _, attr := range node.Attr {
			if attr.Key == key {
				return attr.Val
			}
		}
		return ""
	}
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, selected bool) {
		if node.Type == html.ElementNode && node.Data == "form" {
			selected = attribute(node, "action") == action && attribute(node, "method") == "post"
			if selected {
				matches++
			}
		}
		if selected && node.Type == html.ElementNode && node.Data == "input" && attribute(node, "type") == "hidden" {
			values.Add(attribute(node, "name"), attribute(node, "value"))
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, selected)
		}
	}
	walk(document, false)
	if matches != 1 || values.Get("decision") != "approved" {
		t.Fatalf("pending refund payout retry form: matches=%d values=%v, want one approved retry", matches, values)
	}
	return values
}

func outboundRefundFinancialOrder(t *testing.T, pool *pgxpool.Pool, number, providerRef string) (orderID, lineID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if queryErr := tx.QueryRow(ctx, `INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT $1, v.id, sm.code, v.name FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, number).Scan(&orderID); queryErr != nil {
		t.Fatal(queryErr)
	}
	if queryErr := tx.QueryRow(ctx, `INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'OUTBOUND-REFUND', 'Refund fixture', 100000, 2) RETURNING id`, orderID).Scan(&lineID); queryErr != nil {
		t.Fatal(queryErr)
	}
	if _, execErr := tx.Exec(ctx, `INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
		VALUES ($1, 'refund@example.com', 'Refund', '0912345678', '110', 'Taipei', 'Xinyi', '1 Test Road')`, orderID); execErr != nil {
		t.Fatal(execErr)
	}
	if _, execErr := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`, orderID, providerRef); execErr != nil {
		t.Fatal(execErr)
	}
	if _, execErr := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`, providerRef); execErr != nil {
		t.Fatal(execErr)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}
	return orderID, lineID
}

func outboundRefundFixture(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	orderID, lineID := outboundRefundFinancialOrder(t, pool, "GO-260914-000005", "cs_outbound_refund")
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
	if queryErr := tx.QueryRow(ctx, `INSERT INTO order_shipments (order_id, carrier, tracking_number) VALUES ($1, 'fixture', 'outbound-refund') RETURNING id`, orderID).Scan(&shipment); queryErr != nil {
		t.Fatal(queryErr)
	}
	if _, execErr := tx.Exec(ctx, `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity) VALUES ($1, $2, $3, 2)`, orderID, shipment, lineID); execErr != nil {
		t.Fatal(execErr)
	}
	if queryErr := tx.QueryRow(ctx, `INSERT INTO return_requests (order_id, reason) VALUES ($1, 'outbound refund recovery') RETURNING id`, orderID).Scan(&request); queryErr != nil {
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

func outboundRefundFinancialHistory(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var history string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
  'audit', (SELECT coalesce(jsonb_agg(a ORDER BY a.id), '[]'::jsonb) FROM audit_events a),
  'credit', (SELECT coalesce(jsonb_agg(c ORDER BY c.id), '[]'::jsonb) FROM store_credit_entries c)
 )::text`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	return history
}

func assertOutboundRefund(t *testing.T, pool *pgxpool.Pool, id, actor uuid.UUID, wantStatus, wantKey string, events, attempts int) {
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
