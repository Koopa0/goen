//go:build integration

package product_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
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

func openCache(t *testing.T, cfg product.CacheConfig) (c *product.PresentationCache, stop func()) {
	t.Helper()
	addr := dbtest.Valkey(t)
	opened, err := product.OpenPresentationCache(addr, cfg)
	if err != nil {
		t.Fatalf("open cache: %v", err)
	}
	return opened, func() { opened.Close() }
}

func openCacheOnAddr(t *testing.T, addr string, cfg product.CacheConfig) *product.PresentationCache {
	t.Helper()
	cache, err := product.OpenPresentationCache(addr, cfg)
	if err != nil {
		t.Fatalf("open cache on %s: %v", addr, err)
	}
	return cache
}

func coalesceConfig() product.CacheConfig {
	return product.CacheConfig{
		LeaseTTL:        5 * time.Second,
		PayloadTTL:      time.Minute,
		MaxPayloadBytes: 1 << 20,
		MaxWaiters:      32,
		MaxFallback:     8,
	}
}

func checkoutAttemptKey(label string) string {
	digest := sha256.Sum256([]byte(label))
	return base64.RawURLEncoding.EncodeToString(digest[:16])
}

func checkoutQuote(
	t *testing.T,
	s *cart.Store,
	cartID uuid.UUID,
	shippingID uuid.UUID,
	addr *cart.Address,
) cart.CheckoutQuoteID {
	t.Helper()
	view, err := s.View(t.Context(), cartID)
	if err != nil {
		t.Fatalf("read cart quote: %v", err)
	}
	delivery, err := s.QuoteShipping(t.Context(), shippingID, view.SubtotalCents, addr.PostalCode)
	if err != nil {
		t.Fatalf("quote delivery: %v", err)
	}
	shipping, err := delivery.Total()
	if err != nil {
		t.Fatalf("quote delivery total: %v", err)
	}
	lines := make([]cart.CheckoutQuoteLine, 0, len(view.Lines))
	for i := range view.Lines {
		line := &view.Lines[i]
		variantID, parseErr := uuid.Parse(line.VariantID)
		if parseErr != nil {
			t.Fatalf("parse quoted variant: %v", parseErr)
		}
		lines = append(lines, cart.CheckoutQuoteLine{
			VariantID: variantID,
			Quantity:  line.Quantity,
			UnitCents: line.UnitCents,
		})
	}
	id, err := (cart.CheckoutQuote{
		CartID:            cartID,
		Lines:             lines,
		ShippingVersionID: shippingID,
		ShippingCents:     shipping,
	}).ID()
	if err != nil {
		t.Fatalf("build checkout quote: %v", err)
	}
	return id
}

func shipVersionFor(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("shipping version: %v", err)
	}
	return id
}

func checkoutAddr() *cart.Address {
	return &cart.Address{
		Email: "cache-checkout@example.com", Name: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
}

func TestWarmProductPageCutsPresentationQueries(t *testing.T) {
	cache, stopCache := openCache(t, product.DefaultCacheConfig())
	defer stopCache()

	var warmTotal, warmPresentation atomic.Int64
	warmPool := tracedPool(pool, &warmTotal, &warmPresentation)
	store := product.NewStoreWithCache(warmPool, cache)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	if _, err := store.Load(ctx, slug, nil); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	warmTotal.Store(0)
	warmPresentation.Store(0)
	for range 10 {
		if _, err := store.Load(ctx, slug, nil); err != nil {
			t.Fatalf("warm load: %v", err)
		}
	}
	warmTotalAfter := warmTotal.Load()
	warmAfter := warmPresentation.Load()
	stats := product.CacheStatsOf(cache)
	if stats.Hits < 9 {
		t.Fatalf("expected steady-state cache hits, got hits=%d fills=%d", stats.Hits, stats.Fills)
	}

	var coldTotal, coldPresentation atomic.Int64
	coldPool := tracedPool(pool, &coldTotal, &coldPresentation)
	coldStore := product.NewStore(coldPool)
	if _, err := coldStore.Load(ctx, slug, nil); err != nil {
		t.Fatalf("prime cold pool: %v", err)
	}
	coldTotal.Store(0)
	coldPresentation.Store(0)
	for range 10 {
		if _, err := coldStore.Load(ctx, slug, nil); err != nil {
			t.Fatalf("cold load: %v", err)
		}
	}
	coldTotalAfter := coldTotal.Load()
	coldAfter := coldPresentation.Load()

	if warmAfter >= coldAfter {
		t.Fatalf("warm presentation query count %d is not below cache-disabled %d",
			warmAfter, coldAfter)
	}
	reduction := 1 - float64(warmAfter)/float64(coldAfter)
	if reduction < 0.9 {
		t.Fatalf("presentation query reduction %.0f%% < 90%% (warm=%d cold=%d)",
			reduction*100, warmAfter, coldAfter)
	}
	t.Logf("total queries warm=%d cold=%d; cacheable presentation warm=%d cold=%d",
		warmTotalAfter, coldTotalAfter, warmAfter, coldAfter)
}

func TestPresentationRevisionAdvancesOnStaffEdit(t *testing.T) {
	cache, stopCache := openCache(t, product.DefaultCacheConfig())
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
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx,
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
	cache, stopCache := openCache(t, product.DefaultCacheConfig())
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
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx,
			`UPDATE products SET status = 'active' WHERE slug = $1`, slug)
	})
	if _, err := store.Load(ctx, slug, nil); !errors.Is(err, product.ErrNotFound) {
		t.Fatalf("unpublished load = %v, want not found", err)
	}
}

