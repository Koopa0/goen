//go:build integration

package product_test

import (
	"context"
	"errors"
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

	"github.com/koopa0/goen/internal/product"
)

func awaitCacheState(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case <-deadline.C:
			t.Fatal("cache concurrency barrier not reached")
		case <-tick.C:
		}
	}
}

func cacheResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("cache request did not finish")
		return nil
	}
}

func TestCacheBoundsLocalWaitersAndReleasesCancelledFollowers(t *testing.T) {
	cfg := product.DefaultCacheConfig()
	cfg.MaxWaiters = 2
	cfg.FillBudget = 15 * time.Second
	cache, stop := openCache(t, cfg)
	defer stop()
	id := uuid.New()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	owner := make(chan error, 1)
	go func() {
		_, err := cache.Get(t.Context(), id, 1, "en", func(ctx context.Context) (product.Presentation, error) {
			close(entered)
			select {
			case <-release:
				return product.Presentation{ProductID: id, Name: "owned fill"}, nil
			case <-ctx.Done():
				return product.Presentation{}, ctx.Err()
			}
		})
		owner <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owner never entered fill")
	}
	forbidden := func(context.Context) (product.Presentation, error) {
		t.Error("follower performed a second fill")
		return product.Presentation{}, errors.New("unexpected fill")
	}
	followCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, err := cache.Get(followCtx, id, 1, "en", forbidden); first <- err }()
	go func() { _, err := cache.Get(t.Context(), id, 1, "en", forbidden); second <- err }()
	awaitCacheState(t, func() bool { return product.CacheStatsOf(cache).Waiters == 2 })
	for range 6 {
		if _, err := cache.Get(t.Context(), id, 1, "en", forbidden); !errors.Is(err, product.ErrOverloaded) {
			t.Fatalf("excess follower=%v, want overload", err)
		}
	}
	cancel()
	if err := cacheResult(t, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled follower=%v", err)
	}
	for range 25 {
		cancelled, cancelFollower := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() { _, err := cache.Get(cancelled, id, 1, "en", forbidden); result <- err }()
		awaitCacheState(t, func() bool { return product.CacheStatsOf(cache).Waiters == 2 })
		cancelFollower()
		if err := cacheResult(t, result); !errors.Is(err, context.Canceled) {
			t.Fatalf("replacement follower=%v", err)
		}
	}
	releaseOnce.Do(func() { close(release) })
	if err := cacheResult(t, owner); err != nil {
		t.Fatal(err)
	}
	if err := cacheResult(t, second); err != nil {
		t.Fatal(err)
	}
	state := product.CacheStatsOf(cache)
	if state.Waiters != 0 || state.Filling != 0 || state.Fills != 1 {
		t.Fatalf("after drain: %+v", state)
	}
}

func TestCacheBoundsDistinctFillsAndCancelsThemOnClose(t *testing.T) {
	cfg := product.DefaultCacheConfig()
	cfg.MaxFills = 2
	cfg.FillBudget = 15 * time.Second
	cache, stop := openCache(t, cfg)
	defer stop()
	entered := make(chan struct{}, 2)
	result := make(chan error, 2)
	for range 2 {
		id := uuid.New()
		go func() {
			_, err := cache.Get(t.Context(), id, 1, "en", func(ctx context.Context) (product.Presentation, error) {
				entered <- struct{}{}
				<-ctx.Done()
				return product.Presentation{}, ctx.Err()
			})
			result <- err
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("fills not admitted")
		}
	}
	for range 20 {
		if _, err := cache.Get(t.Context(), uuid.New(), 1, "en", func(context.Context) (product.Presentation, error) {
			t.Error("unbounded distinct fill")
			return product.Presentation{}, nil
		}); !errors.Is(err, product.ErrOverloaded) {
			t.Fatalf("distinct fill=%v", err)
		}
	}
	cache.Close()
	for range 2 {
		if err := cacheResult(t, result); !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown fill=%v", err)
		}
	}
	state := product.CacheStatsOf(cache)
	if state.Filling != 0 || state.Waiters != 0 {
		t.Fatalf("shutdown retained work: %+v", state)
	}
}

type gateHoldTracer struct {
	entered chan struct{}
	release chan struct{}
	queries atomic.Int64
}

func (p *gateHoldTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "-- name: ProductPresentationGate") {
		p.queries.Add(1)
		p.entered <- struct{}{}
		select {
		case <-p.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (*gateHoldTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestProductRouteBoundsUnknownSlugMetadataBeforeDatabase(t *testing.T) {
	cfg := product.DefaultCacheConfig()
	cfg.MaxRequests = 2
	cache, stop := openCache(t, cfg)
	defer stop()
	hold := &gateHoldTracer{entered: make(chan struct{}, 2), release: make(chan struct{})}
	var once sync.Once
	defer once.Do(func() { close(hold.release) })
	pcfg := pool.Config().Copy()
	pcfg.ConnConfig.Tracer = hold
	traced, err := pgxpool.NewWithConfig(t.Context(), pcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	handler := product.NewHandler(product.NewStoreWithCache(traced, cache), testLogger(), "https://goen.example")
	request := func(slug string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/p/"+slug, http.NoBody)
		req.SetPathValue("slug", slug)
		rec := httptest.NewRecorder()
		handler.Detail(rec, req)
		return rec
	}
	done := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { done <- request("absent-" + uuid.NewString()) }()
	}
	for range 2 {
		select {
		case <-hold.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("metadata gate not reached")
		}
	}
	for range 20 {
		rec := request("absent-" + uuid.NewString())
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
			t.Fatalf("metadata overload=%d retry=%q", rec.Code, rec.Header().Get("Retry-After"))
		}
	}
	if got := hold.queries.Load(); got != 2 {
		t.Fatalf("admitted metadata queries=%d, want 2", got)
	}
	invalid := request(strings.Repeat("a", 121))
	if invalid.Code != http.StatusNotFound || hold.queries.Load() != 2 {
		t.Fatal("invalid slug reached database or lost 404")
	}
	once.Do(func() { close(hold.release) })
	for range 2 {
		select {
		case rec := <-done:
			if rec.Code != http.StatusNotFound {
				t.Fatalf("missing slug=%d", rec.Code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("metadata request did not finish")
		}
	}
	if product.CacheStatsOf(cache).Loads != 0 {
		t.Fatal("metadata admission not released")
	}
}
