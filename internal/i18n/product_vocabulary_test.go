package i18n

import "testing"

func TestProductVocabularyDistinguishesVariantsOptionsAndSpecifications(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		key    Key
		zh, en string
	}{
		{KeyAdminProdVariants, "款式", "Variants"},
		{KeyAdminProdVariantAdd, "新增款式", "Add variant"},
		{KeyAdminProdOptions, "選項", "Variant options"},
		{KeyAdminProdOptionAdd, "新增選項", "Add option"},
		{KeyAdminProdOptionName, "選項名稱", "Option name"},
		{KeyAdminProdOptionNameEn, "選項名稱（英文）", "Option name (English)"},
		{KeyAdminProdNoValues, "還沒有選項值", "No values yet"},
		{KeyAdminProdValueOption, "加值到哪個選項", "Which option this value belongs to"},
		{KeyAdminProdValue, "選項值", "Value"},
		{KeyAdminProdValueEn, "選項值（英文）", "Value (English)"},
		{KeyAdminProdValueAdd, "新增選項值", "Add value"},
		{KeyAdminProdOptionsLead, "例如顏色、容量，商品頁的選擇器顯示的就是這些。有選項的商品，每個 SKU 都要在每一項選一個選項值。", "Colour, capacity and the like: the picker on the product page offers these. On a product with options, every SKU takes one value from each."},
		{KeyFormOptionBeforeVariants, "此商品已有 SKU，無法再新增選項。若要使用新的選項組合，請建立新商品，並在建立 SKU 之前設定好所有選項；既有商品與 SKU 不會變更。", "This product already has SKUs, so another option cannot be added. To use a new set of options, create a new product and define every option before creating its SKUs; existing products and SKUs stay unchanged."},
		{KeyFormOptionPick, "請選擇要加值的選項。", "Pick which option this value belongs to."},
		{KeyFormOptionValueTaken, "這個選項已經有同樣的選項值了。", "That option already has this value."},
		{KeyFormOptionMissing, "找不到這個選項。", "No such option on this product."},
		{KeyFormVariantNeedsEveryOption, "每一個選項都要選一個選項值，否則商品頁的選擇器找不到這個款式。", "Every option needs a value chosen, or the product page's picker cannot reach this variant at all."},
		{KeyFormOptionNameTaken, "已有同名的選項。", "This product already has an option with that name."},
		{KeyFormSpecLabelDuplicate, "已有同名的規格項目。", "This product already has a spec with that label."},
		{KeyFormOptionsInvalid, "選項值有誤，請重新選擇。", "Those option values do not work together. Choose again."},
		{KeyAdminProdVariantStockHint, "新款式的庫存是 0。庫存只能從「庫存」頁調整，那裡每一次異動都會寫進帳本。", "A new variant starts at zero stock. Stock changes only from the Stock page, where every movement is written to the ledger."},
		{KeyAdminLedgerEmpty, "這個款式還沒有任何異動。新款式的庫存是 0，所有的量都從這張帳本進來。", "Nothing has moved for this variant yet. A new variant starts at zero, and every unit it ever holds arrives through this ledger."},
		{KeyRestockUnavailable, "這個款式目前不需要補貨通知（已經有貨，或已不存在）。請確認款式後再試。", "This variant needs no restock notice (it is in stock, or it is gone). Check the variant and try again."},
		{KeyAdminProdNoOptions, "還沒有選項。單一款式的商品不需要。", "No options yet. A product with a single variant does not need any."},
		{KeyAdminProdNoVariants, "還沒有款式。沒有款式的商品沒有價格，無法上架。", "No variants yet. A product with no variant has no price and cannot be published."},
		{KeyAdminProdNeedsVariant, "先新增至少一個款式，才能上架。", "Add at least one variant before this can be published."},
		{KeyAddRefused, "這個款式剛剛被買走了，沒有加入購物車。", "That variant has just sold out, so nothing was added."},
		{KeyChooseOptions, "請選擇選項", "Choose options"},
		{KeyAdminRetNoVariant, "這個品項已無對應款式，無法入庫。", "This item no longer has a variant to go back into, so nothing can be restocked."},
		{KeyAuditVariantRetire, "款式上下架", "Variant availability"},
		{KeyAuditVariantCreate, "新增款式", "Add variant"},
		{KeyAuditOptionAdd, "新增選項", "Add option"},
		{KeyAuditOptionValueAdd, "新增選項值", "Add option value"},
		{KeyAuditFieldOptionValue, "選項值", "Option value"},
		{KeyAuditFieldSKU, "款式 SKU", "SKU"},
		{KeyAuditEntityProductVariants, "款式", "Variant"},
		{KeySectionSpecs, "規格", "Specifications"},
		{KeyAdminProdSpecs, "規格表", "Specification table"},
		{KeyAuditEntityProductSpecs, "商品規格表", "Product specification"},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			t.Parallel()
			for _, locale := range []Locale{ZhHant, En} {
				want := tt.zh
				if locale == En {
					want = tt.en
				}
				if got := T(WithLocale(t.Context(), locale), tt.key); got != want {
					t.Errorf("%s: %q, want %q", locale, got, want)
				}
			}
		})
	}
}
