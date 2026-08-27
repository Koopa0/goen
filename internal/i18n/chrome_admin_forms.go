package i18n

var (
	KeyFormSlugFormat = key("form.slug.format", Message{
		ZhHant: "網址代稱只能用小寫英數與連字號。",
		En:     "A slug takes lower-case letters, digits and hyphens only.",
	})
	KeyFormSlugFormatExample = key("form.slug.format.example", Message{
		ZhHant: "網址代稱只能用小寫英數與連字號,例如 pixelight-9-pro。",
		En:     "A slug takes lower-case letters, digits and hyphens only — pixelight-9-pro, for example.",
	})
	KeyFormSlugTakenBrand = key("form.slug.taken.brand", Message{
		ZhHant: "這個網址代稱已經有品牌用了。",
		En:     "Another brand already uses that slug.",
	})
	KeyFormSlugTakenCategory = key("form.slug.taken.category", Message{
		ZhHant: "這個網址代稱已經有分類用了。",
		En:     "Another category already uses that slug.",
	})
	KeyFormSlugTakenCampaign = key("form.slug.taken.campaign", Message{
		ZhHant: "這個網址代稱已經有活動用了。",
		En:     "Another campaign already uses that slug.",
	})
	KeyFormSlugTakenProduct = key("form.slug.taken.product", Message{
		ZhHant: "這個網址代稱已經有人用了。",
		En:     "Something already uses that slug.",
	})

	KeyFormNameRequired = key("form.name.required", Message{
		ZhHant: "請填寫名稱,不超過 60 個字。",
		En:     "A name is required, 60 characters at most.",
	})
	KeyFormNameEnTooLong = key("form.name.en.toolong", Message{
		ZhHant: "英文名稱不超過 60 個字。",
		En:     "The English name is 60 characters at most.",
	})
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
	KeyFormParentMissing = key("form.parent.missing", Message{
		ZhHant: "找不到這個上層分類。",
		En:     "No such parent category.",
	})

	KeyFormProductName = key("form.product.name", Message{
		ZhHant: "請填寫商品名稱。",
		En:     "A product name is required.",
	})
	KeyFormProductSummaryLong = key("form.product.summary.long", Message{
		ZhHant: "一句話簡介太長了。",
		En:     "That one-line summary is too long.",
	})
	KeyFormProductDescriptionLong = key("form.product.description.long", Message{
		ZhHant: "商品說明太長了。",
		En:     "That description is too long.",
	})
	KeyFormProductNameEnLong = key("form.product.name.en.long", Message{
		ZhHant: "英文名稱太長了。",
		En:     "The English name is too long.",
	})
	KeyFormProductSummaryEnLong = key("form.product.summary.en.long", Message{
		ZhHant: "英文簡介太長了。",
		En:     "The English summary is too long.",
	})
	KeyFormProductDescriptionEnLong = key("form.product.description.en.long", Message{
		ZhHant: "英文說明太長了。",
		En:     "The English description is too long.",
	})
	KeyFormWarrantyMonths = key("form.warranty.months", Message{
		ZhHant: "保固月數請填 1 到 120,或留空表示未提供保固。",
		En:     "A warranty term is 1 to 120 months, or blank for no stated cover.",
	})
	KeyFormBrandRequired    = key("form.brand.required", Message{ZhHant: "請選擇品牌。", En: "Pick a brand."})
	KeyFormCategoryRequired = key("form.category.required", Message{ZhHant: "請選擇分類。", En: "Pick a category."})

	KeyFormSKURequired = key("form.sku.required", Message{ZhHant: "請填寫 SKU。", En: "A SKU is required."})
	KeyFormSKUTaken    = key("form.sku.taken", Message{
		ZhHant: "這個 SKU 已經有人用了。",
		En:     "Something already uses that SKU.",
	})
	KeyFormPricePositive = key("form.price.positive", Message{
		ZhHant: "價格必須大於 0。",
		En:     "A price has to be above zero.",
	})
	KeyFormCompareHigher = key("form.compare.higher", Message{
		ZhHant: "原價要高於售價,否則就不是折扣。",
		En:     "The compare-at price has to be above the selling price, or it is not a discount.",
	})
	KeyFormSafetyStock = key("form.safety.stock", Message{
		ZhHant: "安全庫存請填 0 到 1,000,000 的整數。",
		En:     "Safety stock is a whole number from 0 to 1,000,000.",
	})
	KeyFormParcelMeasurement = key("form.parcel.measurement", Message{
		ZhHant: "請填 1 到 %d 的整數,或留空或填 0 表示尚未量測。",
		En:     "Type a whole number from 1 to %d, or leave it blank or type 0 for unmeasured.",
	})
	KeyFormParcelSumShort = key("form.parcel.sum.short", Message{
		ZhHant: "三邊和不能小於最長邊。",
		En:     "The sum of the three sides cannot be less than the longest side.",
	})
	KeyFormOptionsInvalid = key("form.options.invalid", Message{
		ZhHant: "規格選項有誤,請重新選擇。",
		En:     "Those option values do not work together. Choose again.",
	})
	KeyFormSpecLabel       = key("form.spec.label", Message{ZhHant: "請填寫規格名稱", En: "A spec label is required"})
	KeyFormSpecLabelLong   = key("form.spec.label.long", Message{ZhHant: "規格名稱太長", En: "That spec label is too long"})
	KeyFormSpecValue       = key("form.spec.value", Message{ZhHant: "請填寫規格內容", En: "A spec value is required"})
	KeyFormSpecValueLong   = key("form.spec.value.long", Message{ZhHant: "規格內容太長", En: "That spec value is too long"})
	KeyFormSpecLabelEnLong = key("form.spec.label.en.long", Message{
		ZhHant: "英文規格名稱太長",
		En:     "That English spec label is too long",
	})
	KeyFormSpecValueEnLong = key("form.spec.value.en.long", Message{
		ZhHant: "英文規格內容太長",
		En:     "That English spec value is too long",
	})
)

