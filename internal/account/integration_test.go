//go:build integration

package account_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
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

// This is behaviour preservation rather than the timing lock: both account
// states already returned ErrBadCredentials, but now do so before any read.
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

func TestSessionExpiryIsInTheFuture(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "ttl@example.com")

	token, err := s.StartSession(ctx, u.ID, "agent", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	var expires time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM sessions WHERE token_hash = $1`,
		account.HashToken(token)).Scan(&expires); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	if !expires.After(time.Now().Add(24 * time.Hour)) {
		t.Errorf("session expires at %v, which is less than a day away", expires)
	}
}

// invoice_preferences is keyed by ORDER rather than by user, so nulling the
// account does not reach the invoice carrier that identifies a person.
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
		INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code)
		VALUES ($1, 'mobile_carrier', '/ABC+123')`, orderID); err != nil {
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
		{"the invoice carrier", `SELECT count(*) FROM invoice_preferences WHERE order_id = $1`, orderID},
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

func TestAResetTokenIsSpentExactlyOnce(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	register(t, s, "spendonce@example.com")

	token, _, found, err := s.BeginReset(ctx, "spendonce@example.com")
	if err != nil || !found {
		t.Fatalf("begin reset: %v (found=%v)", err, found)
	}

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

	token, _, _, err := s.BeginReset(ctx, "expiredreset@example.com")
	if err != nil {
		t.Fatalf("begin reset: %v", err)
	}
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

	// Distinct from the password register() sets, or the second assertion passes
	// for a reason that has nothing to do with the fix.
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
	first, _, _, err := s.BeginReset(ctx, "siblings@example.com")
	if err != nil {
		t.Fatalf("begin first reset: %v", err)
	}
	second, _, _, err := s.BeginReset(ctx, "siblings@example.com")
	if err != nil {
		t.Fatalf("begin second reset: %v", err)
	}
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

func TestBeginResetSaysNothingAboutWhoHasAnAccount(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	register(t, s, "real@example.com")

	realToken, sendTo, found, err := s.BeginReset(ctx, "real@example.com")
	if err != nil {
		t.Fatalf("begin reset for a real address: %v", err)
	}
	if !found || realToken == "" || sendTo != "real@example.com" {
		t.Fatalf("a real address produced found=%v tokenIssued=%v sendTo=%q", found, realToken != "", sendTo)
	}

	for _, address := range []string{"nobody@example.com", "not an address", ""} {
		token, to, found, err := s.BeginReset(ctx, address)
		if err != nil {
			t.Errorf("BeginReset(%q) errored where it must stay quiet: %v", address, err)
		}
		if found || token != "" || to != "" {
			t.Errorf("BeginReset(%q) leaked found=%v token=%q sendTo=%q", address, found, token, to)
		}
	}
}

func TestAResetTokenIsStoredHashed(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	register(t, s, "hashedreset@example.com")

	token, _, _, err := s.BeginReset(ctx, "hashedreset@example.com")
	if err != nil {
		t.Fatalf("begin reset: %v", err)
	}
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

	token, _, _, err := s.BeginReset(ctx, "weakreset@example.com")
	if err != nil {
		t.Fatalf("begin reset: %v", err)
	}
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

	token, err := s.RequestVerification(ctx, u.ID, u.Email)
	if err != nil {
		t.Fatalf("RequestVerification: %v", err)
	}
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

	token, err := s.RequestVerification(ctx, u.ID, next)
	if err != nil {
		t.Fatalf("RequestVerification: %v", err)
	}

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

// users_email_key is the real guard: this requests first, then lets somebody
// else take the address before the confirmation.
func TestAChangeToATakenAddressIsRefused(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	mine := register(t, s, "mine-"+uuid.NewString()+"@goen.invalid")
	wanted := "contested-" + uuid.NewString() + "@goen.invalid"

	token, err := s.RequestVerification(ctx, mine.ID, wanted)
	if err != nil {
		t.Fatalf("RequestVerification: %v", err)
	}
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

	first, err := s.RequestVerification(ctx, u.ID, u.Email)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	second, err := s.RequestVerification(ctx, u.ID, u.Email)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}

	var rows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM email_verifications WHERE user_id = $1`, u.ID).Scan(&rows); err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d outstanding requests after asking twice, want 1", rows)
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

	token, err := s.RequestVerification(ctx, u.ID, u.Email)
	if err != nil {
		t.Fatalf("RequestVerification: %v", err)
	}
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
	if _, err := s.RequestVerification(ctx, u.ID, u.Email); err != nil {
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

// Driven through Overview: asserting against localized_name directly stays green
// with the locale mutated out of the query the account page actually reads.
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

// Keyed on the SUBJECT, and the test changes the EMAIL to prove it.
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

// The control that proves the refusal above is about verification rather than
// about refusing every existing account.
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
