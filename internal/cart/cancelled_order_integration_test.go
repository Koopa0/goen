//go:build integration

package cart_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestOrderTimelineRefundID holds that the shopper's order timeline never shows
// the provider's refund id.
func TestOrderTimelineRefundID(t *testing.T) {
	ctx := t.Context()
	vid := ordertest.FreshVariant(t, pool, "refundid-1")
	orderID := ordertest.HeldOrderFor(t, pool, uuid.NullUUID{}, vid, -time.Hour, true)
	ordertest.RefundedAndCancelled(t, pool, orderID)

	var number, staffNote string
	if err := pool.QueryRow(ctx, `
		SELECT o.order_number, e.note FROM orders o
		JOIN order_events e ON e.order_id = o.id AND e.kind = 'refunded'
		WHERE o.id = $1`, orderID).Scan(&number, &staffNote); err != nil || !strings.HasPrefix(staffNote, "re_") {
		t.Fatalf("the fixture must keep the refund id on the event: %q, %v", staffNote, err)
	}
	view, err := buyer.NewStore(pool).Order(ctx, number)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	var sawRefund bool
	for _, e := range view.Timeline {
		if e.Kind == "refunded" {
			sawRefund = true
		}
		if strings.Contains(e.Note, "re_") {
			t.Errorf("%s event shows the provider refund id %q", e.Kind, e.Note)
		}
	}
	if !sawRefund {
		t.Error("the refunded event is missing from the shopper's timeline")
	}
}

// TestProductReviewsVerifiedIsReal holds that a cancelled order is not a
// verified purchase: the product_reviews_verified_is_real constraint refuses
// the badge once the order is cancelled.
func TestProductReviewsVerifiedIsReal(t *testing.T) {
	ctx := t.Context()
	vid := ordertest.FreshVariant(t, pool, "stockfix-15")

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('cancelbadge@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	orderID := ordertest.HeldOrderFor(t, pool, uuid.NullUUID{UUID: userID, Valid: true}, vid, -time.Hour, true) // funded

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE id = $1`, vid).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	review := func() error {
		_, err := pool.Exec(ctx, `
			INSERT INTO product_reviews (product_id, user_id, rating, body, is_verified_purchase)
			VALUES ($1, $2, 5, '測試評價', true)
			ON CONFLICT (product_id, user_id) DO UPDATE SET is_verified_purchase = true`,
			productID, userID)
		return err
	}

	if err := review(); err != nil {
		t.Fatalf("a real purchase could not claim 已購買: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM product_reviews WHERE product_id = $1 AND user_id = $2`,
		productID, userID); err != nil {
		t.Fatalf("clear review: %v", err)
	}

	ordertest.RefundedAndCancelled(t, pool, orderID)
	err := review()
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "product_reviews_verified_is_real" {
		t.Errorf("a cancelled order still earned 已購買: %v", err)
	}
}
