//go:build integration

package payment_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
)

// TestFullyCreditFundedOrderEarnsPointsAfterPicking holds that store credit
// closing an order posts the same funding-complete effects as a card capture
// once fulfilment takes it on. Advance is a back-office write: the staff
// actor has to be on the context, or auditIn refuses the row before
// CompleteFunding runs.
func TestFullyCreditFundedOrderEarnsPointsAfterPicking(t *testing.T) {
	const cents = int64(50000)
	userID := creditedUser(t, cents)
	number, orderID := ownedOrder(t, userID, cents)
	spendCreditOnOrder(t, orderID, -cents)
	advanceToPicking(t, number)
	assertFundingComplete(t, orderID, number, 5, fundingReceipt{amountCents: cents})
}

// TestAdminRoleCompletesCreditFundingOnPicking drives the live picking door as
// the production admin role. The suite pool is a superuser and would hide a
// missing EXECUTE on award_loyalty_points behind a green Advance.
func TestAdminRoleCompletesCreditFundingOnPicking(t *testing.T) {
	const cents = int64(50000)
	userID := creditedUser(t, cents)
	number, orderID := ownedOrder(t, userID, cents)
	spendCreditOnOrder(t, orderID, -cents)

	adminPool := adminRolePool(t)
	var role string
	var superuser bool
	if err := adminPool.QueryRow(t.Context(),
		`SELECT current_user, current_setting('is_superuser')::boolean`).
		Scan(&role, &superuser); err != nil {
		t.Fatalf("read admin-role session: %v", err)
	}
	if role != "admin" || superuser {
		t.Fatalf("admin-role pool is %s superuser=%t; SET ROLE did not bind", role, superuser)
	}

	ctx, actor := staffContext(t)
	backOffice := admintest.OrderStore(adminPool, disabledGateway(t), nil, nil)
	if _, err := backOffice.Advance(ctx, number, "picking", uuid.NullUUID{UUID: actor, Valid: true}); err != nil {
		t.Fatalf("admin-role advance to picking: %v", err)
	}
	assertFundingComplete(t, orderID, number, 5, fundingReceipt{amountCents: cents})
}

