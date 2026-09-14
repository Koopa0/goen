//go:build integration

package product_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/product"
)

type countingTracer struct {
	queries      *atomic.Int64
	presentation *atomic.Int64
}

func (t countingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	t.queries.Add(1)
	if t.presentation != nil && isPresentationQuery(data.SQL) {
		t.presentation.Add(1)
	}
	return ctx
}

func isPresentationQuery(sql string) bool {
	lower := strings.ToLower(sql)
	return strings.Contains(lower, "-- name: productbyslug") ||
		strings.Contains(lower, "-- name: categoryancestors") ||
		strings.Contains(lower, "-- name: productimages") ||
		strings.Contains(lower, "-- name: productspecs")
}

func (countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func tracedPool(base *pgxpool.Pool, queries, presentation *atomic.Int64) *pgxpool.Pool {
	cfg := base.Config()
	cfg.ConnConfig.Tracer = countingTracer{queries: queries, presentation: presentation}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		panic(err)
	}
	return p
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func openCache(t *testing.T) (cache *product.PresentationCache, stop func()) {
	t.Helper()
	addr := dbtest.Valkey(t)
	cache, err := product.OpenPresentationCache(addr, product.DefaultCacheConfig())
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	return cache, func() { cache.Close() }
}

func TestWarmProductPageCutsPresentationQueries(t *testing.T) {
	cache, stopCache := openCache(t)
	defer stopCache()

	var warmPresentation atomic.Int64
	warmPool := tracedPool(pool, &atomic.Int64{}, &warmPresentation)
	store := product.NewStoreWithCache(warmPool, cache)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	if _, err := store.Load(ctx, slug, nil); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	warmPresentation.Store(0)
	for range 10 {
		if _, err := store.Load(ctx, slug, nil); err != nil {
			t.Fatalf("warm load: %v", err)
		}
	}
	warmAfter := warmPresentation.Load()
	stats := product.CacheStatsOf(cache)
	hits, fills := stats.Hits, stats.Fills
	if hits < 9 {
		t.Fatalf("expected steady-state cache hits, got hits=%d fills=%d", hits, fills)
	}

	var coldPresentation atomic.Int64
	coldPool := tracedPool(pool, &atomic.Int64{}, &coldPresentation)
	coldStore := product.NewStore(coldPool)
	if _, err := coldStore.Load(ctx, slug, nil); err != nil {
		t.Fatalf("prime cold pool: %v", err)
	}
	coldPresentation.Store(0)
	for range 10 {
		if _, err := coldStore.Load(ctx, slug, nil); err != nil {
			t.Fatalf("cold load: %v", err)
		}
	}
	coldAfter := coldPresentation.Load()

	if warmAfter >= coldAfter {
		t.Fatalf("warm presentation query count %d is not below cache-disabled %d", warmAfter, coldAfter)
	}
	reduction := 1 - float64(warmAfter)/float64(coldAfter)
	if reduction < 0.9 {
		t.Fatalf("presentation query reduction %.0f%% < 90%% (warm=%d cold=%d)",
			reduction*100, warmAfter, coldAfter)
	}
}

func TestPresentationRevisionAdvancesOnStaffEdit(t *testing.T) {
	cache, stopCache := openCache(t)
	defer stopCache()
	store := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	viewBefore, err := store.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("load before: %v", err)
	}

	newName := viewBefore.Name + " 更新"
	if _, execErr := pool.Exec(ctx,
		`UPDATE products SET name = $1 WHERE slug = $2`, newName, slug); execErr != nil {
		t.Fatalf("staff edit: %v", execErr)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(),
			`UPDATE products SET name = $1 WHERE slug = $2`, viewBefore.Name, slug)
	})

	viewAfter, err := store.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("load after: %v", err)
	}
	if viewAfter.Name != newName {
		t.Fatalf("name after edit = %q, want %q", viewAfter.Name, newName)
	}
}

