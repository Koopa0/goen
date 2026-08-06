//go:build integration

package account_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"os"
	"strconv"
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

// TestPasswordIsNeverStoredInTheClear is the property a leak turns on. The
// column must hold an argon2id hash and nothing resembling the password.
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

// TestAuthenticateDoesNotDistinguishUnknownFromWrong is the anti-enumeration
// rule: a wrong email and a wrong password must be the same answer, or the form
// tells an attacker which addresses have accounts.
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

// TestAuthenticateIsCaseInsensitiveOnEmail matches the unique index, which is
// on lower(email). Without this someone who registered as Ming@Example.com
// could not sign in as ming@example.com, and worse, could register twice.
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

// TestSessionsAreStoredHashed is the same rule as the cart's: a leaked sessions
// table must not hand over live sessions.
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

// TestExpiredSessionIsNobody pins that expiry is enforced by the query rather
// than by a sweeper. A session that has expired must be dead immediately, not
// whenever something next cleans up.
func TestExpiredSessionIsNobody(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "expired@example.com")

	token, err := s.StartSession(ctx, u.ID, "agent", "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	// Both timestamps move: sessions_expiry_after_creation requires expires_at
	// to be later than created_at, so an expired session is one whose whole
	// lifetime is in the past. Setting expires_at alone to "a second from
	// creation" leaves it in the future for the first second and the test would
	// pass or fail depending on how quickly it ran.
	if _, err := pool.Exec(ctx, `
		UPDATE sessions
		SET created_at = now() - interval '2 hours',
		    expires_at = now() - interval '1 hour'
		WHERE token_hash = $1`, account.HashToken(token)); err != nil {
		t.Fatalf("expire: %v", err)
	}
	// The row still exists — nothing has swept it — and must still be refused.
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

// TestChangingPasswordEndsEveryOtherSession is the point of a password change.
// One that leaves existing sessions alive has locked nobody out, so a stolen
// session survives the very action taken to stop it.
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

// TestOrdersAreScopedToTheirOwner is the access rule for the account pages. An
// order belonging to someone else must be indistinguishable from one that does
// not exist — the owner is part of the query, not a check afterwards.
func TestOrdersAreScopedToTheirOwner(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	mine := register(t, s, "mine@example.com")
	theirs := register(t, s, "theirs@example.com")

	number := placeOrderFor(t, theirs.ID)

	// Its owner sees it.
	if _, err := s.Order(ctx, theirs, number); err != nil {
		t.Fatalf("the owner cannot see their own order: %v", err)
	}
	// Nobody else does, and the answer is the same as for an order that does
	// not exist.
	_, otherErr := s.Order(ctx, mine, number)
	_, missingErr := s.Order(ctx, mine, "GO-000000-999999")
	if !errors.Is(otherErr, account.ErrNotFound) {
		t.Errorf("another customer's order gave %v, want ErrNotFound", otherErr)
	}
	if !errors.Is(missingErr, account.ErrNotFound) {
		t.Errorf("a nonexistent order gave %v, want ErrNotFound", missingErr)
	}

	// And it does not appear in the wrong account's history either.
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

// TestAdoptCartMergesRatherThanReplaces pins that signing in does not throw
// away either cart. Someone who added things while signed out has not agreed to
// lose what was already in their account, and the reverse is just as true.
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

	// The account already has a cart holding variant a.
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

	// The guest cart holds a and b.
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

// TestSessionExpiryIsInTheFuture is a sanity check on the TTL: a session that
// expires on creation would sign everyone straight back out, and the schema's
// own CHECK would refuse it.
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

// TestEraseRemovesPersonalDataAndKeepsTheRecord is the erasure contract. Two
// halves, and both matter: the personal data must be gone, and the financial
// record must survive without it — an order is a tax record, not a courtesy.
//
// The invoice preference is the row that used to survive erasure entirely. It
// is keyed by order rather than by user, so nulling the account and blanking
// the delivery fields left carrier_code — a 手機條碼載具, which identifies a
// person — behind for good.
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

	// The order itself remains, with its money.
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

// TestAResetTokenIsSpentExactlyOnce is the property that decides whether a
// reset link is a credential or a key.
//
// Eight requests carry the same token through one barrier. The guard is in the
// UPDATE's own WHERE clause, so exactly one may win; a read-then-write check in
// Go is a check all eight pass, and the prize is somebody else's account.
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
			// A different password per racer, so a survivor can be identified
			// by which one authenticates.
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

// TestAnExpiredResetTokenIsRefused holds the window. The link is the whole
// credential, so a link that outlives its hour is a standing key sitting in a
// mailbox.
func TestAnExpiredResetTokenIsRefused(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	register(t, s, "expiredreset@example.com")

	token, _, _, err := s.BeginReset(ctx, "expiredreset@example.com")
	if err != nil {
		t.Fatalf("begin reset: %v", err)
	}
	// Aged past its window in the database rather than by waiting an hour. The
	// row is found by the same digest the store computes, and created_at moves
	// with it — password_reset_tokens_expiry_after_creation refuses a row whose
	// window closed before it opened, which is the constraint doing its job.
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

	// Distinct from the password register() sets, or the second assertion
	// passes because the two are the same string rather than because nothing
	// changed — which is how this test first read green.
	const attempted = "the password an expired link tried to set"
	if err := s.CompleteReset(ctx, token, attempted); !errors.Is(err, account.ErrResetInvalid) {
		t.Errorf("an expired token was accepted: %v", err)
	}
	if _, err := s.Authenticate(ctx, "expiredreset@example.com", attempted); err == nil {
		t.Error("the password changed anyway")
	}
}

// TestAResetInvalidatesSiblingTokensAndSessions is the clean-up a reset owes.
//
// Somebody who clicked "forgot password" three times has two more live links in
// the mailbox an attacker is reading, and a session that survives the reset
// means the reset changed nothing for whoever was already inside.
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

// TestBeginResetSaysNothingAboutWhoHasAnAccount is the anti-oracle property.
//
// It reads the store rather than the handler because that is where the answer
// is decided: found is the ONLY difference between the two calls, and the
// handler redirects identically on both. An error, a different duration or a
// different shape would each be a way to enumerate the customer list.
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

// TestAResetTokenIsStoredHashed. The table holds a digest, so a database dump
// is not a pile of working reset links.
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

// TestAWeakNewPasswordIsRefusedWithoutSpendingTheToken. Getting the rules wrong
// must not cost the customer their one link — otherwise the reset form locks
// people out on a typo.
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

// TestTheDefaultAddressCanBeMoved holds that a customer can choose which
// address their orders go to.
//
// SetDefaultAddress and ClearDefaultAddress were written when the address book
// shipped and neither was ever called: the first address saved became the
// default and stayed it forever, and checkout prefills from whichever one that
// is. A customer who moved house could add the new address and never make it
// the one their orders go to.
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
	// Exactly one, which addresses_one_default_per_user enforces and this
	// asserts anyway: a transaction that set before it cleared would trip the
	// index, and one that cleared without setting would leave none.
	if n := countDefaults(t, u.ID); n != 1 {
		t.Errorf("the account has %d default addresses, want 1", n)
	}

	// And back again, so the test is about moving rather than about the first
	// move happening to work.
	if err := s.MakeDefaultAddress(ctx, u.ID, home); err != nil {
		t.Fatalf("move it back: %v", err)
	}
	if got := defaultAddressID(t, u.ID); got != home {
		t.Errorf("the default is %s, want the home address %s", got, home)
	}
}

// TestMovingTheDefaultToSomebodyElsesAddressChangesNothing. The id comes off a
// form, and the scoping is in the query — a check afterwards is one somebody
// forgets, and what it costs is a stranger's address book.
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
	// Mine is untouched — the rollback is what guarantees it. Without one the
	// clear would have run and left me with no default at all.
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

// TestExpiredSessionsArePruned holds that dead session rows go away and live
// ones do not.
//
// DeleteExpiredSessions shipped with the account pages and nothing called it.
// The reads were all correct — every one enforces expiry in its own WHERE
// clause — so nothing was ever wrong, and the table grew without bound behind
// them. Each dead row still holds the user id it belonged to.
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
	// whose window closed before it opened, and a session that expired an hour
	// ago was issued before that.
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
	// The live one still works, which is the half that matters: a pruner that
	// deleted everything would pass a count-only assertion.
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

// TestATierIsDerivedFromSpendAndNotStored holds that a tier follows the orders
// behind it.
//
// A stored tier drifts from the orders behind it the moment one is cancelled,
// and nobody notices until a customer asks why a benefit they were told they
// had has gone. This walks a customer up a band and then takes the order away.
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

	// Cancel it. A derived tier goes with the order; a stored one would not.
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

// TestTheSweepDropsDeadResetTokensAndKeepsLiveOnes proves a reset token stops
// being a row once it stops being a key.
//
// Nothing deleted these. Every reset goen ever issued stayed in the table, each
// carrying the user id it belonged to — the same defect the session sweep was
// written for, on the table beside it.
//
// The half that matters is the LIVE token: a sweep that took one would lock
// somebody out of the account they are in the middle of recovering.
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

// TestAnAddressIsProvedByFollowingTheLink is the whole feature.
//
// users.email_verified_at was declared with the users table and nothing ever set
// it. Underneath that was worse: UpdateProfile writes full_name and phone, so a
// customer could not change their address at all — somebody who mistyped it at
// registration received nothing, for good.
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
	// Spent by the statement: a second use finds nothing.
	if _, err := s.ConfirmVerification(ctx, token); !errors.Is(err, account.ErrVerifyInvalid) {
		t.Errorf("re-using the link = %v, want ErrVerifyInvalid", err)
	}
}

