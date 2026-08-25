package pages

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminReviewsView is the review queue, newest first.
type AdminReviewsView struct {
	Rows   []AdminReview
	Notice string
}

// AdminReview is one review as the back office sees it, hidden ones included.
type AdminReview struct {
	ID       string
	Rating   int
	Title    string
	Body     string
	Verified bool
	Hidden   bool
	At       string
	Slug     string
	Product  string
	Author   string
}

// Empty reports whether nobody has written one yet.
func (v AdminReviewsView) Empty() bool { return len(v.Rows) == 0 }

// Stars is the rating as a reader scans it.
func (r AdminReview) Stars() string { return starsOf(r.Rating) }

// RatingText is the number beside them, for anyone the stars do not reach.
func (r AdminReview) RatingText() string { return strconv.Itoa(r.Rating) }

// DisplayAuthor is who wrote it, or a stand-in for an erased account.
func (r AdminReview) DisplayAuthor(ctx context.Context) string {
	if r.Author == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedAccount)
	}
	return r.Author
}

// Href is the product page it appears on.
func (r AdminReview) Href() string { return "/p/" + r.Slug }

// Action is where the toggle posts; hiding and showing are separate paths.
func (r AdminReview) Action() string {
	if r.Hidden {
		return "/admin/reviews/show"
	}
	return "/admin/reviews/hide"
}

// ActionLabel is what the button says.
func (r AdminReview) ActionLabel(ctx context.Context) string {
	if r.Hidden {
		return i18n.T(ctx, i18n.KeyAdminReviewShow)
	}
	return i18n.T(ctx, i18n.KeyAdminReviewHide)
}
