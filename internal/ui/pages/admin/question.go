package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

type Question struct {
	ID             string
	Body           string
	Asker          string
	ProductSlug    string
	ProductName    string
	Asked          string
	Answers        int64
	AnsweredByShop bool
	// Draft is the reply staff typed when it was refused, and Error the sentence
	// under it.
	Draft, Error string
}

func (q Question) Waiting() bool { return !q.AnsweredByShop }

func (q Question) State(ctx context.Context) string {
	switch {
	case q.AnsweredByShop:
		return i18n.T(ctx, i18n.KeyAdminQAnswered)
	case q.Answers > 0:
		return i18n.T(ctx, i18n.KeyAdminQCustomerOnly)
	default:
		return i18n.T(ctx, i18n.KeyAdminQWaiting)
	}
}

func (q Question) AnswersText() string { return strconv.FormatInt(q.Answers, 10) }

func (q Question) Who(ctx context.Context) string {
	if q.Asker == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedAccountPlain)
	}
	return q.Asker
}

func (q Question) ProductHref() string { return "/p/" + q.ProductSlug }

func (q Question) AnswerAction() string { return "/admin/questions/" + q.ID }

type QuestionsView struct {
	Rows   []Question
	Notice string
}

func (v QuestionsView) Empty() bool { return len(v.Rows) == 0 }

func (v QuestionsView) Waiting() int {
	n := 0
	for i := range v.Rows {
		if v.Rows[i].Waiting() {
			n++
		}
	}
	return n
}

func (v QuestionsView) WaitingText() string { return strconv.Itoa(v.Waiting()) }
