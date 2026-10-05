// Package pickup owns the stable wire identities of convenience-store chains
// that can receive an order, never their display names.
package pickup

import "slices"

// Chain is the value persisted in order_private_data.pickup_chain and carried
// by checkout and back-office forms. It is not a display name.
type Chain string

const (
	SevenEleven Chain = "seven_eleven"
	FamilyMart  Chain = "family_mart"
	HiLife      Chain = "hi_life"
	OKMart      Chain = "ok_mart"
)

var offered = [...]Chain{
	SevenEleven,
	FamilyMart,
	HiLife,
	OKMart,
}

// Offered returns every chain an order can carry, in display order: the back
// office renders and corrects orders placed at chains checkout no longer takes. The result
// owns its storage, so a caller cannot mutate the canonical closed set.
func Offered() []Chain { return slices.Clone(offered[:]) }

func (c Chain) Known() bool {
	for _, candidate := range offered {
		if c == candidate {
			return true
		}
	}
	return false
}

// maxStoreCodeLen is ECPay's published pickup-point store code length, not any
// one chain's width.
const maxStoreCodeLen = 10

// ValidStoreCode reports whether s is a convenience-store number: digits or upper
// case, never digits alone — Hi-Life leads 149 of its 1,350 store codes with a
// letter (ECPay GetStoreList, 2026-08-06).
func ValidStoreCode(s string) bool {
	if s == "" || len(s) > maxStoreCodeLen {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}
