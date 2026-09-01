// Package pickup owns the stable wire identities of convenience-store chains
// that can receive an order. Human labels belong to the presentation layer;
// provider/store details belong to the order that selected one.
package pickup

import "slices"

// Brand is the value persisted in order_private_data.pickup_brand and carried
// by checkout and back-office forms. It is not a display name.
type Brand string

const (
	// SevenEleven is 7-ELEVEN Taiwan.
	SevenEleven Brand = "seven_eleven"
	// FamilyMart is FamilyMart Taiwan.
	FamilyMart Brand = "family_mart"
	// HiLife is Hi-Life Taiwan.
	HiLife Brand = "hi_life"
	// OKMart is OK mart Taiwan.
	OKMart Brand = "ok_mart"
)

var offered = [...]Brand{
	SevenEleven,
	FamilyMart,
	HiLife,
	OKMart,
}

// Offered returns the brands checkout supports, in display order. The result
// owns its storage, so a caller cannot mutate the canonical closed set.
func Offered() []Brand { return slices.Clone(offered[:]) }

// Known reports whether b is a brand this shop can accept for pickup.
func (b Brand) Known() bool {
	for _, candidate := range offered {
		if b == candidate {
			return true
		}
	}
	return false
}
