//go:build integration

// Package checkouttest places orders through the real checkout for the
// integration suites of other packages, and for checkout's own. Fidelity is why
// it exists: what a placed order writes (its coupon redemptions, its lines, its
// holds, the credit it spends) is written in the placing transaction, so SQL
// cannot build such an order honestly. An order written with SQL is ordertest's.
//
// It imports checkout, which imports the packages whose suites would want it,
// so only an external test package (package foo_test) may import it. Every file
// carries the integration tag, and only test files import it.
package checkouttest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/discount/coupon"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// AttemptKey turns a readable test label into the same canonical
// fixed-size identity the checkout page issues. It is idempotent so concurrency
// fixtures may share one identity between a manual advisory lock and PlaceOrder.
func AttemptKey(label string) string {
	if decoded, err := base64.RawURLEncoding.DecodeString(label); err == nil &&
		len(decoded) == 16 && base64.RawURLEncoding.EncodeToString(decoded) == label {
		var nonzero bool
		for _, b := range decoded {
			nonzero = nonzero || b != 0
		}
		if nonzero {
			return label
		}
	}
	digest := sha256.Sum256([]byte(label))
	return base64.RawURLEncoding.EncodeToString(digest[:16])
}

// QuoteTotal is what a shipping quote comes to.
func QuoteTotal(t *testing.T, quote checkout.ShippingQuote) int64 {
	t.Helper()
	total, err := quote.Total()
	if err != nil {
		t.Fatalf("total shipping quote: %v", err)
	}
	return total
}

// NewCart opens an empty cart on the store's own pool and returns its id.
func NewCart(t *testing.T, s *checkout.Store) uuid.UUID {
	t.Helper()
	id, _ := NewCartSession(t, s)
	return id
}

