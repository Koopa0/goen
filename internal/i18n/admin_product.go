package i18n

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
