package site

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/db"
)

func TestFAQGroupsUseCanonicalCategories(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		rows []db.FAQEntriesRow
		want FAQView
	}{
		{name: "empty"},
		{
			name: "partial translations keep one group",
			rows: []db.FAQEntriesRow{
				{CanonicalCategory: "Orders", Category: "Orders", Question: "First?", Answer: "First."},
				{CanonicalCategory: "Orders", Category: "Translated orders", Question: "Second?", Answer: "Second."},
			},
			want: FAQView{Groups: []FAQGroup{
				{Category: "Orders", Items: []FAQItem{
					{Question: "First?", Answer: "First."},
					{Question: "Second?", Answer: "Second."},
				}},
			}},
		},
		{
			name: "colliding translations keep separate groups",
			rows: []db.FAQEntriesRow{
				{CanonicalCategory: "Orders", Category: "Help", Question: "Order?", Answer: "Order answer."},
				{CanonicalCategory: "Shipping", Category: "Help", Question: "Shipping?", Answer: "Shipping answer."},
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
