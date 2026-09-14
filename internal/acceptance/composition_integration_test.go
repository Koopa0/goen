//go:build integration

package acceptance_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/outbox"
)

var pool *pgxpool.Pool

type stubRefunder struct{}

func (stubRefunder) PaymentIntentFor(context.Context, string) (string, error) {
	return "", admin.ErrNoRefunder
}

func (stubRefunder) Refund(context.Context, string, string, int64) (string, admin.RefundState, error) {
	return "", admin.RefundFailed, admin.ErrNoRefunder
}

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

func checkoutAttemptKey(label string) string {
	digest := sha256.Sum256([]byte(label))
	return base64.RawURLEncoding.EncodeToString(digest[:16])
}

func freshVariant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	slug := name + "-" + uuid.NewString()[:8]
	var productID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, $1, 'draft', NULL
		FROM brands b, categories c
		ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	var vid uuid.UUID
	sku := strings.ToUpper(slug) + "-0"
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
		VALUES ($1, $2, 199900, true, 0) RETURNING id`,
		productID, sku).Scan(&vid); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_inventory_movement($1, 3, 'adjustment', $2, NULL, NULL, NULL)`,
		vid, "fixture:"+vid.String()); err != nil {
		t.Fatalf("stock variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET safety_stock = 2 WHERE id = $1`, vid); err != nil {
		t.Fatalf("set safety stock: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active', published_at = now() WHERE id = $1`,
		productID); err != nil {
		t.Fatalf("publish product: %v", err)
	}
	return vid
}

func shipVersionFor(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT v.id FROM shipping_method_versions v
		 JOIN shipping_methods sm ON sm.id = v.method_id
		 WHERE sm.code = 'home_delivery' ORDER BY v.effective_at DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("shipping version: %v", err)
	}
	return id
}

func newCart(t *testing.T, s *cart.Store) uuid.UUID {
	t.Helper()
	tok, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	id, err := s.Create(t.Context(), tok, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	return id
}

// TestTwoBuyersContendForTheLastSellableUnit is the application composition for
// C05: two independent checkouts race the last sellable unit and exactly one may
// succeed while the other receives a buyer-visible shortage.
func TestTwoBuyersContendForTheLastSellableUnit(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "acceptance-last-unit")
	shippingID := shipVersionFor(t)
	addr := &cart.Address{
		Email: "buyer@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 号",
	}

	attempt := func(label string) error {
		s := cart.NewStore(pool)
		cartID := newCart(t, s)
		if err := s.Add(ctx, cartID, vid, 1); err != nil {
			return err
		}
		view, err := s.View(ctx, cartID)
		if err != nil {
			return err
		}
		delivery, err := s.QuoteShipping(ctx, shippingID, view.SubtotalCents, addr.PostalCode)
		if err != nil {
			return err
		}
		shipping, err := delivery.Total()
		if err != nil {
			return err
		}
		lines := make([]cart.CheckoutQuoteLine, 0, len(view.Lines))
		for i := range view.Lines {
			line := &view.Lines[i]
			variantID, parseErr := uuid.Parse(line.VariantID)
			if parseErr != nil {
				return parseErr
			}
			lines = append(lines, cart.CheckoutQuoteLine{
				VariantID: variantID,
				Quantity:  line.Quantity,
				UnitCents: line.UnitCents,
			})
		}
		quote, err := (cart.CheckoutQuote{
			CartID:            cartID,
			Lines:             lines,
			ShippingVersionID: shippingID,
			ShippingCents:     shipping,
		}).ID()
		if err != nil {
			return err
		}
		_, err = s.PlaceOrder(
			ctx, cartID, uuid.NullUUID{}, shippingID, addr, nil, "", quote,
			checkoutAttemptKey(label),
		)
		return err
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			results <- attempt(fmt.Sprintf("buyer-%d-%s", n, uuid.NewString()))
		}(i)
	}
	wg.Wait()
	close(results)

	var successes, shortages int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, cart.ErrUnavailable):
			shortages++
		default:
			t.Fatalf("unexpected checkout error: %v", err)
		}
	}
	if successes != 1 || shortages != 1 {
		t.Fatalf("outcomes successes=%d shortages=%d, want one of each", successes, shortages)
	}

	var holds int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM inventory_reservations WHERE variant_id = $1 AND state = 'held'`,
		vid).Scan(&holds); err != nil {
		t.Fatalf("count holds: %v", err)
	}
	if holds != 1 {
		t.Fatalf("live holds = %d, want one committed reservation", holds)
	}
}

// TestReconciliationSurfacesRequiresReconciliationPayments is the C14
// composition hook: a payment left in requires_reconciliation must alarm health.
func TestReconciliationSurfacesRequiresReconciliationPayments(t *testing.T) {
	ctx := t.Context()
	tx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id`).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'RECON-SKU', '測試', 500000, 1)`, orderID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'recon@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit order: %v", err)
	}
	providerRef := "pi_acceptance_" + uuid.NewString()[:8]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
		VALUES ($1, $2, 'requires_reconciliation', 500000)`, orderID, providerRef); err != nil {
		t.Fatalf("insert payment: %v", err)
	}

	health, err := admin.NewStore(pool, stubRefunder{}, nil, nil).
		WorkerHealth(ctx, outbox.NewStore(pool, slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatalf("worker health: %v", err)
	}
	if health.PaymentsReconciled() {
		t.Fatal("requires_reconciliation payment reads healthy")
	}
	found := false
	for _, issue := range health.UnreconciledCompletePayments {
		if issue.ProviderRef == providerRef {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("operator health did not name provider reference %q", providerRef)
	}
}
