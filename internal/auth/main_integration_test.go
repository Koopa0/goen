//go:build integration

package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/user"
)

var pool *pgxpool.Pool

func checkoutAttemptKey(label string) string {
	digest := sha256.Sum256([]byte(label))
	return base64.RawURLEncoding.EncodeToString(digest[:16])
}

func TestMain(m *testing.M) {
	auth.LowerPasswordCostForTests()

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
// worker to send it.
func register(t *testing.T, s *auth.Store, email string) user.User {
	t.Helper()
	if err := s.Register(t.Context(), &auth.Credentials{
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
func registerProved(t *testing.T, s *auth.Store, email string) user.User {
	t.Helper()
	u := register(t, s, email)
	if _, err := pool.Exec(t.Context(),
		`UPDATE users SET email_verified_at = now() WHERE id = $1`, uuid.MustParse(u.ID)); err != nil {
		t.Fatalf("prove the address of %s: %v", email, err)
	}
	return u
}

func beginReset(t *testing.T, s *auth.Store, email string) string {
	t.Helper()
	if err := auth.BeginReset(t.Context(), s, email); err != nil {
		t.Fatalf("begin reset for %s: %v", email, err)
	}
	var token string
	if err := pool.QueryRow(t.Context(), `
		SELECT payload->>'token'
		FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)
		ORDER BY id DESC
		LIMIT 1`, outbox.TopicPasswordReset.Name(), email).Scan(&token); err != nil {
		t.Fatalf("read reset token from its queued message: %v", err)
	}
	if token == "" {
		t.Fatal("queued reset message has no token")
	}
	return token
}

func requestVerification(t *testing.T, s *auth.Store, userID, email string) string {
	t.Helper()
	if err := auth.RequestVerification(t.Context(), s, userID, email); err != nil {
		t.Fatalf("request verification for %s: %v", email, err)
	}
	var token string
	if err := pool.QueryRow(t.Context(), `
		SELECT payload->>'token'
		FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)
		ORDER BY id DESC
		LIMIT 1`, outbox.TopicEmailVerify.Name(), email).Scan(&token); err != nil {
		t.Fatalf("read verification token from its queued message: %v", err)
	}
	if token == "" {
		t.Fatal("queued verification message has no token")
	}
	return token
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

func waitForAccountLock(t *testing.T, applicationName string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("%s returned before reaching the intended database lock: %v",
				applicationName, err)
		default:
		}

		_, waiting, err := dbtest.Blocked(t.Context(), pool, dbtest.Match{Application: applicationName})
		if err == nil && waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never blocked on the intended database lock: %v", applicationName, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func operationResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("operation did not finish after its database lock was released")
		return nil
	}
}

func accountCheckoutQuote(
	t *testing.T,
	s *checkout.Store,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	shippingID uuid.UUID,
	postalCode string,
) checkout.QuoteID {
	t.Helper()
	view, err := s.Carts().View(t.Context(), cartID)
	if err != nil {
		t.Fatalf("read account checkout cart: %v", err)
	}
	quote, err := s.QuoteShipping(t.Context(), shippingID, view.SubtotalCents, postalCode)
	if err != nil {
		t.Fatalf("quote account checkout delivery: %v", err)
	}
	balance, err := s.AvailableCredit(t.Context(), owner)
	if err != nil {
		t.Fatalf("read account checkout credit: %v", err)
	}
	lines := make([]checkout.QuoteLine, 0, len(view.Lines))
	for i := range view.Lines {
		line := &view.Lines[i]
		variantID, parseErr := uuid.Parse(line.VariantID)
		if parseErr != nil {
			t.Fatalf("parse account checkout variant: %v", parseErr)
		}
		lines = append(lines, checkout.QuoteLine{
			VariantID: variantID, Quantity: line.Quantity, UnitCents: line.UnitCents,
		})
	}
	shipping, err := quote.Total()
	if err != nil {
		t.Fatalf("total account checkout shipping: %v", err)
	}
	id, err := (checkout.Quote{
		CartID: cartID, Lines: lines,
		ShippingVersionID: shippingID, ShippingCents: shipping,
		CreditCents: min(balance, view.SubtotalCents+shipping),
	}).ID()
	if err != nil {
		t.Fatalf("build account checkout quote: %v", err)
	}
	return id
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

func sellableVariant(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active'
		  AND (pv.stock_quantity - pv.safety_stock) > 0
		ORDER BY pv.position, pv.id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("sellable variant: %v", err)
	}
	return id
}

func anotherSellableVariant(t *testing.T, ctx context.Context, not uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active'
		  AND (pv.stock_quantity - pv.safety_stock) > 0
		  AND pv.id <> $1
		ORDER BY pv.position, pv.id LIMIT 1`, not).Scan(&id); err != nil {
		t.Fatalf("second sellable variant: %v", err)
	}
	return id
}

func cartLineQuantity(body string, variant uuid.UUID) string {
	marker := `id="qty-` + variant.String() + `"`
	start := strings.Index(body, marker)
	if start < 0 {
		return ""
	}
	rest := body[start:]
	value := `value="`
	at := strings.Index(rest, value)
	if at < 0 {
		return ""
	}
	rest = rest[at+len(value):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
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

func cartItemQuantity(t *testing.T, cartID, variantID uuid.UUID) int32 {
	t.Helper()
	var qty int32
	if err := pool.QueryRow(t.Context(), `
		SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
		cartID, variantID).Scan(&qty); err != nil {
		t.Fatalf("read cart line quantity: %v", err)
	}
	return qty
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func emailOf(t *testing.T, userID string) string {
	t.Helper()
	var addr string
	if err := pool.QueryRow(t.Context(),
		`SELECT email FROM users WHERE id = $1`, userID).Scan(&addr); err != nil {
		t.Fatalf("read email: %v", err)
	}
	return addr
}

func assertSignInPrefill(t *testing.T, body, address string, erasure bool) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var emailValue string
	var emailFocus, passwordFocus, registerLink bool
	for node := range doc.Descendants() {
		attrs := map[string]string{}
		for _, attr := range node.Attr {
			attrs[attr.Key] = attr.Val
		}
		switch {
		case node.Data == "input" && attrs["id"] == "email":
			emailValue = attrs["value"]
			_, emailFocus = attrs["autofocus"]
		case node.Data == "input" && attrs["id"] == "password":
			_, passwordFocus = attrs["autofocus"]
		case node.Data == "a" && attrs["href"] == "/register":
			registerLink = true
		}
	}
	if emailValue != address || emailFocus || !passwordFocus || registerLink == erasure {
		t.Errorf("returned sign-in form: email=%q email focus=%t password focus=%t register=%t; want email=%q password focus and register=%t",
			emailValue, emailFocus, passwordFocus, registerLink, address, !erasure)
	}
}

func accountRecoveryState(t *testing.T, userID string) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
	    'account', (SELECT to_jsonb(u) FROM users u WHERE id = $1),
	    'verification', (SELECT jsonb_agg(to_jsonb(v) ORDER BY v.digest) FROM email_verifications v WHERE user_id = $1),
	    'reset', (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.digest) FROM password_reset_tokens r WHERE user_id = $1),
	    'mail', (SELECT jsonb_agg(to_jsonb(m) ORDER BY m.id) FROM outbox_messages m WHERE payload->>'user_id' = $1::text OR lower(payload->>'email') = (SELECT lower(email) FROM users WHERE id = $1)))::text`,
		userID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// accountPage is package account's handler on p, the pool the flow under test
// runs on: the account page is where a change here sends the customer back.
func accountPage(p *pgxpool.Pool) *account.Handler {
	return account.NewHandler(account.NewStore(p), slog.New(slog.DiscardHandler), false, false)
}

// refuseOnAccountPage wires h as cmd/goen does: a refused request is answered
// with the account page on p.
func refuseOnAccountPage(h *auth.Handler, p *pgxpool.Pool) *auth.Handler {
	h.RefuseOnAccountPage(accountPage(p).Refuse)
	return h
}
