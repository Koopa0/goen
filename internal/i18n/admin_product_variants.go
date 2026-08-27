package i18n

var (
	KeyFormSKURequired = key("form.sku.required", Message{ZhHant: "請填寫 SKU。", En: "A SKU is required."})

	KeyFormSKUTaken = key("form.sku.taken", Message{
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

	KeyAdminProdVariantsFrom = key("admin.prod.variantsfrom", Message{
		ZhHant: "%s 個規格 · %s 起",
		En:     "%s variants · from %s",
	})

	KeyAdminProdVariants = key("admin.prod.variants", Message{ZhHant: "規格", En: "Variants"})

	KeyAdminProdPrice = key("admin.prod.price", Message{ZhHant: "售價(元)", En: "Price (NT$)"})

	KeyAdminProdCompare = key("admin.prod.compare", Message{
		ZhHant: "原價(元)",
		En:     "Compare-at price (NT$)",
	})

	KeyAdminProdSafety = key("admin.prod.safety", Message{ZhHant: "安全庫存", En: "Safety stock"})

	KeyAdminProdLongest = key("admin.prod.longest", Message{
		ZhHant: "最長邊(mm)",
		En:     "Longest side (mm)",
	})

	KeyAdminProdParcelSum = key("admin.prod.parcelsum", Message{
		ZhHant: "三邊合(mm)",
		En:     "Sum of the three sides (mm)",
	})

	KeyAdminProdWeight = key("admin.prod.weight", Message{ZhHant: "重量(g)", En: "Weight (g)"})

	KeyAdminProdVariantAdd = key("admin.prod.variantadd", Message{ZhHant: "新增規格", En: "Add variant"})

	KeyAdminProdVariantStockHint = key("admin.prod.variantstockhint", Message{
		ZhHant: "新規格的庫存是 0。庫存只能從「庫存」頁調整,那裡每一次異動都會寫進帳本。",
		En: "A new variant starts at zero stock. Stock changes only from the Stock page, where " +
			"every movement is written to the ledger.",
	})
)
