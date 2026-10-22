//go:build integration

package checkout_test

import (
	"context"
	"encoding/base64"
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

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/user"
)

func TestPlaceOrderIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, variantOf(t, "pixelight-9-pro"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}

	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}

	addr := &delivery.Details{
		Email: "idem@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	key := checkoutAttemptKey("idem-key-test-1")
	shown := checkoutQuote(t, s, id, uuid.NullUUID{}, shipID, addr, "")

	first, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, "", shown, key)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	second, err := s.PlaceOrder(ctx, id, uuid.NullUUID{}, shipID, addr, nil, "", shown, key)
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

func TestCheckoutKeyCannotBeReusedByAnotherCart(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	shippingID := shipVersionFor(t, "home_delivery")
	key := checkoutAttemptKey("cross-cart-idempotency-key")

	firstCart := newCart(t, s)
	if err := s.Carts().Add(ctx, firstCart, freshVariant(t, "key-owner"), 1); err != nil {
		t.Fatalf("add first cart: %v", err)
	}
	firstAddress := &delivery.Details{
		Email: "key-owner@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	firstQuote := checkoutQuote(
		t, s, firstCart, uuid.NullUUID{}, shippingID, firstAddress, "",
	)
	if _, err := s.PlaceOrder(
		ctx, firstCart, uuid.NullUUID{}, shippingID, firstAddress, nil, "", firstQuote, key,
	); err != nil {
		t.Fatalf("place first cart: %v", err)
	}

	secondCart := newCart(t, s)
	if err := s.Carts().Add(ctx, secondCart, freshVariant(t, "key-collision"), 1); err != nil {
		t.Fatalf("add second cart: %v", err)
	}
	secondAddress := &delivery.Details{
		Email: "key-collision@example.com", RecipientName: "李大華", Phone: "0987654321",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松仁路 2 號",
	}
	secondQuote := checkoutQuote(
		t, s, secondCart, uuid.NullUUID{}, shippingID, secondAddress, "",
	)
	if _, err := s.PlaceOrder(
		ctx, secondCart, uuid.NullUUID{}, shippingID, secondAddress, nil, "", secondQuote, key,
	); err == nil {
		t.Fatal("another cart reused an owned checkout key")
	}

	var attempts, secondOrders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_private_data WHERE email = $1`, secondAddress.Email).
		Scan(&secondOrders); err != nil {
		t.Fatalf("count second cart orders: %v", err)
	}
	if attempts != 1 || secondOrders != 0 {
		t.Fatalf("key collision left %d attempt(s), %d second-cart order(s)", attempts, secondOrders)
	}
	view, err := s.Carts().View(ctx, secondCart)
	if err != nil || len(view.Lines) != 1 {
		t.Fatalf("key collision changed second cart: lines=%d err=%v", len(view.Lines), err)
	}
}

func TestCheckoutReplacesMalformedAttemptIdentityBeforeWriting(t *testing.T) {
	ctx := t.Context()
	canonical := checkoutAttemptKey("noncanonical-attempt")
	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "missing"},
		{name: "whitespace", value: "   "},
		{name: "overlong", value: strings.Repeat("A", 4096)},
		{name: "noncanonical", value: canonical + "="},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := checkout.NewStore(pool)
			token := bearer.New()
			cartID, err := s.Carts().Create(ctx, token, uuid.NullUUID{})
			if err != nil {
				t.Fatalf("create cart: %v", err)
			}
			if err := s.Carts().Add(ctx, cartID, freshVariant(t, "bad-attempt-"+tt.name), 1); err != nil {
				t.Fatalf("add: %v", err)
			}
			shippingID := shipVersionFor(t, "home_delivery")
			addr := &delivery.Details{
				Email:         "attempt-" + tt.name + "@example.com",
				RecipientName: "王小明", Phone: "0912345678", PostalCode: "110",
				City: "台北市", District: "信義區", Street: "松高路 1 號",
			}
			form := url.Values{
				"email": {addr.Email}, "name": {addr.RecipientName}, "phone": {addr.Phone},
				"postal_code": {addr.PostalCode}, "city": {addr.City},
				"district": {addr.District}, "street": {addr.Street},
				"shipping":       {shippingID.String()},
				"checkout_quote": {checkoutQuote(t, s, cartID, uuid.NullUUID{}, shippingID, addr, "").String()},
				"idempotency":    {tt.value},
			}
			req := httptest.NewRequestWithContext(
				ctx, http.MethodPost, "/checkout", strings.NewReader(form.Encode()),
			)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			//nolint:gosec // G124: the browser's own cart cookie
			req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
			h := newHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), nil)

			res := httptest.NewRecorder()
			h.PlaceOrder(res, req)

			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("response = %d, want 422; body=%s", res.Code, res.Body.String())
			}
			fresh, found := hiddenInputValue(res.Body.String(), "idempotency")
			decoded, decodeErr := base64.RawURLEncoding.DecodeString(fresh)
			if !found || decodeErr != nil || len(decoded) != 16 ||
				base64.RawURLEncoding.EncodeToString(decoded) != fresh || fresh == tt.value {
				t.Fatalf("replacement attempt ID = %q (found=%v, decode=%v), want fresh canonical ID",
					fresh, found, decodeErr)
			}

			var orders, lines int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM order_private_data WHERE email = $1`, addr.Email).Scan(&orders); err != nil {
				t.Fatalf("count orders: %v", err)
			}
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM cart_items WHERE cart_id = $1`, cartID).Scan(&lines); err != nil {
				t.Fatalf("count cart lines: %v", err)
			}
			if orders != 0 || lines != 1 {
				t.Fatalf("malformed attempt wrote %d order(s) and left %d cart line(s)", orders, lines)
			}
			// Other tests share this database; the only relevant durable assertion is
			// that neither the refused text nor its fresh replacement was claimed.
			var refusedAttempts int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM checkout_attempts
				WHERE idempotency_key = $1 OR idempotency_key = $2`, tt.value, fresh).
				Scan(&refusedAttempts); err != nil {
				t.Fatalf("count refused attempts: %v", err)
			}
			if refusedAttempts != 0 {
				t.Fatalf("malformed attempt or replacement wrote %d checkout attempt(s)", refusedAttempts)
			}
		})
	}
}

