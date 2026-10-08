//go:build integration

package content_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/invoice"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/site"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

const (
	invoiceFAQQuestion = "發票怎麼開立？"
	invoiceFAQZh       = "結帳時可以選擇會員載具、手機條碼載具或公司統編，付款完成時系統會依你的選擇自動開立電子發票。若本店尚未設定綠界電子發票，則不會開立。"
	invoiceFAQEn       = "At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and the electronic invoice is issued automatically against that choice when your payment completes. If the shop has not set up its ECPay credentials, no invoice is issued."
	staleInvoiceFAQZh  = "結帳時可以選擇會員載具、手機條碼載具或公司統編,系統會記錄您的選擇。電子發票的實際開立需要串接加值中心,這部分尚未完成。"
	staleInvoiceFAQEn  = "At checkout you can choose a member carrier, a mobile barcode carrier, or a company tax ID, and we record your choice. Actually issuing the electronic invoice needs an integration with a certified provider, which is not built yet."
)

// TestSeededInvoiceFAQMatchesTheWiredIssuer holds the published invoice FAQ
// to Gateway.Issue: a fresh seed, the shipped repair file on a kept stale
// row, and the back-office form all say the same deployment-gated thing,
// in both locales.
func TestSeededInvoiceFAQMatchesTheWiredIssuer(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	siteStore := site.NewStore(pool)

	if _, err := invoice.NewGateway("", "", "", ""); err != nil {
		t.Fatalf("an empty configuration must be legal: %v", err)
	}

	assertInvoiceFAQLocales(t, siteStore, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, siteStore, ctx)

	catalog, err := os.ReadFile("../../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read catalogue seed: %v", err)
	}
	repair, err := os.ReadFile("../../../seed/repair_invoice_faq.sql")
	if err != nil {
		t.Fatalf("read shipped invoice FAQ repair: %v", err)
	}

	isolated := dbtest.Pool(t)
	if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
		t.Fatalf("seed isolated catalogue: %v", loadErr)
	}
	plantStaleInvoiceFAQ(t, ctx, isolated)
	isolatedContent := site.NewStore(isolated)
	zh, en := invoiceFAQAnswers(t, isolatedContent, ctx)
	if !strings.Contains(zh, "尚未完成") || !strings.Contains(en, "not built yet") {
		t.Fatalf("the planted stale answers did not reach /faq: zh=%q en=%q", zh, en)
	}
	conn, err := isolated.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire isolated connection: %v", err)
	}
	if _, seedErr := conn.Exec(ctx, string(catalog)); seedErr == nil {
		conn.Release()
		t.Fatal("the catalogue seed succeeded against a kept database; " +
			"db-seed cannot be the repair path")
	}
	if _, rollErr := conn.Exec(ctx, "ROLLBACK"); rollErr != nil {
		conn.Release()
		t.Fatalf("rollback failed catalogue re-seed: %v", rollErr)
	}
	conn.Release()
	zh, en = invoiceFAQAnswers(t, isolatedContent, ctx)
	if !strings.Contains(zh, "尚未完成") || !strings.Contains(en, "not built yet") {
		t.Fatal("the failed catalogue re-seed changed the kept invoice FAQ")
	}
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped invoice FAQ repair: %v", repairErr)
	}
	assertInvoiceFAQLocales(t, isolatedContent, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, isolatedContent, ctx)

	t.Cleanup(func() {
		if _, restoreErr := pool.Exec(context.WithoutCancel(ctx), string(repair)); restoreErr != nil {
			t.Errorf("restore invoice FAQ: %v", restoreErr)
		}
	})
	plantStaleInvoiceFAQ(t, ctx, pool)
	var entry admin.FAQEntry
	view, err := content.NewStore(pool).FAQ(ctx)
	if err != nil {
		t.Fatalf("FAQ: %v", err)
	}
	for i := range view.Rows {
		if view.Rows[i].Question == invoiceFAQQuestion {
			entry = view.Rows[i]
			break
		}
	}
	if entry.ID == "" {
		t.Fatal("the invoice FAQ row is not in the back office")
	}
	if errs, updErr := content.NewStore(pool).UpdateFAQEntry(ctx, &content.FAQForm{
		ID: entry.ID, Category: entry.Category,
		Question: invoiceFAQQuestion, Answer: invoiceFAQZh,
		CategoryEn: entry.CategoryEn, QuestionEn: entry.QuestionEn,
		AnswerEn: invoiceFAQEn,
	}); updErr != nil || len(errs) > 0 {
		t.Fatalf("UpdateFAQEntry: %v %v", updErr, errs)
	}
	assertInvoiceFAQLocales(t, siteStore, ctx, invoiceFAQZh, invoiceFAQEn)
	assertStatutoryReturnFAQUntouched(t, siteStore, ctx)
}

