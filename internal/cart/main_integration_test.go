//go:build integration

package cart_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/bearer"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/ratelimit"
)

var pool *pgxpool.Pool

func hiddenInputValue(body, name string) (string, bool) {
	marker := `name="` + name + `" value="`
	start := strings.Index(body, marker)
	if start < 0 {
		return "", false
	}
	value := body[start+len(marker):]
	end := strings.IndexByte(value, '"')
	if end < 0 {
		return "", false
	}
	return value[:end], true
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

func variantOf(t *testing.T, slug string, sellable bool) uuid.UUID {
	t.Helper()
	cmp := ">"
	if !sellable {
		cmp = "<="
	}
	var id uuid.UUID
	err := pool.QueryRow(t.Context(), `
		SELECT pv.id FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.slug = $1 AND pv.is_active
		  AND pv.stock_quantity `+cmp+` pv.safety_stock
		ORDER BY pv.position LIMIT 1`, slug).Scan(&id)
	if err != nil {
		t.Fatalf("no %v variant for %q: %v", sellable, slug, err)
	}
	return id
}

func newCart(t *testing.T, s *cart.Store) uuid.UUID {
	t.Helper()
	id, _ := newCartSession(t, s)
	return id
}

func newCartSession(t *testing.T, s *cart.Store) (id uuid.UUID, token string) {
	t.Helper()
	tok := bearer.New()
	id, err := s.Create(t.Context(), tok, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	return id, tok
}

func applicationPool(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse application pool config: %v", err)
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open application pool: %v", err)
	}
	var actual string
	if err := p.QueryRow(t.Context(), `SHOW application_name`).Scan(&actual); err != nil {
		p.Close()
		t.Fatalf("read application name: %v", err)
	}
	if actual != name {
		p.Close()
		t.Fatalf("application name = %q, want %q", actual, name)
	}
	t.Cleanup(p.Close)
	return p
}

func storeRolePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse store-role pool config: %v", err)
	}
	cfg.MaxConns = 2
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, `SET ROLE store`)
		return execErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open store-role pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func waitForApplicationLock(t *testing.T, name string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("%s returned before reaching the intended database lock: %v", name, err)
		default:
		}
		_, waiting, err := dbtest.Blocked(t.Context(), pool, dbtest.Match{Application: name})
		if err == nil && waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never blocked on the intended database lock: %v", name, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// heldOrder writes an order holding one unit of vid whose hold expired `ago` in
// the past. paid decides whether it is funded.
func heldOrder(t *testing.T, vid uuid.UUID, ago time.Duration, paid bool) (orderID uuid.UUID) {
	t.Helper()
	return heldOrderFor(t, uuid.NullUUID{}, vid, ago, paid)
}

// heldOrderFor is heldOrder placed by owner, or by a guest when owner is not
// valid.
func heldOrderFor(t *testing.T, owner uuid.NullUUID, vid uuid.UUID, ago time.Duration, paid bool) (orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, owner).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, vid); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'w@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	// Held normally, then aged — BOTH timestamps move, because expires_at >
	// created_at is a CHECK and a hold that expired an hour ago was taken before it.
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, vid, "sweep:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - $2::interval - interval '30 minutes',
		    expires_at = now() - $2::interval
		WHERE order_id = $1`, orderID, ago.String()); err != nil {
		t.Fatalf("age the hold: %v", err)
	}
	if paid {
		if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`,
			orderID, "cs_sweep_"+number); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`,
			"cs_sweep_"+number); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID
}

// creditFundedHeldOrder writes an order holding one unit of vid, paid entirely
// from store credit, whose hold expired `ago` in the past.
func creditFundedHeldOrder(t *testing.T, vid uuid.UUID, ago time.Duration) (orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	// The line price IS the order total, which is what makes order_amount_after_credit come
	// to exactly zero once the credit is spent.
	const cents = 100000
	userID := creditedCustomer(t, cents)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

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
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, $3, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`, orderID, vid, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'credit-sweep@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, interval '30 minutes', $3)`,
		orderID, vid, "sweep-credit:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - $2::interval - interval '30 minutes',
		    expires_at = now() - $2::interval
		WHERE order_id = $1`, orderID, ago.String()); err != nil {
		t.Fatalf("age the hold: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT post_store_credit($1, $2, NULL, $3, $4, NULL)`,
		userID, -int64(cents), orderID, "order:"+orderID.String()); err != nil {
		t.Fatalf("spend credit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fixture: %v", err)
	}
	return orderID
}

