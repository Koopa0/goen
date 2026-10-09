package i18n

var (
	KeySectionDescription = key("pdp.description", Message{ZhHant: "商品說明", En: "Description"})

	// The source of a campaign price takes the campaign, what is left of it and the date of its last day, and
	// names the campaign first: the page links the one and sets the other apart.
	KeyCampaignPriceDaysLeft = key("pdp.campaignprice.daysleft", Message{ZhHant: "「%[1]s」活動價，%[2]s，至 %[3]s", En: "%[1]s price, %[2]s, until %[3]s"})

	KeyCampaignPriceToday = key("pdp.campaignprice.today", Message{ZhHant: "「%[1]s」活動價，%[2]s", En: "%[1]s price, %[2]s"})

	KeySectionSpecs = key("pdp.specs", Message{ZhHant: "規格", En: "Specifications"})

	KeySectionWarranty = key("pdp.warranty", Message{ZhHant: "保固", En: "Warranty"})

	KeyPDPWarranty = key("pdp.warranty.months", Message{
		ZhHant: "保固 %s 個月",
		En:     "%s-month warranty",
	})

	KeySectionDelivery = key("pdp.delivery", Message{ZhHant: "配送與退貨", En: "Delivery and returns"})

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
		ZhHant: "（此組合無現貨）",
		En:     "(this combination is out of stock)",
	})

	KeyVariantNotFound = key("pdp.variant.notfound", Message{
		ZhHant: "找不到這個組合，請重新選擇。",
		En:     "That combination does not exist. Please choose again.",
	})

	KeyRestockPick = key("pdp.restock.pick", Message{
		ZhHant: "選一個款式，有貨時通知你。",
		En:     "Pick an option and we will tell you when it is available.",
	})

	KeyRestockNoOption = key("pdp.restock.nooption", Message{
		ZhHant: "請先選一個款式。",
		En:     "Pick an option first.",
	})

	KeyRestockNote = key("pdp.restock.note", Message{
		ZhHant: "有貨時通知你。",
		En:     "We will tell you when it is available.",
	})

	KeyOptionChosen   = key("pdp.option.chosen", Message{ZhHant: "%s：", En: "%s: "})
	KeyOptionChoose   = key("pdp.option.choose", Message{ZhHant: "請選擇", En: "Please choose"})
	KeyBrowseCategory = key("pdp.category.browse", Message{ZhHant: "瀏覽「%s」全部商品", En: "Browse all %s products"})

	KeyRestockHeading = key("pdp.restock", Message{ZhHant: "到貨通知我", En: "Tell me when it is back"})

	KeyRestockDoneTo = key("pdp.restock.doneto", Message{
		ZhHant: "已經記下了，有貨時會寄信到 %s。",
		En:     "Noted. We will email %s when it is back in stock.",
	})

	KeyRestockDone = key("pdp.restock.done", Message{
		ZhHant: "已經記下了，有貨時會寄信給你。",
		En:     "Noted. We will email you when it is back in stock.",
	})

	KeyRestockBadEmail = key("pdp.restock.bademail", Message{
		ZhHant: "請填寫正確的電子郵件。",
		En:     "Enter a valid email address.",
	})

	KeyRestockUnavailable = key("pdp.restock.unavailable", Message{
		ZhHant: "這個規格目前不需要補貨通知（已經有貨，或已不存在）。請確認規格後再試。",
		En:     "This option needs no restock notice (it is in stock, or it is gone). Check the option and try again.",
	})

	KeyRestockSubmit = key("pdp.restock.submit", Message{ZhHant: "有貨時通知我", En: "Notify me"})

	KeyAddToCompare = key("pdp.compare.add", Message{ZhHant: "加入比較", En: "Add to compare"})

	// KeyCompareSimilar is the product page's one way into a comparison, offered
	// only where the department compares: it says what it compares WITH.
	KeyCompareSimilar = key("pdp.compare.similar", Message{ZhHant: "與同類商品比較", En: "Compare with similar"})

	KeyViewCompare = key("pdp.compare.view", Message{ZhHant: "查看比較", En: "View comparison"})

	KeyWishlistRemove = key("pdp.wishlist.remove", Message{
		ZhHant: "已在願望清單 · 移除",
		En:     "Saved · Remove",
	})

	KeyWishlistAdd = key("pdp.wishlist.add", Message{ZhHant: "加入願望清單", En: "Save for later"})

	KeyProductNotFound = key("pdp.notfound", Message{ZhHant: "找不到這個商品", En: "Product not found"})

	KeyProductNotFoundBody = key("pdp.notfound.body", Message{
		ZhHant: "這個商品目前沒有販售，可能已經下架。回首頁看看其他選擇。",
		En: "This product is not on sale — it may have been discontinued. " +
			"Have a look at what else there is.",
	})

	KeyCannotLoad = key("error.cannotload", Message{ZhHant: "暫時無法載入", En: "Cannot load this right now"})

	KeyCannotLoadProduct = key("error.cannotload.product", Message{
		ZhHant: "商品資訊暫時無法顯示，請稍後再試。",
		En:     "We cannot show this product right now. Please try again shortly.",
	})
)

