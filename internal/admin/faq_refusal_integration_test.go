//go:build integration

package admin_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
)

// A refused FAQ edit answers 422 on the entry that was edited. Its text used
// to be re-rendered into the add form, where pressing 新增 published a second
// copy and left the original unedited.
func TestARefusedFAQEditAnswersOnTheEntryItself(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	category := "編輯分類-" + uuid.NewString()[:8]
	if errs, err := s.CreateFAQEntry(ctx, &admin.FAQForm{
		Category: category, Question: "原本的問題", Answer: "原本的答案",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("CreateFAQEntry: %v %v", err, errs)
	}
	view, err := s.FAQ(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for i := range view.Rows {
		if view.Rows[i].Category == category {
			id = view.Rows[i].ID
		}
	}
	if id == "" {
		t.Fatal("the entry is not listed")
	}

	body := url.Values{
		"action": {"save"}, "category": {category}, "question": {""},
		"answer": {"我改過的答案"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/faq/"+id, strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	adminHandlerOver(pool, s).EditFAQEntry(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	page := rec.Body.String()
	for _, want := range []string{`id="faq-err-question-` + id + `"`, "我改過的答案"} {
		if !strings.Contains(page, want) {
			t.Errorf("refusal page missing %s", want)
		}
	}
	if strings.Contains(page, `id="faq-question-error"`) {
		t.Error("the refusal landed on the add form")
	}
	if strings.Count(page, "我改過的答案") != 1 {
		t.Errorf("the edited answer appears %d times, want once, inside the entry's own form",
			strings.Count(page, "我改過的答案"))
	}
}
