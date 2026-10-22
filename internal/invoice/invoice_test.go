package invoice

import "testing"

// TestValidTaxID checks that ValidTaxID uses the current Ministry of Finance
// checksum.
func TestValidTaxID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  bool
	}{
		{value: "04595257", want: true},  // legacy /10-compatible number
		{value: "04595252", want: true},  // current /5-only example
		{value: "10458574", want: true},  // seventh digit 7, contribution 1
		{value: "10458570", want: true},  // seventh digit 7, contribution 0
		{value: "00000000", want: false}, // checksum passes but MOF forbids all-zero
		{value: "12345678", want: false},
		{value: "1045857x", want: false},
		{value: "0459525", want: false},
	}
	for _, tt := range tests {
		if got := ValidTaxID(tt.value); got != tt.want {
			t.Errorf("ValidTaxID(%q) = %t, want %t", tt.value, got, tt.want)
		}
	}
}
