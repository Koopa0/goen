//go:build integration

package cart_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
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
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/ui/pages"
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

func variantOf(t *testing.T, slug string, sellable bool) uuid.UUID {
	t.Helper()
	cmp := ">"
	if !sellable {
		cmp = "<="
	}
	var id uuid.UUID
	err := pool.QueryRow(t.Context(), `
		SELECT pv.id FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.slug = $1 AND pv.is_active
		  AND pv.stock_quantity `+cmp+` pv.safety_stock
		ORDER BY pv.position LIMIT 1`, slug).Scan(&id)
	if err != nil {
		t.Fatalf("no %v variant for %q: %v", sellable, slug, err)
	}
	return id
}

func newCart(t *testing.T, s *cart.Store) uuid.UUID {
	t.Helper()
	tok, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	id, err := s.Create(t.Context(), tok, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	return id
}

func applicationPool(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse application pool config: %v", err)
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["application_name"] = name
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open application pool: %v", err)
	}
	var actual string
	if err := p.QueryRow(t.Context(), `SHOW application_name`).Scan(&actual); err != nil {
		p.Close()
		t.Fatalf("read application name: %v", err)
	}
	if actual != name {
		p.Close()
		t.Fatalf("application name = %q, want %q", actual, name)
	}
	t.Cleanup(p.Close)
	return p
}

func waitForApplicationLock(t *testing.T, name string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("%s returned before reaching the intended database lock: %v", name, err)
		default:
		}
		var waiting bool
		err := pool.QueryRow(t.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE application_name = $1 AND wait_event_type = 'Lock'
			)`, name).Scan(&waiting)
		if err == nil && waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never blocked on the intended database lock: %v", name, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAnInfrastructureFailureIsNotReportedAsSoldOut(t *testing.T) {
	ctx := t.Context()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse timeout pool config: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "1500"
	timeoutPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open timeout pool: %v", err)
	}
	t.Cleanup(timeoutPool.Close)
	s := cart.NewStore(timeoutPool)
	id := newCart(t, s)
	vid := freshVariant(t, "infra-is-not-sold-out")
	if addErr := s.Add(ctx, id, vid, 1); addErr != nil {
		t.Fatalf("add: %v", addErr)
	}

	blocker, err := pgx.Connect(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Close(context.Background()) }) //nolint:usetesting // cleanup runs after t.Context is canceled
	tx, err := blocker.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, lockErr := tx.Exec(ctx, `LOCK TABLE inventory_movements IN ACCESS EXCLUSIVE MODE`); lockErr != nil {
		t.Fatalf("lock inventory ledger: %v", lockErr)
	}

	addr := &cart.Address{
		Email: "infra@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	_, err = s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipVersionFor(t, "home_delivery"),
		addr, nil, nil, "infra-not-sold-out-"+uuid.NewString())
	if errors.Is(err, cart.ErrUnavailable) {
		t.Fatalf("a statement timeout was reported as sold-out inventory: %v", err)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "57014" {
		t.Fatalf("hold failure = %v, want preserved PgError 57014", err)
	}
	if !strings.Contains(err.Error(), "hold 1 of variant "+vid.String()) {
		t.Fatalf("timeout landed outside the intended inventory hold: %v", err)
	}
}

func TestInventoryConstraintIsReportedAsSoldOut(t *testing.T) {
	ctx := t.Context()
	app := "wave0-inventory-" + uuid.NewString()
	appPool := applicationPool(t, app)
	s := cart.NewStore(appPool)
	vid := freshVariant(t, "named-inventory-refusal")
	if _, err := pool.Exec(ctx, `
		SELECT record_inventory_movement($1, -9, 'adjustment', $2, NULL, NULL, NULL)`,
		vid, "leave-one:"+vid.String()); err != nil {
		t.Fatalf("leave one unit: %v", err)
	}
	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add last unit: %v", err)
	}

	blockOrder := commitBareOrder(t)
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
		blockOrder, vid, "take-last:"+vid.String()); err != nil {
		t.Fatalf("hold the last unit: %v", err)
	}

	addr := &cart.Address{
		Email: "soldout@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shipID := shipVersionFor(t, "home_delivery")
	done := make(chan error, 1)
	go func() {
		_, placeErr := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID,
			addr, nil, nil, "named-inventory-"+uuid.NewString())
		done <- placeErr
	}()
	waitForApplicationLock(t, app, done)
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("commit competing hold: %v", err)
	}
	placeErr := <-done
	if !errors.Is(placeErr, cart.ErrUnavailable) {
		t.Fatalf("inventory_never_negative mapped to %v, want ErrUnavailable", placeErr)
	}

	// Bind the fixture's exhausted state to the database's typed answer. The
	// store intentionally returns the bare domain sentinel after checking this
	// name, so the PgError itself is verified through the same production door.
	_, namedErr := pool.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
		commitBareOrder(t), vid, "prove-empty:"+vid.String())
	pgErr, ok := errors.AsType[*pgconn.PgError](namedErr)
	if !ok || pgErr.ConstraintName != "inventory_never_negative" {
		t.Fatalf("exhausted fixture was refused by %v, want inventory_never_negative", namedErr)
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
	token, err := cart.NewToken()
	if err != nil {
		t.Fatalf("new cart token: %v", err)
	}
	id, err := s.Create(ctx, token, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		t.Fatalf("create customer cart: %v", err)
	}
	if addErr := s.Add(ctx, id, freshVariant(t, "credit-balance-race"), 1); addErr != nil {
		t.Fatalf("add: %v", addErr)
	}
	shipID := shipVersionFor(t, "home_delivery")
	key := "credit-race-" + uuid.NewString()
	form := url.Values{
		"email": {"credit-race@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":      {"松高路 88 號"},
		"shipping":    {shipID.String()},
		"idempotency": {key},
	}
	newRequest := func() *http.Request {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		//nolint:gosec // G124: the browser's own cart cookie, read by this handler
		req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
		return req.WithContext(account.WithUser(req.Context(), account.User{
			ID: userID.String(), Email: "credit-race@example.com", Role: "customer",
		}))
	}

	competitor, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin competing debit: %v", err)
	}
	defer func() { _ = competitor.Rollback(ctx) }()
	if _, err := competitor.Exec(ctx, `
		SELECT post_store_credit($1, -4000, 'competing spend', NULL, $2, NULL)`,
		userID, "competing:"+uuid.NewString()); err != nil {
		t.Fatalf("post competing debit: %v", err)
	}

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}), nil)
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
	wantNotice := fmt.Sprintf(i18n.T(ctx, i18n.KeyCreditChanged), pages.TWD(1000))
	if !strings.Contains(body, wantNotice) {
		t.Errorf("checkout does not name the fresh NT$10 balance; want %q", wantNotice)
	}
	for _, preserved := range []string{"credit-race@example.com", "松高路 88 號", key} {
		if !strings.Contains(body, preserved) {
			t.Errorf("the 422 re-render lost submitted value %q", preserved)
		}
	}
	if loc := res.Header().Get("Location"); loc != "" {
		t.Errorf("changed credit redirected to %q instead of keeping checkout visible", loc)
	}

	second := httptest.NewRecorder()
	h.PlaceOrder(second, newRequest())
	if second.Code != http.StatusSeeOther || second.Header().Get("Location") == "/cart" ||
		!strings.HasSuffix(second.Header().Get("Location"), "/pay") {
		t.Fatalf("second submission = %d Location %q, want the order's payment page",
			second.Code, second.Header().Get("Location"))
	}
}

func TestCartIsFoundByTokenNotByID(t *testing.T) {
	s := cart.NewStore(pool)
	tok, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	id, err := s.Create(t.Context(), tok, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.Find(t.Context(), tok)
	if err != nil || got != id {
		t.Fatalf("Find(token) = %v/%v, want %v", got, err, id)
	}
	if _, err := s.Find(t.Context(), tok+"x"); err == nil {
		t.Error("a near-miss token found a cart")
	}
	if _, err := s.Find(t.Context(), ""); err == nil {
		t.Error("an empty token found a cart")
	}

	var stored []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT token_hash FROM carts WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read token_hash: %v", err)
	}
	if string(stored) == tok {
		t.Error("the cart table stores the raw token; a leak would hand over live cart cookies")
	}
}

func TestAddRefusesWhatCannotBeSold(t *testing.T) {
	s := cart.NewStore(pool)
	id := newCart(t, s)

	unsellable := variantOf(t, "meridian-book-14", false) // wholly sold out in the seed
	err := s.Add(t.Context(), id, unsellable, 1)
	if err == nil {
		t.Fatal("a sold-out variant was added to a cart")
	}
	if !errors.Is(err, cart.ErrUnavailable) {
		t.Errorf("refused with %v, want ErrUnavailable — the store must recognise this "+
			"before the write, not leave it to a constraint violation", err)
	}

	ok := variantOf(t, "pixelight-9-pro", true)
	if err := s.Add(t.Context(), id, ok, 1); err != nil {
		t.Fatalf("a sellable variant was refused: %v", err)
	}
}

func TestAddClampsToWhatCanBeSupplied(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	vid := freshVariant(t, "stockfix-1")
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

	// 3 can be sold: 5 on hand less the floor of 2.
	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 10); err != nil {
		t.Fatalf("add: %v", err)
	}

	var stored int32
	if err := pool.QueryRow(ctx,
		`SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
		id, vid).Scan(&stored); err != nil {
		t.Fatalf("read line: %v", err)
	}
	if stored != 3 {
		t.Errorf("asked for 10 of a variant with 3 sellable, cart holds %d; want 3", stored)
	}
}

func TestCartShowsCurrentPriceAndAvailability(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	vid := variantOf(t, "pixelight-9-pro", true)

	if err := s.Add(ctx, id, vid, 2); err != nil {
		t.Fatalf("add: %v", err)
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(view.Lines) != 1 {
		t.Fatalf("cart holds %d lines, want 1", len(view.Lines))
	}
	if view.Lines[0].Unavailable || view.Lines[0].Short {
		t.Fatal("a freshly added, in-stock line reads as unavailable")
	}
	if view.ItemCount != 2 {
		t.Errorf("item count = %d, want 2 — the badge counts units, not lines", view.ItemCount)
	}
	want := view.Lines[0].UnitCents * 2
	if view.SubtotalCents != want {
		t.Errorf("subtotal = %d, want %d", view.SubtotalCents, want)
	}
}

func TestPlaceOrderIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}

	addr := &cart.Address{
		Email: "idem@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	const key = "idem-key-test-1"

	first, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil, key)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	second, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil, key)
	if err != nil {
		t.Fatalf("replace with the same key: %v", err)
	}
	if first != second {
		t.Errorf("the same idempotency key produced two orders: %q then %q", first, second)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if n != 1 {
		t.Errorf("checkout_attempts holds %d rows for one key, want 1", n)
	}
}

func TestPlaceOrderEmptiesTheCart(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "empty@example.com", Name: "李大華", Phone: "0987654321",
		PostalCode: "220", City: "新北市", District: "板橋區", Street: "文化路一段 1 號",
	}
	if _, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil, "empties-cart-1"); err != nil {
		t.Fatalf("place: %v", err)
	}

	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(view.Lines) != 0 {
		t.Errorf("the cart still holds %d lines after the order was placed", len(view.Lines))
	}
}

func TestPlaceOrderRefusesAFabricatedShippingVersion(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	addr := &cart.Address{
		Email: "x@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	if _, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, uuid.New(), addr, nil, nil, "fabricated-1"); err == nil {
		t.Error("an order was placed against a shipping version that does not exist")
	}
}

func TestPlaceOrderRefusesAnEmptyCart(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "x@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	if _, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil, "empty-cart-1"); err == nil {
		t.Error("an order was placed from an empty cart")
	}
}

