package shipping

import "slices"

// Chain is the value persisted in order_private_data.pickup_chain and carried
// by checkout and back-office forms. It is not a display name.
type Chain string

const (
	ChainSevenEleven Chain = "seven_eleven"
	ChainFamilyMart  Chain = "family_mart"
	ChainHiLife      Chain = "hi_life"
	ChainOKMart      Chain = "ok_mart"
)

var chainSet = [...]Chain{
	ChainSevenEleven,
	ChainFamilyMart,
	ChainHiLife,
	ChainOKMart,
}

// Chains gives callers their own storage so they cannot mutate the canonical closed set.
func Chains() []Chain { return slices.Clone(chainSet[:]) }

// Known reports whether c is one of the chains.
func (c Chain) Known() bool {
	for _, candidate := range chainSet {
		if c == candidate {
			return true
		}
	}
	return false
}

// AtCheckout excludes historical chains whose store picker checkout does not integrate.
func (c Chain) AtCheckout() bool {
	return c == ChainSevenEleven || c == ChainFamilyMart
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
