package site

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
)

// TestFAQViewGroups: the FAQ groups its entries by category row.
func TestFAQViewGroups(t *testing.T) {
	t.Parallel()
	orders, shipping := uuid.New(), uuid.New()
	for _, tt := range []struct {
		name string
		rows []db.FAQEntriesRow
		want FAQView
	}{
		{name: "empty"},
		{
			name: "one category is one group",
			rows: []db.FAQEntriesRow{
				{CategoryID: orders, Category: "Orders", Question: "First?", Answer: "First."},
				{CategoryID: orders, Category: "Orders", Question: "Second?", Answer: "Second."},
			},
			want: FAQView{Groups: []FAQGroup{
				{Category: "Orders", Items: []FAQItem{
					{Question: "First?", Answer: "First."},
					{Question: "Second?", Answer: "Second."},
				}},
			}},
		},
		{
			// A Chinese-only heading and another heading's English name may read
			// the same to an English visitor; they are still two categories.
			name: "two categories reading the same keep separate groups",
			rows: []db.FAQEntriesRow{
				{CategoryID: orders, Category: "Help", Question: "Order?", Answer: "Order answer."},
				{CategoryID: shipping, Category: "Help", Question: "Shipping?", Answer: "Shipping answer."},
			},
			want: FAQView{Groups: []FAQGroup{
				{Category: "Help", Items: []FAQItem{{Question: "Order?", Answer: "Order answer."}}},
				{Category: "Help", Items: []FAQItem{{Question: "Shipping?", Answer: "Shipping answer."}}},
			}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, faqView(tt.rows)); diff != "" {
				t.Errorf("FAQ groups (-want +got):\n%s", diff)
			}
		})
	}
}
