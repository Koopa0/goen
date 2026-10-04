//go:build integration

package feedback_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/feedback"
	"github.com/koopa0/goen/internal/web"
)

func TestAdminReadsAnswersAndRestoresQuestionsWithExistingPrivileges(t *testing.T) {
	p := admintest.Pool(t)
	ctx, actor := admintest.StaffContext(t, p)
	var question, answer uuid.UUID
	if err := p.QueryRow(ctx, `INSERT INTO product_questions (product_id, body, hidden_at)
		VALUES ((SELECT id FROM products ORDER BY id LIMIT 1), 'Which specification is correct?', now()) RETURNING id`).Scan(&question); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `INSERT INTO product_answers (question_id, user_id, body, is_staff)
		VALUES ($1, $2, 'The original official specification', true) RETURNING id`, question, actor).Scan(&answer); err != nil {
		t.Fatal(err)
	}
	s := feedback.NewStore(feedbackAdminPool(t, p))
	visible, err := s.Questions(ctx, feedback.VisibleQuestions)
	if err != nil || len(visible.Rows) != 0 {
		t.Fatalf("hidden question appears in visible queue: %+v, %v", visible.Rows, err)
	}
	hidden, err := s.Questions(ctx, feedback.HiddenQuestions)
	if err != nil || len(hidden.Rows) != 1 || !hidden.Rows[0].Hidden || len(hidden.Rows[0].Answers) != 1 {
		t.Fatalf("hidden queue = %+v, %v", hidden.Rows, err)
	}
	if got := hidden.Rows[0].Answers[0]; got.ID != answer.String() || got.Body != "The original official specification" || !got.IsStaff {
		t.Errorf("official answer = %+v", got)
	}
	log := slog.New(slog.DiscardHandler)
	h := feedback.NewHandler(s, log)
	ac := access.New(log, nil)
	body := url.Values{"action": {"show"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/questions/"+question.String(), strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", question.String())
	res := httptest.NewRecorder()
	ac.RequireStaff(h.AnswerQuestion)(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/questions?ok=1" {
		t.Fatalf("restore = %d, %s", res.Code, res.Body.String())
	}
	visible, err = s.Questions(ctx, feedback.VisibleQuestions)
	if err != nil || len(visible.Rows) != 1 || visible.Rows[0].Hidden || len(visible.Rows[0].Answers) != 1 {
		t.Fatalf("restored queue = %+v, %v", visible.Rows, err)
	}
	if err := s.ShowQuestion(ctx, question.String()); !errors.Is(err, feedback.ErrNotFound) {
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
	ac.RequireStaff(h.AnswerQuestion)(refused, bad)
	if refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), `aria-invalid="true"`) {
		t.Errorf("empty answer = %d without its field error", refused.Code)
	}
}

