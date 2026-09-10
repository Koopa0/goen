//go:build integration

package account_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

var pool *pgxpool.Pool

func checkoutAttemptKey(label string) string {
	digest := sha256.Sum256([]byte(label))
	return base64.RawURLEncoding.EncodeToString(digest[:16])
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

func register(t *testing.T, s *account.Store, email string) account.User {
	t.Helper()
	u, err := s.Register(t.Context(), &account.Credentials{
		Email: email, Password: "a sufficiently long password", Name: "測試",
	})
	if err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	return u
}

func beginReset(t *testing.T, s *account.Store, email string) string {
	t.Helper()
	if err := account.BeginReset(t.Context(), s, email); err != nil {
		t.Fatalf("begin reset for %s: %v", email, err)
	}
	var token string
	if err := pool.QueryRow(t.Context(), `
		SELECT payload->>'token'
		FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)
		ORDER BY id DESC
		LIMIT 1`, outbox.TopicPasswordReset, email).Scan(&token); err != nil {
		t.Fatalf("read reset token from its queued message: %v", err)
	}
	if token == "" {
		t.Fatal("queued reset message has no token")
	}
	return token
}

func requestVerification(t *testing.T, s *account.Store, userID, email string) string {
	t.Helper()
	if err := account.RequestVerification(t.Context(), s, userID, email); err != nil {
		t.Fatalf("request verification for %s: %v", email, err)
	}
	var token string
	if err := pool.QueryRow(t.Context(), `
		SELECT payload->>'token'
		FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)
		ORDER BY id DESC
		LIMIT 1`, outbox.TopicEmailVerify, email).Scan(&token); err != nil {
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

		var waiting bool
		err := pool.QueryRow(t.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name = $1 AND wait_event_type = 'Lock'
			)`, applicationName).Scan(&waiting)
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
	s *cart.Store,
	cartID uuid.UUID,
	owner uuid.NullUUID,
	shippingID uuid.UUID,
	postalCode string,
) cart.CheckoutQuoteID {
	t.Helper()
	view, err := s.View(t.Context(), cartID)
	if err != nil {
		t.Fatalf("read account checkout cart: %v", err)
	}
	delivery, err := s.QuoteShipping(t.Context(), shippingID, view.SubtotalCents, postalCode)
	if err != nil {
		t.Fatalf("quote account checkout delivery: %v", err)
	}
	balance, err := s.AvailableCredit(t.Context(), owner)
	if err != nil {
		t.Fatalf("read account checkout credit: %v", err)
	}
	lines := make([]cart.CheckoutQuoteLine, 0, len(view.Lines))
	for i := range view.Lines {
		line := &view.Lines[i]
		variantID, parseErr := uuid.Parse(line.VariantID)
		if parseErr != nil {
			t.Fatalf("parse account checkout variant: %v", parseErr)
		}
		lines = append(lines, cart.CheckoutQuoteLine{
			VariantID: variantID, Quantity: line.Quantity, UnitCents: line.UnitCents,
		})
	}
	shipping, err := delivery.Total()
	if err != nil {
		t.Fatalf("total account checkout shipping: %v", err)
	}
	id, err := (cart.CheckoutQuote{
		CartID: cartID, Lines: lines,
		ShippingVersionID: shippingID, ShippingCents: shipping,
		CreditCents: min(balance, view.SubtotalCents+shipping),
	}).ID()
	if err != nil {
		t.Fatalf("build account checkout quote: %v", err)
	}
	return id
}

func TestPasswordIsNeverStoredInTheClear(t *testing.T) {
	s := account.NewStore(pool)
	const pw = "a sufficiently long password"
	u := register(t, s, "clear@example.com")

	var stored string
	if err := pool.QueryRow(t.Context(),
		`SELECT password_hash FROM users WHERE id = $1`, u.ID).Scan(&stored); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if stored == pw {
		t.Fatal("the password is stored in the clear")
	}
	if !account.VerifyPassword(stored, pw) {
		t.Error("the stored value does not verify the password it was made from")
	}
	if len(stored) < 40 || stored[:10] != "$argon2id$" {
		t.Errorf("the stored value is not an argon2id hash: %q", stored)
	}
}

func TestAuthenticateDoesNotDistinguishUnknownFromWrong(t *testing.T) {
	s := account.NewStore(pool)
	register(t, s, "enum@example.com")

	_, wrongPassword := s.Authenticate(t.Context(), "enum@example.com", "not the password")
	_, unknownEmail := s.Authenticate(t.Context(), "nobody@example.com", "not the password")

	if !errors.Is(wrongPassword, account.ErrBadCredentials) {
		t.Errorf("a wrong password gave %v, want ErrBadCredentials", wrongPassword)
	}
	if !errors.Is(unknownEmail, account.ErrBadCredentials) {
		t.Errorf("an unknown email gave %v, want ErrBadCredentials", unknownEmail)
	}
}

func TestAnOverLongPasswordIsAlwaysBadCredentials(t *testing.T) {
	s := account.NewStore(pool)
	register(t, s, "overlong-password@example.com")
	password := strings.Repeat("a", account.MaxPasswordBytes+1)

	for _, email := range []string{"overlong-password@example.com", "absent-overlong@example.com"} {
		if _, err := s.Authenticate(t.Context(), email, password); !errors.Is(err, account.ErrBadCredentials) {
			t.Errorf("Authenticate(%q, over-long password) = %v, want ErrBadCredentials", email, err)
		}
	}
}

// The unique index is on lower(email).
func TestAuthenticateIsCaseInsensitiveOnEmail(t *testing.T) {
	s := account.NewStore(pool)
	register(t, s, "Mixed@Example.com")

	if _, err := s.Authenticate(t.Context(), "mixed@example.com", "a sufficiently long password"); err != nil {
		t.Errorf("sign-in with a differently-cased email failed: %v", err)
	}
	if _, dupErr := s.Register(t.Context(), &account.Credentials{
		Email: "MIXED@EXAMPLE.COM", Password: "a sufficiently long password",
	}); !errors.Is(dupErr, account.ErrEmailTaken) {
		t.Errorf("registering the same address in another case gave %v, want ErrEmailTaken", dupErr)
	}
}

func TestSessionsAreStoredHashed(t *testing.T) {
	s := account.NewStore(pool)
	u := register(t, s, "session@example.com")

	token, err := s.StartSession(t.Context(), u.ID, "test-agent", "192.0.2.1")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	var n int
	if countErr := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM sessions WHERE token_hash = $1`, []byte(token)).Scan(&n); countErr != nil {
		t.Fatalf("count: %v", countErr)
	}
	if n != 0 {
		t.Error("the session table is keyed by the raw token; a leak would hand over live sessions")
	}

	got, err := s.SessionUser(t.Context(), token)
	if err != nil || got.ID != u.ID {
		t.Fatalf("SessionUser(token) = %v/%v, want %s", got.ID, err, u.ID)
	}
	if _, err := s.SessionUser(t.Context(), token+"x"); !errors.Is(err, account.ErrNotFound) {
		t.Error("a near-miss token found a session")
	}
}

func TestUnsafeUserAgentDoesNotRefuseOrDecorateASession(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "session-user-agent-"+uuid.NewString()+"@example.com")

	for _, tt := range []struct {
		name      string
		userAgent string
	}{
		{name: "overlong", userAgent: strings.Repeat("a", 513)},
		{name: "control character", userAgent: "browser\nforged"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			token, err := s.StartSession(ctx, u.ID, tt.userAgent, "192.0.2.1")
			if err != nil {
				t.Fatalf("unsafe optional user-agent refused the session: %v", err)
			}
			if got, sessionErr := s.SessionUser(ctx, token); sessionErr != nil || got.ID != u.ID {
				t.Fatalf("session created without unsafe decoration resolved to %q/%v, want %s/nil",
					got.ID, sessionErr, u.ID)
			}

			var stored *string
			if err := pool.QueryRow(ctx,
				`SELECT user_agent FROM sessions WHERE token_hash = $1`, account.HashToken(token)).
				Scan(&stored); err != nil {
				t.Fatalf("read persisted user-agent: %v", err)
			}
			if stored != nil {
				t.Errorf("unsafe user-agent persisted as %q, want NULL decoration", *stored)
			}
		})
	}

	safe := strings.Repeat("a", 512)
	token, err := s.StartSession(ctx, u.ID, safe, "192.0.2.1")
	if err != nil {
		t.Fatalf("exact safe user-agent ceiling refused the session: %v", err)
	}
	var stored string
	if err := pool.QueryRow(ctx,
		`SELECT user_agent FROM sessions WHERE token_hash = $1`, account.HashToken(token)).
		Scan(&stored); err != nil {
		t.Fatalf("read exact-ceiling user-agent: %v", err)
	}
	if stored != safe {
		t.Errorf("exact-ceiling user-agent persisted as %d runes, want %d",
			len([]rune(stored)), len([]rune(safe)))
	}
}

func TestExpiredSessionIsNobody(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "expired@example.com")

	token, err := s.StartSession(ctx, u.ID, "agent", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	// Both timestamps move: sessions_expiry_after_creation refuses a row whose
	// window closed before it opened.
	if _, err := pool.Exec(ctx, `
		UPDATE sessions
		SET created_at = now() - interval '2 hours',
		    expires_at = now() - interval '1 hour'
		WHERE token_hash = $1`, account.HashToken(token)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token_hash = $1`,
		account.HashToken(token)).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("the session row is gone; this test would prove nothing")
	}
	if _, err := s.SessionUser(ctx, token); !errors.Is(err, account.ErrNotFound) {
		t.Error("an expired session still resolved to a user")
	}
}

func TestChangingPasswordEndsEveryOtherSession(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "rotate@example.com")

	stolen, err := s.StartSession(ctx, u.ID, "thief", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if _, err := s.SessionUser(ctx, stolen); err != nil {
		t.Fatalf("the session did not start: %v", err)
	}

	if err := s.ChangePassword(ctx, u.ID, "an entirely different password"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, err := s.SessionUser(ctx, stolen); !errors.Is(err, account.ErrNotFound) {
		t.Error("a session survived the password change that was meant to end it")
	}
	if _, err := s.Authenticate(ctx, "rotate@example.com", "an entirely different password"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
	if _, err := s.Authenticate(ctx, "rotate@example.com", "a sufficiently long password"); err == nil {
		t.Error("the old password still works")
	}
}

func TestOrdersAreScopedToTheirOwner(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	mine := register(t, s, "mine@example.com")
	theirs := register(t, s, "theirs@example.com")

	number := placeOrderFor(t, theirs.ID)

	if _, err := s.Order(ctx, theirs, number); err != nil {
		t.Fatalf("the owner cannot see their own order: %v", err)
	}
	_, otherErr := s.Order(ctx, mine, number)
	_, missingErr := s.Order(ctx, mine, "GO-000000-999999")
	if !errors.Is(otherErr, account.ErrNotFound) {
		t.Errorf("another customer's order gave %v, want ErrNotFound", otherErr)
	}
	if !errors.Is(missingErr, account.ErrNotFound) {
		t.Errorf("a nonexistent order gave %v, want ErrNotFound", missingErr)
	}

	view, err := s.Overview(ctx, mine)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	for _, o := range view.Orders {
		if o.Number == number {
			t.Error("another customer's order is listed in this account's history")
		}
	}
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
		VALUES ($1, $2, 'TEST-SKU', '測試商品', 100000, 1)`, orderID, variantID); err != nil {
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

func TestAdoptCartMergesRatherThanReplaces(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "merge@example.com")
	uid := uuid.MustParse(u.ID)

	var a, b uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position LIMIT 1`).Scan(&a); err != nil {
		t.Fatalf("variant a: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active AND id <> $1 ORDER BY position LIMIT 1`,
		a).Scan(&b); err != nil {
		t.Fatalf("variant b: %v", err)
	}

	var accountCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (token_hash, user_id) VALUES ($1, $2) RETURNING id`,
		account.HashToken("account-cart-"+u.ID), uid).Scan(&accountCart); err != nil {
		t.Fatalf("account cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
		accountCart, a); err != nil {
		t.Fatalf("account line: %v", err)
	}

	var guestCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (token_hash) VALUES ($1) RETURNING id`,
		account.HashToken("guest-cart-"+u.ID)).Scan(&guestCart); err != nil {
		t.Fatalf("guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity)
		VALUES ($1, $2, 2), ($1, $3, 5)`, guestCart, a, b); err != nil {
		t.Fatalf("guest lines: %v", err)
	}

	if err := s.AdoptCart(ctx, u.ID, guestCart); err != nil {
		t.Fatalf("adopt: %v", err)
	}

	rows, err := pool.Query(ctx,
		`SELECT variant_id, quantity FROM cart_items WHERE cart_id = $1`, accountCart)
	if err != nil {
		t.Fatalf("read merged: %v", err)
	}
	defer rows.Close()
	got := map[uuid.UUID]int32{}
	for rows.Next() {
		var v uuid.UUID
		var q int32
		if err := rows.Scan(&v, &q); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[v] = q
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("iterate merged cart lines: %v", rowsErr)
	}
	if got[a] != 3 {
		t.Errorf("variant in both carts has quantity %d, want 3 (1 + 2 summed, not replaced)", got[a])
	}
	if got[b] != 5 {
		t.Errorf("variant only in the guest cart has quantity %d, want 5", got[b])
	}

	var guestStillThere int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM carts WHERE id = $1`, guestCart).Scan(&guestStillThere); err != nil {
		t.Fatalf("count guest: %v", err)
	}
	if guestStillThere != 0 {
		t.Error("the guest cart survived the merge and would be adopted again on the next sign-in")
	}
}

