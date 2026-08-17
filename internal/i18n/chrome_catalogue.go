package i18n

var (
	KeyHomeTitle = key("home.title", Message{
		ZhHant: "goen — 3C 選物",
		En:     "goen — curated 3C",
	})
	KeyHomeDescription = key("home.description", Message{
		ZhHant: "goen 精選手機、筆電、平板、耳機與周邊配件。台灣出貨,原廠保固。",
		En: "Phones, laptops, tablets, headphones and accessories, chosen rather than " +
			"listed. Ships from Taiwan with the manufacturer's warranty.",
	})
	KeyListingDescription = key("listing.description", Message{
		ZhHant: "%s — goen 精選 3C。台灣出貨,原廠保固。",
		En:     "%s — curated 3C from goen. Ships from Taiwan with the manufacturer's warranty.",
	})
	KeySearchTitle = key("search.title", Message{ZhHant: "搜尋", En: "Search"})
	KeySearchFor   = key("search.for", Message{ZhHant: "搜尋:%s", En: "Search: %s"})

	KeyBreadcrumb = key("nav.breadcrumb", Message{ZhHant: "麵包屑", En: "Breadcrumb"})
	KeyHome       = key("nav.home", Message{ZhHant: "首頁", En: "Home"})

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
	KeyFilters       = key("listing.filters", Message{ZhHant: "篩選", En: "Filters"})
	KeyClearFilters  = key("listing.filters.clear", Message{ZhHant: "清除全部", En: "Clear all"})
	KeyApplyFilters  = key("listing.filters.apply", Message{ZhHant: "套用篩選", En: "Apply filters"})
	KeyFacetBrand    = key("listing.facet.brand", Message{ZhHant: "品牌", En: "Brand"})
	KeyFacetStock    = key("listing.facet.stock", Message{ZhHant: "供應狀況", En: "Availability"})
	KeyFacetInStock  = key("listing.facet.instock", Message{ZhHant: "現貨供應", En: "In stock"})
	KeyFacetPrice    = key("listing.facet.price", Message{ZhHant: "價格", En: "Price"})
	KeyPriceMin      = key("listing.facet.price.min", Message{ZhHant: "最低價", En: "Lowest price"})
	KeyPriceMax      = key("listing.facet.price.max", Message{ZhHant: "最高價", En: "Highest price"})
	KeyPriceMinShort = key("listing.facet.price.min.short", Message{ZhHant: "最低", En: "Min"})
	KeyPriceMaxShort = key("listing.facet.price.max.short", Message{ZhHant: "最高", En: "Max"})

	KeySort          = key("listing.sort", Message{ZhHant: "排序", En: "Sort"})
	KeySortNewest    = key("listing.sort.newest", Message{ZhHant: "最新上架", En: "Newest"})
	KeySortPriceAsc  = key("listing.sort.price.asc", Message{ZhHant: "價格由低到高", En: "Price, low to high"})
	KeySortPriceDesc = key("listing.sort.price.desc", Message{ZhHant: "價格由高到低", En: "Price, high to low"})
	KeySortRating    = key("listing.sort.rating", Message{ZhHant: "評價最高", En: "Best rated"})

	KeyPagination = key("listing.pager", Message{ZhHant: "分頁", En: "Pagination"})
	KeyPrevPage   = key("listing.pager.prev", Message{ZhHant: "上一頁", En: "Previous"})
	KeyNextPage   = key("listing.pager.next", Message{ZhHant: "下一頁", En: "Next"})
	KeyPageOf     = key("listing.pager.at", Message{ZhHant: "第 %s / %s 頁", En: "Page %s of %s"})

	KeySearchHeading    = key("search.heading", Message{ZhHant: "搜尋商品", En: "Search products"})
	KeySearchResults    = key("search.results", Message{ZhHant: "找到 %s 件商品", En: "%s products found"})
	KeyResultsHeading   = key("search.results.heading", Message{ZhHant: "搜尋結果", En: "Results"})
	KeySearchPrompt     = key("search.prompt", Message{ZhHant: "想找什麼?", En: "What are you looking for?"})
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

	KeyOnSale        = key("card.onsale", Message{ZhHant: "特價", En: "On sale"})
	KeyWasPrice      = key("card.wasprice", Message{ZhHant: "原價", En: "Was"})
	KeyRatingSummary = key("card.rating", Message{
		ZhHant: "評分 %s 分,共 %s 則評價",
		En:     "Rated %s out of 5, from %s reviews",
	})

	KeySectionDescription = key("pdp.description", Message{ZhHant: "商品說明", En: "Description"})
	KeySectionSpecs       = key("pdp.specs", Message{ZhHant: "規格", En: "Specifications"})

	KeySectionWarranty = key("pdp.warranty", Message{ZhHant: "保固", En: "Warranty"})
	KeyPDPWarranty     = key("pdp.warranty.months", Message{
		ZhHant: "保固 %s 個月",
		En:     "%s-month warranty",
	})
	KeySectionRelated    = key("pdp.related", Message{ZhHant: "同類商品", En: "Similar products"})
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
	KeyRestockHeading = key("pdp.restock", Message{ZhHant: "到貨通知我", En: "Tell me when it is back"})
	KeyRestockDone    = key("pdp.restock.done", Message{
		ZhHant: "已經記下了,補貨時會寄信給你。",
		En:     "Noted. We will email you when it is back in stock.",
	})
	KeyRestockBadEmail = key("pdp.restock.bademail", Message{
		ZhHant: "請填寫正確的 Email。",
		En:     "Enter a valid email address.",
	})
	KeyRestockSubmit  = key("pdp.restock.submit", Message{ZhHant: "補貨時通知我", En: "Notify me"})
	KeyAddToCompare   = key("pdp.compare.add", Message{ZhHant: "加入比較", En: "Add to compare"})
	KeyViewCompare    = key("pdp.compare.view", Message{ZhHant: "查看比較", En: "View comparison"})
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

	KeySectionReviews = key("pdp.reviews", Message{ZhHant: "顧客評價", En: "Customer reviews"})
	KeyReviewCount    = key("pdp.reviews.count", Message{ZhHant: "%s 則評價", En: "%s reviews"})
	KeyStarsLabel     = key("pdp.reviews.stars", Message{ZhHant: "%s 星", En: "%s stars"})
	KeyVerifiedBuyer  = key("pdp.reviews.verified", Message{ZhHant: "已購買", En: "Verified purchase"})
	// A reviewer who gave no name, and one whose account has been erased. Says
	// nothing about whether they bought: the badge beside it does that, and
	// product_reviews_verified_is_real is what makes the badge true.
	KeyAnonymousReviewer = key("pdp.reviews.anonymous", Message{
		ZhHant: "匿名顧客",
		En:     "Anonymous",
	})
	KeyRatingOutOf = key("pdp.reviews.ratingof", Message{
		ZhHant: "評分 %s 分,滿分 5 分",
		En:     "Rated %s out of 5",
	})
	KeyRatingScore    = key("pdp.reviews.score", Message{ZhHant: "評分 %s 分", En: "Rated %s"})
	KeySignInFirst    = key("pdp.reviews.signin", Message{ZhHant: "登入", En: "Sign in"})
	KeySignInToReview = key("pdp.reviews.signin.rest", Message{
		ZhHant: "後可以留下評價。",
		En:     " to leave a review.",
	})
	KeyAlreadyReviewed = key("pdp.reviews.already", Message{
		ZhHant: "你已經評價過這個商品了,每個商品只能評價一次。",
		En:     "You have already reviewed this product. One review each.",
	})
	KeyWriteReview          = key("pdp.reviews.write", Message{ZhHant: "留下你的評價", En: "Write a review"})
	KeyReviewWillBeVerified = key("pdp.reviews.willverify", Message{
		ZhHant: "你買過這個商品,評價會標示「已購買」。",
		En:     "You bought this, so your review will be marked as a verified purchase.",
	})
	KeyFieldRating      = key("field.rating", Message{ZhHant: "評分", En: "Rating"})
	KeyFieldReviewTitle = key("field.review.title", Message{
		ZhHant: "標題(可留空)",
		En:     "Title (optional)",
	})
	KeyFieldReviewBody = key("field.review.body", Message{ZhHant: "心得", En: "Your review"})
	KeyReviewSubmit    = key("pdp.reviews.submit", Message{ZhHant: "送出評價", En: "Post review"})

	KeyRatingOutOfRange = key("valid.rating", Message{
		ZhHant: "請選擇 1 到 5 顆星。",
		En:     "Choose between 1 and 5 stars.",
	})
	KeyReviewTitleTooLong = key("valid.review.title", Message{
		ZhHant: "標題太長了。",
		En:     "That title is too long.",
	})
	KeyReviewBodyLength = key("valid.review.body", Message{
		ZhHant: "請寫下 5 到 2000 個字的心得。",
		En:     "Write between 5 and 2000 characters.",
	})
	KeyReviewBodyUnprintable = key("valid.review.body.chars", Message{
		ZhHant: "內容含有無法顯示的字元。",
		En:     "That contains characters we cannot display.",
	})

	KeySectionQA      = key("pdp.qa", Message{ZhHant: "問與答", En: "Questions & answers"})
	KeyQuestionPosted = key("pdp.qa.posted", Message{
		ZhHant: "問題已經送出了。我們回覆之後會出現在這裡。",
		En:     "Your question is posted. Our answer will appear here.",
	})
	KeyQuestionRefused = key("pdp.qa.refused", Message{
		ZhHant: "問題沒有送出 —— 請確認內容不是空的,而且在 300 字以內。",
		En:     "That question was not posted — check it is not empty and under 300 characters.",
	})
	KeyStaffAnswer = key("pdp.qa.staff", Message{ZhHant: "官方回覆", En: "From goen"})
	KeyNoAnswerYet = key("pdp.qa.noanswer", Message{
		ZhHant: "還沒有人回答。",
		En:     "No answer yet.",
	})
	KeyNoQuestionsYet = key("pdp.qa.none", Message{
		ZhHant: "還沒有人問過這個商品。",
		En:     "Nobody has asked about this yet.",
	})
	KeyAskLabel       = key("field.question", Message{ZhHant: "想問什麼?", En: "What would you like to know?"})
	KeyAskPlaceholder = key("field.question.placeholder", Message{
		ZhHant: "例如:這個型號支援哪些快充協定?",
		En:     "For example: which fast-charge standards does this model support?",
	})
	KeyAskNeedsSignIn = key("pdp.qa.signin", Message{
		ZhHant: "需要登入才能發問。回答會公開顯示。",
		En:     "Sign in to ask. Answers are shown publicly.",
	})
	KeyAskSubmit     = key("pdp.qa.submit", Message{ZhHant: "送出問題", En: "Ask"})
	KeyErasedAccount = key("pdp.qa.erased", Message{ZhHant: "已刪除的帳號", En: "Deleted account"})

	KeyCompareTitle       = key("compare.title", Message{ZhHant: "比較", En: "Compare"})
	KeyCompareDescription = key("compare.description", Message{
		ZhHant: "把規格擺在一起看,而不是在兩個分頁之間來回。",
		En:     "Put the specifications side by side instead of flipping between two tabs.",
	})
	KeyCompareInStock = key("compare.instock", Message{ZhHant: "有貨", En: "In stock"})
	KeyWarrantyYears  = key("compare.warranty.years", Message{ZhHant: "%d 年", En: "%d years"})
	KeyWarrantyMonths = key("compare.warranty.months", Message{ZhHant: "%d 個月", En: "%d months"})

	KeyCampaignEyebrow = key("campaign.eyebrow", Message{ZhHant: "限時活動", En: "Limited-time offer"})
	KeyCampaignEndsAt  = key("campaign.endsat", Message{ZhHant: "活動至 %s", En: "Until %s"})
	KeyCampaignEmpty   = key("campaign.empty", Message{
		ZhHant: "這個活動目前沒有可購買的商品",
		En:     "Nothing in this promotion is available right now",
	})
	KeyCampaignEmptyHint = key("campaign.empty.hint", Message{
		ZhHant: "商品可能剛好都下架了。看看",
		En:     "Everything in it may have just sold out. Have a look at",
	})
	KeyCampaignEmptyLink = key("campaign.empty.link", Message{
		ZhHant: "其他優惠",
		En:     "the other offers",
	})
	KeyCampaignDescription = key("campaign.description", Message{
		ZhHant: "%s — goen 限時優惠",
		En:     "%s — a limited-time offer from goen",
	})
	KeyCampaignProducts = key("campaign.products", Message{ZhHant: "%s 件商品", En: "%s products"})
	KeyDealsEmpty       = key("deals.empty", Message{
		ZhHant: "目前沒有正在特價的商品。歡迎逛逛全部分類。",
		En:     "Nothing is on sale at the moment. Have a look through the categories.",
	})

	KeyEndsWithinHour = key("campaign.ends.soon", Message{
		ZhHant: "不到 1 小時",
		En:     "under an hour left",
	})
	KeyEndsInHours = key("campaign.ends.hours", Message{ZhHant: "剩 %d 小時", En: "%d hours left"})
	KeyEndsInDays  = key("campaign.ends.days", Message{ZhHant: "剩 %d 天", En: "%d days left"})

	KeyProductNotFound     = key("pdp.notfound", Message{ZhHant: "找不到這個商品", En: "Product not found"})
	KeyProductNotFoundBody = key("pdp.notfound.body", Message{
		ZhHant: "這個商品目前沒有販售,可能已經下架。回首頁看看其他選擇。",
		En: "This product is not on sale — it may have been discontinued. " +
			"Have a look at what else there is.",
	})
	KeyCategoryNotFound     = key("listing.notfound", Message{ZhHant: "找不到這個分類", En: "Category not found"})
	KeyCategoryNotFoundBody = key("listing.notfound.body", Message{
		ZhHant: "這個分類目前不存在,可能已經調整過。回首頁看看其他分類。",
		En:     "That category does not exist — it may have been reorganised. Try the home page.",
	})
	KeyCampaignNotFound     = key("campaign.notfound", Message{ZhHant: "找不到這個活動", En: "Offer not found"})
	KeyCampaignNotFoundBody = key("campaign.notfound.body", Message{
		ZhHant: "這個活動可能已經結束了。看看目前的優惠。",
		En:     "That promotion has probably ended. Have a look at what is running now.",
	})
	KeyCannotLoad        = key("error.cannotload", Message{ZhHant: "暫時無法載入", En: "Cannot load this right now"})
	KeyCannotLoadProduct = key("error.cannotload.product", Message{
		ZhHant: "商品資訊暫時無法顯示,請稍後再試。",
		En:     "We cannot show this product right now. Please try again shortly.",
	})
	KeyCannotLoadListing = key("error.cannotload.listing", Message{
		ZhHant: "商品列表暫時無法顯示,請稍後再試。",
		En:     "We cannot show the product list right now. Please try again shortly.",
	})
	KeyCannotLoadHome = key("error.cannotload.home", Message{
		ZhHant: "商店首頁暫時無法顯示,請稍後再試。",
		En:     "We cannot show the home page right now. Please try again shortly.",
	})

	KeyHeroPlaceholder = key("home.hero.placeholder", Message{
		ZhHant: "主視覺待實拍素材",
		En:     "Hero photography pending",
	})
	KeySectionCategories  = key("home.categories", Message{ZhHant: "分類選購", En: "Shop by category"})
	KeySectionRecommended = key("home.recommended", Message{
		ZhHant: "綜合推薦",
		En:     "Recommended",
	})
	KeySectionTrust      = key("home.trust", Message{ZhHant: "購物保障", En: "Shopping with goen"})
	KeyTrustWarrantyBody = key("home.trust.warranty", Message{
		ZhHant: "全機種原廠保固,維修免費到府收送,保固可以線上登錄查詢。",
		En: "Every model carries its manufacturer's warranty. We collect repairs from your " +
			"door for free, and you can register and check your cover online.",
	})
	KeyTrustShippingBody = key("home.trust.shipping", Message{
		ZhHant: "宅配與超商取貨皆適用;未達門檻運費 NT$60 起。",
		En: "Home delivery and store pickup alike. Below the threshold, delivery is from " +
			"NT$60.",
	})
	KeyTrustReturnsBody = key("home.trust.returns", Message{
		ZhHant: "線上申請、宅配回收,退款 3–5 個工作天入帳。",
		En: "Request it online, we collect it, and the refund lands in 3 to 5 working " +
			"days.",
	})
	KeyTrustPayment     = key("home.trust.payment", Message{ZhHant: "付款安全", En: "Secure payment"})
	KeyTrustPaymentBody = key("home.trust.payment.body", Message{
		ZhHant: "Stripe 加密金流,卡號不經過 goen 伺服器。",
		En:     "Stripe handles the card. Your number never touches a goen server.",
	})

	KeyDeals2     = key("deals.eyebrow", Message{ZhHant: "優惠", En: "Offers"})
	KeyDealsTitle = key("deals.title", Message{ZhHant: "現正優惠", En: "On sale now"})
	KeyDealsCount = key("deals.count", Message{ZhHant: "%s 件商品正在特價", En: "%s products reduced"})

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
	KeyCompareCaption = key("compare.caption", Message{
		ZhHant: "商品規格比較",
		En:     "Product specification comparison",
	})
	KeyCompareRowLabel    = key("compare.row.label", Message{ZhHant: "項目", En: "Attribute"})
	KeyCompareRowPrice    = key("compare.row.price", Message{ZhHant: "價格", En: "Price"})
	KeyCompareRowStock    = key("compare.row.stock", Message{ZhHant: "供貨", En: "Availability"})
	KeyCompareRowRating   = key("compare.row.rating", Message{ZhHant: "評價", En: "Rating"})
	KeyCompareRowWarranty = key("compare.row.warranty", Message{ZhHant: "保固", En: "Warranty"})
	KeyCompareRowCategory = key("compare.row.category", Message{ZhHant: "分類", En: "Category"})

	KeyHeroEyebrow = key("home.hero.eyebrow", Message{
		ZhHant: "台灣出貨 · 原廠保固",
		En:     "Ships from Taiwan · manufacturer's warranty",
	})
	KeyHeroHeadline = key("home.hero.headline", Message{
		ZhHant: "挑一台好的,值得。",
		En:     "A good one is worth choosing.",
	})
	KeyHeroBody = key("home.hero.body", Message{
		ZhHant: "goen 精選手機、筆電、平板與耳機周邊——把難挑的東西,挑好給你。",
		En: "Phones, laptops, tablets and audio, chosen rather than listed — the hard " +
			"decisions made for you.",
	})
	KeyHeroPrimaryCTA = key("home.hero.cta.primary", Message{
		ZhHant: "看本週優惠",
		En:     "This week's offers",
	})
	KeyHeroSecondaryCTA = key("home.hero.cta.secondary", Message{
		ZhHant: "關於 goen",
		En:     "About goen",
	})
)
