//go:build integration

package questions_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/questions"
	"github.com/koopa0/goen/internal/keyset"
)

// TestStore_ShowQuestionAdminRole: the admin role reads answers and restores questions with its existing privileges.
func TestStore_ShowQuestionAdminRole(t *testing.T) {
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
	s := questions.NewStore(admintest.AdminRolePool(t, p))
	visible, err := s.Questions(ctx, questions.VisibleQuestions)
	if err != nil || len(visible.Rows) != 0 {
		t.Fatalf("hidden question appears in visible queue: %+v, %v", visible.Rows, err)
	}
	hidden, err := s.Questions(ctx, questions.HiddenQuestions)
	if err != nil || len(hidden.Rows) != 1 || !hidden.Rows[0].Hidden || len(hidden.Rows[0].Answers) != 1 {
		t.Fatalf("hidden queue = %+v, %v", hidden.Rows, err)
	}
	if got := hidden.Rows[0].Answers[0]; got.ID != answer.String() || got.Body != "The original official specification" || !got.IsStaff {
		t.Errorf("official answer = %+v", got)
	}
	log := slog.New(slog.DiscardHandler)
	h := questions.NewHandler(s, log)
	ac := admin.NewControl(log, nil)
	body := url.Values{"action": {"show"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/questions/"+question.String(), strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", question.String())
	res := httptest.NewRecorder()
	ac.RequireStaff(h.AnswerQuestion)(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/questions?ok=1" {
		t.Fatalf("restore = %d, %s", res.Code, res.Body.String())
	}
	visible, err = s.Questions(ctx, questions.VisibleQuestions)
	if err != nil || len(visible.Rows) != 1 || visible.Rows[0].Hidden || len(visible.Rows[0].Answers) != 1 {
		t.Fatalf("restored queue = %+v, %v", visible.Rows, err)
	}
	if err := s.ShowQuestion(ctx, question.String()); !errors.Is(err, questions.ErrNotFound) {
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

// TestStore_HideAnswer: an answer withdrawal is bound to its question and audited under the admin role.
func TestStore_HideAnswer(t *testing.T) {
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
	s := questions.NewStore(admintest.AdminRolePool(t, p))
	if err := s.HideAnswer(ctx, otherQuestion.String(), answer.String()); !errors.Is(err, questions.ErrNotFound) {
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
	h := questions.NewHandler(s, log)
	mux := http.NewServeMux()
	h.Routes(mux, admin.NewControl(log, nil))
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
	view, err := s.Questions(ctx, questions.VisibleQuestions)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range view.Rows {
		if row.ID != question.String() {
			continue
		}
		found = true
		if row.AnsweredByShop || row.AnswerCount != 0 || len(row.Answers) != 1 || !row.Answers[0].Hidden {
			t.Errorf("withdrawn answer queue row = %+v", row)
		}
	}
	if !found {
		t.Error("withdrawing the answer removed its question from the queue")
	}
	if err := s.HideAnswer(ctx, question.String(), answer.String()); !errors.Is(err, questions.ErrNotFound) {
		t.Errorf("HideAnswer(already hidden) = %v, want ErrNotFound", err)
	}
	if got := admintest.AuditRows(t, p, audit.ActionHideAnswer); got != 1 {
		t.Errorf("withdrawal audit rows = %d, want 1", got)
	}
}

// TestStore_QuestionsHiddenQueue: the hidden question queue pages every question, newest first.
func TestStore_QuestionsHiddenQueue(t *testing.T) {
	p := admintest.Pool(t)
	ctx := t.Context()
	const total = keyset.PageSize + 3
	if _, err := p.Exec(ctx, `INSERT INTO product_questions(id, product_id, body, hidden_at)
 SELECT ('99990000-0000-4000-8000-' || lpad(to_hex(n), 12, '0'))::uuid,
        (SELECT id FROM products ORDER BY id LIMIT 1), 'Hidden fixture ' || n,
        timestamptz '2028-01-01 00:00:00+08' + ((n + 1) / 2) * interval '1 minute'
 FROM generate_series(1, $1::integer) AS n`, total); err != nil {
		t.Fatal(err)
	}
	s := questions.NewStore(admintest.AdminRolePool(t, p))
	seen := map[string]bool{}
	after := ""
	for page := range 2 {
		view, err := s.Questions(ctx, questions.HiddenQuestions, after)
		if err != nil {
			t.Fatal(err)
		}
		wantSize := keyset.PageSize
		if page == 1 {
			wantSize = total - keyset.PageSize
		}
		if len(view.Rows) != wantSize {
			t.Fatalf("hidden page %d rows = %d, want %d", page, len(view.Rows), wantSize)
		}
		for i, row := range view.Rows {
			want := "Hidden fixture " + strconv.Itoa(total-page*keyset.PageSize-i)
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
		if err != nil || next == nil || next.Query().Get("hidden") != "1" || next.Query().Get(keyset.Param) == "" {
			t.Fatalf("hidden next URL = %q, %v", view.Next, err)
		}
		after = next.Query().Get(keyset.Param)
		visible, err := s.Questions(ctx, questions.VisibleQuestions, after)
		if err != nil || len(visible.Rows) != 0 || visible.PastEnd {
			t.Fatalf("hidden cursor escaped its queue: %+v, %v", visible, err)
		}
	}
}