// TestConcurrentFirstAdoptersKeepBothGuestCarts holds the account row before
// either sign-in starts. Both calls must wait there, which makes the "no account
// cart exists" observation deterministic rather than scheduler luck. One cart is
// then adopted and the other merged into it, both through the store role.
func TestConcurrentFirstAdoptersKeepBothGuestCarts(t *testing.T) {
	ctx := t.Context()
	u := register(t, account.NewStore(pool), "first-adopters-"+uuid.NewString()+"@example.com")
	userID := uuid.MustParse(u.ID)

	var firstVariant, secondVariant uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position, id LIMIT 1`).
		Scan(&firstVariant); err != nil {
		t.Fatalf("first variant: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT id FROM product_variants
		WHERE is_active AND id <> $1 ORDER BY position, id LIMIT 1`, firstVariant).
		Scan(&secondVariant); err != nil {
		t.Fatalf("second variant: %v", err)
	}

	guest := func(label string, variantID uuid.UUID, quantity int32) uuid.UUID {
		t.Helper()
		var cartID uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO carts (token_hash) VALUES ($1) RETURNING id`,
			account.HashToken(label+u.ID)).Scan(&cartID); err != nil {
			t.Fatalf("create %s: %v", label, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, $3)`,
			cartID, variantID, quantity); err != nil {
			t.Fatalf("fill %s: %v", label, err)
		}
		return cartID
	}
	firstCart := guest("first-adopter-a-", firstVariant, 2)
	secondCart := guest("first-adopter-b-", secondVariant, 3)

	blocker, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin account blocker: %v", beginErr)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, lockErr := blocker.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID); lockErr != nil {
		t.Fatalf("lock account: %v", lockErr)
	}

	suffix := uuid.NewString()[:8]
	firstName, secondName := "adopt-first-a-"+suffix, "adopt-first-b-"+suffix
	firstStore := account.NewStore(accountStorePool(t, firstName))
	secondStore := account.NewStore(accountStorePool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- firstStore.AdoptCart(ctx, u.ID, firstCart) }()
	go func() { secondDone <- secondStore.AdoptCart(ctx, u.ID, secondCart) }()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if commitErr := blocker.Commit(ctx); commitErr != nil {
		t.Fatalf("release account: %v", commitErr)
	}
	if firstErr := operationResult(t, firstDone); firstErr != nil {
		t.Errorf("first adoption: %v", firstErr)
	}
	if secondErr := operationResult(t, secondDone); secondErr != nil {
		t.Errorf("second adoption: %v", secondErr)
	}

	var accountCart uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM carts WHERE user_id = $1`, userID).
		Scan(&accountCart); err != nil {
		t.Fatalf("read adopted account cart: %v", err)
	}
	if accountCart != firstCart && accountCart != secondCart {
		t.Fatalf("account cart = %s, want one of the two guest carts", accountCart)
	}
	var accountCarts, originalCarts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM carts WHERE user_id = $1`, userID).
		Scan(&accountCarts); err != nil {
		t.Fatalf("count account carts: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM carts WHERE id = ANY($1::uuid[])`, []uuid.UUID{firstCart, secondCart}).
		Scan(&originalCarts); err != nil {
		t.Fatalf("count original carts: %v", err)
	}
	if accountCarts != 1 || originalCarts != 1 {
		t.Fatalf("after concurrent adoption account/original carts = %d/%d, want 1/1",
			accountCarts, originalCarts)
	}

	rows, err := pool.Query(ctx,
		`SELECT variant_id, quantity FROM cart_items WHERE cart_id = $1`, accountCart)
	if err != nil {
		t.Fatalf("read adopted cart lines: %v", err)
	}
	defer rows.Close()
	got := map[uuid.UUID]int32{}
	for rows.Next() {
		var variantID uuid.UUID
		var quantity int32
		if err := rows.Scan(&variantID, &quantity); err != nil {
			t.Fatalf("scan adopted cart line: %v", err)
		}
		got[variantID] = quantity
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate adopted cart lines: %v", err)
	}
	if got[firstVariant] != 2 || got[secondVariant] != 3 || len(got) != 2 {
		t.Errorf("adopted cart lines = %v, want both guest facts", got)
	}
}

// TestTwoAccountsCannotAdoptTheSameGuestCart holds the shared cart after each
// transaction has locked its own user, so both make the first-adopter decision
// before either can update the cart. The winner keeps the cart; the loser must
// observe its new owner under the cart lock and leave it untouched.
func TestTwoAccountsCannotAdoptTheSameGuestCart(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	firstUser := register(t, s, "shared-guest-a-"+uuid.NewString()+"@example.com")
	secondUser := register(t, s, "shared-guest-b-"+uuid.NewString()+"@example.com")
	firstUserID, secondUserID := uuid.MustParse(firstUser.ID), uuid.MustParse(secondUser.ID)

	var variantID, guestCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position, id LIMIT 1`).
		Scan(&variantID); err != nil {
		t.Fatalf("variant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (token_hash) VALUES ($1) RETURNING id`,
		account.HashToken("shared-guest-"+uuid.NewString())).Scan(&guestCart); err != nil {
		t.Fatalf("create shared guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 4)`,
		guestCart, variantID); err != nil {
		t.Fatalf("fill shared guest cart: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin cart blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM carts WHERE id = $1 FOR UPDATE`, guestCart); err != nil {
		t.Fatalf("lock shared guest cart: %v", err)
	}

	suffix := uuid.NewString()[:8]
	firstName, secondName := "adopt-shared-a-"+suffix, "adopt-shared-b-"+suffix
	firstStore := account.NewStore(accountStorePool(t, firstName))
	secondStore := account.NewStore(accountStorePool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- firstStore.AdoptCart(ctx, firstUser.ID, guestCart) }()
	go func() { secondDone <- secondStore.AdoptCart(ctx, secondUser.ID, guestCart) }()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release shared guest cart: %v", err)
	}
	firstErr, secondErr := operationResult(t, firstDone), operationResult(t, secondDone)
	firstWon := firstErr == nil && errors.Is(secondErr, account.ErrNotFound)
	secondWon := secondErr == nil && errors.Is(firstErr, account.ErrNotFound)
	if !firstWon && !secondWon {
		t.Fatalf("shared guest adoption results = %v / %v, want one success and one ErrNotFound",
			firstErr, secondErr)
	}

	wantOwner, loser := firstUserID, secondUserID
	if secondWon {
		wantOwner, loser = secondUserID, firstUserID
	}
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM carts WHERE id = $1`, guestCart).
		Scan(&owner); err != nil {
		t.Fatalf("read shared cart owner: %v", err)
	}
	if owner != wantOwner {
		t.Errorf("shared cart owner = %s, want winning account %s", owner, wantOwner)
	}
	var loserCarts, quantity int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM carts WHERE user_id = $1`, loser).
		Scan(&loserCarts); err != nil {
		t.Fatalf("count losing account carts: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
		guestCart, variantID).Scan(&quantity); err != nil {
		t.Fatalf("read preserved shared cart line: %v", err)
	}
	if loserCarts != 0 || quantity != 4 {
		t.Errorf("loser carts / preserved quantity = %d/%d, want 0/4", loserCarts, quantity)
	}
}

// TestCheckoutAndAdoptionShareUserBeforeCartLockOrder pauses checkout after it
// owns the account cart. Adoption then holds NO KEY UPDATE on the user and waits
// for that cart. Releasing checkout is safe only because its user KEY SHARE is
// already held (and compatible); the former cart -> user acquisition deadlocked.
func TestCheckoutAndAdoptionShareUserBeforeCartLockOrder(t *testing.T) {
	ctx := t.Context()
	u := register(t, account.NewStore(pool), "checkout-adopt-"+uuid.NewString()+"@example.com")
	userID := uuid.MustParse(u.ID)
	owner := uuid.NullUUID{UUID: userID, Valid: true}

	var checkoutVariant, guestVariant uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position, id LIMIT 1`).
		Scan(&checkoutVariant); err != nil {
		t.Fatalf("checkout variant: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT id FROM product_variants
		WHERE is_active AND id <> $1 ORDER BY position, id LIMIT 1`, checkoutVariant).
		Scan(&guestVariant); err != nil {
		t.Fatalf("guest variant: %v", err)
	}

	var accountCart, guestCart uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO carts (token_hash, user_id) VALUES ($1, $2) RETURNING id`,
		account.HashToken("checkout-adopt-account-"+u.ID), userID).Scan(&accountCart); err != nil {
		t.Fatalf("create account cart: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (token_hash) VALUES ($1) RETURNING id`,
		account.HashToken("checkout-adopt-guest-"+u.ID)).Scan(&guestCart); err != nil {
		t.Fatalf("create guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity)
		VALUES ($1, $2, 1), ($3, $4, 2)`,
		accountCart, checkoutVariant, guestCart, guestVariant); err != nil {
		t.Fatalf("fill checkout/adoption carts: %v", err)
	}

	var shippingID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT v.id
		FROM shipping_method_versions v
		JOIN shipping_methods m ON m.id = v.method_id
		WHERE m.code = 'home_delivery'
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`).Scan(&shippingID); err != nil {
		t.Fatalf("read home-delivery version: %v", err)
	}
	addr := &cart.Address{
		Email: u.Email, Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shown := accountCheckoutQuote(t, cart.NewStore(pool), accountCart, owner, shippingID, addr.PostalCode)

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin catalogue blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx,
		`SELECT 1 FROM product_variants WHERE id = $1 FOR UPDATE`, checkoutVariant); err != nil {
		t.Fatalf("lock checkout variant: %v", err)
	}

	suffix := uuid.NewString()[:8]
	checkoutName, adoptionName := "checkout-before-adopt-"+suffix, "adopt-behind-checkout-"+suffix
	checkoutStore := cart.NewStore(accountStorePool(t, checkoutName))
	adoptionStore := account.NewStore(accountStorePool(t, adoptionName))
	checkoutDone, adoptionDone := make(chan error, 1), make(chan error, 1)
	var orderNumber string
	go func() {
		var placeErr error
		orderNumber, placeErr = checkoutStore.PlaceOrder(
			ctx, accountCart, owner, shippingID, addr, nil, "", shown,
			checkoutAttemptKey("checkout-adopt-"+suffix),
		)
		checkoutDone <- placeErr
	}()
	waitForAccountLock(t, checkoutName, checkoutDone)
	go func() { adoptionDone <- adoptionStore.AdoptCart(ctx, u.ID, guestCart) }()
	waitForAccountLock(t, adoptionName, adoptionDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release checkout catalogue: %v", err)
	}
	if err := operationResult(t, checkoutDone); err != nil {
		t.Errorf("checkout in user/cart lock interleaving: %v", err)
	}
	if err := operationResult(t, adoptionDone); err != nil {
		t.Errorf("adoption in user/cart lock interleaving: %v", err)
	}
	if orderNumber == "" {
		t.Error("checkout produced no order number")
	}

	var adoptedQuantity int32
	if err := pool.QueryRow(ctx, `
		SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
		accountCart, guestVariant).Scan(&adoptedQuantity); err != nil {
		t.Fatalf("read cart merged after checkout: %v", err)
	}
	if adoptedQuantity != 2 {
		t.Errorf("cart merged after checkout with quantity %d, want 2", adoptedQuantity)
	}
}

func TestSessionExpiryUsesTheDatabaseClockAndTTL(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "ttl@example.com")

	token, err := s.StartSession(ctx, u.ID, "agent", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	var expires time.Time
	var exactTTL bool
	if err := pool.QueryRow(ctx, `
		SELECT expires_at,
		       expires_at = created_at + ($2 * interval '1 second')
		FROM sessions WHERE token_hash = $1`,
		account.HashToken(token), account.SessionTTL).Scan(&expires, &exactTTL); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	if !exactTTL {
		t.Error("session expiry was not derived from the row's database creation clock and TTL")
	}
	if !expires.After(time.Now().Add(24 * time.Hour)) {
		t.Errorf("session expires at %v, which is less than a day away", expires)
	}
}

type statusRecorder struct {
	*httptest.ResponseRecorder

	statuses []int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
	w.ResponseRecorder.WriteHeader(status)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// TestSessionWriteFailureDoesNotFallThroughToRedirect proves all three entry
// paths stop after startSession writes its 500. net/http keeps the first status
// code, so a second WriteHeader is what this observes.
func TestSessionWriteFailureDoesNotFallThroughToRedirect(t *testing.T) {
	s := account.NewStore(pool)
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	existing := register(t, s, "session-failure-signin-"+uuid.NewString()+"@example.com")

	for _, tt := range []struct {
		name   string
		target string
		form   func() url.Values
		handle http.HandlerFunc
	}{
		{
			name: "sign in", target: "/signin", handle: h.SignIn,
			form: func() url.Values {
				return url.Values{
					"email": {existing.Email}, "password": {"a sufficiently long password"},
					"next": {"/account"},
				}
			},
		},
		{
			name: "registration", target: "/register", handle: h.Register,
			form: func() url.Values {
				return url.Values{
					"email":    {"session-failure-register-" + uuid.NewString() + "@example.com"},
					"password": {"another sufficiently long password"},
					"confirm":  {"another sufficiently long password"}, "name": {"測試"},
					"next": {"/account"},
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			forceSessionInsertFailure(t)
			body := tt.form().Encode()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				tt.target, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			out := &statusRecorder{ResponseRecorder: httptest.NewRecorder()}
			tt.handle(out, req)
			if out.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", out.Code)
			}
			if len(out.statuses) != 1 || out.statuses[0] != http.StatusInternalServerError {
				t.Errorf("WriteHeader calls = %v, want one 500 and no redirect", out.statuses)
			}
			if location := out.Header().Get("Location"); location != "" {
				t.Errorf("session failure also wrote redirect Location %q", location)
			}
		})
	}

	google, err := account.NewGoogle("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("new google client: %v", err)
	}
	account.SetGoogleHTTPClient(google, &http.Client{Transport: roundTripFunc(
		func(r *http.Request) (*http.Response, error) {
			var body string
			switch r.URL.String() {
			case "https://oauth2.googleapis.com/token":
				body = `{"access_token":"test-access-token"}`
			case "https://openidconnect.googleapis.com/v1/userinfo":
				body = fmt.Sprintf(
					`{"sub":%q,"email":%q,"email_verified":true,"name":"Google Test"}`,
					"session-failure-google-"+uuid.NewString(),
					"session-failure-google-"+uuid.NewString()+"@example.com",
				)
			default:
				return nil, fmt.Errorf("unexpected google endpoint %s", r.URL)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		},
	)})
	googleHandler := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, google)

	start := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/google?next=/account", http.NoBody)
	startOut := httptest.NewRecorder()
	googleHandler.GoogleSignIn(startOut, start)
	if startOut.Code != http.StatusSeeOther {
		t.Fatalf("start google sign-in status = %d, want 303", startOut.Code)
	}
	target, err := url.Parse(startOut.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse google redirect: %v", err)
	}
	state := target.Query().Get("state")
	if state == "" {
		t.Fatal("google redirect has no state")
	}
	cookies := startOut.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("google start cookies = %d, want OAuth state cookie", len(cookies))
	}

	forceSessionInsertFailure(t)
	callback := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/google/callback?state="+url.QueryEscape(state)+"&code=test-code", http.NoBody)
	callback.AddCookie(cookies[0])
	callbackOut := &statusRecorder{ResponseRecorder: httptest.NewRecorder()}
	googleHandler.GoogleCallback(callbackOut, callback)
	if callbackOut.Code != http.StatusInternalServerError {
		t.Fatalf("google callback status = %d, want 500", callbackOut.Code)
	}
	if len(callbackOut.statuses) != 1 || callbackOut.statuses[0] != http.StatusInternalServerError {
		t.Errorf("google callback WriteHeader calls = %v, want one 500 and no redirect",
			callbackOut.statuses)
	}
	if location := callbackOut.Header().Get("Location"); location != "" {
		t.Errorf("google session failure also wrote redirect Location %q", location)
	}
}

func forceSessionInsertFailure(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_fail_session_insert_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_fail_session_insert_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			RAISE EXCEPTION USING MESSAGE = 'forced session insert failure',
			                      ERRCODE = 'check_violation';
		END
		$body$;
		CREATE TRIGGER %s BEFORE INSERT ON sessions
		FOR EACH ROW EXECUTE FUNCTION %s()`, functionName, triggerName, functionName)); err != nil {
		t.Fatalf("install session insert failure: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON sessions; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName))
	})
}