func TestUnpublishedProductIsNotServedFromCache(t *testing.T) {
	cache, stopCache := openCache(t)
	defer stopCache()
	store := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()
	slug := "aurora-slate-11"

	if _, err := store.Load(ctx, slug, nil); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	if _, execErr := pool.Exec(ctx,
		`UPDATE products SET status = 'draft' WHERE slug = $1`, slug); execErr != nil {
		t.Fatalf("unpublish: %v", execErr)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(),
			`UPDATE products SET status = 'active' WHERE slug = $1`, slug)
	})
	if _, err := store.Load(ctx, slug, nil); !errors.Is(err, product.ErrNotFound) {
		t.Fatalf("unpublished load = %v, want not found", err)
	}
}

func TestConcurrentFillCoalescesUnderLease(t *testing.T) {
	addr := dbtest.Valkey(t)
	cfg := product.DefaultCacheConfig()
	cfg.LeaseTTL = 2 * time.Second
	cache, err := product.OpenPresentationCache(addr, cfg)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	defer cache.Close()

	store := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, loadErr := store.Load(ctx, slug, nil); loadErr != nil {
				t.Errorf("load: %v", loadErr)
			}
		}()
	}
	wg.Wait()
	recordedFills := product.CacheStatsOf(cache).Fills
	if recordedFills == 0 {
		t.Fatal("expected at least one cache fill under concurrent load")
	}
}

func TestCacheFailureFallsBackWithBoundedAdmission(t *testing.T) {
	addr := dbtest.Valkey(t)
	cache, err := product.OpenPresentationCache(addr, product.DefaultCacheConfig())
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	cache.Close()

	cache, err = product.OpenPresentationCache(addr, product.CacheConfig{
		LeaseTTL: 5 * time.Second, PayloadTTL: time.Minute,
		MaxPayloadBytes: 1 << 20, MaxWaiters: 4, MaxFallback: 2,
	})
	if err != nil {
		t.Fatalf("reopen cache: %v", err)
	}
	defer cache.Close()

	store := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	if _, err := store.Load(ctx, slug, nil); err != nil {
		t.Fatalf("fallback load: %v", err)
	}
}

func TestOverloadedProductRouteReturns503(t *testing.T) {
	addr := dbtest.Valkey(t)
	cfg := product.CacheConfig{
		LeaseTTL: 30 * time.Second, PayloadTTL: time.Minute,
		MaxPayloadBytes: 1 << 20, MaxWaiters: 0, MaxFallback: 0,
	}
	cache, err := product.OpenPresentationCache(addr, cfg)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	defer cache.Close()

	h := product.NewHandler(product.NewStoreWithCache(pool, cache),
		testLogger(), "https://goen.example")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p/missing-forever", http.NoBody)
	req.SetPathValue("slug", "missing-forever-slug-xyz")
	res := httptest.NewRecorder()
	h.Detail(res, req)
	if res.Code != http.StatusNotFound && res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 404 or 503", res.Code)
	}
}

func TestCheckoutStillValidatesLivePriceAfterWarmCache(t *testing.T) {
	cache, stopCache := openCache(t)
	defer stopCache()
	store := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	view, err := store.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("warm load: %v", err)
	}
	cachedPrice := view.PriceCents

	if _, execErr := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = price_cents + 100
		 WHERE product_id = (SELECT id FROM products WHERE slug = $1)`, slug); execErr != nil {
		t.Fatalf("raise price: %v", execErr)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(),
			`UPDATE product_variants SET price_cents = price_cents - 100
			 WHERE product_id = (SELECT id FROM products WHERE slug = $1)`, slug)
	})

	view2, err := store.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if view2.PriceCents == cachedPrice {
		t.Fatalf("price stayed %d after stock edit; live reads must bypass cache", cachedPrice)
	}
}

func TestRandomSlugStreamDoesNotGrowCacheWithoutBound(t *testing.T) {
	cache, stopCache := openCache(t)
	defer stopCache()
	store := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()

	for i := range 50 {
		slug := fmt.Sprintf("no-such-product-%04d", i)
		if _, err := store.Load(ctx, slug, nil); !errors.Is(err, product.ErrNotFound) {
			t.Fatalf("random slug %q: %v", slug, err)
		}
	}
	stats := product.CacheStatsOf(cache)
	misses, fills, fallbacks := stats.Misses, stats.Fills, stats.Fallbacks
	if fills > 0 {
		t.Fatalf("random slug stream recorded %d fills; negative results must not populate cache", fills)
	}
	t.Logf("misses=%d fallbacks=%d", misses, fallbacks)
}
