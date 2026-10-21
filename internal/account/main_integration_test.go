//go:build integration

package account_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/user"
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

// register makes an account the way the registration form does, up to the
// mailed link: the account exists and holds the password, and its address is
// unproved. The queued follow-up is marked delivered, because a fixture has no
// worker to send it. The password is hashed at the cost production spends:
// only auth's own test binary may lower it, and these pages never read it.
func register(t *testing.T, email string) user.User {
	t.Helper()
	if err := auth.NewStore(pool).Register(t.Context(), &auth.Credentials{
		Email: email, Password: "a sufficiently long password", Name: "測試",
	}, "/account"); err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	var id uuid.UUID
	var u user.User
	if err := pool.QueryRow(t.Context(), `
		SELECT id, email, coalesce(full_name, ''), role
		FROM users WHERE lower(email) = lower($1)`, email).
		Scan(&id, &u.Email, &u.Name, &u.Role); err != nil {
		t.Fatalf("read the account registered for %s: %v", email, err)
	}
	u.ID = id.String()
	if _, err := pool.Exec(t.Context(), `
		UPDATE outbox_messages SET delivered_at = now()
		WHERE topic = $1 AND payload->>'user_id' = $2 AND delivered_at IS NULL`,
		outbox.TopicRegistration.Name(), u.ID); err != nil {
		t.Fatalf("settle the registration follow-up for %s: %v", email, err)
	}
	return u
}

// registerProved is a completed registration: register, then what following
// the mailed link does to the account. A password signs into nothing less.
func registerProved(t *testing.T, email string) user.User {
	t.Helper()
	u := register(t, email)
	if _, err := pool.Exec(t.Context(),
		`UPDATE users SET email_verified_at = now() WHERE id = $1`, uuid.MustParse(u.ID)); err != nil {
		t.Fatalf("prove the address of %s: %v", email, err)
	}
	return u
}

func accountStorePool(t *testing.T, applicationName string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse account application pool config: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, setRoleErr := conn.Exec(ctx, `SET ROLE store`)
		return setRoleErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open account application pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// placeOrderFor writes a minimal order owned by userID and returns its number.
func placeOrderFor(t *testing.T, userID string) string {
	t.Helper()
	ctx := t.Context()

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	var variantID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active LIMIT 1`).Scan(&variantID); err != nil {
		t.Fatalf("variant: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name)
		SELECT next_order_number(), $1, $2, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		WHERE v.id = $2
		RETURNING id, order_number`, userID, shipID).Scan(&orderID, &number); err != nil {
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
		VALUES ($1, 'x@example.com', '收件人', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "goen_session" || strings.HasSuffix(c.Name, "goen_session") {
			return c
		}
	}
	t.Fatal("session cookie was not set")
	return nil
}

func addAddress(t *testing.T, s *account.Store, userID, label string, isDefault bool) string {
	t.Helper()
	if err := s.AddAddress(t.Context(), userID, &account.Address{
		Label: label, Name: "收件人", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: label + "路 1 號",
		Default: isDefault,
	}); err != nil {
		t.Fatalf("add address %s: %v", label, err)
	}
	var id string
	if err := pool.QueryRow(t.Context(),
		`SELECT id::text FROM addresses WHERE user_id = $1 AND label = $2`,
		userID, label).Scan(&id); err != nil {
		t.Fatalf("read address %s: %v", label, err)
	}
	return id
}

func defaultAddressID(t *testing.T, userID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce((SELECT id::text FROM addresses
		                  WHERE user_id = $1 AND is_default), '')`, userID).Scan(&id); err != nil {
		t.Fatalf("read default address: %v", err)
	}
	return id
}

func countDefaults(t *testing.T, userID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM addresses WHERE user_id = $1 AND is_default`, userID).Scan(&n); err != nil {
		t.Fatalf("count defaults: %v", err)
	}
	return n
}

// postForm is a browser's form POST to target.
func postForm(ctx context.Context, target string, form url.Values) *http.Request {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}
