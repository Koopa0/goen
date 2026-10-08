package i18n

var (
	KeyCompareTitle = key("compare.title", Message{ZhHant: "比較", En: "Compare"})

	// KeyCompareChoose heads the page while there is nothing yet to put side by
	// side: it says what the reader is here to do.
	KeyCompareChoose = key("compare.choose", Message{
		ZhHant: "選擇要比較的商品",
		En:     "Choose products to compare",
	})

	// KeyCompareBrowse is the link to the whole shelf the chosen product is on;
	// %s is that shelf's name.
	KeyCompareBrowse = key("compare.browse", Message{
		ZhHant: "瀏覽「%s」的全部商品",
		En:     "Browse everything in %s",
	})

	// KeyCompareDiffers is said to a screen reader on a row where the products
	// disagree, which the page otherwise shows by weight alone.
	KeyCompareDiffers = key("compare.differs", Message{ZhHant: "規格不同", En: "Differs"})

	KeyCompareDescription = key("compare.description", Message{
		ZhHant: "把規格擺在一起看，而不是在兩個分頁之間來回。",
		En:     "Put the specifications side by side instead of flipping between two tabs.",
	})

	KeyCompareHeading = key("compare.heading", Message{
		ZhHant: "把規格擺在一起",
		En:     "Specifications, side by side",
	})

	KeyCompareSub = key("compare.sub", Message{
		ZhHant: "每個商品都有標示的規格排在前面，這些最能直接比較。",
		En: "The specifications every product states come first — those are the ones " +
			"that actually compare.",
	})

	KeyCompareTooFew = key("compare.toofew", Message{
		ZhHant: "至少要選兩個商品才能比較",
		En:     "Pick at least two products to compare",
	})

	KeyCompareTooFewHint = key("compare.toofew.hint", Message{
		ZhHant: "在",
		En:     "Tick Compare on products in the ",
	})

	KeyCompareTooFewLink = key("compare.toofew.link", Message{
		ZhHant: "商品列表",
		En:     "product list",
	})

	// KeyCompareTooFewTail ends the sentence KeyCompareTooFewHint and the link
	// begin; %d is the most products a comparison holds.
	KeyCompareTooFewTail = key("compare.toofew.tail", Message{
		ZhHant: "勾選商品上的「比較」，最多 %d 件。",
		En:     ", up to %d.",
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
		ZhHant: "最多比較 %d 件",
		En:     "Up to %d at a time",
	})

	KeyCompareFull = key("compare.full", Message{
		ZhHant: "比較已滿 %d 個，請先移除一個再加入。",
		En:     "The comparison holds %d products. Remove one to add another.",
	})

	KeyCompareOverflow = key("compare.overflow", Message{
		ZhHant: "連結列出的商品超過 %d 個，只顯示前 %d 個。",
		En:     "The link names more than %d products; only the first %d are shown.",
	})

	KeyCompareSearchLabel = key("compare.search.label", Message{
		ZhHant: "搜尋要加入比較的商品",
		En:     "Search for a product to add",
	})

	KeyCompareNoMatch = key("compare.search.none", Message{
		ZhHant: "沒有可加入的相符商品。",
		En:     "No matching product to add.",
	})

	KeyCompareOneChosen = key("compare.one", Message{
		ZhHant: "%s 已在比較中，再加入至少一個商品就能並排比較。",
		En:     "%s is in the comparison. Add at least one more product to compare side by side.",
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

	KeyCompareRowCategory = key("compare.row.category", Message{ZhHant: "館別", En: "Department"})
)
