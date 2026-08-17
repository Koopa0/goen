package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/ui/layouts"

	"github.com/koopa0/goen/internal/i18n"
)

// ReturnsLine is one order line the customer may send back.
type ReturnsLine struct {
	ID         string
	SKU        string
	Name       string
	Label      string
	UnitCents  int64
	Returnable int32
	// Chosen is what was submitted, carried back when a refusal re-renders the
	// form.
	Chosen int32
}

// CanReturn reports whether any of this line is still returnable.
func (l ReturnsLine) CanReturn() bool { return l.Returnable > 0 }

// UnitPrice is the agreed price per unit.
func (l ReturnsLine) UnitPrice() string { return twd(l.UnitCents) }

// Field is the form field this line's quantity is submitted under.
func (l ReturnsLine) Field() string { return "qty_" + l.ID }

// Max is the highest quantity the form will accept for this line.
func (l ReturnsLine) Max() string { return strconv.FormatInt(int64(l.Returnable), 10) }

// ChosenText is what to put in the input's value.
func (l ReturnsLine) ChosenText() string {
	if l.Chosen == 0 {
		return ""
	}
	return strconv.FormatInt(int64(l.Chosen), 10)
}

// ReturnsExisting is a request already filed against this order.
type ReturnsExisting struct {
	StatusText string
	Reason     string
	Resolution string
	CreatedAt  string
	DecidedAt  string
}

// ReturnsView is the return form.
type ReturnsView struct {
	Number   string
	Reason   string
	Lines    []ReturnsLine
	Existing []ReturnsExisting
	HasOpen  bool
	Error    string
}

// Action is where the form posts.
func (v ReturnsView) Action() string { return "/orders/" + v.Number + "/return" }

// AnyReturnable reports whether the form has anything to offer.
func (v ReturnsView) AnyReturnable() bool {
	for _, l := range v.Lines {
		if l.CanReturn() {
			return true
		}
	}
	return false
}

// ReturnsMeta is the chrome view model for the return page.
func ReturnsMeta(ctx context.Context, number string) layouts.Page {
	return layouts.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeyReturnMeta), number)}
}
