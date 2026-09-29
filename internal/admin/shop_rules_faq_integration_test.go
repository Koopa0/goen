//go:build integration

package admin_test

import (
	"os"
	"testing"

	"github.com/koopa0/goen/internal/db/dbtest"
)

// The repair must take a kept database's previous answers to exactly what a
// fresh seed writes, per locale, and leave anything the shop edited alone.
func TestShopRulesFAQRepairMatchesTheSeedAndPreservesShopEdits(t *testing.T) {
	ctx := t.Context()
	isolated := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	repair, err := os.ReadFile("../../seed/repair_shop_rules_faq.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.Exec(ctx, string(seed)); err != nil {
		t.Fatal(err)
	}
	type previous struct{ question, zh, en string }
	for _, p := range []previous{
		{"折扣碼要怎麼使用?",
			"在結帳頁的「折扣碼」欄位輸入即可,大小寫不拘。每筆訂單限用一組折扣碼,折抵金額不會超過商品小計。",
			"Type it into the discount field at checkout; case does not matter. One code per order, and the discount never exceeds the item subtotal."},
		{"一定要註冊才能購買嗎?",
			"不用。goen 支援訪客結帳,只需要填寫收件資訊。註冊後可以查看訂單紀錄、使用願望清單,以及累積與使用商店額度。",
			"No. goen supports guest checkout — you only need delivery details. Registering lets you see your order history, keep a wishlist, and earn and spend store credit."},
		{"可以開公司統編嗎?",
			"可以。結帳時選擇「公司統編」並填入八位數字的統一編號即可。",
			"Yes. Choose \"company tax ID\" at checkout and enter the eight digits."},
	} {
		var freshZh, freshEn string
		if err := isolated.QueryRow(ctx, "SELECT answer, answer_en FROM faq_entries WHERE question=$1", p.question).Scan(&freshZh, &freshEn); err != nil {
			t.Fatal(err)
		}
		if freshZh == p.zh || freshEn == p.en {
			t.Fatalf("%s: the fresh seed still carries the previous answer", p.question)
		}
		for _, tc := range []struct{ name, zh, en, wantZh, wantEn string }{
			{"both previous", p.zh, p.en, freshZh, freshEn},
			{"Chinese edited", "店家自訂", p.en, "店家自訂", freshEn},
			{"English edited", p.zh, "Shop wording", freshZh, "Shop wording"},
		} {
			t.Run(p.question+"/"+tc.name, func(t *testing.T) {
				if _, err := isolated.Exec(ctx, "UPDATE faq_entries SET answer=$1, answer_en=$2 WHERE question=$3", tc.zh, tc.en, p.question); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if _, err := isolated.Exec(ctx, string(repair)); err != nil {
						t.Fatal(err)
					}
				}
				var zh, en string
				if err := isolated.QueryRow(ctx, "SELECT answer, answer_en FROM faq_entries WHERE question=$1", p.question).Scan(&zh, &en); err != nil {
					t.Fatal(err)
				}
				if zh != tc.wantZh || en != tc.wantEn {
					t.Errorf("after repair = %q / %q, want %q / %q", zh, en, tc.wantZh, tc.wantEn)
				}
			})
		}
	}
}