func TestOrderPricesAreCopiedNotReferenced(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	vid := variantOf(t, "koto-over-ear", true)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "price@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil, "copied-price-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	before, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = price_cents + 100000 WHERE id = $1`, vid); err != nil {
		t.Fatalf("reprice: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), //nolint:usetesting // t.Context is already cancelled in Cleanup
			`UPDATE product_variants SET price_cents = price_cents - 100000 WHERE id = $1`, vid)
	})

	after, readErr := s.Order(ctx, number)
	if readErr != nil {
		t.Fatalf("re-read order: %v", readErr)
	}
	if before.SubtotalCents != after.SubtotalCents {
		t.Errorf("the order's subtotal moved with the catalogue: %d then %d",
			before.SubtotalCents, after.SubtotalCents)
	}
}

func TestOrderConfirmationIsNotEnumerable(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "enumerate@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil, "enum-test-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		nil)

	stranger := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	stranger.SetPathValue("number", number)
	res := httptest.NewRecorder()
	h.OrderPage(res, stranger)

	if res.Code != http.StatusNotFound {
		t.Errorf("a stranger reached the confirmation page: status %d, want 404", res.Code)
	}
	if strings.Contains(res.Body.String(), "enumerate@example.com") {
		t.Error("the confirmation page leaked the customer's email to a stranger")
	}
	if strings.Contains(res.Body.String(), "松高路") {
		t.Error("the confirmation page leaked the delivery address to a stranger")
	}

	placer := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	placer.SetPathValue("number", number)
	placer.AddCookie(placedCookie(t, s, number))
	ok := httptest.NewRecorder()
	h.OrderPage(ok, placer)

	if ok.Code != http.StatusOK {
		t.Fatalf("the browser that placed the order got %d, want 200", ok.Code)
	}
	if !strings.Contains(ok.Body.String(), number) {
		t.Error("the confirmation page does not name the order it confirms")
	}
}

func TestOrderIsAttachedToASignedInCustomer(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"owner-"+uuid.NewString()+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "owned@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{UUID: userID, Valid: true},
		shipID, addr, nil, nil, "owned-test-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	owns, err := s.OrderBelongsTo(ctx, number, userID.String())
	if err != nil {
		t.Fatalf("check ownership: %v", err)
	}
	if !owns {
		t.Error("a signed-in customer's order is not attached to their account")
	}

	var otherID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"other-"+uuid.NewString()+"@example.com").Scan(&otherID); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	otherOwns, otherErr := s.OrderBelongsTo(ctx, number, otherID.String())
	if otherErr != nil {
		t.Fatalf("check other ownership: %v", otherErr)
	}
	if otherOwns {
		t.Error("the order is reported as belonging to a different account")
	}
}

func TestCheckoutHoldsStock(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	vid := variantOf(t, "koto-over-ear", true)
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		vid).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3 WHERE id = $1`,
			vid, wasStock, wasSafety)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 3, safety_stock = 2 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "hold@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	// The delta, not the absolute figure: other tests in this package hold stock too.
	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil, "hold-test-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if before-after != 1 {
		t.Errorf("stock went %d to %d over an order for one unit; the order held %d",
			before, after, before-after)
	}

	var reserved int32
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(ir.quantity), 0) FROM inventory_reservations ir
		JOIN orders o ON o.id = ir.order_id
		WHERE o.order_number = $1 AND ir.variant_id = $2 AND ir.state = 'held'`,
		number, vid).Scan(&reserved); err != nil {
		t.Fatalf("read reservations: %v", err)
	}
	if reserved != 1 {
		t.Errorf("order %s holds %d units, want 1", number, reserved)
	}

	var movements int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_movements
		WHERE variant_id = $1 AND reason = 'hold' AND idempotency_key LIKE 'hold:%'
		  AND source_id = (SELECT id FROM orders WHERE order_number = $2)`,
		vid, number).Scan(&movements); err != nil {
		t.Fatalf("read movements: %v", err)
	}
	if movements != 1 {
		t.Errorf("%d movements recorded for the hold, want 1", movements)
	}
}

// TestTwoOrdersCannotTakeTheSameLastUnit is the race the hold exists to lose
// safely. T1's transaction is held OPEN while T2 runs, and both orders are
// created first because next_order_number() locks the per-day counter row.
func TestTwoOrdersCannotTakeTheSameLastUnit(t *testing.T) {
	ctx := t.Context()

	vid := variantOf(t, "nimbus-band-2", true)
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		vid).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		//nolint:usetesting // t.Context is already cancelled in Cleanup
		_, _ = pool.Exec(context.Background(),
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3 WHERE id = $1`,
			vid, wasStock, wasSafety)
	})
	// Exactly one unit may be sold: 3 on hand, floor of 2.
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 3, safety_stock = 2 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	order1 := commitBareOrder(t)
	order2 := commitBareOrder(t)

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin t1: %v", err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	if _, holdErr := tx1.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
		order1, vid, "race-t1"); holdErr != nil {
		t.Fatalf("t1 hold: %v", holdErr)
	}

	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin t2: %v", err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()

	// T2's backend id, read BEFORE the racing statement.
	var pid int
	if err := tx2.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read t2 pid: %v", err)
	}

	done := make(chan error, 1)
	// t.Context() and not context.Background(): the goroutine must be cancelled
	// with the test.
	raceCtx := t.Context()
	go func() {
		_, holdErr := tx2.Exec(raceCtx,
			`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
			order2, vid, "race-t2")
		done <- holdErr
	}()

	// Wait until T2 is ACTUALLY blocked rather than pausing: on a loaded machine
	// T1 would commit first and the case would prove nothing about contention.
	waitUntilBlocked(t, pid, done)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit t1: %v", err)
	}

	select {
	case holdErr := <-done:
		if holdErr == nil {
			t.Fatal("both orders held the same last unit; the stock floor did not bite")
		}
		// WHICH rule refused matters, and the name is on PgError rather than in the
		// message text.
		pgErr, ok := errors.AsType[*pgconn.PgError](holdErr)
		if !ok || pgErr.ConstraintName != "inventory_never_negative" {
			t.Errorf("t2 was refused by %v, want constraint inventory_never_negative", holdErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("t2 never returned; the two holds are not contending for the same row")
	}
}

// commitBareOrder writes the minimum an order needs to exist, and commits it.
func commitBareOrder(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code, shipping_method_name)
		SELECT next_order_number(), v.id, sm.code, v.name
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'RACE-SKU', '測試', 100000, 1)`, id); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		id); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit order: %v", err)
	}
	return id
}

// heldOrder writes an order holding one unit of vid whose hold expired `ago` in
// the past. paid decides whether it is funded.
func heldOrder(t *testing.T, vid uuid.UUID, ago time.Duration, paid bool) (orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, 'SWEEP-SKU', '測試商品', 100000, 1)`, orderID, vid); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'w@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	// Held normally, then aged — BOTH timestamps move, because expires_at >
	// created_at is a CHECK and a hold that expired an hour ago was taken before it.
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
		orderID, vid, "sweep:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - $2::interval - interval '30 minutes',
		    expires_at = now() - $2::interval
		WHERE order_id = $1`, orderID, ago.String()); err != nil {
		t.Fatalf("age the hold: %v", err)
	}
	if paid {
		if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`,
			orderID, "cs_sweep_"+number); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`,
			"cs_sweep_"+number); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return orderID
}

func TestSweepReturnsAbandonedHoldsToTheShelf(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-2")

	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	orderID := heldOrder(t, vid, time.Hour, false)

	var mid int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&mid); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if before-mid != 1 {
		t.Fatalf("holding one unit moved stock %d to %d; the fixture is wrong", before, mid)
	}

	if _, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if after != before {
		t.Errorf("stock is %d after sweeping an expired hold, want %d — the unit "+
			"never came back", after, before)
	}

	var state string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, orderID).Scan(&state); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if state != "released" {
		t.Errorf("reservation is %q, want released", state)
	}

	var movements int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_movements
		WHERE variant_id = $1 AND reason = 'release'`, vid).Scan(&movements); err != nil {
		t.Fatalf("read movements: %v", err)
	}
	if movements == 0 {
		t.Error("stock came back with no movement recorded")
	}
}

// TestSweepNeverTouchesAPaidOrdersHold. The lock here is the database:
// release_reservation refuses a committed order's hold anyway, so the query
// filter this also exercises is defence in depth rather than a lock.
func TestSweepNeverTouchesAPaidOrdersHold(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-3")

	// Expired AND paid: without the expiry this passes with the funding check gone.
	paidOrder := heldOrder(t, vid, time.Hour, true)
	abandoned := heldOrder(t, vid, time.Hour, false)

	if _, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var paidState, abandonedState string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, paidOrder).Scan(&paidState); err != nil {
		t.Fatalf("read paid reservation: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, abandoned).Scan(&abandonedState); err != nil {
		t.Fatalf("read abandoned reservation: %v", err)
	}
	if paidState != "held" {
		t.Errorf("a PAID order's hold is %q after a sweep, want held — its stock "+
			"is now sellable twice", paidState)
	}
	if abandonedState != "released" {
		t.Errorf("the abandoned hold is %q, want released", abandonedState)
	}
}

// TestSweepNeverTouchesAFullyCreditFundedOrdersHold is the half committed_orders
// cannot answer: a fully store-credited order has no payment row and cannot have
// one, so the view reports it uncommitted while the customer has paid in full.
func TestSweepNeverTouchesAFullyCreditFundedOrdersHold(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-credit")

	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	funded := creditFundedHeldOrder(t, vid, time.Hour)
	abandoned := heldOrder(t, vid, time.Hour, false)

	if _, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var fundedState, abandonedState string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, funded).Scan(&fundedState); err != nil {
		t.Fatalf("read funded reservation: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, abandoned).Scan(&abandonedState); err != nil {
		t.Fatalf("read abandoned reservation: %v", err)
	}
	if fundedState != "held" {
		t.Errorf("a fully store-credited order's hold is %q after a sweep, want held — "+
			"the customer paid for it and its stock is now sellable twice", fundedState)
	}
	if abandonedState != "released" {
		t.Errorf("the abandoned hold is %q, want released", abandonedState)
	}

	var after int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&after); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if want := before - 1; after != want {
		t.Errorf("stock is %d after the sweep, want %d — the funded order's unit "+
			"went back on the shelf", after, want)
	}
}

func TestReleasingAFundedOrdersHoldIsRefusedByName(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "stockfix-credit-door")
	orderID := creditFundedHeldOrder(t, vid, time.Hour)

	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM inventory_reservations WHERE order_id = $1`, orderID).
		Scan(&reservationID); err != nil {
		t.Fatalf("read reservation: %v", err)
	}

	_, err := pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "inventory_reservation_funded_no_release" {
		t.Fatalf("releasing a fully-funded order's hold = %v, want "+
			"inventory_reservation_funded_no_release", err)
	}
	if !cart.BenignSweepFailure(err) {
		t.Errorf("the sweeper reads this refusal as a failure; it is the sweeper " +
			"being safe, and an operator cannot tell those apart if it logs as an error")
	}

	var state string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE id = $1`, reservationID).
		Scan(&state); err != nil {
		t.Fatalf("read reservation state: %v", err)
	}
	if state != "held" {
		t.Errorf("reservation is %q after a refused release, want held", state)
	}
}

