package loyalty

import (
	"errors"
	"math"
	"testing"
)

func TestTierDollarAndPercentOverflowIsRefused(t *testing.T) {
	t.Parallel()
	if err := (&Store{}).CreateTier(t.Context(), "overflow", "Overflow", "",
		math.MaxInt64, math.MaxInt64); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tier dollar/percent overflow = %v, want ErrInvalid", err)
	}
}