// NewCartSession is NewCart with the token that owns the cart.
func NewCartSession(t *testing.T, s *checkout.Store) (id uuid.UUID, token string) {
	t.Helper()
	tok := bearer.New()
	id, err := s.Carts().Create(t.Context(), tok, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	return id, tok
}

// Quote builds the quote a direct Store test would have rendered. HTTP
// tests carry these values through the real hidden controls instead. Keeping
// this helper in a test package avoids a production escape hatch that could
// place an unconfirmed current quote.
func Quote(
	t *testing.T,
	s *checkout.Store,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	shippingID uuid.UUID,
	addr *delivery.Details,
	couponCode string,
) checkout.QuoteID {
	t.Helper()
	facts := QuoteFacts(t, s, cartID, owner, shippingID, addr, couponCode)
	id, err := facts.ID()
	if err != nil {
		t.Fatalf("build checkout quote: %v", err)
	}
	return id
}

// QuoteFacts is the quote Quote hashes, for a test that has to
// change one fact of what the checkout rendered.
func QuoteFacts(
	t *testing.T,
	s *checkout.Store,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	shippingID uuid.UUID,
	addr *delivery.Details,
	couponCode string,
) checkout.Quote {
	t.Helper()
	view, err := s.Carts().View(t.Context(), cartID)
	if err != nil {
		t.Fatalf("read cart quote: %v", err)
	}
	quote, err := s.QuoteShipping(t.Context(), shippingID, view.SubtotalCents, addr.PostalCode)
	if err != nil {
		t.Fatalf("quote delivery: %v", err)
	}
	shippingFee, discount := QuoteTotal(t, quote), int64(0)
	if couponCode != "" {
		c, findErr := s.CouponByCode(t.Context(), couponCode)
		if findErr != nil {
			t.Fatalf("find quoted coupon: %v", findErr)
		}
		var free bool
		discount, free, err = c.Apply(view.SubtotalCents, quote.FeeCents == 0)
		if err != nil {
			t.Fatalf("apply coupon to quote: %v", err)
		}
		if free {
			shippingFee = quote.Surcharge
		}
	}
	gross := view.SubtotalCents + shippingFee - discount
	balance, err := s.AvailableCredit(t.Context(), owner)
	if err != nil {
		t.Fatalf("read quoted credit: %v", err)
	}
	lines := make([]checkout.QuoteLine, 0, len(view.Lines))
	for i := range view.Lines {
		line := &view.Lines[i]
		variantID, parseErr := uuid.Parse(line.VariantID)
		if parseErr != nil {
			t.Fatalf("parse quoted variant: %v", parseErr)
		}
		lines = append(lines, checkout.QuoteLine{
			VariantID: variantID,
			Quantity:  line.Quantity,
			UnitCents: line.UnitCents,
		})
	}
	return checkout.Quote{
		CartID:            cartID,
		Lines:             lines,
		ShippingVersionID: shippingID,
		ShippingCents:     shippingFee,
		CouponCode:        coupon.Normalise(couponCode),
		DiscountCents:     discount,
		CreditCents:       min(balance, gross),
	}
}

// PlaceOrder places the cart through the real checkout, with the quote a direct
// Store test would have rendered, and returns the order number.
func PlaceOrder(
	t *testing.T,
	s *checkout.Store,
	ctx context.Context,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	shippingID uuid.UUID,
	addr *delivery.Details,
	couponCode string,
	key string,
) (string, error) {
	t.Helper()
	quote := Quote(t, s, cartID, owner, shippingID, addr, couponCode)
	return s.PlaceOrder(
		ctx, cartID, owner, shippingID, addr, nil, couponCode, quote,
		AttemptKey(key),
	)
}

// CreditedAccount is ordertest.CreditedCustomer plus the account id.
func CreditedAccount(t *testing.T, pool *pgxpool.Pool, cents int64) (userID, accountID uuid.UUID) {
	t.Helper()
	userID = ordertest.CreditedCustomer(t, pool, cents)
	if err := pool.QueryRow(t.Context(),
		`SELECT id FROM store_credit_accounts WHERE user_id = $1`, userID).
		Scan(&accountID); err != nil {
		t.Fatalf("read credit account: %v", err)
	}
	return userID, accountID
}

// CreditBalance is what the ledger sums to for one account.
func CreditBalance(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE account_id = $1`,
		accountID).Scan(&cents); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return cents
}

// PlaceUnpaidOrderFor places one order for an address, through the store's own
// checkout.
func PlaceUnpaidOrderFor(t *testing.T, pool *pgxpool.Pool, s *checkout.Store, address string) string {
	t.Helper()
	return PlaceOrderOfUnits(t, pool, s, address, 1)
}

// PlaceOrderOfUnits is PlaceUnpaidOrderFor with that many units of its one line.
func PlaceOrderOfUnits(t *testing.T, pool *pgxpool.Pool, s *checkout.Store, address string, units int32) string {
	t.Helper()
	ctx := t.Context()

	id := NewCart(t, s)
	if err := s.Carts().Add(ctx, id, ordertest.FreshVariant(t, pool, "unpaid-order"), units); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).
		Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: address, RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := PlaceOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "",
		"forge-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	return number
}

// PlaceLapsed places an order through the real checkout for a customer who
// spends credit and a single-use coupon on it, then ages its holds past expiry.
func PlaceLapsed(t *testing.T, pool *pgxpool.Pool, label string) (number string, accountID, couponID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	s := checkout.NewStore(pool)
	vid := ordertest.FreshVariant(t, pool, label)
	userID, accountID := CreditedAccount(t, pool, 30000)
	code := "LAPSE-" + strings.ToUpper(uuid.NewString()[:8])
	if err := pool.QueryRow(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, max_redemptions, per_customer_limit)
		VALUES ($1, '只能用一次', 'amount', 20000, 1, 1)
		RETURNING id`, code).Scan(&couponID); err != nil {
		t.Fatalf("create coupon: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "lapsed@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	cartID := NewCart(t, s)
	if err := s.Carts().Add(ctx, cartID, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := PlaceOrder(t, s, ctx, cartID, uuid.NullUUID{UUID: userID, Valid: true},
		shipID, addr, code, label+"-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if got := CreditBalance(t, pool, accountID); got != 0 {
		t.Fatalf("balance after checkout = %d, want 0; the fixture spends no credit", got)
	}
	ExpireHolds(t, pool, number)
	return number, accountID, couponID
}

// ExpireHolds ages every hold of an order past its expiry.
func ExpireHolds(t *testing.T, pool *pgxpool.Pool, number string) {
	t.Helper()
	tag, err := pool.Exec(t.Context(), `
		UPDATE inventory_reservations
		SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 minute'
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number)
	if err != nil || tag.RowsAffected() == 0 {
		t.Fatalf("expire holds of %s: %v (%d rows)", number, err, tag.RowsAffected())
	}
}
