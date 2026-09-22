//go:build integration

package admin_test

import (
	"os"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/db/dbtest"
)

func TestPaymentFAQRepairPreservesShopEditedLocales(t *testing.T) {
	ctx := t.Context()
	isolated := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	repair, err := os.ReadFile("../../seed/repair_payment_faq.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.Exec(ctx, string(seed)); err != nil {
		t.Fatal(err)
	}
	const question = "可以用哪些方式付款?"
	const oldZh = "目前接受信用卡付款,由 Stripe 處理,goen 不會接觸到您的卡片資料。付款頁面在 Stripe 網域上,完成後會自動回到訂單頁。"
	const oldEn = "Credit card, handled by Stripe. goen never sees your card details: the payment page is on Stripe's own domain and you return to your order afterwards."
	var freshZh, freshEn string
	if err := isolated.QueryRow(ctx, "SELECT answer, answer_en FROM faq_entries WHERE question=$1", question).Scan(&freshZh, &freshEn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(freshZh, "商店額度") || !strings.Contains(freshEn, "not a payment method") {
		t.Fatal("fresh FAQ does not explain the payment choices")
	}
	for _, tc := range []struct{ name, zh, en, wantZh, wantEn string }{
		{"both stale", oldZh, oldEn, freshZh, freshEn},
		{"Chinese edited", "店家自訂付款說明", oldEn, "店家自訂付款說明", freshEn},
		{"English edited", oldZh, "Shop payment instructions", freshZh, "Shop payment instructions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := isolated.Exec(ctx, "UPDATE faq_entries SET answer=$1, answer_en=$2 WHERE question=$3", tc.zh, tc.en, question); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := isolated.Exec(ctx, string(repair)); err != nil {
					t.Fatal(err)
				}
			}
			var zh, en string
			if err := isolated.QueryRow(ctx, "SELECT answer, answer_en FROM faq_entries WHERE question=$1", question).Scan(&zh, &en); err != nil {
				t.Fatal(err)
			}
			if zh != tc.wantZh || en != tc.wantEn {
				t.Errorf("FAQ after repair = %q / %q, want %q / %q", zh, en, tc.wantZh, tc.wantEn)
			}
		})
	}
}
