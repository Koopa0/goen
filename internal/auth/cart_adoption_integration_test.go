//go:build integration

package auth_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
)

// TestHandler_SignInMergedCart: SignIn adopts the guest cart, and GET /cart
// with the guest cookie that now names a deleted row shows the merged account
// cart. This is the storefront path: sign-in adopt deletes the guest row, the
// browser cookie still names it, and /cart must show the surviving account
// cart.
func TestHandler_SignInMergedCart(t *testing.T) {
	ctx := t.Context()
	accounts := auth.NewStore(pool)
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

	carts := cart.NewHandler(cart.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

	h := auth.NewHandler(accounts, carts, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

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

// TestHandler_SignInFailedAdoption: when cart adoption fails reversibly (the
// guest cart is locked), SignIn still signs the customer in and lands on the
// recovery page for any safe continuation, and both carts stay intact until
// RetryCartAdoption succeeds.
func TestHandler_SignInFailedAdoption(t *testing.T) {
	ctx := t.Context()
	accounts := auth.NewStore(pool)
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

	carts := cart.NewHandler(cart.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

	h := auth.NewHandler(accounts, carts, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

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

// TestHandler_AdoptForeignGuestCart: SignIn and RetryCartAdoption never adopt
// or recover a guest cart already attached to another account; both land where
// the customer was going and the other account's cart keeps its owner and
// lines. The browser's token names no cart this account may use, so there is
// nothing to adopt and nothing to recover.
func TestHandler_AdoptForeignGuestCart(t *testing.T) {
	ctx := t.Context()
	accounts := auth.NewStore(pool)
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

	carts := cart.NewHandler(cart.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

	h := auth.NewHandler(accounts, carts, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

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