// creditFundedHeldOrder writes an order holding one unit of vid, paid entirely
// from store credit, whose hold expired `ago` in the past.
func creditFundedHeldOrder(t *testing.T, vid uuid.UUID, ago time.Duration) (orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	// The line price IS the order total, which is what makes order_amount_owed come
	// to exactly zero once the credit is spent.
	const cents = 100000
	userID := creditedCustomer(t, cents)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

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
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, $2, 'SWEEP-CREDIT-SKU', '測試商品', $3, 1)`, orderID, vid, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'credit-sweep@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
		orderID, vid, "sweep-credit:"+number); err != nil {
		t.Fatalf("hold: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_reservations
		SET created_at = now() - $2::interval - interval '30 minutes',
		    expires_at = now() - $2::interval
		WHERE order_id = $1`, orderID, ago.String()); err != nil {
		t.Fatalf("age the hold: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT post_store_credit($1, $2, '訂單折抵', $3, $4, NULL)`,
		userID, -int64(cents), orderID, "spend:"+orderID.String()); err != nil {
		t.Fatalf("spend credit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fixture: %v", err)
	}
	return orderID
}

func TestSweepLeavesUnexpiredHolds(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-4")

	// -(-10 minutes) is ten minutes in the FUTURE: still holding.
	fresh := heldOrder(t, vid, -10*time.Minute, false)

	if _, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	var state string
	if err := pool.QueryRow(ctx,
		`SELECT state FROM inventory_reservations WHERE order_id = $1`, fresh).Scan(&state); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if state != "held" {
		t.Errorf("an unexpired hold is %q after a sweep, want held — the customer "+
			"lost the item while paying for it", state)
	}
}

func TestSweepIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-5")
	orderID := heldOrder(t, vid, time.Hour, false)

	first, firstSkipped, err := s.Sweep(ctx, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if first == 0 {
		t.Fatal("the first sweep released nothing; the fixture is wrong")
	}
	if firstSkipped != 0 {
		t.Errorf("the first sweep skipped %d, want 0", firstSkipped)
	}
	second, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if second != 0 {
		t.Errorf("the second sweep released %d rows, want 0", second)
	}

	var releases int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_movements
		WHERE source_id = (SELECT id FROM inventory_reservations WHERE order_id = $1)
		  AND reason = 'release'`, orderID).Scan(&releases); err != nil {
		t.Fatalf("count movements: %v", err)
	}
	if releases != 1 {
		t.Errorf("%d release movements for one reservation, want 1 — the stock "+
			"came back more than once", releases)
	}
}

func TestSweepCountsABenignRefusalAsSkippedNotFailed(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "stockfix-6")

	// Expired, and paid — the benign case: a sweeper whose query filter is gone
	// still sees it, and release_reservation refuses it.
	paid := heldOrder(t, vid, time.Hour, true)

	// Reach past the query, so the refusal happens rather than being filtered out.
	var reservationID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM inventory_reservations WHERE order_id = $1`, paid).Scan(&reservationID); err != nil {
		t.Fatalf("find reservation: %v", err)
	}
	_, err := pool.Exec(ctx, `SELECT release_reservation($1)`, reservationID)
	if err == nil {
		t.Fatal("release_reservation accepted a committed order's hold")
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "inventory_reservation_committed_no_release" {
		t.Fatalf("refused by %v, want inventory_reservation_committed_no_release", err)
	}
	if !cart.BenignSweepFailure(err) {
		t.Error("the sweeper would count this refusal as a failure; it is the sweeper being safe")
	}

	if cart.BenignSweepFailure(errors.New("connection reset")) {
		t.Error("an ordinary error was classified as benign")
	}
}

// TestCreditIsCappedAtWhatTheOrderOwesAfterTheDiscount. The cap is
// `subtotal - discount + shipping`: leaving the discount out lets a coupon and a
// balance spend more credit than the order is worth, which nothing refuses.
func TestCreditIsCappedAtWhatTheOrderOwesAfterTheDiscount(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, min_subtotal_cents)
		VALUES ('CREDITCAP', '測試折抵', 'amount', 30000, 0)`); err != nil {
		t.Fatalf("create the coupon: %v", err)
	}
	userID := creditedCustomer(t, 10000000) // NT$100,000 — far above the order

	id := newCart(t, s)
	if err := s.Add(ctx, id, freshVariant(t, "creditcap"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	view, err := s.View(ctx, id)
	if err != nil {
		t.Fatalf("read cart: %v", err)
	}
	coupon, err := s.FindCoupon(ctx, "CREDITCAP", view.SubtotalCents, 8000)
	if err != nil {
		t.Fatalf("find the coupon: %v", err)
	}

	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{UUID: userID, Valid: true},
		shipVersionFor(t, "home_delivery"), &cart.Address{
			Email: "creditcap@example.com", Name: "王小明", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
		}, nil, coupon, "creditcap-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var owed, debit int64
	if err := pool.QueryRow(ctx, `
		SELECT order_amount_owed(o.id),
		       -coalesce((SELECT sum(e.amount_cents) FROM store_credit_entries e
		                  WHERE e.order_id = o.id), 0)
		FROM orders o WHERE o.order_number = $1`, number).Scan(&owed, &debit); err != nil {
		t.Fatalf("read the order: %v", err)
	}

	if owed != 0 {
		t.Errorf("order_amount_owed(%s) = %d, want 0. A negative figure means credit was "+
			"spent against the GROSS total, which loses the customer the discount and "+
			"leaves the order permanently unpayable", number, owed)
	}
	if debit <= 0 {
		t.Errorf("credit debited %d on the order, want a positive spend", debit)
	}
}

// TestADoubleClickedCheckoutPlacesOneOrder covers two requests IN FLIGHT AT
// ONCE, which the advisory lock on the key serialises. T1's transaction is held
// OPEN while T2 runs, rather than raced from two goroutines that never overlap.
func TestADoubleClickedCheckoutPlacesOneOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	key := "double-" + uuid.NewString()
	vid := freshVariant(t, "doubleclick")

	// T1 takes the lock on the key by hand and HOLDS it, which is the state a first
	// checkout is in between its lock and its commit.
	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(ctx) }()
	if _, err := t1.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, key); err != nil {
		t.Fatalf("T1 take the lock: %v", err)
	}

	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	placed := make(chan error, 1)
	go func() {
		_, placeErr := s.PlaceOrder(ctx, id, uuid.NullUUID{},
			shipVersionFor(t, "home_delivery"), &cart.Address{
				Email: "double@example.com", Name: "王小明", Phone: "0912345678",
				PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			}, nil, nil, key)
		placed <- placeErr
	}()

	select {
	case err := <-placed:
		t.Fatalf("the second checkout completed (%v) while the key was held by "+
			"another transaction — it is not taking the lock", err)
	case <-time.After(750 * time.Millisecond):
	}

	if err := t1.Rollback(ctx); err != nil {
		t.Fatalf("release T1: %v", err)
	}
	select {
	case err := <-placed:
		if err != nil {
			t.Fatalf("the second checkout failed after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second checkout never completed after the lock was released")
	}

	var orders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).
		Scan(&orders); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if orders != 1 {
		t.Errorf("the key produced %d checkout attempts, want 1", orders)
	}
}

// creditedCustomer makes a user with a store-credit balance and returns their id.
func creditedCustomer(t *testing.T, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('credit-'||gen_random_uuid()||'@example.com', 'customer', '額度測試')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if cents > 0 {
		if _, err := pool.Exec(ctx,
			`SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
			id, cents, "grant:"+id.String()); err != nil {
			t.Fatalf("grant credit: %v", err)
		}
	}
	return id
}

func TestCreditIsSpentInsideTheOrdersOwnTransaction(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := variantOf(t, "koto-over-ear", true)
	userID := creditedCustomer(t, 300000) // NT$3,000

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "c@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := s.PlaceOrder(ctx, id,
		uuid.NullUUID{UUID: userID, Valid: true}, shipID, addr, nil, nil, "credit-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var spent int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		JOIN orders o ON o.id = e.order_id
		WHERE o.order_number = $1`, number).Scan(&spent); err != nil {
		t.Fatalf("read spend: %v", err)
	}
	if spent != -300000 {
		t.Errorf("credit spent is %d, want -300000 — the whole balance, since the "+
			"order costs more than it", spent)
	}

	var balance int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		JOIN store_credit_accounts a ON a.id = e.account_id
		WHERE a.user_id = $1`, userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance != 0 {
		t.Errorf("balance is %d after spending it all, want 0", balance)
	}
}

func TestCreditNeverExceedsWhatTheOrderOwes(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := variantOf(t, "koto-over-ear", true)
	userID := creditedCustomer(t, 100000000) // NT$1,000,000: far more than any order

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "c2@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}

	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := s.PlaceOrder(ctx, id,
		uuid.NullUUID{UUID: userID, Valid: true}, shipID, addr, nil, nil, "credit-2")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var spent, total int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0),
		       (SELECT coalesce(sum(ol.unit_price_cents * ol.quantity), 0) + o.shipping_cents
		        FROM order_lines ol WHERE ol.order_id = o.id)
		FROM orders o
		LEFT JOIN store_credit_entries e ON e.order_id = o.id
		WHERE o.order_number = $1
		GROUP BY o.id, o.shipping_cents`, number).Scan(&spent, &total); err != nil {
		t.Fatalf("read: %v", err)
	}
	if -spent != total {
		t.Errorf("spent %d against an order owing %d; credit must be capped at the total",
			-spent, total)
	}

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("find order: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("a fully-credited order could not enter fulfilment: %v", err)
	}

	var payments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE order_id = $1`, orderID).Scan(&payments); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if payments != 0 {
		t.Errorf("%d payment rows on a fully-credited order, want 0", payments)
	}
}

func TestAGuestSpendsNoCredit(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := variantOf(t, "koto-over-ear", true)

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "g@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil, "credit-guest")
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	var entries int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM store_credit_entries e JOIN orders o ON o.id = e.order_id
		WHERE o.order_number = $1`, number).Scan(&entries); err != nil {
		t.Fatalf("count: %v", err)
	}
	if entries != 0 {
		t.Errorf("%d credit entries on a GUEST order, want 0", entries)
	}
}

func TestTheHeaderBadgeCountsWhatTheCartHolds(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	a, b, _ := threeVariants(t, "badgefix")

	count := func() int {
		t.Helper()
		n, err := s.ItemCount(ctx, id)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	if got := count(); got != 0 {
		t.Errorf("an empty cart counts %d, want 0", got)
	}
	if err := s.Add(ctx, id, a, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 1 {
		t.Errorf("one unit counts %d, want 1", got)
	}
	if err := s.Add(ctx, id, a, 2); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 3 {
		t.Errorf("three units of one variant count %d, want 3 — the badge is "+
			"counting lines, not items", got)
	}
	if err := s.Add(ctx, id, b, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 4 {
		t.Errorf("four units across two variants count %d, want 4", got)
	}

	other := newCart(t, s)
	if err := s.Add(ctx, other, a, 5); err != nil {
		t.Fatalf("add to other: %v", err)
	}
	if got := count(); got != 4 {
		t.Errorf("this cart counts %d after another cart was filled, want 4", got)
	}
}

func TestTheConfirmationMessageCommitsWithTheOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := variantOf(t, "koto-over-ear", true)

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "ob@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "路 1 號",
	}

	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil, "outbox-ok")
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	var messages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = 'order.placed' AND dedupe_key = $1`, number).Scan(&messages); err != nil {
		t.Fatalf("count: %v", err)
	}
	if messages != 1 {
		t.Errorf("%d messages for a placed order, want 1", messages)
	}

	// A fabricated shipping version is refused AFTER the transaction has begun,
	// which is the window a message written outside it would survive.
	before := countMessages(t)
	failed := newCart(t, s)
	if err := s.Add(ctx, failed, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := s.PlaceOrder(ctx, failed, uuid.NullUUID{}, uuid.New(), addr, nil, nil, "outbox-fail"); err == nil {
		t.Fatal("a fabricated shipping version was accepted")
	}
	if after := countMessages(t); after != before {
		t.Errorf("a failed checkout left %d new messages; the enqueue is not in "+
			"the order's transaction", after-before)
	}
}

