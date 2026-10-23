//go:build integration

package cart_test

import (
	"context"
	"errors"
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
	"github.com/koopa0/goen/internal/order/ordertest"
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

// cartItemQuantity is the quantity of one line of a cart.
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
	firstVariant, secondVariant, _ := ordertest.ThreeVariants(t, pool, "adopt-first")

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

// TestStore_AdoptMerges: cart.Store.Adopt sums a guest cart's lines into
// the account cart rather than replacing them, keeps guest-only lines, and
// deletes the guest cart so the next sign-in cannot adopt it again.
func TestStore_AdoptMerges(t *testing.T) {
	ctx := t.Context()
	u := adopter(t, "merge-"+uuid.NewString()+"@example.com")
	uid := uuid.MustParse(u.ID)

	a := ordertest.FreshVariant(t, pool, "adopt")
	b := ordertest.FreshVariant(t, pool, "adopt-other")

	var accountCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
		bearer.Digest("account-cart-"+u.ID), uid).Scan(&accountCart); err != nil {
		t.Fatalf("account cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
		accountCart, a); err != nil {
		t.Fatalf("account line: %v", err)
	}

	var guestCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
		bearer.Digest("guest-cart-"+u.ID)).Scan(&guestCart); err != nil {
		t.Fatalf("guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity)
		VALUES ($1, $2, 2), ($1, $3, 5)`, guestCart, a, b); err != nil {
		t.Fatalf("guest lines: %v", err)
	}

	if err := cart.NewStore(pool).Adopt(ctx, u.ID, guestCart); err != nil {
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

// TestStore_AdoptClampsQuantity: cart.Store.Adopt clamps a merged line to
// the sellable quantity (stock less safety stock) and reports
// ErrQuantityAdjusted.
func TestStore_AdoptClampsQuantity(t *testing.T) {
	ctx := t.Context()
	u := adopter(t, "merge-clamp-"+uuid.NewString()+"@example.com")
	uid := uuid.MustParse(u.ID)

	var vid uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE pv.is_active AND p.status = 'active'
		  AND (pv.stock_quantity - pv.safety_stock) > 0
		ORDER BY pv.position, pv.id LIMIT 1`).Scan(&vid); err != nil {
		t.Fatalf("variant: %v", err)
	}
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		vid).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), //nolint:usetesting // t.Context is already cancelled in Cleanup
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3 WHERE id = $1`,
			vid, wasStock, wasSafety)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 5, safety_stock = 2 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var accountCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
		bearer.Digest("account-cart-"+u.ID), uid).Scan(&accountCart); err != nil {
		t.Fatalf("account cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 2)`,
		accountCart, vid); err != nil {
		t.Fatalf("account line: %v", err)
	}

	var guestCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
		bearer.Digest("guest-cart-"+u.ID)).Scan(&guestCart); err != nil {
		t.Fatalf("guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 2)`,
		guestCart, vid); err != nil {
		t.Fatalf("guest line: %v", err)
	}

	if err := cart.NewStore(pool).Adopt(ctx, u.ID, guestCart); !errors.Is(err, cart.ErrQuantityAdjusted) {
		t.Fatalf("merge over stock returned %v, want ErrQuantityAdjusted", err)
	}

	var merged int32
	if err := pool.QueryRow(ctx,
		`SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
		accountCart, vid).Scan(&merged); err != nil {
		t.Fatalf("read merged line: %v", err)
	}
	if merged != 3 {
		t.Errorf("merged quantity = %d, want 3 (2 + 2 clamped to sellable 3)", merged)
	}
}

// TestStore_AdoptUnavailable: cart.Store.Adopt refuses with ErrMergeRefused
// when a guest line is out of stock and leaves both carts as they were.
func TestStore_AdoptUnavailable(t *testing.T) {
	ctx := t.Context()
	u := adopter(t, "merge-refuse-"+uuid.NewString()+"@example.com")
	uid := uuid.MustParse(u.ID)

	var available, unavailable uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position LIMIT 1`).Scan(&available); err != nil {
		t.Fatalf("available variant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active AND id <> $1 ORDER BY position LIMIT 1`,
		available).Scan(&unavailable); err != nil {
		t.Fatalf("second variant: %v", err)
	}
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		unavailable).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), //nolint:usetesting // t.Context is already cancelled in Cleanup
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3, is_active = true WHERE id = $1`,
			unavailable, wasStock, wasSafety)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 0, safety_stock = 0 WHERE id = $1`,
		unavailable); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var accountCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
		bearer.Digest("account-cart-"+u.ID), uid).Scan(&accountCart); err != nil {
		t.Fatalf("account cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
		accountCart, available); err != nil {
		t.Fatalf("account line: %v", err)
	}

	var guestCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
		bearer.Digest("guest-cart-"+u.ID)).Scan(&guestCart); err != nil {
		t.Fatalf("guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
		guestCart, unavailable); err != nil {
		t.Fatalf("guest line: %v", err)
	}

	if err := cart.NewStore(pool).Adopt(ctx, u.ID, guestCart); !errors.Is(err, cart.ErrMergeRefused) {
		t.Fatalf("adopt with unavailable guest line returned %v, want ErrMergeRefused", err)
	}

	var guestCount, accountCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM cart_items WHERE cart_id = $1`, guestCart).Scan(&guestCount); err != nil {
		t.Fatalf("count guest items: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM cart_items WHERE cart_id = $1`, accountCart).Scan(&accountCount); err != nil {
		t.Fatalf("count account items: %v", err)
	}
	if guestCount != 1 {
		t.Errorf("guest cart items = %d, want 1 unchanged", guestCount)
	}
	if accountCount != 1 {
		t.Errorf("account cart items = %d, want 1 unchanged", accountCount)
	}
	var guestStillThere int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM carts WHERE id = $1`, guestCart).Scan(&guestStillThere); err != nil {
		t.Fatalf("count guest cart row: %v", err)
	}
	if guestStillThere != 1 {
		t.Errorf("guest cart row count = %d, want 1", guestStillThere)
	}
}

// TestStore_AdoptInactive: cart.Store.Adopt refuses with ErrMergeRefused a
// guest line whose variant or product can no longer be sold, and the guest's
// cart, and the account's when it has one, keep every line at its original
// quantity.
func TestStore_AdoptInactive(t *testing.T) {
	ctx := t.Context()

	breakers := []struct {
		name string
		// break makes the variant unsellable; restore is run from Cleanup.
		breakIt, restore string
	}{
		{
			"inactive variant",
			`UPDATE product_variants SET is_active = false WHERE id = $1`,
			`UPDATE product_variants SET is_active = true WHERE id = $1`,
		},
		{
			"inactive product",
			`UPDATE products SET status = 'archived'
			 WHERE id = (SELECT product_id FROM product_variants WHERE id = $1)`,
			`UPDATE products SET status = 'active'
			 WHERE id = (SELECT product_id FROM product_variants WHERE id = $1)`,
		},
	}
	for _, b := range breakers {
		for _, hasAccountCart := range []bool{false, true} {
			name := b.name + "/guest-only adoption"
			if hasAccountCart {
				name = b.name + "/account and guest merge"
			}
			t.Run(name, func(t *testing.T) {
				u := adopter(t, "adopt-inactive-"+uuid.NewString()+"@example.com")
				uid := uuid.MustParse(u.ID)

				good := ordertest.FreshVariant(t, pool, "adopt")
				// A product of two variants: a product cannot be left active with its
				// only variant inactive.
				bad, _, _ := ordertest.VariantsOf(t, pool, "adopt-other", 2)
				if _, err := pool.Exec(ctx, b.breakIt, bad); err != nil {
					t.Fatalf("make the variant unsellable: %v", err)
				}
				t.Cleanup(func() {
					_, _ = pool.Exec(context.Background(), b.restore, bad) //nolint:usetesting // t.Context is already cancelled in Cleanup
				})

				var accountCart uuid.UUID
				if hasAccountCart {
					if err := pool.QueryRow(ctx,
						`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
						bearer.Digest("account-cart-"+u.ID), uid).Scan(&accountCart); err != nil {
						t.Fatalf("account cart: %v", err)
					}
					if _, err := pool.Exec(ctx,
						`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
						accountCart, good); err != nil {
						t.Fatalf("account line: %v", err)
					}
				}
				var guestCart uuid.UUID
				if err := pool.QueryRow(ctx,
					`INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
					bearer.Digest("guest-cart-"+u.ID)).Scan(&guestCart); err != nil {
					t.Fatalf("guest cart: %v", err)
				}
				for _, line := range []struct {
					variant uuid.UUID
					qty     int32
				}{{good, 2}, {bad, 3}} {
					if _, err := pool.Exec(ctx,
						`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, $3)`,
						guestCart, line.variant, line.qty); err != nil {
						t.Fatalf("guest line: %v", err)
					}
				}

				if err := cart.NewStore(pool).Adopt(ctx, u.ID, guestCart); !errors.Is(err, cart.ErrMergeRefused) {
					t.Fatalf("adopt returned %v, want ErrMergeRefused", err)
				}

				quantities := func(cartID uuid.UUID) map[uuid.UUID]int32 {
					rows, err := pool.Query(ctx,
						`SELECT variant_id, quantity FROM cart_items WHERE cart_id = $1`, cartID)
					if err != nil {
						t.Fatalf("read lines: %v", err)
					}
					defer rows.Close()
					got := map[uuid.UUID]int32{}
					for rows.Next() {
						var v uuid.UUID
						var q int32
						if err := rows.Scan(&v, &q); err != nil {
							t.Fatalf("scan line: %v", err)
						}
						got[v] = q
					}
					if err := rows.Err(); err != nil {
						t.Fatalf("read lines: %v", err)
					}
					return got
				}
				if got := quantities(guestCart); len(got) != 2 || got[good] != 2 || got[bad] != 3 {
					t.Errorf("guest lines after refusal = %v, want both at their original quantities", got)
				}
				var carts int
				if err := pool.QueryRow(ctx,
					`SELECT count(*) FROM carts WHERE user_id = $1`, uid).Scan(&carts); err != nil {
					t.Fatalf("count account carts: %v", err)
				}
				if hasAccountCart {
					if got := quantities(accountCart); len(got) != 1 || got[good] != 1 {
						t.Errorf("account lines after refusal = %v, want the one original line", got)
					}
				} else if carts != 0 {
					t.Errorf("a refused adoption left %d account carts, want none", carts)
				}
			})
		}
	}
}

