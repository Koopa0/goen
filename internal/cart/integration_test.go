//go:build integration

package cart_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ratelimit"
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

// variant returns a sellable variant of a product, and one that is not.
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

// TestCartIsFoundByTokenNotByID is the access rule for a guest cart. The cookie
// is the only credential, and it is stored hashed — so a cart is reachable by
// its token and by nothing else.
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

	// The token itself must not be in the table — only its digest.
	var stored []byte
	if err := pool.QueryRow(t.Context(),
		`SELECT token_hash FROM carts WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatalf("read token_hash: %v", err)
	}
	if string(stored) == tok {
		t.Error("the cart table stores the raw token; a leak would hand over live cart cookies")
	}
}

// TestAddRefusesWhatCannotBeSold covers the check that happens before the
// write. Without it an unsellable variant lands in the cart and fails later, at
// the inventory hold, after an address has been typed.
func TestAddRefusesWhatCannotBeSold(t *testing.T) {
	s := cart.NewStore(pool)
	id := newCart(t, s)

	// Bound to WHICH error, not merely that one happened. Without the guard the
	// quantity clamps to zero and the schema's own cart_items_quantity_in_range
	// CHECK refuses the insert — the cart is still protected, but by accident
	// and with a message no visitor can be shown. Asserting on ErrUnavailable is
	// what tells the two apart.
	unsellable := variantOf(t, "meridian-book-14", false) // wholly sold out in the seed
	err := s.Add(t.Context(), id, unsellable, 1)
	if err == nil {
		t.Fatal("a sold-out variant was added to a cart")
	}
	if !errors.Is(err, cart.ErrUnavailable) {
		t.Errorf("refused with %v, want ErrUnavailable — the store must recognise this "+
			"before the write, not leave it to a constraint violation", err)
	}

	// Control: a sellable one goes in, so the refusal above is not "refuses
	// everything".
	ok := variantOf(t, "pixelight-9-pro", true)
	if err := s.Add(t.Context(), id, ok, 1); err != nil {
		t.Fatalf("a sellable variant was refused: %v", err)
	}
}

// TestAddClampsToWhatCanBeSupplied pins that asking for more than exists does
// not put an impossible quantity in the cart. Without the clamp the line sits
// there looking fine and fails at the inventory hold, after an address has been
// typed.
func TestAddClampsToWhatCanBeSupplied(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	// A variant with a known, small supply. Restored afterwards so the seed the
	// other tests read stays as it was.
	vid := freshVariant(t, "stockfix-1")
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		vid).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		// context.Background, not t.Context: Go cancels the test's context just
		// before cleanup runs, so t.Context() here would abort the restore and
		// leave the seed altered for every test after this one.
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

// TestCartShowsCurrentPriceAndAvailability is the rule that a cart line is not
// a promise. A variant can sell out while it sits there, and the page must say
// so rather than quoting a total the checkout will refuse.
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

// TestPlaceOrderIsIdempotent is what stops a double-click and a back button
// from becoming two orders. The form carries a key; the same key must find the
// order it already placed.
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

// TestPlaceOrderEmptiesTheCart pins that the cart and the order move together.
// Emptying afterwards would leave a window where the order exists and the cart
// still looks full, and a failure in that window loses or duplicates the order.
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

// TestPlaceOrderRefusesAFabricatedShippingVersion is what stops a hand-edited
// form from placing an order at a fee that was never offered. The version is
// re-read from the database, and the fee is computed from what it says.
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

// TestPlaceOrderRefusesAnEmptyCart covers the deferred orders_have_lines
// constraint from the application side: an order with no lines is not a legal
// row, so the attempt must fail before it starts rather than at commit.
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

// TestOrderPricesAreCopiedNotReferenced pins that an order records what was
// agreed. A later price change must not rewrite an order that has been placed.
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

	// The catalogue price moves. The placed order must not.
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = price_cents + 100000 WHERE id = $1`, vid); err != nil {
		t.Fatalf("reprice: %v", err)
	}
	t.Cleanup(func() {
		// See above: t.Context() is cancelled before Cleanup runs.
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

// TestOrderConfirmationIsNotEnumerable is the access rule on the confirmation
// page, and it exists because of what an order number IS.
//
// next_order_number() produces GO-YYMMDD-NNNNNN off a per-day counter, so
// numbers are sequential and guessable. The page carries an email and a
// delivery address. Reachable by number alone, it is an enumeration hole:
// increment the digits and read the next customer's details.
//
// Access is therefore the browser that placed it, or the account that owns it.
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
		// Generous: these cases are about the lookup's ANSWER, and a limiter that
		// refused mid-suite would be testing the limiter.
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		// nil: no Stripe key in an integration test, so no session was ever opened
		// and there is nothing for a cancellation to close.
		nil)

	// A browser that did not place it and is not signed in.
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

	// The browser that placed it does reach the page — the control, without
	// which a handler that 404s unconditionally would pass the check above.
	placer := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	placer.SetPathValue("number", number)
	// A bare test cookie: the attributes the handler sets are not what is under
	// test here, and adding them would assert the fixture rather than the rule.
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

// TestOrderIsAttachedToASignedInCustomer pins that a signed-in checkout
// produces an order the account can see. Without the owner the order is a
// guest's: it never appears in the history, and the customer has only the
// confirmation link.
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

	// And it is not attached to somebody else.
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

// TestCheckoutHoldsStock is the correctness rule the cart existed without.
//
// Placing an order must reserve what it promises. Without a hold, two customers
// each read "one available", each write an order, and nothing between them
// decrements anything — both succeed and one of them will never be shipped.
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

	// The delta, not the absolute figure. Earlier tests in this package place
	// orders too, and now that placing one holds stock, a test asserting
	// "stock is exactly 2" or "one unit is held anywhere" is measuring the
	// whole package's history rather than this order.
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

	// Held against THIS order, which is the claim being made.
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

	// The shelf and the ledger move together: a hold that decrements stock
	// without posting a movement is how an audit stops adding up.
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
// safely. Both transactions are open at once, and the second must be refused by
// record_inventory_movement's floor rather than by luck.
//
// T1's transaction is held OPEN while T2 runs. Two goroutines with a start
// channel finish microseconds apart and never actually overlap — the failure
// mode CLAUDE.md names — so the overlap is constructed explicitly.
//
// Both orders are created and COMMITTED first. next_order_number() locks the
// per-day counter row, so creating the second order inside a second open
// transaction blocks on the first and the test deadlocks before it reaches the
// contention it is about to measure. The orders are setup; only the holds race.
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

	// T1 takes the last unit and stays open.
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

	// T2 tries for the same unit while T1 is still open. It must block on the
	// row lock and then be refused — not succeed.
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin t2: %v", err)
	}
	defer func() { _ = tx2.Rollback(ctx) }()

	// T2's backend id, read BEFORE the racing statement, so the wait below can
	// watch the right connection.
	var pid int
	if err := tx2.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read t2 pid: %v", err)
	}

	done := make(chan error, 1)
	// t.Context() and not context.Background(): the goroutine must be cancelled
	// with the test, or a hold that never unblocks outlives it.
	raceCtx := t.Context()
	go func() {
		_, holdErr := tx2.Exec(raceCtx,
			`SELECT hold_inventory($1, $2, 1, now() + interval '30 minutes', $3)`,
			order2, vid, "race-t2")
		done <- holdErr
	}()

	// Wait until T2 is ACTUALLY blocked on a lock, then release T1.
	//
	// This used to be time.Sleep(300ms) and a hope. A fixed pause makes the
	// interleaving an assumption: on a loaded machine T2 may not have reached
	// the lock yet, T1 commits first, and the test passes having proven nothing
	// about contention — green for the wrong reason, which is the failure mode
	// a concurrency test can least afford.
	waitUntilBlocked(t, pid, done)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("commit t1: %v", err)
	}

	select {
	case holdErr := <-done:
		if holdErr == nil {
			t.Fatal("both orders held the same last unit; the stock floor did not bite")
		}
		// Which rule refused matters. A hold can fail for reasons that are not
		// the stock floor — a bad order id, a duplicate idempotency key — and a
		// test that accepts any error would stay green when the floor is gone.
		// The name is on PgError, not in the message text.
		pgErr, ok := errors.AsType[*pgconn.PgError](holdErr)
		if !ok || pgErr.ConstraintName != "inventory_never_negative" {
			t.Errorf("t2 was refused by %v, want constraint inventory_never_negative", holdErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("t2 never returned; the two holds are not contending for the same row")
	}
}

// commitBareOrder writes the minimum an order needs to exist and commits it, so
// the transactions that race afterwards contend only over stock.
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

// heldOrder writes an order holding one unit, with a hold that expired `ago`
// in the past. paid decides whether it is funded.
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
	// Held normally, then aged — BOTH timestamps move, because the schema keeps
	// expires_at > created_at and a hold that expired an hour ago was taken out
	// before that. Backdating only the expiry fabricates a row time could never
	// produce, and the CHECK says so.
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

// TestSweepReturnsAbandonedHoldsToTheShelf proves an abandoned checkout's stock
// comes back.
//
// Nothing released these before. An abandoned checkout held its stock until the
// row was deleted by hand, so a customer saw "out of stock" for goods sitting
// against a session somebody closed half an hour ago.
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

	// The shelf and the ledger move together, or an audit stops adding up.
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

// TestSweepNeverTouchesAPaidOrdersHold is the one that costs money.
//
// The LOCK here is the database: release_reservation refuses a committed
// order's hold outright, and internal/db proves that bound to
// inventory_reservation_committed_no_release. The query filter this test also
// exercises is defence in depth — removing it leaves this test green, because
// the function refuses anyway. Recorded rather than dressed up as a lock it
// does not hold.
//
// A paid order's stock is spoken for. Releasing it puts goods back on the shelf
// that are going to be shipped, so the next customer buys something that is
// already gone — an oversell created by the cleanup, not by the sale.
func TestSweepNeverTouchesAPaidOrdersHold(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-3")

	// Expired AND paid. The expiry is what makes this a real test: a sweeper
	// that only skipped unexpired holds would pass with the funding check gone.
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
	// The control: without it, a sweeper that released nothing at all would pass.
	if abandonedState != "released" {
		t.Errorf("the abandoned hold is %q, want released", abandonedState)
	}
}

// TestSweepNeverTouchesAFullyCreditFundedOrdersHold is the other half of the
// test above, and the half committed_orders cannot answer.
//
// The paid fixture opens and captures a payment, so committed_orders sees a
// succeeded row and the sweeper stands off. A fully store-credited order has no
// payment row and CANNOT have one — payments_succeeded_is_captured forbids a
// zero-value succeeded payment — and it stays 'pending' until a human picks it,
// because orders_funded_to_leave_pending has nothing left to demand. So the view
// reported it uncommitted while the customer had paid in full, and the sweeper
// put the units back on the shelf thirty minutes later.
//
// Measured before it was fixed: stock 10 -> 11 on an order that then SHIPPED,
// consuming no reservation, leaving the shelf permanently one too high.
func TestSweepNeverTouchesAFullyCreditFundedOrdersHold(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-credit")

	var before int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity FROM product_variants WHERE id = $1`, vid).Scan(&before); err != nil {
		t.Fatalf("read stock: %v", err)
	}

	// Expired AND funded. The expiry is what makes this a real test: a sweeper
	// that only skipped unexpired holds would pass with the funding check gone.
	funded := creditFundedHeldOrder(t, vid, time.Hour)
	// The control. Without it a sweeper that released nothing at all would pass.
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

	// The shelf, not just the row: two units left it and exactly one came back.
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

// TestReleasingAFundedOrdersHoldIsRefusedByName holds the DOOR rather than the
// query that walks up to it.
//
// ExpiredReservations declines to offer a funded order's hold, and a guard that
// lives only in the query it is written for is a guard the next caller does not
// get. The refusal is bound to the constraint NAME because a statement meant to
// prove one rule often trips another first (CLAUDE.md #8), and because the
// sweeper's own classification reads that name to tell "being safe" from
// "broken".
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

// creditFundedHeldOrder writes an order holding one unit of vid whose whole
// total is paid from store credit, with a hold that expired `ago` in the past.
//
// Written by hand rather than through PlaceOrder because what matters is the
// FUNDING shape — owing nothing, with no payment row, still pending — and
// PlaceOrder would need a cart, a session and a shipping choice to reach it.
func creditFundedHeldOrder(t *testing.T, vid uuid.UUID, ago time.Duration) (orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	// The line price IS the order total: no shipping, no discount, no tax. That
	// is what makes order_amount_owed come to exactly zero once the credit is
	// spent, which is the state under test.
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
	// Held normally, then aged — BOTH timestamps move, for the reason heldOrder
	// gives: expires_at > created_at is a CHECK, and a hold that expired an hour
	// ago was taken out before that.
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

// TestSweepLeavesUnexpiredHolds. A customer typing a card number must not lose
// the item under them.
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

// TestSweepIsIdempotent. Two instances of the binary sweep the same rows, and
// the loser must not turn a released reservation into an error the logs fill up
// with.
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

// TestSweepCountsABenignRefusalAsSkippedNotFailed proves the sweeper tells a
// safe refusal apart from a real failure.
//
// "Another instance released it first" and "the order became committed" are the
// sweeper being safe. If those were counted as failures an operator would see a
// permanent error rate and learn to ignore it — which is how a real failure
// then goes unnoticed.
func TestSweepCountsABenignRefusalAsSkippedNotFailed(t *testing.T) {
	ctx := t.Context()
	vid := freshVariant(t, "stockfix-6")

	// Expired, and paid — so it appears to a sweeper whose query filter is gone
	// and is refused by release_reservation. That is exactly the benign case.
	paid := heldOrder(t, vid, time.Hour, true)

	// Reach past the query and hand the reservation straight to the release, so
	// the refusal happens for certain rather than being filtered out first.
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

	// And a refusal that is NOT benign must still read as one.
	if cart.BenignSweepFailure(errors.New("connection reset")) {
		t.Error("an ordinary error was classified as benign")
	}
}

// TestCreditIsCappedAtWhatTheOrderOwesAfterTheDiscount holds the cap to the NET
// total.
//
// spendCredit used to cap at `subtotal + shipping`, leaving the DISCOUNT out —
// so a coupon and a credit balance on one order spent more credit than the order
// was worth, and order_amount_owed went NEGATIVE.
//
// Nothing underneath refuses that: store_credit_never_negative guards the
// ACCOUNT, not the order, and no rule caps a spend at what its order owes. The
// customer loses the difference and the order is then unpayable forever —
// FullyFunded() keeps a non-positive figure away from Stripe, while
// orders_funded_to_leave_pending asks `owed <> 0`, which a negative satisfies.
//
// The assertion is the invariant rather than an arithmetic re-derivation: an
// order may never owe less than nothing, and the debit may never exceed the
// order's own recorded total. Both are readable off the row.
func TestCreditIsCappedAtWhatTheOrderOwesAfterTheDiscount(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	// A coupon big enough that the gross and the net differ by a visible amount,
	// and a balance big enough to cover the gross — which is what makes the
	// missing discount reachable at all.
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

	// Exactly zero, and that figure is stated rather than re-derived: the balance
	// is NT$100,000 against an order of a few hundred, so credit covers the whole
	// net total and the order owes nothing. Under the old cap it is MINUS the
	// coupon — the order owes less than nothing.
	if owed != 0 {
		t.Errorf("order_amount_owed(%s) = %d, want 0. A negative figure means credit was "+
			"spent against the GROSS total, which loses the customer the discount and "+
			"leaves the order permanently unpayable", number, owed)
	}
	// And the credit really was spent, or a spendCredit that did nothing at all
	// would satisfy the line above by leaving a positive balance owing.
	if debit <= 0 {
		t.Errorf("credit debited %d on the order, want a positive spend", debit)
	}
}

// TestADoubleClickedCheckoutPlacesOneOrder holds the concurrent half of
// checkout idempotency.
//
// The serial half was already covered: a resubmit AFTER the first checkout
// committed reads the attempt row and gets the same order number. What nothing
// covered is two requests IN FLIGHT AT ONCE — a double-click, or a browser
// retrying a request it thinks timed out.
//
// Both used to read the attempt table on the POOL before either had a
// transaction, so both missed. The attempt row was written second-to-last and
// its ON CONFLICT DO NOTHING was :exec, so the row count was discarded and the
// loser committed its own order regardless. With stock for two the customer got
// two orders, two stock holds, two confirmation emails and two store-credit
// debits — the credit spend keys on the ORDER id, and there were two.
//
// # Why this is not two goroutines and a start channel
//
// Mistake #9 in this repository: they finish microseconds apart and never
// actually overlap, and every guard here stayed green with its lock removed.
// T1's transaction is held OPEN while T2 runs, so the overlap is a fact of the
// test rather than a hope about the scheduler. T2 must block on the advisory
// lock; if it does not, it is not being serialised and the test says so.
func TestADoubleClickedCheckoutPlacesOneOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	key := "double-" + uuid.NewString()
	vid := freshVariant(t, "doubleclick")

	// T1 takes the lock on the key by hand and HOLDS it, which is exactly the
	// state a first checkout is in between its lock and its commit.
	t1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin T1: %v", err)
	}
	defer func() { _ = t1.Rollback(ctx) }()
	if _, err := t1.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, key); err != nil {
		t.Fatalf("T1 take the lock: %v", err)
	}

	// T2 is a real checkout carrying the same key. It must BLOCK.
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

	// It must still be waiting. A checkout that has finished while another
	// transaction holds its key was never serialised at all.
	select {
	case err := <-placed:
		t.Fatalf("the second checkout completed (%v) while the key was held by "+
			"another transaction — it is not taking the lock", err)
	case <-time.After(750 * time.Millisecond):
	}

	// Release, and let it through.
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

// TestCreditIsSpentInsideTheOrdersOwnTransaction proves the debit lands with
// the order rather than after it.
//
// orders_funded_to_leave_pending reads the LEDGER to decide whether an order is
// funded. A debit posted after the order commits leaves a window in which a
// fully-credited order looks unpaid — and the back office could pick it up,
// find it unfunded, and refuse to ship something already paid for.
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

// TestCreditNeverExceedsWhatTheOrderOwes proves credit is capped at the total,
// and walks the zero-owed order all the way into fulfilment.
//
// A debit larger than the order hands money back as a negative balance, which
// store_credit_never_negative would refuse — but the refusal would be a failed
// checkout rather than a correct one.
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

	// A fully-credited order owes nothing and is funded with NO payment row —
	// the zero-owed path order_is_committed exists for.
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

// TestAGuestSpendsNoCredit. There is no account to hold it against.
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

// TestTheHeaderBadgeCountsWhatTheCartHolds proves the badge tracks units in
// this cart.
//
// The badge read 0 for every visitor, whatever was in their cart, for as long
// as the header existed. layouts.Page carried a CartCount field that every page
// declared and NOTHING ever assigned — the count now comes from middleware
// through the request context, because a number each handler must remember to
// fill is a number that goes unfilled.
//
// It counts UNITS, not lines: a badge reading 1 over a cart holding three of
// something is wrong in the way a visitor notices at checkout.
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
	// The same variant again: one LINE, three UNITS.
	if err := s.Add(ctx, id, a, 2); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 3 {
		t.Errorf("three units of one variant count %d, want 3 — the badge is "+
			"counting lines, not items", got)
	}
	// A second variant: two lines, four units.
	if err := s.Add(ctx, id, b, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := count(); got != 4 {
		t.Errorf("four units across two variants count %d, want 4", got)
	}

	// Another cart's contents are not this one's.
	other := newCart(t, s)
	if err := s.Add(ctx, other, a, 5); err != nil {
		t.Fatalf("add to other: %v", err)
	}
	if got := count(); got != 4 {
		t.Errorf("this cart counts %d after another cart was filled, want 4", got)
	}
}

// TestTheConfirmationMessageCommitsWithTheOrder is the whole point of an
// outbox, and it is asserted in BOTH directions.
//
// Sending from the handler is wrong in either ordering: before the commit tells
// somebody about an order that may roll back, after it loses the message when
// the process dies in between. Writing the intent to the same transaction
// removes the choice — so a placed order always has its message, and a failed
// checkout never does.
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

	// A successful order carries its message.
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

	// A checkout that FAILS leaves none. A fabricated shipping version is
	// refused after the transaction has begun, which is exactly the window
	// where a message written outside it would survive.
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

// waitUntilBlocked returns once the backend at pid is waiting on a lock, or
// once done fires.
//
// pg_stat_activity is the synchronisation point: it reports what PostgreSQL is
// actually doing, so the caller commits at a moment where the interleaving is a
// fact rather than an assumption. It fails the test if the second writer
// neither finishes nor blocks — that would mean the case proved nothing.
//
// The 5ms is a poll interval with a deadline above it, not a guess at how long
// something takes.
func waitUntilBlocked(t *testing.T, pid int, done <-chan error) {
	t.Helper()
	ctx := t.Context()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-done:
			// T2 finished while T1 still held its row. That means hold_inventory
			// did not take the lock, which is the defect this case exists to
			// catch — and without this branch the case would pass anyway,
			// because T2 running AFTER T1 commits is also refused. The
			// interleaving has to be load-bearing or the test is theatre.
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
			t.Fatal("the second writer neither finished nor blocked on a lock; " +
				"the interleaving did not happen and this case proved nothing")
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

// TestThePickupDestinationComesFromTheMethodNotTheForm is the property that
// makes 超商取貨 real rather than a label on an order nobody can deliver.
//
// The submission below carries BOTH destinations, which is what a hand-edited
// form does. The address must not survive: the method says where the parcel
// goes, and a street address on a pickup order is a second answer to a question
// that has one.
func TestThePickupDestinationComesFromTheMethodNotTheForm(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	id := newCart(t, s)
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	addr := &cart.Address{
		// The destination the CALLER claims is deliberately wrong. PlaceOrder
		// re-reads the method and overrides it.
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

// TestAnAddressOrderKeepsNoPickupPoint is the other direction. A customer who
// filled the store fields, switched to 宅配 and submitted must not leave a
// convenience store attached to a parcel going to their house.
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

// TestBothPagesShowWhereAPickupOrderGoes. The destination is useless if only
// the database has it: the customer checks which store they picked, and the
// back office has to put it on the label.
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
	// The label, the store name and the code — a customer checking they picked
	// the right shop needs all three, and a page showing only "萊爾富" names
	// about 1,400 of them.
	for _, want := range []string{"萊爾富", "民生門市", "778899"} {
		if !strings.Contains(view.DeliveryTo, want) {
			t.Errorf("the confirmation does not show %q: %q", want, view.DeliveryTo)
		}
	}
}

// TestTheAddressBookIsScopedToItsOwner. The address id comes off a URL, and the
// scoping is in the query rather than checked after the read — a check
// afterwards is a check somebody eventually forgets, and what leaks is a
// stranger's home address.
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

	// The control: the other account's row exists, so the assertion above is
	// about scoping rather than about an empty table.
	other, err := s.SavedAddresses(ctx, uuid.NullUUID{UUID: theirs, Valid: true})
	if err != nil {
		t.Fatalf("read the other account's addresses: %v", err)
	}
	if len(other) != 1 || other[0].Label != "別人的地址" {
		t.Fatalf("the other account has %d addresses %+v", len(other), other)
	}
}

// TestAGuestHasNoAddressBook. A null owner must not read every address in the
// table, which is what a query with no owner predicate would do.
func TestAGuestHasNoAddressBook(t *testing.T) {
	s := cart.NewStore(pool)
	// A row exists, so an empty result is scoping rather than an empty table.
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

// TestCancellingAnOrderHandsBackItsOpenCheckouts is the database half of the
// finding that cancelling never closed the Stripe session.
//
// Releasing the stock and returning the credit were both wired; the CHECKOUT was
// left payable, so "cancel the order, then finish paying on the tab that is
// still open" put money against goods already back on the shelf. goen had a name
// for that arriving — payment.ErrOrderCancelled — and a comment ending "a human
// refunds it".
//
// What is asserted here is the handover, not the Stripe call: the session ids
// come out of the cancelling transaction, and closing them is the handler's
// post-commit job. The HTTP half is TestCancellingClosesTheCheckoutAtStripe.
//
// The row is opened through open_payment rather than an INSERT, because `store`
// holds no INSERT on payments — a born-succeeded payment row is the forgery that
// revoke prevents, and a fixture reaching past it would be a fixture for a claim
// nobody is testing.
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
	// slices.Equal rather than cmp.Diff: a local in this file is already named
	// cmp, and renaming it to import go-cmp for one string slice would touch code
	// this change has nothing to do with.
	want := []string{"cs_test_still_open"}
	if !slices.Equal(sessions, want) {
		t.Errorf("Cancel() sessions = %v, want %v\n"+
			"  A cancellation that hands back nothing leaves the customer's checkout "+
			"payable for as long as the stock hold lasts.", sessions, want)
	}
}

// TestCancellingAnOrderWithNoCheckoutHandsBackNothing is the other half, and it
// is what stops the test above passing on a query with no predicate at all.
//
// An order can be cancelled before anybody has opened a payment — that is the
// ordinary case, since the pay page is a separate click — and a cancellation that
// reported a session there would send the handler to Stripe with an id that names
// nothing.
func TestCancellingAnOrderWithNoCheckoutHandsBackNothing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "cancel-session-2")

	// A DIFFERENT order with a live session, so the query is asked to tell two
	// orders apart rather than merely to find none. Without it, a predicate
	// missing its order join would still return an empty slice here.
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

// TestCancellingAnOrderPutsTheStockBack holds that a cancellation is a shelf
// movement and not only a status change.
//
// The status change on its own is bookkeeping: the units stay off the shelf
// until the sweeper notices, which is up to HoldTTL later. For the last unit of
// something that is a sale lost to a customer who changed their mind.
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

// TestAFundedOrderCannotBeCancelledByItsCustomer holds the funding half of the
// rule from the side where stock is still held.
//
// Paid is the shop's problem, not a button's: an order somebody has been
// charged for is refunded, on a decision that belongs to the shop. The guard is
// in the UPDATE's own WHERE clause, so a capture landing while the customer
// looks at the page cannot be raced.
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

// TestCancellingTwiceIsRefusedTheSecondTime. The second POST reaches the same
// WHERE clause and matches nothing, which is what makes the button safe to
// double-click and safe to reload.
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

// TestCancellingAnOrderThatIsBeingPickedIsRefused. Once the shop has started,
// stopping it is a conversation rather than a form.
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

// TestCancellingAnOrderThatDoesNotExistIsTheSameRefusal. Order numbers come off
// a guessable counter, so "no such order" and "not yours to cancel" must be one
// answer — telling them apart says which numbers are real.
func TestCancellingAnOrderThatDoesNotExistIsTheSameRefusal(t *testing.T) {
	s := cart.NewStore(pool)
	if _, err := s.Cancel(t.Context(), "GO-990101-999999"); !errors.Is(err, cart.ErrNotCancellable) {
		t.Errorf("an unknown order answered %v, want ErrNotCancellable", err)
	}
}

// TestAFundedOrderWithNoHeldStockIsStillRefused is what proves the funding
// guard in CancelOrderByCustomer rather than the one in release_reservation.
//
// With stock held, a paid order is refused by the release — so the earlier
// funded case passed with the UPDATE's own predicate deleted, and said nothing
// about it. An order whose holds are already consumed reaches the UPDATE with
// an empty loop behind it, and only the WHERE clause is left to refuse.
func TestAFundedOrderWithNoHeldStockIsStillRefused(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-12")

	orderID := heldOrder(t, vid, -time.Hour, true) // funded
	number := numberOf(t, orderID)
	// Consume the hold, the way a dispatch does. The order is now funded with
	// nothing held against it.
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

// TestAStrangerCannotCancelSomebodyElsesOrder holds the cancel endpoint to the
// same access rule as the page it is posted from.
//
// Order numbers come off a per-day counter, so they are guessable: without the
// same access rule the confirmation page has, cancelling a stranger's order is
// one form submission. That it changes nothing is the important half — a 404
// that had already released the stock would be a denial of service with a
// polite status code.
func TestAStrangerCannotCancelSomebodyElsesOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-13")
	orderID := heldOrder(t, vid, -time.Hour, false)
	number := numberOf(t, orderID)
	before := stockOf(t, vid)

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		// Generous: these cases are about the lookup's ANSWER, and a limiter that
		// refused mid-suite would be testing the limiter.
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		// nil: no Stripe key in an integration test, so no session was ever opened
		// and there is nothing for a cancellation to close.
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

	// The control: the browser that placed it does cancel, without which a
	// handler that 404s unconditionally would pass every check above.
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

// TestACancelledOrdersStockComesBackByEveryDoor is the defect the split between
// committed_orders and settled_orders exists to fix.
//
// A cancelled order used to read as COMMITTED, because the view said any
// non-pending status was. release_reservation refuses a committed order's hold
// and the sweeper skips one — so the units behind a cancelled order could not
// come back by any route at all. They were simply gone.
//
// The case that mattered most was a FUNDED order the shop cancels: nothing else
// in the system would ever have released it.
func TestACancelledOrdersStockComesBackByEveryDoor(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid := freshVariant(t, "stockfix-14")

	// A funded order, cancelled the way the back office cancels one — the
	// status moved directly, with no release. This is the state the old
	// definition made permanent.
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

// TestACancelledOrderIsNotAVerifiedPurchase. The same conflation gave the 已購買
// badge to somebody whose order was cancelled — a claim about a product they
// never received, on a page other customers read to decide.
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

	// The control: while the order stands, the claim is true and accepted.
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

// TestAnOffshoreAddressCostsMoreThanATaipeiOne holds the surcharge and its
// relationship to the free-shipping threshold.
//
// The fee was one number for the whole country, so a parcel to 金門 was charged
// a Taipei price and the shop paid the difference out of the margin.
//
// The surcharge survives 免運 on purpose: the threshold is the shop's own offer
// on its own base rate, and the carrier still charges to cross the water.
func TestAnOffshoreAddressCostsMoreThanATaipeiOne(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	version := shipVersionFor(t, "home_delivery")

	// Under the free-over threshold: base fee plus surcharge.
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

	// A FIVE-digit code. Taiwan writes 3+2 and Address.Validate accepts 3 to 6
	// digits, so this is what a customer actually types — and the zone is found
	// from the first three. Without the truncation the surcharge silently
	// vanishes for everybody who fills the field in properly.
	full, err := s.QuoteShipping(ctx, version, 100000, "89052")
	if err != nil {
		t.Fatalf("quote for a five-digit Kinmen code: %v", err)
	}
	if full.Total() != kinmen.Total() {
		t.Errorf("89052 costs %d and 890 costs %d — the prefix is not being taken",
			full.Total(), kinmen.Total())
	}

	// Over the threshold: the base goes to zero and the surcharge does not.
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

// TestAPickupOrderIsNeverInAZone. Its destination is a store, so there is no
// postal code to find a zone from — which is why shipping_version_zones has no
// serviceable flag: the one method that could not serve 離島 is the one that
// can never be matched to it.
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

// TestAnOrderIsChargedTheZoneItShipsTo is the property the whole feature is
// for: the number in the ORDER is the one the address earns, not the one the
// method chooser showed before an address existed.
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

// TestAFreeShippingCouponDoesNotPayForTheCrossing holds a 免運 coupon to the
// same line the free-over threshold is held to.
//
// 免運 is the shop's own offer on its own base rate. A coupon that also ate the
// 離島 surcharge would have the shop paying NT$200 a parcel to honour a NT$0
// discount — which is the same reasoning the free-over threshold follows, and
// the two must not disagree.
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

// TestReorderPutsBackWhatCanStillBeBought holds what a 再買一次 adds and what
// it reports it could not.
//
// A reorder that quietly drops two of five lines is a customer who checks out
// with the wrong basket, so what was SKIPPED is reported alongside what was
// added — and the two reasons are kept apart, because "no longer sold" and
// "sold out" lead somewhere different.
func TestReorderPutsBackWhatCanStillBeBought(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	// Variants of this test's OWN product. Retiring and emptying a seeded one
	// breaks every other test that needs it — which is what happened, under
	// -shuffle, to a test that had passed a dozen times in file order.
	live, retired, empty := threeVariants(t, "reorder-fixture")

	number := orderOfVariants(t, map[uuid.UUID]int32{live: 2, retired: 1, empty: 1})

	// One variant is retired and one is emptied, AFTER the order — which is the
	// whole case: an order from last year has both.
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

	// And the cart holds what was put back, at the quantity that was bought.
	view, err := s.View(ctx, basket)
	if err != nil {
		t.Fatalf("read cart: %v", err)
	}
	if len(view.Lines) != 1 || view.Lines[0].Quantity != 2 {
		t.Errorf("the cart holds %d lines %+v, want one line of 2", len(view.Lines), view.Lines)
	}
}

// TestReorderPricesFromTheCatalogueAndNotTheOrder. A reorder is a new purchase:
// showing last year's price would quote a figure the checkout will not honour.
func TestReorderPricesFromTheCatalogueAndNotTheOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid, _, _ := threeVariants(t, "reorder-price-fixture")
	number := orderOfVariants(t, map[uuid.UUID]int32{vid: 1})

	// The price moves after the order, which is what an old order has.
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

// TestAStrangerCannotFillTheirCartFromSomebodyElsesOrder holds the reorder
// endpoint to the same access rule as the page it is posted from.
//
// Order numbers come off a guessable per-day counter, so without the same
// access rule the page has, a stranger learns what somebody bought by watching
// their own cart fill up.
func TestAStrangerCannotFillTheirCartFromSomebodyElsesOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	own, _, _ := threeVariants(t, "reorder-access-fixture")
	number := orderOfVariants(t, map[uuid.UUID]int32{own: 1})

	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false,
		// Generous: these cases are about the lookup's ANSWER, and a limiter that
		// refused mid-suite would be testing the limiter.
		ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour}),
		// nil: no Stripe key in an integration test, so no session was ever opened
		// and there is nothing for a cancellation to close.
		nil)

	stranger := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/orders/"+number+"/reorder", http.NoBody)
	stranger.SetPathValue("number", number)
	res := httptest.NewRecorder()
	h.ReorderItems(res, stranger)

	if res.Code != http.StatusNotFound {
		t.Errorf("a stranger's reorder answered %d, want 404", res.Code)
	}
	// No cart was opened either: a 404 that had already set a cookie and filled
	// a basket would leak the order's contents by another route.
	if cookie := res.Header().Get("Set-Cookie"); strings.Contains(cookie, "goen_cart") {
		t.Errorf("a refused reorder opened a cart: %q", cookie)
	}

	// The control: the browser that placed it does reorder.
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

// freshVariant creates a product of this test's own with one sellable variant.
//
// Tests that HOLD, retire or empty stock must not share a variant: each one
// takes units the next assumes are there, so the suite passes in file order and
// fails under -shuffle. That was true of this file before these helpers
// existed, and test-integration never shuffled, so nothing said so.
// TestAMethodIsNotOfferedForAParcelItsCarrierRefuses holds the rule that decides
// which delivery methods a customer sees.
//
// Every active method used to be offered to every cart. 超商店到店 refuses a
// parcel over 45cm on its longest side, 105cm across three, or 10kg — so a shop
// selling a 27-inch monitor offered 超商取貨 for it, the customer chose it, the
// order was placed and paid, and the shop found out at the counter with the
// parcel already packed and the customer already waiting.
//
// The test is PER ITEM and never over the cart total, which is the half worth
// locking: more parcels are always possible, so two things that each fit are two
// parcels — but one item that does not fit cannot be split, whatever else is in
// the basket. The "one fits, one does not" case is what tells the two rules
// apart; a cart with a single oversized item passes under either.
func TestAMethodIsNotOfferedForAParcelItsCarrierRefuses(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	// A method with 超商店到店's real ceilings, and the seeded methods beside it.
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
	// A phone-sized box, and a 27-inch monitor.
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

func freshVariant(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	a, _, _ := variantsOf(t, slug, 1)
	return a
}

// threeVariants is freshVariant with three, for a test that needs them to
// differ from one another.
func threeVariants(t *testing.T, slug string) (a, b, c uuid.UUID) {
	t.Helper()
	return variantsOf(t, slug, 3)
}

// variantsOf creates one product with n sellable variants.
func variantsOf(t *testing.T, name string, n int) (a, b, c uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	// A unique slug per CALL, not per test: a test that needs two independent
	// products calls this twice, and products_slug_key would refuse the second.
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
		// Stock arrives through the one door, as it does everywhere else.
		if _, err := pool.Exec(ctx,
			`SELECT record_inventory_movement($1, 10, 'adjustment', $2, NULL, NULL, NULL)`,
			vid, "fixture:"+vid.String()); err != nil {
			t.Fatalf("stock the variant: %v", err)
		}
		ids[i] = vid
	}
	// Published only now: products_active_has_variant is DEFERRED and fires
	// from both sides, so a product cannot be active before it has one.
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active', published_at = now() WHERE id = $1`,
		productID); err != nil {
		t.Fatalf("publish %s: %v", slug, err)
	}
	return ids[0], ids[1], ids[2]
}

