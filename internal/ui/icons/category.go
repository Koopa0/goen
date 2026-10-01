// Package icons renders the repository-owned SVG icon vocabulary.
package icons

const (
	categoryPhone      = "phone"
	categoryLaptop     = "laptop"
	categoryTablet     = "tablet"
	categoryHeadphones = "headphones"
	categoryWatch      = "watch"
	categoryPlug       = "plug"
	categoryShield     = "shield"
	categoryBook       = "book"
	categoryStationery = "stationery"
	categoryHome       = "home"
	categoryKitchen    = "kitchen"
	categoryFood       = "food"
	categoryDrink      = "drink"
	categoryBeauty     = "beauty"
	categoryApparel    = "apparel"
	categoryKids       = "kids"
	categoryGift       = "gift"
)

// categoryKeys is the closed set Category renders, in picker order.
var categoryKeys = [...]string{
	categoryPhone,
	categoryLaptop,
	categoryTablet,
	categoryHeadphones,
	categoryWatch,
	categoryPlug,
	categoryShield,
	categoryBook,
	categoryStationery,
	categoryHome,
	categoryKitchen,
	categoryFood,
	categoryDrink,
	categoryBeauty,
	categoryApparel,
	categoryKids,
	categoryGift,
}

// CategoryKeys returns the keys Category can render, in picker order. Each
// call returns independent storage.
func CategoryKeys() []string {
	out := make([]string, len(categoryKeys))
	copy(out, categoryKeys[:])
	return out
}

// KnownCategory reports whether key names a glyph Category can render.
func KnownCategory(key string) bool {
	for _, known := range categoryKeys {
		if key == known {
			return true
		}
	}
	return false
}