// TestStore_AdoptForeignCart: cart.Store.Adopt, called directly with a cart
// another account owns, as a sign-in that lost a race for it would, answers
// ErrNotFound and leaves that cart, its owner and its lines alone. The lines
// are sellable, so nothing but the ownership guard stands between that call and
// the other account's cart being merged away or taken over.
func TestStore_AdoptForeignCart(t *testing.T) {
	ctx := t.Context()
	ownVariant := ordertest.FreshVariant(t, pool, "adopt")
	theirVariant := ordertest.FreshVariant(t, pool, "adopt-other")

	for _, tt := range []struct {
		name     string
		hasACart bool
	}{
		{"into the account's own cart", true},
		{"as the account's first cart", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := adopter(t, "adopt-owned-"+uuid.NewString()+"@example.com")
			other := adopter(t, "adopt-owned-other-"+uuid.NewString()+"@example.com")
			otherUID := uuid.MustParse(other.ID)

			var ownCart uuid.UUID
			if tt.hasACart {
				if err := pool.QueryRow(ctx,
					`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
					bearer.Digest("adopt-owned-own-"+u.ID), uuid.MustParse(u.ID)).Scan(&ownCart); err != nil {
					t.Fatalf("account cart: %v", err)
				}
				if _, err := pool.Exec(ctx,
					`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
					ownCart, ownVariant); err != nil {
					t.Fatalf("account line: %v", err)
				}
			}
			var theirCart uuid.UUID
			if err := pool.QueryRow(ctx,
				`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
				bearer.Digest("adopt-owned-theirs-"+u.ID), otherUID).Scan(&theirCart); err != nil {
				t.Fatalf("other account's cart: %v", err)
			}
			if _, err := pool.Exec(ctx,
				`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 2)`,
				theirCart, theirVariant); err != nil {
				t.Fatalf("other account's line: %v", err)
			}

			if err := cart.NewStore(pool).Adopt(ctx, u.ID, theirCart); !errors.Is(err, cart.ErrNotFound) {
				t.Errorf("adopting another account's cart returned %v, want ErrNotFound", err)
			}

			var owner uuid.NullUUID
			if err := pool.QueryRow(ctx, `SELECT user_id FROM carts WHERE id = $1`, theirCart).
				Scan(&owner); err != nil {
				t.Fatalf("the other account's cart is gone: %v", err)
			}
			if owner.UUID != otherUID {
				t.Errorf("the other account's cart is owned by %v, want %s", owner, otherUID)
			}
			if qty := cartItemQuantity(t, theirCart, theirVariant); qty != 2 {
				t.Errorf("the other account's line quantity = %d, want 2", qty)
			}
			if tt.hasACart {
				var lines int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM cart_items WHERE cart_id = $1`,
					ownCart).Scan(&lines); err != nil {
					t.Fatalf("count the account's lines: %v", err)
				}
				if qty := cartItemQuantity(t, ownCart, ownVariant); lines != 1 || qty != 1 {
					t.Errorf("the account's cart holds %d lines, its own at %d; want only its "+
						"own line at 1", lines, qty)
				}
			}
		})
	}
}

