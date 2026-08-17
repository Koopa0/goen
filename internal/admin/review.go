package admin

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
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
			At:   r.CreatedAt.Format("2006-01-02 15:04"),
			Slug: r.Slug, Product: r.ProductName, Author: r.Author,
		})
	}
	return view, nil
}

// SetReviewHidden hides a review or puts it back.
//
// Hiding takes it out of the SCORE as well as the list, because every rating is
// computed from visible_reviews. Hide and show are separate calls, never a
// toggle, so a double-submitted form cannot un-hide what was just hidden.
func (s *Store) SetReviewHidden(ctx context.Context, id string, hidden bool) error {
	reviewID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	action := ActionShowReview
	if hidden {
		action = ActionHideReview
	}

	return s.audited(ctx, Event{
		Action: action, Table: "product_reviews", ID: nullableID(reviewID),
		Before: nil,
		// Never the review's own words: audit_events is append-only where
		// product_reviews is not, so a body copied here outlives an erasure.
		After: map[string]any{"review_id": id, "hidden": hidden},
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
				// Already in the state asked for, or gone.
				return ErrNotFound
			}
			return nil
		})
}
