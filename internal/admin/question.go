package admin

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MaxQuestionRows bounds the queue.
const MaxQuestionRows = 50

// Questions reads what customers have asked, unanswered first.
func (s *Store) Questions(ctx context.Context) (pages.AdminQuestionsView, error) {
	rows, err := s.q.UnansweredQuestions(ctx, MaxQuestionRows)
	if err != nil {
		return pages.AdminQuestionsView{}, fmt.Errorf("read questions: %w", err)
	}
	view := pages.AdminQuestionsView{}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminQuestion{
			ID: r.ID.String(), Body: r.Body, Asker: r.Asker,
			ProductSlug: r.ProductSlug, ProductName: r.ProductName,
			Asked: r.CreatedAt.Format("2006-01-02 15:04"),
			// Days waiting, computed here rather than in the template: how long
			// is a fact about now, and a template would compute it against
			// whatever clock rendered the page.
			Answers: r.Answers, AnsweredByShop: r.AnsweredByShop,
		})
	}
	return view, nil
}

// HideQuestion takes a question off the product page.
//
// The answers go with it, which the storefront query already does — an answer
// published under nothing is text with no context.
func (s *Store) HideQuestion(ctx context.Context, id string) error {
	qID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionHideQuestion, Table: "product_questions",
		ID: uuid.NullUUID{UUID: qID, Valid: true},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, hideErr := q.HideQuestion(ctx, qID)
			if hideErr != nil {
				return fmt.Errorf("%w: %s", ErrRefused, hideErr.Error())
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// AnswerQuestion posts the SHOP's answer.
//
// is_staff is true and stored, because this endpoint is the shop — not because
// of who is signed in. A staff member answering from their own account on the
// storefront would be answering as a customer, which is the correct reading of
// where the words appeared.
func (s *Store) AnswerQuestion(ctx context.Context, id, userID, body string) error {
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > MaxStaffAnswerRunes {
		return ErrInvalid
	}
	qID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	author, err := uuid.Parse(userID)
	if err != nil {
		return ErrInvalid
	}
	return s.audited(ctx, Event{
		Action: ActionAnswerQuestion, Table: "product_answers",
		ID:    uuid.NullUUID{UUID: qID, Valid: true},
		After: map[string]any{"length": utf8.RuneCountInString(body)},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, answerErr := q.AnswerQuestion(ctx, db.AnswerQuestionParams{
				QuestionID: qID,
				UserID:     uuid.NullUUID{UUID: author, Valid: true},
				Body:       body, IsStaff: true,
			})
			if answerErr != nil {
				return fmt.Errorf("%w: %s", ErrRefused, answerErr.Error())
			}
			if n == 0 {
				// The question is gone or somebody hid it while this was being
				// typed. An answer under a hidden question would be text with
				// no context.
				return ErrNotFound
			}
			return nil
		})
}

// MaxStaffAnswerRunes bounds the shop's reply.
//
// Longer than a customer's, because an official answer to "does this support PD
// 3.1" sometimes needs the caveats — and counted in runes for the reason every
// other limit here is.
const MaxStaffAnswerRunes = 1000
