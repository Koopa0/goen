//go:build integration

package cart_test

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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/user"
)

func TestStoreRoleCanLockTheCreditAndCouponQuoteFacts(t *testing.T) {
	ctx := t.Context()
	code := insertCoupon(t, "ROLEQUOTE", "amount", 1000, 0, 0, 0)
	userID := creditedCustomer(t, 5000)
	s := cart.NewStore(storeRolePool(t))
	token := bearer.New()
	cartID, err := s.Create(ctx, token, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		t.Fatalf("store role creates cart: %v", err)
	}
	if err := s.Add(ctx, cartID, freshVariant(t, "store-role-quote"), 1); err != nil {
		t.Fatalf("store role adds cart line: %v", err)
	}
	shippingID := shipVersionFor(t, "home_delivery")
	addr := &delivery.Details{
		Email: "role-quote@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	owner := uuid.NullUUID{UUID: userID, Valid: true}
	shown := checkoutQuote(t, s, cartID, owner, shippingID, addr, code)
	if _, err := s.PlaceOrder(
		ctx, cartID, owner, shippingID, addr, nil, code, shown,
		checkoutAttemptKey("role-quote-"+uuid.NewString()),
	); err != nil {
		t.Fatalf("store role places locked credit+coupon quote: %v", err)
	}
}

// TestAChangedCreditBalanceReRendersCheckoutWithTheFreshFigure creates the
// interleaving behind store_credit_never_negative using the production guard:
// checkout reads the old committed balance, then waits behind a competing
// debit. Once that debit commits, the customer must see the new figure at 422,
// not a fictitious sold-out cart, and the same form can be submitted again.
func TestAChangedCreditBalanceReRendersCheckoutWithTheFreshFigure(t *testing.T) {
	ctx := t.Context()
	userID := creditedCustomer(t, 5000)
	app := "wave0-credit-" + uuid.NewString()
	appPool := applicationPool(t, app)
	s := cart.NewStore(appPool)
	token := bearer.New()
	id, err := s.Create(ctx, token, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		t.Fatalf("create customer cart: %v", err)
	}
	if addErr := s.Add(ctx, id, freshVariant(t, "credit-balance-race"), 1); addErr != nil {
		t.Fatalf("add: %v", addErr)
	}
	shipID := shipVersionFor(t, "home_delivery")
	key := checkoutAttemptKey("credit-race-" + uuid.NewString())
	checkoutAddress := &delivery.Details{
		Email: "credit-race@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 88 號",
	}
	owner := uuid.NullUUID{UUID: userID, Valid: true}
	shown := checkoutQuote(t, s, id, owner, shipID, checkoutAddress, "")
	form := url.Values{
		"email": {"credit-race@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":         {"松高路 88 號"},
		"shipping":       {shipID.String()},
		"checkout_quote": {shown.String()},
		"idempotency":    {key},
	}
	newRequest := func() *http.Request {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
		return req.WithContext(user.NewContext(req.Context(), user.User{
			ID: userID.String(), Email: "credit-race@example.com", Role: user.RoleCustomer,
		}))
	}

	competitor, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin competing debit: %v", err)
	}
	defer func() { _ = competitor.Rollback(ctx) }()
	if _, err := competitor.Exec(ctx, `
		SELECT post_store_credit($1, -4000, NULL, NULL, $2, NULL)`,
		userID, "order:competing-"+uuid.NewString()); err != nil {
		t.Fatalf("post competing debit: %v", err)
	}

	h := cart.NewHandler(s, access.NewStore(appPool, false), slog.New(slog.DiscardHandler), false, nil, nil)

	res := httptest.NewRecorder()
	done := make(chan error, 1)
	go func() {
		h.PlaceOrder(res, newRequest())
		done <- nil
	}()
	waitForApplicationLock(t, app, done)
	if err := competitor.Commit(ctx); err != nil {
		t.Fatalf("commit competing debit: %v", err)
	}
	<-done

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("changed credit answered %d, want 422; Location=%q body=%s",
			res.Code, res.Header().Get("Location"), res.Body.String())
	}
	body := res.Body.String()
	wantNotice := i18n.T(ctx, i18n.KeyCheckoutChanged)
	if !strings.Contains(body, wantNotice) {
		t.Errorf("checkout does not require confirmation of the fresh quote; want %q", wantNotice)
	}
	if !strings.Contains(body, money.TWD(1000)) {
		t.Errorf("checkout does not show the fresh NT$10 credit balance")
	}
	for _, preserved := range []string{"credit-race@example.com", "松高路 88 號", key} {
		if !strings.Contains(body, preserved) {
			t.Errorf("the 422 re-render lost submitted value %q", preserved)
		}
	}
	if loc := res.Header().Get("Location"); loc != "" {
		t.Errorf("changed credit redirected to %q instead of keeping checkout visible", loc)
	}

	form.Set("checkout_quote", checkoutQuote(t, s, id, owner, shipID, checkoutAddress, "").String())
	second := httptest.NewRecorder()
	h.PlaceOrder(second, newRequest())
	if second.Code != http.StatusSeeOther || second.Header().Get("Location") == "/cart" ||
		!strings.HasSuffix(second.Header().Get("Location"), "/pay") {
		t.Fatalf("second submission = %d Location %q, want the order's payment page",
			second.Code, second.Header().Get("Location"))
	}
}

