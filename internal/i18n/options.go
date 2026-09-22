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
		ZhHant: "請選擇要加值的規格項目。",
		En:     "Pick which option this value belongs to.",
	})

	KeyFormOptionValueTaken = key("form.option.value.taken", Message{
		ZhHant: "這個規格項目已經有同樣的值了。",
		En:     "That option already has this value.",
	})

	KeyFormOptionMissing = key("form.option.missing", Message{
		ZhHant: "找不到這個規格項目。",
		En:     "No such option on this product.",
	})

	KeyFormVariantNeedsEveryOption = key("form.variant.everyoption", Message{
		ZhHant: "每一個規格項目都要選一個值,否則商品頁的選擇器找不到這個規格。",
		En: "Every option needs a value chosen, or the product page's picker cannot " +
			"reach this variant at all.",
	})

	KeyFormOptionNameTaken = key("form.option.name.taken", Message{
		ZhHant: "這個商品已經有同名的規格項目了。",
		En:     "This product already has an option with that name.",
	})

	KeyAdminProdOptions = key("admin.prod.options", Message{
		ZhHant: "規格項目",
		En:     "Variant options",
	})

	KeyAdminProdOptionsLead = key("admin.prod.optionslead", Message{
		ZhHant: "顏色、容量這一類的軸線。商品頁的選擇器讀的就是這些 —— 有規格項目的商品,每一個 SKU 都要選一個值。",
		En: "Axes such as colour or capacity. The picker on the product page reads exactly these — " +
			"on a product that has options, every SKU must name one value on each of them.",
	})

	KeyAdminProdNoValues = key("admin.prod.novalues", Message{ZhHant: "還沒有值", En: "No values yet"})

	KeyAdminProdOptionName = key("admin.prod.optionname", Message{
		ZhHant: "項目名稱",
		En:     "Option name",
	})

	KeyAdminProdOptionNameEn = key("admin.prod.optionnameen", Message{
		ZhHant: "項目名稱(英文)",
		En:     "Option name (English)",
	})

	KeyAdminProdOptionAdd = key("admin.prod.optionadd", Message{
		ZhHant: "新增規格項目",
		En:     "Add option",
	})

	KeyAdminProdValueOption = key("admin.prod.valueoption", Message{
		ZhHant: "加值到哪個項目",
		En:     "Which option this value belongs to",
	})

	KeyAdminProdValue = key("admin.prod.value", Message{ZhHant: "值", En: "Value"})

	KeyAdminProdValueEn = key("admin.prod.valueen", Message{
		ZhHant: "值(英文)",
		En:     "Value (English)",
	})

	// PROPOSED WORDING, awaiting the shop's own: the field is optional and the
	// value is a hex colour, which the placeholder shows.
	KeyAdminProdValueSwatch = key("admin.prod.valueswatch", Message{
		ZhHant: "色碼(選填)",
		En:     "Colour (optional)",
	})

	// PROPOSED WORDING, awaiting the shop's own.
	KeyFormSwatchHex = key("form.swatchhex", Message{
		ZhHant: "色碼請寫成 # 加六位十六進位,例如 #1c1c1e。",
		En:     "A colour is # and six hexadecimal digits, for example #1c1c1e.",
	})

	KeyAdminProdValueAdd = key("admin.prod.valueadd", Message{ZhHant: "新增值", En: "Add value"})
)

var KeyFormOptionBeforeVariants = key("form.option.before_variants", Message{
	ZhHant: "此商品已有 SKU，無法新增規格軸。請先在新商品設定完整規格，再建立 SKU；既有商品與 SKU 不會變更。",
	En:     "This product already has SKUs, so another option axis cannot be added. Define all options on a new product before creating its SKUs; existing products and SKUs stay unchanged.",
})