var (
	KeyFormSlugFormatExample = key("form.slug.format.example", Message{
		ZhHant: "網址代稱只能用小寫英數與連字號，例如 ceramic-mug。",
		En:     "A slug takes lower-case letters, digits and hyphens only — ceramic-mug, for example.",
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
		ZhHant: "保固月數請填 1 到 120，或留空表示未提供保固。",
		En:     "A warranty term is 1 to 120 months, or blank for no stated cover.",
	})

	KeyFormBrandInvalid = key("form.brand.invalid", Message{ZhHant: "請選擇有效的品牌。", En: "Pick a valid brand."})

	KeyFormCategoryRequired = key("form.category.required", Message{ZhHant: "請選擇分類。", En: "Pick a category."})

	KeyAdminPageProducts = key("admin.page.products", Message{ZhHant: "商品", En: "Products"})

	KeyAdminPageNewProduct = key("admin.page.product.new", Message{
		ZhHant: "新增商品",
		En:     "Add a product",
	})

	KeyAdminProdPublished = countKey("admin.prod.published", "上架中 %d 項商品", "%d product published", "%d products published")

	KeyAdminProdLead = key("admin.prod.lead", Message{
		ZhHant: "新商品是草稿，加了變體、確認資料之後再上架。",
		En: "A new product is a draft. Add its variants, check the details, and publish it " +
			"after that.",
	})

	KeyAdminProdEmpty = key("admin.prod.empty", Message{
		ZhHant: "目錄裡還沒有商品。",
		En:     "Nothing in the catalogue yet.",
	})

	KeyAdminProdName = key("admin.prod.name", Message{ZhHant: "商品名稱", En: "Product name"})

	KeyAdminProdBasics = key("admin.prod.basics", Message{ZhHant: "基本資料", En: "Basic details"})

	KeyAdminProdSlugHint = key("admin.prod.slughint", Message{
		ZhHant: "上架後不能更改，商品網址會是 /p/ 加上這串。",
		En:     "It cannot be changed once the product is published; the product's address is /p/ followed by it.",
	})

	KeyAdminProdBrand   = key("admin.prod.brand", Message{ZhHant: "品牌", En: "Brand"})
	KeyAdminProdNoBrand = key("admin.prod.no-brand", Message{ZhHant: "無品牌", En: "No brand"})

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
		ZhHant: "商品名稱（英文）",
		En:     "Product name (English)",
	})

	KeyAdminProdSummaryEn = key("admin.prod.summaryen", Message{
		ZhHant: "一句話簡介（英文）",
		En:     "One-line summary (English)",
	})

	KeyAdminProdDescriptionEn = key("admin.prod.descriptionen", Message{
		ZhHant: "商品說明（英文）",
		En:     "Description (English)",
	})

	KeyAdminProdWarrantyMonths = key("admin.prod.warrantymonths", Message{
		ZhHant: "保固月數",
		En:     "Warranty term (months)",
	})

	KeyAdminProdWarrantyHint = key("admin.prod.warrantyhint", Message{
		ZhHant: "依商品填寫。留空表示不提供保固，顧客也無法登錄保固。",
		En:     "Set per product. Leave it blank for no warranty; the customer then cannot register one.",
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
		ZhHant: "還沒有規格。沒有規格的商品沒有價格，無法上架。",
		En:     "No variants yet. A product with no variant has no price and cannot be published.",
	})

	KeyAdminProdWasPrice = key("admin.prod.wasprice", Message{ZhHant: "· 原價 %s", En: "· was %s"})

	KeyAdminProdNoSpecs = key("admin.prod.nospecs", Message{
		ZhHant: "還沒有規格表。",
		En:     "No specification table yet.",
	})

	KeyAdminProdSectionNav = key("admin.prod.sectionnav", Message{ZhHant: "商品區段", En: "Product sections"})

	KeyAdminProdPublishing = key("admin.prod.publishing", Message{
		ZhHant: "上架狀態",
		En:     "Publication status",
	})

	KeyAdminProdNeedsVariant = key("admin.prod.needsvariant", Message{
		ZhHant: "先新增至少一個規格，才能上架。",
		En:     "Add at least one variant before this can be published.",
	})

	KeyAdminProdPublish = key("admin.prod.publish", Message{ZhHant: "上架", En: "Publish"})

	KeyAdminProdUnpublish = key("admin.prod.unpublish", Message{
		ZhHant: "下架為草稿",
		En:     "Unpublish to draft",
	})

	KeyAdminProdArchive = key("admin.prod.archive", Message{ZhHant: "封存", En: "Archive"})
)
