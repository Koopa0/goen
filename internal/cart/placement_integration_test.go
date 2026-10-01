//go:build integration

package cart_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/cart"
)

// TestAPlacedOrderTakesNoLineFromALaterRequest replays, as store, how a
// storefront request would take stock off sale against somebody else's order:
// add a line for another variant to an order checkout has already placed, then
// hold that variant. Checkout is the neighbour: it writes every line in the
// transaction that places the order, and it has just done so here as store.
func TestAPlacedOrderTakesNoLineFromALaterRequest(t *testing.T) {
	ctx := t.Context()
	asStore := storeRolePool(t)
	number := placeUnpaidOrderFor(t, cart.NewStore(asStore),
		"placed-lines-"+uuid.NewString()[:8]+"@example.com")

	var orderID, target uuid.UUID
	var stockBefore int32
	if err := pool.QueryRow(ctx, `
		SELECT o.id, pv.id, pv.stock_quantity
		FROM orders o, product_variants pv
		WHERE o.order_number = $1
		  AND pv.stock_quantity > 3
		  AND NOT EXISTS (SELECT 1 FROM order_lines l
		                  WHERE l.order_id = o.id AND l.variant_id = pv.id)
		ORDER BY pv.stock_quantity DESC, pv.id LIMIT 1`, number).
		Scan(&orderID, &target, &stockBefore); err != nil {
		t.Fatalf("read the placed order and a variant it does not carry: %v", err)
	}

	constraint := func(err error) string {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			return pgErr.ConstraintName
		}
		return ""
	}
	inTx := func(stmt string, args ...any) error {
		t.Helper()
		tx, err := asStore.Begin(ctx)
		if err != nil {
			t.Fatalf("begin as store: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		if _, err := tx.Exec(ctx, stmt, args...); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	lineErr := inTx(`
		INSERT INTO order_lines (order_id, variant_id, sku, product_name,
		                         unit_price_cents, quantity, position)
		SELECT $1, pv.id, pv.sku, p.name, 0, 3, 99
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.id = $2`, orderID, target)
	if constraint(lineErr) != "order_lines_written_while_placing" {
		t.Errorf("a line added to an order another transaction placed = %v, "+
			"want order_lines_written_while_placing", lineErr)
	}

	holdErr := inTx(`SELECT hold_inventory($1, $2, 3, interval '60 minutes', $3)`,
		orderID, target, "replay:"+uuid.NewString())
	if constraint(holdErr) != "inventory_hold_within_order_lines" {
		t.Errorf("a hold on the placed order for a variant it never carried = %v, "+
			"want inventory_hold_within_order_lines", holdErr)
	}

	var stockAfter int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, target).
		Scan(&stockAfter); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if stockAfter != stockBefore {
		t.Errorf("stock moved from %d to %d against an order placed by somebody else",
			stockBefore, stockAfter)
	}
}

// TestAnOrderTakesNoLineFromAnotherTransactionThatBeganWithIt: two checkouts
// can begin at the same instant, so the begin time alone does not say which
// transaction placed an order; the transaction's id does.
func TestAnOrderTakesNoLineFromAnotherTransactionThatBeganWithIt(t *testing.T) {
	ctx := t.Context()
	asStore := storeRolePool(t)
	number := placeUnpaidOrderFor(t, cart.NewStore(asStore),
		"same-instant-"+uuid.NewString()[:8]+"@example.com")

	var orderID, target uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id, pv.id
		FROM orders o, product_variants pv
		WHERE o.order_number = $1
		  AND pv.stock_quantity > 3
		  AND NOT EXISTS (SELECT 1 FROM order_lines l
		                  WHERE l.order_id = o.id AND l.variant_id = pv.id)
		ORDER BY pv.stock_quantity DESC, pv.id LIMIT 1`, number).
		Scan(&orderID, &target); err != nil {
		t.Fatalf("read the placed order and a variant it does not carry: %v", err)
	}

	tx, err := asStore.Begin(ctx)
	if err != nil {
		t.Fatalf("begin as store: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var began time.Time
	if err := tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&began); err != nil {
		t.Fatalf("read the transaction's start: %v", err)
	}
	// Another transaction that began at the same instant placed this order.
	if _, err := pool.Exec(ctx, `UPDATE orders SET placed_in_xact_began = $2 WHERE id = $1`,
		orderID, began); err != nil {
		t.Fatalf("forge the placing transaction's start: %v", err)
	}

	_, lineErr := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name,
		                         unit_price_cents, quantity, position)
		SELECT $1, pv.id, pv.sku, p.name, 0, 5, 99
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.id = $2`, orderID, target)
	pgErr, ok := errors.AsType[*pgconn.PgError](lineErr)
	if !ok || pgErr.ConstraintName != "order_lines_written_while_placing" {
		t.Errorf("a line added by another transaction that began at the placing one's instant = %v, "+
			"want order_lines_written_while_placing", lineErr)
	}
}

// TestARestoredOrderTakesNoLineFromTheTransactionThatDrawsItsID: a dump
// restored into another cluster keeps each order's placed_in_xact as data, and
// that cluster's transaction counter has not reached those values yet. The
// transaction that eventually draws one, which store can aim for by spending
// ids, is not the one that placed the order and takes it no line.
func TestARestoredOrderTakesNoLineFromTheTransactionThatDrawsItsID(t *testing.T) {
	ctx := t.Context()
	asStore := storeRolePool(t)
	number := placeUnpaidOrderFor(t, cart.NewStore(asStore),
		"restored-lines-"+uuid.NewString()[:8]+"@example.com")

	var orderID, target uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id, pv.id
		FROM orders o, product_variants pv
		WHERE o.order_number = $1
		  AND pv.stock_quantity > 3
		  AND NOT EXISTS (SELECT 1 FROM order_lines l
		                  WHERE l.order_id = o.id AND l.variant_id = pv.id)
		ORDER BY pv.stock_quantity DESC, pv.id LIMIT 1`, number).
		Scan(&orderID, &target); err != nil {
		t.Fatalf("read the placed order and a variant it does not carry: %v", err)
	}

	tx, err := asStore.Begin(ctx)
	if err != nil {
		t.Fatalf("begin as store: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var drawn int64
	if err := tx.QueryRow(ctx, `SELECT pg_current_xact_id()::text::bigint`).Scan(&drawn); err != nil {
		t.Fatalf("draw a transaction id: %v", err)
	}
	// What the restore leaves: the order carries, as data, the id this later
	// transaction has just drawn.
	if _, err := pool.Exec(ctx, `UPDATE orders SET placed_in_xact = $2 WHERE id = $1`,
		orderID, drawn); err != nil {
		t.Fatalf("forge the placing transaction: %v", err)
	}

	_, lineErr := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name,
		                         unit_price_cents, quantity, position)
		SELECT $1, pv.id, pv.sku, p.name, 0, 5, 99
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.id = $2`, orderID, target)
	pgErr, ok := errors.AsType[*pgconn.PgError](lineErr)
	if !ok || pgErr.ConstraintName != "order_lines_written_while_placing" {
		t.Errorf("a line added by the transaction that drew a restored order's placing id = %v, "+
			"want order_lines_written_while_placing", lineErr)
	}
}
