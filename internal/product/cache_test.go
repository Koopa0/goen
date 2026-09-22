package product

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDisabledCacheReadsThroughFill(t *testing.T) {
	cache, err := OpenPresentationCache("", DefaultCacheConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cache.Close()
	if cache.Enabled() {
		t.Fatal("empty address should disable cache")
	}
	want := Presentation{ProductID: uuid.New(), Name: "probe"}
	got, err := cache.Get(t.Context(), want.ProductID, 1, "zh-Hant",
		func(context.Context) (Presentation, error) { return want, nil })
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != want.Name {
		t.Fatalf("got %q, want %q", got.Name, want.Name)
	}
}

// The socket poller can report its deadline before the context timer sets Err.
type pendingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (ctx pendingDeadlineContext) Deadline() (time.Time, bool) { return ctx.deadline, true }

func TestFallbackRefusesElapsedDeadlineBeforeContextTimer(t *testing.T) {
	for _, elapsed := range []bool{false, true} {
		name := "work budget remains"
		deadline := time.Now().Add(time.Minute)
		if elapsed {
			name = "deadline elapsed before Err"
			deadline = time.Now().Add(-time.Millisecond)
		}
		t.Run(name, func(t *testing.T) {
			ctx := pendingDeadlineContext{Context: t.Context(), deadline: deadline}
			if ctx.Err() != nil {
				t.Fatal("fixture requires pending context timer")
			}
			cache := &PresentationCache{metrics: &CacheMetrics{}, maxFallback: 1}
			called := false
			want := Presentation{ProductID: uuid.New(), Name: "fresh"}
			got, err := cache.fallback(ctx, func(context.Context) (Presentation, error) { called = true; return want, nil })
			if elapsed {
				if !errors.Is(err, context.DeadlineExceeded) || called || cache.metrics.Fallbacks.Load() != 0 {
					t.Fatalf("expired work: error=%v fill=%t admissions=%d", err, called, cache.metrics.Fallbacks.Load())
				}
			} else if err != nil || !called || got.ProductID != want.ProductID {
				t.Fatalf("remaining work: error=%v fill=%t result=%+v", err, called, got)
			}
		})
	}
}
