package i18n

var (
	KeySkipToContent = key("nav.skip", Message{ZhHant: "跳至主要內容", En: "Skip to main content"})

	KeySearch = key("nav.search", Message{ZhHant: "搜尋", En: "Search"})

	KeySearchHint = key("nav.search.hint", Message{
		ZhHant: "搜尋商品、品牌或規格",
		En:     "Search products, brands or specifications",
	})

	KeyMenu = key("nav.menu", Message{ZhHant: "選單", En: "Menu"})

	KeyAccount = key("nav.account", Message{ZhHant: "會員中心", En: "Account"})

	KeyWishlist = key("nav.wishlist", Message{ZhHant: "願望清單", En: "Wishlist"})

	KeyCart = key("nav.cart", Message{ZhHant: "購物車", En: "Cart"})

	KeyCartEmpty = key("nav.cart.empty", Message{
		ZhHant: "購物車,目前是空的",
		En:     "Cart, currently empty",
	})

	KeyCartCount = key("nav.cart.count", Message{ZhHant: "購物車,%s 件商品", En: "Cart, %s items"})

	KeyDeals = key("nav.deals", Message{ZhHant: "限時優惠", En: "Deals"})

	KeyLanguage = key("nav.language", Message{ZhHant: "語言", En: "Language"})

	KeyBackToShop = key("nav.back", Message{ZhHant: "回到商店", En: "Back to the shop"})

	// The mirror of KeyBackToShop: that one leaves the back office, this one
	// enters it. Two keys rather than one reused label, because the storefront's
	// entrance and the admin chrome's own eyebrow are different sentences that
	// happen to share a word today.
	KeyBackOffice = key("nav.admin", Message{ZhHant: "後台管理", En: "Back office"})

	KeyBackOfficeHint = key("nav.admin.hint", Message{
		ZhHant: "訂單、庫存與商品都在這裡管理",
		En:     "Orders, stock and the catalogue are managed here",
	})

	KeyFooterHelp = key("footer.help", Message{ZhHant: "顧客服務", En: "Customer service"})

	KeyFooterPolicies = key("footer.policies", Message{ZhHant: "政策", En: "Policies"})

	KeyFooterAbout = key("footer.about", Message{ZhHant: "關於 goen", En: "About goen"})

	KeyFAQ = key("footer.faq", Message{ZhHant: "常見問題", En: "FAQ"})

	KeyContact = key("footer.contact", Message{ZhHant: "聯絡我們", En: "Contact us"})

	KeyShippingPolicy = key("footer.shipping", Message{ZhHant: "配送說明", En: "Delivery"})

	KeyReturnsPolicy = key("footer.returns", Message{ZhHant: "退換貨政策", En: "Returns"})

	KeyPaymentPolicy = key("footer.payment", Message{ZhHant: "付款說明", En: "Payment"})

	KeyWarrantyPolicy = key("footer.warranty", Message{ZhHant: "保固說明", En: "Warranty"})

	KeyPrivacyPolicy = key("footer.privacy", Message{ZhHant: "隱私權政策", En: "Privacy"})

	KeyTermsPolicy = key("footer.terms", Message{ZhHant: "服務條款", En: "Terms"})

	KeyContentNotice = key("notice.content", Message{
		ZhHant: "商品說明與政策條文以繁體中文撰寫。",
		En:     "Product descriptions and policy documents are written in Traditional Chinese.",
	})
)

var (
	KeyAdminEyebrow = key("admin.eyebrow", Message{ZhHant: "後台", En: "Back office"})

	KeyAdminQueueNavLabel = key("admin.queue.nav", Message{
		ZhHant: "後台導覽",
		En:     "Back-office navigation",
	})

	KeyAdminQueueOverview = key("admin.queue.overview", Message{ZhHant: "總覽", En: "Overview"})

	KeyAdminQueueOrders = key("admin.queue.orders", Message{ZhHant: "訂單", En: "Orders"})

	KeyAdminQueueStock = key("admin.queue.stock", Message{ZhHant: "庫存", En: "Stock"})

	KeyAdminQueueReturns = key("admin.queue.returns", Message{ZhHant: "退貨", En: "Returns"})

	KeyAdminQueueWarranty = key("admin.queue.warranty", Message{ZhHant: "保固", En: "Warranty"})

	KeyAdminQueueQuestions = key("admin.queue.questions", Message{ZhHant: "提問", En: "Questions"})

	KeyAdminQueueReviews = key("admin.queue.reviews", Message{ZhHant: "評價", En: "Reviews"})

	KeyAdminQueueTaxonomy = key("admin.queue.taxonomy", Message{
		ZhHant: "品牌分類",
		En:     "Brands and categories",
	})

	KeyAdminQueueHomePage = key("admin.queue.homepage", Message{ZhHant: "首頁", En: "Home page"})

	KeyAdminQueueCampaigns = key("admin.queue.campaigns", Message{ZhHant: "活動", En: "Campaigns"})

	KeyAdminQueueCredit = key("admin.queue.credit", Message{ZhHant: "額度", En: "Credit"})

	KeyAdminQueueShipping = key("admin.queue.shipping", Message{ZhHant: "運費", En: "Delivery fees"})

	KeyAdminQueueStaff = key("admin.queue.staff", Message{ZhHant: "人員", En: "Staff"})
)

// The back office is 23 screens, and flat they are 23 labels a staff member
// reads to find one. These five name the QUESTION a group of screens answers,
// which is what a reader scans by — never the table each one happens to write.
var (
	// Orders, returns, questions, contact messages, reviews: somebody outside
	// the shop is waiting for an answer.
	KeyAdminNavQueues = key("admin.nav.group.queues", Message{ZhHant: "待辦", En: "Queues"})

	// Products, stock, brands and categories, campaigns, coupons: what the shop
	// sells and what it costs.
	KeyAdminNavCatalogue = key("admin.nav.group.catalogue", Message{
		ZhHant: "商品管理",
		En:     "Catalogue",
	})

	// Customers, credit, membership tiers, warranty, newsletter: one person and
	// what the shop owes them. Deliberately not "Customers": that is a SCREEN
	// inside this group, and a group sharing a name with one of its members
	// tells a reader nothing about which of the two they are looking at.
	KeyAdminNavCustomers = key("admin.nav.group.customers", Message{
		ZhHant: "顧客關係",
		En:     "Customer care",
	})

	// Delivery fees, home page, FAQ, staff: what the shop itself is configured
	// to be, rather than anything a customer did.
	KeyAdminNavShop = key("admin.nav.group.shop", Message{
		ZhHant: "店務設定",
		En:     "Shop settings",
	})

	// Reports, activity log, background work: what already happened, and
	// whether it worked.
	KeyAdminNavRecords = key("admin.nav.group.records", Message{
		ZhHant: "紀錄與狀態",
		En:     "Records and status",
	})
)
