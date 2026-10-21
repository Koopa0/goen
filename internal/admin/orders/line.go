package orders

import (
	"strconv"

	"github.com/koopa0/goen/internal/money"
)

// Line is one line of an order as the order page and the picking slips show it:
// what the line is, its price when bought and how many. It carries no warranty:
// the buyer's page draws its own line, with the promise and the registrations.
type Line struct {
	SKU         string
	Name        string
	Label       string
	UnitCents   int64
	Quantity    int32
	ImageURL    string
	ImageSrcset string
	ImageAlt    string
}

func (l Line) HasImage() bool { return l.ImageURL != "" }

func (l Line) UnitPrice() string { return money.TWD(l.UnitCents) }

func (l Line) LineTotal() string { return money.TWD(l.UnitCents * int64(l.Quantity)) }

func (l Line) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }
