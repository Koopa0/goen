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

var knownChains = [...]Chain{
	SevenEleven,
	FamilyMart,
	HiLife,
	OKMart,
}

// KnownChains gives callers their own storage so they cannot mutate the canonical closed set.
func KnownChains() []Chain { return slices.Clone(knownChains[:]) }

func (c Chain) Known() bool {
	for _, candidate := range knownChains {
		if c == candidate {
			return true
		}
	}
	return false
}

// AtCheckout excludes historical chains whose store picker checkout does not integrate.
func (c Chain) AtCheckout() bool {
	return c == SevenEleven || c == FamilyMart
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
