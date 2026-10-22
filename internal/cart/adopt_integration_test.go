//go:build integration

package cart_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order/access"
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

// TestStore_AdoptConcurrentFirst holds that two first sign-ins racing keep both
// guest carts: one cart is adopted and the other merged into it, both through
// the store role. It holds the account row before either sign-in starts, so
// both calls must wait there, which makes the "no account cart exists"
// observation deterministic rather than scheduler luck.
func TestStore_AdoptConcurrentFirst(t *testing.T) {
	ctx := t.Context()
	u := adopter(t, "first-adopters-"+uuid.NewString()+"@example.com")
	userID := uuid.MustParse(u.ID)

	// Variants of this test's own: the cart tests share one database, and the
	// checkout test below spends stock.
	firstVariant, secondVariant, _ := threeVariants(t, "adopt-first")

	guest := func(label string, variantID uuid.UUID, quantity int32) uuid.UUID {
		t.Helper()
		var cartID uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
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

// sellableVariants creates one product with n sellable variants and returns
// their ids in ascending order.
func sellableVariants(t *testing.T, name string, n int) []uuid.UUID {
	t.Helper()
	ctx := t.Context()
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
		SELECT $1, upper($2) || '-' || i, 100000, true, i
		FROM generate_series(0, $3::integer - 1) AS i`, productID, slug, n); err != nil {
		t.Fatalf("create %d variants: %v", n, err)
	}
	if _, err := pool.Exec(ctx, `
		SELECT record_inventory_movement(id, 10, 'adjustment', 'fixture:' || id, 'admin', NULL, NULL)
		FROM product_variants WHERE product_id = $1`, productID); err != nil {
		t.Fatalf("stock %d variants: %v", n, err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active', published_at = now() WHERE id = $1`,
		productID); err != nil {
		t.Fatalf("publish %s: %v", slug, err)
	}
	rows, err := pool.Query(ctx,
		`SELECT id FROM product_variants WHERE product_id = $1 ORDER BY id`, productID)
	if err != nil {
		t.Fatalf("read variants: %v", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatalf("collect variants: %v", err)
	}
	return ids
}

// TestHandler_AdoptRequestCartLineCap holds that a guest cart merges into the
// account cart only within the line cap. It fills the account cart one line
// short of issuer.MaxIssueProductLines and signs in with a guest cart holding
// three products the account lacks and one it has. The merge follows Add: the
// product the guest added first takes the last free line, the two added after
// it are left out, the line the account already has still merges, and the
// caller is told the cart was adjusted. The product kept has the highest id of
// the three, so a merge in id order would keep another.
func TestHandler_AdoptRequestCartLineCap(t *testing.T) {
	ctx := t.Context()
	u := adopter(t, "adopt-line-cap-"+uuid.NewString()+"@example.com")
	userID := uuid.MustParse(u.ID)

	variants := sellableVariants(t, "adopt-line-cap", issuer.MaxIssueProductLines+2)
	held := variants[:issuer.MaxIssueProductLines-1]
	shared := held[0]
	guestOnly := variants[issuer.MaxIssueProductLines-1:]
	kept, leftOut := guestOnly[2], guestOnly[:2]

	var accountCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
		bearer.Digest("adopt-line-cap-account-"+u.ID), userID).Scan(&accountCart); err != nil {
		t.Fatalf("create account cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity)
		SELECT $1, v, 1 FROM unnest($2::uuid[]) AS v`, accountCart, held); err != nil {
		t.Fatalf("fill account cart: %v", err)
	}

	token := "adopt-line-cap-guest-" + uuid.NewString()
	var guestCart uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
		bearer.Digest(token)).Scan(&guestCart); err != nil {
		t.Fatalf("create guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity, added_at) VALUES
			($1, $2, 1, now() - interval '4 minutes'),
			($1, $3, 1, now() - interval '3 minutes'),
			($1, $4, 1, now() - interval '2 minutes'),
			($1, $5, 2, now() - interval '1 minute')`,
		guestCart, kept, leftOut[0], leftOut[1], shared); err != nil {
		t.Fatalf("fill guest cart: %v", err)
	}

	h := cart.NewHandler(cart.NewStore(adoptionPool(t, "adopt-line-cap")), access.NewStore(pool, false),
		slog.New(slog.DiscardHandler), false, nil)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/signin", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cookie under test
	adjusted, err := h.AdoptRequestCart(ctx, req, u.ID)
	if err != nil {
		t.Fatalf("AdoptRequestCart: %v", err)
	}
	if !adjusted {
		t.Error("AdoptRequestCart adjusted = false, want true: the sign-in shows no quantity-adjusted notice")
	}

	rows, err := pool.Query(ctx,
		`SELECT variant_id, quantity FROM cart_items WHERE cart_id = $1`, accountCart)
	if err != nil {
		t.Fatalf("read account cart: %v", err)
	}
	type line struct {
		VariantID uuid.UUID
		Quantity  int32
	}
	lines, err := pgx.CollectRows(rows, pgx.RowToStructByPos[line])
	if err != nil {
		t.Fatalf("collect account cart: %v", err)
	}
	got := make(map[uuid.UUID]int32, len(lines))
	for _, l := range lines {
		got[l.VariantID] = l.Quantity
	}
	if len(got) != issuer.MaxIssueProductLines {
		t.Errorf("account cart holds %d lines, want %d", len(got), issuer.MaxIssueProductLines)
	}
	if got[kept] != 1 {
		t.Errorf("first-added guest line quantity = %d, want 1", got[kept])
	}
	for _, v := range leftOut {
		if q, ok := got[v]; ok {
			t.Errorf("guest line added after the cart filled is in the account cart with quantity %d", q)
		}
	}
	if got[shared] != 3 {
		t.Errorf("line both carts hold has quantity %d, want 3 (1 + 2)", got[shared])
	}
	var guests int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM carts WHERE id = $1`, guestCart).Scan(&guests); err != nil {
		t.Fatalf("count guest carts: %v", err)
	}
	if guests != 0 {
		t.Error("guest cart survived its merge")
	}
}
