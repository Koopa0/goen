//go:build integration

package backinstock_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/backinstock"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/user"
)

func TestRestockNoticeIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s := backinstock.NewStore(pool)
	vid, slug := soldOutVariant(t)
	addr := waitingAddr(t)

	for range 3 {
		if err := s.Request(ctx, slug, vid.String(), addr, ""); err != nil {
			t.Fatalf("request: %v", err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stock_notifications
		WHERE variant_id = $1 AND lower(email) = lower($2)`, vid, addr).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d rows after asking three times, want 1", n)
	}

	// Case does not make it a different person.
	if err := s.Request(ctx, slug, vid.String(), strings.ToUpper(waitingAddr(t)), ""); err != nil {
		t.Fatalf("uppercase: %v", err)
	}
	// Scoped to the address too: every restock test shares one variant.
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stock_notifications
		WHERE variant_id = $1 AND lower(email) = lower($2)`, vid, addr).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d rows after the same address in a different case, want 1", n)
	}
}

func TestANotifiedRequestDoesNotBlockTheNextOne(t *testing.T) {
	ctx := t.Context()
	s := backinstock.NewStore(pool)
	vid, slug := soldOutVariant(t)
	const addr = "again@example.com"

	if err := s.Request(ctx, slug, vid.String(), addr, ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE stock_notifications SET notified_at = now()
		WHERE variant_id = $1 AND lower(email) = lower($2)`, vid, addr); err != nil {
		t.Fatalf("mark notified: %v", err)
	}

	if err := s.Request(ctx, slug, vid.String(), addr, ""); err != nil {
		t.Fatalf("second: %v", err)
	}
	var pending int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stock_notifications
		WHERE variant_id = $1 AND lower(email) = lower($2) AND notified_at IS NULL`,
		vid, addr).Scan(&pending); err != nil {
		t.Fatalf("count: %v", err)
	}
	if pending != 1 {
		t.Errorf("%d pending requests after a previous one was notified, want 1 — "+
			"the index must bound PENDING rows, not every row ever", pending)
	}
}

func TestRestockNoticeRefusesAnUnusableAddress(t *testing.T) {
	ctx := t.Context()
	s := backinstock.NewStore(pool)
	vid, slug := soldOutVariant(t)

	for _, addr := range []string{"", "  ", "nope", "a@", "@b.com", "a b@c.com"} {
		if err := s.Request(ctx, slug, vid.String(), addr, ""); !errors.Is(err, backinstock.ErrInvalid) {
			t.Errorf("%q gave %v, want ErrInvalid", addr, err)
		}
	}
	// The control: a real address IS accepted.
	if err := s.Request(ctx, slug, vid.String(), "real@example.com", ""); err != nil {
		t.Errorf("a valid address was refused: %v", err)
	}
}

func TestRestockNoticeIsBoundToTheRouteAndASoldOutActiveVariant(t *testing.T) {
	ctx := t.Context()
	s := backinstock.NewStore(pool)
	vid, slug := soldOutVariant(t)

	if err := s.Request(ctx, slug+"-wrong", vid.String(),
		"wrong-route@example.com", ""); !errors.Is(err, backinstock.ErrInvalid) {
		t.Errorf("wrong route product gave %v, want ErrInvalid", err)
	}

	var inStockID uuid.UUID
	var inStockSlug string
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, p.slug
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock
		LIMIT 1`).Scan(&inStockID, &inStockSlug); err != nil {
		t.Fatalf("find in-stock variant: %v", err)
	}
	if err := s.Request(ctx, inStockSlug, inStockID.String(),
		"already-stocked@example.com", ""); !errors.Is(err, backinstock.ErrInvalid) {
		t.Errorf("in-stock variant gave %v, want ErrInvalid", err)
	}

	for _, tc := range []struct {
		name   string
		update string
	}{
		{"inactive variant", `UPDATE product_variants SET is_active = false WHERE id = $1`},
		{"inactive product", `UPDATE products p SET status = 'draft'
			FROM product_variants pv WHERE pv.id = $1 AND p.id = pv.product_id`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, tc.update, vid); err != nil {
				t.Fatalf("make fixture ineligible: %v", err)
			}
			if err := backinstock.NewStore(tx).Request(ctx, slug, vid.String(),
				"inactive-"+strings.ReplaceAll(tc.name, " ", "-")+"@example.com", ""); !errors.Is(err, backinstock.ErrInvalid) {
				t.Errorf("request gave %v, want ErrInvalid", err)
			}
		})
	}

	var inserted int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stock_notifications
		WHERE email IN ('wrong-route@example.com', 'already-stocked@example.com')`).Scan(&inserted); err != nil {
		t.Fatalf("count refused notices: %v", err)
	}
	if inserted != 0 {
		t.Errorf("%d notifications were inserted for ineligible route/stock", inserted)
	}
}

