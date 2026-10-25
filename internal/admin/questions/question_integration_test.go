//go:build integration

package questions_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/questions"
	"github.com/koopa0/goen/internal/catalog"
)

// TestStore_QuestionsOrder: the queue puts what the shop owes first.
func TestStore_QuestionsOrder(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := questions.NewStore(pool)
	ps := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	asker := newAskingCustomer(t)
	slug := anyActiveProductSlug(t)

	old := ask(t, ps, slug, asker, "最舊的,店家已回", -3)
	if err := s.AnswerQuestion(ctx, old, asker, "店家的回答"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	middling := ask(t, ps, slug, asker, "中間的,只有顧客回", -2)
	// Historical customer answers do not settle the shop's unanswered queue.
	if _, err := pool.Exec(ctx, `INSERT INTO product_answers (question_id, user_id, body, is_staff) VALUES ($1, $2, $3, false)`, uuid.MustParse(middling), uuid.MustParse(asker), "我覺得可以"); err != nil {
		t.Fatalf("historical customer answer: %v", err)
	}
	newest := ask(t, ps, slug, asker, "最新的,沒人回", -1)

	view, err := s.Questions(ctx, questions.QueueVisible)
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	order := make([]string, 0, len(view.Rows))
	for _, q := range view.Rows {
		if q.ID == old || q.ID == middling || q.ID == newest {
			order = append(order, q.Body)
		}
	}
	if len(order) != 3 {
		t.Fatalf("%d of the three questions are in the queue", len(order))
	}
	if order[0] != "中間的,只有顧客回" {
		t.Errorf("first is %q, want the oldest one the shop still owes", order[0])
	}
	if order[2] != "最舊的,店家已回" {
		t.Errorf("last is %q, want the one the shop already answered", order[2])
	}
}

// TestStore_HideQuestion: hiding a question is recorded and cannot be repeated.
func TestStore_HideQuestion(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := questions.NewStore(pool)
	ps := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	asker := newAskingCustomer(t)
	id := ask(t, ps, anyActiveProductSlug(t), asker, "會被隱藏的", 0)

	before := admintest.AuditRows(t, pool, audit.ActionHideQuestion)
	if err := s.HideQuestion(ctx, id); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionHideQuestion); after != before+1 {
		t.Errorf("hiding left %d audit rows, want one more than %d", after, before)
	}
	if err := s.HideQuestion(ctx, id); !errors.Is(err, questions.ErrNotFound) {
		t.Errorf("hiding twice gave %v, want ErrNotFound", err)
	}
	if after := admintest.AuditRows(t, pool, audit.ActionHideQuestion); after != before+1 {
		t.Errorf("a refused hide wrote an audit row")
	}
}

// TestStore_AnswerQuestionEmpty: an empty official answer is refused.
func TestStore_AnswerQuestionEmpty(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := questions.NewStore(pool)
	ps := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	asker := newAskingCustomer(t)
	id := ask(t, ps, anyActiveProductSlug(t), asker, "等一個回答", 0)

	for _, body := range []string{"", "   ", "\t\n"} {
		if err := s.AnswerQuestion(ctx, id, asker, body); !errors.Is(err, questions.ErrInvalid) {
			t.Errorf("%q gave %v, want ErrInvalid", body, err)
		}
	}
	if err := s.AnswerQuestion(ctx, id, asker, "真的回答"); err != nil {
		t.Errorf("a real answer was refused: %v", err)
	}
}

func ask(t *testing.T, s *catalog.Store, slug, userID, body string, days int) string {
	t.Helper()
	ctx := t.Context()
	if err := s.Ask(ctx, slug, userID, body); err != nil {
		t.Fatalf("ask %q: %v", body, err)
	}
	// The asker is new to each test, so body and asker name one row even when
	// the test runs again against the same database (-count).
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		UPDATE product_questions SET created_at = now() + make_interval(days => $2)
		WHERE body = $1 AND user_id = $3 RETURNING id`, body, days, userID).Scan(&id); err != nil {
		t.Fatalf("backdate %q: %v", body, err)
	}
	return id.String()
}

func newAskingCustomer(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('aq-' || gen_random_uuid() || '@goen.invalid', 'customer', '提問者')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return id.String()
}

func anyActiveProductSlug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products WHERE status = 'active' LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}