// TestTheAttemptSweepKeepsRecentKeys is the retention rule for checkout's
// idempotency ledger.
//
// One row per attempt goen has ever seen, and nothing deleted them. The half
// that matters is the RECENT key: deleting a row frees it to be replayed, so a
// sweep that reached into this week would turn a double-click into two orders.
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

// TestCancellingReturnsSpentStoreCredit is money the customer used to lose.
//
// store_credit_entries.reverses_id, its unique partial index and the entire
// reversal branch of store_credit_guard were written the day the ledger was, and
// nothing ever called them. Cancelling an order released its stock and stopped:
// the goods went back on the shelf and the credit stayed spent, so a customer who
// part-paid with credit and then changed their mind was simply out that money.
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
	// And it came back as a REVERSAL of the spend, not as a fresh grant: the
	// reversal is what ties the refund to the entry it undoes, so a second
	// cancellation cannot pay twice.
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

// TestReversingCancelledCreditIsIdempotent proves a retried cancellation does not
// pay twice.
//
// The idempotency key is derived from the entry being reversed, and the unique
// partial index on reverses_id refuses a second one — so this is the schema's
// guarantee rather than the handler's care.
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

// TestAShippedOrdersCreditIsNotReversed proves the rule the schema already had.
//
// A funded or shipped order is compensated with a refund or a new positive entry,
// never by undoing the spend — an order that is going to ship was paid for, and
// reversing its credit would mean the shop shipped goods nobody paid for.
func TestAShippedOrdersCreditIsNotReversed(t *testing.T) {
	ctx := t.Context()
	userID, accountID := creditedAccount(t, 50000)
	number := pendingOrderSpendingCredit(t, userID, 20000)

	// Through the real transitions: orders_check_transition refuses
	// pending → shipped, because an order is picked before it leaves. The order is
	// wholly credit-funded, which is what lets it leave pending at all.
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

// creditedAccount is creditedCustomer plus the account id, which a balance
// assertion needs.
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

// pendingOrderSpendingCredit places an order for userID that spends `cents` of
// their credit and leaves it pending and unpaid — the state a cancellation acts on.
//
// Written by hand rather than through PlaceOrder because what matters here is the
// LEDGER's shape, not the checkout's: one spend attributed to one pending order.
func pendingOrderSpendingCredit(t *testing.T, userID uuid.UUID, cents int64) string {
	t.Helper()
	ctx := t.Context()

	// One transaction: orders_has_lines is DEFERRED, so an order and its lines
	// have to commit together — an order with no lines is refused at commit, not
	// at insert.
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
	// order_private_data too: orders_has_delivery_details is deferred the same way.
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

// TestAnOrderSaysWhyItWasDiscounted is a defect a customer reads and cannot act
// on.
//
// The order page showed a subtotal, a shipping fee and a total, and the discount
// was absent from all three: subtotal plus shipping did not equal the total, and
// nothing accounted for the difference. Somebody reading their own receipt could
// not tell whether they had been overcharged.
//
// The reason is JOINED rather than snapshotted on the order. There was a
// discount_code column declared with the table and never written, and filling it
// would have been a second copy of a recoverable fact — coupons.code is never
// updated, and the FK is ON DELETE RESTRICT.
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

// TestAnOrderWithNoCouponHasNoReason proves the empty case reads as absent rather
// than as a stray separator.
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
	// holds orders.discount_cents and the row to each other, because they are one
	// fact.
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

// TestAGuestCanFindTheirOwnOrderWithTheEmail is the gap a customer actually hits.
//
// The order page is shown to the browser that placed the order or to the account
// that owns it. A guest who clears their cookies, or opens the confirmation email
// on a different device, is neither — and that was the end of it: they had a number,
// an address, and no way to see their own order.
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

	// Case and surrounding space are the two things somebody retyping from an
	// email gets wrong, and neither is a reason to refuse them.
	if ok, err := s.FindOrder(ctx, "  "+strings.ToLower(number)+" ", strings.ToUpper(addr)); err != nil {
		t.Fatalf("FindOrder with odd casing: %v", err)
	} else if !ok {
		t.Error("the lookup refused its own order over case or whitespace")
	}
}

// TestTheWrongEmailFindsNothing is the half of the credential that is secret.
//
// Order numbers come off a per-day counter and are guessable — which is why
// reaching the page by number alone is refused in the first place. If the address
// did not have to match, this endpoint would hand out delivery addresses to
// anybody who can count.
func TestTheWrongEmailFindsNothing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	number := orderOfVariants(t, map[uuid.UUID]int32{freshVariant(t, "find-wrong"): 1})

	for _, addr := range []string{
		"somebody-else@example.com",
		"",
		// A near miss on the real address, so the test is not passing on length.
		"x" + uuid.NewString() + "@example.com",
	} {
		if ok, err := s.FindOrder(ctx, number, addr); err != nil {
			t.Fatalf("FindOrder(%q): %v", addr, err)
		} else if ok {
			t.Errorf("the lookup accepted %q for somebody else's order", addr)
		}
	}
}

