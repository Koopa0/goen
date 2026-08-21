//go:build integration

// Store-level tests, against a real PostgreSQL and an httptest server speaking
// ECPay's protocol.
//
// WHITE-BOX, unlike every other integration_test.go here, and for one reason:
// the fake provider has to SEAL its replies with the gateway's own AES envelope,
// and seal is unexported by design. Re-implementing the cipher in a test would
// be a second copy of the thing most worth having exactly one of.
//
// The layer had no test at all before this: a review found that
// invoice.Store.Issue — the function assembling what is filed with the 財政部 —
// was executed by nothing, so the whole itemisation could be deleted green.
package invoice

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		slog.Error("read seed", "error", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(context.Background(), string(seed)); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}
	code := m.Run()
	stop()
	os.Exit(code)
}

// TestAnAllowanceIsFiledOncePerPress holds an idempotency key that ECPay does
// not provide.
//
// Issue carries RelateNumber and a repeat is refused with 5070357. The
// allowance endpoint carries NOTHING of the kind, and goen filed at the provider
// before recording anything — correct for an invoice, whose repeat the provider
// refuses, and wrong here: an operator who pressed twice, or whose first press
// timed out after ECPay had filed, put two 折讓 in front of the 財政部 for one
// refund. A 統一發票 cannot be edited, so the correction is a void and a
// reissue of something that should never have existed.
//
// The claim is written BEFORE the provider is asked now, and the unique index on
// request_key is what refuses the second press — here, where the damage is a
// message, rather than there.
func TestAnAllowanceIsFiledOncePerPress(t *testing.T) {
	ctx := t.Context()

	var filings int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filings++
		reply(t, w, result{RtnCode: 1, AllowanceNo: "2026080715227214"})
	}))
	defer srv.Close()

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	s := NewStore(pool, g)

	// Refunded MORE than one allowance relieves, deliberately: with the refund
	// and the allowance equal, the second press is refused by the refunded-total
	// bound and the key is never asked — the test then passes with no
	// idempotency at all, which is what the first version of it did.
	number := invoicedOrderWithRefund(t, 100000)

	if _, err := s.Allowance(ctx, number, 50000); err != nil {
		t.Fatalf("the first allowance was refused: %v", err)
	}
	if filings != 1 {
		t.Fatalf("the provider was called %d times for one press", filings)
	}

	// The same press again — a double-click, or a retry after a timeout. The
	// bound would still admit it: 50000 + 50000 is exactly what went back.
	if _, err := s.Allowance(ctx, number, 50000); err == nil {
		t.Error("a second identical allowance was accepted; two 折讓 now stand " +
			"against one refund at the 財政部")
	}
	if filings != 1 {
		t.Errorf("the provider was called %d times; the second press reached ECPay "+
			"before anything refused it, which is where the damage is", filings)
	}
}

// TestAnAllowanceCannotRelieveMoreThanWentBack holds a bound that
// invoice_allowance_valid cannot see.
//
// That trigger holds the allowance total against the INVOICE, which says
// nothing about refunds — so a full invoice could be fully relieved while the
// customer had been refunded nothing, understating what the shop owes the
// 財政部. The bound is what actually went back, across BOTH sources, because a
// refund can be paid to the card, to store credit, or split across the two.
func TestAnAllowanceCannotRelieveMoreThanWentBack(t *testing.T) {
	ctx := t.Context()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply(t, w, result{RtnCode: 1, AllowanceNo: "2026080715227215"})
	}))
	defer srv.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	s := NewStore(pool, g)

	number := invoicedOrderWithRefund(t, 30000)

	if _, err := s.Allowance(ctx, number, 40000); err == nil {
		t.Error("an allowance relieved 40000 when 30000 had gone back; the shop has " +
			"told the 財政部 that more of the sale did not happen than it refunded")
	}
	if _, err := s.Allowance(ctx, number, 30000); err != nil {
		t.Errorf("an allowance for exactly what was refunded was refused: %v", err)
	}
}

// invoicedOrderWithRefund builds a committed order that already carries a live
// invoice and a settled card refund of refundCents, which is the state a 折讓
// is filed from.
//
// Sequential statements rather than one CTE chain: sibling CTEs share a
// snapshot and cannot see each other's writes, so payments_require_complete_order
// refused a payment inserted alongside the lines it needs.
func invoicedOrderWithRefund(t *testing.T, refundCents int64) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var orderID, number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, locale, fulfillment_status)
		SELECT 'GO-991231-' || lpad((floor(random()*900000)+100000)::bigint::text, 6, '0'),
		       smv.id, sm.code, smv.name, 0, 'zh-Hant', 'pending'
		FROM shipping_method_versions smv
		JOIN shipping_methods sm ON sm.id = smv.method_id
		LIMIT 1
		RETURNING id::text, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create the order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name,
		                         unit_price_cents, quantity, position)
		SELECT $1, pv.id, 'ALLOW-SKU-1', '折讓測試商品', 100000, 1, 0
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' LIMIT 1`, orderID); err != nil {
		t.Fatalf("add a line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'allow@goen.invalid', '王小明', '0912345678',
		        '110', '台北市', '信義區', '松高路 1 號')`, orderID); err != nil {
		t.Fatalf("add delivery details: %v", err)
	}

	var paymentID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO payments (order_id, provider, provider_ref, intended_amount_cents,
		                      captured_amount_cents, status, paid_at)
		VALUES ($1, 'stripe', 'cs_allow_' || $2, 100000, 100000, 'succeeded', now())
		RETURNING id::text`, orderID, number).Scan(&paymentID); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
		VALUES ($1, 'invoice',
		        'GD-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 8), 100000)`,
		orderID); err != nil {
		t.Fatalf("file the invoice: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
		                     status, provider_ref, succeeded_at)
		VALUES ($1, 'allow-fixture:' || $2, $3, '退貨', 'succeeded',
		        're_allow_' || $2, now())`, paymentID, number, refundCents); err != nil {
		t.Fatalf("settle a refund: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fixture: %v", err)
	}
	return number
}