// TestCheckoutQuoteGateLinearizesAfterTheCatalogueLock proves a quote is not a
// preflight-only checksum. The browser's old price is valid when submitted;
// checkout then waits on the variant row and must compare against the price that
// wins that lock, before creating any durable part of an order.
func TestCheckoutQuoteGateLinearizesAfterTheCatalogueLock(t *testing.T) {
	ctx := t.Context()
	variantID := freshVariant(t, "quote-price-race")
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = 100000 WHERE id = $1`, variantID); err != nil {
		t.Fatalf("set displayed price: %v", err)
	}
	baseStore := cart.NewStore(pool)
	cartID := newCart(t, baseStore)
	if err := baseStore.Add(ctx, cartID, variantID, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	shippingID := shipVersionFor(t, "home_delivery")
	addr := &delivery.Details{
		Email: "quote-race@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	appName := "checkout-quote-price-" + uuid.NewString()
	checkoutPool := applicationPool(t, appName)
	checkoutStore := cart.NewStore(checkoutPool)
	shown := checkoutQuote(
		t, checkoutStore, cartID, uuid.NullUUID{}, shippingID, addr, "",
	)

	change, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin price change: %v", err)
	}
	defer func() { _ = change.Rollback(context.WithoutCancel(ctx)) }()
	if _, lockErr := change.Exec(ctx,
		`SELECT 1 FROM product_variants WHERE id = $1 FOR UPDATE`, variantID); lockErr != nil {
		t.Fatalf("lock variant: %v", lockErr)
	}

	key := checkoutAttemptKey("quote-price-" + uuid.NewString())
	done := make(chan error, 1)
	go func() {
		_, placeErr := checkoutStore.PlaceOrder(
			ctx, cartID, uuid.NullUUID{}, shippingID, addr, nil, "", shown, key,
		)
		done <- placeErr
	}()
	waitForApplicationLock(t, appName, done)
	if _, updateErr := change.Exec(ctx,
		`UPDATE product_variants SET price_cents = 100001 WHERE id = $1`, variantID); updateErr != nil {
		t.Fatalf("change price: %v", updateErr)
	}
	if commitErr := change.Commit(ctx); commitErr != nil {
		t.Fatalf("commit price: %v", commitErr)
	}
	if placeErr := <-done; !errors.Is(placeErr, cart.ErrCheckoutChanged) {
		t.Fatalf("stale price returned %v, want ErrCheckoutChanged", placeErr)
	}

	var attempts, privateRows int
	if queryErr := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&attempts); queryErr != nil {
		t.Fatalf("count checkout attempts: %v", queryErr)
	}
	if queryErr := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_private_data WHERE email = $1`, addr.Email).Scan(&privateRows); queryErr != nil {
		t.Fatalf("count order private rows: %v", queryErr)
	}
	if attempts != 0 || privateRows != 0 {
		t.Fatalf("stale quote left %d attempt(s) and %d order row(s)", attempts, privateRows)
	}
	view, err := baseStore.View(ctx, cartID)
	if err != nil || len(view.Lines) != 1 {
		t.Fatalf("stale quote changed its cart: lines=%d err=%v", len(view.Lines), err)
	}
}

