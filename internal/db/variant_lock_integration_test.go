//go:build integration

package db_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
)

// TestAStockLockHoldsBackStockWritersButNotCarts holds each lock a variant is
// taken under, through the query and role that take it, in an open
// transaction. Meanwhile another shopper puts the same SKU in their cart, and
// the stock desk moves its stock.
//
// The cart's INSERT takes FOR KEY SHARE on the variant for its foreign key.
// FOR NO KEY UPDATE admits that and FOR UPDATE does not (PostgreSQL, "Explicit
// Locking", row-level lock modes), so the cart must not wait. The stock
// movement must wait: keeping the stock writers out is what each lock is for.
func TestAStockLockHoldsBackStockWritersButNotCarts(t *testing.T) {
	staff := stockLockStaff(t)
	for _, tc := range []struct {
		name string
		role string
		// hold returns the variant the lock is on and takes the lock in q.
		hold func(t *testing.T) (uuid.UUID, func(ctx context.Context, q *db.Queries) error)
	}{
		{"checkout", "store", func(t *testing.T) (uuid.UUID, func(context.Context, *db.Queries) error) {
			variant, _ := stockLockSKU(t, 5, 0)
			var cart uuid.UUID
			if err := schemaPool(t).QueryRow(t.Context(), `
				WITH c AS (INSERT INTO carts (digest) VALUES (sha256(gen_random_uuid()::text::bytea)) RETURNING id)
				INSERT INTO cart_items (cart_id, variant_id, quantity) SELECT id, $1, 1 FROM c
				RETURNING cart_id`, variant).Scan(&cart); err != nil {
				t.Fatalf("fill the checkout's cart: %v", err)
			}
			return variant, func(ctx context.Context, q *db.Queries) error {
				return q.LockCartCatalogue(ctx, cart)
			}
		}},
		{"hold release", "store", func(t *testing.T) (uuid.UUID, func(context.Context, *db.Queries) error) {
			variant, _ := stockLockSKU(t, 5, 0)
			reservation := stockLockHeldOrder(t, variant)
			return variant, func(ctx context.Context, q *db.Queries) error {
				return q.ReleaseReservation(ctx, reservation)
			}
		}},
		{"stock alert request", "store", func(t *testing.T) (uuid.UUID, func(context.Context, *db.Queries) error) {
			variant, slug := stockLockSKU(t, 0, 0)
			return variant, func(ctx context.Context, q *db.Queries) error {
				eligible, err := q.RequestStockNotice(ctx, db.RequestStockNoticeParams{
					VariantID: variant, Slug: slug,
					Email: "alert-" + uuid.NewString() + "@example.com", Locale: "zh-Hant",
				})
				if err == nil && !eligible {
					t.Fatal("the sold-out variant is not eligible for a stock alert; the lock was never taken")
				}
				return err
			}
		}},
		// A written-off unit is consumed, not restocked, so the explicit lock is
		// the only one settlement takes on the variant.
		{"unsent refund settlement", "admin", func(t *testing.T) (uuid.UUID, func(context.Context, *db.Queries) error) {
			o := newUnsentOrder(t, unsentOrderTotal)
			returnID := o.mustOpen(t, "damaged", unsentClaim{o.lineB, 2})
			o.pay(t, returnID, "succeeded")
			return o.variantB, func(ctx context.Context, q *db.Queries) error {
				return q.SettleUnsentRefund(ctx, db.SettleUnsentRefundParams{
					ReturnRequestID: returnID, ActorUserID: o.staff, RequestID: "settle-" + uuid.NewString(),
				})
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			variant, hold := tc.hold(t)

			holder, holderPID := stockLockTx(t, tc.role)
			if err := hold(ctx, db.New(holder)); err != nil {
				t.Fatalf("take the %s lock: %v", tc.name, err)
			}

			shopper, shopperPID := stockLockTx(t, "store")
			q := db.New(shopper)
			digest := sha256.Sum256([]byte(uuid.NewString()))
			cart, err := q.CreateCart(ctx, db.CreateCartParams{Digest: digest[:]})
			if err != nil {
				t.Fatalf("create the other shopper's cart: %v", err)
			}
			if _, err := q.LockCarts(ctx, []uuid.UUID{cart}); err != nil {
				t.Fatalf("lock the other shopper's cart: %v", err)
			}
			added := make(chan error, 1)
			go func() {
				added <- q.AddCartItem(ctx, db.AddCartItemParams{CartID: cart, VariantID: variant, Quantity: 1})
			}()
			if waited, err := stockLockWait(t, holderPID, shopperPID, added); waited {
				db.Rollback(ctx, holder)
				<-added
				t.Fatalf("another shopper's add-to-cart waited for the %s's variant lock; "+
					"a variant lock stronger than FOR NO KEY UPDATE conflicts with the cart's FOR KEY SHARE", tc.name)
			} else if err != nil {
				t.Fatalf("add the SKU to another cart: %v", err)
			}

			desk, deskPID := stockLockTx(t, "admin")
			moved := make(chan error, 1)
			go func() {
				moved <- db.New(desk).AdjustStock(ctx, db.AdjustStockParams{
					VariantID: variant, Delta: 1, IdempotencyKey: "stock-lock-" + uuid.NewString(), ActorUserID: staff,
				})
			}()
			waited, err := stockLockWait(t, holderPID, deskPID, moved)
			if !waited {
				t.Fatalf("a stock movement went ahead (%v) while the %s held the variant; "+
					"the lock no longer keeps stock writers out", err, tc.name)
			}
			db.Rollback(ctx, holder)
			if err := <-moved; err != nil {
				t.Fatalf("stock movement after the %s ended: %v", tc.name, err)
			}
		})
	}
}

// stockLockWait returns once the statement behind done has finished, or is
// waiting on a lock the holder's backend holds, and says which. The holder
// keeps its lock until the caller ends it, so a statement that has to wait is
// seen waiting: pg_stat_activity decides, and 5ms is only the poll interval.
func stockLockWait(t *testing.T, holder, waiter int32, done <-chan error) (bool, error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return false, err
		default:
		}
		_, blocked, err := dbtest.Blocked(t.Context(), schemaPool(t),
			dbtest.Match{PID: waiter, BlockedBy: []int32{holder}})
		if err != nil {
			t.Fatalf("observe lock wait: %v", err)
		}
		if blocked {
			return true, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("backend %d neither finished nor waited on backend %d", waiter, holder)
	return false, nil
}

// stockLockTx begins a transaction as role and returns it with its backend.
// It is rolled back when the test ends.
func stockLockTx(t *testing.T, role string) (pgx.Tx, int32) {
	t.Helper()
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin as %s: %v", role, err)
	}
	t.Cleanup(func() { db.Rollback(context.WithoutCancel(ctx), tx) })
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatalf("set role %s: %v", role, err)
	}
	var pid int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read the %s backend: %v", role, err)
	}
	return tx, pid
}

