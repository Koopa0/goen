package content

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

const MaxFAQEntries = 200

// ErrInUse is a FAQ category that still holds entries.
var ErrInUse = errors.New("content: faq category in use")

const (
	// MaxFAQCategoryRunes is faq_categories_name_bounded and _name_en_bounded.
	MaxFAQCategoryRunes = 40
	MaxFAQQuestionRunes = 200
	MaxFAQAnswerRunes   = 2000
)

// FAQForm is an entry as the desk posts it. CategoryID is read on create only:
// an entry stays in the category it was added to.
type FAQForm struct {
	ID         string
	CategoryID string
	Question   string
	Answer     string
	QuestionEn string
	AnswerEn   string

	category uuid.UUID
}

func (f *FAQForm) Validate(ctx context.Context) map[string]string {
	f.Question = strings.TrimSpace(f.Question)
	f.Answer = strings.TrimSpace(f.Answer)
	f.QuestionEn = strings.TrimSpace(f.QuestionEn)
	f.AnswerEn = strings.TrimSpace(f.AnswerEn)

	errs := map[string]string{}
	if f.ID == "" {
		id, err := uuid.Parse(strings.TrimSpace(f.CategoryID))
		if err != nil {
			errs["category"] = i18n.T(ctx, i18n.KeyFormFAQPickCategory)
		}
		f.category = id
	}
	if f.Question == "" || utf8.RuneCountInString(f.Question) > MaxFAQQuestionRunes {
		errs["question"] = i18n.T(ctx, i18n.KeyFormFAQQuestion)
	}
	if f.Answer == "" || utf8.RuneCountInString(f.Answer) > MaxFAQAnswerRunes {
		errs["answer"] = i18n.T(ctx, i18n.KeyFormFAQAnswer)
	}
	if utf8.RuneCountInString(f.QuestionEn) > MaxFAQQuestionRunes {
		errs["question_en"] = i18n.T(ctx, i18n.KeyFormFAQQuestionEnLong)
	}
	if utf8.RuneCountInString(f.AnswerEn) > MaxFAQAnswerRunes {
		errs["answer_en"] = i18n.T(ctx, i18n.KeyFormFAQAnswerEnLong)
	}
	return errs
}

// FAQCategoryForm is a category as the desk posts it: a new one when ID is
// empty, a rename otherwise.
type FAQCategoryForm struct {
	ID     string
	Name   string
	NameEn string
}

func (f *FAQCategoryForm) Validate(ctx context.Context) map[string]string {
	f.Name = strings.TrimSpace(f.Name)
	f.NameEn = strings.TrimSpace(f.NameEn)

	errs := map[string]string{}
	if f.Name == "" || utf8.RuneCountInString(f.Name) > MaxFAQCategoryRunes {
		errs["name"] = i18n.T(ctx, i18n.KeyFormFAQCategory)
	}
	if utf8.RuneCountInString(f.NameEn) > MaxFAQCategoryRunes {
		errs["name_en"] = i18n.T(ctx, i18n.KeyFormFAQCategoryEnLong)
	}
	return errs
}