// TestCheckoutWaitsForAZoneRateEditInFlight: a surcharge is read from rows the
// back office edits in place, and orders_have_lines prices the order again
// from them at COMMIT. Checkout holds the zone lock shared from its quote to
// its COMMIT, and the edit holds it exclusively, so a checkout that meets an
// edit in flight waits, is then told the price changed, and places at the new
// one. Without the wait it would price from one surcharge and commit under
// another, and the storefront's order would be refused.
func TestCheckoutWaitsForAZoneRateEditInFlight(t *testing.T) {
	ctx := t.Context()
	var methodID, versionID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_methods (code, destination_kind, position)
		VALUES ($1, 'address', 99) RETURNING id`,
		"zone_lock_"+uuid.NewString()[:8]).Scan(&methodID); err != nil {
		t.Fatalf("create method: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(ctx),
			`UPDATE shipping_methods SET is_active = false WHERE id = $1`, methodID); err != nil {
			t.Errorf("withdraw method: %v", err)
		}
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_method_versions (method_id, name, fee_cents, effective_at)
		VALUES ($1, '離島費率', 8000, now() - interval '1 minute') RETURNING id`,
		methodID).Scan(&versionID); err != nil {
		t.Fatalf("create version: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO shipping_version_zones (version_id, zone_id, surcharge_cents)
		SELECT $1, z.id, 20000 FROM shipping_zones z WHERE z.code = 'offshore'`, versionID); err != nil {
		t.Fatalf("create surcharge: %v", err)
	}

	appName := "checkout-zone-rate-" + uuid.NewString()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse store-role pool config: %v", err)
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["application_name"] = appName
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, execErr := conn.Exec(ctx, `SET ROLE store`)
		return execErr
	}
	asStore, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open store-role pool: %v", err)
	}
	t.Cleanup(asStore.Close)
	checkoutStore := cart.NewStore(asStore)
	cartID := newCart(t, checkoutStore)
	if addErr := checkoutStore.Add(ctx, cartID, freshVariant(t, "zone-rate-race"), 1); addErr != nil {
		t.Fatalf("add: %v", addErr)
	}
	addr := &delivery.Details{
		Email: "zone-race@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "890", City: "金門縣", District: "金城鎮", Street: "民生路 1 號",
	}
	shown := checkoutQuote(t, checkoutStore, cartID, uuid.NullUUID{}, versionID, addr, "")

	edit, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rate edit: %v", err)
	}
	defer func() { _ = edit.Rollback(context.WithoutCancel(ctx)) }()
	// The back office's own lock, so a checkout taking another key fails here.
	if lockErr := db.New(edit).LockZoneRates(ctx); lockErr != nil {
		t.Fatalf("lock zone rates: %v", lockErr)
	}
	if _, updateErr := edit.Exec(ctx,
		`UPDATE shipping_version_zones SET surcharge_cents = 25000 WHERE version_id = $1`,
		versionID); updateErr != nil {
		t.Fatalf("edit surcharge: %v", updateErr)
	}

	done := make(chan error, 1)
	go func() {
		_, placeErr := checkoutStore.PlaceOrder(ctx, cartID, uuid.NullUUID{}, versionID, addr, nil, "",
			shown, checkoutAttemptKey("zone-rate-"+uuid.NewString()))
		done <- placeErr
	}()
	waitForApplicationLock(t, appName, done)
	if commitErr := edit.Commit(ctx); commitErr != nil {
		t.Fatalf("commit rate edit: %v", commitErr)
	}
	if placeErr := <-done; !errors.Is(placeErr, cart.ErrCheckoutChanged) {
		t.Fatalf("a checkout priced before the edit returned %v, want ErrCheckoutChanged", placeErr)
	}

	number, err := placeOrder(t, checkoutStore, ctx, cartID, uuid.NullUUID{}, versionID, addr, "",
		"zone-rate-again-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place at the new rate: %v", err)
	}
	var charged int64
	if err := pool.QueryRow(ctx,
		`SELECT shipping_cents FROM orders WHERE order_number = $1`, number).Scan(&charged); err != nil {
		t.Fatalf("read the order: %v", err)
	}
	if charged != 33000 {
		t.Errorf("the order was charged %d for delivery, want the new 33000", charged)
	}
}

func TestCheckoutCouponDefinitionIsReloadedUnderItsRowLock(t *testing.T) {
	ctx := t.Context()
	code := insertCoupon(t, "LOCKEDCOUPON", "amount", 1000, 0, 0, 0)
	baseStore := cart.NewStore(pool)
	cartID := newCart(t, baseStore)
	if err := baseStore.Add(ctx, cartID, freshVariant(t, "coupon-definition-race"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	shippingID := shipVersionFor(t, "home_delivery")
	addr := &delivery.Details{
		Email: "coupon-race@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	appName := "checkout-coupon-definition-" + uuid.NewString()
	checkoutPool := applicationPool(t, appName)
	checkoutStore := cart.NewStore(checkoutPool)
	shown := checkoutQuote(
		t, checkoutStore, cartID, uuid.NullUUID{}, shippingID, addr, code,
	)

	change, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin coupon change: %v", err)
	}
	defer func() { _ = change.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := change.Exec(ctx,
		`SELECT 1 FROM coupons WHERE code = $1 FOR UPDATE`, code); err != nil {
		t.Fatalf("lock coupon: %v", err)
	}

	key := checkoutAttemptKey("coupon-definition-" + uuid.NewString())
	done := make(chan error, 1)
	go func() {
		_, placeErr := checkoutStore.PlaceOrder(
			ctx, cartID, uuid.NullUUID{}, shippingID, addr, nil, code, shown, key,
		)
		done <- placeErr
	}()
	waitForApplicationLock(t, appName, done)
	if _, err := change.Exec(ctx,
		`UPDATE coupons SET is_active = false WHERE code = $1`, code); err != nil {
		t.Fatalf("deactivate coupon: %v", err)
	}
	if err := change.Commit(ctx); err != nil {
		t.Fatalf("commit coupon change: %v", err)
	}
	if err := <-done; !errors.Is(err, coupon.ErrNotFound) {
		t.Fatalf("deactivated coupon returned %v, want coupon.ErrNotFound", err)
	}
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("deactivated coupon left %d checkout attempt(s)", attempts)
	}
}

func TestCheckoutRejectsMissingOrMalformedQuoteWithoutWrites(t *testing.T) {
	ctx := t.Context()
	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "missing"},
		{name: "malformed", value: "not-a-fixed-checkout-quote"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := cart.NewStore(pool)
			token := bearer.New()
			cartID, err := s.Create(ctx, token, uuid.NullUUID{})
			if err != nil {
				t.Fatalf("create cart: %v", err)
			}
			if err := s.Add(ctx, cartID, freshVariant(t, "bad-quote-"+tt.name), 1); err != nil {
				t.Fatalf("add: %v", err)
			}
			shippingID := shipVersionFor(t, "home_delivery")
			key := checkoutAttemptKey("bad-quote-" + uuid.NewString())
			form := url.Values{
				"email": {"quote@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
				"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
				"street":         {"松高路 1 號"},
				"shipping":       {shippingID.String()},
				"checkout_quote": {tt.value},
				"idempotency":    {key},
			}
			req := httptest.NewRequestWithContext(
				ctx, http.MethodPost, "/checkout", strings.NewReader(form.Encode()),
			)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			//nolint:gosec // G124: the browser's own cart cookie
			req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
			h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil, nil)

			res := httptest.NewRecorder()
			h.PlaceOrder(res, req)

			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("response = %d, want 422; body=%s", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), `name="checkout_quote" value="`) ||
				strings.Contains(res.Body.String(), `value="`+tt.value+`"`) && tt.value != "" {
				t.Error("422 did not replace the submitted value with a fresh quote ID")
			}
			var attempts int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).
				Scan(&attempts); err != nil {
				t.Fatalf("count attempts: %v", err)
			}
			if attempts != 0 {
				t.Fatalf("malformed quote wrote %d checkout attempt(s)", attempts)
			}
		})
	}
}