// TestRawAndDirectAccountWritesRespectRenderedBounds covers both HTTP bypasses
// of maxlength and callers that enter through Store without a browser.
func TestRawAndDirectAccountWritesRespectRenderedBounds(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	u := register(t, s, "bounded-account-"+uuid.NewString()+"@example.com")

	post := func(target string, values url.Values, handle http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(account.WithUser(ctx, u), http.MethodPost,
			target, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		out := httptest.NewRecorder()
		handle(out, req)
		return out
	}

	profile := post("/account/profile", url.Values{
		"name": {strings.Repeat("名", 61)}, "phone": {"0912345678"},
	}, h.UpdateProfile)
	if profile.Code != http.StatusSeeOther || profile.Header().Get("Location") != "/account?profile=invalid" {
		t.Errorf("overlong raw profile = %d Location %q, want 303 invalid",
			profile.Code, profile.Header().Get("Location"))
	}
	if err := s.UpdateProfile(ctx, u.ID, "測試", strings.Repeat("1", 31)); !errors.Is(err, account.ErrInvalidInput) {
		t.Errorf("direct overlong profile = %v, want ErrInvalidInput", err)
	}
	var storedName string
	var storedPhone *string
	if err := pool.QueryRow(ctx, `SELECT full_name, phone FROM users WHERE id = $1`, u.ID).
		Scan(&storedName, &storedPhone); err != nil {
		t.Fatalf("read profile after refusals: %v", err)
	}
	if storedName != "測試" || storedPhone != nil {
		t.Errorf("refused profile was written as name=%v phone=%v", storedName, storedPhone)
	}

	validAddress := url.Values{
		"label": {strings.Repeat("標", 31)}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street": {"松高路 1 號"},
	}
	address := post("/account/addresses", validAddress, h.AddAddress)
	if address.Code != http.StatusSeeOther || address.Header().Get("Location") != "/account?address=invalid" {
		t.Errorf("overlong raw address = %d Location %q, want 303 invalid",
			address.Code, address.Header().Get("Location"))
	}
	directAddress := &account.Address{
		Label: "家", Name: "王小明", Phone: "0912345678", PostalCode: "110",
		City: "台北市", District: "信義區", Street: strings.Repeat("路", 201),
	}
	if err := s.AddAddress(ctx, u.ID, directAddress); !errors.Is(err, account.ErrInvalidInput) {
		t.Errorf("direct overlong address = %v, want ErrInvalidInput", err)
	}
	baseAddress := account.Address{
		Label: "家", Name: "王小明", Phone: "0912345678", PostalCode: "110",
		City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	for _, tt := range []struct {
		name string
		mut  func(*account.Address)
	}{
		{name: "phone letters", mut: func(a *account.Address) { a.Phone = "09AB123456" }},
		{name: "short phone", mut: func(a *account.Address) { a.Phone = "02-12345" }},
		{name: "postal letters", mut: func(a *account.Address) { a.PostalCode = "11A" }},
		{name: "short postal", mut: func(a *account.Address) { a.PostalCode = "11" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := baseAddress
			tt.mut(&candidate)
			if err := s.AddAddress(ctx, u.ID, &candidate); !errors.Is(err, account.ErrInvalidInput) {
				t.Errorf("direct malformed address = %v, want ErrInvalidInput", err)
			}
		})
	}
	var addresses int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM addresses WHERE user_id = $1`, u.ID).
		Scan(&addresses); err != nil {
		t.Fatalf("count saved addresses: %v", err)
	}
	if addresses != 0 {
		t.Errorf("%d refused addresses were written, want 0", addresses)
	}
}

// Delivery PII is erased, while the minimum immutable filing snapshot remains
// with the sale so its statutory invoice/allowance lifecycle is not destroyed.
func TestEraseRemovesPersonalDataAndKeepsTheRecord(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "gdpr@example.com")
	number := placeOrderFor(t, u.ID)

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences
		    (order_id, invoice_type, carrier_code, customer_name, customer_email)
		VALUES ($1, 'mobile_carrier', '/ABC+123', '收件人', 'x@example.com')`, orderID); err != nil {
		t.Fatalf("invoice preference: %v", err)
	}

	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase: %v", err)
	}

	for _, probe := range []struct {
		what  string
		query string
		arg   any
	}{
		{"the account", `SELECT count(*) FROM users WHERE email = 'gdpr@example.com'`, nil},
		{"delivery details", `SELECT count(*) FROM order_private_data
			WHERE order_id = $1 AND email IS NOT NULL`, orderID},
	} {
		var n int
		var err error
		if probe.arg == nil {
			err = pool.QueryRow(ctx, probe.query).Scan(&n)
		} else {
			err = pool.QueryRow(ctx, probe.query, probe.arg).Scan(&n)
		}
		if err != nil {
			t.Fatalf("probe %s: %v", probe.what, err)
		}
		if n != 0 {
			t.Errorf("%s survived erasure (%d row(s))", probe.what, n)
		}
	}

	var invoiceType, carrier, filingName, filingEmail string
	if err := pool.QueryRow(ctx, `
		SELECT invoice_type, carrier_code, customer_name, customer_email
		FROM invoice_preferences WHERE order_id = $1`, orderID).
		Scan(&invoiceType, &carrier, &filingName, &filingEmail); err != nil {
		t.Fatalf("read retained tax snapshot: %v", err)
	}
	if invoiceType != "mobile_carrier" || carrier != "/ABC+123" ||
		filingName != "收件人" || filingEmail != "x@example.com" {
		t.Errorf("retained tax snapshot = %q/%q/%q/%q",
			invoiceType, carrier, filingName, filingEmail)
	}

	var subtotal int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(unit_price_cents * quantity), 0) FROM order_lines WHERE order_id = $1`,
		orderID).Scan(&subtotal); err != nil {
		t.Fatalf("read order lines: %v", err)
	}
	if subtotal != 100000 {
		t.Errorf("the erased customer's order is worth %d, want 100000 — erasure altered "+
			"the financial record", subtotal)
	}
}

// TestErasureWaitsForAStoreCreditFundedReturn keeps the customer attached to an
// open claim until the shop has returned the money needed to settle it. The
// account relation is what lets compensate_return_with_credit identify the
// owner; erasing it while a requested or approved return is still owed credit
// would turn a valid return into an orphan the payout door must refuse.
func TestErasureWaitsForAStoreCreditFundedReturn(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "erase-open-return-"+uuid.NewString()+"@example.com")
	userID := uuid.MustParse(u.ID)
	requestID, orderID, creditAccountID := creditFundedOpenReturnForErasure(t, u)

	if err := s.Erase(ctx, u.ID); !errors.Is(err, account.ErrOpenReturn) {
		t.Fatalf("erase with requested return = %v, want ErrOpenReturn", err)
	}
	assertOpenReturnErasureRolledBack(t, userID, orderID, creditAccountID)

	// The browser gets a recoverable account-page outcome and keeps its session;
	// an ordinary server error would hide the action the customer must wait for.
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	form := url.Values{"confirm": {u.Email}}
	req := httptest.NewRequestWithContext(account.WithUser(ctx, u), http.MethodPost,
		"/account/erase", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	out := httptest.NewRecorder()
	h.Erase(out, req)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/account?erase=return" {
		t.Fatalf("erase handler = %d Location %q, want 303 /account?erase=return",
			out.Code, out.Header().Get("Location"))
	}
	if cookies := out.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("refused erasure changed %d cookie(s); the signed-in account must remain usable", len(cookies))
	}
	assertOpenReturnErasureRolledBack(t, userID, orderID, creditAccountID)

	// A decision does not settle the customer's money. Approval therefore stays
	// protected until the exact credit-funded amount has landed in the ledger.
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', resolution = '同意退貨', decided_at = now()
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve return: %v", err)
	}
	if err := s.Erase(ctx, u.ID); !errors.Is(err, account.ErrOpenReturn) {
		t.Fatalf("erase with approved unpaid return = %v, want ErrOpenReturn", err)
	}
	assertOpenReturnErasureRolledBack(t, userID, orderID, creditAccountID)

	var actorID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('erase-return-staff-' || gen_random_uuid() || '@goen.invalid',
		        'staff', '退貨處理員')
		RETURNING id`).Scan(&actorID); err != nil {
		t.Fatalf("create return actor: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT compensate_return_with_credit($1, 100000, $2)`, requestID, actorID); err != nil {
		t.Fatalf("settle return credit: %v", err)
	}
	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase after return credit settled: %v", err)
	}

	var userGone, creditDetached, orderDetached, deliveryErased, returnPreserved bool
	if err := pool.QueryRow(ctx, `
		SELECT
			NOT EXISTS (SELECT 1 FROM users WHERE id = $1),
			EXISTS (SELECT 1 FROM store_credit_accounts
			        WHERE id = $2 AND user_id IS NULL),
			EXISTS (SELECT 1 FROM orders WHERE id = $3 AND user_id IS NULL),
			EXISTS (SELECT 1 FROM order_private_data
			        WHERE order_id = $3 AND erased_at IS NOT NULL AND email IS NULL),
			EXISTS (SELECT 1 FROM return_requests
			        WHERE id = $4 AND status = 'approved')`,
		userID, creditAccountID, orderID, requestID).Scan(
		&userGone, &creditDetached, &orderDetached, &deliveryErased, &returnPreserved,
	); err != nil {
		t.Fatalf("read settled erasure state: %v", err)
	}
	if !userGone || !creditDetached || !orderDetached || !deliveryErased || !returnPreserved {
		t.Errorf("settled erasure state userGone/creditDetached/orderDetached/"+
			"deliveryErased/returnPreserved = %t/%t/%t/%t/%t, want all true",
			userGone, creditDetached, orderDetached, deliveryErased, returnPreserved)
	}
}

