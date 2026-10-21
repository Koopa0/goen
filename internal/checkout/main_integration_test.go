//go:build integration

package checkout_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shipping/storemap"
	"github.com/koopa0/goen/internal/web/ratelimit"
)

var pool *pgxpool.Pool

// checkoutAttemptKey turns a readable test label into the same canonical
// fixed-size identity the checkout page issues. It is idempotent so concurrency
// fixtures may share one identity between a manual advisory lock and placeOrder.
func checkoutAttemptKey(label string) string {
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

func quoteTotal(t *testing.T, quote checkout.ShippingQuote) int64 {
	t.Helper()
	total, err := quote.Total()
	if err != nil {
		t.Fatalf("total shipping quote: %v", err)
	}
	return total
}

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

// variantOf is the first sellable variant of a seeded product.
func variantOf(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(t.Context(), `
		SELECT pv.id FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.slug = $1 AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock
		ORDER BY pv.position LIMIT 1`, slug).Scan(&id)
	if err != nil {
		t.Fatalf("no sellable variant for %q: %v", slug, err)
	}
	return id
}

func newCart(t *testing.T, s *checkout.Store) uuid.UUID {
	t.Helper()
	id, _ := newCartSession(t, s)
	return id
}

func newCartSession(t *testing.T, s *checkout.Store) (id uuid.UUID, token string) {
	t.Helper()
	tok := bearer.New()
	id, err := s.Carts().Create(t.Context(), tok, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	return id, tok
}

// checkoutQuote builds the quote a direct Store test would have rendered. HTTP
// tests carry these values through the real hidden controls instead. Keeping
// this helper in cart_test avoids a production escape hatch that could place an
// unconfirmed current quote.
func checkoutQuote(
	t *testing.T,
	s *checkout.Store,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	shippingID uuid.UUID,
	addr *delivery.Details,
	couponCode string,
) checkout.QuoteID {
	t.Helper()
	facts := checkoutQuoteFacts(t, s, cartID, owner, shippingID, addr, couponCode)
	id, err := facts.ID()
	if err != nil {
		t.Fatalf("build checkout quote: %v", err)
	}
	return id
}

// checkoutQuoteFacts is the quote checkoutQuote hashes, for a test that has to
// change one fact of what the checkout rendered.
func checkoutQuoteFacts(
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
	shippingFee, discount := quoteTotal(t, quote), int64(0)
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

func placeOrder(
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
	quote := checkoutQuote(t, s, cartID, owner, shippingID, addr, couponCode)
	return s.PlaceOrder(
		ctx, cartID, owner, shippingID, addr, nil, couponCode, quote,
		checkoutAttemptKey(key),
	)
}

// newHandler is a checkout over s and the cart s reads, behind one access
// store and one store map, as cmd/goen builds the two; it takes no payment.
func newHandler(
	s *checkout.Store, grants *access.Store, log *slog.Logger,
	storeMap *storemap.Client, checkers ...checkout.MobileBarcodeChecker,
) *checkout.Handler {
	return checkout.NewHandler(s, cart.NewHandler(s.Carts(), grants, log, false, storeMap),
		grants, log, false, nil, storeMap, checkers...)
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

// shipVersionFor is the current version of the named shipping method.
func shipVersionFor(t *testing.T, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT v.id FROM shipping_method_versions v
		 JOIN shipping_methods sm ON sm.id = v.method_id
		 WHERE sm.code = $1 ORDER BY v.effective_at DESC LIMIT 1`, code).Scan(&id); err != nil {
		t.Fatalf("shipping version for %s: %v", code, err)
	}
	return id
}

// destinationOf reads back what an order recorded as its destination.
func destinationOf(t *testing.T, number string) (street, brand, code, name string) {
	t.Helper()
	var s, b, c, n *string
	if err := pool.QueryRow(t.Context(),
		`SELECT pd.street, pd.pickup_chain, pd.pickup_store_code, pd.pickup_store_name
		 FROM order_private_data pd JOIN orders o ON o.id = pd.order_id
		 WHERE o.order_number = $1`, number).Scan(&s, &b, &c, &n); err != nil {
		t.Fatalf("read destination of %s: %v", number, err)
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return deref(s), deref(b), deref(c), deref(n)
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

// creditedAccount is creditedCustomer plus the account id.
func creditedAccount(t *testing.T, cents int64) (userID, accountID uuid.UUID) {
	t.Helper()
	userID = creditedCustomer(t, cents)
	if err := pool.QueryRow(t.Context(),
		`SELECT id FROM store_credit_accounts WHERE user_id = $1`, userID).
		Scan(&accountID); err != nil {
		t.Fatalf("read credit account: %v", err)
	}
	return userID, accountID
}

// creditBalance is what the ledger sums to for one account.
func creditBalance(t *testing.T, accountID uuid.UUID) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE account_id = $1`,
		accountID).Scan(&cents); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return cents
}

// translatedVariant makes a product whose Chinese and English names differ, and
// returns the variant id and both names.
func translatedVariant(t *testing.T) (variantID uuid.UUID, zhName, enName string) {
	t.Helper()
	ctx := t.Context()

	suffix := uuid.NewString()[:8]
	zhName, enName = "快照測試 "+suffix, "Snapshot Test "+suffix
	var productID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, name_en, status,
		                      published_at)
		SELECT b.id, c.id, $1, $2, $3, 'draft', now()
		FROM brands b, categories c
		WHERE b.slug = 'koto' AND c.parent_id IS NULL
		ORDER BY c.position LIMIT 1
		RETURNING id`, "snapshot-"+suffix, zhName, enName).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, safety_stock)
		VALUES ($1, $2, 100000, 0) RETURNING id`,
		productID, "SNAP-"+strings.ToUpper(suffix)).Scan(&variantID); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	// Stock arrives through the ledger, the only door that writes stock_quantity.
	if _, err := pool.Exec(ctx,
		`SELECT record_inventory_movement($1, 5, 'receipt', $2, 'admin', NULL)`,
		variantID, "snapshot-stock-"+suffix); err != nil {
		t.Fatalf("stock the variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active' WHERE id = $1`, productID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return variantID, zhName, enName
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

func testPayHandler(t *testing.T) *payment.Handler {
	t.Helper()
	gateway, err := payment.NewGateway("", "", "")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	return payment.NewHandler(payment.NewStore(pool), gateway, access.NewStore(pool, false), slog.New(slog.DiscardHandler))
}

func followPayWithCookie(
	t *testing.T, ctx context.Context, payH *payment.Handler, number string, cookie *http.Cookie,
) {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/pay", http.NoBody)
	req.SetPathValue("number", number)
	req.AddCookie(cookie)
	res := httptest.NewRecorder()
	payH.Page(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("issued cookie could not reach pay page for %s: %d", number, res.Code)
	}
}

// placeUnpaidOrderFor places one order for an address, through the store's own
// checkout.
func placeUnpaidOrderFor(t *testing.T, s *checkout.Store, address string) string {
	t.Helper()
	return placeOrderOfUnits(t, s, address, 1)
}

// placeOrderOfUnits is placeUnpaidOrderFor with that many units of its one line.
func placeOrderOfUnits(t *testing.T, s *checkout.Store, address string, units int32) string {
	t.Helper()
	ctx := t.Context()

	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, freshVariant(t, "unpaid-order"), units); err != nil {
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
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "",
		"forge-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	return number
}