// countMessages is how many order.placed messages exist.
func countMessages(t *testing.T) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM outbox_messages WHERE topic = 'order.placed'`).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}

// waitUntilBlocked returns once the backend at pid is waiting on a lock, or once
// done fires. pg_stat_activity is the synchronisation point, so the interleaving
// is a fact rather than an assumption; the 5ms is a poll interval, not a guess.
func waitUntilBlocked(t *testing.T, pid int, done <-chan error) {
	t.Helper()
	ctx := t.Context()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-done:
			// T2 finishing while T1 still held its row means hold_inventory never took the
			// lock, and without this branch the case would pass anyway.
			t.Fatal("the second writer finished without ever blocking; nothing " +
				"serialised the two, so the row lock is not being taken")
		default:
		}
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT wait_event_type = 'Lock'
			FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting); err == nil && waiting {
			return
		}
		if time.Now().After(deadline) {
			var state, waitType, waitEvent, query string
			_ = pool.QueryRow(ctx, `
				SELECT coalesce(state, ''), coalesce(wait_event_type, ''),
				       coalesce(wait_event, ''), coalesce(query, '')
				FROM pg_stat_activity WHERE pid = $1`, pid).
				Scan(&state, &waitType, &waitEvent, &query)
			t.Fatalf("the second writer neither finished nor blocked on a lock; "+
				"pid=%d state=%q wait=%q/%q query=%q", pid, state, waitType, waitEvent, query)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// shipVersionFor is the current version of the named shipping method.
func shipVersionFor(t *testing.T, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT v.id FROM shipping_method_versions v
		 JOIN shipping_methods sm ON sm.id = v.method_id
		 WHERE sm.code = $1 ORDER BY v.effective_at DESC LIMIT 1`, code).Scan(&id); err != nil {
		t.Fatalf("shipping version for %s: %v", code, err)
	}
	return id
}

// destinationOf reads back what an order recorded as its destination.
func destinationOf(t *testing.T, number string) (street, brand, code, name string) {
	t.Helper()
	var s, b, c, n *string
	if err := pool.QueryRow(t.Context(),
		`SELECT pd.street, pd.pickup_brand, pd.pickup_store_code, pd.pickup_store_name
		 FROM order_private_data pd JOIN orders o ON o.id = pd.order_id
		 WHERE o.order_number = $1`, number).Scan(&s, &b, &c, &n); err != nil {
		t.Fatalf("read destination of %s: %v", number, err)
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return deref(s), deref(b), deref(c), deref(n)
}

func TestThePickupDestinationComesFromTheMethodNotTheForm(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	addr := &cart.Address{
		// The destination the CALLER claims is deliberately wrong: PlaceOrder re-reads
		// the method and overrides it.
		To:    cart.ToAddress,
		Email: "pickup@example.com", Name: "陳小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
		PickupBrand: "family_mart", PickupStoreCode: "012345", PickupStoreName: "台北車站門市",
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{},
		shipVersionFor(t, "store_pickup"), addr, nil, nil, "dest-pickup-1")
	if err != nil {
		t.Fatalf("place pickup order: %v", err)
	}

	street, brand, code, name := destinationOf(t, number)
	if street != "" {
		t.Errorf("a pickup order kept a street address: %q", street)
	}
	if brand != "family_mart" || code != "012345" || name != "台北車站門市" {
		t.Errorf("pickup destination is %q/%q/%q, want family_mart/012345/台北車站門市",
			brand, code, name)
	}
}

func TestAnAddressOrderKeepsNoPickupPoint(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	addr := &cart.Address{
		To:    cart.ToPickupPoint,
		Email: "home@example.com", Name: "王大明", Phone: "0922333444",
		PostalCode: "106", City: "台北市", District: "大安區", Street: "復興南路一段 1 號",
		PickupBrand: "seven_eleven", PickupStoreCode: "987654", PickupStoreName: "光復門市",
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{},
		shipVersionFor(t, "home_delivery"), addr, nil, nil, "dest-home-1")
	if err != nil {
		t.Fatalf("place address order: %v", err)
	}

	street, brand, code, name := destinationOf(t, number)
	if street != "復興南路一段 1 號" {
		t.Errorf("the street address did not survive: %q", street)
	}
	if brand != "" || code != "" || name != "" {
		t.Errorf("an address order kept a pickup point: %q/%q/%q", brand, code, name)
	}
}

func TestBothPagesShowWhereAPickupOrderGoes(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	addr := &cart.Address{
		Email: "shown@example.com", Name: "李小華", Phone: "0933444555",
		PickupBrand: "hi_life", PickupStoreCode: "778899", PickupStoreName: "民生門市",
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{},
		shipVersionFor(t, "store_pickup"), addr, nil, nil, "dest-shown-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("read order view: %v", err)
	}
	for _, want := range []string{"萊爾富", "民生門市", "778899"} {
		if !strings.Contains(view.DeliveryTo, want) {
			t.Errorf("the confirmation does not show %q: %q", want, view.DeliveryTo)
		}
	}
}

func TestTheAddressBookIsScopedToItsOwner(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	mine := addressOwner(t, "mine@example.com", "我的地址")
	theirs := addressOwner(t, "theirs@example.com", "別人的地址")

	got, err := s.SavedAddresses(ctx, uuid.NullUUID{UUID: mine, Valid: true})
	if err != nil {
		t.Fatalf("read saved addresses: %v", err)
	}
	if len(got) != 1 || got[0].Label != "我的地址" {
		t.Fatalf("read %d addresses %+v, want only my own", len(got), got)
	}

	other, err := s.SavedAddresses(ctx, uuid.NullUUID{UUID: theirs, Valid: true})
	if err != nil {
		t.Fatalf("read the other account's addresses: %v", err)
	}
	if len(other) != 1 || other[0].Label != "別人的地址" {
		t.Fatalf("the other account has %d addresses %+v", len(other), other)
	}
}

func TestAGuestHasNoAddressBook(t *testing.T) {
	s := cart.NewStore(pool)
	addressOwner(t, "guestcontrol@example.com", "有人的地址")

	got, err := s.SavedAddresses(t.Context(), uuid.NullUUID{})
	if err != nil {
		t.Fatalf("read saved addresses for a guest: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a guest was offered %d saved addresses: %+v", len(got), got)
	}
}

// addressOwner registers an account with one saved address and returns its id.
func addressOwner(t *testing.T, email, label string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO addresses (user_id, label, recipient_name, phone,
		                        postal_code, city, district, street, is_default)
		 VALUES ($1, $2, '收件人', '0912345678', '110', '台北市', '信義區', '松高路 1 號', true)`,
		id, label); err != nil {
		t.Fatalf("save an address for %s: %v", email, err)
	}
	return id
}

// numberOf is an order's customer-facing number.
func numberOf(t *testing.T, orderID uuid.UUID) string {
	t.Helper()
	var number string
	if err := pool.QueryRow(t.Context(),
		`SELECT order_number FROM orders WHERE id = $1`, orderID).Scan(&number); err != nil {
		t.Fatalf("read order number: %v", err)
	}
	return number
}

// stockOf is a variant's shelf quantity.
func stockOf(t *testing.T, vid uuid.UUID) int32 {
	t.Helper()
	var n int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

// TestCancellingAnOrderHandsBackItsOpenCheckouts is the database half of closing
// a cancelled order's checkout at Stripe. The row is opened through open_payment
// because `store` holds no INSERT on payments.
func TestCancellingAnOrderHandsBackItsOpenCheckouts(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "cancel-session-1")

	orderID := heldOrder(t, vid, -time.Hour, false)
	number := numberOf(t, orderID)
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`,
		orderID, "cs_test_still_open"); err != nil {
		t.Fatalf("open a checkout session against the order: %v", err)
	}

	sessions, err := s.Cancel(ctx, number)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	want := []string{"cs_test_still_open"}
	if !slices.Equal(sessions, want) {
		t.Errorf("Cancel() sessions = %v, want %v\n"+
			"  A cancellation that hands back nothing leaves the customer's checkout "+
			"payable for as long as the stock hold lasts.", sessions, want)
	}
}