func creditFundedOpenReturnForErasure(
	t *testing.T,
	u account.User,
) (requestID, orderID, creditAccountID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	userID := uuid.MustParse(u.ID)
	number := placeOrderFor(t, u.ID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin credit-funded return: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT o.id, ol.id
		FROM orders o JOIN order_lines ol ON ol.order_id = o.id
		WHERE o.order_number = $1`, number).Scan(&orderID, &lineID); err != nil {
		t.Fatalf("read return order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		SELECT post_store_credit($1, 100000, '測試退貨額度', NULL, $2, NULL)`,
		userID, "erase-return-grant:"+orderID.String()); err != nil {
		t.Fatalf("grant return fixture credit: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT spend_store_credit($1, -100000)`, orderID); err != nil {
		t.Fatalf("fund return fixture order with credit: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move credit-funded order to picking: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move credit-funded order to shipped: %v", err)
	}
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, '黑貓', 'ERASE-RETURN-' || $2)
		RETURNING id`, orderID, number).Scan(&shipmentID); err != nil {
		t.Fatalf("create return fixture shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines
			(order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("ship return fixture line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, requested_by_user_id, reason)
		VALUES ($1, $2, '不合用')
		RETURNING id`, orderID, userID).Scan(&requestID); err != nil {
		t.Fatalf("open return fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines
			(order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, requestID, lineID); err != nil {
		t.Fatalf("add return fixture line: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT id FROM store_credit_accounts WHERE user_id = $1`, userID).
		Scan(&creditAccountID); err != nil {
		t.Fatalf("read return fixture credit account: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit credit-funded return: %v", err)
	}
	return requestID, orderID, creditAccountID
}

func assertOpenReturnErasureRolledBack(
	t *testing.T,
	userID, orderID, creditAccountID uuid.UUID,
) {
	t.Helper()
	var userPresent, creditAttached, orderAttached, deliveryPresent bool
	if err := pool.QueryRow(t.Context(), `
		SELECT
			EXISTS (SELECT 1 FROM users WHERE id = $1),
			EXISTS (SELECT 1 FROM store_credit_accounts
			        WHERE id = $2 AND user_id = $1),
			EXISTS (SELECT 1 FROM orders WHERE id = $3 AND user_id = $1),
			EXISTS (SELECT 1 FROM order_private_data
			        WHERE order_id = $3 AND erased_at IS NULL AND email IS NOT NULL)`,
		userID, creditAccountID, orderID).Scan(
		&userPresent, &creditAttached, &orderAttached, &deliveryPresent,
	); err != nil {
		t.Fatalf("read refused erasure state: %v", err)
	}
	if !userPresent || !creditAttached || !orderAttached || !deliveryPresent {
		t.Errorf("refused erasure state user/credit/order/delivery = %t/%t/%t/%t, want all true",
			userPresent, creditAttached, orderAttached, deliveryPresent)
	}
}

// TestUnverifiedAccountErasureDoesNotClaimTheMailbox keeps account deletion
// from becoming an address-wide eraser. Registration accepts an unproved email;
// the account therefore has no authority over guest or independently confirmed
// records that happen to use the same address.
func TestUnverifiedAccountErasureDoesNotClaimTheMailbox(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	victim := "unproved-victim-" + suffix + "@goen.invalid"
	u := register(t, s, victim)

	var variantID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position, id LIMIT 1`).
		Scan(&variantID); err != nil {
		t.Fatalf("read victim restock variant: %v", err)
	}
	mailKey := "unproved-victim-mail:" + suffix
	for name, seed := range map[string]struct {
		query string
		args  []any
	}{
		"newsletter subscription": {
			`INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ($1, $2)`,
			[]any{victim, "unproved-victim-unsubscribe-" + suffix},
		},
		"contact message": {
			`INSERT INTO contact_messages (name, email, subject, message)
			 VALUES ('Guest owner', $1, '訂單問題', 'This belongs to the guest mailbox owner.')`,
			[]any{victim},
		},
		"guest restock request": {
			`INSERT INTO stock_notifications (variant_id, email) VALUES ($1, $2)`,
			[]any{variantID, victim},
		},
		"transactional mail": {
			`INSERT INTO outbox_messages (topic, dedupe_key, payload)
			 VALUES ($1, $2, jsonb_build_object('email', $3::text, 'order_number', 'G-VICTIM'))`,
			[]any{outbox.TopicOrderPlaced, mailKey, victim},
		},
	} {
		if _, err := pool.Exec(ctx, seed.query, seed.args...); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM outbox_messages WHERE dedupe_key = $1`, mailKey)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM stock_notifications WHERE lower(email) = lower($1)`, victim)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM contact_messages WHERE lower(email) = lower($1)`, victim)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM newsletter_subscribers WHERE lower(email) = lower($1)`, victim)
	})

	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase unverified account: %v", err)
	}

	for name, probe := range map[string]struct {
		query string
		args  []any
	}{
		"newsletter subscription": {
			`SELECT count(*) FROM newsletter_subscribers WHERE lower(email) = lower($1)`,
			[]any{victim},
		},
		"contact message": {
			`SELECT count(*) FROM contact_messages WHERE lower(email) = lower($1)`,
			[]any{victim},
		},
		"guest restock request": {
			`SELECT count(*) FROM stock_notifications WHERE lower(email) = lower($1)`,
			[]any{victim},
		},
		"transactional mail": {
			`SELECT count(*) FROM outbox_messages WHERE dedupe_key = $1`,
			[]any{mailKey},
		},
	} {
		var rows int
		if err := pool.QueryRow(ctx, probe.query, probe.args...).Scan(&rows); err != nil {
			t.Fatalf("count surviving %s: %v", name, err)
		}
		if rows != 1 {
			t.Errorf("unverified erasure left %d %s rows, want 1", rows, name)
		}
	}
}

// TestErasureSnapshotsBeforeConcurrentCheckout pauses erase_user after it has
// cleared every order it can currently see. A signed-in checkout starts in that
// window. Its user KEY SHARE must wait before taking the cart; erasure can then
// delete the account and cart, and checkout returns without creating fresh PII.
func TestErasureSnapshotsBeforeConcurrentCheckout(t *testing.T) {
	ctx := t.Context()
	u := register(t, account.NewStore(pool), "erase-checkout-"+uuid.NewString()+"@example.com")
	userID := uuid.MustParse(u.ID)
	owner := uuid.NullUUID{UUID: userID, Valid: true}

	var variantID, cartID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position, id LIMIT 1`).
		Scan(&variantID); err != nil {
		t.Fatalf("checkout variant: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO carts (token_hash, user_id) VALUES ($1, $2) RETURNING id`,
		account.HashToken("erase-checkout-cart-"+u.ID), userID).Scan(&cartID); err != nil {
		t.Fatalf("create erase-race cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
		cartID, variantID); err != nil {
		t.Fatalf("fill erase-race cart: %v", err)
	}

	var shippingID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT v.id
		FROM shipping_method_versions v
		JOIN shipping_methods m ON m.id = v.method_id
		WHERE m.code = 'home_delivery'
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`).Scan(&shippingID); err != nil {
		t.Fatalf("read home-delivery version: %v", err)
	}
	addr := &cart.Address{
		Email: u.Email, Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shown := accountCheckoutQuote(t, cart.NewStore(pool), cartID, owner, shippingID, addr.PostalCode)

	var notificationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO stock_notifications (variant_id, user_id, email)
		VALUES ($1, $2, $3) RETURNING id`, variantID, userID, u.Email).
		Scan(&notificationID); err != nil {
		t.Fatalf("create erasure blocker row: %v", err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin erasure blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx,
		`SELECT 1 FROM stock_notifications WHERE id = $1 FOR UPDATE`, notificationID); err != nil {
		t.Fatalf("lock erasure blocker row: %v", err)
	}

	suffix := uuid.NewString()[:8]
	eraseName, checkoutName := "erase-before-checkout-"+suffix, "checkout-behind-erase-"+suffix
	eraseStore := account.NewStore(accountStorePool(t, eraseName))
	checkoutStore := cart.NewStore(accountStorePool(t, checkoutName))
	eraseDone, checkoutDone := make(chan error, 1), make(chan error, 1)
	go func() { eraseDone <- eraseStore.Erase(ctx, u.ID) }()
	waitForAccountLock(t, eraseName, eraseDone)
	go func() {
		_, placeErr := checkoutStore.PlaceOrder(
			ctx, cartID, owner, shippingID, addr, nil, "", shown,
			checkoutAttemptKey("erase-checkout-"+suffix),
		)
		checkoutDone <- placeErr
	}()
	waitForAccountLock(t, checkoutName, checkoutDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release erasure: %v", err)
	}
	if err := operationResult(t, eraseDone); err != nil {
		t.Fatalf("erase in checkout interleaving: %v", err)
	}
	if err := operationResult(t, checkoutDone); !errors.Is(err, cart.ErrNotFound) {
		t.Fatalf("checkout after erasure = %v, want cart.ErrNotFound", err)
	}

	var users, orders, privateRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, userID).
		Scan(&users); err != nil {
		t.Fatalf("count erased user: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE user_id = $1`, userID).
		Scan(&orders); err != nil {
		t.Fatalf("count concurrent checkout orders: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_private_data WHERE lower(email) = lower($1)`, u.Email).
		Scan(&privateRows); err != nil {
		t.Fatalf("count PII written after erasure: %v", err)
	}
	if users != 0 || orders != 0 || privateRows != 0 {
		t.Errorf("erasure survivors user/order/private = %d/%d/%d, want 0/0/0",
			users, orders, privateRows)
	}
}

// TestConcurrentAdminErasureKeepsOneAdmin holds erase_user's global decision
// lock so both requests are known to have started. After release, one may erase
// itself; the other must count the committed survivor state and receive the
// named last-admin refusal rather than letting both snapshots observe two.
func TestConcurrentAdminErasureKeepsOneAdmin(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	first := register(t, s, "erase-admin-a-"+uuid.NewString()+"@example.com")
	second := register(t, s, "erase-admin-b-"+uuid.NewString()+"@example.com")
	adminIDs := []uuid.UUID{uuid.MustParse(first.ID), uuid.MustParse(second.ID)}
	t.Cleanup(func() {
		// Keep a suite sentinel before removing the surviving fixture: the schema
		// invariant deliberately gives even the owner no transition back to zero.
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, `
			INSERT INTO users (email, role, full_name)
			VALUES ('account-suite-admin@goen.invalid', 'admin', 'Account suite sentinel')
			ON CONFLICT (lower(email)) DO UPDATE SET role = 'admin'`)
		_, _ = pool.Exec(cleanupCtx,
			`UPDATE users SET role = 'customer' WHERE id = ANY($1::uuid[])`, adminIDs)
		_, _ = pool.Exec(cleanupCtx,
			`DELETE FROM users WHERE id = ANY($1::uuid[])`, adminIDs)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = ANY($1::uuid[])`,
		adminIDs); err != nil {
		t.Fatalf("promote erasure-race admins: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE users SET role = 'customer'
		WHERE role = 'admin' AND id <> ALL($1::uuid[])`, adminIDs); err != nil {
		t.Fatalf("leave exactly the erasure-race admins: %v", err)
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&before); err != nil {
		t.Fatalf("count admins before erasure race: %v", err)
	}
	if before != 2 {
		t.Fatalf("erasure-race fixture has %d admins, want exactly 2", before)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin admin-erasure blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT lock_admin_roster()`); err != nil {
		t.Fatalf("lock admin-erasure decision: %v", err)
	}

	suffix := uuid.NewString()[:8]
	firstName, secondName := "erase-admin-a-"+suffix, "erase-admin-b-"+suffix
	firstStore := account.NewStore(accountStorePool(t, firstName))
	secondStore := account.NewStore(accountStorePool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- firstStore.Erase(ctx, first.ID) }()
	go func() { secondDone <- secondStore.Erase(ctx, second.ID) }()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release admin-erasure decision: %v", err)
	}
	firstErr, secondErr := operationResult(t, firstDone), operationResult(t, secondDone)
	refused := func(err error) bool {
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		return ok && pgErr.ConstraintName == "erase_user_keeps_one_admin"
	}
	oneSucceededAndOneWasRefused := firstErr == nil && refused(secondErr) ||
		secondErr == nil && refused(firstErr)
	if !oneSucceededAndOneWasRefused {
		t.Fatalf("concurrent admin erasures = %v / %v, want one success and one named refusal",
			firstErr, secondErr)
	}

	var admins int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
		t.Fatalf("count admins after erasure race: %v", err)
	}
	if admins != 1 {
		t.Errorf("concurrent erasure left %d admins, want 1", admins)
	}
}

// TestResetIssueAndErasureCannotSplitTokenFromMessage pauses reset issuance at
// its outbox insert, after it has locked the account and inserted the token in
// the same transaction. Erasure must wait, then purge both committed records;
// if erasure wins before the account lock, reset instead becomes a quiet no-op.
func TestResetIssueAndErasureCannotSplitTokenFromMessage(t *testing.T) {
	ctx := t.Context()
	email := "reset-erase-" + uuid.NewString() + "@example.com"
	u := register(t, account.NewStore(pool), email)

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_reset_outbox_barrier_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_reset_outbox_trigger_" + suffix}.Sanitize()
	const barrierKey int64 = 8_112_233_445_566_778
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.topic = 'account.password_reset' THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN NEW;
		END
		$body$`, functionName, barrierKey)); err != nil {
		t.Fatalf("create reset outbox barrier: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT ON outbox_messages
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create reset outbox trigger: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON outbox_messages`, triggerName))
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reset outbox blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); err != nil {
		t.Fatalf("lock reset outbox barrier: %v", err)
	}

	resetName, eraseName := "reset-issue-"+suffix[:8], "reset-erase-"+suffix[:8]
	resetStore := account.NewStore(accountStorePool(t, resetName))
	eraseStore := account.NewStore(accountStorePool(t, eraseName))
	resetDone, eraseDone := make(chan error, 1), make(chan error, 1)
	go func() { resetDone <- account.BeginReset(context.WithoutCancel(ctx), resetStore, email) }()
	waitForAccountLock(t, resetName, resetDone)
	go func() { eraseDone <- eraseStore.Erase(context.WithoutCancel(ctx), u.ID) }()
	waitForAccountLock(t, eraseName, eraseDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release reset outbox barrier: %v", err)
	}
	if err := operationResult(t, resetDone); err != nil {
		t.Fatalf("issue reset: %v", err)
	}
	if err := operationResult(t, eraseDone); err != nil {
		t.Fatalf("erase reset account: %v", err)
	}

	var users, tokens, messages int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`,
		uuid.MustParse(u.ID)).Scan(&users); err != nil {
		t.Fatalf("count reset user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM password_reset_tokens WHERE user_id = $1`,
		uuid.MustParse(u.ID)).Scan(&tokens); err != nil {
		t.Fatalf("count reset tokens: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)`,
		outbox.TopicPasswordReset, email).Scan(&messages); err != nil {
		t.Fatalf("count reset messages: %v", err)
	}
	if users != 0 || tokens != 0 || messages != 0 {
		t.Errorf("reset/erase survivors user/token/message = %d/%d/%d, want 0/0/0",
			users, tokens, messages)
	}
}

// TestResetCompletionAndErasureUseUserBeforeToken pauses the token spend after
// completion has locked the account. Erasure must wait on that account instead
// of holding it while completion holds the token, the former ABBA cycle.
func TestResetCompletionAndErasureUseUserBeforeToken(t *testing.T) {
	ctx := t.Context()
	email := "complete-erase-" + uuid.NewString() + "@example.com"
	u := register(t, account.NewStore(pool), email)
	token := beginReset(t, account.NewStore(pool), email)
	digest := sha256.Sum256([]byte(token))

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_reset_spend_barrier_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_reset_spend_trigger_" + suffix}.Sanitize()
	const barrierKey int64 = 8_112_233_445_566_779
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF OLD.token_hash = decode('%x', 'hex') THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN NEW;
		END
		$body$`, functionName, digest, barrierKey)); err != nil {
		t.Fatalf("create reset spend barrier: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE UPDATE OF used_at ON password_reset_tokens
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create reset spend trigger: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON password_reset_tokens`, triggerName))
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reset spend blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); err != nil {
		t.Fatalf("lock reset spend barrier: %v", err)
	}

	completeName, eraseName := "reset-complete-"+suffix[:8], "complete-erase-"+suffix[:8]
	completeStore := account.NewStore(accountStorePool(t, completeName))
	eraseStore := account.NewStore(accountStorePool(t, eraseName))
	completeDone, eraseDone := make(chan error, 1), make(chan error, 1)
	go func() {
		completeDone <- completeStore.CompleteReset(
			context.WithoutCancel(ctx), token, "completed before erasure")
	}()
	waitForAccountLock(t, completeName, completeDone)
	go func() { eraseDone <- eraseStore.Erase(context.WithoutCancel(ctx), u.ID) }()
	waitForAccountLock(t, eraseName, eraseDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release reset spend barrier: %v", err)
	}
	if err := operationResult(t, completeDone); err != nil {
		t.Fatalf("complete reset: %v", err)
	}
	if err := operationResult(t, eraseDone); err != nil {
		t.Fatalf("erase reset account: %v", err)
	}

	var users, tokens int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`,
		uuid.MustParse(u.ID)).Scan(&users); err != nil {
		t.Fatalf("count reset user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM password_reset_tokens WHERE user_id = $1`,
		uuid.MustParse(u.ID)).Scan(&tokens); err != nil {
		t.Fatalf("count reset tokens: %v", err)
	}
	if users != 0 || tokens != 0 {
		t.Errorf("complete/erase survivors user/token = %d/%d, want 0/0", users, tokens)
	}
}

func TestAResetTokenIsSpentExactlyOnce(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	register(t, s, "spendonce@example.com")

	token := beginReset(t, s, "spendonce@example.com")

	const racers = 8
	start := make(chan struct{})
	results := make(chan error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			<-start
			// A different password per racer, so the survivor is identifiable.
			results <- s.CompleteReset(ctx, token, "racer password number "+strconv.Itoa(i))
		})
	}
	close(start)
	wg.Wait()
	close(results)

	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, account.ErrResetInvalid):
		default:
			t.Errorf("a racer failed for the wrong reason: %v", err)
		}
	}
	if won != 1 {
		t.Errorf("the token was spent %d times, want exactly 1", won)
	}
}

