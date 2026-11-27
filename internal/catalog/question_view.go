package catalog

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

type Answer struct {
	Author string
	Body   string
	// IsStaff is what was true when the answer was written, never now.
	IsStaff bool
	At      string
}

func (a Answer) Who(ctx context.Context) string {
	if a.IsStaff {
		return "goen"
	}
	if a.Author == "" {
		return i18n.T(ctx, i18n.KeyErasedAccount)
	}
	return maskedName(i18n.FromContext(ctx), a.Author)
}

type Question struct {
	Asker   string
	Body    string
	Asked   string
	Answers []Answer
}

func (q Question) Who(ctx context.Context) string {
	if q.Asker == "" {
		return i18n.T(ctx, i18n.KeyErasedAccount)
	}
	return maskedName(i18n.FromContext(ctx), q.Asker)
}

func (q Question) Answered() bool { return len(q.Answers) > 0 }

// AnswerCount is what a closed question says of its thread: how many answers it holds, or that none has
// come, so a shopper sees which questions are worth opening.
func (q Question) AnswerCount(ctx context.Context) string {
	if !q.Answered() {
		return i18n.T(ctx, i18n.KeyNoAnswerYet)
	}
	n := int64(len(q.Answers))
	return i18n.Count(ctx, i18n.KeyAnswerCount, n, strconv.FormatInt(n, 10))
}
