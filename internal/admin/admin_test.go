package admin

import "testing"

// TestAReceiptIsAlwaysPositive proves a receipt cannot take stock away, which is
// what keeps a delivery distinguishable from a correction.
func TestAReceiptIsAlwaysPositive(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  int32
		wantY bool
	}{
		{name: "an ordinary delivery", in: "12", want: 12, wantY: true},
		{name: "one unit", in: "1", want: 1, wantY: true},
		{name: "surrounding space is not a typo worth refusing", in: "  8 ", want: 8, wantY: true},
		{name: "the ceiling", in: "10000", want: 10000, wantY: true},
		{name: "a correction typed into the receipt box", in: "-3"},
		{name: "nothing arrived is not a delivery", in: "0"},
		{name: "a warehouse invented by a typo", in: "10001"},
		{name: "empty", in: ""},
		{name: "not a number", in: "十二"},
		{name: "a fraction of a unit", in: "1.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseReceipt(tt.in)
			if ok != tt.wantY || got != tt.want {
				t.Errorf("ParseReceipt(%q) = %d, %v, want %d, %v",
					tt.in, got, ok, tt.want, tt.wantY)
			}
		})
	}
}
