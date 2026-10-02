//go:build integration

package admin_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
)

// The dashboard names the earliest last day of the statutory seven days among
// the open return requests, taken from return_window_ends and not restated in
// Go: a parcel delivered on 1 January ends on the 8th on the shop's calendar
// however late in that day it arrived.
func TestTheDashboardNamesTheNearestStatutoryDeadlineOfOpenReturns(t *testing.T) {
	isolated := isolatedAdminSeedPool(t)
	s := admin.NewStore(isolated, fakeRefunder{}, nil, nil)

	none, err := s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if none.PendingReturns != 0 || none.ReturnsDeadline != "" {
		t.Fatalf("an empty shop shows %d returns with deadline %q, want 0 and none",
			none.PendingReturns, none.ReturnsDeadline)
	}

	later := mustRFC3339(t, "2026-01-03T23:30:00+08:00")
	earlier := mustRFC3339(t, "2026-01-01T07:00:00+08:00")
	requested := mustRFC3339(t, "2026-01-06T12:00:00+08:00")
	returnedOrderAtWithReasonOn(t, isolated, later, requested, "")
	returnedOrderAtWithReasonOn(t, isolated, earlier, requested, "")

	got, err := s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if got.PendingReturns != 2 {
		t.Errorf("pending returns = %d, want 2", got.PendingReturns)
	}
	if got.ReturnsDeadline != "2026-01-08" {
		t.Errorf("nearest deadline = %q, want 2026-01-08 (the 1 January parcel's seventh day)", got.ReturnsDeadline)
	}
}

// A question counts until the SHOP has answered it, and a customer's reply is
// not the shop's. It is the queue's own predicate: the tile and /admin/questions
// must agree.
func TestTheDashboardCountsQuestionsTheShopHasNotAnswered(t *testing.T) {
	isolated := isolatedAdminSeedPool(t)
	s := admin.NewStore(isolated, fakeRefunder{}, nil, nil)
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
