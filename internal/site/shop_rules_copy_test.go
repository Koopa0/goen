package site

import (
	"os"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func policyBody(t *testing.T, page string, locale i18n.Locale, heading string) string {
	t.Helper()
	for _, s := range policies[page].For(locale).Sections {
		if s.Heading == heading {
			return strings.Join(s.Body, " ")
		}
	}
	t.Fatalf("%s (%s) has no section %q", page, locale, heading)
	return ""
}

func TestPaymentPolicySaysDiscountCodesStackWithSales(t *testing.T) {
	t.Parallel()
	zh := policyBody(t, "payment", i18n.ZhHant, "折扣碼")
	en := policyBody(t, "payment", i18n.En, "Discount codes")
	for _, w := range []string{"特價", "疊加"} {
		if !strings.Contains(zh, w) {
			t.Errorf("payment policy (zh) omits %q", w)
		}
	}
	for _, w := range []string{"on sale", "stack"} {
		if !strings.Contains(en, w) {
			t.Errorf("payment policy (en) omits %q", w)
		}
	}
}

func TestTermsSayPointsExpireButRedeemedCreditDoesNot(t *testing.T) {
	t.Parallel()
	zh := policyBody(t, "terms", i18n.ZhHant, "點數與購物金")
	en := policyBody(t, "terms", i18n.En, "Points and store credit")
	if !strings.Contains(zh, "一年到期") || !strings.Contains(zh, "購物金不會到期") {
		t.Errorf("terms (zh) do not state the expiry rule: %s", zh)
	}
	if !strings.Contains(en, "one year") || !strings.Contains(en, "does not expire") {
		t.Errorf("terms (en) do not state the expiry rule: %s", en)
	}
}

// return_goods_refundable_amount takes the returned goods' share of the order
// discount off the refund; a reader of either language is promised the same sum.
func TestReturnsRefundTakesOffTheDiscountShareInBothLanguages(t *testing.T) {
	t.Parallel()
	zh := policyBody(t, "returns", i18n.ZhHant, "退款")
	en := policyBody(t, "returns", i18n.En, "Refunds")
	if !strings.Contains(zh, "扣除這些商品分攤的折扣") {
		t.Errorf("returns policy (zh) does not take the discount share off the refund: %s", zh)
	}
	if !strings.Contains(en, "less the share of any discount those goods carried") {
		t.Errorf("returns policy (en) does not take the discount share off the refund: %s", en)
	}
}

func TestWarrantyCollectionDependsOnHowTheOrderWasDelivered(t *testing.T) {
	t.Parallel()
	zh := policyBody(t, "warranty", i18n.ZhHant, "怎麼送修")
	en := policyBody(t, "warranty", i18n.En, "Sending something in")
	for _, w := range []string{"宅配訂單", "到府收件", "超商取貨", "由超商寄回", "兩種訂單的收送費用都由 goen 負擔"} {
		if !strings.Contains(zh, w) {
			t.Errorf("warranty policy (zh) omits %q", w)
		}
	}
	for _, w := range []string{"home-delivery", "from your door", "convenience-store pickup", "sent back from a convenience store", "we pay the carriage both ways in either case"} {
		if !strings.Contains(en, w) {
			t.Errorf("warranty policy (en) omits %q", w)
		}
	}
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		for _, k := range []i18n.Key{i18n.KeyGuaranteeWarranty, i18n.KeyAboutWarrantyBody, i18n.KeyTrustWarrantyBody} {
			if got := i18n.T(ctx, k); strings.Contains(got, "到府收送") && !strings.Contains(got, "宅配") {
				t.Errorf("%s still promises door collection to every order: %q", loc, got)
			}
		}
	}
}

func TestShopRulesFAQSeedAndRepairAgree(t *testing.T) {
	t.Parallel()
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	repair, err := os.ReadFile("../../seed/repair_shop_rules_faq.sql")
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ question, enQuestion, zhWord, enWord string }
	for _, r := range []row{
		{"折扣碼要怎麼使用？", "How do I use a discount code?", "特價與折扣碼可以疊加", "a sale and a code stack"},
		{"一定要註冊才能購買嗎？", "Do I have to register to buy?", "購物金不會到期", "does not expire"},
		{"可以開公司統編嗎？", "Can you invoice a company tax ID?", "綠界電子發票載具", "ECPay e-invoice carrier"},
	} {
		zh := sqlStringAfter(t, string(seed), "'"+r.question+"',")
		en := sqlStringAfter(t, string(seed), "'"+r.enQuestion+"',")
		var repairZh, repairEn string
		for _, block := range strings.Split(string(repair), "\n\n") {
			_, where, ok := strings.Cut(block, "WHERE question ")
			where, _, _ = strings.Cut(where, "\n")
			if !ok || !strings.Contains(where, "'"+r.question+"'") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(block), "UPDATE") && strings.Contains(block, "SET answer_en =") {
				repairEn = sqlStringAfter(t, block, "SET answer_en =")
			} else {
				repairZh = sqlStringAfter(t, block, "SET answer =")
			}
		}
		if zh != repairZh || en != repairEn {
			t.Errorf("%s: repair differs from fresh seed\n zh %q\n repair %q\n en %q\n repair %q", r.question, zh, repairZh, en, repairEn)
		}
		if !strings.Contains(zh, r.zhWord) || !strings.Contains(en, r.enWord) {
			t.Errorf("%s: answer omits the stated rule (%q / %q)", r.question, r.zhWord, r.enWord)
		}
	}
}