var (
	KeyFormCouponCode = key("form.coupon.code", Message{
		ZhHant: "折扣碼只能用英數與連字號,2 到 32 個字元。",
		En:     "A coupon code takes letters, digits and hyphens, 2 to 32 characters.",
	})
	KeyFormCouponDescription = key("form.coupon.description", Message{
		ZhHant: "請填寫顧客會看到的說明,不超過 60 個字。",
		En:     "A description the customer will see is required, 60 characters at most.",
	})
	KeyFormCouponMinSpend = key("form.coupon.min", Message{
		ZhHant: "最低消費不能是負數。",
		En:     "A minimum spend cannot be negative.",
	})
	KeyFormCouponMaxUses = key("form.coupon.max", Message{
		ZhHant: "總使用次數不能是負數。",
		En:     "A total redemption limit cannot be negative.",
	})
	KeyFormCouponPerCustomer = key("form.coupon.percustomer", Message{
		ZhHant: "每人至少可以用一次。",
		En:     "Each customer gets at least one use.",
	})
	KeyFormCouponDays = key("form.coupon.days", Message{
		ZhHant: "天數不能是負數。",
		En:     "A number of days cannot be negative.",
	})
	KeyFormCouponKind = key("form.coupon.kind", Message{
		ZhHant: "請選擇折扣類型。",
		En:     "Pick a discount type.",
	})
	KeyFormCouponAmount = key("form.coupon.amount", Message{
		ZhHant: "折抵金額必須大於 0。",
		En:     "A discount amount has to be above zero.",
	})
	KeyFormCouponPercent = key("form.coupon.percent", Message{
		ZhHant: "折扣百分比必須介於 1 到 100。",
		En:     "A percentage discount is between 1 and 100.",
	})
	KeyFormCouponCapNegative = key("form.coupon.cap.negative", Message{
		ZhHant: "上限不能是負數。",
		En:     "A cap cannot be negative.",
	})
	KeyFormCouponCapOnAmount = key("form.coupon.cap.amount", Message{
		ZhHant: "固定金額不需要上限,上限只用在百分比折扣。",
		En:     "A fixed amount needs no cap — a cap only bounds a percentage discount.",
	})
	KeyFormCouponCapOnShipping = key("form.coupon.cap.shipping", Message{
		ZhHant: "免運不需要上限。",
		En:     "Free shipping needs no cap.",
	})
	KeyFormCouponTaken = key("form.coupon.taken", Message{
		ZhHant: "這組折扣碼已經存在了。",
		En:     "That coupon code already exists.",
	})
	KeyCouponKindAmount   = key("coupon.kind.amount", Message{ZhHant: "折抵金額", En: "Fixed amount"})
	KeyCouponKindPercent  = key("coupon.kind.percent", Message{ZhHant: "百分比折扣", En: "Percentage off"})
	KeyCouponKindShipping = key("coupon.kind.shipping", Message{ZhHant: "免運", En: "Free shipping"})
)

