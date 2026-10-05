package content

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

const MaxFAQEntries = 200

const (
	MaxFAQCategoryRunes = 40
	MaxFAQQuestionRunes = 200
	MaxFAQAnswerRunes   = 2000
)

type FAQForm struct {
	ID         string
	Category   string
	Question   string
	Answer     string
	CategoryEn string
	QuestionEn string
	AnswerEn   string
}

func (f *FAQForm) Validate(ctx context.Context) map[string]string {
	f.Category = strings.TrimSpace(f.Category)
	f.Question = strings.TrimSpace(f.Question)
	f.Answer = strings.TrimSpace(f.Answer)
	f.CategoryEn = strings.TrimSpace(f.CategoryEn)
	f.QuestionEn = strings.TrimSpace(f.QuestionEn)
	f.AnswerEn = strings.TrimSpace(f.AnswerEn)

	errs := map[string]string{}
	if f.Category == "" || utf8.RuneCountInString(f.Category) > MaxFAQCategoryRunes {
		errs["category"] = i18n.T(ctx, i18n.KeyFormFAQCategory)
	}
	if f.Question == "" || utf8.RuneCountInString(f.Question) > MaxFAQQuestionRunes {
		errs["question"] = i18n.T(ctx, i18n.KeyFormFAQQuestion)
	}
	if f.Answer == "" || utf8.RuneCountInString(f.Answer) > MaxFAQAnswerRunes {
		errs["answer"] = i18n.T(ctx, i18n.KeyFormFAQAnswer)
	}
	if utf8.RuneCountInString(f.CategoryEn) > MaxFAQCategoryRunes {
		errs["category_en"] = i18n.T(ctx, i18n.KeyFormFAQCategoryEnLong)
	}
	if utf8.RuneCountInString(f.QuestionEn) > MaxFAQQuestionRunes {
		errs["question_en"] = i18n.T(ctx, i18n.KeyFormFAQQuestionEnLong)
	}
	if utf8.RuneCountInString(f.AnswerEn) > MaxFAQAnswerRunes {
		errs["answer_en"] = i18n.T(ctx, i18n.KeyFormFAQAnswerEnLong)
	}
	return errs
}

func (s *Store) FAQ(ctx context.Context) (admin.FAQView, error) {
	rows, err := s.q.AdminFAQEntries(ctx, MaxFAQEntries)
	if err != nil {
		return admin.FAQView{}, fmt.Errorf("read faq entries: %w", err)
	}
	view := admin.FAQView{Rows: make([]admin.FAQEntry, 0, len(rows))}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, admin.FAQEntry{
			ID: r.ID.String(), Category: r.Category, Question: r.Question,
			Answer: r.Answer, CategoryEn: r.CategoryEn, QuestionEn: r.QuestionEn,
			AnswerEn: r.AnswerEn, UpdatedAt: shoptime.Day(r.UpdatedAt),
		})
	}
	return view, nil
}

func (s *Store) CreateFAQEntry(ctx context.Context, f *FAQForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	if err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateFAQ, Table: "faq_entries",
		After: map[string]any{"category": f.Category, "question": f.Question},
	}, func(ctx context.Context, q *db.Queries) error {
		if err := q.LockFAQAppendPosition(ctx, f.Category); err != nil {
			return err
		}
		return q.CreateFAQEntry(ctx, db.CreateFAQEntryParams{
			Category: f.Category, Question: f.Question, Answer: f.Answer,
			CategoryEn: f.CategoryEn, QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn,
		})
	}); err != nil {
		return nil, pgerr.WrapRefusal(err, ErrRefused)
	}
	return nil, nil
}

func (s *Store) UpdateFAQEntry(ctx context.Context, f *FAQForm) (map[string]string, error) {
	entryID, err := uuid.Parse(f.ID)
	if err != nil {
		return nil, ErrNotFound
	}
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	if err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionUpdateFAQ, Table: "faq_entries", ID: audit.EntityID(entryID),
		After: map[string]any{"category": f.Category},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.UpdateFAQEntry(ctx, db.UpdateFAQEntryParams{
			EntryID: entryID, Question: f.Question, Answer: f.Answer,
			CategoryEn: f.CategoryEn, QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn,
		})
		if execErr != nil {
			return pgerr.WrapRefusal(execErr, ErrRefused)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *Store) DeleteFAQEntry(ctx context.Context, id string) error {
	entryID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionDeleteFAQ, Table: "faq_entries", ID: audit.EntityID(entryID),
		Before: map[string]any{"id": id},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.DeleteFAQEntry(ctx, entryID)
		if execErr != nil {
			return pgerr.WrapRefusal(execErr, ErrRefused)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
