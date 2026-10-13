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
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestFAQTranslationsKeepCanonicalGroupsAndStaffOrder(t *testing.T) {
	pool := dbtest.Pool(t)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO faq_entries (category, category_en, position, question, answer)
		VALUES
			('B canonical', 'Shared', 5, 'B first?', 'B first.'),
			('A canonical', 'Later translation', 30, 'A third?', 'A third.'),
			('C canonical', NULL, 10, 'C second?', 'C second.'),
			('A canonical', NULL, 10, 'A first?', 'A first.'),
			('B canonical', 'Shared', 15, 'B second?', 'B second.'),
			('A canonical', 'Shared', 20, 'A second?', 'A second.'),
			('C canonical', NULL, 5, 'C first?', 'C first.')`)
	if err != nil {
		t.Fatalf("insert FAQ entries: %v", err)
	}
	store := NewStore(pool)
	h := Handler{content: store, log: slog.New(slog.DiscardHandler)}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			rows, err := store.FAQEntries(ctx)
			if err != nil {
				t.Fatalf("FAQEntries: %v", err)
			}
			labels := []string{"A canonical", "B canonical", "C canonical"}
			if locale == i18n.En {
				labels[0], labels[1] = "Shared", "Shared"
			}
			want := pages.FAQView{Groups: []pages.FAQGroup{
				{Category: labels[0], Items: []pages.FAQItem{
					{Question: "A first?", Answer: "A first."},
					{Question: "A second?", Answer: "A second."},
					{Question: "A third?", Answer: "A third."},
				}},
				{Category: labels[1], Items: []pages.FAQItem{
					{Question: "B first?", Answer: "B first."},
					{Question: "B second?", Answer: "B second."},
				}},
				{Category: labels[2], Items: []pages.FAQItem{
					{Question: "C first?", Answer: "C first."},
					{Question: "C second?", Answer: "C second."},
				}},
			}}
			if diff := cmp.Diff(want, faqView(rows)); diff != "" {
				t.Errorf("FAQ groups (-want +got):\n%s", diff)
			}
			for i, row := range rows {
				group := 0
				if i >= 3 {
					group = 1
				}
				if i >= 5 {
					group = 2
				}
				if row.Category != labels[group] {
					t.Errorf("entry %d label = %q, want %q", i, row.Category, labels[group])
				}
			}

			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/faq", http.NoBody)
			res := httptest.NewRecorder()
			h.FAQ(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", res.Code)
			}
			body := res.Body.String()
			if count := strings.Count(body, `class="goen-doc__section"`); count != len(want.Groups) {
				t.Fatalf("rendered %d sections, want %d", count, len(want.Groups))
			}
			for i, group := range want.Groups {
				section := strings.Index(body, `<h2>`+group.Category+`</h2>`)
				if section < 0 {
					t.Fatalf("group %d heading %q missing", i, group.Category)
				}
				body = body[section:]
				for _, item := range group.Items {
					question := strings.Index(body, `<summary>`+item.Question+`</summary>`)
					if question < 0 {
						t.Fatalf("question %q missing or out of order", item.Question)
					}
					body = body[question+1:]
				}
			}
		})
	}
}
