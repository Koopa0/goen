package admin

import (
	"context"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/web"
)

type ReviewsView struct {
	web.Bound

	Rows               []Review
	Notice             components.Result
	ThreeStarsAndBelow bool
}

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

func (v ReviewsView) Empty() bool { return len(v.Rows) == 0 }

func (r Review) Stars() string {
	n := max(0, min(r.Rating, 5))
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

func (v ReviewsView) EmptyLabel(ctx context.Context) string {
	if v.ThreeStarsAndBelow {
		return i18n.T(ctx, i18n.KeyAdminReviewsFilteredEmpty)
	}
	return i18n.T(ctx, i18n.KeyAdminReviewsEmpty)
}

func (r Review) DisplayAuthor(ctx context.Context) string {
	if r.Author == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedAccount)
	}
	return r.Author
}

func (r Review) Href() string { return "/admin/products/" + r.Slug }

func (r Review) Action() string {
	if r.Hidden {
		return "/admin/reviews/show"
	}
	return "/admin/reviews/hide"
}

func (r Review) ActionLabel(ctx context.Context) string {
	if r.Hidden {
		return i18n.T(ctx, i18n.KeyAdminReviewShow)
	}
	return i18n.T(ctx, i18n.KeyAdminReviewHide)
}