// TestAnErasedOrderCannotBeFound proves erasure reaches this door too.
//
// erase_user blanks order_private_data and the order survives as a financial
// record. A lookup that still matched on the blanked address would be a way to
// reach an order the shop has promised to stop knowing anything about — and it
// would be a NEW door, opened after the erasure was written.
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

// TestAnOrderLineIsSnapshottedInTheBuyersLanguage locks a property that currently
// holds by construction, which is exactly the kind that regresses silently.
//
// order_lines.product_name is a SNAPSHOT: an order is a record of what was agreed,
// and a later catalogue change must not rewrite it. That is also why it cannot be
// localized at read time — a receipt already sent in one language must not start
// disagreeing with the copy in somebody's mailbox, which is the same rule
// orders_freeze_money holds orders.locale to.
//
// So the language has to be decided at PLACEMENT, and it is: the lines are copied
// from the cart read, which localizes. If some future change reads the catalogue
// directly here instead, this goes red.
func TestAnOrderLineIsSnapshottedInTheBuyersLanguage(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	// Its OWN product, created here rather than borrowed from the seed. The first
	// version picked the seed's koto-over-ear and placed two orders against it,
	// which took its stock and broke two other tests — trap #22 in CLAUDE.md, and
	// the shuffle is what surfaced it.
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

// placeOrderInLocale puts one unit of vid through a cart and places the order, with
// the buying context's locale carried all the way through.
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

// translatedVariant makes a product whose Chinese and English names differ, with a
// sellable variant, and returns the variant id and both names.
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
	// Stock arrives through the ledger, the same door the application uses — there
	// is no other way to write stock_quantity.
	if _, err := pool.Exec(ctx,
		`SELECT record_inventory_movement($1, 5, 'receipt', $2, NULL, NULL)`,
		variantID, "snapshot-stock-"+suffix); err != nil {
		t.Fatalf("stock the variant: %v", err)
	}
	// Published only once it has something to sell: products_active_has_variant is
	// deferred and fires from both sides.
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active' WHERE id = $1`, productID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return variantID, zhName, enName
}