// creditedCustomer makes a user with a store-credit balance and returns their id.
func creditedCustomer(t *testing.T, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('credit-'||gen_random_uuid()||'@example.com', 'customer', '額度測試')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if cents > 0 {
		if _, err := pool.Exec(ctx,
			`SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
			id, cents, "grant:"+id.String()); err != nil {
			t.Fatalf("grant credit: %v", err)
		}
	}
	return id
}

// numberOf is an order's customer-facing number.
func numberOf(t *testing.T, orderID uuid.UUID) string {
	t.Helper()
	var number string
	if err := pool.QueryRow(t.Context(),
		`SELECT order_number FROM orders WHERE id = $1`, orderID).Scan(&number); err != nil {
		t.Fatalf("read order number: %v", err)
	}
	return number
}

// stockOf is a variant's shelf quantity.
func stockOf(t *testing.T, vid uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

// orderOfVariants places a committed order holding the given variants.
func orderOfVariants(t *testing.T, want map[uuid.UUID]int32) string {
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
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	position := 0
	for vid, qty := range want {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name,
			                         unit_price_cents, quantity, position)
			SELECT $1, pv.id, pv.sku, p.name, pv.price_cents, $3, $4
			FROM product_variants pv JOIN products p ON p.id = pv.product_id
			WHERE pv.id = $2`, orderID, vid, qty, position); err != nil {
			t.Fatalf("create line: %v", err)
		}
		position++
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'reorder@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

// emptyTheShelfFor takes a variant down to nothing sellable.
func emptyTheShelfFor(t *testing.T, vid uuid.UUID) {
	t.Helper()
	var stock int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&stock); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if stock > 0 {
		if _, err := pool.Exec(t.Context(),
			`SELECT record_inventory_movement($1, $2, 'adjustment', $3, 'admin', NULL, NULL)`,
			vid, -stock, "reorder-empty:"+vid.String()); err != nil {
			t.Fatalf("empty the shelf: %v", err)
		}
	}
}

// freshVariant creates a product of this test's own with one sellable variant.
// Tests that hold, retire or empty stock must not share one: test-integration
// shuffles, so a shared variant fails whenever the order changes.
func freshVariant(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	a, _, _ := variantsOf(t, slug, 1)
	return a
}

// threeVariants is freshVariant with three.
func threeVariants(t *testing.T, slug string) (a, b, c uuid.UUID) {
	t.Helper()
	return variantsOf(t, slug, 3)
}

// variantsOf creates one product with n sellable variants.
func variantsOf(t *testing.T, name string, n int) (a, b, c uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	// A unique slug per CALL, not per test: products_slug_key would refuse the
	// second product of a test that needs two.
	slug := name + "-" + uuid.NewString()[:8]

	var productID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, $1, 'draft', NULL
		FROM brands b, categories c
		ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create product %s: %v", slug, err)
	}

	ids := make([]uuid.UUID, 3)
	for i := range n {
		var vid uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
			VALUES ($1, $2, 199900, true, $3) RETURNING id`,
			// product_variants_sku_format wants upper case and hyphens.
			productID, strings.ToUpper(slug)+"-"+strconv.Itoa(i), i).Scan(&vid); err != nil {
			t.Fatalf("create variant: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`SELECT record_inventory_movement($1, 10, 'adjustment', $2, 'admin', NULL, NULL)`,
			vid, "fixture:"+vid.String()); err != nil {
			t.Fatalf("stock the variant: %v", err)
		}
		ids[i] = vid
	}
	// Published only now: products_active_has_variant is DEFERRED and fires from
	// both sides.
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active', published_at = now() WHERE id = $1`,
		productID); err != nil {
		t.Fatalf("publish %s: %v", slug, err)
	}
	return ids[0], ids[1], ids[2]
}

// placedCookie issues a REAL access token through the same grant the checkout
// writes: a fixture putting the order NUMBER in the cookie would assert a
// forgeable design rather than the rule.
func placedCookie(t *testing.T, number string) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	if err := access.NewStore(pool, false).Grant(w, r, number); err != nil {
		t.Fatalf("grant access to %s: %v", number, err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "goen_placed" {
			return c
		}
	}
	t.Fatalf("Grant set no goen_placed cookie")
	return nil
}

// testLimiter is generous on purpose: these cases are about an ANSWER, and a
// limiter that refused mid-suite would be testing the limiter.
func testLimiter() *ratelimit.Limiter {
	return ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000})
}
