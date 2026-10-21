package buyer

import (
	"strconv"
	"time"

	"github.com/koopa0/goen/internal/money"
)

// OrderLine is one line of the order as the buyer's page shows it, with the
// warranty the line carries and how much of it is registered.
type OrderLine struct {
	SKU         string
	Name        string
	Label       string
	UnitCents   int64
	Quantity    int32
	ImageURL    string
	ImageSrcset string
	ImageAlt    string
	// WarrantyMonths is the promise the line copied at checkout; zero when it carried none.
	WarrantyMonths int
	// Registered counts the units this line's share has registered, and WarrantyUntil is the day their cover ends.
	Registered    int
	WarrantyUntil time.Time
}

func (l OrderLine) HasImage() bool { return l.ImageURL != "" }

func (l OrderLine) UnitPrice() string { return money.TWD(l.UnitCents) }

func (l OrderLine) LineTotal() string { return money.TWD(l.UnitCents * int64(l.Quantity)) }

func (l OrderLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }
