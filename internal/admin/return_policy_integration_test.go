//go:build integration

package admin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestReturnDecisionEnforcesAdvertisedPolicy(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	customer := returns.NewStore(pool)

	t.Run("statutory blank reason cannot be the sole rejection", func(t *testing.T) {
		delivered := mustRFC3339(t, "2026-01-01T07:00:00+08:00")
		requested := mustRFC3339(t, "2026-01-06T12:00:00+08:00")
		requestID := returnedOrderAtWithReason(t, delivered, requested, "")

		if window := queueWindow(t, s, requestID); window != "within" {
			t.Fatalf("window = %q, want within — shop_today would have aged this January filing", window)
		}
		if err := s.Decide(ctx, requestID.String(), "rejected", "", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("blank statutory rejection = %v, want ErrRefused", err)
		}
		status, refunds := returnPayout(t, requestID)
		if status != "requested" || refunds != 0 {
			t.Errorf("after a refused rejection the return is %q with %d refunds, want requested/0",
				status, refunds)
		}

		if err := s.Decide(ctx, requestID.String(), "rejected", "parcel never arrived",
			"ineligible", uuid.NullUUID{}); err != nil {
			t.Fatalf("statutory rejection with another ground: %v", err)
		}
		status, refunds = returnPayout(t, requestID)
		if status != "rejected" || refunds != 0 {
			t.Errorf("rejected statutory return is %q with %d refunds, want rejected/0", status, refunds)
		}
	})

	t.Run("statutory blank reason may be approved and paid", func(t *testing.T) {
		number, lineID := deliveredOrderAt(t, shopNoonDaysAgo(t, 3))
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open statutory blank-reason return: %v", err)
		}
		requestID := openReturnID(t, number)
		if window := queueWindow(t, s, requestID); window != "within" {
			t.Fatalf("window = %q, want within", window)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve statutory blank-reason return: %v", err)
		}
		status, refunds := returnPayout(t, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("approved statutory return is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaim(t, requestID); got != "statutory" {
			t.Errorf("entitlement = %q, want statutory", got)
		}
	})

	t.Run("rejection explicitly for missing customer reason is refused", func(t *testing.T) {
		delivered := mustRFC3339(t, "2026-01-01T07:00:00+08:00")
		requested := mustRFC3339(t, "2026-01-06T12:00:00+08:00")
		requestID := returnedOrderAtWithReason(t, delivered, requested, "")

		if err := s.Decide(ctx, requestID.String(), "rejected", "原因未填", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("rejection explicitly for missing customer reason = %v, want ErrRefused", err)
		}
		status, refunds := returnPayout(t, requestID)
		if status != "requested" || refunds != 0 {
			t.Errorf("after refused missing-reason rejection the return is %q with %d refunds, want requested/0",
				status, refunds)
		}
	})

	t.Run("day 10 approval records no entitlement without unused and complete facts", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAt(t, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "box opened", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnID(t, number)
		if window := queueWindow(t, s, requestID); window != "goodwill" {
			t.Fatalf("window = %q, want goodwill", window)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve day-10 return: %v", err)
		}
		status, refunds := returnPayout(t, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("day-10 return is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaim(t, requestID); got != "" {
			t.Errorf("day-10 entitlement = %q, want empty — unused/complete are not decision facts", got)
		}
	})

	t.Run("day 10 may still be rejected", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAt(t, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnID(t, number)
		if err := s.Decide(ctx, requestID.String(), "rejected", "used", "ineligible", uuid.NullUUID{}); err != nil {
			t.Fatalf("reject day-10 return: %v", err)
		}
		status, refunds := returnPayout(t, requestID)
		if status != "rejected" || refunds != 0 {
			t.Errorf("rejected day-10 return is %q with %d refunds, want rejected/0", status, refunds)
		}
	})

	t.Run("day 15 approval is a staff exception and is still paid", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 15)
		number, lineID := deliveredOrderAt(t, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-15 return: %v", err)
		}
		requestID := openReturnID(t, number)
		if window := queueWindow(t, s, requestID); window != "after" {
			t.Fatalf("window = %q, want after", window)
		}
		found := queueRow(t, s, requestID)
		if found.Rescission() {
			t.Error("a day-15 request still reads as statutory entitlement")
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "goodwill exception", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve day-15 return: %v", err)
		}
		status, refunds := returnPayout(t, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("day-15 return is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaim(t, requestID); got != "exception" {
			t.Errorf("day-15 entitlement = %q, want exception", got)
		}
	})

	t.Run("undelivered keeps the existing decide path", func(t *testing.T) {
		requestID, _ := returnedOrder(t, 1)
		if window := queueWindow(t, s, requestID); window != "undelivered" {
			t.Fatalf("window = %q, want undelivered", window)
		}
		if err := s.Decide(ctx, requestID.String(), "rejected", "", "ineligible", uuid.NullUUID{}); err != nil {
			t.Fatalf("reject undelivered return: %v", err)
		}
		status, refunds := returnPayout(t, requestID)
		if status != "rejected" || refunds != 0 {
			t.Errorf("undelivered rejection is %q with %d refunds, want rejected/0", status, refunds)
		}
	})

	t.Run("partial delivery still decides against the delivered clock", func(t *testing.T) {
		requestID := partiallyDeliveredReturn(t,
			mustRFC3339(t, "2026-01-01T07:00:00+08:00"),
			mustRFC3339(t, "2026-01-06T12:00:00+08:00"),
		)
		if window := queueWindow(t, s, requestID); window != "within" {
			t.Fatalf("partial-delivery window = %q, want within", window)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve partial-delivery return: %v", err)
		}
		status, refunds := returnPayout(t, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("partial-delivery return is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaim(t, requestID); got != "statutory" {
			t.Errorf("partial-delivery entitlement = %q, want statutory", got)
		}
	})
}

func TestTwoStaffCannotBothRejectABlankStatutoryReason(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID := returnedOrderAtWithReason(t,
		mustRFC3339(t, "2026-01-01T07:00:00+08:00"),
		mustRFC3339(t, "2026-01-06T12:00:00+08:00"),
		"",
	)

	errc := make(chan error, 2)
	for range 2 {
		go func() {
			errc <- s.Decide(ctx, requestID.String(), "rejected", "", "", uuid.NullUUID{})
		}()
	}
	for range 2 {
		if err := <-errc; !errors.Is(err, admin.ErrRefused) {
			t.Errorf("concurrent blank statutory rejection = %v, want ErrRefused", err)
		}
	}
	status, refunds := returnPayout(t, requestID)
	if status != "requested" || refunds != 0 {
		t.Errorf("after two refused rejections the return is %q with %d refunds, want requested/0",
			status, refunds)
	}
}

func TestTwoStaffStillSerialiseALateApproval(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	requestID := returnedOrderAtWithReason(t,
		mustRFC3339(t, "2026-01-01T12:00:00+08:00"),
		mustRFC3339(t, "2026-01-16T12:00:00+08:00"),
		"",
	)
	if window := queueWindow(t, s, requestID); window != "after" {
		t.Fatalf("window = %q, want after", window)
	}

	errc := make(chan error, 2)
	for range 2 {
		go func() {
			errc <- s.Decide(ctx, requestID.String(), "approved", "exception", "", uuid.NullUUID{})
		}()
	}
	var won, lost int
	for range 2 {
		err := <-errc
		switch {
		case err == nil:
			won++
		case errors.Is(err, admin.ErrRefused):
			lost++
		default:
			t.Errorf("late concurrent approval = %v, want nil or ErrRefused", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Errorf("late concurrent approval won=%d lost=%d, want 1/1", won, lost)
	}
	status, refunds := returnPayout(t, requestID)
	if status != "approved" || refunds != 1 {
		t.Errorf("late concurrent approval is %q with %d refunds, want approved/1", status, refunds)
	}
	if got := decisionClaim(t, requestID); got != "exception" {
		t.Errorf("late concurrent entitlement = %q, want exception", got)
	}
}

func mustRFC3339(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %s: %v", value, err)
	}
	return ts
}

func shopNoonDaysAgo(t *testing.T, days int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(shoptime.Zone)
	if err != nil {
		t.Fatalf("load %s: %v", shoptime.Zone, err)
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, -days)
}

func openReturnID(t *testing.T, number string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT r.id FROM return_requests r
		JOIN orders o ON o.id = r.order_id
		WHERE o.order_number = $1 AND r.status = 'requested'`, number).Scan(&id); err != nil {
		t.Fatalf("find open return for %s: %v", number, err)
	}
	return id
}

func queueRow(t *testing.T, s *admin.Store, requestID uuid.UUID) pages.AdminReturn {
	t.Helper()
	view, err := s.Returns(t.Context())
	if err != nil {
		t.Fatalf("read the queue: %v", err)
	}
	for i := range view.Rows {
		if view.Rows[i].ID == requestID.String() {
			return view.Rows[i]
		}
	}
	t.Fatalf("return %s is not in the queue", requestID)
	return pages.AdminReturn{}
}

func queueWindow(t *testing.T, s *admin.Store, requestID uuid.UUID) string {
	t.Helper()
	return queueRow(t, s, requestID).Window
}

func returnPayout(t *testing.T, requestID uuid.UUID) (status string, refunds int) {
	t.Helper()
	if err := pool.QueryRow(t.Context(),
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read return status: %v", err)
	}
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refunds); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	return status, refunds
}

func decisionClaim(t *testing.T, requestID uuid.UUID) string {
	t.Helper()
	var entitlement string
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce("after"->>'entitlement', '')
		FROM audit_events
		WHERE action = 'return.decide' AND entity_id = $1
		ORDER BY occurred_at DESC LIMIT 1`, requestID).Scan(&entitlement); err != nil {
		t.Fatalf("read decision entitlement: %v", err)
	}
	return entitlement
}

func partiallyDeliveredReturn(t *testing.T, delivered, requested time.Time) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	var lines [2]uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	for i := range lines {
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '部分送達退貨', 100000, 1, $3) RETURNING id`,
			orderID, "PARTIAL-RET-"+uuid.NewString()[:8], i).Scan(&lines[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'partial-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	sessionID := "cs_ret_partial_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`, sessionID); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)

	var deliveredID, undeliveredID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (
			order_id, carrier, tracking_number, shipped_at, delivered_at
		) VALUES ($1, '黑貓', 'T-PART-D-' || $2, $3, $4)
		RETURNING id`, orderID, number, delivered.Add(-48*time.Hour), delivered).Scan(&deliveredID); err != nil {
		t.Fatalf("create delivered parcel: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at)
		VALUES ($1, '黑貓', 'T-PART-U-' || $2, $3)
		RETURNING id`, orderID, number, delivered.Add(-24*time.Hour)).Scan(&undeliveredID); err != nil {
		t.Fatalf("create undelivered parcel: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, deliveredID, lines[0]); err != nil {
		t.Fatalf("ship delivered line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, undeliveredID, lines[1]); err != nil {
		t.Fatalf("ship undelivered line: %v", err)
	}

	var requestID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason, created_at)
		VALUES ($1, '', $2) RETURNING id`, orderID, requested).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, requestID, lines[0]); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID
}