var (
	KeyFormMethodCode = key("form.method.code", Message{
		ZhHant: "代碼只能用小寫英數與底線,例如 home_delivery。",
		En:     "A code takes lower-case letters, digits and underscores — home_delivery, for example.",
	})
	KeyFormMethodDestination = key("form.method.destination", Message{
		ZhHant: "請選擇送到地址或送到門市。",
		En:     "Choose whether this delivers to an address or to a pickup store.",
	})
	KeyFormMethodFee = key("form.method.fee", Message{
		ZhHant: "運費超出範圍。",
		En:     "That delivery fee is out of range.",
	})
	KeyFormMethodFreeOver = key("form.method.freeover", Message{
		ZhHant: "免運門檻不能是負數。",
		En:     "A free-delivery threshold cannot be negative.",
	})
	KeyFormMethodCodeTaken = key("form.method.code.taken", Message{
		ZhHant: "這個代碼已經有配送方式用了。",
		En:     "Another delivery method already uses that code.",
	})
	KeyFormMethodParcelLimit = key("form.method.parcel.limit", Message{
		ZhHant: "上限請填 1 到 %d 的整數,或留空或填 0 表示不設限。",
		En:     "A limit is a whole number from 1 to %d, or blank or 0 for no stated limit.",
	})
	KeyFormZoneCode = key("form.zone.code", Message{
		ZhHant: "代碼只能用小寫英數與底線,例如 offshore。",
		En:     "A code takes lower-case letters, digits and underscores — offshore, for example.",
	})
	KeyFormZoneCodeTaken = key("form.zone.code.taken", Message{
		ZhHant: "這個代碼已經有區域用了。",
		En:     "Another zone already uses that code.",
	})
	KeyFormZonePrefixRequired = key("form.zone.prefix.required", Message{
		ZhHant: "請至少填一個三位數郵遞區號前綴。",
		En:     "At least one three-digit postal-code prefix is required.",
	})
	KeyFormZonePrefixTooMany = key("form.zone.prefix.toomany", Message{
		ZhHant: "一次最多 100 個前綴。",
		En:     "100 prefixes at a time, at most.",
	})
	KeyFormZonePrefixShape = key("form.zone.prefix.shape", Message{
		ZhHant: "前綴必須是三位數字,例如 880。看到的是「%s」。",
		En:     "A prefix is three digits — 880, for example. This one reads %q.",
	})
)

