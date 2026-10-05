package feedback

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type QuestionQueue uint8

const (
	VisibleQuestions QuestionQueue = iota
	HiddenQuestions
)

type questionPosition struct {
	Rank bool
	ID   uuid.UUID
	At   time.Time
}

func (s *Store) Questions(ctx context.Context, queue QuestionQueue, after ...string) (admin.QuestionsView, error) {
	if queue != VisibleQuestions && queue != HiddenQuestions {
		return admin.QuestionsView{}, ErrInvalid
	}
	showHidden := queue == HiddenQuestions
	scope := web.ScopeURL("/admin/questions", "hidden", "")
	if showHidden {
		scope = web.ScopeURL("/admin/questions", "hidden", "1")
	}
	from, resumed := web.ResumeKeyset(scope, after, func(p questionPosition) bool { return p.ID != uuid.Nil })
	rows, err := s.q.AdminQuestions(ctx, db.AdminQuestionsParams{Hidden: showHidden, HasCursor: resumed, AfterRank: from.Rank, AfterAt: from.At, AfterID: from.ID, RowLimit: web.PageLimit})
	if err != nil {
		return admin.QuestionsView{}, fmt.Errorf("read questions: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminQuestionsRow) string { return r.PageCursor })
	view := admin.QuestionsView{Bound: bound, Hidden: showHidden}
	ids := make([]uuid.UUID, 0, len(rows))
	index := make(map[uuid.UUID]int, len(rows))
	for i := range rows {
		r := &rows[i]
		ids = append(ids, r.ID)
		index[r.ID] = i
		view.Rows = append(view.Rows, admin.Question{
			ID: r.ID.String(), Body: r.Body, Asker: r.Asker,
			ProductSlug: r.ProductSlug, ProductName: r.ProductName,
			Asked:       shoptime.Minute(r.CreatedAt),
			AnswerCount: r.Answers, AnsweredByShop: r.AnsweredByShop, Hidden: r.HiddenAt.Valid,
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
		row.Answers = append(row.Answers, admin.Answer{ID: a.ID.String(), Body: a.Body, Author: a.Author, At: shoptime.Minute(a.CreatedAt), IsStaff: a.IsStaff, Hidden: a.HiddenAt.Valid})
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
				return pgerr.WrapRefusal(hideErr, ErrRefused)
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
				return pgerr.WrapRefusal(answerErr, ErrRefused)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}

const MaxStaffAnswerRunes = 1000

func (s *Store) ShowQuestion(ctx context.Context, id string) error {
	questionID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{Action: audit.ActionShowQuestion, Table: "product_questions", ID: audit.EntityID(questionID), After: map[string]any{"hidden": false}}, func(ctx context.Context, q *db.Queries) error {
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

func (s *Store) HideAnswer(ctx context.Context, questionID, answerID string) error {
	question, err := uuid.Parse(questionID)
	if err != nil {
		return ErrNotFound
	}
	answer, err := uuid.Parse(answerID)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{Action: audit.ActionHideAnswer, Table: "product_answers", ID: audit.EntityID(answer), After: map[string]any{"question_id": questionID, "hidden": true}}, func(ctx context.Context, q *db.Queries) error {
		changed, hideErr := q.HideQuestionAnswer(ctx, db.HideQuestionAnswerParams{QuestionID: question, AnswerID: answer})
		if hideErr != nil {
			return fmt.Errorf("hide answer: %w", hideErr)
		}
		if changed == 0 {
			return ErrNotFound
		}
		return nil
	})
}
