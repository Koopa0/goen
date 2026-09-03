package i18n

var (
	KeySectionDescription = key("pdp.description", Message{ZhHant: "商品說明", En: "Description"})

	KeySectionSpecs = key("pdp.specs", Message{ZhHant: "規格", En: "Specifications"})

	KeySectionWarranty = key("pdp.warranty", Message{ZhHant: "保固", En: "Warranty"})

	KeyPDPWarranty = key("pdp.warranty.months", Message{
		ZhHant: "保固 %s 個月",
		En:     "%s-month warranty",
	})

	KeySectionRelated = key("pdp.related", Message{ZhHant: "同類商品", En: "Similar products"})

	KeySectionAlsoBought = key("pdp.alsobought", Message{
		ZhHant: "買了這個的人也買了",
		En:     "People who bought this also bought",
	})

	KeyImagePlaceholder = key("pdp.image.placeholder", Message{
		ZhHant: "商品照待實拍素材",
		En:     "Photography pending",
	})

	KeyVariantUnavailable = key("pdp.variant.unavailable", Message{
		ZhHant: "(此組合無現貨)",
		En:     "(this combination is out of stock)",
	})

	KeyVariantNotFound = key("pdp.variant.notfound", Message{
		ZhHant: "找不到這個組合,請重新選擇。",
		En:     "That combination does not exist. Please choose again.",
	})

	// KeyAllSoldOut is the product with nothing left in any spec, distinct from
	// KeySoldOut, which is one combination.
	KeyAllSoldOut = key("pdp.allsoldout", Message{
		ZhHant: "目前全部規格都已售完",
		En:     "Every option is sold out",
	})

	KeyAllSoldOutHint = key("pdp.allsoldout.hint", Message{
		ZhHant: "選一個規格,補貨時通知你。",
		En:     "Pick an option and we will tell you when it is back.",
	})

	KeyRestockHeading = key("pdp.restock", Message{ZhHant: "到貨通知我", En: "Tell me when it is back"})

	KeyRestockDone = key("pdp.restock.done", Message{
		ZhHant: "已經記下了,補貨時會寄信給你。",
		En:     "Noted. We will email you when it is back in stock.",
	})

	KeyRestockBadEmail = key("pdp.restock.bademail", Message{
		ZhHant: "請填寫正確的 Email。",
		En:     "Enter a valid email address.",
	})

	KeyRestockSubmit = key("pdp.restock.submit", Message{ZhHant: "補貨時通知我", En: "Notify me"})

	KeyAddToCompare = key("pdp.compare.add", Message{ZhHant: "加入比較", En: "Add to compare"})

	KeyViewCompare = key("pdp.compare.view", Message{ZhHant: "查看比較", En: "View comparison"})

	KeyWishlistRemove = key("pdp.wishlist.remove", Message{
		ZhHant: "已在願望清單 · 移除",
		En:     "Saved · Remove",
	})

	KeyWishlistAdd = key("pdp.wishlist.add", Message{ZhHant: "加入願望清單", En: "Save for later"})

	KeyGuaranteeWarranty = key("pdp.guarantee.warranty", Message{
		ZhHant: "原廠保固 · 到府收送",
		En:     "Manufacturer's warranty · collected from your door",
	})

	// %s is the threshold, interpolated from shipping_method_versions: a literal
	// here is a promise that stops agreeing with what checkout charges.
	KeyGuaranteeShipping = key("pdp.guarantee.shipping", Message{
		ZhHant: "滿 %s 免運",
		En:     "Free delivery over %s",
	})

	KeyGuaranteeReturns = key("pdp.guarantee.returns", Message{
		ZhHant: "7 天鑑賞期退換貨",
		En:     "7-day return window",
	})

	KeyProductNotFound = key("pdp.notfound", Message{ZhHant: "找不到這個商品", En: "Product not found"})

	KeyProductNotFoundBody = key("pdp.notfound.body", Message{
		ZhHant: "這個商品目前沒有販售,可能已經下架。回首頁看看其他選擇。",
		En: "This product is not on sale — it may have been discontinued. " +
			"Have a look at what else there is.",
	})

	KeyCannotLoad = key("error.cannotload", Message{ZhHant: "暫時無法載入", En: "Cannot load this right now"})

	KeyCannotLoadProduct = key("error.cannotload.product", Message{
		ZhHant: "商品資訊暫時無法顯示,請稍後再試。",
		En:     "We cannot show this product right now. Please try again shortly.",
	})
)

