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

// LineTotalCents is the goods as recorded. TotalCents is still what is owed.
// The two only match when nothing else was applied.
func (v PayView) LineTotalCents() int64 {
	var n int64
	for _, l := range v.Lines {
		n += l.UnitCents * int64(l.Quantity)
	}
	return n
}

// AdjustmentCents is LineTotalCents minus what is still owed. A positive
// figure is the net reduction the summary can name. The page is given only
// those two totals, so shipping and tax that also sit in TotalCents are
// already netted in and cannot be labelled on their own.
func (v PayView) AdjustmentCents() int64 { return v.LineTotalCents() - v.TotalCents }

// HasAdjustment is the extra summary row that closes the arithmetic when
// owed is below the recorded lines.
func (v PayView) HasAdjustment() bool { return v.AdjustmentCents() > 0 }

// Adjustment is the extra row's amount. Prefixed so it cannot be read as
// another charge sitting under the goods.
func (v PayView) Adjustment() string { return "-" + twd(v.AdjustmentCents()) }

// Total is what is owed.
func (v PayView) Total() string { return twd(v.TotalCents) }

// Action is where the form posts; the amount is recomputed server-side.
func (v PayView) Action() string { return "/orders/" + v.Number + "/pay" }

// PayMeta is the chrome view model for the payment page.
func PayMeta(ctx context.Context, number string) layouts.Page {
	return layouts.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeyPayMeta), number)}
}
