package loyalty

import "testing"

// TestPointsAreEarnedOnWholeHundredsOnly proves the programme cannot be gamed
// by splitting a basket.
//
// The fraction is dropped rather than rounded. Rounding up would let a NT$50
// order earn a point, and a programme that pays out on the smallest possible
// purchase is one people split their basket to game.
func TestPointsAreEarnedOnWholeHundredsOnly(t *testing.T) {
	tests := []struct {
		cents int64
		want  int64
	}{
		{0, 0},
		{-100, 0},
		{9999, 0},      // NT$99.99
		{10000, 1},     // NT$100
		{19999, 1},     // NT$199.99 — one point, not two
		{100000, 10},   // NT$1,000
		{2590000, 259}, // NT$25,900
	}
	for _, tt := range tests {
		if got := PointsFor(tt.cents); got != tt.want {
			t.Errorf("PointsFor(%d) = %d, want %d", tt.cents, got, tt.want)
		}
	}
}

// TestARedemptionNeverKeepsTheRemainder proves no points disappear in the
// exchange.
//
// Redeeming 105 points at ten to the dollar must not take 105 and return NT$10,
// quietly keeping five. The remainder stays where the customer can see it.
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

// TestTheExchangeIsExactInBothDirections proves the rate creates and destroys
// nothing.
//
// Points in, cents out, and no value created or destroyed on the way: a rate
// that lost a cent per redemption would be a rounding error the shop keeps, and
// one that gained a cent would be one it pays.
func TestTheExchangeIsExactInBothDirections(t *testing.T) {
	for points := int64(0); points <= 10_000; points += PointsPerCredit {
		cents := CreditFor(points)
		if want := points / PointsPerCredit * 100; cents != want {
			t.Fatalf("CreditFor(%d) = %d, want %d", points, cents, want)
		}
		// A whole exchange converts back to exactly the points it cost.
		if back := cents / 100 * PointsPerCredit; back != points {
			t.Fatalf("%d points became %d cents became %d points", points, cents, back)
		}
	}
}
