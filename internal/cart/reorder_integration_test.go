//go:build integration

package cart_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/ratelimit"
)

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

// TestCheckoutAndReorderSerializeTheCartAggregate fixes the lost-update window
// between checkout's cart snapshot and ClearCart. Reorder must wait for the
// checkout transaction, then add to the now-empty cart; otherwise it can report
// success only for ClearCart to erase items that were never copied to the order.
func TestCheckoutAndReorderSerializeTheCartAggregate(t *testing.T) {
	ctx := t.Context()
	baseStore := cart.NewStore(pool)
	checkoutVariant, reorderedVariant, _ := threeVariants(t, "cart-aggregate-lock")
	sourceOrder := orderOfVariants(t, map[uuid.UUID]int32{reorderedVariant: 2})
	basket := newCart(t, baseStore)
	if err := baseStore.Add(ctx, basket, checkoutVariant, 1); err != nil {
		t.Fatalf("add checkout line: %v", err)
	}

	// Hold the checkout's first stock root. The fixed checkout has already
	// locked the cart aggregate before it reaches this wait.
	variantTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin variant blocker: %v", err)
	}
	defer func() { _ = variantTx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := variantTx.Exec(ctx,
		`SELECT 1 FROM product_variants WHERE id = $1 FOR UPDATE`, checkoutVariant); err != nil {
		t.Fatalf("lock checkout variant: %v", err)
	}

	checkoutPool := applicationPool(t, "checkout-cart-aggregate")
	reorderPool := applicationPool(t, "reorder-cart-aggregate")
	shippingVersionID := shipVersionFor(t, "home_delivery")
	checkoutStore := cart.NewStore(checkoutPool)
	checkoutAddress := &delivery.Details{
		Email: "aggregate@example.com", RecipientName: "購物車鎖", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shown := checkoutQuote(
		t, checkoutStore, basket, uuid.NullUUID{}, shippingVersionID, checkoutAddress, "",
	)
	checkoutDone := make(chan error, 1)
	checkoutNumber := make(chan string, 1)
	go func() {
		number, placeErr := checkoutStore.PlaceOrder(
			ctx,
			basket,
			uuid.NullUUID{},
			shippingVersionID,
			checkoutAddress, nil, "", shown,
			checkoutAttemptKey("checkout-cart-aggregate-"+uuid.NewString()),
		)
		checkoutNumber <- number
		checkoutDone <- placeErr
	}()
	waitForApplicationLock(t, "checkout-cart-aggregate", checkoutDone)

	reorderDone := make(chan error, 1)
	reorderResult := make(chan cart.Reorder, 1)
	go func() {
		result, reorderErr := cart.NewStore(reorderPool).Reorder(ctx, basket, sourceOrder)
		reorderResult <- result
		reorderDone <- reorderErr
	}()
	waitForApplicationLock(t, "reorder-cart-aggregate", reorderDone)

	if err := variantTx.Commit(ctx); err != nil {
		t.Fatalf("release checkout variant: %v", err)
	}
	if err := <-checkoutDone; err != nil {
		t.Fatalf("checkout after variant release: %v", err)
	}
	placedOrder := <-checkoutNumber
	if err := <-reorderDone; err != nil {
		t.Fatalf("reorder after checkout: %v", err)
	}
	if result := <-reorderResult; result.Added != 1 || len(result.Skipped) != 0 {
		t.Fatalf("reorder result = %+v, want one added line", result)
	}

	var checkoutLines, reorderedInOrder int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE ol.variant_id = $2),
		       count(*) FILTER (WHERE ol.variant_id = $3)
		FROM order_lines ol
		JOIN orders o ON o.id = ol.order_id
		WHERE o.order_number = $1`, placedOrder, checkoutVariant, reorderedVariant).
		Scan(&checkoutLines, &reorderedInOrder); err != nil {
		t.Fatalf("read placed order lines: %v", err)
	}
	if checkoutLines != 1 || reorderedInOrder != 0 {
		t.Errorf("placed order has checkout/reordered lines %d/%d, want 1/0",
			checkoutLines, reorderedInOrder)
	}

	var checkoutInCart, reorderedInCart int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE variant_id = $2),
		       count(*) FILTER (WHERE variant_id = $3)
		FROM cart_items WHERE cart_id = $1`, basket, checkoutVariant, reorderedVariant).
		Scan(&checkoutInCart, &reorderedInCart); err != nil {
		t.Fatalf("read cart after checkout and reorder: %v", err)
	}
	if checkoutInCart != 0 || reorderedInCart != 1 {
		t.Errorf("cart has checkout/reordered lines %d/%d, want 0/1",
			checkoutInCart, reorderedInCart)
	}
}

