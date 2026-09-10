package i18n

var (
	KeyTagline = key("site.tagline", Message{
		ZhHant: "讓買家與對的好商品相遇",
		En:     "Bringing buyers and the right things together",
	})

	KeySiteTitle = key("site.title", Message{
		ZhHant: "goen · 讓買家與對的好商品相遇",
		En:     "goen · bringing buyers and the right things together",
	})

	KeyFooterTagline = key("site.footer.tagline", Message{
		ZhHant: "讓買家與對的好商品相遇。規格看得懂、保固靠得住的 3C 選品店。",
		En: "Bringing buyers and the right things together — a 3C shop where the " +
			"specifications make sense and the warranty holds.",
	})

	KeySupportHours = key("site.hours", Message{
		ZhHant: "客服時間 週一至週五 09:00–18:00",
		En:     "Support Monday to Friday, 09:00–18:00",
	})

	KeyCategoryNav = key("nav.categories", Message{ZhHant: "商品分類", En: "Categories"})

	KeyCloseBanner = key("site.banner.close", Message{ZhHant: "關閉公告", En: "Dismiss"})

	KeyPageNotFound = key("site.notfound", Message{ZhHant: "找不到這個頁面", En: "Page not found"})

	KeyPageNotFoundTitle = key("site.notfound.title", Message{
		ZhHant: "找不到頁面",
		En:     "Not found",
	})

	KeyPageNotFoundBody = key("site.notfound.body", Message{
		ZhHant: "這個網址目前沒有對應的內容。看看店裡其他的東西吧。",
		En:     "Nothing lives at this address. Have a look at what else there is.",
	})
)
