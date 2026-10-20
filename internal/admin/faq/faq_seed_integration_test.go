//go:build integration

package faq_test

import (
	"context"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/site"
)

const (
	invoiceFAQQuestion = "發票怎麼開立？"
	invoiceFAQZh       = "結帳時可以選擇會員載具、手機條碼載具或公司統編，付款完成時系統會依你的選擇自動開立電子發票。若本店尚未設定綠界電子發票，則不會開立。"
	invoiceFAQEn       = "At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and the electronic invoice is issued automatically against that choice when your payment completes. If the shop has not set up its ECPay credentials, no invoice is issued."
)

// TestSeededInvoiceFAQMatchesTheWiredIssuer holds the seeded invoice FAQ, as
// /faq reads it, to Gateway.Issue: deployment-gated, in both locales.
func TestSeededInvoiceFAQMatchesTheWiredIssuer(t *testing.T) {
	ctx := t.Context()
	siteStore := site.NewStore(pool)

	if _, err := issuer.NewGateway("", "", "", ""); err != nil {
		t.Fatalf("an empty configuration must be legal: %v", err)
	}

	assertInvoiceFAQLocales(t, siteStore, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, siteStore, ctx)
}

func invoiceFAQAnswers(t *testing.T, storefront *site.Store, ctx context.Context) (zh, en string) {
	t.Helper()
	return faqAnswer(t, storefront, ctx, i18n.ZhHant, invoiceFAQQuestion),
		faqAnswer(t, storefront, ctx, i18n.En, "How is my invoice issued?")
}

func faqAnswer(t *testing.T, storefront *site.Store, ctx context.Context, loc i18n.Locale, question string) string {
	t.Helper()
	rows, err := storefront.FAQEntries(i18n.WithLocale(ctx, loc))
	if err != nil {
		t.Fatalf("FAQEntries(%s): %v", loc, err)
	}
	for i := range rows {
		if rows[i].Question == question {
			return rows[i].Answer
		}
	}
	t.Fatalf("/faq has no %q in %s", question, loc)
	return ""
}

func assertInvoiceFAQLocales(t *testing.T, storefront *site.Store, ctx context.Context, wantZh, wantEn string) {
	t.Helper()
	zh, en := invoiceFAQAnswers(t, storefront, ctx)
	if zh != wantZh {
		t.Errorf("Chinese invoice FAQ = %q, want %q", zh, wantZh)
	}
	if en != wantEn {
		t.Errorf("English invoice FAQ = %q, want %q", en, wantEn)
	}
	if strings.Contains(zh, "尚未完成") || strings.Contains(en, "not built yet") {
		t.Errorf("published invoice FAQ still calls the issuer unfinished: zh=%q en=%q", zh, en)
	}
	if strings.Contains(zh, "已成功開立") || strings.Contains(en, "successfully issued") {
		t.Errorf("published invoice FAQ invents a successful filing: zh=%q en=%q", zh, en)
	}
	if !strings.Contains(zh, "綠界") || !strings.Contains(en, "ECPay") {
		t.Errorf("published invoice FAQ does not name the issuer: zh=%q en=%q", zh, en)
	}
}

func assertStatutoryReturnFAQUntouched(t *testing.T, storefront *site.Store, ctx context.Context) {
	t.Helper()
	zh := faqAnswer(t, storefront, ctx, i18n.ZhHant, "退貨要付運費嗎？")
	en := faqAnswer(t, storefront, ctx, i18n.En, "Who pays return postage?")
	if !strings.Contains(zh, "退貨運費由 goen 負擔") {
		t.Errorf("statutory return FAQ was edited: %q", zh)
	}
	if !strings.Contains(en, "Rescinding within seven days") {
		t.Errorf("statutory English return FAQ was edited: %q", en)
	}
}

const (
	refundFAQQuestion = "退款什麼時候會收到？"
	refundFAQZh       = "退貨經審核同意後，系統依原付款組成退回：卡款立刻向 Stripe 發出退款，購物金退回餘額。卡款入帳時間依發卡銀行而定，通常是數個工作天；購物金退回後可立刻使用。"
	refundFAQEn       = "As soon as a return is approved we pay it back the way you paid: the card share through Stripe, store credit back to your balance. When a card refund lands depends on your card issuer, usually a few working days; credit is available again at once."
)

// TestTheSeededRefundFAQNamesThePaymentComposition holds the seeded refund
// FAQ, as /faq reads it, to the way a return pays back: card and credit.
func TestTheSeededRefundFAQNamesThePaymentComposition(t *testing.T) {
	assertRefundFAQLocales(t, site.NewStore(pool), t.Context(), refundFAQZh, refundFAQEn)
}

func refundFAQAnswers(t *testing.T, storefront *site.Store, ctx context.Context) (zh, en string) {
	t.Helper()
	return faqAnswer(t, storefront, ctx, i18n.ZhHant, refundFAQQuestion),
		faqAnswer(t, storefront, ctx, i18n.En, "When will I get my refund?")
}

func assertRefundFAQLocales(t *testing.T, storefront *site.Store, ctx context.Context, wantZh, wantEn string) {
	t.Helper()
	zh, en := refundFAQAnswers(t, storefront, ctx)
	if zh != wantZh {
		t.Errorf("Chinese refund FAQ = %q, want %q", zh, wantZh)
	}
	if en != wantEn {
		t.Errorf("English refund FAQ = %q, want %q", en, wantEn)
	}
}
