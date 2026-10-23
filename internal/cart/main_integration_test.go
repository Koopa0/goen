//go:build integration

package cart_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
