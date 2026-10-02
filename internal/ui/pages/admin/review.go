package admin

import (
	"context"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// ReviewsView is the review queue, newest first.
type ReviewsView struct {
	pages.ListBound

	Rows   []Review
	Notice string
}

// Review is one review as the back office sees it, hidden ones included.
type Review struct {
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
func (v ReviewsView) Empty() bool { return len(v.Rows) == 0 }

// Stars is the rating as a reader scans it.
func (r Review) Stars() string {
	n := max(0, min(r.Rating, 5))
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

// RatingText is the number beside them, for anyone the stars do not reach.
func (r Review) RatingText() string { return strconv.Itoa(r.Rating) }

// DisplayAuthor is who wrote it, or a stand-in for an erased account.
func (r Review) DisplayAuthor(ctx context.Context) string {
	if r.Author == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedAccount)
	}
	return r.Author
}

// Href is the product page it appears on.
func (r Review) Href() string { return "/p/" + r.Slug }

// Action is where the toggle posts; hiding and showing are separate paths.
func (r Review) Action() string {
	if r.Hidden {
		return "/admin/reviews/show"
	}
	return "/admin/reviews/hide"
}

// ActionLabel is what the button says.
func (r Review) ActionLabel(ctx context.Context) string {
	if r.Hidden {
		return i18n.T(ctx, i18n.KeyAdminReviewShow)
	}
	return i18n.T(ctx, i18n.KeyAdminReviewHide)
}
