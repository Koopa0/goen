// Package money renders amounts in the shop's currency.
//
// It is a package rather than a helper because two callers that cannot import
// each other both print prices: internal/ui/pages and internal/email, where the
// edge would close a cycle through internal/invoice. Left apart they grew two
// implementations that disagreed about a negative amount.
package money

import (
	"strconv"
	"strings"
)

// TWD renders cents as New Taiwan dollars: 149_900 is NT$1,499, and a negative
// amount is -NT$200 rather than NT$-200.
func TWD(cents int64) string {
	// Divided before the sign is taken: negating first overflows on the most
	// negative int64 and leaves a minus sign for the grouping loop to read as a
	// digit.
	dollars := cents / 100
	negative := dollars < 0
	if negative {
		dollars = -dollars
	}
	digits := strconv.FormatInt(dollars, 10)

	var b strings.Builder
	b.Grow(len(digits) + len(digits)/3 + len("-NT$"))
	if negative {
		b.WriteByte('-')
	}
	b.WriteString("NT$")
	for i := range len(digits) { // digits is ASCII
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(digits[i])
	}
	return b.String()
}