// TestRetiringAProductLinearizesBeforeStaleCartCheckout proves that publication
// is part of the checkout snapshot, not merely a listing concern. Checkout
// first owns the variant and waits behind an in-flight retirement's product
// lock; once retirement commits it must observe archived and refuse the whole
// cart without silently dropping a line or creating an order.
func TestRetiringAProductLinearizesBeforeStaleCartCheckout(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	variantID := freshVariant(t, "archived-stale-cart")
	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE id = $1`, variantID).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	cartID := newCart(t, s)
	if err := s.Add(ctx, cartID, variantID, 1); err != nil {
		t.Fatalf("add active product: %v", err)
	}
	stockBefore := stockOf(t, variantID)
	shippingID := shipVersionFor(t, "home_delivery")

	retirement, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin retirement: %v", err)
	}
	defer func() { _ = retirement.Rollback(context.WithoutCancel(ctx)) }()
	if _, lockErr := retirement.Exec(ctx,
		`SELECT 1 FROM products WHERE id = $1 FOR UPDATE`, productID); lockErr != nil {
		t.Fatalf("lock retiring product: %v", lockErr)
	}

	checkoutPool := applicationPool(t, "checkout-behind-product-retirement")
	checkoutStore := cart.NewStore(checkoutPool)
	checkoutAddress := &delivery.Details{
		Email: "retired@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shown := checkoutQuote(
		t, checkoutStore, cartID, uuid.NullUUID{}, shippingID, checkoutAddress, "",
	)
	checkoutDone := make(chan error, 1)
	go func() {
		_, placeErr := checkoutStore.PlaceOrder(
			ctx, cartID, uuid.NullUUID{}, shippingID, checkoutAddress, nil, "", shown,
			checkoutAttemptKey("retired-product:"+uuid.NewString()))
		checkoutDone <- placeErr
	}()
	waitForApplicationLock(t, "checkout-behind-product-retirement", checkoutDone)

	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin variant probe: %v", err)
	}
	_, probeErr := probe.Exec(ctx,
		`SELECT 1 FROM product_variants WHERE id = $1 FOR UPDATE NOWAIT`, variantID)
	if rollbackErr := probe.Rollback(ctx); rollbackErr != nil {
		t.Fatalf("rollback variant probe: %v", rollbackErr)
	}
	pgErr, locked := errors.AsType[*pgconn.PgError](probeErr)
	if !locked || pgErr.Code != "55P03" {
		t.Fatalf("checkout did not lock variant before waiting for product: %v", probeErr)
	}

	if _, archiveErr := retirement.Exec(ctx,
		`UPDATE products SET status = 'archived' WHERE id = $1`, productID); archiveErr != nil {
		t.Fatalf("archive product: %v", archiveErr)
	}
	if commitErr := retirement.Commit(ctx); commitErr != nil {
		t.Fatalf("commit retirement: %v", commitErr)
	}
	if checkoutErr := <-checkoutDone; !errors.Is(checkoutErr, cart.ErrUnavailable) {
		t.Fatalf("checkout after retirement returned %v, want ErrUnavailable", checkoutErr)
	}

	view, err := s.View(ctx, cartID)
	if err != nil {
		t.Fatalf("view stale cart: %v", err)
	}
	if len(view.Lines) != 1 || !view.Lines[0].Unavailable {
		t.Errorf("archived product cart view = %+v, want one unavailable line", view.Lines)
	}
	if got := stockOf(t, variantID); got != stockBefore {
		t.Errorf("retired checkout moved stock from %d to %d", stockBefore, got)
	}
	var orders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_lines WHERE variant_id = $1`, variantID).Scan(&orders); err != nil {
		t.Fatalf("count orders for retired variant: %v", err)
	}
	if orders != 0 {
		t.Errorf("retired variant appears on %d orders, want none", orders)
	}
}

