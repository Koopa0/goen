package pages

import "strconv"

// AdminReviewsView is the review queue.
//
// Newest first, unlike the question queue. A question waiting three days is
// owed an answer and gets more urgent; a review is owed nothing, and what a
// shop wants to see is what has just appeared on its product pages.
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

// HiddenCount is how many are currently out of the score.
func (v AdminReviewsView) HiddenCount() int {
	n := 0
	for i := range v.Rows {
		if v.Rows[i].Hidden {
			n++
		}
	}
	return n
}

// Stars is the rating as a reader scans it.
func (r AdminReview) Stars() string { return starsOf(r.Rating) }

// RatingText is the number beside them, for anyone the stars do not reach.
func (r AdminReview) RatingText() string { return strconv.Itoa(r.Rating) }

// DisplayAuthor is who wrote it, or a stand-in — an erased account leaves the
// review behind with no name, which is the point of user_id being nullable.
func (r AdminReview) DisplayAuthor() string {
	if r.Author == "" {
		return "(已刪除帳號)"
	}
	return r.Author
}

// Href is the product page it appears on, so a staff member can see it in
// context before deciding.
func (r AdminReview) Href() string { return "/p/" + r.Slug }

// Action is where the toggle posts. Hiding and showing are separate paths
// rather than one toggle, so a double-submitted form cannot un-hide something
// the staff member just hid.
func (r AdminReview) Action() string {
	if r.Hidden {
		return "/admin/reviews/show"
	}
	return "/admin/reviews/hide"
}

// ActionLabel is what the button says.
func (r AdminReview) ActionLabel() string {
	if r.Hidden {
		return "恢復顯示"
	}
	return "隱藏"
}
