//go:build integration

package content_test

import (
	"os"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/db/dbtest"
)

func TestFAQRepairsStillMatchOlderQuestionSpellings(t *testing.T) {
	ctx := t.Context()
	isolated := dbtest.Pool(t)
	seed, err := os.ReadFile("../../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.Exec(ctx, string(seed)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ script, question, answer string }{
		{"hold", "下單之後商品會保留嗎?", "會。送出訂單的同時系統就會保留庫存 60 分鐘,讓您完成付款。超過時間未付款,商品會回到架上供其他人購買,訂單仍然保留,可以重新付款(若庫存還在)。"},
		{"invoice", "發票怎麼開立?", "結帳時可以選擇會員載具、手機條碼載具或公司統編,系統會記錄您的選擇。電子發票的實際開立需要串接加值中心,這部分尚未完成。"},
		{"payment", "可以用哪些方式付款?", "目前接受信用卡付款,由 Stripe 處理,goen 不會接觸到您的卡片資料。付款頁面在 Stripe 網域上,完成後會自動回到訂單頁。"},
		{"refund", "退款什麼時候會收到?", "退貨經審核同意後,系統會立即向 Stripe 發出退款。實際入帳時間依發卡銀行而定,通常是數個工作天。"},
		{"shop_rules", "折扣碼要怎麼使用?", "在結帳頁的「折扣碼」欄位輸入即可,大小寫不拘。每筆訂單限用一組折扣碼,折抵金額不會超過商品小計。"},
		{"shop_rules", "一定要註冊才能購買嗎?", "不用。goen 支援訪客結帳,只需要填寫收件資訊。註冊後可以查看訂單紀錄、使用願望清單,以及累積與使用商店額度。"},
		{"shop_rules", "可以開公司統編嗎?", "可以。結帳時選擇「公司統編」並填入八位數字的統一編號即可。"},
	} {
		t.Run(tc.question, func(t *testing.T) {
			current := strings.ReplaceAll(tc.question, "?", "？")
			var want string
			if err := isolated.QueryRow(ctx, "SELECT answer FROM faq_entries WHERE question=$1", current).Scan(&want); err != nil {
				t.Fatal(err)
			}
			if _, err := isolated.Exec(ctx, "UPDATE faq_entries SET question=$1, answer=$2, answer_en='Shop-authored answer' WHERE question=$3", tc.question, tc.answer, current); err != nil {
				t.Fatal(err)
			}
			repair, err := os.ReadFile("../../../seed/repair_" + tc.script + "_faq.sql")
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := isolated.Exec(ctx, string(repair)); err != nil {
					t.Fatal(err)
				}
			}
			var zh, en string
			if err := isolated.QueryRow(ctx, "SELECT answer, answer_en FROM faq_entries WHERE question=$1", tc.question).Scan(&zh, &en); err != nil {
				t.Fatal(err)
			}
			if zh != want || en != "Shop-authored answer" {
				t.Errorf("legacy repair = %q / %q, want %q with edited English preserved", zh, en, want)
			}
		})
	}
}