// TestStore_AdoptConcurrent: two accounts adopting the same guest cart at
// once cannot both take it: one succeeds and the other gets ErrNotFound and
// leaves the cart with the winner. Both transactions lock their own user before
// either can update the cart; the loser must observe the new owner under the
// cart lock and leave the cart untouched.
func TestStore_AdoptConcurrent(t *testing.T) {
	ctx := t.Context()
	firstUser := adopter(t, "shared-guest-a-"+uuid.NewString()+"@example.com")
	secondUser := adopter(t, "shared-guest-b-"+uuid.NewString()+"@example.com")
	firstUserID, secondUserID := uuid.MustParse(firstUser.ID), uuid.MustParse(secondUser.ID)

	var variantID, guestCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position, id LIMIT 1`).
		Scan(&variantID); err != nil {
		t.Fatalf("variant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
		bearer.Digest("shared-guest-"+uuid.NewString())).Scan(&guestCart); err != nil {
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
	firstStore := cart.NewStore(adoptionPool(t, firstName))
	secondStore := cart.NewStore(adoptionPool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- firstStore.Adopt(ctx, firstUser.ID, guestCart) }()
	go func() { secondDone <- secondStore.Adopt(ctx, secondUser.ID, guestCart) }()
	waitForApplicationLock(t, firstName, firstDone)
	waitForApplicationLock(t, secondName, secondDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release shared guest cart: %v", err)
	}
	firstErr, secondErr := operationResult(t, firstDone), operationResult(t, secondDone)
	firstWon := firstErr == nil && errors.Is(secondErr, cart.ErrNotFound)
	secondWon := secondErr == nil && errors.Is(firstErr, cart.ErrNotFound)
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
