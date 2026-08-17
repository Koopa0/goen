package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// PayLine is one item on the payment page, as the order recorded it.
type PayLine struct {
	Name      string
	Label     string
	UnitCents int64
	Quantity  int32
}

// UnitPrice is the agreed price per unit.
func (l PayLine) UnitPrice() string { return twd(l.UnitCents) }

// LineTotal is what the line comes to.
func (l PayLine) LineTotal() string { return twd(l.UnitCents * int64(l.Quantity)) }

// QuantityText is how many were ordered.
func (l PayLine) QuantityText() string { return strconv.FormatInt(int64(l.Quantity), 10) }

// PayView is the page that hands a customer over to the card form.
type PayView struct {
	Number     string
	TotalCents int64
	Email      string
	Lines      []PayLine
	Enabled    bool
	Cancelled  bool
}

// Total is what is owed.
func (v PayView) Total() string { return twd(v.TotalCents) }

// Action is where the form posts; the amount is recomputed server-side.
func (v PayView) Action() string { return "/orders/" + v.Number + "/pay" }

// PayMeta is the chrome view model for the payment page.
func PayMeta(ctx context.Context, number string) layouts.Page {
	return layouts.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeyPayMeta), number)}
}
