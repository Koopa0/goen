package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// Answer is one reply to a question.
type Answer struct {
	Author string
	Body   string
	// IsStaff is what was true when it was written, not what is true about the
	// author now — see product_answers.is_staff. A customer who later joins the
	// shop must not retroactively turn their old answers into official ones.
	IsStaff bool
	At      string
}

// Who is the name to show beside an answer.
//
// A staff answer is attributed to the SHOP, not to the person: a customer
// deciding wants to know the answer is official, and which member of staff
// typed it is not their business — and putting a staff member's name on a
// public page is a decision nobody made.
func (a Answer) Who(ctx context.Context) string {
	if a.IsStaff {
		return "goen"
	}
	if a.Author == "" {
		return i18n.T(ctx, i18n.KeyErasedAccount)
	}
	return a.Author
}

// Question is one question and everything said in reply.
type Question struct {
	Asker   string
	Body    string
	Asked   string
	Answers []Answer
}

// Who is the name to show beside a question.
func (q Question) Who(ctx context.Context) string {
	if q.Asker == "" {
		return i18n.T(ctx, i18n.KeyErasedAccount)
	}
	return q.Asker
}

// Answered reports whether anybody has replied.
func (q Question) Answered() bool { return len(q.Answers) > 0 }

// AnsweredByShop reports whether the SHOP has replied, which is the thing
// somebody deciding is looking for.
func (q Question) AnsweredByShop() bool {
	for _, a := range q.Answers {
		if a.IsStaff {
			return true
		}
	}
	return false
}
