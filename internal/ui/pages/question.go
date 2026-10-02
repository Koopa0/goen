package pages

import (
	"context"

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
