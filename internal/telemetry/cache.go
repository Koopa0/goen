package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// CacheOutcome is a bounded cache instrumentation label set shared with #330.
type CacheOutcome string

const (
	CacheHit      CacheOutcome = "hit"
	CacheMiss     CacheOutcome = "miss"
	CacheFill     CacheOutcome = "fill"
	CacheFallback CacheOutcome = "fallback"
)

// CacheDomain names a cache surface without embedding keys or payloads.
type CacheDomain string

const (
	CacheDomainProduct CacheDomain = "product"
	CacheDomainMedia   CacheDomain = "media"
)

var cacheEvents metric.Int64Counter

func initCacheInstruments(m metric.Meter) error {
	var err error
	cacheEvents, err = m.Int64Counter(
		"goen.cache.events",
		metric.WithDescription("Product cache hit, miss, fill and fallback events"),
	)
	if err != nil {
		return fmt.Errorf("cache events counter: %w", err)
	}
	return nil
}

// RecordCacheEvent emits one cache event with bounded labels only.
func RecordCacheEvent(ctx context.Context, domain CacheDomain, outcome CacheOutcome) {
	if cacheEvents == nil {
		return
	}
	cacheEvents.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("cache.domain", string(domain)),
			attribute.String("cache.outcome", string(outcome)),
		),
	)
}