var (
	KeyFormHeroHeadline = key("form.hero.headline", Message{
		ZhHant: "請填寫標題,不超過 40 個字。",
		En:     "A headline is required, 40 characters at most.",
	})
	KeyFormHeroPrimary = key("form.hero.primary", Message{
		ZhHant: "請填寫主要按鈕的文字。",
		En:     "The main button needs a label.",
	})
	KeyFormHeroPrimaryHref = key("form.hero.primary.href", Message{
		ZhHant: "連結必須是本站的路徑,例如 /deals。",
		En:     "The link has to be a path on this site — /deals, for example.",
	})
	KeyFormHeroSecondPair = key("form.hero.second.pair", Message{
		ZhHant: "次要按鈕的文字和連結要一起填,或都留空。",
		En:     "The second button needs both a label and a link, or neither.",
	})
	KeyFormHeroSecondHref = key("form.hero.second.href", Message{
		ZhHant: "連結必須是本站的路徑,例如 /about。",
		En:     "The link has to be a path on this site — /about, for example.",
	})
	KeyFormHeroAlt = key("form.hero.alt", Message{
		ZhHant: "有圖片就要有說明文字 —— 讀螢幕的人靠它知道圖裡是什麼。",
		En:     "An image needs alt text — it is how somebody using a screen reader knows what it shows.",
	})
	KeyFormRunDays = key("form.run.days", Message{
		ZhHant: "檔期天數必須介於 0(不限)到 365 天。",
		En:     "A run is 0 days (no end) to 365 days.",
	})
	KeyFormBannerMessage = key("form.banner.message", Message{
		ZhHant: "請填寫訊息,不超過 60 個字。",
		En:     "A message is required, 60 characters at most.",
	})
	KeyFormBannerFieldLong = key("form.banner.field.long", Message{
		ZhHant: "不超過 60 個字。",
		En:     "60 characters at most.",
	})
	KeyFormBannerCTAPair = key("form.banner.cta.pair", Message{
		ZhHant: "按鈕文字和連結要一起填,或都留空。",
		En:     "The button needs both a label and a link, or neither.",
	})
	KeyFormBannerCTAHref = key("form.banner.cta.href", Message{
		ZhHant: "連結必須是本站路徑,例如 /deals。",
		En:     "The link has to be a path on this site — /deals, for example.",
	})

	KeyFormCampaignTitle = key("form.campaign.title", Message{
		ZhHant: "請填寫活動標題,不超過 60 個字。",
		En:     "A campaign title is required, 60 characters at most.",
	})
	KeyFormCampaignDays = key("form.campaign.days", Message{
		ZhHant: "活動天數必須介於 1 到 90 天。",
		En:     "A campaign runs 1 to 90 days.",
	})

	KeyFormFAQCategory = key("form.faq.category", Message{
		ZhHant: "請填寫分類,不超過 40 個字。",
		En:     "A category is required, 40 characters at most.",
	})
	KeyFormFAQQuestion = key("form.faq.question", Message{
		ZhHant: "請填寫問題,不超過 200 個字。",
		En:     "A question is required, 200 characters at most.",
	})
	KeyFormFAQAnswer = key("form.faq.answer", Message{
		ZhHant: "請填寫答案,不超過 2000 個字。",
		En:     "An answer is required, 2000 characters at most.",
	})
	KeyFormFAQCategoryEnLong = key("form.faq.category.en.long", Message{
		ZhHant: "英文分類太長。",
		En:     "The English category is too long.",
	})
	KeyFormFAQQuestionEnLong = key("form.faq.question.en.long", Message{
		ZhHant: "英文問題太長。",
		En:     "The English question is too long.",
	})
	KeyFormFAQAnswerEnLong = key("form.faq.answer.en.long", Message{
		ZhHant: "英文答案太長。",
		En:     "The English answer is too long.",
	})
)

var (
	KeyFormIconUnknown = key("form.icon.unknown", Message{
		ZhHant: "請從清單中選一個圖示。",
		En:     "Pick an icon from the list.",
	})
	KeyAdminColIcon  = key("admin.col.icon", Message{ZhHant: "圖示", En: "Icon"})
	KeyAdminIconNone = key("admin.icon.none", Message{ZhHant: "不設圖示", En: "No icon"})
)

// KeyAdminTaxIconOf labels the icon control on one category's row.
var KeyAdminTaxIconOf = key("admin.tax.iconof", Message{
	ZhHant: "%s 的圖示",
	En:     "Icon for %s",
})
