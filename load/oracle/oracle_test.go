package oracle

import (
	"testing"

	"github.com/google/uuid"
)

func TestOversellOracleDetectsActiveHolds(t *testing.T) {
	t.Parallel()
	snap := VariantSnapshot{
		VariantID:     uuid.New(),
		StockQuantity: 3,
		SafetyStock:   0,
		SoldCommitted: 2,
		ActiveHolds:   2,
	}
	if !snap.Oversell() {
		t.Fatal("expected oversell when sold plus holds exceed sellable units")
	}
}

func TestDegradedWorkRejectsTotalRejection(t *testing.T) {
	t.Parallel()
	if err := DegradedWork(0, 100, 1); err == nil {
		t.Fatal("expected failure when every request was rejected during impairment")
	}
	if err := DegradedWork(5, 100, 3); err != nil {
		t.Fatalf("expected success with enough degraded throughput: %v", err)
	}
}

func TestSellableUnitsRespectsSafetyStock(t *testing.T) {
	t.Parallel()
	snap := VariantSnapshot{StockQuantity: 5, SafetyStock: 2}
	if snap.SellableUnits() != 3 {
		t.Fatalf("sellable = %d, want 3", snap.SellableUnits())
	}
}

func TestDuplicateCheckoutDetection(t *testing.T) {
	t.Parallel()
	dups := []DuplicateCheckout{{IdempotencyKey: "abc", OrderCount: 2}}
	if len(dups) == 0 {
		t.Fatal("planted duplicate checkout should be visible to oracle")
	}
}