// TestRepairInvoiceFAQPreservesEditedLocales runs the shipped repair file against
// kept rows and rewrites only a locale that still exactly matches the stale copy.
func TestRepairInvoiceFAQPreservesEditedLocales(t *testing.T) {
	ctx := t.Context()
	catalog, err := os.ReadFile("../../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read catalogue seed: %v", err)
	}
	repair, err := os.ReadFile("../../../seed/repair_invoice_faq.sql")
	if err != nil {
		t.Fatalf("read shipped invoice FAQ repair: %v", err)
	}

	cases := []struct {
		name           string
		zh, en         string
		wantZh, wantEn string
	}{
		{
			name: "both stale",
			zh:   staleInvoiceFAQZh, en: staleInvoiceFAQEn,
			wantZh: invoiceFAQZh, wantEn: invoiceFAQEn,
		},
		{
			name: "custom Chinese stale English",
			zh:   "店家自訂發票說明", en: staleInvoiceFAQEn,
			wantZh: "店家自訂發票說明", wantEn: invoiceFAQEn,
		},
		{
			name: "stale Chinese custom English",
			zh:   staleInvoiceFAQZh, en: "Merchant invoice instructions",
			wantZh: invoiceFAQZh, wantEn: "Merchant invoice instructions",
		},
		{
			name: "both custom",
			zh:   "店家自訂發票說明", en: "Merchant invoice instructions",
			wantZh: "店家自訂發票說明", wantEn: "Merchant invoice instructions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolated := dbtest.Pool(t)
			if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
				t.Fatalf("seed catalogue: %v", loadErr)
			}
			if _, plantErr := isolated.Exec(ctx, `
				UPDATE faq_entries SET answer = $1, answer_en = $2
				WHERE question = $3`, tc.zh, tc.en, invoiceFAQQuestion); plantErr != nil {
				t.Fatalf("plant invoice FAQ: %v", plantErr)
			}
			if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
				t.Fatalf("apply shipped invoice FAQ repair: %v", repairErr)
			}
			storefront := site.NewStore(isolated)
			gotZh, gotEn := invoiceFAQAnswers(t, storefront, ctx)
			if gotZh != tc.wantZh {
				t.Errorf("Chinese = %q, want %q", gotZh, tc.wantZh)
			}
			if gotEn != tc.wantEn {
				t.Errorf("English = %q, want %q", gotEn, tc.wantEn)
			}
			assertStatutoryReturnFAQUntouched(t, storefront, ctx)
		})
	}

	t.Run("absent row", func(t *testing.T) {
		isolated := dbtest.Pool(t)
		if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
			t.Fatalf("seed catalogue: %v", loadErr)
		}
		if _, delErr := isolated.Exec(ctx,
			`DELETE FROM faq_entries WHERE question = $1`, invoiceFAQQuestion); delErr != nil {
			t.Fatalf("delete invoice FAQ: %v", delErr)
		}
		if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
			t.Fatalf("apply shipped invoice FAQ repair: %v", repairErr)
		}
		var count int
		if err := isolated.QueryRow(ctx,
			`SELECT count(*) FROM faq_entries WHERE question = $1`, invoiceFAQQuestion).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Error("the repair recreated the invoice FAQ row")
		}
		storefront := site.NewStore(isolated)
		assertStatutoryReturnFAQUntouched(t, storefront, ctx)
	})
}

func plantStaleInvoiceFAQ(t *testing.T, ctx context.Context, p *pgxpool.Pool) {
	t.Helper()
	if _, err := p.Exec(ctx, `
		UPDATE faq_entries
		SET answer = $1, answer_en = $2
		WHERE question = $3`,
		staleInvoiceFAQZh, staleInvoiceFAQEn, invoiceFAQQuestion); err != nil {
		t.Fatalf("plant stale invoice FAQ: %v", err)
	}
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
	refundFAQQuestion   = "退款什麼時候會收到？"
	refundFAQZh         = "退貨經審核同意後，系統依原付款組成退回：卡款立刻向 Stripe 發出退款，購物金退回餘額。卡款入帳時間依發卡銀行而定，通常是數個工作天；購物金退回後可立刻使用。"
	shippedRefundFAQZh  = "退貨經審核同意後，系統依原付款組成退回：卡款立刻向 Stripe 發出退款，店儲退回購物金。卡款入帳時間依發卡銀行而定，通常是數個工作天；購物金退回後可立刻使用。"
	refundFAQEn         = "As soon as a return is approved we pay it back the way you paid: the card share through Stripe, store credit back to your balance. When a card refund lands depends on your card issuer, usually a few working days; credit is available again at once."
	previousRefundFAQZh = "退貨經審核同意後，系統依原付款組成退回：卡款立刻向 Stripe 發出退款，店儲退回購物金。卡款入帳時間依發卡銀行而定，通常是數個工作天；額度退回後可立刻使用。"
	staleRefundFAQZh    = "退貨經審核同意後,系統會立即向 Stripe 發出退款。實際入帳時間依發卡銀行而定,通常是數個工作天。"
	staleRefundFAQEn    = "As soon as a return is approved we ask Stripe to refund. When it lands depends on your card issuer, usually a few working days."
	customRefundFAQZh   = "店家自訂退款說明"
	customRefundFAQEn   = "Shop-edited refund note"
)

