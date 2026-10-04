package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

type Question struct {
	ID             string
	Body           string
	Asker          string
	ProductSlug    string
	ProductName    string
	Asked          string
	AnswerCount    int64
	AnsweredByShop bool
	Hidden         bool
	Answers        []Answer
	// Draft is the reply staff typed when it was refused, and Error the sentence
	// under it.
	Draft, Error string
}

func (q Question) Waiting() bool { return !q.Hidden && !q.AnsweredByShop }

func (q Question) State(ctx context.Context) string {
	switch {
	case q.Hidden:
		return i18n.T(ctx, i18n.KeyAdminQHidden)
	case q.AnsweredByShop:
		return i18n.T(ctx, i18n.KeyAdminQAnswered)
	case q.AnswerCount > 0:
		return i18n.T(ctx, i18n.KeyAdminQCustomerOnly)
	default:
		return i18n.T(ctx, i18n.KeyAdminQWaiting)
	}
}

func (q Question) AnswersText() string { return strconv.FormatInt(q.AnswerCount, 10) }

func (q Question) Who(ctx context.Context) string {
	if q.Asker == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedAccountPlain)
	}
	return q.Asker
}

func (q Question) ProductHref() string { return "/p/" + q.ProductSlug }

func (q Question) AnswerAction() string { return "/admin/questions/" + q.ID }

type QuestionsView struct {
	web.Bound
	Hidden bool
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

type QuestionAction string

const (
	QuestionAnswerAction     QuestionAction = "answer"
	QuestionHideAction       QuestionAction = "hide"
	QuestionShowAction       QuestionAction = "show"
	QuestionHideAnswerAction QuestionAction = "hide_answer"
)

type Answer struct {
	ID, Body, Author, At string
	IsStaff, Hidden      bool
}

func (q Question) VisibilityAction() QuestionAction {
	if q.Hidden {
		return QuestionShowAction
	}
	return QuestionHideAction
}

func (q Question) VisibilityLabel(ctx context.Context) string {
	if q.Hidden {
		return i18n.T(ctx, i18n.KeyAdminQuestionShow)
	}
	return i18n.T(ctx, i18n.KeyAdminQuestionHide)
}

func (v QuestionsView) OtherQueueHref() string {
	if v.Hidden {
		return "/admin/questions"
	}
	return "/admin/questions?hidden=1"
}

func (v QuestionsView) OtherQueueLabel(ctx context.Context) string {
	if v.Hidden {
		return i18n.T(ctx, i18n.KeyAdminQuestionsVisible)
	}
	return i18n.T(ctx, i18n.KeyAdminQuestionsHidden)
}

func (v QuestionsView) EmptyLabel(ctx context.Context) string {
	if v.Hidden {
		return i18n.T(ctx, i18n.KeyAdminQuestionsHiddenEmpty)
	}
	return i18n.T(ctx, i18n.KeyAdminQuestionsEmpty)
}