func stockLockStaff(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := schemaPool(t).QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('stock-lock-' || gen_random_uuid() || '@goen.invalid', 'staff', '庫存人員')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create a staff member: %v", err)
	}
	return id
}

// stockLockSKU commits an active product with one variant holding stock units
// above a floor of safety, and returns the variant and the product's slug.
func stockLockSKU(t *testing.T, stock, safety int) (uuid.UUID, string) {
	t.Helper()
	ctx := t.Context()
	slug := "stock-lock-" + uuid.NewString()[:8]
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(context.WithoutCancel(ctx), tx)
	var variant uuid.UUID
	if err := tx.QueryRow(ctx, `
		WITH c AS (
			INSERT INTO categories (slug, name, position)
			SELECT $1, '庫存鎖', greatest(max(position), 10000) + 1
			FROM categories WHERE parent_id IS NULL RETURNING id
		), p AS (
			INSERT INTO products (category_id, slug, name, status, published_at)
			SELECT id, $1, '庫存鎖測試', 'active', now() FROM c RETURNING id
		)
		INSERT INTO product_variants (product_id, sku, price_cents, safety_stock)
		SELECT id, upper($1), 50000, $2 FROM p RETURNING id`, slug, safety).Scan(&variant); err != nil {
		t.Fatalf("create the SKU: %v", err)
	}
	if stock > 0 {
		if _, err := tx.Exec(ctx, `SELECT record_inventory_movement($1, $2, 'receipt', 'seed:' || $3, 'admin', NULL, NULL)`,
			variant, stock, slug); err != nil {
			t.Fatalf("stock the SKU: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the SKU: %v", err)
	}
	return variant, slug
}

// stockLockHeldOrder commits an unpaid checkout of one unit of variant, the
// kind the hold sweeper releases, and returns its hold.
func stockLockHeldOrder(t *testing.T, variant uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(context.WithoutCancel(ctx), tx)
	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, execErr := tx.Exec(ctx, stmt, args...); execErr != nil {
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}
	exec(`INSERT INTO shipping_methods (id, code)
	      VALUES ('5e5e0001-0000-4000-8000-0000000000a1', 'stock_lock_home') ON CONFLICT DO NOTHING`)
	exec(`INSERT INTO shipping_method_versions (id, method_id, name, fee_cents)
	      VALUES ('5e5e0002-0000-4000-8000-0000000000a1', '5e5e0001-0000-4000-8000-0000000000a1',
	              '宅配', 0) ON CONFLICT DO NOTHING`)
	var order uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		VALUES (next_order_number(), '5e5e0002-0000-4000-8000-0000000000a1', 'stock_lock_home', '宅配')
		RETURNING id`).Scan(&order); err != nil {
		t.Fatalf("create the order: %v", err)
	}
	exec(`INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
	      SELECT $1, pv.id, pv.sku, '庫存鎖測試', pv.price_cents, 1 FROM product_variants pv WHERE pv.id = $2`,
		order, variant)
	exec(`INSERT INTO order_private_data (order_id, email, recipient_name, phone,
	                                      postal_code, city, district, street)
	      VALUES ($1, 'stock-lock@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		order)
	var reservation uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		order, variant, "hold:"+order.String()).Scan(&reservation); err != nil {
		t.Fatalf("hold the unit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the order: %v", err)
	}
	return reservation
}
