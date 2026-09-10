package loyalty

import "testing"

func TestARedemptionNeverKeepsTheRemainder(t *testing.T) {
	tests := []struct {
		balance    int64
		redeemable int64
		worth      int64
	}{
		{0, 0, 0},
		{99, 0, 0},       // under the minimum
		{100, 100, 1000}, // exactly the minimum: NT$10
		{105, 100, 1000}, // the five stay behind
		{1009, 1000, 10000},
	}
	for _, tt := range tests {
		got := Redeemable(tt.balance)
		if got != tt.redeemable {
			t.Errorf("Redeemable(%d) = %d, want %d", tt.balance, got, tt.redeemable)
		}
		if worth := CreditFor(got); worth != tt.worth {
			t.Errorf("CreditFor(Redeemable(%d)) = %d cents, want %d",
				tt.balance, worth, tt.worth)
		}
	}
}

func TestTheExchangeIsExactInBothDirections(t *testing.T) {
	for points := int64(0); points <= 10_000; points += PointsPerCredit {
		cents := CreditFor(points)
		if want := points / PointsPerCredit * 100; cents != want {
			t.Fatalf("CreditFor(%d) = %d, want %d", points, cents, want)
		}
		if back := cents / 100 * PointsPerCredit; back != points {
			t.Fatalf("%d points became %d cents became %d points", points, cents, back)
		}
	}
}
