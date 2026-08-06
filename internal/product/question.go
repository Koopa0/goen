package product

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MaxQuestionRunes and MaxAnswerRunes bound what can be written.
//
// Counted in RUNES, not bytes: a question in Chinese is three bytes a
// character, and a byte limit would cut it to a third of an English one.
const (
	MaxQuestionRunes = 300
	MaxAnswerRunes   = 600
	// MaxQuestions bounds what one product page shows. Ten, because a page
	// with forty questions needs pagination and a search box, which is a
	// different feature.
	MaxQuestions = 10
)

// ErrQuestionInvalid is a question or answer goen refused before the database
// saw it.
var ErrQuestionInvalid = errors.New("product: the question or answer is not usable")

// Ask records a question about a product.
//
// Signed in only, the same rule reviews follow. An anonymous public writing
// surface is a spam target with nobody to hold responsible, and the sign-in is
// the only thing that makes the rate limit mean anything.
func (s *Store) Ask(ctx context.Context, slug, userID, body string) error {
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > MaxQuestionRunes {
		return ErrQuestionInvalid
	}
	asker, err := uuid.Parse(userID)
	if err != nil {
		return ErrQuestionInvalid
	}
	if err := s.q.AskQuestion(ctx, db.AskQuestionParams{
		Slug: slug, UserID: uuid.NullUUID{UUID: asker, Valid: true}, Body: body,
	}); err != nil {
		return fmt.Errorf("ask question: %w", err)
	}
	return nil
}

// Answer records an answer.
//
// staff is what the caller knows about the author RIGHT NOW, and it is stored
// rather than re-derived at read time — see product_answers.is_staff.
func (s *Store) Answer(ctx context.Context, questionID, userID, body string, staff bool) error {
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > MaxAnswerRunes {
		return ErrQuestionInvalid
	}
	qID, err := uuid.Parse(questionID)
	if err != nil {
		return ErrQuestionInvalid
	}
	author, err := uuid.Parse(userID)
	if err != nil {
		return ErrQuestionInvalid
	}
	n, err := s.q.AnswerQuestion(ctx, db.AnswerQuestionParams{
		QuestionID: qID, UserID: uuid.NullUUID{UUID: author, Valid: true},
		Body: body, IsStaff: staff,
	})
	if err != nil {
		return fmt.Errorf("answer question: %w", err)
	}
	if n == 0 {
		// The question does not exist or staff hid it. An answer published
		// under a hidden question would be text with no context.
		return ErrNotFound
	}
	return nil
}

// loadQuestions fills the Q&A section of a product page.
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

	ids := make([]uuid.UUID, 0, len(rows))
	byID := make(map[uuid.UUID]int, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
		byID[rows[i].ID] = i
		view.Questions = append(view.Questions, pages.Question{
			Asker: rows[i].Asker,
			Body:  rows[i].Body,
			Asked: rows[i].CreatedAt.Format("2006-01-02"),
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
			// An answer to a question this page did not load. Not an error —
			// the question list is capped — but it belongs to nothing here.
			continue
		}
		view.Questions[at].Answers = append(view.Questions[at].Answers, pages.Answer{
			Author:  a.Author,
			Body:    a.Body,
			IsStaff: a.IsStaff,
			At:      a.CreatedAt.Format("2006-01-02"),
		})
	}
	return nil
}
