package cart

import (
	"strconv"

	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/money"
)

// Line is one line of the cart as the cart page and checkout's summary draw it:
// the variant, its price today and how much of it can still be sold.
type Line struct {
	VariantID    string
	Slug         string
	Name         string
	Brand        string
	SKU          string
	Label        string
	UnitCents    int64
	CompareCents int64
	Quantity     int32
	Available    int32

	// The three flags sit together so the struct packs them into one word.
	TaxExempt   bool
	Unavailable bool
	Short       bool

	// Sale is when the first listed campaign featuring the product ends, set
	// only where campaign.OnSale holds for the line: the campaign runs, the line
	// can be bought and its compare-at price is the higher.
	Sale *campaign.End

	ImageURL    string
	ImageSrcset string
	ImageAlt    string
}

func (l Line) UnitPrice() string { return money.TWD(l.UnitCents) }

// OnSale reports that the line is priced by a running campaign.
func (l Line) OnSale() bool { return l.Sale != nil }

// Compare is the struck original price of a line on sale.
func (l Line) Compare() string { return money.TWD(l.CompareCents) }

func (l Line) LineTotal() string { return money.TWD(l.UnitCents * int64(l.effectiveQuantity())) }

func (l Line) effectiveQuantity() int32 {
	if l.Unavailable {
		return 0
	}
	if l.Short {
		return l.Available
	}
	return l.Quantity
}

func (l Line) PricedQuantityText() string {
	return strconv.FormatInt(int64(l.effectiveQuantity()), 10)
}

func (l Line) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

func (l Line) AvailableText() string { return strconv.FormatInt(int64(l.Available), 10) }

func (l Line) MaxQuantity() string {
	n := max(min(l.Available, 999), 1)
	return strconv.FormatInt(int64(n), 10)
}

func (l Line) HasImage() bool { return l.ImageURL != "" }