func TestAnExpiredResetTokenIsRefused(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	register(t, s, "expiredreset@example.com")

	token := beginReset(t, s, "expiredreset@example.com")
	// created_at moves with it: password_reset_tokens_expiry_after_creation
	// refuses a row whose window closed before it opened.
	digest := sha256.Sum256([]byte(token))
	tag, err := pool.Exec(ctx,
		`UPDATE password_reset_tokens
		 SET created_at = now() - interval '2 hours',
		     expires_at = now() - interval '1 second'
		 WHERE token_hash = $1`, digest[:])
	if err != nil {
		t.Fatalf("age the token: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("aged %d rows, want 1 — the token was not stored where this test looks",
			tag.RowsAffected())
	}

	// Distinct from the password register() sets, or the second assertion proves nothing.
	const attempted = "the password an expired link tried to set"
	if err := s.CompleteReset(ctx, token, attempted); !errors.Is(err, account.ErrResetInvalid) {
		t.Errorf("an expired token was accepted: %v", err)
	}
	if _, err := s.Authenticate(ctx, "expiredreset@example.com", attempted); err == nil {
		t.Error("the password changed anyway")
	}
}

func TestAResetInvalidatesSiblingTokensAndSessions(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "siblings@example.com")

	stolen, err := s.StartSession(ctx, u.ID, "thief", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	first := beginReset(t, s, "siblings@example.com")
	second := beginReset(t, s, "siblings@example.com")
	if first == second {
		t.Fatal("two requests produced the same token")
	}

	if err := s.CompleteReset(ctx, second, "the newly chosen password"); err != nil {
		t.Fatalf("complete reset: %v", err)
	}

	if err := s.CompleteReset(ctx, first, "yet another password"); !errors.Is(err, account.ErrResetInvalid) {
		t.Errorf("the older link still works: %v", err)
	}
	if _, err := s.SessionUser(ctx, stolen); !errors.Is(err, account.ErrNotFound) {
		t.Error("a session survived the reset that was meant to end it")
	}
	if _, err := s.Authenticate(ctx, "siblings@example.com", "the newly chosen password"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

// TestConcurrentSiblingResetsSerializeOnTheAccount holds the user row until
// both reset requests have reached it. Only one token may change the password;
// that winner invalidates the sibling before the second request can spend it.
func TestConcurrentSiblingResetsSerializeOnTheAccount(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "sibling-race-"+uuid.NewString()+"@example.com")
	first := beginReset(t, s, u.Email)
	second := beginReset(t, s, u.Email)

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reset blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx,
		`SELECT id FROM users WHERE id = $1 FOR UPDATE`, uuid.MustParse(u.ID)); err != nil {
		t.Fatalf("lock reset account: %v", err)
	}

	suffix := uuid.NewString()[:8]
	firstName, secondName := "sibling-reset-a-"+suffix, "sibling-reset-b-"+suffix
	firstStore := account.NewStore(accountStorePool(t, firstName))
	secondStore := account.NewStore(accountStorePool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	const firstPassword = "first sibling reset password"
	const secondPassword = "second sibling reset password"
	go func() { firstDone <- firstStore.CompleteReset(context.WithoutCancel(ctx), first, firstPassword) }()
	go func() { secondDone <- secondStore.CompleteReset(context.WithoutCancel(ctx), second, secondPassword) }()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release reset account: %v", err)
	}
	firstErr, secondErr := operationResult(t, firstDone), operationResult(t, secondDone)
	oneSucceededAndOneWasInvalid := firstErr == nil && errors.Is(secondErr, account.ErrResetInvalid) ||
		secondErr == nil && errors.Is(firstErr, account.ErrResetInvalid)
	if !oneSucceededAndOneWasInvalid {
		t.Fatalf("sibling resets = %v / %v, want one success and one invalid token",
			firstErr, secondErr)
	}
	winnerPassword := firstPassword
	if secondErr == nil {
		winnerPassword = secondPassword
	}
	if _, err := s.Authenticate(ctx, u.Email, winnerPassword); err != nil {
		t.Errorf("winning reset password does not authenticate: %v", err)
	}
}