// TestCheckoutAndFeaturedVariantMutationShareTheCatalogueLockOrder pins the
// variant -> product contract. The admin transaction owns the variant while
// checkout waits for it, then runs the campaign trigger that locks the product.
// If checkout had locked product before waiting for variant, this is an ABBA
// deadlock; with the shared order the mutation completes and checkout follows.
func TestCheckoutAndFeaturedVariantMutationShareTheCatalogueLockOrder(t *testing.T) {
	ctx := t.Context()
	variantID := freshVariant(t, "featured-checkout-lock-order")
	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE id = $1`, variantID).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET compare_at_price_cents = price_cents + 100000 WHERE id = $1`,
		variantID); err != nil {
		t.Fatalf("discount featured variant: %v", err)
	}
	var campaignID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO sale_campaigns (slug, title, ends_at)
		VALUES ($1, '鎖序測試', now() + interval '1 day') RETURNING id`,
		"lock-order-"+uuid.NewString()[:8]).Scan(&campaignID); err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sale_campaign_products (campaign_id, product_id)
		VALUES ($1, $2)`, campaignID, productID); err != nil {
		t.Fatalf("feature product: %v", err)
	}

	s := cart.NewStore(pool)
	cartID := newCart(t, s)
	if err := s.Add(ctx, cartID, variantID, 1); err != nil {
		t.Fatalf("add featured variant: %v", err)
	}
	shippingID := shipVersionFor(t, "home_delivery")

	adminTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin variant mutation: %v", err)
	}
	defer func() { _ = adminTx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := adminTx.Exec(ctx,
		`SELECT 1 FROM product_variants WHERE id = $1 FOR UPDATE`, variantID); err != nil {
		t.Fatalf("lock variant: %v", err)
	}

	checkoutPool := applicationPool(t, "checkout-behind-featured-variant")
	checkoutStore := cart.NewStore(checkoutPool)
	checkoutAddress := &delivery.Details{
		Email: "featured@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shown := checkoutQuote(
		t, checkoutStore, cartID, uuid.NullUUID{}, shippingID, checkoutAddress, "",
	)
	checkoutDone := make(chan error, 1)
	go func() {
		_, placeErr := checkoutStore.PlaceOrder(
			ctx, cartID, uuid.NullUUID{}, shippingID, checkoutAddress, nil, "", shown,
			checkoutAttemptKey("featured-lock-order:"+uuid.NewString()))
		checkoutDone <- placeErr
	}()
	waitForApplicationLock(t, "checkout-behind-featured-variant", checkoutDone)

	// This AFTER trigger takes the product row. It must not wait on checkout,
	// which is queued behind this transaction's variant lock and therefore must
	// not already own any product root.
	if _, err := adminTx.Exec(ctx, `
		UPDATE product_variants
		SET compare_at_price_cents = compare_at_price_cents + 1
		WHERE id = $1`, variantID); err != nil {
		t.Fatalf("campaign-safe variant mutation deadlocked with checkout: %v", err)
	}
	select {
	case err := <-checkoutDone:
		t.Fatalf("checkout passed the uncommitted variant mutation: %v", err)
	default:
	}
	if err := adminTx.Commit(ctx); err != nil {
		t.Fatalf("commit variant mutation: %v", err)
	}
	if err := <-checkoutDone; err != nil {
		t.Fatalf("checkout after variant mutation: %v", err)
	}
}