// TestAShippedRefundFAQRepairRewritesOnlyStaleLocales holds the published
// refund FAQ to the original payment composition: a fresh seed, the shipped
// repair file on a kept stale row, and each locale matched on its own
// published predecessor sentences so a shop-edited answer stays.
func TestAShippedRefundFAQRepairRewritesOnlyStaleLocales(t *testing.T) {
	ctx := t.Context()
	storefront := site.NewStore(pool)
	assertRefundFAQLocales(t, storefront, ctx, refundFAQZh, refundFAQEn)

	catalog, err := os.ReadFile("../../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatalf("read catalogue seed: %v", err)
	}
	repair, err := os.ReadFile("../../../seed/repair_refund_faq.sql")
	if err != nil {
		t.Fatalf("read shipped refund FAQ repair: %v", err)
	}

	isolated := dbtest.Pool(t)
	if _, loadErr := isolated.Exec(ctx, string(catalog)); loadErr != nil {
		t.Fatalf("seed isolated catalogue: %v", loadErr)
	}
	plantRefundFAQ(t, ctx, isolated, staleRefundFAQZh, staleRefundFAQEn)
	isolatedContent := site.NewStore(isolated)
	zh, en := refundFAQAnswers(t, isolatedContent, ctx)
	if zh != staleRefundFAQZh || en != staleRefundFAQEn {
		t.Fatalf("the planted stale answers did not reach /faq: zh=%q en=%q", zh, en)
	}

	conn, err := isolated.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire isolated connection: %v", err)
	}
	if _, seedErr := conn.Exec(ctx, string(catalog)); seedErr == nil {
		conn.Release()
		t.Fatal("the catalogue seed succeeded against a kept database; " +
			"db-seed cannot be the repair path")
	}
	if _, rollErr := conn.Exec(ctx, "ROLLBACK"); rollErr != nil {
		conn.Release()
		t.Fatalf("rollback failed catalogue re-seed: %v", rollErr)
	}
	conn.Release()
	zh, en = refundFAQAnswers(t, isolatedContent, ctx)
	if zh != staleRefundFAQZh || en != staleRefundFAQEn {
		t.Fatal("the failed catalogue re-seed changed the kept refund FAQ")
	}

	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, refundFAQZh, refundFAQEn)

	plantRefundFAQ(t, ctx, isolated, previousRefundFAQZh, refundFAQEn)
	for range 2 {
		if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
			t.Fatalf("apply shipped refund FAQ repair after the previous credit wording: %v", repairErr)
		}
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, refundFAQZh, refundFAQEn)

	plantRefundFAQ(t, ctx, isolated, shippedRefundFAQZh, refundFAQEn)
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair after the 店儲 wording: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, refundFAQZh, refundFAQEn)

	plantRefundFAQ(t, ctx, isolated, customRefundFAQZh, staleRefundFAQEn)
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair after a Chinese edit: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, customRefundFAQZh, refundFAQEn)

	plantRefundFAQ(t, ctx, isolated, staleRefundFAQZh, customRefundFAQEn)
	if _, repairErr := isolated.Exec(ctx, string(repair)); repairErr != nil {
		t.Fatalf("apply shipped refund FAQ repair after an English edit: %v", repairErr)
	}
	assertRefundFAQLocales(t, isolatedContent, ctx, refundFAQZh, customRefundFAQEn)
}

func plantRefundFAQ(t *testing.T, ctx context.Context, p *pgxpool.Pool, zh, en string) {
	t.Helper()
	if _, err := p.Exec(ctx, `
		UPDATE faq_entries
		SET answer = $1, answer_en = $2
		WHERE question = $3`, zh, en, refundFAQQuestion); err != nil {
		t.Fatalf("plant refund FAQ: %v", err)
	}
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