func TestCheckoutHTTPReplayFindsTheSameOrderAfterTheCartIsEmpty(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	token := bearer.New()
	cartID, err := s.Carts().Create(ctx, token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Carts().Add(ctx, cartID, freshVariant(t, "http-replay"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	shippingID := shipVersionFor(t, "home_delivery")
	addr := &delivery.Details{
		Email: "replay@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	key := checkoutAttemptKey("http-replay-" + uuid.NewString())
	form := url.Values{
		"email": {addr.Email}, "name": {addr.RecipientName}, "phone": {addr.Phone},
		"postal_code": {addr.PostalCode}, "city": {addr.City}, "district": {addr.District},
		"street":         {addr.Street},
		"shipping":       {shippingID.String()},
		"checkout_quote": {checkoutQuote(t, s, cartID, uuid.NullUUID{}, shippingID, addr, "").String()},
		"idempotency":    {key},
	}
	request := func() *http.Request {
		req := httptest.NewRequestWithContext(
			ctx, http.MethodPost, "/checkout", strings.NewReader(form.Encode()),
		)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
		return req
	}
	h := newHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), nil)

	first := httptest.NewRecorder()
	h.PlaceOrder(first, request())
	second := httptest.NewRecorder()
	h.PlaceOrder(second, request())

	if first.Code != http.StatusSeeOther || second.Code != http.StatusSeeOther {
		t.Fatalf("responses = %d, %d; want two 303s", first.Code, second.Code)
	}
	if first.Header().Get("Location") != second.Header().Get("Location") {
		t.Fatalf("replay locations differ: %q != %q",
			first.Header().Get("Location"), second.Header().Get("Location"))
	}
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("replay wrote %d checkout attempts, want one", attempts)
	}
}

// TestADoubleClickedCheckoutPlacesOneOrder covers two requests IN FLIGHT AT
// ONCE, which the advisory lock on the key serialises. T1's transaction is held
// OPEN while T2 runs, rather than raced from two goroutines that never overlap.
func TestADoubleClickedCheckoutPlacesOneOrder(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	key := checkoutAttemptKey("double-" + uuid.NewString())
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
	if err := s.Carts().Add(ctx, id, vid, 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	placed := make(chan error, 1)
	go func() {
		_, placeErr := placeOrder(t, s, ctx, id, uuid.NullUUID{},
			shipVersionFor(t, "home_delivery"), &delivery.Details{
				Email: "double@example.com", RecipientName: "王小明", Phone: "0912345678",
				PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			}, "", key)
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

// TestADoubleClickThatLosesTheRaceLandsOnTheOrder covers the clicks of one
// double click that miss the order on their first look and then read the
// winner's work: they asked for the attempt before the winner committed, and
// read the emptied cart or the spent credit after (#2245). Each must be answered
// as a later retry is, with the winner's order, and nothing may be placed, held
// or spent twice.
func TestADoubleClickThatLosesTheRaceLandsOnTheOrder(t *testing.T) {
	for _, tt := range []struct {
		name   string
		clicks int
		credit bool
		// gate is where the losers first read the winner's work: queued behind
		// the winner on it, they read it only once the winner has committed.
		gate string
	}{
		{name: "emptied cart", clicks: 2, gate: "cart_items"},
		{name: "emptied cart, eight clicks", clicks: 8, gate: "cart_items"},
		{name: "spent credit", clicks: 2, credit: true, gate: "store_credit_entries"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raceTheSameAttempt(t, tt.clicks, tt.credit, tt.gate)
		})
	}
}

func raceTheSameAttempt(t *testing.T, clicks int, credit bool, gate string) {
	t.Helper()
	ctx := t.Context()
	base := checkout.NewStore(pool)
	vid := freshVariant(t, "double-click")
	var owner uuid.NullUUID
	var who *user.User
	if credit {
		id := creditedCustomer(t, 5000)
		owner = uuid.NullUUID{UUID: id, Valid: true}
		who = &user.User{ID: id.String(), Email: "double-click@example.com", Role: user.RoleCustomer}
	}
	token := bearer.New()
	cartID, err := base.Carts().Create(ctx, token, owner)
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if addErr := base.Carts().Add(ctx, cartID, vid, 1); addErr != nil {
		t.Fatalf("add: %v", addErr)
	}
	shippingID := shipVersionFor(t, "home_delivery")
	addr := &delivery.Details{
		Email: "double-click-" + uuid.NewString()[:8] + "@example.com", RecipientName: "王小明",
		Phone: "0912345678", PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	key := checkoutAttemptKey("double-click-" + uuid.NewString())
	form := url.Values{
		"email": {addr.Email}, "name": {addr.RecipientName}, "phone": {addr.Phone},
		"postal_code": {addr.PostalCode}, "city": {addr.City}, "district": {addr.District},
		"street":         {addr.Street},
		"shipping":       {shippingID.String()},
		"checkout_quote": {checkoutQuote(t, base, cartID, owner, shippingID, addr, "").String()},
		"idempotency":    {key},
	}
	stockBefore := stockOf(t, vid)

	type click struct {
		app  string
		res  *httptest.ResponseRecorder
		done chan error
	}
	// Each click has a connection pool of its own, named so the test can see
	// which lock it waits on.
	submit := func(name string) *click {
		c := &click{app: name + "-" + uuid.NewString(), res: httptest.NewRecorder(), done: make(chan error, 1)}
		p := applicationPool(t, c.app)
		h := newHandler(checkout.NewStore(p), access.NewStore(p, false), slog.New(slog.DiscardHandler), nil)
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
		if who != nil {
			req = req.WithContext(user.NewContext(req.Context(), *who))
		}
		go func() {
			h.PlaceOrder(c.res, req)
			c.done <- nil
		}()
		return c
	}

	// The winner stops just before it records the attempt and empties the cart:
	// an uncommitted row under the same key makes its INSERT wait.
	hold, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hold: %v", err)
	}
	defer func() { _ = hold.Rollback(context.WithoutCancel(ctx)) }()
	if _, holdErr := hold.Exec(ctx,
		`INSERT INTO checkout_attempts (idempotency_key) VALUES ($1)`, key); holdErr != nil {
		t.Fatalf("hold the attempt: %v", holdErr)
	}
	winner := submit("double-click-winner")
	waitForApplicationLock(t, winner.app, winner.done)

	// The gate queues behind the winner's lock on the table and the losers queue
	// behind the gate, so they read it only after the winner commits.
	gateApp := "double-click-gate-" + uuid.NewString()
	gatePool := applicationPool(t, gateApp)
	gated := make(chan error, 1)
	go func() {
		tx, beginErr := gatePool.Begin(ctx)
		if beginErr != nil {
			gated <- beginErr
			return
		}
		_, lockErr := tx.Exec(ctx, "LOCK TABLE "+pgx.Identifier{gate}.Sanitize()+" IN ACCESS EXCLUSIVE MODE")
		gated <- errors.Join(lockErr, tx.Rollback(ctx))
	}()
	waitForApplicationLock(t, gateApp, gated)
	losers := make([]*click, clicks-1)
	for i := range losers {
		losers[i] = submit("double-click-loser")
		waitForApplicationLock(t, losers[i].app, losers[i].done)
	}

	if err = hold.Rollback(ctx); err != nil {
		t.Fatalf("release the winner: %v", err)
	}
	<-winner.done
	if err = <-gated; err != nil {
		t.Fatalf("gate: %v", err)
	}
	for _, c := range losers {
		<-c.done
	}

	placed := winner.res.Header().Get("Location")
	if winner.res.Code != http.StatusSeeOther || !strings.HasSuffix(placed, "/pay") {
		t.Fatalf("winner answered %d %q, want 303 to its order's payment; body=%s",
			winner.res.Code, placed, winner.res.Body.String())
	}
	for i, c := range losers {
		if c.res.Code != http.StatusSeeOther || c.res.Header().Get("Location") != placed {
			t.Errorf("loser %d answered %d %q, want 303 %q, the order the winner placed",
				i+1, c.res.Code, c.res.Header().Get("Location"), placed)
		}
	}

	var attempts, orders, held int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, key).Scan(&attempts); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(DISTINCT order_id) FROM order_lines WHERE variant_id = $1`, vid).Scan(&orders); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(quantity), 0) FROM inventory_reservations
		WHERE variant_id = $1 AND state = 'held'`, vid).Scan(&held); err != nil {
		t.Fatalf("sum held units: %v", err)
	}
	if attempts != 1 || orders != 1 || held != 1 || stockBefore-stockOf(t, vid) != 1 {
		t.Errorf("%d clicks left %d attempt(s), %d order(s), %d held unit(s) and took %d from stock; want 1 of each",
			clicks, attempts, orders, held, stockBefore-stockOf(t, vid))
	}
	if credit {
		var spends int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM store_credit_entries e
			JOIN store_credit_accounts a ON a.id = e.account_id
			WHERE a.user_id = $1 AND e.amount_cents < 0`, owner.UUID).Scan(&spends); err != nil {
			t.Fatalf("count credit spends: %v", err)
		}
		if spends != 1 {
			t.Errorf("%d clicks spent credit %d time(s), want once", clicks, spends)
		}
	}
}
