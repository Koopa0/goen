package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func renderCustomer(t *testing.T, locale i18n.Locale, v CustomerView) string {
	t.Helper()
	var b strings.Builder
	if err := Customer(layouts.Page{Title: "customer"}, &v).Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
		t.Fatalf("Customer.Render: %v", err)
	}
	return b.String()
}

func TestCustomerTierShowsWhatIsLeftToTheNextTier(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		view      CustomerView
		locale    i18n.Locale
		sentence  string
		wantMeter bool
		wantFill  string
		wantLabel string
	}{
		{
			name:   "below a tier, zh",
			view:   CustomerView{WindowDays: 365, WindowSpendCents: 320000, NextTierName: "金卡", NextTierCents: 500000},
			locale: i18n.ZhHant, sentence: "近 365 天消費 NT$3,200，再消費 NT$1,800 可達 金卡。",
			wantMeter: true, wantFill: `width="64.00%"`,
			wantLabel: "NT$3,200 / NT$5,000",
		},
		{
			name:   "below a tier, en",
			view:   CustomerView{WindowDays: 365, WindowSpendCents: 320000, NextTierName: "Gold", NextTierCents: 500000},
			locale: i18n.En, sentence: "NT$3,200 spent in the last 365 days; NT$1,800 more reaches Gold.",
			wantMeter: true, wantFill: `width="64.00%"`,
			wantLabel: "NT$3,200 / NT$5,000",
		},
		{
			name:   "top tier shows the amount alone",
			view:   CustomerView{WindowDays: 365, WindowSpendCents: 900000},
			locale: i18n.En, sentence: "NT$9,000 spent in the last 365 days.",
		},
		{
			name:   "no tiers shows the amount alone",
			view:   CustomerView{WindowDays: 365, WindowSpendCents: 0},
			locale: i18n.ZhHant, sentence: "近 365 天消費 NT$0。",
		},
	} {
		got := renderCustomer(t, tt.locale, tt.view)
		if !strings.Contains(got, tt.sentence) {
			t.Errorf("%s: Customer page lacks the sentence %q", tt.name, tt.sentence)
		}
		if has := strings.Contains(got, `class="goen-chartmeter"`); has != tt.wantMeter {
			t.Errorf("%s: Customer page has a meter = %v, want %v", tt.name, has, tt.wantMeter)
		}
		if tt.wantMeter {
			for _, want := range []string{tt.wantFill, `goen-chartmeter__limit`, tt.wantLabel} {
				if !strings.Contains(got, want) {
					t.Errorf("%s: Customer page lacks %s", tt.name, want)
				}
			}
		}
	}
}
