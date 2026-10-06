package product

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

const (
	MaxQuestionRunes = 300
	MaxQuestions     = 10
)

var ErrQuestionInvalid = errors.New("product: the question or answer is not usable")

func (s *Store) Ask(ctx context.Context, slug, userID, body string) error {
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > MaxQuestionRunes {
		return ErrQuestionInvalid
	}
	asker, err := uuid.Parse(userID)
	if err != nil {
		return ErrQuestionInvalid
	}
	n, err := s.q.AskQuestion(ctx, db.AskQuestionParams{
		Slug: slug, UserID: uuid.NullUUID{UUID: asker, Valid: true}, Body: body,
	})
	if err != nil {
		return fmt.Errorf("ask question: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) loadQuestions(ctx context.Context, productID uuid.UUID, view *pages.ProductView) error {
	rows, err := s.q.ProductQuestions(ctx, db.ProductQuestionsParams{
		ProductID: productID, Limit: MaxQuestions,
	})
	if err != nil {
		return fmt.Errorf("read questions: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	now := s.now()
	ids := make([]uuid.UUID, 0, len(rows))
	byID := make(map[uuid.UUID]int, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
		byID[rows[i].ID] = i
		view.Questions = append(view.Questions, pages.Question{
			Asker: rows[i].Asker,
			Body:  rows[i].Body,
			Asked: shoptime.DateText(ctx, shoptime.DateOf(rows[i].CreatedAt, now)),
		})
	}

	answers, err := s.q.AnswersForQuestions(ctx, ids)
	if err != nil {
		return fmt.Errorf("read answers: %w", err)
	}
	for i := range answers {
		a := &answers[i]
		at, ok := byID[a.QuestionID]
		if !ok {
			continue
		}
		view.Questions[at].Answers = append(view.Questions[at].Answers, pages.Answer{
			Author:  a.Author,
			Body:    a.Body,
			IsStaff: a.IsStaff,
			At:      shoptime.DateText(ctx, shoptime.DateOf(a.CreatedAt, now)),
		})
	}
	return nil
}
