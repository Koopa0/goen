package pages

import "strconv"

type CartLine struct {
	VariantID    string
	Slug         string
	Name         string
	Brand        string
	SKU          string
	Label        string
	TaxExempt    bool
	UnitCents    int64
	CompareCents int64
	Quantity     int32
	Available    int32

	Unavailable bool
	Short       bool

	ImageURL    string
	ImageSrcset string
	ImageAlt    string
}

func (l CartLine) UnitPrice() string { return twd(l.UnitCents) }

func (l CartLine) LineTotal() string { return twd(l.UnitCents * int64(l.effectiveQuantity())) }

func (l CartLine) effectiveQuantity() int32 {
	if l.Unavailable {
		return 0
	}
	if l.Short {
		return l.Available
	}
	return l.Quantity
}

func (l CartLine) PricedQuantityText() string {
	return strconv.FormatInt(int64(l.effectiveQuantity()), 10)
}

func (l CartLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

func (l CartLine) AvailableText() string { return strconv.FormatInt(int64(l.Available), 10) }

func (l CartLine) MaxQuantity() string {
	n := max(min(l.Available, 999), 1)
	return strconv.FormatInt(int64(n), 10)
}

func (l CartLine) HasImage() bool { return l.ImageURL != "" }