func TestIndependentInstancesCoalesceFillUnderSharedLease(t *testing.T) {
	addr := dbtest.Valkey(t)
	cfg := coalesceConfig()
	cacheA := openCacheOnAddr(t, addr, cfg)
	defer cacheA.Close()
	cacheB := openCacheOnAddr(t, addr, cfg)
	defer cacheB.Close()

	storeA := product.NewStoreWithCache(pool, cacheA)
	storeB := product.NewStoreWithCache(pool, cacheB)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	var wg sync.WaitGroup
	for i := range 8 {
		store := storeA
		if i%2 == 1 {
			store = storeB
		}
		wg.Add(1)
		go func(s *product.Store) {
			defer wg.Done()
			if _, loadErr := s.Load(ctx, slug, nil); loadErr != nil {
				t.Errorf("load: %v", loadErr)
			}
		}(store)
	}
	wg.Wait()

	fills := product.CacheStatsOf(cacheA).Fills + product.CacheStatsOf(cacheB).Fills
	coalesced := product.CacheStatsOf(cacheA).Coalesced + product.CacheStatsOf(cacheB).Coalesced
	if fills != 1 {
		t.Fatalf("shared lease allowed %d fills across two instances, want 1", fills)
	}
	if coalesced < 3 {
		t.Fatalf("expected local singleflight coalescing, got coalesced=%d fills=%d",
			coalesced, fills)
	}
}

func TestCacheFailureFallsBackWithBoundedAdmission(t *testing.T) {
	ctx := t.Context()
	addr, stopValkey, err := dbtest.StartValkey(ctx)
	if err != nil {
		t.Fatalf("start valkey: %v", err)
	}
	cfg := product.CacheConfig{
		LeaseTTL: 5 * time.Second, PayloadTTL: time.Minute,
		MaxPayloadBytes: 1 << 20, MaxWaiters: 32, MaxFallback: 2,
	}
	cache := openCacheOnAddr(t, addr, cfg)
	defer cache.Close()
	store := product.NewStoreWithCache(pool, cache)
	slug := "pixelight-9-pro"

	stopValkey()
	cache.Close()

	releaseFallback := make(chan struct{})
	maxFallback := cfg.MaxFallback
	var fallbackHolding atomic.Int32
	product.SetIntegrationFillPause(func(pauseCtx context.Context) error {
		if int(fallbackHolding.Add(1)) > maxFallback {
			fallbackHolding.Add(-1)
			return nil
		}
		select {
		case <-releaseFallback:
			return nil
		case <-pauseCtx.Done():
			return pauseCtx.Err()
		}
	})
	defer product.SetIntegrationFillPause(nil)

	const workers = 5
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, loadErr := store.Load(ctx, slug, nil)
			results[idx] = loadErr
		}(i)
	}
	for int(fallbackHolding.Load()) < maxFallback {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(25 * time.Millisecond)
	close(releaseFallback)
	wg.Wait()

	var overloaded, succeeded int
	for _, loadErr := range results {
		switch {
		case errors.Is(loadErr, product.ErrOverloaded):
			overloaded++
		case loadErr == nil:
			succeeded++
		default:
			t.Errorf("unexpected load error: %v", loadErr)
		}
	}
	stats := product.CacheStatsOf(cache)
	if stats.Fallbacks != 2 {
		t.Fatalf("fallback admissions = %d, want 2", stats.Fallbacks)
	}
	if succeeded != 2 {
		t.Fatalf("fallback successes = %d, want 2", succeeded)
	}
	if overloaded < 1 {
		t.Fatalf("expected at least one overload rejection, got %d", overloaded)
	}

	newAddr, stopNew, err := dbtest.StartValkey(ctx)
	if err != nil {
		t.Fatalf("restart valkey: %v", err)
	}
	defer stopNew()
	recovered := openCacheOnAddr(t, newAddr, cfg)
	defer recovered.Close()
	recoveredStore := product.NewStoreWithCache(pool, recovered)
	if _, loadErr := recoveredStore.Load(ctx, slug, nil); loadErr != nil {
		t.Fatalf("recovery load after valkey restart: %v", loadErr)
	}
}

