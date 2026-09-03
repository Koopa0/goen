package i18n

var (
	KeyCompareTitle = key("compare.title", Message{ZhHant: "比較", En: "Compare"})

	KeyCompareDescription = key("compare.description", Message{
		ZhHant: "把規格擺在一起看,而不是在兩個分頁之間來回。",
		En:     "Put the specifications side by side instead of flipping between two tabs.",
	})

	KeyCompareInStock = key("compare.instock", Message{ZhHant: "有貨", En: "In stock"})

	KeyWarrantyYears = key("compare.warranty.years", Message{ZhHant: "%d 年", En: "%d years"})

	KeyWarrantyMonths = key("compare.warranty.months", Message{ZhHant: "%d 個月", En: "%d months"})

	KeyCompareHeading = key("compare.heading", Message{
		ZhHant: "把規格擺在一起",
		En:     "Specifications, side by side",
	})

	KeyCompareSub = key("compare.sub", Message{
		ZhHant: "每個商品都有的規格排在前面 —— 那些才是真的能比的。",
		En: "The specifications every product states come first — those are the ones " +
			"that actually compare.",
	})

	KeyCompareTooFew = key("compare.toofew", Message{
		ZhHant: "至少要選兩個商品才能比較",
		En:     "Pick at least two products to compare",
	})

	KeyCompareTooFewHint = key("compare.toofew.hint", Message{
		ZhHant: "到",
		En:     "Add them from a product page — up to four. Start from the ",
	})

	KeyCompareTooFewLink = key("compare.toofew.link", Message{
		ZhHant: "商品列表",
		En:     "product list",
	})

	KeyCompareTooFewTail = key("compare.toofew.tail", Message{
		ZhHant: "裡從商品頁加入比較,最多四個。",
		En:     ".",
	})

	KeyCompareAdd = key("compare.add", Message{ZhHant: "比較", En: "Compare"})

	// KeyCompareAddNamed is the checkbox's accessible name; a screen reader
	// would otherwise read "Compare" once per tile.
	KeyCompareAddNamed = key("compare.add.named", Message{
		ZhHant: "把 %s 加入比較",
		En:     "Add %s to the comparison",
	})

	// KeyCompareLimit is on the form rather than only in the empty state: ticking
	// six and being shown four is a cap that never said so.
	KeyCompareLimit = key("compare.limit", Message{
		ZhHant: "最多比較四個",
		En:     "Up to four at a time",
	})

	KeyCompareSelected = key("compare.selected", Message{
		ZhHant: "比較所選商品",
		En:     "Compare selected",
	})

	KeyCompareCaption = key("compare.caption", Message{
		ZhHant: "商品規格比較",
		En:     "Product specification comparison",
	})

	KeyCompareRowLabel = key("compare.row.label", Message{ZhHant: "項目", En: "Attribute"})

	KeyCompareRowPrice = key("compare.row.price", Message{ZhHant: "價格", En: "Price"})

	KeyCompareRowStock = key("compare.row.stock", Message{ZhHant: "供貨", En: "Availability"})

	KeyCompareRowRating = key("compare.row.rating", Message{ZhHant: "評價", En: "Rating"})

	KeyCompareRowWarranty = key("compare.row.warranty", Message{ZhHant: "保固", En: "Warranty"})

	KeyCompareRowCategory = key("compare.row.category", Message{ZhHant: "分類", En: "Category"})
)
