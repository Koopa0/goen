package i18n

var (
	KeySiteTitle = key("site.title", Message{
		ZhHant: "goen",
		En:     "goen",
	})

	KeySupportHours = key("site.hours", Message{
		ZhHant: "客服時間 週一至週五 09:00–18:00",
		En:     "Support Monday to Friday, 09:00–18:00",
	})

	// KeyCompanyRegistration is the registration line under a company's name, in
	// the footers and on the about page: the tax number belongs to the sentence.
	KeyCompanyRegistration = key("site.company.registration", Message{
		ZhHant: "統編 90123456",
		En:     "Tax ID 90123456",
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
