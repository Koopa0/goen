//go:build integration

package site

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
)

// TestFAQGroupsByCategoryRowInStaffOrder reads /faq from category rows whose
// position order is the reverse of their names' order, and two of which read
// "Help" to an English visitor. Grouping or ordering by the name, in SQL or in
// the handler, puts the headings out of order or merges the two "Help" ones.
// A rename is one UPDATE, and every entry in the category moves with it.
func TestFAQGroupsByCategoryRowInStaffOrder(t *testing.T) {
	pool := dbtest.Pool(t)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO faq_categories (id, name, name_en, position) VALUES
			('0f000001-0000-4000-8000-000000000001', 'B 說明', 'Help', 1),
			('0f000001-0000-4000-8000-000000000002', 'Help', NULL, 2),
			('0f000001-0000-4000-8000-000000000003', 'A 配送', 'Delivery', 3);
		INSERT INTO faq_entries (category_id, position, question, answer, question_en, answer_en)
		VALUES
			('0f000001-0000-4000-8000-000000000003', 5, '配送一？', '配送一。', 'Delivery one?', 'Delivery one.'),
			('0f000001-0000-4000-8000-000000000001', 20, '說明二？', '說明二。', NULL, NULL),
			('0f000001-0000-4000-8000-000000000002', 7, '求助？', '求助。', 'Help me?', 'Helped.'),
			('0f000001-0000-4000-8000-000000000001', 10, '說明一？', '說明一。', 'Help one?', 'Help one.'),
			('0f000001-0000-4000-8000-000000000003', 9, '配送二？', '配送二。', 'Delivery two?', 'Delivery two.')`)
	if err != nil {
		t.Fatalf("insert FAQ categories and entries: %v", err)
	}
	store := NewStore(pool)
	h := Handler{content: store, log: slog.New(slog.DiscardHandler)}

	want := func(locale i18n.Locale, first, firstEn string) FAQView {
		zh := locale == i18n.ZhHant
		pick := func(zhText, enText string) string {
			if zh {
				return zhText
			}
			return enText
		}
		return FAQView{Groups: []FAQGroup{
			{Category: pick(first, firstEn), Items: []FAQItem{
				{Question: pick("說明一？", "Help one?"), Answer: pick("說明一。", "Help one.")},
				// No English: the Chinese stands in, in either locale.
				{Question: "說明二？", Answer: "說明二。"},
			}},
			{Category: "Help", Items: []FAQItem{
				{Question: pick("求助？", "Help me?"), Answer: pick("求助。", "Helped.")},
			}},
			{Category: pick("A 配送", "Delivery"), Items: []FAQItem{
				{Question: pick("配送一？", "Delivery one?"), Answer: pick("配送一。", "Delivery one.")},
				{Question: pick("配送二？", "Delivery two?"), Answer: pick("配送二。", "Delivery two.")},
			}},
		}}
	}

	check := func(t *testing.T, locale i18n.Locale, view FAQView) {
		t.Helper()
		ctx := i18n.WithLocale(t.Context(), locale)
		rows, err := store.FAQEntries(ctx)
		if err != nil {
			t.Fatalf("FAQEntries: %v", err)
		}
		if diff := cmp.Diff(view, faqView(rows)); diff != "" {
			t.Errorf("FAQ groups (-want +got):\n%s", diff)
		}

		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/faq", http.NoBody)
		res := httptest.NewRecorder()
		h.FAQ(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", res.Code)
		}
		body := res.Body.String()
		if count := strings.Count(body, `class="goen-doc__section"`); count != len(view.Groups) {
			t.Fatalf("rendered %d sections, want %d", count, len(view.Groups))
		}
		for i, group := range view.Groups {
			section := strings.Index(body, `<h2>`+group.Category+`</h2>`)
			if section < 0 {
				t.Fatalf("group %d heading %q missing or out of order", i, group.Category)
			}
			body = body[section+1:]
			for _, item := range group.Items {
				question := strings.Index(body, `<summary>`+item.Question+`</summary>`)
				if question < 0 {
					t.Fatalf("question %q missing or out of order", item.Question)
				}
				body = body[question+1:]
			}
		}
	}

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			check(t, locale, want(locale, "B 說明", "Help"))
		})
	}

	if _, err := pool.Exec(t.Context(), `
		UPDATE faq_categories SET name = 'C 問答', name_en = 'Questions'
		WHERE id = '0f000001-0000-4000-8000-000000000001'`); err != nil {
		t.Fatalf("rename the category: %v", err)
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag()+"/renamed", func(t *testing.T) {
			check(t, locale, want(locale, "C 問答", "Questions"))
		})
	}
}