func TestCancellingAnOrderWithNoCheckoutHandsBackNothing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "cancel-session-2")

	// A DIFFERENT order with a live session, so the query is asked to tell two
	// orders apart rather than merely to find none.
	other := heldOrder(t, freshVariant(t, "cancel-session-3"), -time.Hour, false)
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, 100000)`,
		other, "cs_test_someone_elses"); err != nil {
		t.Fatalf("open a checkout session against another order: %v", err)
	}

	number := numberOf(t, heldOrder(t, vid, -time.Hour, false))
	sessions, err := s.Cancel(ctx, number)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("Cancel() returned %v for an order with no checkout of its own — "+
			"the handler would ask Stripe to expire another order's session", sessions)
	}
}

func TestCancellingAnOrderPutsTheStockBack(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-8")

	orderID := heldOrder(t, vid, -time.Hour, false) // held, not yet expired, unpaid
	number := numberOf(t, orderID)
	held := stockOf(t, vid)

	if _, err := s.Cancel(ctx, number); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if got := stockOf(t, vid); got != held+1 {
		t.Errorf("stock is %d after cancelling, want %d — the hold was not released", got, held+1)
	}
	var status, state string
	if err := pool.QueryRow(ctx, `
		SELECT o.fulfillment_status,
		       (SELECT state FROM inventory_reservations WHERE order_id = o.id LIMIT 1)
		FROM orders o WHERE o.id = $1`, orderID).Scan(&status, &state); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "cancelled" || state != "released" {
		t.Errorf("order is %s with a %s hold, want cancelled/released", status, state)
	}
}

func TestAFundedOrderCannotBeCancelledByItsCustomer(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-9")

	orderID := heldOrder(t, vid, -time.Hour, true) // paid
	number := numberOf(t, orderID)
	before := stockOf(t, vid)

	if _, err := s.Cancel(ctx, number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Fatalf("a paid order was cancellable: %v", err)
	}
	if got := stockOf(t, vid); got != before {
		t.Errorf("stock moved to %d on a refused cancellation, want %d", got, before)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "pending" {
		t.Errorf("a refused cancellation moved the order to %s", status)
	}
}

func TestCancellingTwiceIsRefusedTheSecondTime(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-10")

	number := numberOf(t, heldOrder(t, vid, -time.Hour, false))
	if _, err := s.Cancel(ctx, number); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	after := stockOf(t, vid)

	if _, err := s.Cancel(ctx, number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Errorf("the second cancellation was accepted: %v", err)
	}
	if got := stockOf(t, vid); got != after {
		t.Errorf("the second cancellation moved stock to %d, want %d — it released twice", got, after)
	}
}

func TestCancellingAnOrderThatIsBeingPickedIsRefused(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-11")

	orderID := heldOrder(t, vid, -time.Hour, true) // funded, so it may leave pending
	number := numberOf(t, orderID)
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move to picking: %v", err)
	}

	if _, err := s.Cancel(ctx, number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Errorf("an order being picked was cancellable: %v", err)
	}
}

func TestCancellingAnOrderThatDoesNotExistIsTheSameRefusal(t *testing.T) {
	s := cart.NewStore(pool)
	if _, err := s.Cancel(t.Context(), "GO-990101-999999"); !errors.Is(err, cart.ErrNotCancellable) {
		t.Errorf("an unknown order answered %v, want ErrNotCancellable", err)
	}
}

// TestAFundedOrderWithNoHeldStockIsStillRefused proves the funding guard in
// CancelOrderByCustomer rather than the one in release_reservation: with stock
// held the release refuses first, and only the WHERE clause is left here.
func TestAFundedOrderWithNoHeldStockIsStillRefused(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-12")

	orderID := heldOrder(t, vid, -time.Hour, true) // funded
	number := numberOf(t, orderID)
	var reservation uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM inventory_reservations WHERE order_id = $1`, orderID).Scan(&reservation); err != nil {
		t.Fatalf("read reservation: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT consume_reservation($1)`, reservation); err != nil {
		t.Fatalf("consume: %v", err)
	}

	if _, err := s.Cancel(ctx, number); !errors.Is(err, cart.ErrNotCancellable) {
		t.Fatalf("a paid order with no held stock was cancellable: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "pending" {
		t.Errorf("a refused cancellation moved the order to %s", status)
	}
}

func TestAStrangerCannotCancelSomebodyElsesOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-13")
	orderID := heldOrder(t, vid, -time.Hour, false)
	number := numberOf(t, orderID)
	before := stockOf(t, vid)

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		nil)

	stranger := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
	stranger.SetPathValue("number", number)
	res := httptest.NewRecorder()
	h.CancelOrder(res, stranger)

	if res.Code != http.StatusNotFound {
		t.Errorf("a stranger's cancellation answered %d, want 404", res.Code)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT fulfillment_status FROM orders WHERE id = $1`, orderID).Scan(&status); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "pending" {
		t.Errorf("a stranger moved the order to %s", status)
	}
	if got := stockOf(t, vid); got != before {
		t.Errorf("a stranger's cancellation moved stock to %d, want %d", got, before)
	}

	placer := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
	placer.SetPathValue("number", number)
	placer.AddCookie(placedCookie(t, s, number))
	ok := httptest.NewRecorder()
	h.CancelOrder(ok, placer)

	if ok.Code != http.StatusSeeOther {
		t.Fatalf("the browser that placed the order got %d, want 303", ok.Code)
	}
	if got := stockOf(t, vid); got != before+1 {
		t.Errorf("the accepted cancellation left stock at %d, want %d", got, before+1)
	}
}

// TestACancelledOrdersStockComesBackByEveryDoor. Cancelled is SETTLED and not
// committed: reading any non-pending status as committed puts a cancelled order
// on the wrong side of both doors, and its units come back by no route at all.
func TestACancelledOrdersStockComesBackByEveryDoor(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-14")

	orderID := heldOrder(t, vid, time.Hour, true) // expired hold, paid
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`, orderID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	released, _, err := s.Sweep(ctx, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if released == 0 {
		t.Fatal("the sweeper released nothing — a cancelled order's hold is still unreachable")
	}

	var after int32
	var state string
	if err := pool.QueryRow(ctx, `
		SELECT pv.stock_quantity,
		       (SELECT r.state FROM inventory_reservations r WHERE r.order_id = $2 LIMIT 1)
		FROM product_variants pv WHERE pv.id = $1`, vid, orderID).Scan(&after, &state); err != nil {
		t.Fatalf("read stock after: %v", err)
	}
	if after != before+1 {
		t.Errorf("stock is %d after the sweep, want %d", after, before+1)
	}
	if state != "released" {
		t.Errorf("the hold is %s, want released", state)
	}
}

func TestACancelledOrderIsNotAVerifiedPurchase(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "stockfix-15")

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('cancelbadge@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	orderID := heldOrder(t, vid, -time.Hour, true) // funded
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET user_id = $2 WHERE id = $1`, orderID, userID); err != nil {
		t.Fatalf("attach owner: %v", err)
	}

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT product_id FROM product_variants WHERE id = $1`, vid).Scan(&productID); err != nil {
		t.Fatalf("read product: %v", err)
	}
	review := func() error {
		_, err := pool.Exec(ctx, `
			INSERT INTO product_reviews (product_id, user_id, rating, body, is_verified_purchase)
			VALUES ($1, $2, 5, '測試評價', true)
			ON CONFLICT (product_id, user_id) DO UPDATE SET is_verified_purchase = true`,
			productID, userID)
		return err
	}

	if err := review(); err != nil {
		t.Fatalf("a real purchase could not claim 已購買: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM product_reviews WHERE product_id = $1 AND user_id = $2`,
		productID, userID); err != nil {
		t.Fatalf("clear review: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE id = $1`, orderID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	err := review()
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "product_reviews_verified_is_real" {
		t.Errorf("a cancelled order still earned 已購買: %v", err)
	}
}

func TestAnOffshoreAddressCostsMoreThanATaipeiOne(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	version := shipVersionFor(t, "home_delivery")

	taipei, err := s.QuoteShipping(ctx, version, 100000, "110")
	if err != nil {
		t.Fatalf("quote for Taipei: %v", err)
	}
	kinmen, err := s.QuoteShipping(ctx, version, 100000, "890")
	if err != nil {
		t.Fatalf("quote for Kinmen: %v", err)
	}
	if kinmen.Total() <= taipei.Total() {
		t.Errorf("金門 costs %d and 台北 costs %d — the surcharge is not applied",
			kinmen.Total(), taipei.Total())
	}
	if kinmen.Surcharge == 0 || kinmen.ZoneName == "" {
		t.Errorf("the quote does not name the surcharge: %+v", kinmen)
	}

	// A FIVE-digit code, which is what a customer types: Taiwan writes 3+2, and the
	// zone is found from the first three.
	full, err := s.QuoteShipping(ctx, version, 100000, "89052")
	if err != nil {
		t.Fatalf("quote for a five-digit Kinmen code: %v", err)
	}
	if full.Total() != kinmen.Total() {
		t.Errorf("89052 costs %d and 890 costs %d — the prefix is not being taken",
			full.Total(), kinmen.Total())
	}

	freeTaipei, err := s.QuoteShipping(ctx, version, 500000, "110")
	if err != nil {
		t.Fatalf("quote for a large Taipei order: %v", err)
	}
	freeKinmen, err := s.QuoteShipping(ctx, version, 500000, "890")
	if err != nil {
		t.Fatalf("quote for a large Kinmen order: %v", err)
	}
	if freeTaipei.Total() != 0 {
		t.Errorf("a large 台北 order pays %d, want free", freeTaipei.Total())
	}
	if freeKinmen.Total() != kinmen.Surcharge {
		t.Errorf("a large 金門 order pays %d, want the surcharge %d — 免運 must "+
			"cover the base rate and not the crossing",
			freeKinmen.Total(), kinmen.Surcharge)
	}
}

func TestAPickupOrderIsNeverInAZone(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	quote, err := s.QuoteShipping(ctx, shipVersionFor(t, "store_pickup"), 100000, "")
	if err != nil {
		t.Fatalf("quote for a pickup order: %v", err)
	}
	if quote.Surcharge != 0 || quote.ZoneName != "" {
		t.Errorf("a pickup order was priced into a zone: %+v", quote)
	}
}

func TestAnOrderIsChargedTheZoneItShipsTo(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, freshVariant(t, "stockfix-16"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	addr := &cart.Address{
		Email: "kinmen@example.com", Name: "金門", Phone: "0912345678",
		PostalCode: "890", City: "金門縣", District: "金城鎮", Street: "民生路 1 號",
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{},
		shipVersionFor(t, "home_delivery"), addr, nil, nil, "zone-order-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	var charged, subtotal int64
	if readErr := pool.QueryRow(ctx, `
		SELECT o.shipping_cents,
		       coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
		                 FROM order_lines ol WHERE ol.order_id = o.id), 0)
		FROM orders o WHERE o.order_number = $1`, number).Scan(&charged, &subtotal); readErr != nil {
		t.Fatalf("read order: %v", readErr)
	}

	want, err := s.QuoteShipping(ctx, shipVersionFor(t, "home_delivery"), subtotal, "890")
	if err != nil {
		t.Fatalf("re-quote: %v", err)
	}
	if charged != want.Total() {
		t.Errorf("the order was charged %d, want %d", charged, want.Total())
	}
	if want.Surcharge == 0 {
		t.Fatal("the fixture priced no surcharge — this test proved nothing")
	}
}

func TestAFreeShippingCouponDoesNotPayForTheCrossing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, min_subtotal_cents)
		VALUES ('FREESHIPZONE', '測試免運', 'free_shipping', 0)`); err != nil {
		t.Fatalf("create the coupon: %v", err)
	}

	place := func(postal, city, district, key string) int64 {
		t.Helper()
		id := newCart(t, s)
		if err := s.Add(ctx, id, freshVariant(t, "stockfix-17"), 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		subtotal, err := s.View(ctx, id)
		if err != nil {
			t.Fatalf("read cart: %v", err)
		}
		coupon, err := s.FindCoupon(ctx, "FREESHIPZONE", subtotal.SubtotalCents, 8000)
		if err != nil {
			t.Fatalf("find the coupon: %v", err)
		}
		number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{},
			shipVersionFor(t, "home_delivery"), &cart.Address{
				Email: "ship@example.com", Name: "測試", Phone: "0912345678",
				PostalCode: postal, City: city, District: district, Street: "路 1 號",
			}, nil, coupon, key)
		if err != nil {
			t.Fatalf("place: %v", err)
		}
		var cents int64
		if err := pool.QueryRow(ctx,
			`SELECT shipping_cents FROM orders WHERE order_number = $1`, number).Scan(&cents); err != nil {
			t.Fatalf("read shipping: %v", err)
		}
		return cents
	}

	if got := place("110", "台北市", "信義區", "freeship-main"); got != 0 {
		t.Errorf("a mainland order with a 免運 coupon pays %d, want 0", got)
	}
	offshore := place("890", "金門縣", "金城鎮", "freeship-offshore")
	if offshore == 0 {
		t.Error("a 免運 coupon paid for the 離島 crossing")
	}
}

func TestReorderPutsBackWhatCanStillBeBought(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	live, retired, empty := threeVariants(t, "reorder-fixture")

	number := orderOfVariants(t, map[uuid.UUID]int32{live: 2, retired: 1, empty: 1})

	// One variant is retired and one emptied AFTER the order, which is what an order
	// from last year has.
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET is_active = false WHERE id = $1`, retired); err != nil {
		t.Fatalf("retire: %v", err)
	}
	emptyTheShelfFor(t, empty)

	basket := newCart(t, s)
	got, err := s.Reorder(ctx, basket, number)
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}

	if got.Added != 1 {
		t.Errorf("put back %d lines, want 1 — only one is still sellable", got.Added)
	}
	if len(got.Skipped) != 2 {
		t.Fatalf("skipped %d lines, want 2: %+v", len(got.Skipped), got.Skipped)
	}
	reasons := map[cart.SkipReason]int{}
	for _, sk := range got.Skipped {
		reasons[sk.Reason]++
		if sk.Name == "" {
			t.Errorf("a skipped line has no name: %+v", sk)
		}
	}
	if reasons[cart.SkipGone] != 1 || reasons[cart.SkipSoldOut] != 1 {
		t.Errorf("the reasons are %v, want one gone and one sold out", reasons)
	}

	view, err := s.View(ctx, basket)
	if err != nil {
		t.Fatalf("read cart: %v", err)
	}
	if len(view.Lines) != 1 || view.Lines[0].Quantity != 2 {
		t.Errorf("the cart holds %d lines %+v, want one line of 2", len(view.Lines), view.Lines)
	}
}

func TestReorderPricesFromTheCatalogueAndNotTheOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid, _, _ := threeVariants(t, "reorder-price-fixture")
	number := orderOfVariants(t, map[uuid.UUID]int32{vid: 1})

	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = price_cents + 100000 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("raise the price: %v", err)
	}
	var now int64
	if err := pool.QueryRow(ctx,
		`SELECT price_cents FROM product_variants WHERE id = $1`, vid).Scan(&now); err != nil {
		t.Fatalf("read the price: %v", err)
	}

	basket := newCart(t, s)
	if _, err := s.Reorder(ctx, basket, number); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	view, err := s.View(ctx, basket)
	if err != nil {
		t.Fatalf("read cart: %v", err)
	}
	if len(view.Lines) != 1 {
		t.Fatalf("the cart holds %d lines, want 1", len(view.Lines))
	}
	if view.Lines[0].UnitCents != now {
		t.Errorf("the cart prices the line at %d, want today's %d",
			view.Lines[0].UnitCents, now)
	}
}

