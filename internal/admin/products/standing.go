package products

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/chart"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// salesWeeks is how many whole weeks of sales the editor shows, the last of
// them ending today.
const salesWeeks = 13

// ErrStanding is returned with a complete product whose sales or reviews could
// not be read: the view marks what is missing and is otherwise whole.
var ErrStanding = errors.New("read sales and reviews")

// standing reads what the product sold and what its reviewers said, each
// failing alone: the page is the product's editor first.
func (s *Store) standing(ctx context.Context, id uuid.UUID, now time.Time, view *admin.ProductView) error {
	sales, salesErr := s.sales(ctx, id, now)
	view.Sales = sales
	view.Sales.Unavailable = salesErr != nil

	// ProductRating is what the product page counts its reviews with, so the two
	// pages cannot disagree about which reviews are visible.
	rating, ratingErr := s.q.ProductRating(ctx, id)
	if ratingErr == nil {
		view.Ratings = admin.ProductRatings{
			Count: rating.RatingCount, Average: rating.Rating,
			Stars: [5]int64{rating.Five, rating.Four, rating.Three, rating.Two, rating.One},
		}
	} else {
		view.Ratings.Unavailable = true
	}
	if err := errors.Join(salesErr, ratingErr); err != nil {
		return fmt.Errorf("%w: %w", ErrStanding, err)
	}
	return nil
}

// sales is a bucket for each shop day of the last salesWeeks weeks, counted up
// to now.
func (s *Store) sales(ctx context.Context, id uuid.UUID, now time.Time) (admin.ProductSales, error) {
	from := shoptime.Midnight(now).AddDate(0, 0, -salesWeeks*7)
	rows, err := s.q.ProductUnitsByShopDay(ctx, db.ProductUnitsByShopDayParams{
		ProductID: id, FromAt: from, ToAt: now,
	})
	if err != nil {
		return admin.ProductSales{}, fmt.Errorf("read units by day: %w", err)
	}
	days := chart.Series{Partial: true, Buckets: make([]chart.Bucket, 0, len(rows))}
	for _, r := range rows {
		days.Buckets = append(days.Buckets, chart.Bucket{Day: r.Day, Value: r.Units})
	}
	return admin.ProductSales{Days: days, Cut: shoptime.Clock(now)}, nil
}
