package admin

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Reviews reads the moderation queue, hidden ones included.
func (s *Store) Reviews(ctx context.Context) (pages.AdminReviewsView, error) {
	rows, err := s.q.AdminReviews(ctx, PageSize)
	if err != nil {
		return pages.AdminReviewsView{}, fmt.Errorf("read reviews: %w", err)
	}
	view := pages.AdminReviewsView{Rows: make([]pages.AdminReview, 0, len(rows))}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminReview{
			ID: r.ID.String(), Rating: int(r.Rating), Title: r.Title, Body: r.Body,
			Verified: r.IsVerifiedPurchase, Hidden: r.HiddenAt.Valid,
			At:   shoptime.Minute(r.CreatedAt),
			Slug: r.Slug, Product: r.ProductName, Author: r.Author,
		})
	}
	return view, nil
}

// SetReviewHidden hides a review or puts it back; separate calls, never a toggle.
func (s *Store) SetReviewHidden(ctx context.Context, id string, hidden bool) error {
	reviewID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	action := actionShowReview
	if hidden {
		action = actionHideReview
	}

	return s.audited(ctx, Event{
		Action: action, Table: "product_reviews", ID: nullableID(reviewID),
		Before: nil,
		After:  map[string]any{"review_id": id, "hidden": hidden},
	},
		func(ctx context.Context, q *db.Queries) error {
			var n int64
			var setErr error
			if hidden {
				n, setErr = q.HideReview(ctx, reviewID)
			} else {
				n, setErr = q.ShowReview(ctx, reviewID)
			}
			if setErr != nil {
				return fmt.Errorf("set review hidden: %w", setErr)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}
