//go:build integration

package admin_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/admintest"
)

// The dashboard shows how long the oldest open return request has waited, in
// shop days from when it was filed. The consumer's own seven days are not shown:
// for a request already filed that window is not the operator's clock.
func TestTheDashboardShowsHowLongTheOldestOpenReturnHasWaited(t *testing.T) {
	isolated := admintest.Pool(t)
	s := admin.NewStore(isolated, admintest.Refunder{}, nil, nil)

	none, err := s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if none.PendingReturns != 0 || none.OldestReturnDays != 0 {
		t.Fatalf("an empty shop shows %d returns, oldest %d days", none.PendingReturns, none.OldestReturnDays)
	}

	delivered := admintest.ShopNoonDaysAgo(t, 12)
	admintest.ReturnedOrderAtWithReason(t, isolated, delivered, admintest.ShopNoonDaysAgo(t, 3), "")
	admintest.ReturnedOrderAtWithReason(t, isolated, delivered, admintest.ShopNoonDaysAgo(t, 9), "")

	got, err := s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if got.PendingReturns != 2 {
		t.Errorf("pending returns = %d, want 2", got.PendingReturns)
	}
	if got.OldestReturnDays != 9 {
		t.Errorf("oldest open return = %d days, want 9", got.OldestReturnDays)
	}
}

// A question counts until the SHOP has answered it, and a customer's reply is
// not the shop's. It is the queue's own predicate: the tile and /admin/questions
// must agree.
func TestTheDashboardCountsQuestionsTheShopHasNotAnswered(t *testing.T) {
	isolated := admintest.Pool(t)
	s := admin.NewStore(isolated, admintest.Refunder{}, nil, nil)
	waiting := func() int64 {
		t.Helper()
		v, err := s.Dashboard(t.Context())
		if err != nil {
			t.Fatalf("Dashboard: %v", err)
		}
		return v.UnansweredQuestions
	}
	ask := func(hidden bool) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := isolated.QueryRow(t.Context(), `
			INSERT INTO product_questions (product_id, body, hidden_at)
			VALUES ((SELECT id FROM products ORDER BY id LIMIT 1), 'Does it fit?',
			        CASE WHEN $1 THEN now() END)
			RETURNING id`, hidden).Scan(&id); err != nil {
			t.Fatalf("ask: %v", err)
		}
		return id
	}
	answer := func(p *pgxpool.Pool, question uuid.UUID, staff bool) {
		t.Helper()
		if _, err := p.Exec(t.Context(),
			`INSERT INTO product_answers (question_id, body, is_staff) VALUES ($1, 'Yes.', $2)`,
			question, staff); err != nil {
			t.Fatalf("answer: %v", err)
		}
	}

	base := waiting()
	open := ask(false)
	ask(true)
	if got := waiting() - base; got != 1 {
		t.Fatalf("an open and a hidden question add %d, want 1: a hidden question is not waiting", got)
	}
	answer(isolated, open, false)
	if got := waiting() - base; got != 1 {
		t.Errorf("a customer's reply left %d waiting, want 1: only the shop's answer settles it", got)
	}
	answer(isolated, open, true)
	if got := waiting() - base; got != 0 {
		t.Errorf("a shop answer left %d waiting, want 0", got)
	}
}
