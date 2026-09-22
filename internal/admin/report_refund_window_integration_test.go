//go:build integration

package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
)

func TestRefundOnlyReportWindow(t *testing.T) {
	p := isolatedAdminSeedPool(t)
	ctx := t.Context()
	var orderID uuid.UUID
	// Historical timestamps distinguish placement, refund request and settlement.
	// Every row passes the production financial and deferred order constraints.
	err := p.QueryRow(ctx, `
		WITH ordered AS (
		    INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                        shipping_method_name, shipping_cents, placed_at)
		    SELECT next_order_number(), v.id, sm.code, v.name, 0, now() - interval '365 days'
		    FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		    WHERE sm.code = 'home_delivery' ORDER BY v.effective_at LIMIT 1 RETURNING id
		), lined AS (
		    INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		    SELECT id, 'REPORT-REFUND-WINDOW', 'Report fixture', 1000000, 1 FROM ordered
		), addressed AS (
		    INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                    postal_code, city, district, street)
		    SELECT id, 'report@example.com', 'Report recipient', '0912345678',
		           '110', 'Taipei', 'Xinyi', '1 Test Road' FROM ordered
		)
		SELECT id FROM ordered`).Scan(&orderID)
	if err != nil {
		t.Fatalf("create historical report order: %v", err)
	}
	var paymentID uuid.UUID
	if err := p.QueryRow(ctx, `
		INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents,
		                      captured_amount_cents, paid_at, created_at)
		VALUES ($1, 'cs_report_refund_window', 'succeeded', 1000000, 1000000,
		        now() - interval '365 days', now() - interval '365 days') RETURNING id`,
		orderID).Scan(&paymentID); err != nil {
		t.Fatalf("record historical capture: %v", err)
	}
	if _, err := p.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, provider_ref, status, amount_cents,
		                     created_at, succeeded_at)
		VALUES ($1, 'report-old-settled', 're_report_old', 'succeeded', 22222,
		        now() - interval '110 days', now() - interval '100 days'),
		       ($1, 'report-still-pending', NULL, 'pending', 33333,
		        now() - interval '1 day', NULL)`, paymentID); err != nil {
		t.Fatalf("record excluded refunds: %v", err)
	}
	var refundID uuid.UUID
	if err := p.QueryRow(ctx, `
		INSERT INTO refunds (payment_id, request_key, status, amount_cents, created_at)
		VALUES ($1, 'report-awaiting-settlement', 'pending', 12500,
		        now() - interval '100 days') RETURNING id`, paymentID).Scan(&refundID); err != nil {
		t.Fatalf("record older pending refund: %v", err)
	}

	s := admin.NewStore(p, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(p, s)
	for _, days := range []int32{7, 30, 90} {
		view, err := s.Report(ctx, days)
		if err != nil {
			t.Fatalf("read empty report: %v", err)
		}
		if view.Placed != 0 || view.RefundedCents != 0 || !view.Empty() {
			t.Fatalf("pending or out-of-window refunds contaminated empty report: %+v", view)
		}
	}
	if _, err := p.Exec(ctx, `
		UPDATE refunds SET status = 'succeeded', provider_ref = 're_report_new', succeeded_at = now()
		WHERE id = $1`, refundID); err != nil {
		t.Fatalf("settle the older refund request: %v", err)
	}
	for _, days := range []int32{7, 30, 90} {
		view, err := s.Report(ctx, days)
		if err != nil {
			t.Fatalf("read refund-only report: %v", err)
		}
		if view.Placed != 0 || view.Orders != 0 || view.RevenueCents != 0 || view.RefundedCents != 12500 {
			t.Fatalf("refund-only window used the wrong transaction clock or total: %+v", view)
		}
		if view.Completion() != "—" {
			t.Fatalf("zero orders invented a completion rate: %q", view.Completion())
		}
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			localized := i18n.WithLocale(ctx, locale)
			r := httptest.NewRequestWithContext(localized, http.MethodGet,
				"/admin/reports?days="+strconv.FormatInt(int64(days), 10), nil)
			w := httptest.NewRecorder()
			h.Reports(w, r)
			body := w.Body.String()
			if w.Code != http.StatusOK {
				t.Fatalf("refund-only report HTTP status = %d", w.Code)
			}
			if !strings.Contains(body, `class="goen-report__figures"`) ||
				!strings.Contains(body, i18n.T(localized, i18n.KeyAdminRepRefunded)) ||
				!strings.Contains(body, view.Refunded()) ||
				strings.Contains(body, i18n.T(localized, i18n.KeyAdminRepEmpty)) {
				t.Fatalf("refund-only window hid settled money: days=%d locale=%s", days, locale)
			}
		}
	}
}
