package i18n

var (
	KeySearchTitle = key("search.title", Message{ZhHant: "搜尋", En: "Search"})

	KeySearchFor = key("search.for", Message{ZhHant: "搜尋「%s」", En: "Search for “%s”"})

	KeyBreadcrumb = key("nav.breadcrumb", Message{ZhHant: "麵包屑", En: "Breadcrumb"})

	KeyHome = key("nav.home", Message{ZhHant: "首頁", En: "Home"})

	// The shelf of every product, and the first line of the sidebar's tree.
	KeyShopAll = key("listing.all", Message{ZhHant: "全館選物", En: "All products"})

	KeyListingCount = countKey("listing.count", "共 %s 件商品", "%s product", "%s products")

	KeyListingEmpty = key("listing.empty", Message{
		ZhHant: "找不到符合的商品",
		En:     "Nothing matches those filters",
	})

	KeyListingEmptyHint = key("listing.empty.hint", Message{
		ZhHant: "試著放寬篩選條件，或",
		En:     "Try loosening a filter, or",
	})

	KeyListingEmptyReset = key("listing.empty.reset", Message{
		ZhHant: "清除所有篩選條件",
		En:     "clear every filter",
	})

	KeyFilters = key("listing.filters", Message{ZhHant: "篩選", En: "Filters"})

	KeyResetFilters = key("listing.filters.reset", Message{ZhHant: "重設條件", En: "Reset"})

	KeyCloseFilters = key("listing.filters.close", Message{ZhHant: "關閉篩選", En: "Close filters"})

	// The drawer's last button: it closes the drawer onto the results it
	// counts, which are already shown under it.
	KeyShowResults = countKey("listing.filters.done", "顯示 %s 件商品", "Show %s product", "Show %s products")

	KeyFiltersActive = key("listing.filters.active", Message{
		ZhHant: "已套用篩選",
		En:     "Active filters",
	})

	KeyFiltersSelected = key("listing.filters.selected", Message{
		ZhHant: "已選 %d 項",
		En:     "%d selected",
	})

	KeyPriceRangeChip = key("listing.filters.chip.price.range", Message{
		ZhHant: "%s–%s",
		En:     "%s–%s",
	})

	KeyPriceFromChip = key("listing.filters.chip.price.from", Message{
		ZhHant: "%s 起",
		En:     "From %s",
	})

	KeyPriceUnder = key("listing.filters.price.under", Message{
		ZhHant: "未滿 %s",
		En:     "Under %s",
	})

	KeyPriceAny = key("listing.filters.price.any", Message{ZhHant: "全部價格", En: "Any price"})

	KeyRemoveFilter = key("listing.filters.remove", Message{ZhHant: "移除「%s」", En: "Remove “%s”"})

	KeyClearFilters = key("listing.filters.clear", Message{ZhHant: "清除全部", En: "Clear all"})

	KeyFacetBrand = key("listing.facet.brand", Message{ZhHant: "品牌", En: "Brand"})

	KeyFacetInStock = key("listing.facet.instock", Message{ZhHant: "現貨供應", En: "In stock"})

	KeyFacetShowOnly = key("listing.facet.only", Message{ZhHant: "只看", En: "Show only"})

	KeyFacetPrice = key("listing.facet.price", Message{ZhHant: "價格區間", En: "Price"})

	KeySearchAllResults = key("search.facet.all", Message{ZhHant: "全部結果", En: "All results"})

	KeyViewSwitch = key("listing.view", Message{ZhHant: "檢視方式", En: "View"})

	KeyViewGrid = key("listing.view.grid", Message{ZhHant: "圖卡格線", En: "Grid"})

	KeyViewList = key("listing.view.list", Message{ZhHant: "規格清單", En: "Spec list"})

	// Read before a specification's value in the list, which shows only the
	// value: "螢幕：6.7 吋".
	KeySpecNamed = key("listing.spec.named", Message{ZhHant: "%s：", En: "%s: "})

	KeySort = key("listing.sort", Message{ZhHant: "排序", En: "Sort"})

	KeySortNewest = key("listing.sort.newest", Message{ZhHant: "最新上架", En: "Newest"})

	KeySortBestMatch = key("listing.sort.bestmatch", Message{ZhHant: "最相關", En: "Best match"})

	KeySortPriceAsc = key("listing.sort.price.asc", Message{ZhHant: "價格由低到高", En: "Price, low to high"})

	KeySortPriceDesc = key("listing.sort.price.desc", Message{ZhHant: "價格由高到低", En: "Price, high to low"})

	KeySortRating = key("listing.sort.rating", Message{ZhHant: "評價最高", En: "Best rated"})

	KeyPagination = key("listing.pager", Message{ZhHant: "分頁", En: "Pagination"})

	KeyPrevPage = key("listing.pager.prev", Message{ZhHant: "上一頁", En: "Previous"})

	KeyNextPage = key("listing.pager.next", Message{ZhHant: "下一頁", En: "Next"})

	KeySubcategories = key("listing.subcategories", Message{ZhHant: "子分類", En: "Subcategories"})

	KeyShowMore = key("listing.pager.more", Message{ZhHant: "顯示更多", En: "Show more"})

	KeyPageOf = key("listing.pager.at", Message{ZhHant: "第 %s / %s 頁", En: "Page %s of %s"})

	KeySearchHeading = key("search.heading", Message{ZhHant: "搜尋商品", En: "Search products"})

	KeySearchResults = countKey("search.results", "找到 %s 件商品", "%s product found", "%s products found")

	KeyResultsHeading = key("search.results.heading", Message{ZhHant: "搜尋結果", En: "Results"})

	KeySearchPrompt = key("search.prompt", Message{ZhHant: "想找什麼？", En: "What are you looking for?"})

	KeySearchPromptHint = key("search.prompt.hint", Message{
		ZhHant: "用上方的搜尋框輸入商品名稱、品牌或規格。",
		En:     "Use the box above: a product name, a brand, or a specification.",
	})

	KeySearchNoResults = key("search.none", Message{
		ZhHant: "找不到「%s」",
		En:     "Nothing found for %q",
	})

	KeySearchNoResultsHint = key("search.none.hint", Message{
		ZhHant: "試試更短的關鍵字，或從下面的館別開始逛。",
		En:     "Try a shorter term, or start from one of these departments.",
	})

	KeySearchNewest = key("search.none.newest", Message{ZhHant: "最新上架的商品", En: "Newest products"})

	KeyOnSale = key("card.onsale", Message{ZhHant: "特價", En: "On sale"})

	KeyWasPrice = key("card.wasprice", Message{ZhHant: "原價", En: "Was"})

	KeyNoPhoto = key("card.nophoto", Message{ZhHant: "沒有照片", En: "No photo"})

	// KeyColourCount is said to a screen reader, which cannot see the dots.
	KeyColourCount = countKey("card.colours", "%d 種顏色", "%d colour", "%d colours")

	// KeyColourCountSoldOut is KeyColourCount when some colours are sold out;
	// the second argument is their names, joined by JoinList.
	KeyColourCountSoldOut = countKey("card.colours.soldout", "%d 種顏色，%s已售完", "%d colour, %s sold out", "%d colours, %s sold out")

	KeyRatingSummary = countKey("card.rating",
		"評分 %s 分，共 %s 則評價",
		"Rated %s out of 5, from %s review",
		"Rated %s out of 5, from %s reviews")

	KeyCategoryNotFound = key("listing.notfound", Message{ZhHant: "找不到這個分類", En: "Category not found"})

	KeyCategoryNotFoundBody = key("listing.notfound.body", Message{
		ZhHant: "這個分類目前不存在，可能已經調整過。回首頁看看其他分類。",
		En:     "That category does not exist — it may have been reorganised. Try the home page.",
	})

	KeyCannotLoadListing = key("error.cannotload.listing", Message{
		ZhHant: "商品列表暫時無法顯示，請稍後再試。",
		En:     "We cannot show the product list right now. Please try again shortly.",
	})

	// KeyFromPrice marks the cheapest of several variant prices. The figure is
	// INSIDE the message because 起 is a suffix and "From" is a prefix, so a
	// bare word beside the price cannot serve both.
	KeyFromPrice = key("price.from", Message{ZhHant: "%s 起", En: "From %s"})
)
