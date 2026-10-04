// Package coupon holds the closed set of what a coupon takes off an order.
package coupon

// Kind is coupons.kind.
type Kind string

const (
	Amount       Kind = "amount"
	Percent      Kind = "percent"
	FreeShipping Kind = "free_shipping"
)

// Parse turns a column's or form's value into a Kind, refusing an unknown one.
func Parse(s string) (Kind, bool) {
	switch k := Kind(s); k {
	case Amount, Percent, FreeShipping:
		return k, true
	}
	return "", false
}
