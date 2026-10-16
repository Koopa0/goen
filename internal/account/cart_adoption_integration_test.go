//go:build integration

package account_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
)

func TestAdoptCartMergesRatherThanReplaces(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "merge@example.com")
	uid := uuid.MustParse(u.ID)

	a := sellableVariant(t, ctx)
	b := anotherSellableVariant(t, ctx, a)

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

func TestAdoptCartMergeClampsCombinedQuantity(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "merge-clamp-"+uuid.NewString()+"@example.com")
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

func TestAdoptUnavailableGuestLinePreservesBothCarts(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "merge-refuse-"+uuid.NewString()+"@example.com")
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

// TestAdoptRefusesInactiveLinesWithoutTouchingEitherCart holds the rejection
// for a line that can no longer be sold at all: the guest's cart, and the
// account's when it has one, keep every line at its original quantity.
func TestAdoptRefusesInactiveLinesWithoutTouchingEitherCart(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)

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
				u := register(t, s, "adopt-inactive-"+uuid.NewString()+"@example.com")
				uid := uuid.MustParse(u.ID)

				good := sellableVariant(t, ctx)
				bad := anotherSellableVariant(t, ctx, good)
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

// TestAMergedCartIsVisibleAfterSignInWithTheDeletedGuestCookie holds the
// storefront path: sign-in adopt deletes the guest row, the browser cookie
// still names it, and GET /cart must show the surviving account cart.
func TestAMergedCartIsVisibleAfterSignInWithTheDeletedGuestCookie(t *testing.T) {
	ctx := t.Context()
	accounts := account.NewStore(pool)
	u := registerProved(t, accounts, "merge-cookie-"+uuid.NewString()+"@example.com")
	uid := uuid.MustParse(u.ID)

	a := sellableVariant(t, ctx)
	b := anotherSellableVariant(t, ctx, a)

	guestToken := "guest-cart-" + u.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO carts (digest, user_id) VALUES ($1, $2)`,
		bearer.Digest("account-cart-"+u.ID), uid); err != nil {
		t.Fatalf("account cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity)
		 SELECT id, $2, 1 FROM carts WHERE digest = $1`,
		bearer.Digest("account-cart-"+u.ID), a); err != nil {
		t.Fatalf("account line: %v", err)
	}

	var guestCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
		bearer.Digest(guestToken)).Scan(&guestCart); err != nil {
		t.Fatalf("guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity)
		VALUES ($1, $2, 2), ($1, $3, 5)`, guestCart, a, b); err != nil {
		t.Fatalf("guest lines: %v", err)
	}

	carts := cart.NewHandler(cart.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil, nil)

	h := account.NewHandler(accounts, carts, slog.New(slog.DiscardHandler), false, nil)

	form := url.Values{
		"email": {u.Email}, "password": {"a sufficiently long password"}, "next": {"/cart"},
	}
	signIn := httptest.NewRequestWithContext(ctx, http.MethodPost, "/signin",
		strings.NewReader(form.Encode()))
	signIn.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie, read by this handler
	signIn.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
	signed := httptest.NewRecorder()
	h.SignIn(signed, signIn)
	if signed.Code != http.StatusSeeOther {
		t.Fatalf("sign-in status = %d, want 303; body=%s", signed.Code, signed.Body.String())
	}

	var session *http.Cookie
	for _, c := range signed.Result().Cookies() {
		if c.Name == "goen_session" || strings.HasSuffix(c.Name, "goen_session") {
			session = c
			break
		}
	}
	if session == nil {
		t.Fatalf("sign-in cookies = %v, want a session", signed.Result().Cookies())
	}

	page := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart", http.NoBody)
	page.AddCookie(session)
	//nolint:gosec // G124: the deleted guest token the browser still holds
	page.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
	shown := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(carts.Page)).ServeHTTP(shown, page)
	if shown.Code != http.StatusOK {
		t.Fatalf("GET /cart status = %d, want 200; body=%s", shown.Code, shown.Body.String())
	}
	body := shown.Body.String()
	if got := cartLineQuantity(body, a); got != "3" {
		t.Errorf("merged cart still empty after sign-in: variant %s quantity = %q, want 3", a, got)
	}
	if got := cartLineQuantity(body, b); got != "5" {
		t.Errorf("merged guest-only line missing: variant %s quantity = %q, want 5", b, got)
	}
}

// TestFailedCartAdoptionOnSignInShowsNoticeAndPreservesBothCarts holds that a
// reversible adoption failure still signs the customer in, lands them on the
// recovery page for any safe continuation, and leaves both eligible carts intact
// until POST retry succeeds.
func TestFailedCartAdoptionOnSignInShowsNoticeAndPreservesBothCarts(t *testing.T) {
	ctx := t.Context()
	accounts := account.NewStore(pool)
	u := registerProved(t, accounts, "adopt-fail-"+uuid.NewString()+"@example.com")
	uid := uuid.MustParse(u.ID)

	accountVariant := sellableVariant(t, ctx)
	guestVariant := anotherSellableVariant(t, ctx, accountVariant)

	var accountCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
		bearer.Digest("account-cart-"+u.ID), uid).Scan(&accountCart); err != nil {
		t.Fatalf("account cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
		accountCart, accountVariant); err != nil {
		t.Fatalf("account line: %v", err)
	}

	guestToken := "guest-cart-" + u.ID
	var guestCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest) VALUES ($1) RETURNING id`,
		bearer.Digest(guestToken)).Scan(&guestCart); err != nil {
		t.Fatalf("guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 2)`,
		guestCart, guestVariant); err != nil {
		t.Fatalf("guest lines: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin guest-cart blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM carts WHERE id = $1 FOR UPDATE`, guestCart); err != nil {
		t.Fatalf("lock guest cart: %v", err)
	}

	carts := cart.NewHandler(cart.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil, nil)

	h := account.NewHandler(accounts, carts, slog.New(slog.DiscardHandler), false, nil)

	signCtx, signCancel := context.WithTimeout(ctx, 3*time.Second)
	defer signCancel()
	form := url.Values{
		"email": {u.Email}, "password": {"a sufficiently long password"}, "next": {"/checkout"},
	}
	signIn := httptest.NewRequestWithContext(signCtx, http.MethodPost, "/signin",
		strings.NewReader(form.Encode()))
	signIn.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie, read by this handler
	signIn.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
	signed := httptest.NewRecorder()
	h.SignIn(signed, signIn)

	if signed.Code != http.StatusSeeOther {
		t.Fatalf("sign-in status = %d, want 303; body=%s", signed.Code, signed.Body.String())
	}
	wantRecovery := "/account/cart-recovery?next=%2Fcheckout"
	if loc := signed.Header().Get("Location"); loc != wantRecovery {
		t.Fatalf("sign-in redirect = %q, want %q", loc, wantRecovery)
	}

	session := sessionCookie(t, signed)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release guest cart lock: %v", err)
	}

	recoveryReq := httptest.NewRequestWithContext(ctx, http.MethodGet, wantRecovery, http.NoBody)
	recoveryReq.AddCookie(session)
	//nolint:gosec // G124: the browser's own cart cookie, read by this handler
	recoveryReq.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
	recoveryRec := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(h.CartRecoveryPage)).ServeHTTP(recoveryRec, recoveryReq)
	if recoveryRec.Code != http.StatusOK {
		t.Fatalf("GET recovery status = %d, want 200", recoveryRec.Code)
	}
	wantNotice := i18n.T(ctx, i18n.KeyCartMergeFailed)
	wantRetry := i18n.T(ctx, i18n.KeyCartMergeRetry)
	recoveryBody := recoveryRec.Body.String()
	if !strings.Contains(recoveryBody, wantNotice) {
		t.Fatalf("recovery page does not contain notice %q", wantNotice)
	}
	if !strings.Contains(recoveryBody, wantRetry) {
		t.Fatalf("recovery page does not offer retry %q", wantRetry)
	}

	if qty := cartItemQuantity(t, guestCart, guestVariant); qty != 2 {
		t.Errorf("guest cart quantity = %d, want 2 before retry", qty)
	}
	if qty := cartItemQuantity(t, accountCart, accountVariant); qty != 1 {
		t.Errorf("account cart quantity = %d, want 1 before retry", qty)
	}

	retryForm := url.Values{"next": {"/checkout"}}
	retryReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/cart/retry",
		strings.NewReader(retryForm.Encode()))
	retryReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	retryReq.AddCookie(session)
	//nolint:gosec // G124: the browser's own cart cookie, read by this handler
	retryReq.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
	retryRec := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(h.RetryCartAdoption)).ServeHTTP(retryRec, retryReq)
	if retryRec.Code != http.StatusSeeOther {
		t.Fatalf("retry status = %d, want 303; body=%s", retryRec.Code, retryRec.Body.String())
	}
	if loc := retryRec.Header().Get("Location"); loc != "/checkout" {
		t.Fatalf("retry redirect = %q, want /checkout", loc)
	}

	var guestRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM carts WHERE id = $1`, guestCart).Scan(&guestRows); err != nil {
		t.Fatalf("guest cart row: %v", err)
	}
	if guestRows != 0 {
		t.Errorf("guest cart row survived merge, want deleted")
	}
	if qty := cartItemQuantity(t, accountCart, accountVariant); qty != 1 {
		t.Errorf("merged account line %s quantity = %d, want 1", accountVariant, qty)
	}
	if qty := cartItemQuantity(t, accountCart, guestVariant); qty != 2 {
		t.Errorf("merged guest line %s quantity = %d, want 2", guestVariant, qty)
	}

	lookupReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/cart", http.NoBody)
	lookupReq.AddCookie(session)
	//nolint:gosec // G124: the browser's own cart cookie, read by this handler
	lookupReq.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
	lookupRec := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(carts.Page)).ServeHTTP(lookupRec, lookupReq)
	if lookupRec.Code != http.StatusOK {
		t.Fatalf("GET /cart after the merge status = %d, want 200", lookupRec.Code)
	}
	for _, line := range []uuid.UUID{accountVariant, guestVariant} {
		if want := `id="line-` + line.String() + `"`; !strings.Contains(lookupRec.Body.String(), want) {
			t.Errorf("guest cookie after merge did not show the account cart: no %s on /cart", want)
		}
	}
}

// TestAdoptCartRefusesGuestCartOwnedByAnotherAccount holds that a cart already
// attached to someone else is never recovered through sign-in or POST retry.
// The browser's token names no cart this account may use, so there is nothing
// to adopt and nothing to recover: both land where they were going.
func TestAdoptCartRefusesGuestCartOwnedByAnotherAccount(t *testing.T) {
	ctx := t.Context()
	accounts := account.NewStore(pool)
	u := registerProved(t, accounts, "adopt-refuse-"+uuid.NewString()+"@example.com")
	uid := uuid.MustParse(u.ID)
	otherUser := register(t, accounts, "adopt-refuse-other-"+uuid.NewString()+"@example.com")
	otherUID := uuid.MustParse(otherUser.ID)

	var accountVariant, guestVariant uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position LIMIT 1`).
		Scan(&accountVariant); err != nil {
		t.Fatalf("account variant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active AND id <> $1 ORDER BY position LIMIT 1`,
		accountVariant).Scan(&guestVariant); err != nil {
		t.Fatalf("guest variant: %v", err)
	}

	var accountCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
		bearer.Digest("refuse-account-"+u.ID), uid).Scan(&accountCart); err != nil {
		t.Fatalf("account cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
		accountCart, accountVariant); err != nil {
		t.Fatalf("account line: %v", err)
	}

	guestToken := "refuse-guest-" + u.ID
	var guestCart uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
		bearer.Digest(guestToken), otherUID).Scan(&guestCart); err != nil {
		t.Fatalf("guest cart: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 3)`,
		guestCart, guestVariant); err != nil {
		t.Fatalf("guest line: %v", err)
	}

	carts := cart.NewHandler(cart.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil, nil)

	h := account.NewHandler(accounts, carts, slog.New(slog.DiscardHandler), false, nil)

	form := url.Values{
		"email": {u.Email}, "password": {"a sufficiently long password"}, "next": {"/account"},
	}
	signIn := httptest.NewRequestWithContext(ctx, http.MethodPost, "/signin",
		strings.NewReader(form.Encode()))
	signIn.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser names another account's cart, which must not merge
	signIn.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
	signed := httptest.NewRecorder()
	h.SignIn(signed, signIn)

	if signed.Code != http.StatusSeeOther {
		t.Fatalf("sign-in status = %d, want 303", signed.Code)
	}
	const wantLanding = "/account"
	if loc := signed.Header().Get("Location"); loc != wantLanding {
		t.Fatalf("sign-in redirect = %q, want %q", loc, wantLanding)
	}

	session := sessionCookie(t, signed)
	retryForm := url.Values{"next": {"/account"}}
	retryReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/cart/retry",
		strings.NewReader(retryForm.Encode()))
	retryReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	retryReq.AddCookie(session)
	//nolint:gosec // G124: the browser names another account's cart, which must not merge
	retryReq.AddCookie(&http.Cookie{Name: "goen_cart", Value: guestToken})
	retryRec := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(h.RetryCartAdoption)).ServeHTTP(retryRec, retryReq)
	if retryRec.Code != http.StatusSeeOther {
		t.Fatalf("retry status = %d, want 303", retryRec.Code)
	}
	if loc := retryRec.Header().Get("Location"); loc != wantLanding {
		t.Fatalf("retry redirect = %q, want %q", loc, wantLanding)
	}

	if qty := cartItemQuantity(t, accountCart, accountVariant); qty != 1 {
		t.Errorf("account cart quantity = %d, want 1", qty)
	}
	if qty := cartItemQuantity(t, guestCart, guestVariant); qty != 3 {
		t.Errorf("other account's guest cart quantity = %d, want 3", qty)
	}
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM carts WHERE id = $1`, guestCart).
		Scan(&owner); err != nil {
		t.Fatalf("read guest cart owner: %v", err)
	}
	if owner != otherUID {
		t.Errorf("guest cart owner = %s, want other account %s", owner, otherUID)
	}
}

// TestAdoptCartNeverTakesAnotherAccountsCart calls the store directly with a
// cart another account owns, as a sign-in that lost a race for it would. The
// lines are sellable, so nothing but the ownership guard stands between that
// call and the other account's cart being merged away or taken over.
func TestAdoptCartNeverTakesAnotherAccountsCart(t *testing.T) {
	ctx := t.Context()
	accounts := account.NewStore(pool)
	ownVariant := sellableVariant(t, ctx)
	theirVariant := anotherSellableVariant(t, ctx, ownVariant)

	for _, tt := range []struct {
		name     string
		hasACart bool
	}{
		{"into the account's own cart", true},
		{"as the account's first cart", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := register(t, accounts, "adopt-owned-"+uuid.NewString()+"@example.com")
			other := register(t, accounts, "adopt-owned-other-"+uuid.NewString()+"@example.com")
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
	firstStore := cart.NewStore(accountStorePool(t, firstName))
	secondStore := cart.NewStore(accountStorePool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- firstStore.Adopt(ctx, firstUser.ID, guestCart) }()
	go func() { secondDone <- secondStore.Adopt(ctx, secondUser.ID, guestCart) }()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

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
