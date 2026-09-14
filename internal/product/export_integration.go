//go:build integration

package product

import "context"

// Answer exposes the customer-answer fixture seam to integration tests only.
// No production route currently owns this operation.
func (s *Store) Answer(ctx context.Context, questionID, userID, body string) error {
	return s.answer(ctx, questionID, userID, body)
}

// CacheStats is a point-in-time presentation-cache counter set.
type CacheStats struct {
	Hits, Misses, Errors, Fills, Coalesced, Fallbacks uint64
}

// CacheStatsOf reads presentation-cache counters for acceptance tests.
func CacheStatsOf(c *PresentationCache) CacheStats {
	if c == nil || c.metrics == nil {
		return CacheStats{}
	}
	return CacheStats{
		Hits:      c.metrics.Hits.Load(),
		Misses:    c.metrics.Misses.Load(),
		Errors:    c.metrics.Errors.Load(),
		Fills:     c.metrics.Fills.Load(),
		Coalesced: c.metrics.Coalesced.Load(),
		Fallbacks: c.metrics.Fallbacks.Load(),
	}
}
