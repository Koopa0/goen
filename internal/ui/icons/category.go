// Package icons holds goen's inline SVG icons.
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

// categoryKeys is in picker order.
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

// CategoryKeys returns a fresh slice on every call.
func CategoryKeys() []string {
	out := make([]string, len(categoryKeys))
	copy(out, categoryKeys[:])
	return out
}

func KnownCategory(key string) bool {
	for _, known := range categoryKeys {
		if key == known {
			return true
		}
	}
	return false
}