func TestBeginResetSaysNothingAboutWhoHasAnAccount(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	register(t, s, "real@example.com")

	realToken := beginReset(t, s, "real@example.com")
	if realToken == "" {
		t.Fatal("a real address did not queue a reset token")
	}

	for _, address := range []string{"nobody@example.com", "not an address", ""} {
		if err := account.BeginReset(ctx, s, address); err != nil {
			t.Errorf("BeginReset(%q) errored where it must stay quiet: %v", address, err)
		}
		var queued int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM outbox_messages
			WHERE topic = $1 AND lower(payload->>'email') = lower($2)`,
			outbox.TopicPasswordReset, address).Scan(&queued); err != nil {
			t.Fatalf("count reset messages for %q: %v", address, err)
		}
		if queued != 0 {
			t.Errorf("BeginReset(%q) queued %d messages for an unknown address", address, queued)
		}
	}
}

func TestAResetTokenIsStoredHashed(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	register(t, s, "hashedreset@example.com")

	token := beginReset(t, s, "hashedreset@example.com")
	var stored []byte
	digest := sha256.Sum256([]byte(token))
	if err := pool.QueryRow(ctx,
		`SELECT token_hash FROM password_reset_tokens WHERE token_hash = $1`,
		digest[:]).Scan(&stored); err != nil {
		t.Fatalf("the digest is not what is stored: %v", err)
	}
	var raw int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM password_reset_tokens
		 WHERE encode(token_hash, 'escape') = $1`, token).Scan(&raw); err != nil {
		t.Fatalf("scan raw count: %v", err)
	}
	if raw != 0 {
		t.Error("the raw token is in the table")
	}
}

func TestAWeakNewPasswordIsRefusedWithoutSpendingTheToken(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	register(t, s, "weakreset@example.com")

	token := beginReset(t, s, "weakreset@example.com")
	if err := s.CompleteReset(ctx, token, "short"); !errors.Is(err, account.ErrInvalidPassword) {
		t.Fatalf("a short password was not refused as such: %v", err)
	}
	if err := s.CompleteReset(ctx, token, "a sufficiently long password"); err != nil {
		t.Errorf("the token was burnt by the refused attempt: %v", err)
	}
}

func TestTheDefaultAddressCanBeMoved(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "movedefault@example.com")

	home := addAddress(t, s, u.ID, "家", true)
	work := addAddress(t, s, u.ID, "公司", false)

	if err := s.MakeDefaultAddress(ctx, u.ID, work); err != nil {
		t.Fatalf("move the default: %v", err)
	}

	got := defaultAddressID(t, u.ID)
	if got != work {
		t.Errorf("the default is %s, want the work address %s", got, work)
	}
	// Exactly one: setting before clearing trips addresses_one_default_per_user,
	// and clearing without setting leaves none.
	if n := countDefaults(t, u.ID); n != 1 {
		t.Errorf("the account has %d default addresses, want 1", n)
	}

	// And back again, so this is about moving rather than about the first move.
	if err := s.MakeDefaultAddress(ctx, u.ID, home); err != nil {
		t.Fatalf("move it back: %v", err)
	}
	if got := defaultAddressID(t, u.ID); got != home {
		t.Errorf("the default is %s, want the home address %s", got, home)
	}
}

func TestMovingTheDefaultToSomebodyElsesAddressChangesNothing(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)

	mine := register(t, s, "mineaddr@example.com")
	theirs := register(t, s, "theirsaddr@example.com")
	myHome := addAddress(t, s, mine.ID, "家", true)
	theirHome := addAddress(t, s, theirs.ID, "他家", true)

	if err := s.MakeDefaultAddress(ctx, mine.ID, theirHome); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("setting a stranger's address as my default answered %v, want ErrNotFound", err)
	}
	// Mine is untouched: without the rollback the clear would have left me none.
	if got := defaultAddressID(t, mine.ID); got != myHome {
		t.Errorf("my default became %s, want %s", got, myHome)
	}
	if got := defaultAddressID(t, theirs.ID); got != theirHome {
		t.Errorf("their default became %s, want %s", got, theirHome)
	}
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

func TestExpiredSessionsArePruned(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "pruned@example.com")

	live, err := s.StartSession(ctx, u.ID, "live", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	dead, err := s.StartSession(ctx, u.ID, "dead", "")
	if err != nil {
		t.Fatalf("start second session: %v", err)
	}
	// created_at moves with it: sessions_expiry_after_creation refuses a row
	// whose window closed before it opened.
	tag, err := pool.Exec(ctx, `
		UPDATE sessions
		SET created_at = now() - interval '2 hours',
		    expires_at = now() - interval '1 second'
		WHERE token_hash = sha256($1::bytea)`, dead)
	if err != nil {
		t.Fatalf("age the session: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("aged %d rows, want 1 — the session is not where this test looks",
			tag.RowsAffected())
	}

	if err := s.SweepSessions(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if n := sessionRows(t, u.ID); n != 1 {
		t.Errorf("%d session rows survive, want 1 — the expired one was not deleted", n)
	}
	// A pruner that deleted everything would pass a count-only assertion.
	if _, err := s.SessionUser(ctx, live); err != nil {
		t.Errorf("the live session was pruned too: %v", err)
	}
}

func sessionRows(t *testing.T, userID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM sessions WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestATierIsDerivedFromSpendAndNotStored(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "tiered-account@example.com")

	before, err := s.Overview(ctx, u)
	if err != nil {
		t.Fatalf("read the account: %v", err)
	}
	if before.Standing.HasTier() {
		t.Fatalf("a customer who has bought nothing is in %q", before.Standing.TierName)
	}
	if !before.Standing.HasNext() {
		t.Fatal("no next tier is offered — the seed has no bands and this proves nothing")
	}

	orderID := committedOrderFor(t, u.ID, 2000000) // NT$20,000

	after, err := s.Overview(ctx, u)
	if err != nil {
		t.Fatalf("re-read the account: %v", err)
	}
	if !after.Standing.HasTier() {
		t.Fatalf("NT$20,000 of committed spend earned no tier (spend read as %d)",
			after.Standing.SpendCents)
	}
	if after.Standing.MultiplierBP <= 10000 {
		t.Errorf("the tier earns %d bp, want more than the base rate", after.Standing.MultiplierBP)
	}

	if _, cancelErr := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`, orderID); cancelErr != nil {
		t.Fatalf("cancel: %v", cancelErr)
	}
	gone, err := s.Overview(ctx, u)
	if err != nil {
		t.Fatalf("read the account after cancelling: %v", err)
	}
	if gone.Standing.HasTier() || gone.Standing.SpendCents != 0 {
		t.Errorf("a cancelled order still counts: tier %q, spend %d",
			gone.Standing.TierName, gone.Standing.SpendCents)
	}
}

// committedOrderFor writes a paid order for one customer and returns its id.
func committedOrderFor(t *testing.T, userID string, cents int64) uuid.UUID {
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
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`, userID).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'TIER-SKU', '測試商品', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'tiered-account@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`,
		orderID, "cs_tier_"+number, cents); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`,
		"cs_tier_"+number, cents); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID
}

func TestTheSweepDropsDeadResetTokensAndKeepsLiveOnes(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "sweep-"+uuid.NewString()+"@goen.invalid")
	userID := u.ID

	// Four tokens: spent long ago, expired long ago, spent just now, and live.
	plant := func(digest string, used bool, expires, created string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO password_reset_tokens (token_hash, user_id, expires_at, used_at, created_at)
			VALUES (sha256($1::bytea), $2, now() + $3::interval,
			        CASE WHEN $4 THEN now() ELSE NULL END, now() + $5::interval)`,
			digest, userID, expires, used, created); err != nil {
			t.Fatalf("plant %s: %v", digest, err)
		}
	}
	plant("spent-old", true, "1 hour", "-30 days")
	plant("expired-old", false, "-29 days", "-30 days")
	plant("spent-today", true, "1 hour", "0 days")
	plant("live", false, "1 hour", "0 days")

	if err := s.SweepSessions(ctx); err != nil {
		t.Fatalf("SweepSessions: %v", err)
	}

	for digest, want := range map[string]bool{
		"spent-old": false, "expired-old": false,
		// Inside the grace window: still answerable by support.
		"spent-today": true,
		"live":        true,
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM password_reset_tokens
			               WHERE token_hash = sha256($1::bytea))`, digest).Scan(&exists); err != nil {
			t.Fatalf("read %s: %v", digest, err)
		}
		if exists != want {
			t.Errorf("%s: exists = %v, want %v", digest, exists, want)
		}
	}
}

func TestAnAddressIsProvedByFollowingTheLink(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "verify-"+uuid.NewString()+"@goen.invalid")

	state, err := s.EmailVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("EmailVerification: %v", err)
	}
	if state.Verified {
		t.Error("a freshly registered address reads as proved; nothing has proved it")
	}

	token := requestVerification(t, s, u.ID, u.Email)
	if _, confirmErr := s.ConfirmVerification(ctx, token); confirmErr != nil {
		t.Fatalf("ConfirmVerification: %v", confirmErr)
	}

	state, err = s.EmailVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("EmailVerification: %v", err)
	}
	if !state.Verified {
		t.Error("the address is not proved after its link was followed")
	}
	if _, err := s.ConfirmVerification(ctx, token); !errors.Is(err, account.ErrVerifyInvalid) {
		t.Errorf("re-using the link = %v, want ErrVerifyInvalid", err)
	}
}

func TestAChangeTakesEffectOnlyWhenConfirmed(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	old := "before-" + uuid.NewString() + "@goen.invalid"
	u := register(t, s, old)
	next := "after-" + uuid.NewString() + "@goen.invalid"

	token := requestVerification(t, s, u.ID, next)

	if got := emailOf(t, u.ID); got != old {
		t.Errorf("the account moved to %q before the link was followed", got)
	}
	state, err := s.EmailVerification(ctx, u.ID)
	if err != nil {
		t.Fatalf("EmailVerification: %v", err)
	}
	if state.PendingEmail != next {
		t.Errorf("pending address is %q, want %q", state.PendingEmail, next)
	}

	if _, err := s.ConfirmVerification(ctx, token); err != nil {
		t.Fatalf("ConfirmVerification: %v", err)
	}
	if got := emailOf(t, u.ID); got != next {
		t.Errorf("the account is at %q after confirming, want %q", got, next)
	}
}

// TestChangingEmailInvalidatesResetLinksSentToTheOldMailbox protects the
// account after mailbox ownership moves: a link already delivered to the old
// address must not remain password authority for the new identity.
func TestChangingEmailInvalidatesResetLinksSentToTheOldMailbox(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	old := "old-reset-" + uuid.NewString() + "@goen.invalid"
	u := register(t, s, old)
	oldMailboxToken := beginReset(t, s, old)
	newAddress := "new-reset-" + uuid.NewString() + "@goen.invalid"

	verification := requestVerification(t, s, u.ID, newAddress)
	if _, err := s.ConfirmVerification(ctx, verification); err != nil {
		t.Fatalf("confirm address change: %v", err)
	}
	if err := s.CompleteReset(ctx, oldMailboxToken, "password chosen by old mailbox"); !errors.Is(err, account.ErrResetInvalid) {
		t.Fatalf("old-mailbox reset after address change = %v, want ErrResetInvalid", err)
	}
	var oldMailboxMessages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = $1
		  AND lower(coalesce(payload ->> 'email', payload ->> 'Email', '')) = lower($2)`,
		outbox.TopicPasswordReset, old).Scan(&oldMailboxMessages); err != nil {
		t.Fatalf("count old-mailbox reset messages: %v", err)
	}
	if oldMailboxMessages != 0 {
		t.Errorf("%d reset messages still retain or target the old mailbox", oldMailboxMessages)
	}
	if _, err := s.Authenticate(ctx, newAddress, "a sufficiently long password"); err != nil {
		t.Errorf("address change disturbed the account's existing password: %v", err)
	}
}