func TestOverloadedProductRouteReturns503(t *testing.T) {
	ctx := t.Context()
	addr, stopValkey, err := dbtest.StartValkey(ctx)
	if err != nil {
		t.Fatalf("start valkey: %v", err)
	}
	cfg := product.CacheConfig{
		LeaseTTL: 30 * time.Second, PayloadTTL: time.Minute,
		MaxPayloadBytes: 1 << 20, MaxWaiters: 0, MaxFallback: 0,
	}
	cache := openCacheOnAddr(t, addr, cfg)
	defer cache.Close()
	stopValkey()

	h := product.NewHandler(product.NewStoreWithCache(pool, cache),
		testLogger(), "https://goen.example")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/p/pixelight-9-pro", http.NoBody)
	req.SetPathValue("slug", "pixelight-9-pro")
	res := httptest.NewRecorder()
	h.Detail(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for exhausted fallback on a live product", res.Code)
	}
	if res.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", res.Header().Get("Retry-After"))
	}
}

func TestCheckoutRejectsStalePriceAfterWarmPresentationCache(t *testing.T) {
	cache, stopCache := openCache(t, product.DefaultCacheConfig())
	defer stopCache()
	presentationStore := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	if _, err := presentationStore.Load(ctx, slug, nil); err != nil {
		t.Fatalf("warm presentation cache: %v", err)
	}

	var variantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT pv.id FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		WHERE p.slug = $1 AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock
		ORDER BY pv.position LIMIT 1`, slug).Scan(&variantID); err != nil {
		t.Fatalf("variant: %v", err)
	}
	var originalPrice int64
	if err := pool.QueryRow(ctx,
		`SELECT price_cents FROM product_variants WHERE id = $1`, variantID).Scan(&originalPrice); err != nil {
		t.Fatalf("original price: %v", err)
	}
	if _, execErr := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = 100000 WHERE id = $1`, variantID); execErr != nil {
		t.Fatalf("set checkout price: %v", execErr)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx,
			`UPDATE product_variants SET price_cents = $1 WHERE id = $2`, originalPrice, variantID)
	})

	cartStore := cart.NewStore(pool)
	cartID, err := cartStore.Create(ctx, mustCartToken(t), uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if addErr := cartStore.Add(ctx, cartID, variantID, 1); addErr != nil {
		t.Fatalf("add to cart: %v", addErr)
	}
	shippingID := shipVersionFor(t)
	addr := checkoutAddr()
	shown := checkoutQuote(t, cartStore, cartID, shippingID, addr)

	if _, execErr := pool.Exec(ctx,
		`UPDATE product_variants SET price_cents = 100001 WHERE id = $1`, variantID); execErr != nil {
		t.Fatalf("raise price: %v", execErr)
	}

	_, placeErr := cartStore.PlaceOrder(
		ctx, cartID, uuid.NullUUID{}, shippingID, addr, nil, "", shown,
		checkoutAttemptKey("cache-stale-price-"+uuid.NewString()),
	)
	if !errors.Is(placeErr, cart.ErrCheckoutChanged) {
		t.Fatalf("stale quote checkout = %v, want ErrCheckoutChanged", placeErr)
	}
}