// orderOfVariants places a committed order holding the given variants.
func orderOfVariants(t *testing.T, want map[uuid.UUID]int32) string {
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
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	position := 0
	for vid, qty := range want {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name,
			                         unit_price_cents, quantity, position)
			SELECT $1, pv.id, pv.sku, p.name, pv.price_cents, $3, $4
			FROM product_variants pv JOIN products p ON p.id = pv.product_id
			WHERE pv.id = $2`, orderID, vid, qty, position); err != nil {
			t.Fatalf("create line: %v", err)
		}
		position++
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'reorder@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

// emptyTheShelfFor takes a variant down to nothing sellable.
func emptyTheShelfFor(t *testing.T, vid uuid.UUID) {
	t.Helper()
	var stock int32
	if err := pool.QueryRow(t.Context(),
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&stock); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if stock > 0 {
		if _, err := pool.Exec(t.Context(),
			`SELECT record_inventory_movement($1, $2, 'adjustment', $3, NULL, NULL, NULL)`,
			vid, -stock, "reorder-empty:"+vid.String()); err != nil {
			t.Fatalf("empty the shelf: %v", err)
		}
	}
}

func TestAStrangerCannotFillTheirCartFromSomebodyElsesOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	own, _, _ := threeVariants(t, "reorder-access-fixture")
	number := orderOfVariants(t, map[uuid.UUID]int32{own: 1})

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		nil)

	stranger := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/orders/"+number+"/reorder", http.NoBody)
	stranger.SetPathValue("number", number)
	res := httptest.NewRecorder()
	h.ReorderItems(res, stranger)

	if res.Code != http.StatusNotFound {
		t.Errorf("a stranger's reorder answered %d, want 404", res.Code)
	}
	// No cart was opened either: a 404 that had filled a basket would leak the
	// order's contents by another route.
	if cookie := res.Header().Get("Set-Cookie"); strings.Contains(cookie, "goen_cart") {
		t.Errorf("a refused reorder opened a cart: %q", cookie)
	}

	placer := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/orders/"+number+"/reorder", http.NoBody)
	placer.SetPathValue("number", number)
	placer.AddCookie(placedCookie(t, s, number))
	ok := httptest.NewRecorder()
	h.ReorderItems(ok, placer)

	if ok.Code != http.StatusSeeOther {
		t.Fatalf("the browser that placed the order got %d, want 303", ok.Code)
	}
	if got := ok.Header().Get("Location"); !strings.Contains(got, "added=1") {
		t.Errorf("the redirect is %q, want it to report one line added", got)
	}
}

// TestAMethodIsNotOfferedForAParcelItsCarrierRefuses. A method is offered only
// for a cart its carrier will physically take, PER ITEM and never over the cart
// total: two things that each fit are two parcels, but one item cannot be split.
func TestAMethodIsNotOfferedForAParcelItsCarrierRefuses(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	code := "cvs" + uuid.NewString()[:6]
	var methodID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO shipping_methods (code, destination_kind,
		                              max_parcel_longest_mm, max_parcel_sum_mm, max_parcel_weight_g)
		VALUES ($1, 'pickup_point', 450, 1050, 10000) RETURNING id`, code).Scan(&methodID); err != nil {
		t.Fatalf("create method: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO shipping_method_versions (method_id, name, fee_cents)
		VALUES ($1, '測試超取', 6000)`, methodID); err != nil {
		t.Fatalf("create version: %v", err)
	}

	small, big, _ := variantsOf(t, "parcel", 3)
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET parcel_longest_mm = 180, parcel_sum_mm = 320, parcel_weight_g = 400
		WHERE id = $1`, small); err != nil {
		t.Fatalf("measure the small one: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE product_variants SET parcel_longest_mm = 700, parcel_sum_mm = 1400, parcel_weight_g = 7000
		WHERE id = $1`, big); err != nil {
		t.Fatalf("measure the big one: %v", err)
	}

	offered := func(t *testing.T, cartID uuid.UUID) bool {
		t.Helper()
		choices, err := s.ShippingChoices(ctx, cartID, 100000)
		if err != nil {
			t.Fatalf("ShippingChoices: %v", err)
		}
		for i := range choices {
			if choices[i].Code == code {
				return true
			}
		}
		return false
	}

	t.Run("a parcel that fits", func(t *testing.T) {
		id := newCart(t, s)
		if err := s.Add(ctx, id, small, 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		if !offered(t, id) {
			t.Error("a box the carrier accepts is not being offered the method")
		}
	})

	t.Run("a parcel that does not", func(t *testing.T) {
		id := newCart(t, s)
		if err := s.Add(ctx, id, big, 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		if offered(t, id) {
			t.Error("a 27-inch monitor is being offered 超商取貨; the customer pays " +
				"for it and the shop finds out at the counter")
		}
	})

	t.Run("one that fits beside one that does not", func(t *testing.T) {
		id := newCart(t, s)
		if err := s.Add(ctx, id, small, 1); err != nil {
			t.Fatalf("add small: %v", err)
		}
		if err := s.Add(ctx, id, big, 1); err != nil {
			t.Fatalf("add big: %v", err)
		}
		if offered(t, id) {
			t.Error("the method is offered because one item fits — the oversized one " +
				"still cannot be split, and it is the one that reaches the counter")
		}
	})

	t.Run("an unmeasured variant is refused by nothing", func(t *testing.T) {
		id := newCart(t, s)
		unmeasured := freshVariant(t, "parcel")
		if err := s.Add(ctx, id, unmeasured, 1); err != nil {
			t.Fatalf("add: %v", err)
		}
		if !offered(t, id) {
			t.Error("an unmeasured variant lost the method: NULL means UNKNOWN, and " +
				"hiding the channel 75.2% of shoppers prefer because nobody typed a " +
				"box size costs more than the counter refusal it prevents")
		}
	})
}

// freshVariant creates a product of this test's own with one sellable variant.
// Tests that hold, retire or empty stock must not share one: test-integration
// shuffles, so a shared variant fails whenever the order changes.
func freshVariant(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	a, _, _ := variantsOf(t, slug, 1)
	return a
}

// threeVariants is freshVariant with three.
func threeVariants(t *testing.T, slug string) (a, b, c uuid.UUID) {
	t.Helper()
	return variantsOf(t, slug, 3)
}

// variantsOf creates one product with n sellable variants.
func variantsOf(t *testing.T, name string, n int) (a, b, c uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	// A unique slug per CALL, not per test: products_slug_key would refuse the
	// second product of a test that needs two.
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

	ids := make([]uuid.UUID, 3)
	for i := range n {
		var vid uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
			VALUES ($1, $2, 199900, true, $3) RETURNING id`,
			// product_variants_sku_format wants upper case and hyphens.
			productID, strings.ToUpper(slug)+"-"+strconv.Itoa(i), i).Scan(&vid); err != nil {
			t.Fatalf("create variant: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`SELECT record_inventory_movement($1, 10, 'adjustment', $2, NULL, NULL, NULL)`,
			vid, "fixture:"+vid.String()); err != nil {
			t.Fatalf("stock the variant: %v", err)
		}
		ids[i] = vid
	}
	// Published only now: products_active_has_variant is DEFERRED and fires from
	// both sides.
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active', published_at = now() WHERE id = $1`,
		productID); err != nil {
		t.Fatalf("publish %s: %v", slug, err)
	}
	return ids[0], ids[1], ids[2]
}

func TestTheAttemptSweepKeepsRecentKeys(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	plant := func(key, age string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO checkout_attempts (idempotency_key, created_at)
			VALUES ($1, now() - $2::interval)`, key, age); err != nil {
			t.Fatalf("plant %s: %v", key, err)
		}
	}
	old := "sweep-old-" + uuid.NewString()
	recent := "sweep-recent-" + uuid.NewString()
	plant(old, "60 days")
	plant(recent, "1 hour")

	if err := s.SweepAttempts(ctx); err != nil {
		t.Fatalf("SweepAttempts: %v", err)
	}

	for key, want := range map[string]bool{old: false, recent: true} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM checkout_attempts WHERE idempotency_key = $1)`,
			key).Scan(&exists); err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if exists != want {
			t.Errorf("%s: exists = %v, want %v", key, exists, want)
		}
	}
}

func TestCancellingReturnsSpentStoreCredit(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	userID, accountID := creditedAccount(t, 50000)

	number := pendingOrderSpendingCredit(t, userID, 20000)
	if got := creditBalance(t, accountID); got != 30000 {
		t.Fatalf("balance after spending = %d, want 30000", got)
	}

	if _, err := s.Cancel(ctx, number); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if got := creditBalance(t, accountID); got != 50000 {
		t.Errorf("balance after cancelling = %d, want 50000 — the credit spent on a "+
			"cancelled order has to come back, the same way the stock does", got)
	}
	var reversals int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM store_credit_entries e
		JOIN store_credit_entries orig ON orig.id = e.reverses_id
		WHERE orig.account_id = $1`, accountID).Scan(&reversals); err != nil {
		t.Fatalf("count reversals: %v", err)
	}
	if reversals != 1 {
		t.Errorf("%d reversal entries, want 1", reversals)
	}
}

func TestReversingCancelledCreditIsIdempotent(t *testing.T) {
	ctx := t.Context()
	userID, accountID := creditedAccount(t, 50000)
	number := pendingOrderSpendingCredit(t, userID, 20000)

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
		 WHERE order_number = $1 RETURNING id`, number).Scan(&orderID); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}

	for i := range 3 {
		var returned int64
		if err := pool.QueryRow(ctx,
			`SELECT reverse_order_credit($1)`, orderID).Scan(&returned); err != nil {
			t.Fatalf("reverse %d: %v", i+1, err)
		}
		want := int64(20000)
		if i > 0 {
			want = 0
		}
		if returned != want {
			t.Errorf("call %d returned %d cents, want %d", i+1, returned, want)
		}
	}
	if got := creditBalance(t, accountID); got != 50000 {
		t.Errorf("balance after three reversals = %d, want 50000", got)
	}
}

func TestAShippedOrdersCreditIsNotReversed(t *testing.T) {
	ctx := t.Context()
	userID, accountID := creditedAccount(t, 50000)
	number := pendingOrderSpendingCredit(t, userID, 20000)

	// Through the real transitions: orders_check_transition refuses pending →
	// shipped, because an order is picked before it leaves.
	var orderID uuid.UUID
	for _, status := range []string{"picking", "shipped"} {
		if err := pool.QueryRow(ctx,
			`UPDATE orders SET fulfillment_status = $2 WHERE order_number = $1
			 RETURNING id`, number, status).Scan(&orderID); err != nil {
			t.Fatalf("move the order to %s: %v", status, err)
		}
	}

	var returned int64
	err := pool.QueryRow(ctx, `SELECT reverse_order_credit($1)`, orderID).Scan(&returned)
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "store_credit_posting_matches_order" {
		t.Errorf("reversing a shipped order's credit = %v, want "+
			"store_credit_posting_matches_order", err)
	}
	if got := creditBalance(t, accountID); got != 30000 {
		t.Errorf("balance = %d, want 30000 — the refusal must not have paid anything", got)
	}
}

// creditedAccount is creditedCustomer plus the account id.
func creditedAccount(t *testing.T, cents int64) (userID, accountID uuid.UUID) {
	t.Helper()
	userID = creditedCustomer(t, cents)
	if err := pool.QueryRow(t.Context(),
		`SELECT id FROM store_credit_accounts WHERE user_id = $1`, userID).
		Scan(&accountID); err != nil {
		t.Fatalf("read credit account: %v", err)
	}
	return userID, accountID
}

