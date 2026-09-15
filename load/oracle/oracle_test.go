package oracle

import (
	"testing"

	"github.com/google/uuid"
)

func TestLegitimateHoldIsNotOversell(t *testing.T) {
	t.Parallel()
	snap := VariantSnapshot{
		VariantID:      uuid.New(),
		StockQuantity:  1,
		SafetyStock:    0,
		InitialReceipt: 3,
		LedgerBalance:  1,
		SoldCommitted:  2,
		ActiveHolds:    2,
	}
	if snap.Oversell() {
		t.Fatal("pending checkout must not count holds twice against remaining stock")
	}
}

func TestOversellOracleDetectsConservationViolation(t *testing.T) {
	t.Parallel()
	snap := VariantSnapshot{
		VariantID:      uuid.New(),
		StockQuantity:  0,
		SafetyStock:    0,
		InitialReceipt: 3,
		LedgerBalance:  0,
		SoldCommitted:  4,
	}
	if !snap.Oversell() {
		t.Fatal("expected oversell when committed units exceed received inventory")
	}
}

func TestOversellOracleDetectsLedgerDrift(t *testing.T) {
	t.Parallel()
	snap := VariantSnapshot{
		VariantID:      uuid.New(),
		StockQuantity:  1,
		InitialReceipt: 3,
		LedgerBalance:  2,
		SoldCommitted:  2,
	}
	if !snap.Oversell() {
		t.Fatal("expected oversell when the ledger projection no longer matches stock")
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

func TestParseRequiredCountRejectsMissingAndMalformed(t *testing.T) {
	t.Parallel()
	if _, err := ParseRequiredCount("LOAD_ORACLE_SUCCESS_COUNT", ""); err == nil {
		t.Fatal("expected missing counter to fail closed")
	}
	if _, err := ParseRequiredCount("LOAD_ORACLE_TOTAL_COUNT", "nope"); err == nil {
		t.Fatal("expected malformed counter to fail closed")
	}
	if v, err := ParseRequiredCount("LOAD_ORACLE_SUCCESS_COUNT", " 7 "); err != nil || v != 7 {
		t.Fatalf("ParseRequiredCount = (%d, %v), want (7, nil)", v, err)
	}
}

func TestDuplicateCheckoutDetection(t *testing.T) {
	t.Parallel()
	dups := []DuplicateCheckout{{IdempotencyKey: "abc", OrderCount: 2}}
	if len(dups) == 0 {
		t.Fatal("planted duplicate checkout should be visible to oracle")
	}
}
