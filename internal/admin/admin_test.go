package admin

import "testing"

// TestAReceiptIsAlwaysPositive holds the line between the two stock doors.
//
// 進貨 and 人工調整 write the same ledger with different reasons, and the reason
// is the only thing that tells "twelve arrived from the supplier" from "we had
// counted wrong". That distinction survives only if a receipt cannot be used to
// take stock AWAY: a shop that could type -3 into the 進貨 box would be filing
// corrections as deliveries, and the ledger would be back where it was before
// this door existed — which is to say, unable to answer 「這個為什麼是四」.
//
// inventory_movements_delta_direction refuses a negative receipt at the database
// and is the authority. This exists so the refusal is a form somebody can fix
// rather than a constraint name, and it is deliberately NOT skipped in the
// store: a caller that reaches record_inventory_movement without passing through
// here still meets the CHECK.
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
		// Everything below is refused, and each is a different mistake.
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
