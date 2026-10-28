package i18n

var (
	KeyFormOptionName = key("form.option.name", Message{
		ZhHant: "請填寫名稱。",
		En:     "A name is required.",
	})

	KeyFormOptionNameLong = key("form.option.name.long", Message{
		ZhHant: "名稱太長。",
		En:     "That name is too long.",
	})

	KeyFormOptionNameEnLong = key("form.option.name.en.long", Message{
		ZhHant: "英文名稱太長。",
		En:     "The English name is too long.",
	})

	KeyFormOptionPick = key("form.option.pick", Message{
		ZhHant: "請選擇要加值的選項。",
		En:     "Pick which option this value belongs to.",
	})

	KeyFormOptionValueTaken = key("form.option.value.taken", Message{
		ZhHant: "這個選項已經有同樣的選項值了。",
		En:     "That option already has this value.",
	})

	KeyFormOptionMissing = key("form.option.missing", Message{
		ZhHant: "找不到這個選項。",
		En:     "No such option on this product.",
	})

	KeyFormVariantNeedsEveryOption = key("form.variant.everyoption", Message{
		ZhHant: "每一個選項都要選一個選項值，否則商品頁的選擇器找不到這個款式。",
		En:     "Every option needs a value chosen, or the product page's picker cannot reach this variant at all.",
	})

	KeyFormVariantCombinationTaken = key("form.variant.combinationtaken", Message{
		ZhHant: "這個組合已經有 SKU 了。",
		En:     "Another SKU already has this combination.",
	})

	KeyFormOptionNameTaken = key("form.option.name.taken", Message{
		ZhHant: "已有同名的選項。",
		En:     "This product already has an option with that name.",
	})

	KeyAdminProdOptions = key("admin.prod.options", Message{
		ZhHant: "選項",
		En:     "Variant options",
	})

	KeyAdminProdOptionsLead = key("admin.prod.optionslead", Message{
		ZhHant: "例如顏色、容量，商品頁的選擇器顯示的就是這些。有選項的商品，每個 SKU 都要在每一項選一個選項值。",
		En:     "Colour, capacity and the like: the picker on the product page offers these. On a product with options, every SKU takes one value from each.",
	})

	KeyAdminProdNoValues = key("admin.prod.novalues", Message{ZhHant: "還沒有選項值", En: "No values yet"})

	KeyAdminProdOptionName = key("admin.prod.optionname", Message{
		ZhHant: "選項名稱",
		En:     "Option name",
	})

	KeyAdminProdOptionNameEn = key("admin.prod.optionnameen", Message{
		ZhHant: "選項名稱（英文）",
		En:     "Option name (English)",
	})

	KeyAdminProdOptionAdd = key("admin.prod.optionadd", Message{
		ZhHant: "新增選項",
		En:     "Add option",
	})

	KeyAdminProdValueOption = key("admin.prod.valueoption", Message{
		ZhHant: "加值到哪個選項",
		En:     "Which option this value belongs to",
	})

	KeyAdminProdValue = key("admin.prod.value", Message{ZhHant: "選項值", En: "Value"})

	KeyAdminProdValueEn = key("admin.prod.valueen", Message{
		ZhHant: "選項值（英文）",
		En:     "Value (English)",
	})

	// PROPOSED WORDING, awaiting the shop's own: the field is optional and the
	// value is a hex colour, which the example under it shows.
	KeyAdminProdValueSwatch = key("admin.prod.valueswatch", Message{
		ZhHant: "色碼（選填）",
		En:     "Colour (optional)",
	})

	// PROPOSED WORDING, awaiting the shop's own.
	KeyFormSwatchHex = key("form.swatchhex", Message{
		ZhHant: "色碼請寫成 # 加六位十六進位，例如 #1c1c1e。",
		En:     "A colour is # and six hexadecimal digits, for example #1c1c1e.",
	})

	KeyAdminProdValueAdd = key("admin.prod.valueadd", Message{ZhHant: "新增選項值", En: "Add value"})
)

var KeyFormOptionBeforeVariants = key("form.option.before_variants", Message{
	ZhHant: "此商品已有 SKU，無法再新增選項。若要使用新的選項組合，請建立新商品，並在建立 SKU 之前設定好所有選項；既有商品與 SKU 不會變更。",
	En:     "This product already has SKUs, so another option cannot be added. To use a new set of options, create a new product and define every option before creating its SKUs; existing products and SKUs stay unchanged.",
})
