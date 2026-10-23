//go:build integration

package catalog_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestProductReviewsVerifiedIsReal holds that a cancelled order is not a
// verified purchase: the product_reviews_verified_is_real constraint refuses
// the badge once the order is cancelled.
func TestProductReviewsVerifiedIsReal(t *testing.T) {
	ctx := t.Context()
	vid := ordertest.FreshVariant(t, pool, "stockfix-15")

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"cancelbadge-"+uuid.NewString()+"@example.com").Scan(&userID); err != nil {
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