var (
	KeyFormSlugFormatExample = key("form.slug.format.example", Message{
		ZhHant: "網址代稱只能用小寫英數與連字號,例如 pixelight-9-pro。",
		En:     "A slug takes lower-case letters, digits and hyphens only — pixelight-9-pro, for example.",
	})

	KeyFormSlugTakenProduct = key("form.slug.taken.product", Message{
		ZhHant: "這個網址代稱已經有人用了。",
		En:     "Something already uses that slug.",
	})

	KeyAdminProductDraft = key("admin.product.draft", Message{ZhHant: "草稿", En: "Draft"})

	KeyAdminProductActive = key("admin.product.active", Message{ZhHant: "已上架", En: "Published"})

	KeyAdminProductArchived = key("admin.product.archived", Message{ZhHant: "已封存", En: "Archived"})

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

	KeyFormBrandRequired = key("form.brand.required", Message{ZhHant: "請選擇品牌。", En: "Pick a brand."})

	KeyFormCategoryRequired = key("form.category.required", Message{ZhHant: "請選擇分類。", En: "Pick a category."})

	KeyAdminPageProducts = key("admin.page.products", Message{ZhHant: "商品", En: "Products"})

	KeyAdminPageNewProduct = key("admin.page.product.new", Message{
		ZhHant: "新增商品",
		En:     "Add a product",
	})

	KeyAdminProdLead = key("admin.prod.lead", Message{
		ZhHant: "新商品是草稿,加了變體、確認資料之後再上架。",
		En: "A new product is a draft. Add its variants, check the details, and publish it " +
			"after that.",
	})

	KeyAdminProdEmpty = key("admin.prod.empty", Message{
		ZhHant: "目錄裡還沒有商品。",
		En:     "Nothing in the catalogue yet.",
	})

	KeyAdminProdName = key("admin.prod.name", Message{ZhHant: "商品名稱", En: "Product name"})

	KeyAdminProdSlugHint = key("admin.prod.slughint", Message{
		ZhHant: "上架後不能更改,商品網址會是 /p/ 加上這串。",
		En:     "It cannot be changed once the product is published; the product's address is /p/ followed by it.",
	})

	KeyAdminProdBrand = key("admin.prod.brand", Message{ZhHant: "品牌", En: "Brand"})

	KeyAdminProdChoose = key("admin.prod.choose", Message{ZhHant: "請選擇", En: "Choose one"})

	KeyAdminProdSummary = key("admin.prod.summary", Message{
		ZhHant: "一句話簡介",
		En:     "One-line summary",
	})

	KeyAdminProdDescription = key("admin.prod.description", Message{
		ZhHant: "商品說明",
		En:     "Description",
	})

	KeyAdminProdNameEn = key("admin.prod.nameen", Message{
		ZhHant: "商品名稱(英文)",
		En:     "Product name (English)",
	})

	KeyAdminProdSummaryEn = key("admin.prod.summaryen", Message{
		ZhHant: "一句話簡介(英文)",
		En:     "One-line summary (English)",
	})

	KeyAdminProdDescriptionEn = key("admin.prod.descriptionen", Message{
		ZhHant: "商品說明(英文)",
		En:     "Description (English)",
	})

	KeyAdminProdWarrantyMonths = key("admin.prod.warrantymonths", Message{
		ZhHant: "保固月數",
		En:     "Warranty term (months)",
	})

	KeyAdminProdWarrantyHint = key("admin.prod.warrantyhint", Message{
		ZhHant: "每個商品可以不一樣 —— 手機和編織線本來就不該是同一個數字。留空表示沒有提供保固,顧客就無法登錄保固(而不是給他一個系統自己編出來的期限)。",
		En: "It is per product — a phone and a braided cable were never going to carry the same " +
			"number. Left blank it states no cover, and the customer then cannot register a " +
			"warranty at all, rather than being given a term the system invented for them.",
	})

	KeyAdminProdWarrantyNote = key("admin.prod.warrantynote", Message{
		ZhHant: "保固說明",
		En:     "Warranty note",
	})

	KeyAdminProdCreateDraft = key("admin.prod.createdraft", Message{
		ZhHant: "建立草稿",
		En:     "Create draft",
	})

	KeyAdminProdSave = key("admin.prod.save", Message{ZhHant: "儲存", En: "Save"})

	KeyAdminProdNoImages = key("admin.prod.noimages", Message{
		ZhHant: "這個商品還沒有圖片。",
		En:     "This product has no images yet.",
	})

	KeyAdminProdAddImage = key("admin.prod.addimage", Message{ZhHant: "加入圖片", En: "Add image"})

	KeyAdminProdNoOptions = key("admin.prod.nooptions", Message{
		ZhHant: "還沒有規格項目。單一規格的商品不需要。",
		En:     "No options yet. A product with a single variant does not need any.",
	})

	KeyAdminProdNoVariants = key("admin.prod.novariants", Message{
		ZhHant: "還沒有規格。沒有規格的商品沒有價格,無法上架。",
		En:     "No variants yet. A product with no variant has no price and cannot be published.",
	})

	KeyAdminProdWasPrice = key("admin.prod.wasprice", Message{ZhHant: "· 原價 %s", En: "· was %s"})

	KeyAdminProdNoSpecs = key("admin.prod.nospecs", Message{
		ZhHant: "還沒有規格表。",
		En:     "No specification table yet.",
	})

	KeyAdminProdPublishing = key("admin.prod.publishing", Message{
		ZhHant: "上架狀態",
		En:     "Publication status",
	})

	KeyAdminProdNeedsVariant = key("admin.prod.needsvariant", Message{
		ZhHant: "先新增至少一個規格,才能上架。",
		En:     "Add at least one variant before this can be published.",
	})

	KeyAdminProdPublish = key("admin.prod.publish", Message{ZhHant: "上架", En: "Publish"})

	KeyAdminProdUnpublish = key("admin.prod.unpublish", Message{
		ZhHant: "下架為草稿",
		En:     "Unpublish to draft",
	})

	KeyAdminProdArchive = key("admin.prod.archive", Message{ZhHant: "封存", En: "Archive"})
)
