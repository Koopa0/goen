//go:build integration

package cart_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestStore_ReorderBuyableLines holds that Reorder puts back only what can
// still be bought: a retired or emptied variant is skipped.
func TestStore_ReorderBuyableLines(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	live, retired, empty := ordertest.ThreeVariants(t, pool, "reorder-fixture")

	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{live: 2, retired: 1, empty: 1})

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

// TestStore_ReorderInverseLock holds that inverse reorders share one cart lock.
// It makes the former A→B/B→A deadlock deterministic: the first reorder pauses
// after touching A; the second must be waiting on the cart root, not touching B
// and completing half of a lock cycle.
func TestStore_ReorderInverseLock(t *testing.T) {
	ctx := t.Context()
	baseStore := cart.NewStore(pool)
	variantA, variantB, _ := ordertest.ThreeVariants(t, pool, "inverse-reorder-lock")
	orderAB := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{variantA: 1, variantB: 1})
	orderBA := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{variantA: 1, variantB: 1})
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

// TestStore_ReorderRollback holds that a reorder rolls back every add when one
// write fails: it is one cart change, not a sequence that can strand its
// prefix. The trigger is scoped to this test's cart and the order's second
// variant, so the first insert has already succeeded in the transaction when
// the forced failure occurs.
func TestStore_ReorderRollback(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	first, second, _ := ordertest.ThreeVariants(t, pool, "atomic-reorder")
	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{first: 1, second: 1})
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

// TestStore_ReorderCataloguePrice holds that Reorder prices a reordered line
// from the catalogue, not from the old order.
func TestStore_ReorderCataloguePrice(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	vid, _, _ := ordertest.ThreeVariants(t, pool, "reorder-price-fixture")
	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{vid: 1})

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

// TestHandler_ReorderItemsStranger holds that ReorderItems answers a stranger
// 404 without opening a cart, so the order's contents do not leak by another
// route, while the browser that placed the order may reorder.
func TestHandler_ReorderItemsStranger(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	own, _, _ := ordertest.ThreeVariants(t, pool, "reorder-access-fixture")
	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{own: 1})

	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

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

// slugOf is the slug of the product a variant belongs to; the fixtures name a
// product by its slug.
func slugOf(t *testing.T, vid uuid.UUID) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT p.slug FROM products p JOIN product_variants v ON v.product_id = p.id WHERE v.id = $1`,
		vid).Scan(&slug); err != nil {
		t.Fatalf("read the slug of %s: %v", vid, err)
	}
	return slug
}

// unlist takes the variant's product off sale.
func unlist(t *testing.T, vid uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		UPDATE products SET status = 'draft', published_at = NULL
		WHERE id = (SELECT product_id FROM product_variants WHERE id = $1)`, vid); err != nil {
		t.Fatalf("unlist %s: %v", vid, err)
	}
}

// TestStore_SkippedOnReorder holds that the lines a buy-again would leave out
// come back with the reason, and with a product page only while the product is
// still listed, whatever has become of the variant.
func TestStore_SkippedOnReorder(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	live, retired, empty := ordertest.ThreeVariants(t, pool, "skipped-fixture")
	unlisted := ordertest.FreshVariant(t, pool, "skipped-unlisted")
	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{live: 2, retired: 1, empty: 1, unlisted: 1})

	if _, err := pool.Exec(ctx, `UPDATE product_variants SET is_active = false WHERE id = $1`, retired); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE order_lines SET variant_label = 'Red' WHERE variant_id = $1`, retired); err != nil {
		t.Fatalf("label the retired line: %v", err)
	}
	emptyTheShelfFor(t, empty)
	unlist(t, unlisted)

	got, err := s.SkippedOnReorder(ctx, number)
	if err != nil {
		t.Fatalf("SkippedOnReorder: %v", err)
	}
	shared, alone := slugOf(t, live), slugOf(t, unlisted)
	want := []cart.SkippedLine{
		{Name: shared, Label: "Red", Reason: cart.SkipGone, Slug: shared},
		{Name: shared, Reason: cart.SkipSoldOut, Slug: shared},
		{Name: alone, Reason: cart.SkipGone},
	}
	order := func(a, b cart.SkippedLine) int {
		return strings.Compare(a.Name+string(a.Reason), b.Name+string(b.Reason))
	}
	slices.SortFunc(got, order)
	slices.SortFunc(want, order)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("SkippedOnReorder (-want +got):\n%s", diff)
	}
}

// TestHandler_PageReorderLeftOut holds that after a buy-again the cart page
// names what was left out to the browser that may see the order, and gives any
// other browser the counts and no names.
func TestHandler_PageReorderLeftOut(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			s := cart.NewStore(storeRolePool(t))
			live := ordertest.FreshVariant(t, pool, "leftout-live")
			soldOut := ordertest.FreshVariant(t, pool, "leftout-soldout")
			unlisted := ordertest.FreshVariant(t, pool, "leftout-unlisted")
			number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{live: 1, soldOut: 1, unlisted: 1})
			emptyTheShelfFor(t, soldOut)
			unlist(t, unlisted)

			_, token := newCartSession(t, s)
			cartCookie := &http.Cookie{Name: "goen_cart", Value: token} //nolint:gosec // G124: fixture browser cart cookie.
			h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil)

			reorder := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/reorder", http.NoBody)
			reorder.SetPathValue("number", number)
			reorder.AddCookie(placedCookie(t, number))
			reorder.AddCookie(cartCookie)
			redirect := httptest.NewRecorder()
			h.ReorderItems(redirect, reorder)
			location := redirect.Header().Get("Location")
			if redirect.Code != http.StatusSeeOther || !strings.Contains(location, "reorder="+number) {
				t.Fatalf("reorder answered %d to %q, want a redirect carrying reorder=%s", redirect.Code, location, number)
			}

			page := func(cookies ...*http.Cookie) string {
				r := httptest.NewRequestWithContext(ctx, http.MethodGet, location, http.NoBody)
				for _, c := range cookies {
					r.AddCookie(c)
				}
				w := httptest.NewRecorder()
				h.Page(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("cart page answered %d", w.Code)
				}
				return w.Body.String()
			}

			soldOutSlug, unlistedSlug := slugOf(t, soldOut), slugOf(t, unlisted)
			owner := page(cartCookie, placedCookie(t, number))
			for _, want := range []string{
				`href="/p/` + soldOutSlug + `"`, i18n.T(ctx, i18n.KeySoldOut),
				">" + unlistedSlug + "<", i18n.T(ctx, i18n.KeyReorderGone),
			} {
				if !strings.Contains(owner, want) {
					t.Errorf("the browser that placed the order is not shown %q", want)
				}
			}
			if strings.Contains(owner, `/p/`+unlistedSlug) {
				t.Error("a product that is no longer listed is linked")
			}

			stranger := page(cartCookie)
			for _, name := range []string{soldOutSlug, unlistedSlug} {
				if strings.Contains(stranger, name) {
					t.Errorf("a browser with no access to the order is shown %q", name)
				}
			}
			if want := fmt.Sprintf(i18n.T(ctx, i18n.KeyReorderPartial), 1, 2); !strings.Contains(stranger, want) {
				t.Errorf("a browser with no access to the order is not shown the counts %q", want)
			}
		})
	}
}
