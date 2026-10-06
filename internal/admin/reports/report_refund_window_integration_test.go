//go:build integration

package reports_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/i18n"
)

func TestRefundOnlyReportWindow(t *testing.T) {
	p := admintest.Pool(t)
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

	s := reports.NewStore(p)
	h := reports.NewHandler(s, slog.New(slog.DiscardHandler))
	for _, days := range []int32{7, 30, 90} {
		view, err := s.ReportAt(ctx, days, time.Now())
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
		view, err := s.ReportAt(ctx, days, time.Now())
		if err != nil {
			t.Fatalf("read refund-only report: %v", err)
		}
		if view.Placed != 0 || view.Orders != 0 || view.RevenueCents != 0 || view.RefundedCents != 12500 {
			t.Fatalf("refund-only window used the wrong transaction clock or total: %+v", view)
		}
		if view.Completion(ctx) != "—" {
			t.Fatalf("zero orders invented a completion rate: %q", view.Completion(ctx))
		}
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			localized := i18n.WithLocale(ctx, locale)
			r := httptest.NewRequestWithContext(localized, http.MethodGet,
				"/admin/reports?days="+strconv.FormatInt(int64(days), 10), nil)
			w := httptest.NewRecorder()
			h.Page(w, r)
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

// TestRefundBeforeShipmentLeavesBothReportFigures: an order refunded before
// shipment leaves Revenue, so its refund is not money gone back from that
// revenue either. Counted as refunded, the net would take it off twice.
func TestRefundBeforeShipmentLeavesBothReportFigures(t *testing.T) {
	p := admintest.Pool(t)
	ctx := t.Context()
	var staff, buyer uuid.UUID
	if err := p.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('report-staff@goen.invalid', 'staff')
		RETURNING id`).Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	if err := p.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name) VALUES ('report-buyer@goen.invalid', 'customer', '報表')
		RETURNING id`).Scan(&buyer); err != nil {
		t.Fatalf("create buyer: %v", err)
	}
	if _, err := p.Exec(ctx,
		`SELECT post_store_credit($1, $2, '測試發放', NULL, 'report-grant', NULL)`,
		buyer, int64(300000)); err != nil {
		t.Fatalf("grant credit: %v", err)
	}
	// paidOrder is placed now and paid by card and, when credit > 0, by the
	// buyer's store credit.
	paidOrder := func(user uuid.NullUUID, card, credit int64) (number string, orderID uuid.UUID) {
		t.Helper()
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
			                    shipping_method_name, shipping_cents)
			SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
			FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
			WHERE sm.code = 'home_delivery' ORDER BY v.effective_at LIMIT 1
			RETURNING order_number, id`, user).Scan(&number, &orderID); err != nil {
			t.Fatalf("create order: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
			VALUES ($1, 'REPORT-BEFORE-SHIPMENT', 'Report fixture', $2, 1)`, orderID, card+credit); err != nil {
			t.Fatalf("create line: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_private_data (order_id, email, recipient_name, phone,
			                                postal_code, city, district, street)
			VALUES ($1, 'report@example.com', 'Report recipient', '0912345678',
			        '110', 'Taipei', 'Xinyi', '1 Test Road')`, orderID); err != nil {
			t.Fatalf("create private data: %v", err)
		}
		if credit > 0 {
			if _, err := tx.Exec(ctx, `SELECT spend_store_credit($1, $2)`, orderID, -credit); err != nil {
				t.Fatalf("spend credit: %v", err)
			}
		}
		session := "cs_report_before_shipment_" + number
		if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`, orderID, session, card); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, session, card); err != nil {
			t.Fatalf("capture: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		return number, orderID
	}

	paidOrder(uuid.NullUUID{}, 500000, 0)
	number, orderID := paidOrder(uuid.NullUUID{UUID: buyer, Valid: true}, 700000, 300000)
	var returnID uuid.UUID
	if err := p.QueryRow(ctx, `SELECT open_refund_before_shipment($1, 'report', $2, 'report')`,
		number, staff).Scan(&returnID); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	if _, err := p.Exec(ctx, `
		INSERT INTO refunds (payment_id, return_request_id, request_key, status, amount_cents,
		                     reason, provider_ref, succeeded_at)
		SELECT p.id, r.id, 'return:' || r.id, 'succeeded', r.card_refund_cents,
		       r.resolution, 're_' || r.id, now()
		FROM return_requests r JOIN payments p ON p.order_id = r.order_id AND p.status = 'succeeded'
		WHERE r.id = $1`, returnID); err != nil {
		t.Fatalf("settle the card half: %v", err)
	}
	if _, err := p.Exec(ctx, `SELECT compensate_return_with_credit($1, $2, $3)`,
		returnID, int64(300000), staff); err != nil {
		t.Fatalf("post the credit half: %v", err)
	}

	s := reports.NewStore(p)
	onlyTheOtherOrder := func(state string) {
		t.Helper()
		for _, days := range []int32{7, 30, 90} {
			view, err := s.ReportAt(ctx, days, time.Now())
			if err != nil {
				t.Fatalf("read report: %v", err)
			}
			if view.Orders != 1 || view.RevenueCents != 500000 || view.RefundedCents != 0 {
				t.Fatalf("%s: orders/revenue/refunded = %d/%d/%d, want 1/500000/0",
					state, view.Orders, view.RevenueCents, view.RefundedCents)
			}
		}
	}
	onlyTheOtherOrder("refund settled, order not yet cancelled")

	if _, err := p.Exec(ctx, `
		INSERT INTO order_events (order_id, kind, return_request_id) VALUES ($1, 'refunded', $2)`,
		orderID, returnID); err != nil {
		t.Fatalf("record refunded event: %v", err)
	}
	if _, err := p.Exec(ctx, `
		UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = $1`,
		orderID); err != nil {
		t.Fatalf("cancel the refunded order: %v", err)
	}
	onlyTheOtherOrder("cancelled")

	h := reports.NewHandler(s, slog.New(slog.DiscardHandler))
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		localized := i18n.WithLocale(ctx, locale)
		r := httptest.NewRequestWithContext(localized, http.MethodGet, "/admin/reports?days=30", nil)
		w := httptest.NewRecorder()
		h.Page(w, r)
		if w.Code != http.StatusOK ||
			!strings.Contains(w.Body.String(), i18n.T(localized, i18n.KeyAdminRepBeforeShipment)) {
			t.Fatalf("%s report does not say what it leaves out: %d", locale, w.Code)
		}
	}
}
