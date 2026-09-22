//go:build integration

package product_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	valkey "github.com/valkey-io/valkey-go"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/product"
)

func TestSlowValkeyRespectsCallerCancellationAndRecovers(t *testing.T) {
	addr := dbtest.Valkey(t)
	cache := openCacheOnAddr(t, addr, product.DefaultCacheConfig())
	defer cache.Close()
	control, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{addr}, DisableCache: true, ForceSingleClient: true})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if pauseErr := control.Do(t.Context(), control.B().ClientPause().Timeout(1000).All().Build()).Error(); pauseErr != nil {
		t.Fatal(pauseErr)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	id := uuid.New()
	called := false
	started := time.Now()
	_, loadErr := cache.Get(ctx, id, 1, "en", func(context.Context) (product.Presentation, error) {
		called = true
		return product.Presentation{ProductID: id}, nil
	})
	if !errors.Is(loadErr, context.DeadlineExceeded) || called {
		t.Fatalf("cancelled cache operation error=%v fill-called=%t", loadErr, called)
	}
	if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
		t.Fatalf("cancellation took %v, exceeded budget while server was paused", elapsed)
	}
	if pingErr := control.Do(t.Context(), control.B().Ping().Build()).Error(); pingErr != nil {
		t.Fatal(pingErr)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, readErr := cache.Get(t.Context(), id, 1, "en", func(context.Context) (product.Presentation, error) {
			return product.Presentation{ProductID: id, Name: "after pause"}, nil
		})
		if readErr == nil && product.CacheStatsOf(cache).Hits > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("same client did not recover from cancelled operation: %v", readErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