// TestRestockNoticeAndRestockLinearizeOnVariant covers both serial orders of the
// stock-notice decision and the movement that makes stock sellable. The notice
// query must own the variant row while it decides and inserts: otherwise a restock
// claims before the request commits, and a request queued behind a restock inserts
// from its stale statement snapshot instead of rechecking the committed stock.
func TestRestockNoticeAndRestockLinearizeOnVariant(t *testing.T) {
	t.Run("request wins and restock claims it", func(t *testing.T) {
		ctx := t.Context()
		variantID, slug, sku, actorID := stockRaceFixture(t)
		address := "request-first-" + uuid.NewString() + "@example.com"

		requestTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin notice request: %v", err)
		}
		defer func() { _ = requestTx.Rollback(context.WithoutCancel(ctx)) }()
		if err := backinstock.NewStore(requestTx).Request(
			ctx, slug, variantID.String(), address, "",
		); err != nil {
			t.Fatalf("hold notice request open: %v", err)
		}

		applicationName := "stock-race-request-first-" + uuid.NewString()[:8]
		restockPool := stockRacePool(t, applicationName)
		restockStore := stock.NewStore(restockPool)
		staffCtx := user.NewContext(ctx, user.User{ID: actorID.String(), Role: user.RoleAdmin})
		restocked := make(chan error, 1)
		go func() {
			restocked <- restockStore.Adjust(
				staffCtx, sku, 1, actorID.String(), "stock-race-"+uuid.NewString(),
			)
		}()
		waitForStockRaceLock(t, applicationName, restocked)

		if err := requestTx.Commit(ctx); err != nil {
			t.Fatalf("commit notice request: %v", err)
		}
		if err := stockRaceResult(t, restocked); err != nil {
			t.Fatalf("restock after committed request: %v", err)
		}

		var notified, enqueued bool
		if err := pool.QueryRow(ctx, `
			SELECT sn.notified_at IS NOT NULL,
			       EXISTS (
			           SELECT 1 FROM outbox_messages m
			           WHERE m.topic = 'catalogue.restocked'
			             AND m.dedupe_key = sn.id::text
			       )
			FROM stock_notifications sn
			WHERE sn.variant_id = $1 AND lower(sn.email) = lower($2)`,
			variantID, address).Scan(&notified, &enqueued); err != nil {
			t.Fatalf("read claimed notice: %v", err)
		}
		if !notified || !enqueued {
			t.Errorf("request-first notice notified/enqueued = %t/%t, want true/true",
				notified, enqueued)
		}
	})

	t.Run("restock wins and late request rechecks", func(t *testing.T) {
		ctx := t.Context()
		variantID, slug, sku, actorID := stockRaceFixture(t)
		address := "restock-first-" + uuid.NewString() + "@example.com"
		applicationName := "stock-race-restock-first-" + uuid.NewString()[:8]

		// Pause the real admin transaction at its audit INSERT. At that point its
		// inventory movement and empty notice claim have both run, while the
		// variant update is still invisible and its row lock remains held.
		suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
		functionName := pgx.Identifier{"test_stock_notice_restock_barrier_" + suffix}.Sanitize()
		triggerName := pgx.Identifier{"test_stock_notice_restock_trigger_" + suffix}.Sanitize()
		const barrierKey int64 = 8_112_233_445_566_781
		if _, err := pool.Exec(ctx, fmt.Sprintf(`
			CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
			BEGIN
				IF NEW.action = 'stock.adjust'
				   AND NEW.entity_table = 'product_variants'
				   AND NEW.entity_id = '%s'::uuid THEN
					PERFORM pg_advisory_xact_lock(%d);
				END IF;
				RETURN NEW;
			END
			$body$`, functionName, variantID, barrierKey)); err != nil {
			t.Fatalf("create restock barrier: %v", err)
		}
		if _, err := pool.Exec(ctx, fmt.Sprintf(`
			CREATE TRIGGER %s BEFORE INSERT ON audit_events
			FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
			t.Fatalf("install restock barrier: %v", err)
		}
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
				`DROP TRIGGER IF EXISTS %s ON audit_events`, triggerName))
			_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
				`DROP FUNCTION IF EXISTS %s()`, functionName))
		})

		barrier, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin restock barrier: %v", err)
		}
		defer func() { _ = barrier.Rollback(context.WithoutCancel(ctx)) }()
		if _, err := barrier.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); err != nil {
			t.Fatalf("lock restock barrier: %v", err)
		}

		restockPool := stockRacePool(t, applicationName)
		restockStore := stock.NewStore(restockPool)
		staffCtx := user.NewContext(ctx, user.User{ID: actorID.String(), Role: user.RoleAdmin})
		restocked := make(chan error, 1)
		go func() {
			restocked <- restockStore.Adjust(
				staffCtx, sku, 1, actorID.String(), "stock-race-"+uuid.NewString(),
			)
		}()
		waitForStockRaceLock(t, applicationName, restocked)

		requestApplication := "stock-race-request-late-" + uuid.NewString()[:8]
		requestPool := stockRacePool(t, requestApplication)
		requested := make(chan error, 1)
		go func() {
			requested <- backinstock.NewStore(requestPool).Request(
				ctx, slug, variantID.String(), address, "",
			)
		}()
		waitForStockRaceLock(t, requestApplication, requested)

		if err := barrier.Commit(ctx); err != nil {
			t.Fatalf("release restock transaction: %v", err)
		}
		if err := stockRaceResult(t, restocked); err != nil {
			t.Fatalf("restock: %v", err)
		}
		if err := stockRaceResult(t, requested); !errors.Is(err, backinstock.ErrInvalid) {
			t.Fatalf("request queued behind restock = %v, want ErrInvalid", err)
		}

		var notices, messages int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM stock_notifications
			WHERE variant_id = $1 AND lower(email) = lower($2)`,
			variantID, address).Scan(&notices); err != nil {
			t.Fatalf("count late notices: %v", err)
		}
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM outbox_messages
			WHERE topic = 'catalogue.restocked' AND payload->>'email' = $1`,
			address).Scan(&messages); err != nil {
			t.Fatalf("count late messages: %v", err)
		}
		if notices != 0 || messages != 0 {
			t.Errorf("restock-first race left %d notices and %d messages, want 0/0",
				notices, messages)
		}
	})
}

func stockRaceFixture(t *testing.T) (variantID uuid.UUID, slug, sku string, actorID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin stock-race fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	slug = "stock-race-" + uuid.NewString()
	sku = "STOCK-RACE-" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))
	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, '補貨競態測試商品', 'draft', now()
		FROM brands b CROSS JOIN categories c
		WHERE c.parent_id IS NULL
		ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("create stock-race product: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO product_variants
			(product_id, sku, price_cents, stock_quantity, safety_stock)
		VALUES ($1, $2, 100000, 0, 0)
		RETURNING id`, productID, sku).Scan(&variantID); err != nil {
		t.Fatalf("create stock-race variant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE products SET status = 'active' WHERE id = $1`, productID); err != nil {
		t.Fatalf("publish stock-race product: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('stock-race-' || gen_random_uuid() || '@goen.invalid', 'admin', '補貨競態測試')
		RETURNING id`).Scan(&actorID); err != nil {
		t.Fatalf("create stock-race actor: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit stock-race fixture: %v", err)
	}
	return variantID, slug, sku, actorID
}

func stockRacePool(t *testing.T, applicationName string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse stock-race pool config: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open stock-race pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func waitForStockRaceLock(t *testing.T, applicationName string, done <-chan error) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()

	for {
		select {
		case err := <-done:
			t.Fatalf("%s returned before reaching the intended database lock: %v",
				applicationName, err)
		case <-ticker.C:
			_, waiting, err := dbtest.Blocked(t.Context(), pool, dbtest.Match{Application: applicationName})
			if err == nil && waiting {
				return
			}
		case <-timer.C:
			t.Fatalf("%s never blocked on the intended database lock", applicationName)
		}
	}
}

func stockRaceResult(t *testing.T, done <-chan error) error {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		t.Fatal("stock race operation did not finish after its database lock was released")
		return nil
	}
}

func soldOutVariant(t *testing.T) (id uuid.UUID, slug string) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `
		SELECT pv.id, p.slug FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity <= pv.safety_stock
		ORDER BY pv.stock_quantity - pv.safety_stock
		LIMIT 1`).Scan(&id, &slug); err != nil {
		t.Fatalf("find variant: %v", err)
	}
	return id, slug
}

func waitingAddr(t *testing.T) string {
	t.Helper()
	return "waiting-" + strings.ToLower(t.Name()) + "@example.com"
}
