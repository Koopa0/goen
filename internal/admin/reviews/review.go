// Package reviews is the staff desk where product reviews are hidden or shown;
// it holds the list of every review, hidden ones included, its
// three-stars-and-below filter, and hiding and showing one; it does not hold
// questions (admin/questions) or the review form and rules (catalog).
package reviews

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/shoptime"
)

type Filter uint8

const (
	FilterAll Filter = iota
	FilterThreeStarsAndBelow
)

func (s *Store) Reviews(ctx context.Context, filter Filter, after ...string) (View, error) {
	if filter != FilterAll && filter != FilterThreeStarsAndBelow {
		return View{}, ErrInvalid
	}
	threeStarsAndBelow := filter == FilterThreeStarsAndBelow
	scope := "/admin/reviews"
	if threeStarsAndBelow {
		scope = keyset.ScopeURL(scope, "rating", "3")
	}
	from, resumed := keyset.Resume(scope, after, func(p position) bool { return p.ID != uuid.Nil })
	rows, err := s.q.AdminReviews(ctx, db.AdminReviewsParams{ThreeStarsAndBelow: threeStarsAndBelow, HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, RowLimit: keyset.PageLimit})
	if err != nil {
		return View{}, fmt.Errorf("read reviews: %w", err)
	}
	rows, bound := keyset.PageBound(scope, resumed, rows, keyset.PageSize, func(r *db.AdminReviewsRow) string { return r.PageCursor })
	view := View{
		ThreeStarsAndBelow: threeStarsAndBelow,
		Bound:              bound,
		Rows:               make([]Review, 0, len(rows)),
	}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, Review{
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
		After:  audit.State{audit.FieldReviewID: id, audit.FieldHidden: hidden},
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