func TestCheckoutRejectsExtraUnitAfterWarmPresentationCache(t *testing.T) {
	cache, stopCache := openCache(t, product.DefaultCacheConfig())
	defer stopCache()
	presentationStore := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()

	slug := "cache-stock-" + uuid.NewString()[:8]
	var productID, variantID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, $1, 'draft', NULL
		FROM brands b, categories c
		ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM products WHERE id = $1`, productID)
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, is_active, position)
		VALUES ($1, $2, 50000, true, 0) RETURNING id`,
		productID, strings.ToUpper(slug)+"-0").Scan(&variantID); err != nil {
		t.Fatalf("insert variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT record_inventory_movement($1, 1, 'adjustment', $2, NULL, NULL, NULL)`,
		variantID, "fixture:"+variantID.String()); err != nil {
		t.Fatalf("stock variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'active', published_at = now() WHERE id = $1`,
		productID); err != nil {
		t.Fatalf("publish product: %v", err)
	}
	if _, err := presentationStore.Load(ctx, slug, nil); err != nil {
		t.Fatalf("warm presentation cache: %v", err)
	}

	buyer := cart.NewStore(pool)
	buyerCart, err := buyer.Create(ctx, mustCartToken(t), uuid.NullUUID{})
	if err != nil {
		t.Fatalf("buyer cart: %v", err)
	}
	if addErr := buyer.Add(ctx, buyerCart, variantID, 1); addErr != nil {
		t.Fatalf("buyer add: %v", addErr)
	}
	shippingID := shipVersionFor(t)
	addr := checkoutAddr()
	buyerQuote := checkoutQuote(t, buyer, buyerCart, shippingID, addr)

	late := cart.NewStore(pool)
	lateCart, err := late.Create(ctx, mustCartToken(t), uuid.NullUUID{})
	if err != nil {
		t.Fatalf("late cart: %v", err)
	}
	if addErr := late.Add(ctx, lateCart, variantID, 1); addErr != nil {
		t.Fatalf("late add: %v", addErr)
	}
	staleQuote := checkoutQuote(t, late, lateCart, shippingID, addr)

	if _, placeErr := buyer.PlaceOrder(
		ctx, buyerCart, uuid.NullUUID{}, shippingID, addr, nil, "", buyerQuote,
		checkoutAttemptKey("cache-stock-buyer-"+uuid.NewString()),
	); placeErr != nil {
		t.Fatalf("buyer checkout: %v", placeErr)
	}

	_, placeErr := late.PlaceOrder(
		ctx, lateCart, uuid.NullUUID{}, shippingID, addr, nil, "", staleQuote,
		checkoutAttemptKey("cache-stock-late-"+uuid.NewString()),
	)
	if !errors.Is(placeErr, cart.ErrUnavailable) {
		t.Fatalf("second checkout = %v, want ErrUnavailable", placeErr)
	}
}

func mustCartToken(t *testing.T) string {
	t.Helper()
	tok, err := cart.NewToken()
	if err != nil {
		t.Fatalf("cart token: %v", err)
	}
	return tok
}

func TestPausedFillNeverServesStaleRevisionAfterStaffEdit(t *testing.T) {
	addr := dbtest.Valkey(t)
	cfg := coalesceConfig()
	cacheA := openCacheOnAddr(t, addr, cfg)
	defer cacheA.Close()
	cacheB := openCacheOnAddr(t, addr, cfg)
	defer cacheB.Close()
	storeA := product.NewStoreWithCache(pool, cacheA)
	storeB := product.NewStoreWithCache(pool, cacheB)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	fillEntered := make(chan struct{})
	releaseFill := make(chan struct{})
	var fillEnteredOnce sync.Once
	var pauseRemaining atomic.Int32
	pauseRemaining.Store(1)
	product.SetIntegrationFillPause(func(pauseCtx context.Context) error {
		if pauseRemaining.Add(-1) < 0 {
			return nil
		}
		fillEnteredOnce.Do(func() { close(fillEntered) })
		select {
		case <-releaseFill:
			return nil
		case <-pauseCtx.Done():
			return pauseCtx.Err()
		}
	})
	t.Cleanup(func() { product.SetIntegrationFillPause(nil) })

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := storeA.Load(ctx, slug, nil); err != nil {
			t.Errorf("instance A load: %v", err)
		}
	}()
	select {
	case <-fillEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("fill owner never entered pause hook")
	}

	newName := "快取競態 " + uuid.NewString()
	if _, execErr := pool.Exec(ctx,
		`UPDATE products SET name = $1 WHERE slug = $2`, newName, slug); execErr != nil {
		t.Fatalf("staff edit during paused fill: %v", execErr)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx,
			`UPDATE products SET name = 'Pixelight 9 Pro 5G' WHERE slug = $1`, slug)
	})

	viewDuring, err := storeB.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("instance B load during paused fill: %v", err)
	}
	if viewDuring.Name != newName {
		t.Fatalf("name during paused fill = %q, want %q", viewDuring.Name, newName)
	}

	close(releaseFill)
	wg.Wait()

	viewAfter, err := storeA.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("instance A load after resume: %v", err)
	}
	if viewAfter.Name != newName {
		t.Fatalf("name after resume = %q, want %q", viewAfter.Name, newName)
	}
}

