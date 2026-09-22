package site

import (
	"os"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestPaymentExplainsCreditAndDiscountsSeparately(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		doc := policies["payment"].For(locale)
		want := map[string][]string{"信用卡": {"Stripe", "不會儲存"}, "商店額度": {"登入", "可用", "不足", "信用卡"}, "折扣碼": {"價格折抵", "不是付款方式"}}
		if locale == i18n.En {
			want = map[string][]string{"Credit cards": {"Stripe", "never store"}, "Store credit": {"signed in", "available", "remaining", "card"}, "Discount codes": {"reduces the price", "not a payment method"}}
		}
		for heading, words := range want {
			var body string
			for _, section := range doc.Sections {
				if section.Heading == heading {
					body = strings.Join(section.Body, " ")
				}
			}
			for _, word := range words {
				if !strings.Contains(body, word) {
					t.Errorf("%s section %q omits %q", locale, heading, word)
				}
			}
		}
	}
}

func TestPaymentFAQSeedAndRepairExplainTheSameChoices(t *testing.T) {
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	repair, err := os.ReadFile("../../seed/repair_payment_faq.sql")
	if err != nil {
		t.Fatal(err)
	}
	zh := sqlStringAfter(t, string(seed), "('訂購與付款', '可以用哪些方式付款?',")
	en := sqlStringAfter(t, string(seed), "'How can I pay?',")
	if zh != sqlStringAfter(t, string(repair), "SET answer =") || en != sqlStringAfter(t, string(repair), "SET answer_en =") {
		t.Error("payment FAQ repair differs from fresh seed")
	}
	for _, word := range []string{"Stripe", "商店額度", "折扣碼", "不是付款方式"} {
		if !strings.Contains(zh, word) {
			t.Errorf("payment FAQ omits %q", word)
		}
	}
	for _, word := range []string{"Stripe", "store credit", "discount code", "not a payment method"} {
		if !strings.Contains(en, word) {
			t.Errorf("English payment FAQ omits %q", word)
		}
	}
}
