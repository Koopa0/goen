package pages

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminQuestion is one customer question as the back office sees it.
type AdminQuestion struct {
	ID             string
	Body           string
	Asker          string
	ProductSlug    string
	ProductName    string
	Asked          string
	Answers        int64
	AnsweredByShop bool
}

// Waiting reports whether the shop still owes an answer.
func (q AdminQuestion) Waiting() bool { return !q.AnsweredByShop }

// State is the word a staff member scans for.
func (q AdminQuestion) State(ctx context.Context) string {
	switch {
	case q.AnsweredByShop:
		return i18n.T(ctx, i18n.KeyAdminQAnswered)
	case q.Answers > 0:
		return i18n.T(ctx, i18n.KeyAdminQCustomerOnly)
	default:
		return i18n.T(ctx, i18n.KeyAdminQWaiting)
	}
}

// AnswersText is how many replies it has.
func (q AdminQuestion) AnswersText() string { return strconv.FormatInt(q.Answers, 10) }

// Who is the person who asked.
func (q AdminQuestion) Who(ctx context.Context) string {
	if q.Asker == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedAccountPlain)
	}
	return q.Asker
}

// ProductHref is the product's page on the storefront.
func (q AdminQuestion) ProductHref() string { return "/p/" + q.ProductSlug }

// AnswerAction is where the reply form posts.
func (q AdminQuestion) AnswerAction() string { return "/admin/questions/" + q.ID }

// AdminQuestionsView is the queue.
type AdminQuestionsView struct {
	Rows   []AdminQuestion
	Notice string
}

// Empty reports whether nobody has asked anything.
func (v AdminQuestionsView) Empty() bool { return len(v.Rows) == 0 }

// Waiting is how many still need the shop.
func (v AdminQuestionsView) Waiting() int {
	n := 0
	for _, q := range v.Rows {
		if q.Waiting() {
			n++
		}
	}
	return n
}

// WaitingText is that count.
func (v AdminQuestionsView) WaitingText() string { return strconv.Itoa(v.Waiting()) }
