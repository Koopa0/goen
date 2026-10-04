//go:build integration

package orders_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// The dashboard lists work that waits for a person: a return approved and not
// yet inspected comes from the desk's own counts, a stranded invoice claim from
// the health desk, so the list and /admin/health name the same things.
func TestTheDashboardListsAnUninspectedReturnAndAStrandedClaim(t *testing.T) {
	isolated := admintest.Pool(t)
	s := admintest.OrderStore(isolated, admintest.Refunder{}, nil, nil)
	count := func(tasks []admin.Task, label i18n.Key) int64 {
		for _, task := range tasks {
			if task.Label == label {
				return task.Count
			}
		}
		return 0
	}

	view, err := s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	health, err := s.HealthTasks(t.Context())
	if err != nil {
		t.Fatalf("HealthTasks: %v", err)
	}
	if len(view.Tasks) != 0 || len(health) != 0 {
		t.Fatalf("an empty shop lists tasks %v and %v", view.Tasks, health)
	}

	admintest.PreapprovedReturn(t, isolated)
	number, orderID := admintest.PaidPickingOrderForUser(t, isolated, admintest.Customer(t, isolated), 100000)
	if _, err := isolated.Exec(t.Context(), `
		INSERT INTO invoice_operations
		    (order_id, kind, provider_key, amount_cents, request_payload,
		     actor_kind, request_id, status, last_error, created_at)
		VALUES ($1, 'issue', replace($2, '-', ''), 100000, '{}', 'system',
		        'refused:' || $2, 'rejected', 'issue_provider_rejected_2000006',
		        now() - interval '1 minute')`, orderID, number); err != nil {
		t.Fatalf("record a refused automatic issue: %v", err)
	}

	view, err = s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if got := count(view.Tasks, i18n.KeyAdminQueueTaskUninspected); got != 1 {
		t.Errorf("returns awaiting inspection = %d, want 1", got)
	}
	health, err = s.HealthTasks(t.Context())
	if err != nil {
		t.Fatalf("HealthTasks: %v", err)
	}
	if got := count(health, i18n.KeyAdminHPClaimsHeading); got != 1 {
		t.Errorf("stranded invoice claims = %d, want 1", got)
	}
}

// A question counts until the SHOP has answered it, and a customer's reply is
// not the shop's. It is the queue's own predicate: the tile and /admin/questions
// must agree.
func TestTheDashboardCountsQuestionsTheShopHasNotAnswered(t *testing.T) {
	isolated := admintest.Pool(t)
	s := admintest.OrderStore(isolated, admintest.Refunder{}, nil, nil)
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
