//go:build integration

package checkout_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shipping/storemap"
	"github.com/koopa0/goen/internal/web/ratelimit"
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
