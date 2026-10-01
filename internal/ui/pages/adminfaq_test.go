package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/layouts"
)

// tagHolding is the opening tag that carries attr, so an assertion about one
// control cannot be satisfied by a neighbour's attributes.
func tagHolding(t *testing.T, page, attr string) string {
	t.Helper()
	at := strings.Index(page, attr)
	if at < 0 {
		t.Fatalf("page has no %s", attr)
	}
	start := strings.LastIndex(page[:at], "<")
	return page[start : at+strings.Index(page[at:], ">")]
}

// A refused edit comes back on the entry that was edited. It used to land in
// the add form's draft, so the error sat on an empty 新增 field, the stored
// entry kept its old text, and typing the missing question there published a
// second copy.
func TestARefusedFAQEditReturnsToItsOwnEntryNotTheAddForm(t *testing.T) {
	t.Parallel()
	v := &AdminFAQView{
		Rows: []AdminFAQEntry{
			{ID: "e1", Category: "會員", Question: "stored question", Answer: "stored answer"},
			{ID: "e2", Category: "運送", Question: "other question", Answer: "other answer"},
		},
		EditErrors: map[string]string{"question": "REFUSED-QUESTION"},
		Edit: AdminFAQEntry{
			ID: "e1", Category: "會員", Question: "", Answer: "edited answer",
		},
	}
	page := renderToString(t, AdminFAQ(layouts.Page{Title: "faq"}, v))

	if strings.Contains(tagHolding(t, page, `id="faq-question"`), "aria-invalid") {
		t.Error("the add form's question is marked invalid by an edit's refusal")
	}
	if strings.Contains(page, `id="faq-question-error"`) {
		t.Error("the add form carries the edit's error")
	}
	if strings.Contains(page, "edited answer") && strings.Count(page, "edited answer") != 1 {
		t.Error("the edited answer appears outside the entry's own form")
	}
	if !strings.Contains(tagHolding(t, page, `id="q-e1"`), "aria-invalid") {
		t.Error("the refused entry's question is not marked invalid")
	}
	if !strings.Contains(page, `id="faq-err-question-e1"`) || !strings.Contains(page, "REFUSED-QUESTION") {
		t.Error("the refused entry carries no reason beside it")
	}
	if !strings.Contains(page, "edited answer") || strings.Contains(page, "stored answer") {
		t.Error("the refused entry does not hold the submitted answer")
	}
	if strings.Contains(tagHolding(t, page, `id="q-e2"`), "aria-invalid") {
		t.Error("another entry is marked invalid")
	}
	if !strings.Contains(page, "other answer") {
		t.Error("another entry lost its stored text")
	}
}

// The add form still holds its own refusal.
func TestARefusedFAQAddStaysOnTheAddForm(t *testing.T) {
	t.Parallel()
	v := &AdminFAQView{
		Rows:   []AdminFAQEntry{{ID: "e1", Category: "會員", Question: "q", Answer: "a"}},
		Errors: map[string]string{"question": "REFUSED-QUESTION"},
		Draft:  AdminFAQEntry{Category: "會員", Answer: "typed answer"},
	}
	page := renderToString(t, AdminFAQ(layouts.Page{Title: "faq"}, v))
	if !strings.Contains(tagHolding(t, page, `id="faq-question"`), "aria-invalid") {
		t.Error("the add form's question is not marked invalid")
	}
	if strings.Contains(tagHolding(t, page, `id="q-e1"`), "aria-invalid") {
		t.Error("an add refusal marked a listed entry invalid")
	}
}
