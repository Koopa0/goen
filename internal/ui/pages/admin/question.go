package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// Question is one customer question as the back office sees it.
type Question struct {
	ID             string
	Body           string
	Asker          string
	ProductSlug    string
	ProductName    string
	Asked          string
	Answers        int64
	AnsweredByShop bool
	Hidden         bool
	Replies        []QuestionAnswer
	// Draft is the reply staff typed when it was refused, and Error the sentence
	// under it.
	Draft, Error string
}

// Waiting reports whether the shop still owes an answer.
func (q Question) Waiting() bool { return !q.Hidden && !q.AnsweredByShop }

// State is the word a staff member scans for.
func (q Question) State(ctx context.Context) string {
	switch {
	case q.Hidden:
		return i18n.T(ctx, i18n.KeyAdminQHidden)
	case q.AnsweredByShop:
		return i18n.T(ctx, i18n.KeyAdminQAnswered)
	case q.Answers > 0:
		return i18n.T(ctx, i18n.KeyAdminQCustomerOnly)
	default:
		return i18n.T(ctx, i18n.KeyAdminQWaiting)
	}
}

// AnswersText is how many replies it has.
func (q Question) AnswersText() string { return strconv.FormatInt(q.Answers, 10) }

// Who is the person who asked.
func (q Question) Who(ctx context.Context) string {
	if q.Asker == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedAccountPlain)
	}
	return q.Asker
}

// ProductHref is the product's page on the storefront.
func (q Question) ProductHref() string { return "/p/" + q.ProductSlug }

// AnswerAction is where the reply form posts.
func (q Question) AnswerAction() string { return "/admin/questions/" + q.ID }

// QuestionsView is the queue.
type QuestionsView struct {
	Hidden bool
	Rows   []Question
	Notice string
}

// Empty reports whether nobody has asked anything.
func (v QuestionsView) Empty() bool { return len(v.Rows) == 0 }

// Waiting is how many still need the shop.
func (v QuestionsView) Waiting() int {
	n := 0
	for i := range v.Rows {
		if v.Rows[i].Waiting() {
			n++
		}
	}
	return n
}

// WaitingText is that count.
func (v QuestionsView) WaitingText() string { return strconv.Itoa(v.Waiting()) }

type QuestionAction string

const (
	QuestionAnswerAction QuestionAction = "answer"
	QuestionHideAction   QuestionAction = "hide"
	QuestionShowAction   QuestionAction = "show"
)

type QuestionAnswer struct {
	ID, Body, Author, At string
	Staff, Hidden        bool
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