// TestAChangeTakesEffectOnlyWhenConfirmed is why the old address keeps receiving.
//
// A mistyped change would otherwise point the account at an address nobody reads,
// and the reset link — the one way back in — would go there too.
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

	// Still the old address, and the pending one is visible so a typo is fixable.
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

// TestAChangeToATakenAddressIsRefused proves the unique index is the real guard.
//
// The early check gives a staff-readable sentence, but an address can be taken
// between the request and the confirmation — and only the write can catch that.
// This drives exactly that race: request first, then let somebody else take it.
func TestAChangeToATakenAddressIsRefused(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	mine := register(t, s, "mine-"+uuid.NewString()+"@goen.invalid")
	wanted := "contested-" + uuid.NewString() + "@goen.invalid"

	token, err := s.RequestVerification(ctx, mine.ID, wanted)
	if err != nil {
		t.Fatalf("RequestVerification: %v", err)
	}
	// Somebody else registers it first.
	register(t, s, wanted)

	if _, err := s.ConfirmVerification(ctx, token); !errors.Is(err, account.ErrEmailTaken) {
		t.Errorf("confirming a taken address = %v, want ErrEmailTaken", err)
	}
	// And my account is untouched — the whole transaction rolled back.
	if got := emailOf(t, mine.ID); got != mine.Email {
		t.Errorf("my address became %q after a refused change, want %q", got, mine.Email)
	}
}