// TestTheCheckoutOffersDeliveryInTheVisitorsLanguage was the last shop-typed chrome
// on the buying mainline.
//
// The method chooser is a set of LINKS — the choice lives in the URL, which is what
// makes it work with scripting off — and its labels come from
// shipping_method_versions.name. So an English customer picking a delivery method
// read 宅配到府 and 超商取貨 at the moment of paying, on a page whose every other word
// had been translated.
func TestTheCheckoutOffersDeliveryInTheVisitorsLanguage(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	// An empty cart: what a method is OFFERED for now depends on what is in the
	// basket, because a carrier that refuses a 27-inch monitor is not a choice
	// for a basket with one in it.
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

// placedCookie issues a REAL access token for an order and returns the cookie a
// browser that placed it would carry.
//
// The tests used to write the order NUMBER into this cookie, which is precisely the
// hole a third-party review found: the number was the proof, numbers are sequential,
// and anybody could mint that cookie with curl. Those tests passed and were evidence
// of the defect rather than of the rule — so the fixture now goes through the same
// grant the checkout writes, and a hand-made cookie no longer works anywhere.
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

// TestAForgedPlacedCookieReachesNothing is the Critical a third-party review found.
//
// The cookie used to hold the ORDER NUMBER, and the number was the proof. Numbers
// come off a per-day counter — GO-260803-000001, then 000002 — so an attacker set
// the cookie by hand, incremented, and read a stranger's email, delivery address and
// items, then cancelled the order, started a payment or opened a return.
//
// __Host-, Secure, HttpOnly and SameSite govern how a BROWSER treats a cookie. None
// of them says the value came from this server, and curl does not have to care. The
// tests that existed asserted the old behaviour and were evidence OF the defect.
func TestAForgedPlacedCookieReachesNothing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil)
	number := placeUnpaidOrderFor(t, s, "forged@example.com")

	// The VICTIM's own browser holds a real grant before the attack starts, and
	// that is what makes this a test rather than a coincidence.
	//
	// Without it the order has no grant at all, so the access query's EXISTS is
	// false whatever the digest comparison does — and the first version of this
	// test duly stayed GREEN with `g.digest = ANY(...)` replaced by `OR true`.
	// It refused the forgery for the wrong reason: not "your token is not for
	// this order" but "nobody has a token for this order". With the victim's
	// grant present, the digest comparison is the only thing left that can
	// refuse.
	victim := placedCookie(t, s, number)

	// Every shape an attacker would try: the number itself, a neighbouring number,
	// and the number dressed up as a token.
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

	// The control: a REAL token does reach it, or a handler that 404s
	// unconditionally would pass everything above.
	held := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	held.SetPathValue("number", number)
	held.AddCookie(victim)
	ok := httptest.NewRecorder()
	h.OrderPage(ok, held)
	if ok.Code != http.StatusOK {
		t.Fatalf("the browser holding a real token got %d, want 200", ok.Code)
	}
}

// TestATokenReachesOnlyItsOwnOrder holds the other half: a token is not a skeleton
// key. A browser that placed one order must not reach the next one by number.
func TestATokenReachesOnlyItsOwnOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil)

	mine := placeUnpaidOrderFor(t, s, "mine@example.com")
	theirs := placeUnpaidOrderFor(t, s, "theirs@example.com")

	// Their browser holds a token for their own order. Without this the target has
	// no grant at all and the refusal proves nothing about WHOSE token was
	// presented — the same false green the forged-cookie test above fell into.
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

// nextOrderNumber increments the counter half of an order number, which is what an
// attacker walking the sequence would do.
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

// testLimiter is generous on purpose: these cases are about an ANSWER, and a limiter
// that refused mid-suite would be testing the limiter.
func testLimiter() *ratelimit.Limiter {
	return ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour})
}

// placeUnpaidOrderFor places one order for an address, through the store's own
// checkout — so the order, its hold and its access grant are written the way the
// site writes them.
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