func TestAnswerWithdrawalIsQuestionBoundAndAuditedUnderTheAdminRole(t *testing.T) {
	p := admintest.Pool(t)
	ctx, actor := admintest.StaffContext(t, p)
	var question, otherQuestion, answer uuid.UUID
	for _, id := range []*uuid.UUID{&question, &otherQuestion} {
		if err := p.QueryRow(ctx, `INSERT INTO product_questions(product_id, body)
 VALUES ((SELECT id FROM products ORDER BY id LIMIT 1), 'Withdrawal fixture') RETURNING id`).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.QueryRow(ctx, `INSERT INTO product_answers(question_id, user_id, body, is_staff)
 VALUES ($1, $2, 'Official withdrawal fixture', true) RETURNING id`, question, actor).Scan(&answer); err != nil {
		t.Fatal(err)
	}
	s := feedback.NewStore(feedbackAdminPool(t, p))
	if err := s.HideAnswer(ctx, otherQuestion.String(), answer.String()); !errors.Is(err, feedback.ErrNotFound) {
		t.Errorf("HideAnswer(other question) = %v, want ErrNotFound", err)
	}
	if err := s.HideAnswer(t.Context(), question.String(), answer.String()); !errors.Is(err, audit.ErrNoActor) {
		t.Errorf("HideAnswer(no actor) = %v, want ErrNoActor", err)
	}
	var hidden bool
	if err := p.QueryRow(ctx, `SELECT hidden_at IS NOT NULL FROM product_answers WHERE id = $1`, answer).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if hidden || admintest.AuditRows(t, p, audit.ActionHideAnswer) != 0 {
		t.Fatal("refused withdrawal changed the answer or its audit")
	}
	log := slog.New(slog.DiscardHandler)
	h := feedback.NewHandler(s, log)
	mux := http.NewServeMux()
	h.Routes(mux, access.New(log, nil))
	body := url.Values{"action": {"hide_answer"}, "answer_id": {answer.String()}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/questions/"+question.String(), strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("withdrawal status = %d, want 303: %s", res.Code, res.Body.String())
	}
	var recordedActor uuid.UUID
	var recordedQuestion string
	if err := p.QueryRow(ctx, `SELECT hidden_at IS NOT NULL FROM product_answers WHERE id = $1`, answer).Scan(&hidden); err != nil {
		t.Fatal(err)
	}
	if !hidden {
		t.Error("successful withdrawal left the answer visible")
	}
	if err := p.QueryRow(ctx, `SELECT actor_user_id, after->>'question_id' FROM audit_events
 WHERE action = 'answer.hide' AND entity_id = $1`, answer).Scan(&recordedActor, &recordedQuestion); err != nil {
		t.Fatal(err)
	}
	if recordedActor != actor || recordedQuestion != question.String() {
		t.Errorf("withdrawal audit = %s/%q, want %s/%q", recordedActor, recordedQuestion, actor, question.String())
	}
	view, err := s.Questions(ctx, feedback.VisibleQuestions)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Rows {
		if row.ID == question.String() && (row.AnsweredByShop || row.AnswerCount != 0 || len(row.Answers) != 1 || !row.Answers[0].Hidden) {
			t.Errorf("withdrawn answer queue row = %+v", row)
		}
	}
	if err := s.HideAnswer(ctx, question.String(), answer.String()); !errors.Is(err, feedback.ErrNotFound) {
		t.Errorf("HideAnswer(already hidden) = %v, want ErrNotFound", err)
	}
	if got := admintest.AuditRows(t, p, audit.ActionHideAnswer); got != 1 {
		t.Errorf("withdrawal audit rows = %d, want 1", got)
	}
}

func TestHiddenQuestionQueuePagesEveryQuestionNewestFirst(t *testing.T) {
	p := admintest.Pool(t)
	ctx := t.Context()
	const total = 53
	if _, err := p.Exec(ctx, `INSERT INTO product_questions(product_id, body, hidden_at)
 SELECT (SELECT id FROM products ORDER BY id LIMIT 1), 'Hidden fixture ' || n,
        timestamptz '2028-01-01 00:00:00+08' + n * interval '1 minute'
 FROM generate_series(1, $1::integer) AS n`, total); err != nil {
		t.Fatal(err)
	}
	s := feedback.NewStore(feedbackAdminPool(t, p))
	seen := map[string]bool{}
	after := ""
	for page := 0; page < 2; page++ {
		view, err := s.Questions(ctx, feedback.HiddenQuestions, after)
		if err != nil {
			t.Fatal(err)
		}
		wantSize := web.PageSize
		if page == 1 {
			wantSize = total - web.PageSize
		}
		if len(view.Rows) != wantSize {
			t.Fatalf("hidden page %d rows = %d, want %d", page, len(view.Rows), wantSize)
		}
		for i, row := range view.Rows {
			want := "Hidden fixture " + strconv.Itoa(total-page*web.PageSize-i)
			if row.Body != want || !row.Hidden || seen[row.ID] {
				t.Errorf("hidden page %d row %d = %+v, want unique %q", page, i, row, want)
			}
			seen[row.ID] = true
		}
		if page == 1 {
			if view.Next != "" || view.First != "/admin/questions?hidden=1" {
				t.Errorf("last hidden page bound = %+v, want only its first-page link", view.Bound)
			}
			break
		}
		next, err := url.Parse(view.Next)
		if err != nil || next == nil || next.Query().Get("hidden") != "1" || next.Query().Get(web.KeysetParam) == "" {
			t.Fatalf("hidden next URL = %q, %v", view.Next, err)
		}
		after = next.Query().Get(web.KeysetParam)
		visible, err := s.Questions(ctx, feedback.VisibleQuestions, after)
		if err != nil || len(visible.Rows) != 0 || visible.PastEnd {
			t.Fatalf("hidden cursor escaped its queue: %+v, %v", visible, err)
		}
	}
}

func feedbackAdminPool(t *testing.T, p *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg := p.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE admin")
		return err
	}
	rolePool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rolePool.Close)
	return rolePool
}
