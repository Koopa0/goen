package policy

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestSeveralDeliverySurchargesReadAsOneList(t *testing.T) {
	t.Parallel()
	m := ShippingMethod{Surcharges: []ZoneSurcharge{{Name: "Outlying islands", Cents: 10000}, {Name: "Green Island", Cents: 8000}}}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if got, want := m.SurchargeText(ctx), "Outlying islands: NT$100 extra, Green Island: NT$80 extra"; got != want {
		t.Errorf("SurchargeText = %q, want %q", got, want)
	}
}

func TestThePolicyFooterReadsAsOneSentence(t *testing.T) {
	t.Parallel()
	for loc, want := range map[i18n.Locale]string{
		i18n.ZhHant: `還有問題？<a href="/contact">聯絡我們</a>，或看看<a href="/faq">常見問題</a>。`,
		i18n.En:     `Still have a question? <a href="/contact">Get in touch</a>, or read the <a href="/faq">FAQ</a>.`,
	} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		if err := Policy(layouts.Page{Title: "p"}, PolicyDoc{}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), want) {
			t.Errorf("%v: the policy footer does not read %q", loc, want)
		}
	}
}