func TestErasurePurgesOnlyItsPendingVerificationMessage(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "verification-erase-old-"+uuid.NewString()+"@goen.invalid")
	pending := "verification-erase-new-" + uuid.NewString() + "@goen.invalid"
	if err := account.RequestVerification(ctx, s, u.ID, pending); err != nil {
		t.Fatalf("request address change: %v", err)
	}
	verificationDedupe := func(userID string) string {
		t.Helper()
		var key string
		if err := pool.QueryRow(ctx, `
			SELECT 'verify:' || encode(digest, 'hex')
			FROM email_verifications WHERE user_id = $1`, userID).Scan(&key); err != nil {
			t.Fatalf("read verification message identity: %v", err)
		}
		return key
	}
	erasedDedupe := verificationDedupe(u.ID)

	// A pending address has not been proved. Another account may have asked to
	// prove the same mailbox, and unrelated transactional mail may already target
	// it; neither belongs to the account being erased.
	other := register(t, s, "verification-erase-other-"+uuid.NewString()+"@goen.invalid")
	if err := account.RequestVerification(ctx, s, other.ID, pending); err != nil {
		t.Fatalf("request the same pending address for another account: %v", err)
	}
	otherDedupe := verificationDedupe(other.ID)
	transactionalDedupe := "verification-erase-victim-mail:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload)
		VALUES ($1, $2, jsonb_build_object('email', $3::text, 'order_number', 'G-VICTIM'))`,
		outbox.TopicOrderPlaced, transactionalDedupe, pending); err != nil {
		t.Fatalf("seed victim transactional mail: %v", err)
	}
	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase account: %v", err)
	}

	var erasedMessages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE dedupe_key = $1`, erasedDedupe).Scan(&erasedMessages); err != nil {
		t.Fatalf("count erased account's verification messages: %v", err)
	}
	if erasedMessages != 0 {
		t.Errorf("%d messages still carry the erased account's verification identity", erasedMessages)
	}

	var victimMessages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE dedupe_key IN ($1, $2)`, otherDedupe, transactionalDedupe).Scan(&victimMessages); err != nil {
		t.Fatalf("count other owner's pending-address messages: %v", err)
	}
	if victimMessages != 2 {
		t.Errorf("erasure retained %d/2 messages that target an unproved victim mailbox", victimMessages)
	}
}

// TestErasureMatchesOutboxRecipientsExactly protects both halves of the
// recipient predicate. Percent and underscore are legal mailbox characters but
// SQL pattern wildcards, and an address in a non-recipient field does not make
// that message the erased customer's mail.
func TestErasureMatchesOutboxRecipientsExactly(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	current := "erase_%" + suffix + "@goen.invalid"
	currentLookalike := "erase-looks-like-" + suffix + "@goen.invalid"
	pending := "pending_%" + suffix + "@goen.invalid"
	pendingLookalike := "pending-looks-like-" + suffix + "@goen.invalid"
	u := register(t, s, current)
	currentToken := requestVerification(t, s, u.ID, current)
	if _, err := s.ConfirmVerification(ctx, currentToken); err != nil {
		t.Fatalf("prove patterned current address: %v", err)
	}
	if err := account.RequestVerification(ctx, s, u.ID, pending); err != nil {
		t.Fatalf("request patterned pending address: %v", err)
	}

	dedupePrefix := "erase-recipient-exact:" + suffix + ":"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx),
			`DELETE FROM outbox_messages WHERE dedupe_key LIKE $1`, dedupePrefix+"%")
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload) VALUES
			('test.erase-current-legacy', $1,
			 jsonb_build_object('Email', $2::text, 'token', 'legacy')),
			('test.erase-current-lookalike', $3,
			 jsonb_build_object('email', $4::text, 'name', $2::text)),
			('test.erase-pending-lookalike', $5,
			 jsonb_build_object('email', $6::text))`,
		dedupePrefix+"current", current,
		dedupePrefix+"current-lookalike", currentLookalike,
		dedupePrefix+"pending-lookalike", pendingLookalike,
	); err != nil {
		t.Fatalf("seed exact-recipient messages: %v", err)
	}

	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase patterned address: %v", err)
	}

	var erasedRecipients int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE lower(coalesce(payload ->> 'email', payload ->> 'Email', '')) =
		      ANY(ARRAY[lower($1), lower($2)])`, current, pending).Scan(&erasedRecipients); err != nil {
		t.Fatalf("count erased recipients: %v", err)
	}
	if erasedRecipients != 0 {
		t.Errorf("%d outbox messages still name an erased current or pending recipient", erasedRecipients)
	}

	var lookalikes int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE dedupe_key IN ($1, $2)`,
		dedupePrefix+"current-lookalike", dedupePrefix+"pending-lookalike").Scan(&lookalikes); err != nil {
		t.Fatalf("count lookalike recipients: %v", err)
	}
	if lookalikes != 2 {
		t.Errorf("erasure retained %d/2 lookalike-recipient messages; %% and _ must be literal", lookalikes)
	}
}

// TestVerificationAndErasureUseUserBeforeToken pauses token deletion after
// confirmation has locked the user. Erasure must wait on the user instead of
// holding it while confirmation holds the verification row.
func TestVerificationAndErasureUseUserBeforeToken(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "verify-race-old-"+uuid.NewString()+"@goen.invalid")
	next := "verify-race-new-" + uuid.NewString() + "@goen.invalid"
	token := requestVerification(t, s, u.ID, next)
	digest := account.HashToken(token)

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_verification_spend_barrier_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_verification_spend_trigger_" + suffix}.Sanitize()
	const barrierKey int64 = 8_112_233_445_566_780
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF OLD.digest = decode('%x', 'hex') THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN OLD;
		END
		$body$`, functionName, digest, barrierKey)); err != nil {
		t.Fatalf("create verification spend barrier: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE DELETE ON email_verifications
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create verification spend trigger: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON email_verifications`, triggerName))
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin verification spend blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); err != nil {
		t.Fatalf("lock verification spend barrier: %v", err)
	}

	confirmName, eraseName := "verify-confirm-"+suffix[:8], "verify-erase-"+suffix[:8]
	confirmStore := account.NewStore(accountStorePool(t, confirmName))
	eraseStore := account.NewStore(accountStorePool(t, eraseName))
	confirmDone, eraseDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, confirmErr := confirmStore.ConfirmVerification(context.WithoutCancel(ctx), token)
		confirmDone <- confirmErr
	}()
	waitForAccountLock(t, confirmName, confirmDone)
	go func() { eraseDone <- eraseStore.Erase(context.WithoutCancel(ctx), u.ID) }()
	waitForAccountLock(t, eraseName, eraseDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release verification spend barrier: %v", err)
	}
	if err := operationResult(t, confirmDone); err != nil {
		t.Fatalf("confirm verification: %v", err)
	}
	if err := operationResult(t, eraseDone); err != nil {
		t.Fatalf("erase verified account: %v", err)
	}

	var users, verifications int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`,
		uuid.MustParse(u.ID)).Scan(&users); err != nil {
		t.Fatalf("count verification user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM email_verifications WHERE user_id = $1`,
		uuid.MustParse(u.ID)).Scan(&verifications); err != nil {
		t.Fatalf("count verification rows: %v", err)
	}
	if users != 0 || verifications != 0 {
		t.Errorf("verification/erase survivors user/token = %d/%d, want 0/0",
			users, verifications)
	}
}

// users_email_key is the real guard: this requests first, then lets somebody
// else take the address before the confirmation.
func TestAChangeToATakenAddressIsRefused(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	mine := register(t, s, "mine-"+uuid.NewString()+"@goen.invalid")
	wanted := "contested-" + uuid.NewString() + "@goen.invalid"

	token := requestVerification(t, s, mine.ID, wanted)
	register(t, s, wanted)

	if _, err := s.ConfirmVerification(ctx, token); !errors.Is(err, account.ErrEmailTaken) {
		t.Errorf("confirming a taken address = %v, want ErrEmailTaken", err)
	}
	if got := emailOf(t, mine.ID); got != mine.Email {
		t.Errorf("my address became %q after a refused change, want %q", got, mine.Email)
	}
}

func TestAskingAgainLeavesOneLiveLink(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "again-"+uuid.NewString()+"@goen.invalid")

	first := requestVerification(t, s, u.ID, u.Email)
	second := requestVerification(t, s, u.ID, u.Email)

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM email_verifications WHERE user_id = $1`, u.ID).Scan(&rows); err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d outstanding requests after asking twice, want 1", rows)
	}
	var messages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)`,
		outbox.TopicEmailVerify, u.Email).Scan(&messages); err != nil {
		t.Fatalf("count queued verification links: %v", err)
	}
	if messages != 1 {
		t.Errorf("%d verification messages after asking twice, want only the newest", messages)
	}
	if _, err := s.ConfirmVerification(ctx, first); !errors.Is(err, account.ErrVerifyInvalid) {
		t.Errorf("the earlier link still works: %v", err)
	}
	if _, err := s.ConfirmVerification(ctx, second); err != nil {
		t.Errorf("the newest link does not work: %v", err)
	}
}

