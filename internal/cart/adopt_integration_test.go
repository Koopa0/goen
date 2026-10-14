//go:build integration

package cart_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/user"
)

// adopter is an account with no cart, made the way owner_integration_test makes
// one: adoption needs only the user row.
func adopter(t *testing.T, email string) user.User {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return user.User{ID: id.String(), Email: email, Role: user.RoleCustomer}
}

// adoptionPool is a one-connection pool that runs as the store role under a
// name the lock watcher can find.
func adoptionPool(t *testing.T, applicationName string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse adoption pool config: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, setRoleErr := conn.Exec(ctx, `SET ROLE store`)
		return setRoleErr
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open adoption pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// requireAdopterHoldsUserRow fails unless some transaction holds the account row
// at NO KEY UPDATE, the strength only adoption takes: checkout's KEY SHARE is
// compatible with a probe at that strength. Waiting on a cart row is not enough
// to show the user -> cart order, because an adopter that took the cart first
// waits there just the same.
func requireAdopterHoldsUserRow(t *testing.T, userID uuid.UUID) {
	t.Helper()
	probe, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin account probe: %v", err)
	}
	defer func() { _ = probe.Rollback(context.WithoutCancel(t.Context())) }()
	_, err = probe.Exec(t.Context(), `SELECT 1 FROM users WHERE id = $1 FOR NO KEY UPDATE NOWAIT`, userID)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "55P03" {
		t.Errorf("probe of the account row = %v, want lock_not_available (55P03): adoption waits on the cart without holding the account row first", err)
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

// TestConcurrentFirstAdoptersKeepBothGuestCarts holds the account row before
// either sign-in starts. Both calls must wait there, which makes the "no account
// cart exists" observation deterministic rather than scheduler luck. One cart is
// then adopted and the other merged into it, both through the store role.
func TestConcurrentFirstAdoptersKeepBothGuestCarts(t *testing.T) {
	ctx := t.Context()
	u := adopter(t, "first-adopters-"+uuid.NewString()+"@example.com")
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
			bearer.Digest(label+u.ID)).Scan(&cartID); err != nil {
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
	firstStore := cart.NewStore(adoptionPool(t, firstName))
	secondStore := cart.NewStore(adoptionPool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- firstStore.Adopt(ctx, u.ID, firstCart) }()
	go func() { secondDone <- secondStore.Adopt(ctx, u.ID, secondCart) }()
	waitForApplicationLock(t, firstName, firstDone)
	waitForApplicationLock(t, secondName, secondDone)

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

// TestCheckoutAndAdoptionShareUserBeforeCartLockOrder pauses checkout after it
// owns the account cart. Adoption then holds NO KEY UPDATE on the user and waits
// for that cart. Releasing checkout is safe only because its user KEY SHARE is
// already held (and compatible); the former cart -> user acquisition deadlocked.
func TestCheckoutAndAdoptionShareUserBeforeCartLockOrder(t *testing.T) {
	ctx := t.Context()
	u := adopter(t, "checkout-adopt-"+uuid.NewString()+"@example.com")
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
		bearer.Digest("checkout-adopt-account-"+u.ID), userID).Scan(&accountCart); err != nil {
		t.Fatalf("create account cart: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (token_hash) VALUES ($1) RETURNING id`,
		bearer.Digest("checkout-adopt-guest-"+u.ID)).Scan(&guestCart); err != nil {
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
	addr := &delivery.Details{
		Email: u.Email, RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shown := checkoutQuote(t, cart.NewStore(pool), accountCart, owner, shippingID, addr, "")

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
	checkoutStore := cart.NewStore(adoptionPool(t, checkoutName))
	adoptionStore := cart.NewStore(adoptionPool(t, adoptionName))
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
	waitForApplicationLock(t, checkoutName, checkoutDone)
	go func() { adoptionDone <- adoptionStore.Adopt(ctx, u.ID, guestCart) }()
	waitForApplicationLock(t, adoptionName, adoptionDone)
	requireAdopterHoldsUserRow(t, userID)

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
