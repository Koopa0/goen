package admin

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// MaxQuestionRows bounds the queue.
const MaxQuestionRows = 50

// Questions reads what customers have asked, unanswered first.
func (s *Store) Questions(ctx context.Context, hidden ...bool) (admin.QuestionsView, error) {
	showHidden := len(hidden) > 0 && hidden[0]
	rows, err := s.q.AdminQuestions(ctx, db.AdminQuestionsParams{Hidden: showHidden, RowLimit: MaxQuestionRows})
	if err != nil {
		return admin.QuestionsView{}, fmt.Errorf("read questions: %w", err)
	}
	view := admin.QuestionsView{Hidden: showHidden}
	ids := make([]uuid.UUID, 0, len(rows))
	index := make(map[uuid.UUID]int, len(rows))
	for i := range rows {
		r := &rows[i]
		ids = append(ids, r.ID)
		index[r.ID] = i
		view.Rows = append(view.Rows, admin.Question{
			ID: r.ID.String(), Body: r.Body, Asker: r.Asker,
			ProductSlug: r.ProductSlug, ProductName: r.ProductName,
			Asked:   shoptime.Minute(r.CreatedAt),
			Answers: r.Answers, AnsweredByShop: r.AnsweredByShop, Hidden: r.HiddenAt.Valid,
		})
	}
	if len(ids) == 0 {
		return view, nil
	}
	answers, err := s.q.AdminQuestionAnswers(ctx, ids)
	if err != nil {
		return admin.QuestionsView{}, fmt.Errorf("read question answers: %w", err)
	}
	for i := range answers {
		a := &answers[i]
		row := &view.Rows[index[a.QuestionID]]
		row.Replies = append(row.Replies, admin.QuestionAnswer{ID: a.ID.String(), Body: a.Body, Author: a.Author, At: shoptime.Minute(a.CreatedAt), Staff: a.IsStaff, Hidden: a.HiddenAt.Valid})
	}
	return view, nil
}

// HideQuestion takes a question off the product page. Its answers go with it.
func (s *Store) HideQuestion(ctx context.Context, id string) error {
	qID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: actionHideQuestion, Table: "product_questions",
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
	return s.audited(ctx, Event{
		Action: actionAnswerQuestion, Table: "product_answers",
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

// MaxStaffAnswerRunes bounds the shop's reply, in RUNES.
const MaxStaffAnswerRunes = 1000

func (s *Store) ShowQuestion(ctx context.Context, id string) error {
	questionID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{Action: actionShowQuestion, Table: "product_questions", ID: nullableID(questionID), After: map[string]any{"hidden": false}}, func(ctx context.Context, q *db.Queries) error {
		changed, showErr := q.ShowQuestion(ctx, questionID)
		if showErr != nil {
			return fmt.Errorf("show question: %w", showErr)
		}
		if changed == 0 {
			return ErrNotFound
		}
		return nil
	})
}
