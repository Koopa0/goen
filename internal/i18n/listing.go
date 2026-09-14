package i18n

var (
	KeyListingDescription = key("listing.description", Message{
		ZhHant: "%s — goen。台灣出貨。保固期限寫在各商品頁。",
		En:     "%s — goen. Ships from Taiwan. Warranty terms are on each product page.",
	})

	KeySearchTitle = key("search.title", Message{ZhHant: "搜尋", En: "Search"})

	KeySearchFor = key("search.for", Message{ZhHant: "搜尋:%s", En: "Search: %s"})

	KeyBreadcrumb = key("nav.breadcrumb", Message{ZhHant: "麵包屑", En: "Breadcrumb"})

	KeyHome = key("nav.home", Message{ZhHant: "首頁", En: "Home"})

	KeyListingCount = key("listing.count", Message{ZhHant: "共 %s 件商品", En: "%s products"})

	KeyListingEmpty = key("listing.empty", Message{
		ZhHant: "找不到符合的商品",
		En:     "Nothing matches those filters",
	})

	KeyListingEmptyHint = key("listing.empty.hint", Message{
		ZhHant: "試著放寬篩選條件,或",
		En:     "Try loosening a filter, or",
	})

	KeyListingEmptyLink = key("listing.empty.link", Message{
		ZhHant: "看這個分類的全部商品",
		En:     "see everything in this category",
	})

	KeyFilters = key("listing.filters", Message{ZhHant: "篩選", En: "Filters"})

	KeyClearFilters = key("listing.filters.clear", Message{ZhHant: "清除全部", En: "Clear all"})

	KeyApplyFilters = key("listing.filters.apply", Message{ZhHant: "套用篩選", En: "Apply filters"})

	KeyFacetBrand = key("listing.facet.brand", Message{ZhHant: "品牌", En: "Brand"})

	KeyFacetStock = key("listing.facet.stock", Message{ZhHant: "供應狀況", En: "Availability"})

	KeyFacetInStock = key("listing.facet.instock", Message{ZhHant: "現貨供應", En: "In stock"})

	KeyFacetPrice = key("listing.facet.price", Message{ZhHant: "價格", En: "Price"})

	KeyPriceMin = key("listing.facet.price.min", Message{ZhHant: "最低價", En: "Lowest price"})

	KeyPriceMax = key("listing.facet.price.max", Message{ZhHant: "最高價", En: "Highest price"})

	KeyPriceMinShort = key("listing.facet.price.min.short", Message{ZhHant: "最低", En: "Min"})

	KeyPriceMaxShort = key("listing.facet.price.max.short", Message{ZhHant: "最高", En: "Max"})

	KeySort = key("listing.sort", Message{ZhHant: "排序", En: "Sort"})

	KeySortNewest = key("listing.sort.newest", Message{ZhHant: "最新上架", En: "Newest"})

	KeySortPriceAsc = key("listing.sort.price.asc", Message{ZhHant: "價格由低到高", En: "Price, low to high"})

	KeySortPriceDesc = key("listing.sort.price.desc", Message{ZhHant: "價格由高到低", En: "Price, high to low"})

	KeySortRating = key("listing.sort.rating", Message{ZhHant: "評價最高", En: "Best rated"})

	KeyPagination = key("listing.pager", Message{ZhHant: "分頁", En: "Pagination"})

	KeyPrevPage = key("listing.pager.prev", Message{ZhHant: "上一頁", En: "Previous"})

	KeyNextPage = key("listing.pager.next", Message{ZhHant: "下一頁", En: "Next"})

	KeyPageOf = key("listing.pager.at", Message{ZhHant: "第 %s / %s 頁", En: "Page %s of %s"})

	KeySearchHeading = key("search.heading", Message{ZhHant: "搜尋商品", En: "Search products"})

	KeySearchResults = key("search.results", Message{ZhHant: "找到 %s 件商品", En: "%s products found"})

	KeyResultsHeading = key("search.results.heading", Message{ZhHant: "搜尋結果", En: "Results"})

	KeySearchPrompt = key("search.prompt", Message{ZhHant: "想找什麼?", En: "What are you looking for?"})

	KeySearchPromptHint = key("search.prompt.hint", Message{
		ZhHant: "用上方的搜尋框輸入商品名稱、品牌或規格。",
		En:     "Use the box above: a product name, a brand, or a specification.",
	})

	KeySearchNoResults = key("search.none", Message{
		ZhHant: "找不到「%s」",
		En:     "Nothing found for %q",
	})

	KeySearchNoResultsHint = key("search.none.hint", Message{
		ZhHant: "試試更短的關鍵字,或",
		En:     "Try a shorter term, or",
	})

	KeySearchNoResultsLink = key("search.none.link", Message{
		ZhHant: "回首頁瀏覽分類",
		En:     "browse the categories from the home page",
	})

	KeyOnSale = key("card.onsale", Message{ZhHant: "特價", En: "On sale"})

	KeyWasPrice = key("card.wasprice", Message{ZhHant: "原價", En: "Was"})

	KeyRatingSummary = key("card.rating", Message{
		ZhHant: "評分 %s 分,共 %s 則評價",
		En:     "Rated %s out of 5, from %s reviews",
	})

	KeyCategoryNotFound = key("listing.notfound", Message{ZhHant: "找不到這個分類", En: "Category not found"})

	KeyCategoryNotFoundBody = key("listing.notfound.body", Message{
		ZhHant: "這個分類目前不存在,可能已經調整過。回首頁看看其他分類。",
		En:     "That category does not exist — it may have been reorganised. Try the home page.",
	})

	KeyCannotLoadListing = key("error.cannotload.listing", Message{
		ZhHant: "商品列表暫時無法顯示,請稍後再試。",
		En:     "We cannot show the product list right now. Please try again shortly.",
	})

	// KeyFromPrice marks the cheapest of several variant prices. The figure is
	// INSIDE the message because 起 is a suffix and "From" is a prefix, so a
	// bare word beside the price cannot serve both.
	KeyFromPrice = key("price.from", Message{ZhHant: "%s 起", En: "From %s"})
)