// TestCouponMinimumIsRecheckedWhenCheckoutIsPlaced holds the order transaction
// after the handler has priced its view, then changes the catalogue price. The
// order must apply the minimum to the subtotal it reads inside its transaction,
// not to the earlier view or to state retained by Coupon.
func TestCouponMinimumIsRecheckedWhenCheckoutIsPlaced(t *testing.T) {
	ctx := t.Context()
	appName := "coupon-minimum-" + uuid.NewString()
	s := cart.NewStore(applicationPool(t, appName))

	vid := freshVariant(t, "coupon-minimum-race")
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = 200000 WHERE id = $1`, vid); err != nil {
		t.Fatalf("set preview price: %v", err)
	}
	code := "MINRACE" + strings.ToUpper(uuid.NewString()[:6])
	insertCoupon(t, code, "amount", 10000, 0, 200000, 0)

	token := bearer.New()
	cartID, createErr := s.Create(ctx, token, uuid.NullUUID{})
	if createErr != nil {
		t.Fatalf("create cart: %v", createErr)
	}
	if err := s.Add(ctx, cartID, vid, 1); err != nil {
		t.Fatalf("add item: %v", err)
	}
	preview, viewErr := s.View(ctx, cartID)
	if viewErr != nil {
		t.Fatalf("read preview: %v", viewErr)
	}
	if preview.SubtotalCents != 200000 {
		t.Fatalf("preview subtotal = %d, want 200000", preview.SubtotalCents)
	}
	definition, couponErr := s.CouponByCode(ctx, code)
	if couponErr != nil {
		t.Fatalf("find coupon for preview: %v", couponErr)
	}
	if _, _, err := definition.Apply(preview.SubtotalCents, false); err != nil {
		t.Fatalf("coupon does not qualify in the preview: %v", err)
	}

	shipID := shipVersionFor(t, "home_delivery")
	key := checkoutAttemptKey("coupon-minimum-" + uuid.NewString())
	var ordersBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders`).Scan(&ordersBefore); err != nil {
		t.Fatalf("count orders before checkout: %v", err)
	}

	// Hold the first statement of Store.PlaceOrder. Reaching this lock proves the
	// handler has already built the 200000-cent view and accepted the coupon.
	blocker, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin checkout blocker: %v", beginErr)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, key); err != nil {
		t.Fatalf("hold checkout key: %v", err)
	}

	form := url.Values{
		"email": {"minimum@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":         {"松仁路 200 號"},
		"shipping":       {shipID.String()},
		"coupon":         {code},
		"checkout_quote": {checkoutQuote(t, s, cartID, uuid.NullUUID{}, shipID, &delivery.Details{PostalCode: "110"}, code).String()},
		"idempotency":    {key},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test

	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil, nil)

	res := httptest.NewRecorder()
	served := make(chan error, 1)
	go func() {
		h.PlaceOrder(res, req)
		served <- nil
	}()
	waitForApplicationLock(t, appName, served)

	// The next statement in PlaceOrder reads the cart at READ COMMITTED, so it
	// must see this current price and re-apply the coupon minimum to 199999.
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = 199999 WHERE id = $1`, vid); err != nil {
		t.Fatalf("lower price before transactional pricing: %v", err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release checkout: %v", err)
	}
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("checkout did not finish after its key was released")
	}

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("below-minimum checkout answered %d, want 422", res.Code)
	}
	body := res.Body.String()
	for _, want := range []string{
		i18n.T(ctx, i18n.KeyCouponBelowMinimum),
		"minimum@example.com",
		"松仁路 200 號",
		code,
		money.TWD(199999),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("re-rendered checkout does not contain %q", want)
		}
	}
	if strings.Contains(body, "測試折扣") {
		t.Error("rejected coupon is still rendered as applied")
	}
	if strings.Contains(body, "-"+money.TWD(10000)) {
		t.Error("rejected coupon's stale discount is still included in the summary")
	}

	var ordersAfter, redemptions, attempts, cartLines int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders`).Scan(&ordersAfter); err != nil {
		t.Fatalf("count orders after refusal: %v", err)
	}
	if ordersAfter != ordersBefore {
		t.Errorf("refused checkout left %d new orders", ordersAfter-ordersBefore)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM coupon_redemptions cr
		JOIN coupons c ON c.id = cr.coupon_id WHERE c.code = $1`, code).Scan(&redemptions); err != nil {
		t.Fatalf("count redemptions: %v", err)
	}
	if redemptions != 0 {
		t.Errorf("refused checkout left %d coupon redemptions", redemptions)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&attempts); err != nil {
		t.Fatalf("count checkout attempts: %v", err)
	}
	if attempts != 0 {
		t.Errorf("refused checkout left %d idempotency records", attempts)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM cart_items WHERE cart_id = $1`, cartID).Scan(&cartLines); err != nil {
		t.Fatalf("count cart lines: %v", err)
	}
	if cartLines != 1 {
		t.Errorf("refused checkout left %d cart lines, want the original one", cartLines)
	}
}
