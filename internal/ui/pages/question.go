package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// Answer is one reply to a question.
type Answer struct {
	Author string
	Body   string
	// IsStaff is what was true when the answer was written, never now.
	IsStaff bool
	At      string
}

// Who is the name to show beside an answer; a staff answer is the shop's.
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
