package tiers

import (
	"errors"
	"math"
	"testing"
)

// TestStore_CreateTierOverflow: a tier whose dollar and percent amounts overflow is refused.
func TestStore_CreateTierOverflow(t *testing.T) {
	t.Parallel()
	if err := (&Store{}).CreateTier(t.Context(), "overflow", "Overflow", "",
		math.MaxInt64, math.MaxInt64); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tier dollar/percent overflow = %v, want ErrInvalid", err)
	}
}