func TestAnExpiredVerificationIsRefused(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "expired-"+uuid.NewString()+"@goen.invalid")

	token := requestVerification(t, s, u.ID, u.Email)
	if _, err := pool.Exec(ctx, `
		UPDATE email_verifications
		SET created_at = now() - interval '50 hours', expires_at = now() - interval '2 hours'
		WHERE user_id = $1`, u.ID); err != nil {
		t.Fatalf("age the request: %v", err)
	}

	if _, err := s.ConfirmVerification(ctx, token); !errors.Is(err, account.ErrVerifyInvalid) {
		t.Errorf("an expired link = %v, want ErrVerifyInvalid", err)
	}
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

func TestRegisteringAsksForTheAddressToBeProved(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	addr := "asked-" + uuid.NewString() + "@goen.invalid"

	u := register(t, s, addr)

	// register() goes through the store, so ask the way the handler does.
	if err := account.RequestVerification(ctx, s, u.ID, u.Email); err != nil {
		t.Fatalf("RequestVerification: %v", err)
	}
	var messages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'account.email_verify' AND payload->>'email' = $1`,
		addr).Scan(&messages); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messages != 1 {
		t.Errorf("%d verification messages for a new account, want 1", messages)
	}
}

// Driven through Overview, which is the query the account page actually reads.
func TestTheMembershipBandReadsInTheVisitorsLanguage(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)

	// No spend at all, so the NEXT band is the lowest one.
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('band-'||gen_random_uuid()||'@goen.invalid', 'customer', '等級測試')
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("create customer: %v", err)
	}

	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
		absent string
	}{
		{name: "Chinese", locale: i18n.ZhHant, want: "銀卡會員", absent: "Silver"},
		{name: "English", locale: i18n.En, want: "Silver", absent: "銀卡會員"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			view, err := s.Overview(i18n.WithLocale(ctx, tt.locale),
				account.User{ID: id, Role: "customer"})
			if err != nil {
				t.Fatalf("Overview: %v", err)
			}
			if view.Standing.NextName != tt.want {
				t.Errorf("the next band reads %q, want %q",
					view.Standing.NextName, tt.want)
			}
			if view.Standing.NextName == tt.absent {
				t.Errorf("the next band reads %q, the other language",
					view.Standing.NextName)
			}
		})
	}
}

func TestGoogleSignInCreatesAnAccountWithNoPassword(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	email := "oauth-new-" + uuid.NewString()[:8] + "@example.com"

	u, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if u.Email != email {
		t.Errorf("signed in as %q, want %q", u.Email, email)
	}

	var hash *string
	var verified *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT password_hash, email_verified_at FROM users WHERE id = $1`,
		u.ID).Scan(&hash, &verified); err != nil {
		t.Fatalf("read the new account: %v", err)
	}
	if hash != nil {
		t.Errorf("an account created from an identity has a password hash: %q", *hash)
	}
	if verified == nil {
		t.Error("google proved the address and goen did not record it as proved")
	}

	if _, authErr := s.Authenticate(ctx, email, ""); !errors.Is(authErr, account.ErrBadCredentials) {
		t.Errorf("signing in with no password gave %v, want ErrBadCredentials", authErr)
	}
}

func TestGoogleSignInIsIdempotentOnTheSubject(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	subject := "google-sub-" + uuid.NewString()[:8]

	first, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: subject, Email: "oauth-same-" + uuid.NewString()[:8] + "@example.com",
		EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}

	second, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: subject, Email: "changed-" + uuid.NewString()[:8] + "@example.com",
		EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("the same Google account signed into %s and then %s — a changed "+
			"address stranded the customer with their orders behind them",
			first.ID, second.ID)
	}
}

// TestConcurrentGoogleLinkingReturnsTheDurableExistingOwner makes both
// callbacks reach the subject lock before either can choose an email account.
// Whichever verified account wins is acceptable; returning both is not.
func TestConcurrentGoogleLinkingReturnsTheDurableExistingOwner(t *testing.T) {
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	subject := "google-race-existing-" + suffix
	emailA := "google-race-a-" + suffix + "@example.com"
	emailB := "google-race-b-" + suffix + "@example.com"
	var idA, idB string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, email_verified_at) VALUES ($1, now()) RETURNING id`, emailA).
		Scan(&idA); err != nil {
		t.Fatalf("create first verified account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, email_verified_at) VALUES ($1, now()) RETURNING id`, emailB).
		Scan(&idB); err != nil {
		t.Fatalf("create second verified account: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin subject blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('google:' || $1::text, 0))`, subject); err != nil {
		t.Fatalf("lock subject: %v", err)
	}

	firstName, secondName := "google-existing-a-"+suffix, "google-existing-b-"+suffix
	firstStore := account.NewStore(accountStorePool(t, firstName))
	secondStore := account.NewStore(accountStorePool(t, secondName))
	var first, second account.User
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		var signInErr error
		first, signInErr = firstStore.SignInWithGoogle(context.WithoutCancel(ctx), account.Identity{
			Subject: subject, Email: emailA, EmailVerified: true,
		})
		firstDone <- signInErr
	}()
	go func() {
		var signInErr error
		second, signInErr = secondStore.SignInWithGoogle(context.WithoutCancel(ctx), account.Identity{
			Subject: subject, Email: emailB, EmailVerified: true,
		})
		secondDone <- signInErr
	}()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release subject: %v", err)
	}
	if err := operationResult(t, firstDone); err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if err := operationResult(t, secondDone); err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("one Google subject returned accounts %s and %s", first.ID, second.ID)
	}
	if first.ID != idA && first.ID != idB {
		t.Fatalf("subject owner %s is neither candidate account", first.ID)
	}
	var durable string
	if err := pool.QueryRow(ctx, `
		SELECT user_id::text FROM user_identities
		WHERE provider = 'google' AND provider_subject = $1`, subject).Scan(&durable); err != nil {
		t.Fatalf("read durable subject owner: %v", err)
	}
	if first.ID != durable {
		t.Errorf("both callbacks returned %s, durable subject owner is %s", first.ID, durable)
	}
}

// TestConcurrentGoogleSignUpCreatesOnlyTheSubjectOwner covers the create path:
// the losing callback must not commit and return a second, unlinked account.
func TestConcurrentGoogleSignUpCreatesOnlyTheSubjectOwner(t *testing.T) {
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	subject := "google-race-new-" + suffix
	emailA := "google-new-a-" + suffix + "@example.com"
	emailB := "google-new-b-" + suffix + "@example.com"

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin subject blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('google:' || $1::text, 0))`, subject); err != nil {
		t.Fatalf("lock subject: %v", err)
	}

	firstName, secondName := "google-new-a-"+suffix, "google-new-b-"+suffix
	firstStore := account.NewStore(accountStorePool(t, firstName))
	secondStore := account.NewStore(accountStorePool(t, secondName))
	var first, second account.User
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		var signInErr error
		first, signInErr = firstStore.SignInWithGoogle(context.WithoutCancel(ctx), account.Identity{
			Subject: subject, Email: emailA, EmailVerified: true,
		})
		firstDone <- signInErr
	}()
	go func() {
		var signInErr error
		second, signInErr = secondStore.SignInWithGoogle(context.WithoutCancel(ctx), account.Identity{
			Subject: subject, Email: emailB, EmailVerified: true,
		})
		secondDone <- signInErr
	}()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release subject: %v", err)
	}
	if err := operationResult(t, firstDone); err != nil {
		t.Fatalf("first sign-up: %v", err)
	}
	if err := operationResult(t, secondDone); err != nil {
		t.Fatalf("second sign-up: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("one new Google subject returned accounts %s and %s", first.ID, second.ID)
	}
	var accounts, identities int
	var durable string
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM users WHERE email IN ($1, $2)`, emailA, emailB).Scan(&accounts); err != nil {
		t.Fatalf("count candidate accounts: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM user_identities
		WHERE provider = 'google' AND provider_subject = $1`, subject).Scan(&identities); err != nil {
		t.Fatalf("count durable identity: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT user_id::text FROM user_identities
		WHERE provider = 'google' AND provider_subject = $1`, subject).Scan(&durable); err != nil {
		t.Fatalf("read durable identity owner: %v", err)
	}
	if accounts != 1 || identities != 1 {
		t.Errorf("concurrent sign-up left %d accounts and %d identity rows, want 1 and 1",
			accounts, identities)
	}
	if first.ID != durable {
		t.Errorf("callbacks returned %s, durable subject owner is %s", first.ID, durable)
	}
}

func TestGoogleWillNotLinkToAnUnverifiedAccount(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	email := "oauth-collide-" + uuid.NewString()[:8] + "@example.com"

	victim := register(t, s, email)

	_, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if !errors.Is(err, account.ErrOAuthCollision) {
		t.Fatalf("linking to an unverified account = %v, want ErrOAuthCollision", err)
	}

	var identities, accounts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_identities WHERE user_id = $1`, victim.ID).Scan(&identities); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identities != 0 {
		t.Errorf("%d identities linked to an unverified account, want 0", identities)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&accounts); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accounts != 1 {
		t.Errorf("%d accounts hold that address, want 1", accounts)
	}
}

func TestGoogleLinksToAVerifiedAccount(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	email := "oauth-verified-" + uuid.NewString()[:8] + "@example.com"

	u := register(t, s, email)
	if _, err := pool.Exec(ctx,
		`UPDATE users SET email_verified_at = now() WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("verify the address: %v", err)
	}

	got, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("signed into %s, want the existing account %s", got.ID, u.ID)
	}

	// The password still works: linking adds a way in rather than replacing one.
	if _, authErr := s.Authenticate(ctx, email, "a sufficiently long password"); authErr != nil {
		t.Errorf("the password stopped working after linking Google: %v", authErr)
	}
}

// email_verified is false for some Workspace configurations, where the domain
// administrator controls what the address says.
func TestGoogleRefusesAnAddressGoogleHasNotVerified(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	email := "oauth-unverified-" + uuid.NewString()[:8] + "@example.com"

	_, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: false, Name: "谷歌使用者",
	})
	if !errors.Is(err, account.ErrOAuthUnverified) {
		t.Fatalf("an unverified google address = %v, want ErrOAuthUnverified", err)
	}
	var accounts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, email).Scan(&accounts); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accounts != 0 {
		t.Errorf("%d accounts created from an unverified address, want 0", accounts)
	}
}

func TestUnlinkingTheOnlyWayInIsRefused(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	email := "oauth-only-" + uuid.NewString()[:8] + "@example.com"

	u, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}

	if unlinkErr := s.UnlinkGoogle(ctx, u); !errors.Is(unlinkErr, account.ErrLastSignInMethod) {
		t.Fatalf("unlinking the only way in = %v, want ErrLastSignInMethod", unlinkErr)
	}
	var identities int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_identities WHERE user_id = $1`, u.ID).Scan(&identities); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identities != 1 {
		t.Errorf("%d identities after a refused unlink, want 1", identities)
	}

	// The control: a function that refused everything would pass the assertion above.
	if _, err := pool.Exec(ctx, `
		UPDATE users SET password_hash = 'x' WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("set a password: %v", err)
	}
	if err := s.UnlinkGoogle(ctx, u); err != nil {
		t.Fatalf("unlinking with a password set: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_identities WHERE user_id = $1`, u.ID).Scan(&identities); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identities != 0 {
		t.Errorf("%d identities after unlinking, want 0", identities)
	}
}

// user_identities cascades on the user, so erase_user reaches it without naming it.
func TestErasingAnAccountTakesItsIdentities(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	email := "oauth-erase-" + uuid.NewString()[:8] + "@example.com"

	u, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("Erase: %v", err)
	}

	var identities int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_identities WHERE user_id = $1`, u.ID).Scan(&identities); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identities != 0 {
		t.Errorf("%d identities survived erasure — the same Google account could "+
			"sign back into an account that no longer exists", identities)
	}
}
