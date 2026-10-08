package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// ReturnedProduct is the units of one product on returns, against the units
// sold of it, over the same orders.
type ReturnedProduct struct {
	Slug     string
	Name     string
	Brand    string
	Returned int64
	Sold     int64
}

func (p ReturnedProduct) Href() string { return "/admin/products/" + p.Slug }

func (p ReturnedProduct) ReturnedText() string { return strconv.FormatInt(p.Returned, 10) }

// Counts is "3 / 12 units".
func (p ReturnedProduct) Counts(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyAdminRepReturnedCounts, p.Sold,
		p.ReturnedText(), strconv.FormatInt(p.Sold, 10))
}

// minUnitsForRate is the fewest units sold before a returned percentage is
// shown: below it one more return moves the rate by more than five points.
const minUnitsForRate = 20

func (p ReturnedProduct) ShowsRate() bool { return p.Sold >= minUnitsForRate }

// Share is the returned percentage, rounded half up, never 0% or 100% unless
// none or all came back.
func (p ReturnedProduct) Share(ctx context.Context) string {
	rate := (p.Returned*200 + p.Sold) / (p.Sold * 2)
	if p.Returned > 0 && p.Returned < p.Sold {
		rate = min(max(rate, 1), 99)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepReturnedShare), strconv.FormatInt(rate, 10)+"%")
}

// Figure is what the row says in place of a bar: the counts, or the rate once
// the sale is large enough for one.
func (p ReturnedProduct) Figure(ctx context.Context) string {
	if p.ShowsRate() {
		return p.Share(ctx)
	}
	return p.Counts(ctx)
}

// ReturnedOne is the sentence that stands where a single product's bar would.
func (v *ReportView) ReturnedOne(ctx context.Context) string {
	p := v.Returned[0]
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepReturnedOne), p.Name, p.Counts(ctx))
}