// creditBalance is what the ledger sums to for one account.
func creditBalance(t *testing.T, accountID uuid.UUID) int64 {
	t.Helper()
	var cents int64
	if err := pool.QueryRow(t.Context(),
		`SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE account_id = $1`,
		accountID).Scan(&cents); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return cents
}

// pendingOrderSpendingCredit places a pending unpaid order for userID that
// spends `cents` of their credit — the state a cancellation acts on.
func pendingOrderSpendingCredit(t *testing.T, userID uuid.UUID, cents int64) string {
	t.Helper()
	ctx := t.Context()

	// One transaction: orders_has_lines is DEFERRED, so an order and its lines have
	// to commit together.
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
		VALUES ($1, 'CREDIT-SKU', '測試商品', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'credit@example.com', '額度', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT post_store_credit($1, $2, '訂單折抵', $3, $4, NULL)`,
		userID, -cents, orderID, "spend:"+orderID.String()); err != nil {
		t.Fatalf("spend credit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fixture: %v", err)
	}
	return number
}

func TestAnOrderSaysWhyItWasDiscounted(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	code := "SAVE" + strings.ToUpper(uuid.NewString()[:6])
	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents)
		VALUES ($1, '滿額折抵', 'amount', 20000)`, code); err != nil {
		t.Fatalf("create coupon: %v", err)
	}

	number := orderWithCoupon(t, code)

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if view.DiscountCents != 20000 {
		t.Fatalf("discount is %d, want 20000", view.DiscountCents)
	}
	if !strings.Contains(view.DiscountReason, code) {
		t.Errorf("the order does not say which coupon: %q", view.DiscountReason)
	}
	if !strings.Contains(view.DiscountReason, "滿額折抵") {
		t.Errorf("the order does not say what the coupon was for: %q", view.DiscountReason)
	}
}

func TestAnOrderWithNoCouponHasNoReason(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	number := orderOfVariants(t, map[uuid.UUID]int32{freshVariant(t, "no-coupon"): 1})

	view, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if view.DiscountReason != "" {
		t.Errorf("an order with no coupon reports %q, want empty", view.DiscountReason)
	}
}

// orderWithCoupon places an order that redeems a coupon, and returns its number.
func orderWithCoupon(t *testing.T, code string) string {
	t.Helper()
	ctx := t.Context()
	vid := freshVariant(t, "coupon-order")
	number := orderOfVariants(t, map[uuid.UUID]int32{vid: 1})

	var orderID, couponID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id FROM coupons WHERE upper(code) = upper($1)`, code).Scan(&couponID); err != nil {
		t.Fatalf("read coupon: %v", err)
	}
	// The discount and the redemption together: coupon_redemption_matches_order
	// holds them to each other.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET discount_cents = 20000 WHERE id = $1`, orderID); err != nil {
		t.Fatalf("set the discount: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT redeem_coupon($1, $2, NULL, 20000)`, couponID, orderID); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number
}

func TestAGuestCanFindTheirOwnOrderWithTheEmail(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	number := orderOfVariants(t, map[uuid.UUID]int32{freshVariant(t, "find-mine"): 1})

	var addr string
	if err := pool.QueryRow(ctx, `
		SELECT pd.email FROM order_private_data pd
		JOIN orders o ON o.id = pd.order_id WHERE o.order_number = $1`,
		number).Scan(&addr); err != nil {
		t.Fatalf("read the order's address: %v", err)
	}

	ok, err := s.FindOrder(ctx, number, addr)
	if err != nil {
		t.Fatalf("FindOrder: %v", err)
	}
	if !ok {
		t.Error("the order's own number and address did not find it")
	}

	if ok, err := s.FindOrder(ctx, "  "+strings.ToLower(number)+" ", strings.ToUpper(addr)); err != nil {
		t.Fatalf("FindOrder with odd casing: %v", err)
	} else if !ok {
		t.Error("the lookup refused its own order over case or whitespace")
	}
}

func TestTheWrongEmailFindsNothing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	number := orderOfVariants(t, map[uuid.UUID]int32{freshVariant(t, "find-wrong"): 1})

	for _, addr := range []string{
		"somebody-else@example.com",
		"",
		"x" + uuid.NewString() + "@example.com",
	} {
		if ok, err := s.FindOrder(ctx, number, addr); err != nil {
			t.Fatalf("FindOrder(%q): %v", addr, err)
		} else if ok {
			t.Errorf("the lookup accepted %q for somebody else's order", addr)
		}
	}
}

func TestAnErasedOrderCannotBeFound(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	number := orderOfVariants(t, map[uuid.UUID]int32{freshVariant(t, "find-erased"): 1})

	var addr string
	if err := pool.QueryRow(ctx, `
		SELECT pd.email FROM order_private_data pd
		JOIN orders o ON o.id = pd.order_id WHERE o.order_number = $1`,
		number).Scan(&addr); err != nil {
		t.Fatalf("read the address: %v", err)
	}
	if ok, err := s.FindOrder(ctx, number, addr); err != nil || !ok {
		t.Fatalf("the order could not be found before erasure: ok=%v err=%v", ok, err)
	}

	// Erased the way erase_user leaves it: every field NULL and erased_at stamped.
	if _, err := pool.Exec(ctx, `
		UPDATE order_private_data pd SET
			email = NULL, recipient_name = NULL, phone = NULL, postal_code = NULL,
			city = NULL, district = NULL, street = NULL, erased_at = now()
		FROM orders o WHERE pd.order_id = o.id AND o.order_number = $1`,
		number); err != nil {
		t.Fatalf("erase the delivery details: %v", err)
	}

	if ok, err := s.FindOrder(ctx, number, addr); err != nil {
		t.Fatalf("FindOrder after erasure: %v", err)
	} else if ok {
		t.Error("an erased order can still be found by the address it no longer holds")
	}
}

// TestAnOrderLineIsSnapshottedInTheBuyersLanguage. order_lines.product_name is
// a SNAPSHOT, so the language is decided at PLACEMENT by the cart read that
// localizes; a receipt already sent must not start disagreeing with itself.
func TestAnOrderLineIsSnapshottedInTheBuyersLanguage(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	vid, zhName, enName := translatedVariant(t)

	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
		other  string
	}{
		{name: "a Chinese buyer", locale: i18n.ZhHant, want: zhName, other: enName},
		{name: "an English buyer", locale: i18n.En, want: enName, other: zhName},
	} {
		t.Run(tt.name, func(t *testing.T) {
			buying := i18n.WithLocale(ctx, tt.locale)
			number := placeOrderInLocale(t, buying, s, vid, string(tt.locale))

			var snapshot string
			if err := pool.QueryRow(ctx, `
				SELECT ol.product_name FROM order_lines ol
				JOIN orders o ON o.id = ol.order_id
				WHERE o.order_number = $1`, number).Scan(&snapshot); err != nil {
				t.Fatalf("read the line: %v", err)
			}
			if snapshot != tt.want {
				t.Errorf("the line was snapshotted as %q, want %q", snapshot, tt.want)
			}
			if snapshot == tt.other {
				t.Errorf("the line was snapshotted in the other language: %q", snapshot)
			}
		})
	}
}

// placeOrderInLocale puts one unit of vid through a cart and places the order,
// carrying the buying context's locale.
func placeOrderInLocale(
	t *testing.T, ctx context.Context, s *cart.Store, vid uuid.UUID, key string,
) string {
	t.Helper()

	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).
		Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: "snapshot@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil,
		"snapshot-"+key+"-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	return number
}

// translatedVariant makes a product whose Chinese and English names differ, and
// returns the variant id and both names.
func translatedVariant(t *testing.T) (variantID uuid.UUID, zhName, enName string) {
	t.Helper()
	ctx := t.Context()

	suffix := uuid.NewString()[:8]
	zhName, enName = "快照測試 "+suffix, "Snapshot Test "+suffix
	var productID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, name_en, status,
		                      published_at)
		SELECT b.id, c.id, $1, $2, $3, 'draft', now()
		FROM brands b, categories c
		WHERE b.slug = 'koto' AND c.parent_id IS NULL
		ORDER BY c.position LIMIT 1
		RETURNING id`, "snapshot-"+suffix, zhName, enName).Scan(&productID); err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, safety_stock)
		VALUES ($1, $2, 100000, 0) RETURNING id`,
		productID, "SNAP-"+strings.ToUpper(suffix)).Scan(&variantID); err != nil {
		t.Fatalf("create variant: %v", err)
	}
	// Stock arrives through the ledger, the only door that writes stock_quantity.
	if _, err := pool.Exec(ctx,
		`SELECT record_inventory_movement($1, 5, 'receipt', $2, NULL, NULL)`,
		variantID, "snapshot-stock-"+suffix); err != nil {
		t.Fatalf("stock the variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active' WHERE id = $1`, productID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return variantID, zhName, enName
}

func TestTheCheckoutOffersDeliveryInTheVisitorsLanguage(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	// An empty cart: what a method is OFFERED for depends on what is in the basket.
	id := newCart(t, s)

	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
		absent string
	}{
		{
			name: "Chinese", locale: i18n.ZhHant,
			want: "宅配到府", absent: "Home delivery",
		},
		{
			name: "English", locale: i18n.En,
			want: "Home delivery", absent: "宅配到府",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			choices, err := s.ShippingChoices(i18n.WithLocale(ctx, tt.locale), id, 100000)
			if err != nil {
				t.Fatalf("ShippingChoices: %v", err)
			}
			names := make([]string, 0, len(choices))
			for i := range choices {
				names = append(names, choices[i].Name)
			}
			if !slices.Contains(names, tt.want) {
				t.Errorf("the chooser offers %v, want %q among them", names, tt.want)
			}
			if slices.Contains(names, tt.absent) {
				t.Errorf("the chooser offers %q, the other language: %v", tt.absent, names)
			}
		})
	}
}

// placedCookie issues a REAL access token through the same grant the checkout
// writes: a fixture putting the order NUMBER in the cookie would assert a
// forgeable design rather than the rule.
func placedCookie(t *testing.T, s *cart.Store, number string) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	if err := s.RememberOrder(t.Context(), w, r, number, false); err != nil {
		t.Fatalf("grant access to %s: %v", number, err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "goen_placed" {
			return c
		}
	}
	t.Fatalf("RememberOrder set no goen_placed cookie")
	return nil
}

// TestASecondOrderKeepsTheFirstOnesGrantAlive. The cookie is re-issued with a
// fresh MaxAge on every order while a grant is swept on its own created_at, so
// equal durations are not equal deadlines unless the second order restarts it.
func TestASecondOrderKeepsTheFirstOnesGrantAlive(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil)

	first := placeUnpaidOrderFor(t, s, "twice@example.com")
	firstCookie := placedCookie(t, s, first)

	// Aged PAST the retention window: just inside it, the sweep spares the row
	// either way and the case stays green with the refresh deleted.
	if _, err := pool.Exec(ctx, `
		UPDATE order_access_grants SET created_at = now() - $2::interval
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`,
		first, (cart.GrantRetain + time.Hour).String()); err != nil {
		t.Fatalf("age the first grant: %v", err)
	}

	second := placeUnpaidOrderFor(t, s, "twice@example.com")
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
	r.AddCookie(firstCookie)
	if err := s.RememberOrder(ctx, w, r, second, false); err != nil {
		t.Fatalf("remember the second order: %v", err)
	}
	var carried *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "goen_placed" {
			carried = c
		}
	}
	if carried == nil {
		t.Fatal("the second order set no cookie")
	}
	if !strings.Contains(carried.Value, firstCookie.Value) {
		t.Fatalf("the re-issued cookie dropped the first order's token; " +
			"this test would pass for the wrong reason")
	}

	if err := s.SweepAttempts(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+first, http.NoBody)
	req.SetPathValue("number", first)
	req.AddCookie(carried)
	rec := httptest.NewRecorder()
	h.OrderPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("the browser that placed this order got %d for it, want 200 — its "+
			"grant was swept while the cookie carrying it was still live, which is "+
			"the state GrantRetain's own comment says must never happen", rec.Code)
	}
}

