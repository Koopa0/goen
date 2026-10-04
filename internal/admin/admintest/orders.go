//go:build integration

package admintest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/invoicing"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/invoice"
)

func PlaceUnpaidOrder(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	return PlaceUnpaidOrderHolding(t, pool, false)
}

// PlaceUnpaidOrderHolding is PlaceUnpaidOrder that, when holding, also carries a
// free unit of the variant with the most stock and holds it for 30 minutes, in
// the transaction placing the order as checkout does: no line or hold is taken
// against an order a later transaction did not place.
func PlaceUnpaidOrderHolding(t *testing.T, pool *pgxpool.Pool, holding bool) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'ADMIN-TEST', '測試商品', 500000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'x@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if holding {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
			SELECT $1, pv.id, pv.sku, p.name, 0, 1, 1
			FROM (SELECT id, sku, product_id FROM product_variants
			      ORDER BY stock_quantity DESC, id LIMIT 1) pv
			JOIN products p ON p.id = pv.product_id`, orderID); err != nil {
			t.Fatalf("put the held variant on the order: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			SELECT hold_inventory(ol.order_id, ol.variant_id,
				1, interval '30 minutes', 'deadline-actor:' || $2::text)
			FROM order_lines ol WHERE ol.order_id = $1 AND ol.variant_id IS NOT NULL`,
			orderID, number); err != nil {
			t.Fatalf("hold stock: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func PendingOrderHoldingStock(t *testing.T, pool *pgxpool.Pool) (number string, orderID, variantID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active' AND pv.stock_quantity - pv.safety_stock > 2
		LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("find variant: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, variantID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 's@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, variantID, "ship-test:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID, variantID
}

// ConstraintName is the constraint a PostgreSQL error names, or "" for any
// other error.
func ConstraintName(err error) string {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return ""
	}
	return pgErr.ConstraintName
}

// AsAdmin runs one statement with the back office's own database role: the suite
// otherwise connects as the owner, who is subject to no REVOKE at all.
func AsAdmin(ctx context.Context, t *testing.T, pool *pgxpool.Pool, stmt string) error {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set role admin: %v", err)
	}
	// RESET before release, or the pooled connection hands the admin role to whatever runs next.
	defer func() {
		if _, resetErr := conn.Exec(ctx, `RESET ROLE`); resetErr != nil {
			t.Errorf("reset role: %v", resetErr)
		}
	}()

	_, execErr := conn.Exec(ctx, stmt)
	return execErr
}

func PlaceHeldOrder(t *testing.T, pool *pgxpool.Pool, vid uuid.UUID) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 500000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, vid); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'held@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, vid, "admin-cancel:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TwoLineOrderWithStock(t *testing.T, pool *pgxpool.Pool, name string) (
	number string, orderID uuid.UUID, lineIDs, variantIDs []uuid.UUID,
) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}

	for i := range 2 {
		slug := fmt.Sprintf("%s-%d-%s", name, i, uuid.NewString()[:8])
		var productID, variantID, lineID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
			SELECT b.id, c.id, $1, '分批出貨測試', 'active', now()
			FROM brands b, categories c WHERE b.slug = 'pixelight' AND c.slug = 'phones'
			RETURNING id`, slug).Scan(&productID); err != nil {
			t.Fatalf("create product: %v", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO product_variants (product_id, sku, price_cents, safety_stock, position)
			VALUES ($1, upper($2), 100000, 0, 1) RETURNING id`,
			productID, slug).Scan(&variantID); err != nil {
			t.Fatalf("create variant: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`SELECT record_inventory_movement($1, 10, 'receipt', $2, 'admin', NULL, NULL)`,
			variantID, "seed:"+slug); err != nil {
			t.Fatalf("stock the variant: %v", err)
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, upper($3), '分批出貨測試', 100000, 3, $4) RETURNING id`,
			orderID, variantID, slug, i).Scan(&lineID); err != nil {
			t.Fatalf("create line: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`SELECT hold_inventory($1, $2, 3, interval '30 minutes', $3)`,
			orderID, variantID, "hold:"+slug); err != nil {
			t.Fatalf("hold: %v", err)
		}
		lineIDs = append(lineIDs, lineID)
		variantIDs = append(variantIDs, variantID)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'p@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 600000)`,
		orderID, "cs_part_"+number); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 600000, NULL, NULL)`,
		"cs_part_"+number); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move to picking: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, orderID, lineIDs, variantIDs
}

// DisabledInvoiceWriter is an invoice writer with no provider behind it.
type DisabledInvoiceWriter struct{}

func (DisabledInvoiceWriter) Issue(context.Context, string) (invoice.Document, error) {
	return invoice.Document{}, invoice.ErrDisabled
}

func (DisabledInvoiceWriter) Void(context.Context, string, string) error {
	return invoice.ErrDisabled
}

func (DisabledInvoiceWriter) FileAllowance(
	context.Context, string, uuid.UUID,
) (invoice.Document, error) {
	return invoice.Document{}, invoice.ErrDisabled
}

// OrderStore builds the order desk's store and the refunds, invoicing and stock
// stores it reads through, all over p, as cmd/goen does.
func OrderStore(p *pgxpool.Pool, refunder refunds.Refunder, reader invoicing.Reader, writer invoicing.Writer) *orders.Store {
	return orders.NewStore(p, refunds.NewStore(p, refunder), invoicing.NewStore(p, reader, writer), stock.NewStore(p))
}

func OrderDesk(s *orders.Store) *orders.Handler {
	return orders.NewHandler(s, nil, slog.New(slog.DiscardHandler))
}
