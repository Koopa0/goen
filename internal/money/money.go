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

// MaxCents is the ceiling every money column carries.
const MaxCents = 10000000000

// ParsePositiveDollars reads a whole-dollar amount typed in a form and returns it
// as cents. The bound is checked on the dollars, before the multiplication, so
// no input can overflow it.
func ParsePositiveDollars(raw string, maxCents int64) (int64, bool) {
	dollars, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || dollars <= 0 || dollars > maxCents/100 {
		return 0, false
	}
	return dollars * 100, true
}

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
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	b.WriteString("NT$")
	b.WriteString(grouped(dollars))
	return b.String()
}

// grouped is a non-negative whole number with a comma between thousands.
func grouped(n int64) string {
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	b.Grow(len(digits) + len(digits)/3)
	for i := range len(digits) { // digits is ASCII
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(digits[i])
	}
	return b.String()
}

// Short is the digits of a non-negative amount in whole dollars counted in
// divisor dollars, with one decimal when it is not whole: 25_000_000 cents over
// a divisor of 10_000 is "25", and 12_500_000 is "12.5". The unit's name is the
// caller's, since it depends on the language.
func Short(cents, divisor int64) string {
	dollars := cents / 100
	whole, rest := dollars/divisor, dollars%divisor
	if tenths := rest * 10 / divisor; tenths > 0 {
		return grouped(whole) + "." + strconv.FormatInt(tenths, 10)
	}
	return grouped(whole)
}

// ParseDollars reads a whole-dollar price typed in a form and returns cents. A
// blank field is zero, which a compare-at price reads as "not on sale"; anything
// unreadable, negative or past the schema's ceiling is not ok.
func ParseDollars(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > MaxCents/100 {
		return 0, false
	}
	return n * 100, true
}