// TestFundingCompleteSideEffectsOnce is the #49 acceptance lock: each funding
// path posts exactly one paid event, one receipt, and the points the order
// earns; a second production door must not duplicate any of them.
func TestFundingCompleteSideEffectsOnce(t *testing.T) {
	const cents = int64(50000)
	tests := []struct {
		name       string
		wantPoints int64
		want       fundingReceipt
		run        func(t *testing.T) (number string, orderID uuid.UUID, replay func())
	}{
		{
			name:       "card-only",
			wantPoints: 5,
			want:       fundingReceipt{amountCents: cents, card: "Visa •••• 4242"},
			run: func(t *testing.T) (string, uuid.UUID, func()) {
				t.Helper()
				userID := newCustomer(t)
				number, orderID := ownedOrder(t, userID, cents)
				s := payment.NewStore(pool)
				session := "cs_fund_card_" + number
				captureOwned(t, s, number, session, cents, "visa", "4242")
				return number, orderID, func() {
					if _, err := captureThroughWebhook(t, s, payment.Capture{
						SessionID: session, AmountRecv: cents,
					}); err != nil {
						t.Fatalf("replay capture: %v", err)
					}
					advanceToPicking(t, number)
				}
			},
		},
		{
			name:       "credit-only",
			wantPoints: 5,
			want:       fundingReceipt{amountCents: cents},
			run: func(t *testing.T) (string, uuid.UUID, func()) {
				t.Helper()
				userID := creditedUser(t, cents)
				number, orderID := ownedOrder(t, userID, cents)
				spendCreditOnOrder(t, orderID, -cents)
				advanceToPicking(t, number)
				return number, orderID, func() {
					replayCompleteFunding(t, orderID, number)
				}
			},
		},
		{
			name:       "coupon-to-zero",
			wantPoints: 0,
			want:       fundingReceipt{amountCents: 0},
			run: func(t *testing.T) (string, uuid.UUID, func()) {
				t.Helper()
				userID := newCustomer(t)
				number, orderID := ownedOrder(t, userID, cents)
				redeemCouponCovering(t, orderID, userID, cents)
				advanceToPicking(t, number)
				return number, orderID, func() {
					replayCompleteFunding(t, orderID, number)
				}
			},
		},
		{
			name:       "split funding",
			wantPoints: 5,
			want:       fundingReceipt{amountCents: cents, card: "Mastercard •••• 5555"},
			run: func(t *testing.T) (string, uuid.UUID, func()) {
				t.Helper()
				const credit, card = int64(20000), int64(30000)
				userID := creditedUser(t, credit)
				number, orderID := ownedOrder(t, userID, cents)
				spendCreditOnOrder(t, orderID, -credit)
				s := payment.NewStore(pool)
				session := "cs_fund_split_" + number
				captureOwned(t, s, number, session, card, "mastercard", "5555")
				return number, orderID, func() {
					if _, err := captureThroughWebhook(t, s, payment.Capture{
						SessionID: session, AmountRecv: card,
					}); err != nil {
						t.Fatalf("replay capture: %v", err)
					}
					advanceToPicking(t, number)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			number, orderID, replay := tt.run(t)
			assertFundingComplete(t, orderID, number, tt.wantPoints, tt.want)
			replay()
			assertFundingComplete(t, orderID, number, tt.wantPoints, tt.want)
		})
	}
}

func advanceToPicking(t *testing.T, number string) {
	t.Helper()
	ctx, actor := staffContext(t)
	backOffice := admintest.OrderStore(pool, disabledGateway(t), nil, nil)
	if _, err := backOffice.Advance(ctx, number, "picking", uuid.NullUUID{UUID: actor, Valid: true}); err != nil {
		t.Fatalf("advance to picking: %v", err)
	}
}

func replayCompleteFunding(t *testing.T, orderID uuid.UUID, number string) {
	t.Helper()
	if err := payment.CompleteFunding(t.Context(), db.New(pool), orderID, number, payment.Capture{}); err != nil {
		t.Fatalf("replay CompleteFunding: %v", err)
	}
}

func captureOwned(t *testing.T, s *payment.Store, number, session string, cents int64, cardBrand, cardLast4 string) {
	t.Helper()
	if err := s.OpenPayment(t.Context(), number, session, cents); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := captureThroughWebhook(t, s, payment.Capture{
		SessionID: session, AmountRecv: cents, CardBrand: cardBrand, CardLast4: cardLast4,
	}); err != nil {
		t.Fatalf("capture: %v", err)
	}
}

func redeemCouponCovering(t *testing.T, orderID, userID uuid.UUID, cents int64) {
	t.Helper()
	ctx := t.Context()
	code := "Z" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:12])
	var couponID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents)
		VALUES ($1, '測試折抵', 'amount', $2)
		RETURNING id`, code, cents).Scan(&couponID); err != nil {
		t.Fatalf("create coupon: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin coupon redeem: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET discount_cents = $1 WHERE id = $2`, cents, orderID); err != nil {
		t.Fatalf("set the discount: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT redeem_coupon($1, $2, $3, $4)`, couponID, orderID, userID, cents); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit coupon redeem: %v", err)
	}
}

func newCustomer(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('fund-'||gen_random_uuid()||'@goen.invalid', 'customer', '付款測試')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return id
}

// fundingReceipt is what order.paid must carry after funding closes.
type fundingReceipt struct {
	amountCents int64
	card        string
}

func assertFundingComplete(t *testing.T, orderID uuid.UUID, number string, wantPoints int64, want fundingReceipt) {
	t.Helper()
	ctx := t.Context()
	o, err := payment.NewStore(pool).Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if !o.Paid {
		t.Error("OrderIsPaid disagrees with a closed funding path — reports unpaid")
	}
	for _, assertion := range []struct {
		name string
		sql  string
		args []any
		want int64
	}{
		{"paid event", `SELECT count(*) FROM order_events WHERE order_id = $1 AND kind = 'paid'`, []any{orderID}, 1},
		{"points", `SELECT coalesce(sum(points), 0) FROM loyalty_entries WHERE order_id = $1`, []any{orderID}, wantPoints},
	} {
		var got int64
		if err := pool.QueryRow(ctx, assertion.sql, assertion.args...).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", assertion.name, err)
		}
		if got != assertion.want {
			t.Errorf("%s = %d, want %d", assertion.name, got, assertion.want)
		}
	}

	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM outbox_messages WHERE topic = 'order.paid' AND dedupe_key = $1`,
		number).Scan(&payload); err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	var got outbox.OrderPaid
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	if got.OrderNumber != number {
		t.Errorf("receipt order_number = %q, want %q", got.OrderNumber, number)
	}
	if got.AmountCents != want.amountCents {
		t.Errorf("receipt amount_cents = %d, want %d", got.AmountCents, want.amountCents)
	}
	if got.Card != want.card {
		t.Errorf("receipt card = %q, want %q", got.Card, want.card)
	}
}
