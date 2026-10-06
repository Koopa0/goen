package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/chart"
)

// ProductSales is the units a product sold on each shop day of the last 13
// weeks, today last and counted up to Cut.
type ProductSales struct {
	Days chart.Series
	Cut  string
	// Unavailable is set when the days could not be read, so the section says so
	// rather than read as a product that did not sell.
	Unavailable bool
}

// ProductRatings is the visible reviews of a product: how many there are, their
// average, and how many gave each star, from five stars to one.
type ProductRatings struct {
	Count       int64
	Average     float64
	Stars       [5]int64
	Unavailable bool
}

// MinReviewsForSpread is the fewest reviews a spread is drawn for; below it they
// are told one by one.
const MinReviewsForSpread = 5

// ShowsUnitColumns reports whether the days with sales are enough to draw
// columns; fewer are told in a sentence.
func (s ProductSales) ShowsUnitColumns() bool {
	return s.Days.Density() >= chart.DensitySparse
}

// UnitColumns is the chart of the weeks, each of seven days counted back from
// today.
func (s ProductSales) UnitColumns(ctx context.Context) chart.ColumnsProps {
	days := s.Days
	days.Label = i18n.T(ctx, i18n.KeyAdminProdUnitsSold)
	return chart.ColumnsProps{
		Series:       days,
		Caption:      s.Sentence(ctx),
		Note:         fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepPaidNote), s.Cut),
		DayHeading:   i18n.T(ctx, i18n.KeyAdminRepFromDay),
		PartialLabel: fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepUntil), s.Cut),
	}
}

// Sentence says what the days sold: the caption of the columns, or, when there
// are too few days to draw them, all there is to say.
func (s ProductSales) Sentence(ctx context.Context) string {
	buckets := s.Days.Buckets
	if len(buckets) == 0 {
		return ""
	}
	period := dayText(ctx, dayOf(buckets[0].Day)) + "–" + dayText(ctx, dayOf(buckets[len(buckets)-1].Day))
	var withSales []chart.Bucket
	for _, b := range buckets {
		if b.Value != 0 {
			withSales = append(withSales, b)
		}
	}
	units := func(n int64) string { return i18n.Count(ctx, i18n.KeyAdminProdUnitsCount, n, n) }
	switch s.Days.Density() {
	case chart.DensityNone:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminProdUnitsNone), period)
	case chart.DensityFew:
		items := make([]string, len(withSales))
		for i, b := range withSales {
			items[i] = i18n.Count(ctx, i18n.KeyAdminProdUnitsFewDay, b.Value, dayText(ctx, dayOf(b.Day)), b.Value)
		}
		return i18n.Count(ctx, i18n.KeyAdminProdUnitsFewDays, int64(len(withSales)),
			period, len(withSales), strings.Join(items, i18n.T(ctx, i18n.KeyChartListSeparator)))
	case chart.DensitySparse:
		return i18n.Count(ctx, i18n.KeyAdminProdUnitsSparse, int64(len(withSales)), len(withSales))
	}
	peaks := chart.Peaks(s.Days.Columns())
	if len(peaks) == 1 {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminProdUnitsBestWeek), units(peaks[0].Value), dayText(ctx, dayOf(peaks[0].Day)))
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminProdUnitsBestWeeks), len(peaks), units(peaks[0].Value))
}

// ShowsSpread reports whether there are reviews enough to draw the spread.
func (r ProductRatings) ShowsSpread() bool { return r.Count >= MinReviewsForSpread }

// Sentence says how the reviews stand: the lead of the spread, or, under five
// reviews, the reviews that were given.
func (r ProductRatings) Sentence(ctx context.Context) string {
	count := i18n.Count(ctx, i18n.KeyReviewCount, r.Count, strconv.FormatInt(r.Count, 10))
	switch {
	case r.Count == 0:
		return i18n.T(ctx, i18n.KeyAdminProdRatingsNone)
	case r.ShowsSpread():
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminProdRatingsAverage), count, strconv.FormatFloat(r.Average, 'f', 1, 64))
	}
	var given []string
	for i, n := range r.Stars {
		if n > 0 {
			given = append(given, fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminProdRatingsGiven), 5-i, n))
		}
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminProdRatingsFew), count, strings.Join(given, i18n.T(ctx, i18n.KeyChartListSeparator)))
}

// RatingRow is one star of the spread.
type RatingRow struct {
	Stars int
	Count int64
}

// Rows are the stars from five to one, each with the bar's scale: the most
// reviews any star has.
func (r ProductRatings) Rows() (rows []RatingRow, most int64) {
	for i, n := range r.Stars {
		rows = append(rows, RatingRow{Stars: 5 - i, Count: n})
		most = max(most, n)
	}
	return rows, most
}

// StarsText is the row's head: "5 stars".
func (row RatingRow) StarsText(ctx context.Context) string {
	return i18n.Count(ctx, i18n.KeyStarsLabel, int64(row.Stars), strconv.Itoa(row.Stars))
}

// CountText is the row's count, which is also its bar's label.
func (row RatingRow) CountText() string { return strconv.FormatInt(row.Count, 10) }
