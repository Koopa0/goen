package feedback

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

const MaxQuestionRows = 50

func (s *Store) Questions(ctx context.Context) (admin.QuestionsView, error) {
	rows, err := s.q.UnansweredQuestions(ctx, MaxQuestionRows)
	if err != nil {
		return admin.QuestionsView{}, fmt.Errorf("read questions: %w", err)
	}
	view := admin.QuestionsView{}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, admin.Question{
			ID: r.ID.String(), Body: r.Body, Asker: r.Asker,
			ProductSlug: r.ProductSlug, ProductName: r.ProductName,
			Asked:   shoptime.Minute(r.CreatedAt),
			Answers: r.Answers, AnsweredByShop: r.AnsweredByShop,
		})
	}
	return view, nil
}

func (s *Store) HideQuestion(ctx context.Context, id string) error {
	qID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionHideQuestion, Table: "product_questions",
		ID: uuid.NullUUID{UUID: qID, Valid: true},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, hideErr := q.HideQuestion(ctx, qID)
			if hideErr != nil {
				return fmt.Errorf("%w: %w", ErrRefused, hideErr)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

// AnswerQuestion posts the SHOP's answer; is_staff is true because of the
// ENDPOINT, never of who is signed in.
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
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionAnswerQuestion, Table: "product_answers",
		ID:    uuid.NullUUID{UUID: qID, Valid: true},
		After: map[string]any{"length": utf8.RuneCountInString(body)},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, answerErr := q.AnswerQuestionAsStaff(ctx, db.AnswerQuestionAsStaffParams{
				QuestionID: qID,
				UserID:     uuid.NullUUID{UUID: author, Valid: true},
				Body:       body,
			})
			if answerErr != nil {
				return fmt.Errorf("%w: %w", ErrRefused, answerErr)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

const MaxStaffAnswerRunes = 1000
