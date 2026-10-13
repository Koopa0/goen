// Package feedback is the back office's read of what customers send in: product
// reviews to hide, questions to answer, and the contact inbox.
package feedback

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type ReviewFilter uint8

const (
	AllReviews ReviewFilter = iota
	ThreeStarsAndBelowReviews
)

func (s *Store) Reviews(ctx context.Context, filter ReviewFilter, after ...string) (admin.ReviewsView, error) {
	if filter != AllReviews && filter != ThreeStarsAndBelowReviews {
		return admin.ReviewsView{}, ErrInvalid
	}
	threeStarsAndBelow := filter == ThreeStarsAndBelowReviews
	scope := "/admin/reviews"
	if threeStarsAndBelow {
		scope = web.ScopeURL(scope, "rating", "3")
	}
	from, resumed := web.ResumeKeyset(scope, after, func(p position) bool { return p.ID != uuid.Nil })
	rows, err := s.q.AdminReviews(ctx, db.AdminReviewsParams{ThreeStarsAndBelow: threeStarsAndBelow, HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, RowLimit: web.PageLimit})
	if err != nil {
		return admin.ReviewsView{}, fmt.Errorf("read reviews: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminReviewsRow) string { return r.PageCursor })
	view := admin.ReviewsView{
		ThreeStarsAndBelow: threeStarsAndBelow,
		Bound:              bound,
		Rows:               make([]admin.Review, 0, len(rows)),
	}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, admin.Review{
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
	action := audit.ActionShowReview
	if hidden {
		action = audit.ActionHideReview
	}

	return audit.Run(ctx, s.pool, audit.Event{
		Action: action, Table: "product_reviews", ID: audit.EntityID(reviewID),
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
