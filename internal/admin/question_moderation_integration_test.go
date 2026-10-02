//go:build integration

package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
)

func TestAdminReadsAnswersAndRestoresQuestionsWithExistingPrivileges(t *testing.T) {
	p := isolatedAdminSeedPool(t)
	ctx, actor := staffContextOn(t, p)
	var question, answer uuid.UUID
	if err := p.QueryRow(ctx, `INSERT INTO product_questions (product_id, body, hidden_at)
		VALUES ((SELECT id FROM products ORDER BY id LIMIT 1), 'Which specification is correct?', now()) RETURNING id`).Scan(&question); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO product_answers (question_id, user_id, body, is_staff)
		VALUES ($1, $2, 'The original official specification', true) RETURNING id`, question, actor).Scan(&answer); err != nil {
		t.Fatal(err)
	}
	cfg := p.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE admin")
		return err
	}
	rolePool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rolePool.Close)
	s := admin.NewStore(rolePool, fakeRefunder{}, nil, nil)
	visible, err := s.Questions(ctx)
	if err != nil || len(visible.Rows) != 0 {
		t.Fatalf("hidden question appears in visible queue: %+v, %v", visible.Rows, err)
	}
	hidden, err := s.Questions(ctx, true)
	if err != nil || len(hidden.Rows) != 1 || !hidden.Rows[0].Hidden || len(hidden.Rows[0].Replies) != 1 {
		t.Fatalf("hidden queue = %+v, %v", hidden.Rows, err)
	}
	if got := hidden.Rows[0].Replies[0]; got.ID != answer.String() || got.Body != "The original official specification" || !got.Staff {
		t.Errorf("official answer = %+v", got)
	}
	h := adminHandlerOver(rolePool, s)
	body := url.Values{"action": {"show"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/questions/"+question.String(), strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", question.String())
	res := httptest.NewRecorder()
	h.RequireStaff(h.AnswerQuestion)(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/questions?ok=1" {
		t.Fatalf("restore = %d, %s", res.Code, res.Body.String())
	}
	visible, err = s.Questions(ctx)
	if err != nil || len(visible.Rows) != 1 || visible.Rows[0].Hidden || len(visible.Rows[0].Replies) != 1 {
		t.Fatalf("restored queue = %+v, %v", visible.Rows, err)
	}
	if err := s.ShowQuestion(ctx, question.String()); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("duplicate show = %v, want not found", err)
	}
	var audits int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'question.show' AND entity_id = $1 AND actor_user_id = $2`, question, actor).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Errorf("restore left %d audit rows, want one", audits)
	}
	bad := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/questions/"+question.String(), strings.NewReader("action=answer&body="))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bad.SetPathValue("id", question.String())
	refused := httptest.NewRecorder()
	h.RequireStaff(h.AnswerQuestion)(refused, bad)
	if refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), `aria-invalid="true"`) {
		t.Errorf("empty answer = %d without its field error", refused.Code)
	}
}