// TestAskingAgainLeavesOneLiveLink proves the replace.
//
// Somebody who pressed the button three times holds one key, not three, and it is
// the newest — the letter that just arrived is the one they will use.
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

// TestAnExpiredVerificationIsRefused proves the window, judged by the DATABASE's
// clock — the same one that wrote expires_at.
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

// emailOf is the address on an account right now.
func emailOf(t *testing.T, userID string) string {
	t.Helper()
	var addr string
	if err := pool.QueryRow(t.Context(),
		`SELECT email FROM users WHERE id = $1`, userID).Scan(&addr); err != nil {
		t.Fatalf("read email: %v", err)
	}
	return addr
}

// TestRegisteringAsksForTheAddressToBeProved is where a typo becomes findable.
//
// Somebody who registers with gmial.com otherwise hears nothing, ever: the receipt
// goes nowhere, the shop does not know, and there was no way for them to notice or
// to fix it. The letter goes out with the account.
func TestRegisteringAsksForTheAddressToBeProved(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	addr := "asked-" + uuid.NewString() + "@goen.invalid"

	u := register(t, s, addr)

	// register() goes through the store, not the handler, so ask for it the way
	// the handler does and assert the message that carries the link.
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

// TestTheMembershipBandReadsInTheVisitorsLanguage closes the worst shape a missing
// translation takes.
//
// The account page puts the band name INSIDE a sentence — "NT$10,000 more reaches
// 銀卡會員" — so a missing English name does not read as untranslated content. It
// reads as a broken page, and only to the visitor.
//
// Driven through Overview rather than asserted against localized_name directly: the
// first version did the latter, and mutating the QUERY left it green — it was testing
// the SQL function, which every other guard already covers.
func TestTheMembershipBandReadsInTheVisitorsLanguage(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)

	// A customer with no spend at all, so the NEXT band is the lowest one and the
	// sentence under test is the one an ordinary account page shows.
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