func (s *Store) FAQ(ctx context.Context) (FAQView, error) {
	rows, err := s.q.AdminFAQEntries(ctx, MaxFAQEntries)
	if err != nil {
		return FAQView{}, fmt.Errorf("read faq entries: %w", err)
	}
	cats, err := s.q.AdminFAQCategories(ctx)
	if err != nil {
		return FAQView{}, fmt.Errorf("read faq categories: %w", err)
	}
	view := FAQView{
		Rows:       make([]FAQEntry, 0, len(rows)),
		Categories: make([]FAQCategory, 0, len(cats)),
	}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, FAQEntry{
			ID: r.ID.String(), Category: r.Category, Question: r.Question,
			Answer: r.Answer, QuestionEn: r.QuestionEn,
			AnswerEn: r.AnswerEn, UpdatedAt: shoptime.Day(r.UpdatedAt),
		})
	}
	for i := range cats {
		c := &cats[i]
		view.Categories = append(view.Categories, FAQCategory{
			ID: c.ID.String(), Name: c.Name, NameEn: c.NameEn, InUse: c.InUse,
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
		After: audit.State{audit.FieldCategoryID: f.category.String(), audit.FieldQuestion: f.Question},
	}, func(ctx context.Context, q *db.Queries) error {
		if err := q.LockFAQAppendPosition(ctx, f.category); err != nil {
			return err
		}
		return q.CreateFAQEntry(ctx, db.CreateFAQEntryParams{
			CategoryID: f.category, Question: f.Question, Answer: f.Answer,
			QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn,
		})
	}); err != nil {
		// The category was deleted between the page and the post.
		if db.IsConstraint(err, "faq_entries_category_id_fkey") {
			return map[string]string{"category": i18n.T(ctx, i18n.KeyFormFAQPickCategory)}, nil
		}
		return nil, db.WrapRefusal(err, ErrRefused)
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
		After: audit.State{audit.FieldQuestion: f.Question},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.UpdateFAQEntry(ctx, db.UpdateFAQEntryParams{
			EntryID: entryID, Question: f.Question, Answer: f.Answer,
			QuestionEn: f.QuestionEn, AnswerEn: f.AnswerEn,
		})
		if execErr != nil {
			return db.WrapRefusal(execErr, ErrRefused)
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
		Before: audit.State{audit.FieldID: id},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.DeleteFAQEntry(ctx, entryID)
		if execErr != nil {
			return db.WrapRefusal(execErr, ErrRefused)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// CreateFAQCategory appends a category after the existing ones.
func (s *Store) CreateFAQCategory(ctx context.Context, f *FAQCategoryForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateFAQCategory, Table: "faq_categories",
		After: audit.State{audit.FieldName: f.Name, audit.FieldNameEn: f.NameEn},
	}, func(ctx context.Context, q *db.Queries) error {
		if err := q.LockFAQCategoryAppendPosition(ctx); err != nil {
			return err
		}
		return q.CreateFAQCategory(ctx, db.CreateFAQCategoryParams{Name: f.Name, NameEn: f.NameEn})
	})
	if errs := faqCategoryTaken(ctx, err); errs != nil {
		return errs, nil
	}
	if err != nil {
		return nil, db.WrapRefusal(err, ErrRefused)
	}
	return nil, nil
}

// RenameFAQCategory changes a category's names, and with them the heading of
// every entry in it.
func (s *Store) RenameFAQCategory(ctx context.Context, f *FAQCategoryForm) (map[string]string, error) {
	categoryID, err := uuid.Parse(f.ID)
	if err != nil {
		return nil, ErrNotFound
	}
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	err = audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionRenameFAQCategory, Table: "faq_categories", ID: audit.EntityID(categoryID),
		After: audit.State{audit.FieldName: f.Name, audit.FieldNameEn: f.NameEn},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.RenameFAQCategory(ctx, db.RenameFAQCategoryParams{
			CategoryID: categoryID, Name: f.Name, NameEn: f.NameEn,
		})
		if execErr != nil {
			return execErr
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if errs := faqCategoryTaken(ctx, err); errs != nil {
		return errs, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, db.WrapRefusal(err, ErrRefused)
	}
	return nil, err
}

// faqCategoryTaken answers a name another category already has as a refusal
// of that field; nil for any other outcome.
func faqCategoryTaken(ctx context.Context, err error) map[string]string {
	switch {
	case db.IsConstraint(err, "faq_categories_name_key"):
		return map[string]string{"name": i18n.T(ctx, i18n.KeyFormFAQCategoryTaken)}
	case db.IsConstraint(err, "faq_categories_name_en_key"):
		return map[string]string{"name_en": i18n.T(ctx, i18n.KeyFormFAQCategoryTaken)}
	}
	return nil
}

// DeleteFAQCategory removes a category with no entries. The foreign key
// refuses one that still holds entries, which is ErrInUse.
func (s *Store) DeleteFAQCategory(ctx context.Context, id string) error {
	categoryID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionDeleteFAQCategory, Table: "faq_categories", ID: audit.EntityID(categoryID),
		Before: audit.State{audit.FieldID: id},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.DeleteFAQCategory(ctx, categoryID)
		switch {
		case db.IsConstraint(execErr, "faq_entries_category_id_fkey"):
			return ErrInUse
		case execErr != nil:
			return db.WrapRefusal(execErr, ErrRefused)
		case n == 0:
			return ErrNotFound
		}
		return nil
	})
}