// TestInverseReordersShareOneCartLock makes the former A→B/B→A deadlock
// deterministic. The first reorder pauses after touching A; the second must be
// waiting on the cart root, not touching B and completing half of a lock cycle.
func TestInverseReordersShareOneCartLock(t *testing.T) {
	ctx := t.Context()
	baseStore := cart.NewStore(pool)
	variantA, variantB, _ := threeVariants(t, "inverse-reorder-lock")
	orderAB := orderOfVariants(t, map[uuid.UUID]int32{variantA: 1, variantB: 1})
	orderBA := orderOfVariants(t, map[uuid.UUID]int32{variantA: 1, variantB: 1})
	setPositions := func(t *testing.T, number string, first, second uuid.UUID) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE order_lines SET position = position + 100
			WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number); err != nil {
			t.Fatalf("move source order positions aside: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE order_lines
			SET position = CASE variant_id WHEN $2 THEN 0 WHEN $3 THEN 1 ELSE position END
			WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`,
			number, first, second); err != nil {
			t.Fatalf("set source order positions: %v", err)
		}
	}
	setPositions(t, orderAB, variantA, variantB)
	setPositions(t, orderBA, variantB, variantA)
	basket := newCart(t, baseStore)

	const barrierKey = int64(7_654_321_987_654_321)
	barrier, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin reorder barrier: %v", beginErr)
	}
	defer func() { _ = barrier.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := barrier.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); err != nil {
		t.Fatalf("take reorder barrier: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_pause_inverse_reorder_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_pause_inverse_reorder_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.cart_id = '%s'::uuid AND NEW.variant_id = '%s'::uuid THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON cart_items
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, basket, variantA, barrierKey, triggerName, functionName)); err != nil {
		t.Fatalf("install inverse-reorder barrier: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON cart_items; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName)); err != nil {
			t.Errorf("remove inverse-reorder barrier: %v", err)
		}
	})

	firstPool := applicationPool(t, "inverse-reorder-first")
	secondPool := applicationPool(t, "inverse-reorder-second")
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, reorderErr := cart.NewStore(firstPool).Reorder(ctx, basket, orderAB)
		firstDone <- reorderErr
	}()
	waitForApplicationLock(t, "inverse-reorder-first", firstDone)
	go func() {
		_, reorderErr := cart.NewStore(secondPool).Reorder(ctx, basket, orderBA)
		secondDone <- reorderErr
	}()
	waitForApplicationLock(t, "inverse-reorder-second", secondDone)

	if err := barrier.Commit(ctx); err != nil {
		t.Fatalf("release reorder barrier: %v", err)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("A→B reorder: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("B→A reorder: %v", err)
	}

	quantities := make(map[uuid.UUID]int32, 2)
	rows, err := pool.Query(ctx,
		`SELECT variant_id, quantity FROM cart_items WHERE cart_id = $1`, basket)
	if err != nil {
		t.Fatalf("read inverse-reorder cart: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var variantID uuid.UUID
		var quantity int32
		if err := rows.Scan(&variantID, &quantity); err != nil {
			t.Fatalf("scan inverse-reorder cart: %v", err)
		}
		quantities[variantID] = quantity
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk inverse-reorder cart: %v", err)
	}
	if quantities[variantA] != 2 || quantities[variantB] != 2 {
		t.Errorf("inverse reorders left A/B quantities %d/%d, want 2/2",
			quantities[variantA], quantities[variantB])
	}
}

// TestReorderRollsBackEveryAddWhenOneWriteFails proves a reorder is one cart
// change, not a sequence that can strand its prefix. The trigger is scoped to
// this test's cart and the order's second variant, so the first insert has
// already succeeded in the transaction when the forced failure occurs.
func TestReorderRollsBackEveryAddWhenOneWriteFails(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	first, second, _ := threeVariants(t, "atomic-reorder")
	number := orderOfVariants(t, map[uuid.UUID]int32{first: 1, second: 1})
	basket := newCart(t, s)

	var failVariant uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT ol.variant_id
		FROM order_lines ol JOIN orders o ON o.id = ol.order_id
		WHERE o.order_number = $1
		ORDER BY ol.position, ol.id
		OFFSET 1 LIMIT 1`, number).Scan(&failVariant); err != nil {
		t.Fatalf("read the second reorder variant: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_fail_reorder_add_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_fail_reorder_add_" + suffix}.Sanitize()
	constraintName := "test_reorder_second_add_" + suffix
	createTrigger := fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.cart_id = '%s'::uuid AND NEW.variant_id = '%s'::uuid THEN
				RAISE EXCEPTION USING MESSAGE = 'forced second reorder add failure',
					ERRCODE = 'check_violation', CONSTRAINT = '%s';
			END IF;
			RETURN NEW;
		END;
		$body$;
		CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON cart_items
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, basket, failVariant, constraintName, triggerName, functionName)
	if _, err := pool.Exec(ctx, createTrigger); err != nil {
		t.Fatalf("install scoped cart failure: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		dropTrigger := fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON cart_items; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName)
		if _, err := pool.Exec(cleanupCtx, dropTrigger); err != nil {
			t.Errorf("remove scoped cart failure: %v", err)
		}
	})

	result, err := s.Reorder(ctx, basket, number)
	if err == nil {
		t.Fatal("reorder succeeded despite the forced second write failure")
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != constraintName {
		t.Fatalf("reorder error = %v, want constraint %q", err, constraintName)
	}
	if result.Added != 0 || len(result.Skipped) != 0 {
		t.Errorf("failed reorder returned a partial result: %+v", result)
	}

	var lines int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM cart_items WHERE cart_id = $1`, basket).Scan(&lines); err != nil {
		t.Fatalf("count cart lines after failed reorder: %v", err)
	}
	if lines != 0 {
		t.Errorf("failed reorder left %d cart lines; want the first insert rolled back", lines)
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

func TestAStrangerCannotFillTheirCartFromSomebodyElsesOrder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	own, _, _ := threeVariants(t, "reorder-access-fixture")
	number := orderOfVariants(t, map[uuid.UUID]int32{own: 1})

	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)

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
	placer.AddCookie(placedCookie(t, number))
	ok := httptest.NewRecorder()
	h.ReorderItems(ok, placer)

	if ok.Code != http.StatusSeeOther {
		t.Fatalf("the browser that placed the order got %d, want 303", ok.Code)
	}
	if got := ok.Header().Get("Location"); !strings.Contains(got, "added=1") {
		t.Errorf("the redirect is %q, want it to report one line added", got)
	}
}
