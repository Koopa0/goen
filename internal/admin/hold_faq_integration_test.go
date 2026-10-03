//go:build integration

package admin_test

import (
	"os"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/db/dbtest"
)

func TestHoldFAQRepairPreservesShopEditedLocales(t *testing.T) {
	ctx := t.Context()
	isolated := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	repair, err := os.ReadFile("../../seed/repair_hold_faq.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := isolated.Exec(ctx, string(seed)); err != nil {
		t.Fatal(err)
	}
	const question = "下單之後商品會保留嗎？"
	const oldZh = "會。送出訂單的同時系統就會保留庫存 60 分鐘,讓您完成付款。超過時間未付款,商品會回到架上供其他人購買,訂單仍然保留,可以重新付款(若庫存還在)。"
	const oldEn = "Yes. Placing the order holds the stock for 60 minutes so you can pay. After that the item goes back on the shelf for other people, but your order stays and you can pay again if it is still available."
	var freshZh, freshEn string
	if err := isolated.QueryRow(ctx, "SELECT answer, answer_en FROM faq_entries WHERE question=$1", question).Scan(&freshZh, &freshEn); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(freshZh, "自動取消") || !strings.Contains(freshEn, "cancelled automatically") {
		t.Fatal("fresh FAQ does not say a lapsed order is cancelled")
	}
	for _, tc := range []struct{ name, zh, en, wantZh, wantEn string }{
		{"both stale", oldZh, oldEn, freshZh, freshEn},
		{"Chinese edited", "店家自訂保留說明", oldEn, "店家自訂保留說明", freshEn},
		{"English edited", oldZh, "Shop hold instructions", freshZh, "Shop hold instructions"},
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
