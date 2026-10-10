package site

import (
	"fmt"
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestPolicyPaymentDeadlines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "下單後請在 %[2]s 分鐘內開始付款，並在 %[1]s 分鐘內完成。逾時未付款的訂單會自動取消：商品回到架上，不會收取任何款項，使用的購物金也會退回。"},
		{i18n.En, "Start the payment within %[2]s minutes of ordering and finish it within %[1]s. An order still unpaid after that is cancelled automatically: the goods go back on the shelf, nothing is charged, and any store credit you applied is returned."},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			var out strings.Builder
			if err := pages.Policy(layouts.Page{}, policies["payment"].For(tc.locale)).Render(ctx, &out); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(tc.want, pages.HoldMinutesText(), pages.PayStartMinutesText())
			if !strings.Contains(html.UnescapeString(out.String()), want) {
				t.Errorf("payment policy omits %q", want)
			}
		})
	}
}

func TestShippingPolicyDeadlinesAndSurcharge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale           i18n.Locale
		deadline, charge string
	}{
		{i18n.ZhHant, "下單後請在 %[2]s 分鐘內開始付款，並在 %[1]s 分鐘內完成。逾時未付款的訂單會自動取消，商品回到架上，不會收取任何款項。", "%s，滿額免運也照收"},
		{i18n.En, "Start the payment within %[2]s minutes of ordering and finish it within %[1]s. An order still unpaid after that is cancelled automatically: the goods go back on the shelf and nothing is charged.", "%s, even when the order ships free"},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			method := pages.ShippingMethod{
				Name: "Delivery", FeeCents: 6000, FreeOverCents: 100000,
				Surcharges: []pages.ZoneSurcharge{{Name: "Outlying islands", Cents: 10000}},
			}
			var out strings.Builder
			if err := pages.Shipping(layouts.Page{}, pages.ShippingView{Methods: []pages.ShippingMethod{method}}).Render(ctx, &out); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				fmt.Sprintf(tc.deadline, pages.HoldMinutesText(), pages.PayStartMinutesText()),
				fmt.Sprintf(tc.charge, method.SurchargeText(ctx)),
			} {
				if !strings.Contains(html.UnescapeString(out.String()), want) {
					t.Errorf("shipping policy omits %q", want)
				}
			}
		})
	}
}

func TestWarrantyRegistrationNamesTheOrderPage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "登入後，在該商品的訂單頁登錄保固；登錄後送修時不需要再找收據。"},
		{i18n.En, "Sign in and register the unit from the order it came on — once it is registered you will not need the receipt to claim."},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			var out strings.Builder
			if err := pages.Policy(layouts.Page{}, policies["warranty"].For(tc.locale)).Render(ctx, &out); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(html.UnescapeString(out.String()), tc.want) {
				t.Errorf("warranty policy omits %q", tc.want)
			}
		})
	}
}

func TestPolicyCopyAvoidsSystemJargon(t *testing.T) {
	t.Parallel()
	for page, doc := range policies {
		for _, locale := range i18n.Locales() {
			t.Run(page+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				var out strings.Builder
				if err := pages.Policy(layouts.Page{}, doc.For(locale)).Render(i18n.WithLocale(t.Context(), locale), &out); err != nil {
					t.Fatal(err)
				}
				for _, word := range []string{"系統本身", "店儲", "額度", "argon2id", "簽章", "User-Agent"} {
					if strings.Contains(strings.ToLower(out.String()), strings.ToLower(word)) {
						t.Errorf("policy still contains %q", word)
					}
				}
			})
		}
	}
}