func TestChildTableEditAdvancesCachedPresentation(t *testing.T) {
	cache, stopCache := openCache(t, product.DefaultCacheConfig())
	defer stopCache()
	store := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	if _, err := store.Load(ctx, slug, nil); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	newValue := "OLED " + uuid.NewString()
	var specID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT ps.id FROM product_specs ps
		JOIN products p ON p.id = ps.product_id
		WHERE p.slug = $1 ORDER BY ps.position LIMIT 1`, slug).Scan(&specID); err != nil {
		t.Fatalf("spec id: %v", err)
	}
	var oldValue string
	if err := pool.QueryRow(ctx, `SELECT value FROM product_specs WHERE id = $1`, specID).
		Scan(&oldValue); err != nil {
		t.Fatalf("old spec: %v", err)
	}
	if _, execErr := pool.Exec(ctx,
		`UPDATE product_specs SET value = $1 WHERE id = $2`, newValue, specID); execErr != nil {
		t.Fatalf("edit spec: %v", execErr)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx,
			`UPDATE product_specs SET value = $1 WHERE id = $2`, oldValue, specID)
	})

	view, err := store.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("reload after spec edit: %v", err)
	}
	for _, spec := range view.Specs {
		if spec.Value == newValue {
			return
		}
	}
	t.Fatalf("edited spec value %q not in cached view", newValue)
}

func TestTaxonomyEditAdvancesCachedPresentation(t *testing.T) {
	cache, stopCache := openCache(t, product.DefaultCacheConfig())
	defer stopCache()
	store := product.NewStoreWithCache(pool, cache)
	ctx := t.Context()
	slug := "pixelight-9-pro"

	before, err := store.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	newBrand := "品牌 " + uuid.NewString()
	var brandID uuid.UUID
	if queryErr := pool.QueryRow(ctx, `
		SELECT b.id FROM brands b
		JOIN products p ON p.brand_id = b.id
		WHERE p.slug = $1`, slug).Scan(&brandID); queryErr != nil {
		t.Fatalf("brand id: %v", queryErr)
	}
	if _, execErr := pool.Exec(ctx,
		`UPDATE brands SET name = $1 WHERE id = $2`, newBrand, brandID); execErr != nil {
		t.Fatalf("edit brand: %v", execErr)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx,
			`UPDATE brands SET name = $1 WHERE id = $2`, before.Brand, brandID)
	})

	after, err := store.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("reload after brand edit: %v", err)
	}
	if after.Brand != newBrand {
		t.Fatalf("brand after taxonomy edit = %q, want %q", after.Brand, newBrand)
	}
}

func TestLocaleVariantsUseDistinctCacheKeys(t *testing.T) {
	cache, stopCache := openCache(t, product.DefaultCacheConfig())
	defer stopCache()
	store := product.NewStoreWithCache(pool, cache)
	slug := "aurora-slate-11"

	zhCtx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	enCtx := i18n.WithLocale(t.Context(), i18n.En)

	zhView, err := store.Load(zhCtx, slug, nil)
	if err != nil {
		t.Fatalf("zh load: %v", err)
	}
	enView, err := store.Load(enCtx, slug, nil)
	if err != nil {
		t.Fatalf("en load: %v", err)
	}
	if zhView.Summary == enView.Summary {
		t.Fatalf("zh and en summaries both %q; locale keys must diverge", zhView.Summary)
	}
	stats := product.CacheStatsOf(cache)
	if stats.Fills < 2 {
		t.Fatalf("locale variants recorded %d fills, want at least 2 distinct keys", stats.Fills)
	}

	zhView2, err := store.Load(zhCtx, slug, nil)
	if err != nil {
		t.Fatalf("zh warm load: %v", err)
	}
	if zhView2.Summary != zhView.Summary {
		t.Fatalf("zh warm summary changed from %q to %q", zhView.Summary, zhView2.Summary)
	}
}

func TestRandomSlugStreamDoesNotGrowCacheWithoutBound(t *testing.T) {
	cache, stopCache := openCache(t, product.DefaultCacheConfig())
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
	if stats.Fills > 0 {
		t.Fatalf("random slug stream recorded %d fills; negative results must not populate cache", stats.Fills)
	}
	t.Logf("misses=%d fallbacks=%d", stats.Misses, stats.Fallbacks)
}
