//go:build integration

package product_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	valkeycontainer "github.com/testcontainers/testcontainers-go/modules/valkey"

	"github.com/koopa0/goen/internal/product"
)

func TestCacheFailureFallsBackWithBoundedAdmission(t *testing.T) {
	container, err := valkeycontainer.Run(t.Context(), "valkey/valkey:8.0-alpine", testcontainers.WithCmdArgs("--maxmemory", "64mb", "--maxmemory-policy", "allkeys-lru", "--save", ""), fixedValkeyPort(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if terminateErr := testcontainers.TerminateContainer(container); terminateErr != nil {
			t.Error(terminateErr)
		}
	})
	addr, err := container.Endpoint(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := product.DefaultCacheConfig()
	cfg.MaxFallback = 2
	cfg.FillBudget = 10 * time.Second
	cache := openCacheOnAddr(t, addr, cfg)
	defer cache.Close()
	store := product.NewStoreWithCache(pool, cache)
	slug := "pixelight-9-pro"
	if _, loadErr := store.Load(t.Context(), slug, nil); loadErr != nil {
		t.Fatal(loadErr)
	}
	timeout := time.Second
	if stopErr := container.Stop(t.Context(), &timeout); stopErr != nil {
		t.Fatal(stopErr)
	}
	entered := make(chan struct{}, 5)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	product.SetIntegrationFillPause(func(ctx context.Context) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer product.SetIntegrationFillPause(nil)
	results := make(chan error, 5)
	for range 5 {
		go func() { _, loadErr := store.Load(t.Context(), slug, nil); results <- loadErr }()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("fallback slots were not admitted")
		}
	}
	for range 3 {
		if loadErr := cacheResult(t, results); !errors.Is(loadErr, product.ErrOverloaded) {
			t.Fatalf("excess fallback=%v", loadErr)
		}
	}
	once.Do(func() { close(release) })
	for range 2 {
		if loadErr := cacheResult(t, results); loadErr != nil {
			t.Fatalf("admitted fallback=%v", loadErr)
		}
	}
	if stats := product.CacheStatsOf(cache); stats.Fallbacks != 2 {
		t.Fatalf("fallback admissions=%d, want 2", stats.Fallbacks)
	}
	product.SetIntegrationFillPause(nil)
	if startErr := container.Start(t.Context()); startErr != nil {
		t.Fatal(startErr)
	}
	recoveredAddr, endpointErr := container.Endpoint(t.Context(), "")
	if endpointErr != nil {
		t.Fatal(endpointErr)
	}
	if recoveredAddr != addr {
		t.Fatalf("restart moved endpoint from %s to %s", addr, recoveredAddr)
	}
	deadline := time.Now().Add(5 * time.Second)
	before := product.CacheStatsOf(cache)
	for {
		_, loadErr := store.Load(t.Context(), slug, nil)
		stats := product.CacheStatsOf(cache)
		if loadErr == nil && stats.Fills > before.Fills && stats.Hits > before.Hits {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("same client did not recover cache hits: before=%+v after=%+v error=%v", before, stats, loadErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Docker can allocate a different ephemeral host port after stop/start. Bind a
// selected port explicitly so recovery exercises the original client endpoint.
func fixedValkeyPort(t *testing.T) testcontainers.CustomizeRequestOption {
	t.Helper()
	var config net.ListenConfig
	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
		config.PortBindings = network.PortMap{network.MustParsePort("6379/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: port}}}
	})
}
