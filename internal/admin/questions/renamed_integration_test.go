//go:build integration

package questions_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/questions"
	"github.com/koopa0/goen/internal/order/ordertest"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestStore_QuestionsRenamedStaff: staff A answers a question
// as the shop, then takes staff B's name. The answer still names A by address
// and the page never says B. A customer's answer is a different matter: it is
// labelled by the name that customer gave, which is theirs to change.
func TestStore_QuestionsRenamedStaff(t *testing.T) {
	p := admintest.Pool(t)
	suffix := uuid.NewString()[:8]
	ctx, idA, address := admintest.NamedStaff(t, p, "Actor "+suffix)
	colleague := "Colleague " + suffix
	admintest.NamedStaff(t, p, colleague)
	s := questions.NewStore(admintest.AdminRolePool(t, p))

	var question uuid.UUID
	if err := p.QueryRow(ctx, `INSERT INTO product_questions (product_id, body)
		VALUES ((SELECT id FROM products ORDER BY id LIMIT 1), 'Does it come in blue?') RETURNING id`).Scan(&question); err != nil {
		t.Fatalf("ask a question: %v", err)
	}
	if err := s.AnswerQuestion(ctx, question.String(), idA.String(), "Yes, in two blues."); err != nil {
		t.Fatalf("A answers as the shop: %v", err)
	}
	customer := ordertest.Customer(t, p)
	if _, err := p.Exec(ctx, `INSERT INTO product_answers (question_id, user_id, body, is_staff)
		VALUES ($1, $2, 'Mine is navy.', false)`, question, customer); err != nil {
		t.Fatalf("a customer answers: %v", err)
	}
	var customerName string
	if err := p.QueryRow(ctx, `SELECT full_name FROM users WHERE id = $1`, customer).Scan(&customerName); err != nil {
		t.Fatalf("read the customer's name: %v", err)
	}

	admintest.RenameUser(t, p, idA, colleague)
	view, err := s.Questions(ctx, questions.QueueVisible, "")
	if err != nil {
		t.Fatalf("Questions: %v", err)
	}
	if len(view.Rows) != 1 || len(view.Rows[0].Answers) != 2 {
		t.Fatalf("queue = %+v, want one question with two answers", view.Rows)
	}
	for _, a := range view.Rows[0].Answers {
		switch {
		case a.IsStaff && a.Author != address:
			t.Errorf("the shop's answer is by %q, want the address %q", a.Author, address)
		case !a.IsStaff && a.Author != customerName:
			t.Errorf("the customer's answer is by %q, want the name %q they gave", a.Author, customerName)
		}
	}
	var html bytes.Buffer
	if err := questions.Questions(frame.Page{}, view).Render(ctx, &html); err != nil {
		t.Fatalf("render the questions page: %v", err)
	}
	if !strings.Contains(html.String(), address) {
		t.Errorf("the page does not show %q", address)
	}
	if strings.Contains(html.String(), colleague) {
		t.Errorf("the page shows the name %q the staff member took", colleague)
	}
}