func TestAForgedPlacedCookieReachesNothing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil)
	number := placeUnpaidOrderFor(t, s, "forged@example.com")

	// The VICTIM's own browser holds a REAL grant before the attack starts: without
	// it the access query's EXISTS is false whatever the digest comparison does, and
	// the case stays green with `g.digest = ANY(...)` replaced by `OR true`.
	victim := placedCookie(t, s, number)

	for _, value := range []string{
		number,
		nextOrderNumber(number),
		number + "." + number,
	} {
		t.Run(value, func(t *testing.T) {
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
			r.SetPathValue("number", number)
			r.AddCookie(&http.Cookie{Name: "goen_placed", Value: value}) //nolint:gosec // G124: the forgery under test
			w := httptest.NewRecorder()
			h.OrderPage(w, r)

			if w.Code != http.StatusNotFound {
				t.Errorf("a forged cookie reached the order page: status %d, want 404", w.Code)
			}
			if strings.Contains(w.Body.String(), "forged@example.com") {
				t.Error("a forged cookie leaked the customer's email")
			}
		})
	}

	held := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	held.SetPathValue("number", number)
	held.AddCookie(victim)
	ok := httptest.NewRecorder()
	h.OrderPage(ok, held)
	if ok.Code != http.StatusOK {
		t.Fatalf("the browser holding a real token got %d, want 200", ok.Code)
	}
}

func TestATokenReachesOnlyItsOwnOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil)

	mine := placeUnpaidOrderFor(t, s, "mine@example.com")
	theirs := placeUnpaidOrderFor(t, s, "theirs@example.com")

	placedCookie(t, s, theirs)

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+theirs, http.NoBody)
	r.SetPathValue("number", theirs)
	r.AddCookie(placedCookie(t, s, mine))
	w := httptest.NewRecorder()
	h.OrderPage(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("a token for %s reached %s: status %d", mine, theirs, w.Code)
	}
	if strings.Contains(w.Body.String(), "theirs@example.com") {
		t.Error("one order's token leaked another order's email")
	}
}

// nextOrderNumber increments the counter half of an order number.
func nextOrderNumber(number string) string {
	i := strings.LastIndex(number, "-")
	if i < 0 {
		return number
	}
	n, err := strconv.Atoi(number[i+1:])
	if err != nil {
		return number
	}
	return fmt.Sprintf("%s-%06d", number[:i], n+1)
}

// testLimiter is generous on purpose: these cases are about an ANSWER, and a
// limiter that refused mid-suite would be testing the limiter.
func testLimiter() *ratelimit.Limiter {
	return ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour})
}

// placeUnpaidOrderFor places one order for an address, through the store's own
// checkout.
func placeUnpaidOrderFor(t *testing.T, s *cart.Store, address string) string {
	t.Helper()
	ctx := t.Context()

	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).
		Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &cart.Address{
		Email: address, Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, nil,
		"forge-"+uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	return number
}

// TestASpentCouponComesBackAsAFieldErrorNotA500 drives the checkout handler,
// because the branch that maps ErrCouponUsedUp to a 422 lives there and nothing
// else reaches it. FindCoupon deliberately reads no limit — they are counted
// under redeem_coupon's lock — so a spent code passes the form validation EVERY
// time and is refused inside the transaction EVERY time. That makes this an
// ordinary outcome on the buying mainline, and the failure it replaced discarded
// the whole address the customer had just typed.
func TestASpentCouponComesBackAsAFieldErrorNotA500(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	code := "SPENT" + strings.ToUpper(uuid.NewString()[:6])
	if _, err := pool.Exec(ctx, `
		INSERT INTO coupons (code, description, kind, amount_cents, max_redemptions)
		VALUES ($1, '已用完', 'amount', 10000, 1)`, code); err != nil {
		t.Fatalf("create coupon: %v", err)
	}

	// Spend the only slot through the door a checkout uses, so the state under
	// test is one the application can actually produce.
	spender := newCart(t, s)
	variant := variantOf(t, "pixelight-9-pro", true)
	if err := s.Add(ctx, spender, variant, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	spent, err := s.FindCoupon(ctx, code, 3390000, 8000)
	if err != nil {
		t.Fatalf("find the coupon: %v", err)
	}
	addr := &cart.Address{
		Email: "spender@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	if _, placeErr := s.PlaceOrder(ctx, spender, uuid.NullUUID{}, shipID, addr, nil, spent,
		"coupon-spend-"+code); placeErr != nil {
		t.Fatalf("spend the slot: %v", placeErr)
	}

	// A second customer types the same code.
	token, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	second, err := s.Create(ctx, token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(ctx, second, variant, 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	form := url.Values{
		"email": {"late@example.com"}, "name": {"李大華"}, "phone": {"0987654321"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":      {"松仁路 100 號"},
		"shipping":    {shipID.String()},
		"coupon":      {code},
		"idempotency": {"late-" + uuid.NewString()[:8]},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie, read back by this handler
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		nil)
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a spent coupon answered %d, want 422 — a 500 discards everything "+
			"the customer typed at the moment of paying", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "松仁路 100 號") {
		t.Error("the re-rendered form lost the street the customer had typed")
	}
	if !strings.Contains(body, "late@example.com") {
		t.Error("the re-rendered form lost the email the customer had typed")
	}
	if !strings.Contains(body, i18n.T(ctx, i18n.KeyCouponUsedUp)) {
		t.Error("the page does not say the coupon is spent, so the customer is " +
			"left to guess which field was refused")
	}
}

// TestPressingUpdateChangesTheChoiceAndPlacesNothing is the SERVER half of the
// chooser. The template half is locked by a markup test, and with the branch in
// handler.go deleted every one of those stayed green — while the 更新 button
// carries formnovalidate, so a form the customer had already filled in fell
// straight through and PLACED THE ORDER. Somebody changing their 發票 type
// bought the basket.
func TestPressingUpdateChangesTheChoiceAndPlacesNothing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	token, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	id, err := s.Create(ctx, token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}

	// A COMPLETE form: every field the server would demand is present, so the
	// only thing standing between this submission and an order is the branch.
	form := url.Values{
		"email": {"update@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":      {"松高路 68 號"},
		"shipping":    {shipID.String()},
		"idempotency": {"upd-" + uuid.NewString()[:8]},
		"update":      {"1"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie, read back by this handler
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		nil)
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("changing a choice answered %d, want 200 — a 303 means it placed "+
			"the order somebody was still filling in", res.Code)
	}
	if loc := res.Header().Get("Location"); loc != "" {
		t.Errorf("changing a choice redirected to %q; nothing was written, so there "+
			"is nowhere to go", loc)
	}
	// The values come back: the whole reason this is a submission and not a link.
	if body := res.Body.String(); !strings.Contains(body, "松高路 68 號") {
		t.Error("changing a choice discarded the address already typed, which is " +
			"what the link this replaced did")
	}

	var orders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_private_data WHERE email = 'update@example.com'`).
		Scan(&orders); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if orders != 0 {
		t.Errorf("%d order(s) were placed by pressing 更新", orders)
	}
	// And the cart is still there to finish.
	view, viewErr := s.View(ctx, id)
	if viewErr != nil {
		t.Fatalf("read the cart: %v", viewErr)
	}
	if len(view.Lines) == 0 {
		t.Error("the cart was emptied by a chooser change")
	}
}

// TestPickingASavedAddressFillsTheForm is the chooser nothing rendered.
//
// Every checkout fixture left SavedAddresses empty, so OffersTheAddressBook()
// was false and the address radios appeared in no test — while on the live page
// a hidden input carried the same NAME and came FIRST, so PostFormValue
// returned it every time and the pick was discarded. A repeat customer with two
// saved addresses could use only the default, for ever, on the buying mainline.
func TestPickingASavedAddressFillsTheForm(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, full_name)
		VALUES ('book-' || gen_random_uuid() || '@goen.invalid', '王小明')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("register: %v", err)
	}
	// TWO addresses, and the one we pick is NOT the default: with one, or with
	// the default picked, "fills from the book" and "keeps what was there" are
	// the same outcome and the fixture tests neither.
	var otherID uuid.UUID
	if _, err := pool.Exec(ctx, `
		INSERT INTO addresses (user_id, recipient_name, phone, postal_code, city,
		                       district, street, is_default)
		VALUES ($1, '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', true)`,
		userID); err != nil {
		t.Fatalf("save the default address: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO addresses (user_id, recipient_name, phone, postal_code, city,
		                       district, street, is_default)
		VALUES ($1, '李大華', '0987654321', '407', '台中市', '西屯區', '文心路 200 號', false)
		RETURNING id`, userID).Scan(&otherID); err != nil {
		t.Fatalf("save the second address: %v", err)
	}

	token, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	id, err := s.Create(ctx, token, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}

	// The form as the browser posts it when the SECOND address is ticked and
	// 更新 is pressed: the radio's value, and the fields still holding the
	// default the page was rendered with.
	form := url.Values{
		"email": {"book@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":   {"松高路 68 號"},
		"shipping": {shipID.String()},
		"address":  {otherID.String()},
		"update":   {"address"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie, read back by this handler
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	req = req.WithContext(account.WithUser(ctx, account.User{ID: userID.String(), Role: "customer"}))

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		nil)
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("picking a saved address answered %d, want 200", res.Code)
	}
	// On the FIELD's value, not anywhere in the body: both addresses appear on
	// the page, because the chooser lists them.
	body := res.Body.String()
	if !strings.Contains(body, `value="文心路 200 號"`) {
		t.Error("picking the second saved address did not fill the street field " +
			"with it, so a repeat customer retypes an address they have already " +
			"given us")
	}
	if strings.Contains(body, `value="松高路 68 號"`) {
		t.Error("the street field still holds the address that was NOT picked")
	}
	if !strings.Contains(body, `value="台中市"`) {
		t.Error("the city field did not follow the chosen address")
	}
	if !strings.Contains(body, `value="407"`) {
		t.Error("the postal code did not follow the chosen address, so the " +
			"delivery fee would be priced for the wrong place")
	}
}

// TestChangingAnotherChoiceKeepsATypedAddress is why 更新 names its chooser.
// Filling from the book on every re-render would wipe an address somebody had
// typed by hand the moment they changed their 發票 type.
func TestChangingAnotherChoiceKeepsATypedAddress(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, full_name)
		VALUES ('typed-' || gen_random_uuid() || '@goen.invalid', '王小明')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO addresses (user_id, recipient_name, phone, postal_code, city,
		                       district, street, is_default)
		VALUES ($1, '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', true)`,
		userID); err != nil {
		t.Fatalf("save an address: %v", err)
	}

	token, err := cart.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	id, err := s.Create(ctx, token, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}

	form := url.Values{
		"email": {"typed@example.com"}, "name": {"李大華"}, "phone": {"0987654321"},
		"postal_code": {"407"}, "city": {"台中市"}, "district": {"西屯區"},
		"street":   {"手打的地址 1 號"},
		"shipping": {shipID.String()},
		"update":   {"invoice"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie, read back by this handler
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
	req = req.WithContext(account.WithUser(ctx, account.User{ID: userID.String(), Role: "customer"}))

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		nil)
	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if body := res.Body.String(); !strings.Contains(body, "手打的地址 1 號") {
		t.Error("changing the 發票 type overwrote an address typed by hand with " +
			"the saved one, which is the whole reason 更新 says which chooser it is")
	}
}
