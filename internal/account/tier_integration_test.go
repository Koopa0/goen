//go:build integration

package account_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

// TestStore_OverviewTierDerived: a tier is derived from spend and not stored.
func TestStore_OverviewTierDerived(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, "tiered-account@example.com")

	before, err := s.Overview(ctx, u)
	if err != nil {
		t.Fatalf("read the account: %v", err)
	}
	if before.Standing.HasTier() {
		t.Fatalf("a customer who has bought nothing is in %q", before.Standing.TierName)
	}
	if !before.Standing.HasNext() {
		t.Fatal("no next tier is offered — the seed has no bands and this proves nothing")
	}

	orderID := committedOrderFor(t, u.ID, 2000000) // NT$20,000

	after, err := s.Overview(ctx, u)
	if err != nil {
		t.Fatalf("re-read the account: %v", err)
	}
	if !after.Standing.HasTier() {
		t.Fatalf("NT$20,000 of committed spend earned no tier (spend read as %d)",
			after.Standing.SpendCents)
	}
	if after.Standing.MultiplierBP <= 10000 {
		t.Errorf("the tier earns %d bp, want more than the base rate", after.Standing.MultiplierBP)
	}

	refundAndCancel(t, orderID)
	gone, err := s.Overview(ctx, u)
	if err != nil {
		t.Fatalf("read the account after cancelling: %v", err)
	}
	if gone.Standing.HasTier() || gone.Standing.SpendCents != 0 {
		t.Errorf("a cancelled order still counts: tier %q, spend %d",
			gone.Standing.TierName, gone.Standing.SpendCents)
	}
}

// refundAndCancel cancels a paid order the one way the schema admits: a full
// refund before shipment whose card refund has settled.
func refundAndCancel(t *testing.T, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	var staff, returnID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role)
		VALUES ('refund-' || gen_random_uuid() || '@goen.invalid', 'staff') RETURNING id`).Scan(&staff); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT open_refund_before_shipment(order_number, 'test', $2, 'test')
		FROM orders WHERE id = $1`, orderID, staff).Scan(&returnID); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO refunds (payment_id, return_request_id, request_key, status, amount_cents,
		                      reason, provider_ref, succeeded_at)
		 SELECT p.id, r.id, 'return:' || r.id, 'succeeded', r.card_refund_cents,
		        r.resolution, 're_' || r.id, now()
		 FROM return_requests r JOIN payments p ON p.order_id = r.order_id AND p.status = 'succeeded'
		 WHERE r.id = $1`,
		`INSERT INTO order_events (order_id, kind, return_request_id)
		 SELECT order_id, 'refunded', id FROM return_requests WHERE id = $1`,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = (SELECT order_id FROM return_requests WHERE id = $1)`,
	} {
		if _, err := pool.Exec(ctx, stmt, returnID); err != nil {
			t.Fatalf("settle the refund and cancel: %v", err)
		}
	}
}

// committedOrderFor writes a paid order for one customer and returns its id.
func committedOrderFor(t *testing.T, userID string, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'TIER-SKU', '測試商品', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'tiered-account@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`,
		orderID, "cs_tier_"+number, cents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`,
		"cs_tier_"+number, cents); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID
}

// TestStore_OverviewBandLocale: the membership band reads in the visitor's
// language. Driven through Overview, which is the query the account page
// actually reads.
func TestStore_OverviewBandLocale(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)

	// No spend at all, so the NEXT band is the lowest one.
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('band-'||gen_random_uuid()||'@goen.invalid', 'customer', '等級測試')
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
		absent string
	}{
		{name: "Chinese", locale: i18n.ZhHant, want: "銀卡會員", absent: "Silver"},
		{name: "English", locale: i18n.En, want: "Silver", absent: "銀卡會員"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			view, err := s.Overview(i18n.WithLocale(ctx, tt.locale),
				user.User{ID: id, Role: user.RoleCustomer})
			if err != nil {
				t.Fatalf("Overview: %v", err)
			}
			if view.Standing.NextName != tt.want {
				t.Errorf("the next band reads %q, want %q",
					view.Standing.NextName, tt.want)
			}
			if view.Standing.NextName == tt.absent {
				t.Errorf("the next band reads %q, the other language",
					view.Standing.NextName)
			}
		})
	}
}
